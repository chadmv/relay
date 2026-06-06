---
title: My jobs toggle (server ?mine= filter)
type: idea
status: open
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
