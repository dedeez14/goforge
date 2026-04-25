# Adding a new resource

The blueprint ships with one fully-implemented resource (`User` + Auth) so you have a worked example. To add a new one — say `Order` — run:

```bash
make scaffold name=Order
```

This invokes `scripts/scaffold.sh`, which generates the full vertical slice. The rest of this document explains what was generated, how to fill it in, and the conventions that keep new resources consistent with the rest of the codebase.

## What gets generated

```
internal/domain/order/
  order.go                # entity + constructor + invariants
  repository.go           # repository interface
internal/usecase/
  order.go                # business logic stubs
internal/adapter/repository/postgres/
  order.go                # pgx implementation stubs
internal/adapter/http/dto/
  order.go                # request/response DTOs + mappers
internal/adapter/http/handler/
  order.go                # fiber handlers
migrations/
  NNNN_create_orders.up.sql
  NNNN_create_orders.down.sql
```

## Step 1 — model the domain

Edit `internal/domain/order/order.go`. Define the entity and its invariants:

```go
type Order struct {
    ID        uuid.UUID
    UserID    uuid.UUID
    Total     int64       // cents — never use float for money
    Status    Status
    CreatedAt time.Time
    UpdatedAt time.Time
}

func New(userID uuid.UUID, total int64) (*Order, error) {
    if total <= 0 {
        return nil, errs.Validation("order.total_invalid", "total must be positive", nil)
    }
    return &Order{
        ID:        uuid.New(),
        UserID:    userID,
        Total:     total,
        Status:    StatusPending,
        CreatedAt: time.Now().UTC(),
        UpdatedAt: time.Now().UTC(),
    }, nil
}
```

Rules of thumb:

- Money is `int64` cents. Never `float64`.
- All times are UTC.
- Constructors enforce invariants. Once an entity exists, it is valid by construction.
- Domain errors come from `pkg/errs`. Don't return raw `fmt.Errorf` strings.

## Step 2 — declare the repository interface

In `internal/domain/order/repository.go`:

```go
type Repository interface {
    Create(ctx context.Context, o *Order) error
    FindByID(ctx context.Context, id uuid.UUID) (*Order, error)
    ListByUser(ctx context.Context, userID uuid.UUID, p paginate.Page) ([]*Order, int, error)
    UpdateStatus(ctx context.Context, id uuid.UUID, s Status) error
}
```

Express *what the use-case needs*, not *what SQL can do*. If a query is awkward to express here, that's a sign the use-case might be doing too much.

## Step 3 — implement the use-case

In `internal/usecase/order.go`:

```go
type OrderUseCase struct {
    orders order.Repository
    users  user.Repository
}

func (u *OrderUseCase) Place(ctx context.Context, userID uuid.UUID, total int64) (*order.Order, error) {
    if _, err := u.users.FindByID(ctx, userID); err != nil {
        return nil, err
    }
    o, err := order.New(userID, total)
    if err != nil {
        return nil, err
    }
    return o, u.orders.Create(ctx, o)
}
```

Use-cases:

- Take primitive arguments + `context.Context`.
- Return domain values + `error`.
- Never see `*fiber.Ctx`, JSON, headers, or HTTP statuses.
- Compose multiple repositories when needed; wrap in a transaction if mutation spans aggregates (see *Transactions* below).

## Step 4 — implement the Postgres adapter

In `internal/adapter/repository/postgres/order.go`:

```go
func (r *OrderRepo) Create(ctx context.Context, o *order.Order) error {
    _, err := r.pool.Exec(ctx, `
        INSERT INTO orders (id, user_id, total_cents, status, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6)
    `, o.ID, o.UserID, o.Total, o.Status, o.CreatedAt, o.UpdatedAt)
    return translatePgError(err)
}
```

`translatePgError` is the conversion seam — `23505` (unique violation) → `errs.Conflict(...)`, `pgx.ErrNoRows` → domain "not found". Add new translations there, never sprinkle them through use-cases.

## Step 5 — DTOs and handlers

DTOs live in `internal/adapter/http/dto/order.go`:

```go
type PlaceOrderRequest struct {
    Total int64 `json:"total" validate:"required,gt=0"`
}

type OrderResponse struct {
    ID     uuid.UUID `json:"id"`
    Total  int64     `json:"total"`
    Status string    `json:"status"`
}

func ToOrderResponse(o *order.Order) OrderResponse {
    return OrderResponse{ID: o.ID, Total: o.Total, Status: string(o.Status)}
}
```

Handlers stay thin (`internal/adapter/http/handler/order.go`):

```go
func (h *OrderHandler) Place(c *fiber.Ctx) error {
    var req dto.PlaceOrderRequest
    if err := c.BodyParser(&req); err != nil {
        return errs.BadRequest("body.invalid", "invalid JSON", err)
    }
    if err := h.validator.Struct(req); err != nil {
        return err
    }
    userID := middleware.UserIDFrom(c)
    o, err := h.uc.Place(c.UserContext(), userID, req.Total)
    if err != nil {
        return err
    }
    return httpx.RespondData(c, fiber.StatusCreated, dto.ToOrderResponse(o))
}
```

## Step 6 — wire it up

In `internal/app/app.go`:

```go
orderRepo := postgres.NewOrderRepo(pool)
orderUC := usecase.NewOrderUseCase(orderRepo, userRepo)
orderHandler := handler.NewOrderHandler(orderUC, validator)
```

In `internal/infrastructure/server/router.go`:

```go
orders := api.Group("/orders", authMW)
orders.Post("/", orderHandler.Place)
orders.Get("/:id", orderHandler.Get)
```

## Step 7 — run migrations

```bash
make migrate-up
```

That's it. The new resource follows the same conventions as `User`/Auth and inherits the framework's middleware, error mapper, request ID propagation, and observability for free.

---

## Cross-cutting conventions

### Transactions

Use a transaction whenever a mutation crosses aggregates (e.g. create an order *and* decrement inventory). Pattern:

```go
func (u *OrderUseCase) Place(ctx context.Context, …) error {
    return u.tx.Run(ctx, func(ctx context.Context) error {
        // every repo call inside picks up the transaction from ctx
        if err := u.inventory.Decrement(ctx, …); err != nil { return err }
        return u.orders.Create(ctx, …)
    })
}
```

Wire a `tx.Manager` once in `internal/app/app.go`. Each repository's pgx executor reads the transaction from the context, falling back to the pool if there is none.

### Pagination

Every list endpoint accepts `?page=` and `?per_page=`. Use the `paginate.Page` helper to clamp values:

```go
p := paginate.FromQuery(c, paginate.Defaults{Page: 1, PerPage: 20, MaxPerPage: 100})
items, total, err := repo.List(ctx, p)
return httpx.RespondPaginated(c, items, total, p)
```

The response envelope embeds `meta.pagination` automatically.

### Authorisation

Authentication (who you are) is handled by the `auth` middleware. Authorisation (what you may do) belongs in the use-case:

```go
if order.UserID != actorID && actorRole != "admin" {
    return errs.Forbidden("order.forbidden", "not your order", nil)
}
```

Keep authz in the use-case so it's covered by unit tests with no HTTP involved.

### Testing the new resource

Add a `*_test.go` file next to the use-case using the in-memory repository pattern shown for `User`. For the Postgres adapter, prefer `testcontainers-go` so tests run against a real Postgres in CI.

### Observability

Every handler emits a structured log line for free via `requestid` + Fiber's logger; you don't need to log "received order request" yourself. If you want to add a domain event log, prefer `zerolog`'s context logger pulled from `c.UserContext()` so the `requestId` is preserved.
