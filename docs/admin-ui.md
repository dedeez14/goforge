# Admin UI

goforge ships a bundled single-page back-office at `/panel/` (configurable).
It is served by the `pkg/adminui` package from an embedded filesystem, so
a single binary is still the only deployable artefact — no Node install,
no bundler, no separate `dist/` to version.

## What it gives you out of the box

Once you log in with an account that has `rbac.manage` (and/or
`menu.manage`), the UI exposes:

| Tab            | Backing endpoint                                 | Guard            |
| -------------- | ------------------------------------------------ | ---------------- |
| Dashboard      | `GET /healthz`, `GET /readyz`, `GET /api/v1/me/access` | auth only        |
| Users          | `GET /api/v1/users`                              | `rbac.manage`    |
| Roles          | `GET/POST/PATCH/DELETE /api/v1/roles`, `PUT /api/v1/roles/:id/permissions` | `rbac.manage` |
| Permissions    | `GET/POST/PATCH/DELETE /api/v1/permissions`      | `rbac.manage`    |
| Menus          | `GET/POST/PATCH/DELETE /api/v1/menus`, tree view | `menu.manage`    |
| My sessions    | `GET/DELETE /api/v1/me/sessions`                 | JWT only         |
| My API keys    | `GET/POST/DELETE /api/v1/api-keys`               | JWT only         |
| OpenAPI        | `GET /openapi.json`                              | open             |

The UI enforces nothing itself: every authorization check is still on
the server. A user who opens devtools and crafts their own fetch call
sees the same 403 they would from the UI. The UI just hides controls
the server is guaranteed to reject so the operator does not waste a
click.

## Configuration

`pkg/adminui` is mounted by `internal/platform` when
`platform.admin_ui_enabled` is true (default). Tune via env:

```sh
GOFORGE_PLATFORM_ADMIN_UI_ENABLED=true    # default
GOFORGE_PLATFORM_ADMIN_UI_PATH=/panel     # default; customise e.g. "/admin-ui"
```

Opt-out (for deployments that host their own UI) by setting
`GOFORGE_PLATFORM_ADMIN_UI_ENABLED=false`. The API still works; the
`/panel/*` routes are simply not registered.

## How it works

- `pkg/adminui` owns a `go:embed` directive over the `assets/` folder
  (index.html, app.js, styles.css, favicon.svg). All files are shipped
  inside the Go binary.
- `adminui.Mount(app, cfg)` registers two routes on the Fiber app:
  - `GET /panel` → 308 redirect to `/panel/` (clean trailing-slash).
  - `Use /panel/` → static filesystem middleware with `index.html` as
    the SPA fallback so hash routes work on first load.
- The SPA is a single vanilla-JS ES module. It stores the access + refresh
  tokens in `localStorage` under `goforge.admin.*` keys, and attaches
  `Authorization: Bearer <token>` to every `/api/v1/*` call.

## Custom branding

The admin UI is deliberately minimalist so teams can fork it. Either:

1. Fork `pkg/adminui/assets/` in your own module and mount your copy
   from `internal/app/Run` (skip `pkg/adminui` entirely), or
2. Serve a completely custom UI from any path and disable this one
   (`admin_ui_enabled=false`).

## Non-goals (for now)

- **No WebSocket-driven live dashboard**. The SSE stream at
  `/api/v1/stream` is ready to power real-time widgets but the current
  UI sticks to polling/request-response for simplicity.
- **No multi-tenant switcher UI**. The API supports `X-Tenant-ID` headers
  everywhere; the UI currently assumes a single tenant. A tenant picker
  lands in a follow-up once the tenant directory API exists.
- **No bulk import / CSV upload**. Admin use-cases are record-level.
- **No theming system**. CSS variables in `styles.css` are the current
  escape hatch; a first-class theme engine is a later milestone.
