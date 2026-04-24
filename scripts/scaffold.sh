#!/usr/bin/env bash
#
# scripts/scaffold.sh — generate the layered files for a new resource.
#
# Usage: ./scripts/scaffold.sh <ResourceName>
#   e.g. ./scripts/scaffold.sh Order  (lowercase = order, plural table = orders)
#
# Produces:
#   internal/domain/<lc>/<lc>.go          — entity + domain errors
#   internal/domain/<lc>/repository.go    — repository interface
#   internal/usecase/<lc>.go              — use-case skeleton
#   internal/adapter/repository/postgres/<lc>.go — pgx implementation stub
#   internal/adapter/http/dto/<lc>.go     — DTOs
#   internal/adapter/http/handler/<lc>.go — handler stub
#   migrations/NNNN_create_<lc>s.(up|down).sql
#
# After running, wire the new types inside internal/app/app.go and
# internal/infrastructure/server/router.go.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <ResourceName>" >&2
  exit 64
fi

NAME="$1"
LC=$(echo "$NAME" | tr '[:upper:]' '[:lower:]')
UC="$(echo "${NAME:0:1}" | tr '[:lower:]' '[:upper:]')${NAME:1}"
PLURAL="${LC}s"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
MOD="$(awk '/^module /{print $2}' "$ROOT/go.mod")"

mkdir -p \
  "$ROOT/internal/domain/$LC" \
  "$ROOT/internal/adapter/repository/postgres" \
  "$ROOT/internal/adapter/http/dto" \
  "$ROOT/internal/adapter/http/handler" \
  "$ROOT/migrations"

next_migration() {
  local last
  last=$(ls "$ROOT/migrations" 2>/dev/null | awk -F_ '/^[0-9]+_/{print $1}' | sort -n | tail -1 || true)
  if [[ -z "$last" ]]; then
    echo "0001"
  else
    printf "%04d" $((10#$last + 1))
  fi
}
SEQ=$(next_migration)

cat > "$ROOT/internal/domain/$LC/$LC.go" <<EOF
// Package $LC is the domain package for the $UC aggregate.
package $LC

import (
	"time"

	"github.com/google/uuid"

	"$MOD/pkg/errs"
)

type $UC struct {
	ID        uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

var (
	ErrNotFound = errs.NotFound("$LC.not_found", "$LC not found")
)
EOF

cat > "$ROOT/internal/domain/$LC/repository.go" <<EOF
package $LC

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	Create(ctx context.Context, v *$UC) error
	FindByID(ctx context.Context, id uuid.UUID) (*$UC, error)
	// TODO: add domain-level methods here.
}
EOF

cat > "$ROOT/internal/usecase/$LC.go" <<EOF
package usecase

import (
	"context"

	"github.com/google/uuid"

	"$MOD/internal/domain/$LC"
)

type ${UC}UseCase struct {
	repo $LC.Repository
}

func New${UC}UseCase(repo $LC.Repository) *${UC}UseCase {
	return &${UC}UseCase{repo: repo}
}

func (uc *${UC}UseCase) Get(ctx context.Context, id uuid.UUID) (*$LC.$UC, error) {
	return uc.repo.FindByID(ctx, id)
}
EOF

cat > "$ROOT/internal/adapter/repository/postgres/$LC.go" <<EOF
package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"$MOD/internal/domain/$LC"
	"$MOD/pkg/errs"
)

const (
	qSelect${UC}ByID = \`SELECT id, created_at, updated_at FROM $PLURAL WHERE id = \$1\`
)

type ${UC}Repository struct{ pool *pgxpool.Pool }

func New${UC}Repository(pool *pgxpool.Pool) *${UC}Repository { return &${UC}Repository{pool: pool} }

func (r *${UC}Repository) Create(ctx context.Context, v *$LC.$UC) error {
	// TODO: implement
	return errs.Internal("$LC.not_implemented", "create not implemented")
}

func (r *${UC}Repository) FindByID(ctx context.Context, id uuid.UUID) (*$LC.$UC, error) {
	var v $LC.$UC
	row := r.pool.QueryRow(ctx, qSelect${UC}ByID, id)
	if err := row.Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, $LC.ErrNotFound
		}
		return nil, errs.Wrap(errs.KindInternal, "$LC.scan", "failed to read $LC", err)
	}
	return &v, nil
}
EOF

cat > "$ROOT/internal/adapter/http/dto/$LC.go" <<EOF
package dto

import "time"

type ${UC}Response struct {
	ID        string    \`json:"id"\`
	CreatedAt time.Time \`json:"created_at"\`
	UpdatedAt time.Time \`json:"updated_at"\`
}
EOF

cat > "$ROOT/internal/adapter/http/handler/$LC.go" <<EOF
package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"$MOD/internal/adapter/http/dto"
	"$MOD/internal/usecase"
	"$MOD/pkg/errs"
	"$MOD/pkg/httpx"
)

type ${UC}Handler struct{ uc *usecase.${UC}UseCase }

func New${UC}Handler(uc *usecase.${UC}UseCase) *${UC}Handler { return &${UC}Handler{uc: uc} }

func (h *${UC}Handler) Get(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return httpx.RespondError(c, errs.InvalidInput("$LC.invalid_id", "invalid id"))
	}
	v, err := h.uc.Get(c.UserContext(), id)
	if err != nil {
		return httpx.RespondError(c, err)
	}
	return httpx.OK(c, dto.${UC}Response{
		ID:        v.ID.String(),
		CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	})
}
EOF

cat > "$ROOT/migrations/${SEQ}_create_${PLURAL}.up.sql" <<EOF
CREATE TABLE IF NOT EXISTS $PLURAL (
    id         UUID        PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
EOF

cat > "$ROOT/migrations/${SEQ}_create_${PLURAL}.down.sql" <<EOF
DROP TABLE IF EXISTS $PLURAL;
EOF

cat <<EOF

✔ Scaffolded $UC. Next steps:
  1. Wire repository + use-case + handler inside internal/app/app.go
  2. Register routes in internal/infrastructure/server/router.go
  3. Flesh out the repository's Create() and any other methods
  4. make migrate-up
EOF
