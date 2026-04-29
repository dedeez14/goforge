// fetch-spec.mjs downloads the OpenAPI document from a running
// goforge API and writes it to ./openapi.json in this package.
// Invoked by `npm run fetch`; the URL is taken from
// GOFORGE_OPENAPI_URL or falls back to localhost:8080.
//
// Keeping this in plain Node (no deps) means it works from a
// fresh `npm install` in the devcontainer or CI with no extra
// tooling.

import { writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const url = process.env.GOFORGE_OPENAPI_URL ?? "http://localhost:8080/openapi.json";
const outPath = resolve(fileURLToPath(new URL(".", import.meta.url)), "..", "openapi.json");

console.log(`fetching OpenAPI spec from ${url}`);

const res = await fetch(url);
if (!res.ok) {
    console.error(`fetch failed: ${res.status} ${res.statusText}`);
    process.exit(1);
}

const spec = await res.json();
// Pretty-print deterministically: the file is committed in a repo
// only when a human decides to snapshot it, so a stable layout
// keeps diffs readable.
await writeFile(outPath, JSON.stringify(spec, null, 2) + "\n", "utf8");

console.log(`wrote ${outPath}`);
