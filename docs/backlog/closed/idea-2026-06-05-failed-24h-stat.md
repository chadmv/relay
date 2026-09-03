---
title: FAILED 24h summary stat for the Schedules page
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
created: 2026-06-05
source: deferred from web Schedules list slice (retro 2026-06-05-web-schedules-list)
---

# FAILED 24h summary stat for the Schedules page

## Summary
The "FAILED 24h" summary stat from the Holo design was dropped; surfacing it needs a failed-runs aggregate (count of schedule-spawned jobs that failed in the last 24h).

## Proposal
Add a backend aggregate (likely part of a /scheduled-jobs/stats endpoint) returning the count of failed schedule runs in a recent window, and render it in the summary strip.

## Related
- web/src/schedules/SchedulesPage.tsx (summary strip)
- docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md
- docs/retros/2026-06-05-web-schedules-list.md

## Resolution
Shipped across lane SB (backend: failed_runs_24h counts schedule-spawned jobs, run-now included, that failed in the last 24 hours, cancellations excluded as an operator action; failing counts schedules carrying a last_error) and lane SF (frontend) of the 2026-09-02 web-frontend batch. Both tiles are labelled by their units (FAILED RUNS 24H, FAILING SCHEDULES) because they count different things over different windows; the hi-fi's single FAILED 24H label was refuted. The two tiles' distinct tone tokens are pinned by a test.
