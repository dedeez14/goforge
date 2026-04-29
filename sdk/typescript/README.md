# @dedeez14/goforge-client

Auto-generated TypeScript SDK for a goforge-based API. The types, the
request shapes and the endpoint functions are all produced from the
live `/openapi.json` served by the running API, so the SDK can never
drift away from the backend.

## Installation (once published)

```bash
npm install @dedeez14/goforge-client
```

## Usage

```ts
import { client, postApiV1AuthLogin, getApiV1AuthMe } from "@dedeez14/goforge-client";

client.setConfig({
    baseUrl: "https://api.example.com",
});

const { data } = await postApiV1AuthLogin({
    body: { email: "me@example.com", password: "supersecret" },
});

client.setConfig({
    headers: { Authorization: `Bearer ${data!.tokens.access_token}` },
});

const me = await getApiV1AuthMe();
```

Every endpoint on the server that is registered with
`openapi.Document.AddOperation` produces a typed function here. The
request and response types are reflected from the Go DTOs so the
JSON shape matches byte-for-byte.

## Regenerating

The generated code (`src/generated/`) and the upstream spec
(`openapi.json`) are both gitignored — they are reproduced from the
running API. One-liner from the repo root:

```bash
make sdk-ts
```

That target runs `forge sdk ts`, which:

1. Downloads `/openapi.json` from the URL you pass (default
   `http://localhost:8080/openapi.json`) into `sdk/typescript/openapi.json`.
2. Runs `npm ci` in `sdk/typescript/` (idempotent after the first run).
3. Runs `npm run generate` → `openapi-ts` writes `src/generated/`.
4. Runs `npm run build` → `dist/` is populated for publishing.

Manually:

```bash
cd sdk/typescript
npm ci
GOFORGE_OPENAPI_URL=http://localhost:8080/openapi.json npm run fetch
npm run generate
npm run build
```

## Publishing

The `dist/` directory is what gets published, not the sources. From
`sdk/typescript/`:

```bash
npm version patch                 # or minor / major
npm publish --access public        # needs NPM_TOKEN
```

`prepublishOnly` cleans, regenerates and rebuilds, so publishing
always bundles a fresh SDK.

## CI

The generation is currently a manual step; wiring it into CI requires
the pipeline to stand the API up (docker-compose ships for this
purpose). Check `docs/sdk-typescript.md` for the recommended job
shape.

## How it's generated

`@hey-api/openapi-ts` is the codegen. It emits:

- A fetch-based client (ESM, tree-shakable).
- Typed endpoint functions per operation (`getApiV1AuthMe`, …).
- Request/response types derived from the OpenAPI schemas.
- `@hey-api/client-fetch` runtime for config/headers/baseUrl.

See `openapi-ts.config.ts` for the exact options. The choice is easy
to revisit — every generator reads the same `openapi.json` so
swapping codegens only churns one file.
