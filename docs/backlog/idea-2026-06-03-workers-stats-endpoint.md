---
title: Add GET /v1/workers/stats aggregate endpoint for fleet-wide status counts
type: idea
status: open
created: 2026-06-03
priority: low
source: web workers slice - the summary strip is page-scoped because no aggregate exists
---

# Add GET /v1/workers/stats aggregate endpoint for fleet-wide status counts

## Summary
The Workers list page shows a status summary strip (online/stale/offline/disabled counts), but because `GET /v1/workers` is cursor-paginated and the UI holds only the first page (50 workers), those counts are page-scoped and labeled as such. A dedicated aggregate endpoint would let the strip show true fleet-wide totals.

## Proposal
- Add `GET /v1/workers/stats` returning counts grouped by status plus a grand total (e.g. `{ "online": 12, "stale": 1, "offline": 2, "disabled": 1, "total": 16 }`), computed with a single `GROUP BY status` query.
- Swap the page-scoped summary strip in `WorkersPage.tsx` to consume this endpoint (a small second query, poll-able on the same cadence).

## Acceptance / Done When
- The endpoint returns per-status counts and a total reflecting all workers, not just one page.
- The Workers summary strip shows fleet-wide totals and drops the "counts for the loaded page" caveat.

## Related
- `internal/api/workers.go` - where the handler would live (alongside `handleListWorkers`).
- `web/src/workers/WorkersPage.tsx` - the page-scoped summary strip this would replace.
- `docs/superpowers/specs/2026-06-03-web-workers-design.md` - recorded as a future gap.
