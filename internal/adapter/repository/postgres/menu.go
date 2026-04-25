package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dedeez14/goforge/internal/domain/menu"
	"github.com/dedeez14/goforge/pkg/errs"
)

const (
	qMenuInsert = `
INSERT INTO menus (id, tenant_id, parent_id, code, label, icon, path, sort_order,
                   required_permission_code, is_visible, metadata,
                   created_at, updated_at, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW(), $12, $12)
`
	qMenuUpdate = `
UPDATE menus
SET parent_id                = $2,
    label                    = $3,
    icon                     = $4,
    path                     = $5,
    sort_order               = $6,
    required_permission_code = $7,
    is_visible               = $8,
    metadata                 = $9,
    updated_at               = NOW(),
    updated_by               = $10
WHERE id = $1 AND deleted_at IS NULL
`
	qMenuSoftDelete = `
UPDATE menus
SET deleted_at = NOW(), updated_at = NOW(), updated_by = $2
WHERE id = $1 AND deleted_at IS NULL
`
	qMenuSelectByID = `
SELECT id, tenant_id, parent_id, code, label, icon, path, sort_order,
       required_permission_code, is_visible, metadata,
       created_at, updated_at
FROM menus WHERE id = $1 AND deleted_at IS NULL
`
	qMenuList = `
SELECT id, tenant_id, parent_id, code, label, icon, path, sort_order,
       required_permission_code, is_visible, metadata,
       created_at, updated_at
FROM menus
WHERE deleted_at IS NULL
  AND COALESCE(tenant_id, '00000000-0000-0000-0000-000000000000'::uuid)
    = COALESCE($1::uuid,  '00000000-0000-0000-0000-000000000000'::uuid)
ORDER BY sort_order, label
`
)

// MenuRepository is the pgx-backed implementation of menu.Repository.
type MenuRepository struct {
	pool *pgxpool.Pool
}

// NewMenuRepository wires a MenuRepository to the pool.
func NewMenuRepository(pool *pgxpool.Pool) *MenuRepository {
	return &MenuRepository{pool: pool}
}

// Create inserts m, mapping unique-violation to ErrTaken.
func (r *MenuRepository) Create(ctx context.Context, m *menu.Menu, actor *uuid.UUID) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	if len(m.Metadata) == 0 {
		m.Metadata = json.RawMessage(`{}`)
	}
	_, err := r.pool.Exec(ctx, qMenuInsert,
		m.ID, m.TenantID, m.ParentID, m.Code, m.Label, m.Icon, m.Path, m.SortOrder,
		m.RequiredPermissionCode, m.IsVisible, m.Metadata, actorOrNil(actor),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return menu.ErrTaken
		}
		return errs.Wrap(errs.KindInternal, "menu.create", "failed to create menu", err)
	}
	return nil
}

// Update mutates the row in place.
func (r *MenuRepository) Update(ctx context.Context, m *menu.Menu, actor *uuid.UUID) error {
	if len(m.Metadata) == 0 {
		m.Metadata = json.RawMessage(`{}`)
	}
	tag, err := r.pool.Exec(ctx, qMenuUpdate,
		m.ID, m.ParentID, m.Label, m.Icon, m.Path, m.SortOrder,
		m.RequiredPermissionCode, m.IsVisible, m.Metadata, actorOrNil(actor),
	)
	if err != nil {
		return errs.Wrap(errs.KindInternal, "menu.update", "failed to update menu", err)
	}
	if tag.RowsAffected() == 0 {
		return menu.ErrNotFound
	}
	return nil
}

// Delete soft-deletes the row. Children are cascaded by the FK.
func (r *MenuRepository) Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, qMenuSoftDelete, id, actorOrNil(actor))
	if err != nil {
		return errs.Wrap(errs.KindInternal, "menu.delete", "failed to delete menu", err)
	}
	if tag.RowsAffected() == 0 {
		return menu.ErrNotFound
	}
	return nil
}

// FindByID returns the menu.
func (r *MenuRepository) FindByID(ctx context.Context, id uuid.UUID) (*menu.Menu, error) {
	return scanMenu(r.pool.QueryRow(ctx, qMenuSelectByID, id))
}

// List returns every menu row for the tenant ordered by sort_order/label.
// Tree assembly happens in the use-case layer so the repository
// stays I/O-focused.
func (r *MenuRepository) List(ctx context.Context, tenantID *uuid.UUID) ([]*menu.Menu, error) {
	rows, err := r.pool.Query(ctx, qMenuList, tenantID)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "menu.list", "failed to list menus", err)
	}
	defer rows.Close()
	var out []*menu.Menu
	for rows.Next() {
		m, err := scanMenu(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(errs.KindInternal, "menu.list_iter", "iterating menus failed", err)
	}
	return out, nil
}

func scanMenu(row rowScanner) (*menu.Menu, error) {
	var (
		m         menu.Menu
		tenantID  *uuid.UUID
		parentID  *uuid.UUID
		permCode  *string
		metaBytes []byte
	)
	if err := row.Scan(
		&m.ID, &tenantID, &parentID, &m.Code, &m.Label, &m.Icon, &m.Path, &m.SortOrder,
		&permCode, &m.IsVisible, &metaBytes, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, menu.ErrNotFound
		}
		return nil, errs.Wrap(errs.KindInternal, "menu.scan", "failed to read menu", err)
	}
	m.TenantID = tenantID
	m.ParentID = parentID
	m.RequiredPermissionCode = permCode
	if len(metaBytes) == 0 {
		m.Metadata = json.RawMessage(`{}`)
	} else {
		m.Metadata = json.RawMessage(metaBytes)
	}
	return &m, nil
}
