# Devcontainer / Codespaces

goforge ships a fully-configured development container so a new
contributor can go from a cold clone to a running API in one click.

## Open in VS Code locally

1. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/)
   and the VS Code [Dev Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers)
   extension.
2. Clone the repo and open it in VS Code.
3. Command Palette → **Dev Containers: Reopen in Container**.

The container builds once (Go 1.23 + tooling, Postgres 16 sidecar),
then VS Code reattaches automatically. On a mid-range laptop the
first build takes ~2 minutes; subsequent rebuilds reuse the
`go-mod-cache` / `go-build-cache` volumes and finish in seconds.

## Open in GitHub Codespaces

1. From the repo page click **Code → Codespaces → Create codespace on `main`**.
2. Wait for the codespace to finish provisioning (same stack, running
   in the cloud).

Codespaces automatically forwards port 8080 (API) and 5432 (Postgres);
VS Code surfaces them under the **Ports** panel.

## What's inside

| Component              | Version / image                                      |
| ---------------------- | ---------------------------------------------------- |
| Workspace image        | `mcr.microsoft.com/devcontainers/go:1-1.23-bookworm` |
| Postgres               | `postgres:16-alpine`                                 |
| `golangci-lint`        | v1.61.0 (matches CI)                                 |
| `migrate`              | v4.17.1 (matches the production compose)             |
| `goimports`            | latest                                               |
| Docker-outside-of-Docker | via devcontainer feature, Compose v2               |

The `postCreate.sh` script waits for Postgres, applies migrations,
installs the Go tooling above, and is idempotent - running it again
after `Rebuild Container` is safe.

## Environment

The devcontainer pre-sets a development-only configuration:

- `GOFORGE_APP_ENV=development`
- `GOFORGE_LOG_PRETTY=true`
- `GOFORGE_DATABASE_DSN=postgres://goforge:goforge@postgres:5432/goforge?sslmode=disable`
- `GOFORGE_JWT_SECRET=devcontainer-insecure-secret-only-for-local-dev-0123456789`

These are devcontainer-only and never shipped to production. The
`config.Verify` boot-time check deliberately rejects this secret
when `GOFORGE_APP_ENV=production`.

## Common tasks

```bash
make run          # start the API at http://localhost:8080
make test         # go test -race -count=1 ./...
make lint         # golangci-lint
make migrate-up   # apply pending SQL migrations
make scaffold name=Order   # generate a new resource module
```

Postgres is reachable at `postgres:5432` from inside the workspace
and forwarded to `localhost:5432` on the host, so tools like
`psql`, DBeaver, or SQLTools (pre-configured) can connect from
either side.

## Troubleshooting

- **"go: command not found" in a new terminal** — open a fresh
  terminal; the `postCreate.sh` script appends the Go tool path to
  `~/.bashrc`. Existing terminals stay on the old environment.
- **"migrations failed" in the post-create log** — Postgres may
  still have been starting up. Run `make migrate-up` manually; the
  first-run race is logged as a warning rather than a fatal error
  so that the container is always usable.
- **Rebuilding from scratch** — `Dev Containers: Rebuild Without
  Cache`. The `pgdata` volume is preserved across rebuilds; delete
  the `goforge-dev_pgdata` volume on the host to wipe the database.
