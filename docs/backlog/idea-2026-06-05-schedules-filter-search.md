---
title: Server-side filter and search for the Schedules list
type: idea
status: open
created: 2026-06-05
source: deferred from web Schedules list slice (retro 2026-06-05-web-schedules-list)
---

# Server-side filter and search for the Schedules list

## Summary
Filter chips (All/Enabled/Disabled) and the name/owner/cron text search from the Holo design were deferred; they need server-side enabled and name-search query params on the GET /v1/scheduled-jobs endpoint (the list endpoint currently supports only sort + cursor pagination).

## Proposal
Add `enabled` and `q` (name/owner/cron substring) query params to the list endpoint and its query variants, then render the filter chips and search input on SchedulesPage.

## Related
- internal/api/scheduled_jobs.go (handleListScheduledJobs)
- web/src/schedules/SchedulesPage.tsx
- docs/retros/2026-06-05-web-schedules-list.md
