---
title: Upgrade vite 5→8 and vitest 2→4 to clear dev-tooling audit advisories
type: feature
status: open
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
