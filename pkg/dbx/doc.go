// Package dbx — usage example.
//
//	type Order struct {
//	    ID       uuid.UUID
//	    TenantID string
//	    Total    int64
//	    dbx.Audit
//	}
//
//	const ordersDDL = `
//	CREATE TABLE orders (
//	    id          UUID PRIMARY KEY,
//	    tenant_id   TEXT NOT NULL,
//	    total       BIGINT NOT NULL,` + dbx.AuditColumnsDDL + `
//	);
//	CREATE INDEX orders_tenant_alive ON orders (tenant_id) WHERE deleted_at IS NULL;
//	`
//
//	func (r *OrderRepo) Create(ctx context.Context, o *Order) error {
//	    o.Audit.Create(ctx, time.Now())
//	    // ... INSERT ...
//	}
//
//	func (r *OrderRepo) Update(ctx context.Context, o *Order) error {
//	    o.Audit.Touch(ctx, time.Now())
//	    // ... UPDATE ...
//	}
//
//	func (r *OrderRepo) ListAlive(ctx context.Context, tenant string) ([]Order, error) {
//	    return r.query(ctx,
//	        "SELECT id, tenant_id, total FROM orders WHERE tenant_id = $1"+dbx.SoftDeleteFilter,
//	        tenant)
//	}
package dbx
