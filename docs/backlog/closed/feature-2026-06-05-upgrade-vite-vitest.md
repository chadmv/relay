---
title: Upgrade vite 5→8 and vitest 2→4 to clear dev-tooling audit advisories
type: feature
status: closed
closed: 2026-09-02
resolution: fixed
created: 2026-06-05
priority: low
source: noticed during jsdom bump (commit 3b058f6) — `npm audit` flagged 5 dev-only vulnerabilities
---

# Upgrade vite 5→8 and vitest 2→4 to clear dev-tooling audit advisories

## Summary
`npm audit` in `web/` reports 5 vulnerabilities (4 moderate, 1 critical), all in build/test tooling
(esbuild, vite, vitest and its transitive vite-node / @vitest/mocker). Clearing them requires the
major-version upgrades `vite@5 → vite@8` and `vitest@2 → vitest@4`, which `npm audit fix --force`
applies as breaking changes. These should be done as a deliberate, separately-tested migration rather
than bundled into an unrelated change.

## Context
Surfaced while bumping `jsdom` to `^29` to drop the deprecated `whatwg-encoding` warning. None of these
advisories affect the deployed `relay-server`: the web SPA is built to static assets and embedded, so
vite/vitest/esbuild never ship to production. Real exposure is limited to local dev — and the critical
vitest advisory only triggers when the Vitest UI server is listening (`vitest --ui`), which the project's
`vitest run` / `vitest` scripts never start.

## Repro / Symptoms
Run `npm audit` in `web/`:

- esbuild `<=0.24.2` (moderate) — [GHSA-67mh-4wv8-2f99](https://github.com/advisories/GHSA-67mh-4wv8-2f99): any site can send requests to the running dev server and read responses.
- vite `<=6.4.1` (moderate) — [GHSA-4w7w-66w2-5vf9](https://github.com/advisories/GHSA-4w7w-66w2-5vf9): path traversal in optimized-deps `.map` handling; also pulls vulnerable esbuild.
- vitest `<=4.1.0-beta.6` (critical) — [GHSA-5xrq-8626-4rwp](https://github.com/advisories/GHSA-5xrq-8626-4rwp): arbitrary file read/exec when the Vitest UI server is listening.
- @vitest/mocker, vite-node (moderate) — no own advisory; flagged for depending on vulnerable vite.

## Acceptance / Done When
- `web/package.json` updated to `vite@^8` and `vitest@^4` (plus any required `@vitejs/plugin-react` / `@vitest/*` bumps).
- `npm install` runs clean and `npm audit` reports 0 vulnerabilities (or only accepted, documented ones).
- `npm test` — all web tests pass.
- `npm run build` succeeds and the embedded SPA still serves correctly from `relay-server`.

## Related
- Commit 3b058f6 (jsdom `^25` → `^29`)
- `web/package.json`, `web/vite.config.*`, `web/package-lock.json`

## 2026-08-24: the sequencing argument inverted, and three new requirements

ROADMAP has argued that this upgrade should happen **before** the new test surface grows. That ordering
is now inverted by events: the browser harness shipped first (2026-08-24), so the upgrade has to carry
it rather than precede it. Three concrete additions to this item's scope:

1. **`web/vite.config.ts` now sets `test.exclude`** as `[...configDefaults.exclude, 'e2e/**']`, importing
   `configDefaults` from `vitest/config`. The spread is load-bearing - the bare form deletes vitest's
   default excludes and sends it walking `node_modules`. Any config rewrite must preserve it, and the
   check is that `npm test` still collects exactly 152 files / 1116 tests.
2. **`web/tsconfig.json` now includes `e2e` and `playwright.config.ts`** and adds `"node"` to `types`.
   Verified during that slice: this widens nothing, because vitest already pulled Node types in
   transitively. Re-verify that claim after the upgrade rather than inheriting it - it is exactly the
   kind of fact an upgrade changes.
3. **A green `npm test` is no longer sufficient evidence.** `make test-e2e` and the `web-ci` workflow are
   now part of the frontend gate, and the harness pins `@playwright/test` exactly. An upgrade that moves
   vite must be checked against the production bundle the harness serves, not just the dev server.

## Resolution
Shipped in lane T of the 2026-09-02 web-frontend batch: vite 5.4.21 to 8.2.2, vitest 2.1.9 to 4.1.11, @vitejs/plugin-react 4.7.0 to 6.1.1, holding every other dependency fixed. The item had understated the baseline: npm audit at HEAD reported 11 advisories, not 5; the toolchain swap cleared 8 (esbuild, vite, vitest, @vitest/mocker, vite-node as packages, plus browserslist, nanoid and postcss as side effects), leaving react-router and undici, which are out of this item scope. Unit tests unchanged at 162 files / 1234 tests; make test-e2e green at 65 on chromium and the webkit-tagged subset; the tsconfig "node" types entry was re-measured and is load-bearing, refuting the 2026-08-24 addendum claim that it widens nothing.
