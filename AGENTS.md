# AGENTS.md

> Guidance for AI coding agents (Devin, Claude, GPT, Cursor) working in
> this repository. Skim this file before making changes.

## Repository shape

- Go 1.23 module rooted at `github.com/dedeez14/goforge`.
- Clean architecture in 4 layers: `internal/domain`, `internal/usecase`,
  `internal/adapter`, `internal/infrastructure`. Dependencies flow
  inward only.
- Reusable packages live under `pkg/` and must NOT depend on `internal/*`.
- Composition root is `internal/app/Run`. Nothing else should
  construct concrete infrastructure dependencies.

## Non-negotiables

1. **Keep the dependency rule.** A package in `pkg/` importing anything
   from `internal/` is a bug.
2. **No new global state.** Pass services as constructor arguments.
3. **Errors are values.** Domain code returns `*pkg/errs.Error` -
   never bare strings or `fmt.Errorf` for client-visible errors.
4. **Tests live next to the code.** `_test.go` files only; integration
   tests use testcontainers in CI.
5. **Migrations are append-only.** Add a new pair (`up.sql`, `down.sql`)
   instead of editing existing ones.
6. **Don't break public APIs.** Anything in `pkg/` is public; signatures
   change only with a major version bump.

## Standard workflow

1. Read the relevant package doc comment (every package has one).
2. Write or update tests first.
3. Implement the change.
4. Run `make lint test` locally - both must be green.
5. Update `ROADMAP.md` if you completed a milestone.

## What to leave alone unless explicitly asked

- The benchmark harness in `cmd/bench`.
- The Argon2id parameters in `internal/infrastructure/security`.
- The error envelope shape in `pkg/httpx`.
- The migration filenames already on `main`.

## Adding a feature module

1. Implement `module.Module` from `pkg/module`.
2. Mount routes, workers and migrations through the interface.
3. Register the module in `internal/app/Run` between `platform.Build`
   and `server.Register`.
4. Document it under `docs/modules/<name>.md`.

## Tests we expect

- Repository tests use `testcontainers-go` (see `internal/usecase/auth_test.go`).
- HTTP handler tests use Fiber's `app.Test` helper.
- Race detector must pass: `go test -race ./...`.

## Release engineering

- Tags follow `vMAJOR.MINOR.PATCH`.
- Generated artefacts (`forge`, `goforge-api` Docker image) are pushed
  by GitHub Actions on tag.
- The `main` branch is always shippable.
