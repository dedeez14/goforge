// Configuration for @hey-api/openapi-ts. Consumed by `npm run generate`.
//
// The config deliberately points at ./openapi.json (committed or
// `npm run fetch`-ed from a running API) rather than a URL, so
// generation is deterministic and reproducible in CI without
// network access.
import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
    input: "./openapi.json",
    output: {
        path: "./src/generated",
        // Keep the output stable: we publish the generated files,
        // so random formatting churn would bloat every release diff.
        format: "prettier",
        lint: false,
    },
    // fetch is ubiquitous (browsers + Node 18+ + Deno + Bun). Switch
    // to "axios" here if a downstream consumer needs interceptors.
    client: "@hey-api/client-fetch",
    types: {
        // emits `enum` rather than string unions, which is nicer for
        // exhaustiveness checks in TypeScript.
        enums: "typescript",
    },
});
