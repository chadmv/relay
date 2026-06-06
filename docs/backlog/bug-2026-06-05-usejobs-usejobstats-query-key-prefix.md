---
title: useJobs and useJobStats share query-key prefix
type: bug
status: open
created: 2026-06-05
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
