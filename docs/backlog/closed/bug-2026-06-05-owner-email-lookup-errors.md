---
title: owner_email lookup errors are swallowed silently
type: bug
status: closed
created: 2026-06-05
closed: 2026-06-21
resolution: fixed
priority: low
source: noticed during web Schedules list code review (retro 2026-06-05-web-schedules-list)
---

# owner_email lookup errors are swallowed silently

## Summary
The owner_email batch lookup is best-effort: a GetUserEmailsByIDs failure is swallowed silently (internal/api/scheduled_jobs.go fillOwnerEmails), leaving owner_email empty with no log line, so the failure is invisible to operators.

## Related
- internal/api/scheduled_jobs.go (`fillOwnerEmails`)
- docs/retros/2026-06-05-web-schedules-list.md

## Resolution
Fixed 2026-06-21 (autopilot batch, item owner-email-lookup-errors). The `GetUserEmailsByIDs` error
branch in `fillOwnerEmails` (`internal/api/scheduled_jobs.go`) now logs
`scheduled_jobs: GetUserEmailsByIDs (N owner id(s)): <err>` (stdlib `log.Printf`, matching the
project's `subsystem:`-prefixed convention) before returning, so the failure is visible to operators.
Behavior is otherwise unchanged - still best-effort, still leaves `owner_email` empty on failure. No
dedicated test: the only failure seam is a broken DB and the handler tests are integration-tagged, so
a log-capture unit test would be disproportionate for a one-line observability change; verified by
build + vet + the api suite.
