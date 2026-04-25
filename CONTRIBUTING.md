# Contributing

Thanks for considering a contribution to goforge. The framework is
designed to stay small, opinionated and easy to read - PRs that respect
those constraints are merged faster.

## Ground rules

1. **Discuss before you build.** Open an issue describing the feature
   or fix. Drive-by PRs without context are usually closed without
   review.
2. **Keep changes small.** Prefer multiple focused PRs over one mega
   PR.
3. **No new global state.** Anything stateful belongs to a Module or a
   Service constructed at the composition root (`internal/app`).
4. **Tests + lint must be green.** CI runs `gofmt`, `golangci-lint` and
   `go test -race ./...`.

## Getting set up

```bash
git clone https://github.com/dedeez14/goforge.git
cd goforge
make setup       # installs golangci-lint, goimports, migrate
docker compose -f deploy/docker/docker-compose.yml up -d   # Postgres
cp .env.example .env
make migrate-up
make run
```

## Running checks

```bash
make lint        # golangci-lint
make test        # go test -race ./...
make bench       # 200k-request load test (writes results to docs/)
```

## Pull request checklist

- [ ] Tests cover the behaviour you changed.
- [ ] `make lint` and `make test` pass locally.
- [ ] Public APIs have package-level doc comments.
- [ ] Breaking changes are flagged in the PR body and the changelog.
- [ ] Migrations are reversible (both `up` and `down` scripts).
- [ ] No secrets, sample tokens or production hostnames are
  committed.

## Module authors

If you are publishing a third-party module, follow the contract in
[`docs/modules.md`](docs/modules.md):

- Implement `module.Module` from `pkg/module`.
- Document any new env vars under your module prefix
  (`GOFORGE_MOD_<NAME>_*`).
- Ship migrations under `migrations/` using `NNNN_<name>.up.sql`.

We will happily link community modules from the README once they ship
a stable v0.1.0 with passing CI.

## Code of conduct

By participating in this project you agree to abide by the
[Code of Conduct](CODE_OF_CONDUCT.md).
