# Modules

A goforge **module** is a self-contained, opt-in feature pack. Modules
bundle the things they need to function: routes, migrations, workers,
event subscriptions, health checks. The host application registers
modules at the composition root and pays for what it uses - nothing
more.

## Why modules?

The framework's bootstrap is small on purpose. As soon as you start
adding capabilities (audit log, billing integration, multi-tenancy
billing, S3 uploads, webhooks…), the composition root balloons unless
each capability is its own thing with a stable contract. Modules
give you that contract:

```go
type Module interface {
    Name() string
    Init(ctx context.Context, app *Context) error
    Routes(r fiber.Router)
    Migrations() (fs.FS, string)
    Workers() []Worker
    Subscriptions(bus EventBus)
    Health(ctx context.Context) error
    Shutdown(ctx context.Context) error
}
```

`pkg/module.BaseModule` provides no-op defaults for every method
except `Name()`, so your module struct can implement only what it
actually needs.

## Lifecycle

1. **Construct.** `myMod := mymodule.New(deps)` - injection happens at
   the composition root. The constructor must not block.
2. **Register.** `app.Modules.Register(myMod)`. Order matters: later
   modules can read services published by earlier ones.
3. **Init.** The framework calls `Init(ctx, *Context)`. Use this to
   open connections, install OpenAPI operations, or register event
   subscribers.
4. **Routes.** The framework gives the module a `fiber.Router` mounted
   at `/api/v1` (or whatever the application chose). The module mounts
   its endpoints there.
5. **Workers.** Long-running goroutines (cron, dispatchers,
   reconciliation loops). Each `Worker` is `func(context.Context) error`;
   the framework supervises them and cancels via context on shutdown.
6. **Subscriptions.** Domain events are delivered to `Subscribe` /
   `SubscribeEvent` on the shared bus. Use this to react across
   module boundaries without direct imports.
7. **Health.** `/admin/healthz` invokes `Health(ctx)` on every
   registered module. Return non-nil to mark the module degraded.
8. **Shutdown.** Graceful drain when SIGTERM arrives. Workers should
   already exit on `ctx.Done()`; `Shutdown` is for resources that
   need explicit Close.

## Anatomy of a module

```go
package billing

import (
    "context"
    "io/fs"

    "github.com/gofiber/fiber/v2"
    "github.com/dedeez14/goforge/pkg/module"
)

//go:embed migrations/*.sql
var migFS embed.FS

type Module struct {
    module.BaseModule
    deps Deps
    repo *Repo
}

type Deps struct {
    Pool   *pgxpool.Pool
    Bus    *events.Bus
    Logger zerolog.Logger
}

func New(d Deps) *Module {
    return &Module{deps: d, repo: NewRepo(d.Pool)}
}

func (m *Module) Name() string { return "billing" }

func (m *Module) Init(ctx context.Context, app *module.Context) error {
    // pull shared services from app.Values, register OpenAPI ops, etc.
    return nil
}

func (m *Module) Routes(r fiber.Router) {
    g := r.Group("/billing")
    g.Get("/invoices", m.handleListInvoices)
}

func (m *Module) Migrations() (fs.FS, string) {
    sub, _ := fs.Sub(migFS, "migrations")
    return sub, "billing"
}

func (m *Module) Subscriptions(bus module.EventBus) {
    bus.Subscribe("order.placed", m.onOrderPlaced)
}
```

## Registry & order of operations

The host application owns the registry:

```go
mods := app.Modules
mods.Register(billing.New(billing.Deps{...}))
mods.Register(audit.New(audit.Deps{...}))

for _, m := range mods.Each() {
    if err := m.Init(ctx, modCtx); err != nil { return err }
    m.Routes(router)
    m.Subscriptions(bus)
}
```

Initialisation runs in registration order. Route mounting happens once
per module on the same `fiber.Router`, so module routes are namespaced
inside whichever group the host gave them.

## Distribution

Modules are plain Go packages. Publish them under
`github.com/<you>/<repo>/...`, follow semver, and document the public
API. The framework does not require any registry - users simply
import and `Register`.

## Built-in modules (planned)

| Name | Purpose |
| --- | --- |
| `audit`     | Append-only audit log of state-changing requests |
| `webhooks`  | Outbound webhook subscriptions and retries |
| `s3uploads` | Pre-signed POST uploads with virus scan hook |
| `otel`      | OpenTelemetry tracer + span propagation |
| `apikey`    | Long-lived API key auth alongside JWT |

PRs welcome on any of the above.
