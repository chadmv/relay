---
title: Schedule detail page and Edit action
type: idea
status: closed
created: 2026-06-05
closed: 2026-08-12
resolution: fixed
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
- docs/retros/2026-08-12-schedule-detail-page.md

## Resolution
Shipped 2026-08-12 (`2026-08-12-schedule-detail-page`): `/schedules/:id` with an inline
cron/timezone/overlap edit form, a read-only JSON job-spec panel, the server's `next_run_at`,
and a recent-runs table; the list's NAME and a new `Edit` link both route to it.

Three of the item's four design panels shipped whole. The Proposal's framing was wrong in two
directions, both recorded in the retro. The recent-runs table was assumed unavailable and is
not - `GET /v1/jobs?scheduled_job_id=` exists with ownership checked ahead of pagination
(`internal/api/jobs.go:424-454`), so it ships fully real. Conversely the next-fires *preview*
could not ship honestly: `web/` has no cron parser, and any JS preview would be a second
implementation of `robfig/cron/v3`'s `@every`/`@hourly`/IANA semantics that can silently
disagree with the scheduler. Only the server's single authoritative `next_run_at` is shown.

The load-bearing constraint the item did not know about: `PATCH` recomputes `next_run_at` from
`time.Now()` whenever the body merely *carries* a `cron_expr` or `timezone` key, changed or not
(`internal/api/scheduled_jobs.go:584-596`). Edits are therefore sent as a strict diff against
the loaded row, pinned by a dedicated regression test.

Scoped out with enablers filed rather than fabricated:
[[idea-2026-08-12-schedule-next-fires-preview]],
[[bug-2026-08-12-scheduled-job-detail-missing-owner-email]] (the detail endpoint never calls
`fillOwnerEmails`, so the owner line is omitted rather than rendered blank),
[[idea-2026-08-12-schedule-job-spec-editor]], and
[[idea-2026-08-12-detail-page-state-triad-primitive]].
