---
title: useJobs and useJobStats share query-key prefix
type: bug
status: closed
created: 2026-06-05
closed: 2026-06-21
resolution: fixed
priority: low
source: jobs-list-frontend retro (2026-06-05)
---

# useJobs and useJobStats share query-key prefix

## Summary
`useJobs` uses the react-query key `['jobs', sort, status, cursor]` and `useJobStats` uses `['jobs', 'stats']`. They share the `'jobs'` prefix, so a broad `invalidateQueries({ queryKey: ['jobs'] })` would invalidate the stats query alongside the list. No code triggers this today, but it is a latent coupling.

## Proposal
Give the stats query a distinct top-level key (e.g. `['job-stats']`) so list and stats invalidation are independent.

## Related
- `web/src/jobs/useJobs.ts`, `web/src/jobs/useJobStats.ts`
- `docs/retros/2026-06-05-web-jobs-list.md`

## Resolution
fixed (2026-06-21). `useJobStats`'s react-query key changed from `['jobs', 'stats']` to a distinct top-level `['job-stats']`, so a broad `invalidateQueries({ queryKey: ['jobs'] })` no longer matches the stats query. A grep confirmed the key definition was the only reference site (no `invalidateQueries`/`setQueryData`/`getQueryData` targeted the old key). New `web/src/jobs/queryKeyDecoupling.test.tsx` renders both hooks, invalidates `['jobs']`, and asserts the stats endpoint is not refetched while the list is - proven RED (statsCalls 2 vs expected 1) against the shared-prefix code. Full web suite (149 tests) + `tsc --noEmit` green.
