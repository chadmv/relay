---
title: Server-side filter and search for the Schedules list
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
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

## Resolution
Shipped across lane SB (backend: ?enabled= as a real tri-state and ?q= on name, owner email and cron expression, sharing the jobs list's q validator with byte-identical 400 bodies, and the cursor-guard parenthesisation fixed on the previously unwrapped admin statements) and lane SF (frontend) of the 2026-09-02 web-frontend batch. All / Enabled / Disabled chips (Disabled sends false, pinned against the behaves-as-All mutation) and a search box debounced at 300 ms with the cursor reset in the raw handler and the pager disabled while a debounced search is pending, so no request carries a new q with a stale cursor in either click order. The hi-fi's per-chip counts were refuted because the stats endpoint ignores q.
