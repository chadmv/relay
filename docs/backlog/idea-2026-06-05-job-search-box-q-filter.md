---
title: Job search box (server ?q= filter)
type: idea
status: open
created: 2026-06-05
source: jobs-list-frontend retro (2026-06-05)
---

# Job search box (server ?q= filter)

## Summary
Add the job search box from the design, backed by a server-side `?q=` filter matching job name and owner email. Like the "My jobs" toggle, a client-side-only search is misleading under pagination, so it needs to be a real list-query filter dimension.

## Context
Deferred from the first jobs-list slice. Interacts with the existing sort+status mutual-exclusion rules, so the semantics of combining `?q=` with `?sort=`/`?status=` need to be defined when implemented.

## Related
- `internal/api/jobs.go`, `internal/store/query/jobs.sql`
- `web/src/jobs/JobsPage.tsx`
- `docs/retros/2026-06-05-web-jobs-list.md`
