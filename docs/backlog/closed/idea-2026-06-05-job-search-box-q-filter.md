---
title: Job search box (server ?q= filter)
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
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

## Resolution
Shipped in lane JF of the 2026-09-02 web-frontend batch on the server-side ?q= from PR #178: a debounced search box on the Jobs page whose trimmed value goes out as q, with the pager disabled while the raw input differs from the debounced value and reset when it lands, so a click inside the debounce window cannot mint a cursor from the previous result. The guard compares trimmed against trimmed; a trailing space no longer leaves paging disabled.
