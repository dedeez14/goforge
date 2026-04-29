# `forge gen resource --with-admin`

The resource generator can optionally emit an **admin UI companion**
so new aggregates appear in the bundled `/panel/` SPA without any
manual wiring of JavaScript.

## Usage

```bash
forge gen resource --name Invoice --with-admin
```

In addition to the usual set of files (domain, use case, repo, DTO,
handler, migration, test), this produces one extra file:

```
internal/platform/admin_invoice.go
```

The file declares a single function:

```go
func InvoiceAdminResource() adminui.Resource { ... }
```

The companion lives in **package `platform`** deliberately:
`internal/app` already imports `internal/platform`, so putting the
companion in `app` would create a reverse import cycle. Keeping it
in the same package as the `adminui.Mount` callsite makes wiring a
one-line change with no new imports:

```go
// internal/platform/platform.go
adminui.Mount(app, adminui.Config{
    Enabled: cfg.AdminUIEnabled,
    Path:    cfg.AdminUIPath,
},
    adminui.WithResources(InvoiceAdminResource()),
)
```

Reload `/panel/` — the **Invoices** navigation entry is now rendered,
backed by a generic list / create / edit / delete UI that speaks to
`/api/v1/invoices`.

## How it works

1. `pkg/adminui.Mount` accepts a variadic `...Option`. The
   `WithResources(...Resource)` option records resource metadata.
2. `Mount` serves the resource list at `GET {prefix}/_resources.json`.
3. The SPA fetches the manifest once on boot and registers a route
   per resource using the generic `renderGenericResource` renderer.
4. Every mutation (create / update / delete) hits the standard
   `/api/v1/<path>` endpoint — there is no special admin API, and
   no permissions are bypassed. The `Permission` field on a
   `Resource` is a client-side hint only.

## Tuning the generated file

The template uses a safe default set of columns (`name`, `id`,
`created_at`). Edit `internal/platform/admin_<lc>.go` after generation to:

- Change column types (`text`, `number`, `textarea`, `checkbox`,
  `date`, `email`). Unknown types degrade to `text`.
- Mark fields `Required`.
- Hide fields from the list view (`ListHidden`) or the form
  (`FormHidden`).
- Set the `Permission` code that matches what the handler's
  `RequirePermission(...)` middleware enforces — the SPA uses it to
  hide the nav entry for users who do not hold it.

## Omitting the companion

Leave `--with-admin` off to get the baseline generator output. The
admin companion is opt-in so generators that already ship their own
admin pages are not forced into this pattern.
