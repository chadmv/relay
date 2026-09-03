---
title: My jobs toggle (server ?mine= filter)
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
created: 2026-06-05
source: jobs-list-frontend retro (2026-06-05)
---

# My jobs toggle (server ?mine= filter)

## Summary
Add the "My jobs" toggle from the design, backed by a real server-side `?mine=true` filter (jobs submitted by the current user). A client-side-only filter is misleading under pagination because it only sees the current page, so this needs a WHERE clause added across the jobs list queries.

## Context
Deferred from the first jobs-list slice. Implementing `?mine=` means adding an optional `submitted_by = current_user` predicate to the unfiltered sort-variant queries (similar to how `status != 'revoked'` was threaded through the workers queries).

## Related
- `internal/api/jobs.go`, `internal/store/query/jobs.sql`
- `web/src/jobs/JobsPage.tsx`
- `docs/retros/2026-06-05-web-jobs-list.md`

## Resolution
Shipped in lane JF of the 2026-09-02 web-frontend batch on the server-side ?mine= from PR #178: a My jobs toggle on the Jobs page, page-level state applied to the table, lanes and timeline views alongside q, so every view and its invalidation on cancel, retry and create see the same filter.
