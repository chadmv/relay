---
title: owner_email lookup errors are swallowed silently
type: bug
status: open
created: 2026-06-05
priority: low
source: noticed during web Schedules list code review (retro 2026-06-05-web-schedules-list)
---

# owner_email lookup errors are swallowed silently

## Summary
The owner_email batch lookup is best-effort: a GetUserEmailsByIDs failure is swallowed silently (internal/api/scheduled_jobs.go fillOwnerEmails), leaving owner_email empty with no log line, so the failure is invisible to operators.

## Related
- internal/api/scheduled_jobs.go (`fillOwnerEmails`)
- docs/retros/2026-06-05-web-schedules-list.md
