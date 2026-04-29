// Public entrypoint of @dedeez14/goforge-client.
//
// Everything under ./generated is produced by `npm run generate`
// from openapi.json. Re-exporting from a hand-written index keeps
// the public API stable even if the generator is switched out.
//
// This file is committed; ./generated is not. Run
//   npm run fetch && npm run generate
// before `npm run build` to populate it.

export * from "./generated";
