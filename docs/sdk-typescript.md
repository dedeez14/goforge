# TypeScript SDK

goforge auto-generates a TypeScript client from the same
`openapi.json` it already serves, so frontend code never has to
hand-roll `fetch` URLs or response types. The SDK lives under
`sdk/typescript/` and is published as `@dedeez14/goforge-client`.

## Pipeline

```
                               forge sdk ts
                                    │
  GET /openapi.json ──► openapi.json──► @hey-api/openapi-ts ──► src/generated/ ──► tsc ──► dist/
```

The same command drives local regeneration, the devcontainer and
(eventually) the publish workflow:

```bash
make sdk-ts BASE=http://localhost:8080
```

Under the hood:

1. The Go CLI (`cmd/forge`) fetches `/openapi.json` atomically
   (temp-file + rename so a half-failed download never poisons the
   snapshot) and writes it to `sdk/typescript/openapi.json`.
2. `npm install` (or `npm ci` when a lockfile is present) installs
   the generator into `sdk/typescript/node_modules/`.
3. `npm run generate` runs `@hey-api/openapi-ts`, emitting:
   - `src/generated/types.gen.ts` — request / response DTOs derived
     from the Go DTO reflection in `pkg/openapi`.
   - `src/generated/services.gen.ts` — a typed function per
     operation (`postApiV1AuthLogin`, `getApiV1AuthMe`, …).
   - `src/generated/client.gen.ts` — a configurable `client`
     singleton backed by `@hey-api/client-fetch`.
4. `npm run build` type-checks and emits `dist/` with declaration
   maps.

Pass `--skip-build` to stop after step 3 when iterating inside VS
Code.

## Publishing

`prepublishOnly` reruns `clean → generate → build`, so every
`npm publish` ships a fresh SDK:

```bash
cd sdk/typescript
npm version patch            # commit + tag
npm publish --access public  # needs NPM_TOKEN
```

## Why this shape

- **Generator is replaceable.** Everything downstream of
  `openapi.json` is a single config file; swapping `@hey-api/openapi-ts`
  for another codegen (`orval`, `openapi-typescript`, `oazapfts`, …)
  is a one-file change. The CLI, the Makefile target, the package
  surface and the CI job stay unchanged.
- **No drift is possible.** The spec is reflected from real Go
  structs at runtime; the SDK is reflected from that spec. A change
  in a DTO propagates to the SDK on the next regeneration without
  any manual bookkeeping.
- **Gitignored output.** `dist/`, `src/generated/` and
  `openapi.json` are all ignored — they are never the source of
  truth. That keeps the PR diffs small and stops reviewers from
  approving stale generated code by accident.
- **ESM + fetch by default.** Works in every modern runtime
  (browsers, Node ≥ 18, Deno, Bun, Cloudflare Workers). Axios or
  Node-specific clients can be swapped in via `openapi-ts.config.ts`.

## What's deliberately NOT here (yet)

- **No CI step.** Wiring regeneration into CI needs the API to
  start first (docker-compose ships for this purpose). A future PR
  can stand the stack up in a matrix job, run `make sdk-ts` and
  diff `src/generated/` to catch silent API breakage. The current
  PR keeps the scope to local + publish workflows.
- **No other language SDKs.** `forge sdk <language>` is the
  extension point; add a new case alongside `ts` in
  `cmd/forge/sdk.go` to plug in (say) a Python client. Nothing in
  `sdk/typescript/` is shared with other languages.
- **No committed `openapi.json` snapshot.** Committing a snapshot
  buys deterministic builds but invites silent drift when someone
  forgets to regenerate. Point the Makefile target at a live API
  instead — or add a `make openapi-snapshot` rule in a future PR
  when deterministic builds are needed in CI.

## Consuming the SDK

Once published, a frontend app installs it like any other package:

```bash
npm install @dedeez14/goforge-client
```

```ts
import {
    client,
    postApiV1AuthLogin,
    getApiV1AuthMe,
} from "@dedeez14/goforge-client";

client.setConfig({ baseUrl: "https://api.example.com" });

const { data } = await postApiV1AuthLogin({
    body: { email: "me@example.com", password: "supersecret" },
});

client.setConfig({
    headers: { Authorization: `Bearer ${data!.tokens.access_token}` },
});

const me = await getApiV1AuthMe();
```

Responses are strongly typed — if the Go DTO for `/me` gains a new
field, consumers see it the next time they bump the SDK.
