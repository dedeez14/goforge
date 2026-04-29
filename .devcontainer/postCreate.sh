#!/usr/bin/env bash
# postCreate.sh is invoked by the devcontainer CLI after the
# workspace container has been built and the repo has been cloned
# into it. Keep it idempotent: it runs again on every container
# rebuild.
set -euo pipefail

log() { printf '\033[36m[devcontainer]\033[0m %s\n' "$*"; }

log "configuring git safe.directory"
git config --global --add safe.directory "$PWD" || true

log "fetching go module deps"
go mod download

log "installing dev tools"
# Pin versions to match go.mod / CI so the devcontainer behaves
# the same as GitHub Actions. Anything added here should also
# be referenced in the CI workflow.
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0
go install golang.org/x/tools/cmd/goimports@latest
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1

# Place the binaries on PATH for interactive shells the next time
# the user opens a terminal. `go install` drops them into
# $GOPATH/bin which already gets appended to $PATH by the base
# image, so this is a no-op on a clean install but paves over
# customised $GOPATH setups.
cat <<'EOS' >>~/.bashrc

# goforge devcontainer: ensure go tooling is on PATH
export PATH="${GOPATH:-$HOME/go}/bin:$PATH"
EOS

log "waiting for postgres"
for _ in {1..30}; do
    if pg_isready -h postgres -U goforge -d goforge >/dev/null 2>&1; then
        log "postgres is ready"
        break
    fi
    sleep 1
done

log "applying database migrations"
migrate \
    -path ./migrations \
    -database "postgres://goforge:goforge@postgres:5432/goforge?sslmode=disable" \
    up || log "migrations failed (continue; run 'make migrate-up' manually once the DB is up)"

cat <<'EOS'

goforge devcontainer is ready.

  make run          # start the api on :8080
  make test         # go test -race ./...
  make lint         # golangci-lint
  make migrate-up   # re-apply migrations if needed

Postgres is reachable at postgres:5432 from inside the container,
and forwarded to localhost:5432 for tools running on the host.
EOS
