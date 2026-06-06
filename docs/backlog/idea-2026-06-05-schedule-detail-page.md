---
title: Schedule detail page and Edit action
type: idea
status: open
created: 2026-06-05
source: deferred from web Schedules list slice (retro 2026-06-05-web-schedules-list)
---

# Schedule detail page and Edit action

## Summary
The schedule detail page and its "Edit" action are deferred; the list has no way to view or edit a schedule's cron/spec yet. The Holo design includes a HoloScheduleDetail page (editable cron/tz/overlap, read-only job spec, next-fires preview, recent-runs table).

## Proposal
Add a /schedules/:id detail route and wire the list's Edit action to it. Reuse the existing PATCH /v1/scheduled-jobs/:id endpoint for inline edits.

## Related
- design_handoff_relay_holo (HoloScheduleDetail)
- docs/retros/2026-06-05-web-schedules-list.md
