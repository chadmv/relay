---
title: Jobs Lanes (swimlanes-by-status) view
type: idea
status: closed
closed: 2026-09-02
resolution: fixed
created: 2026-06-05
source: jobs-list-frontend retro (2026-06-05)
---

# Jobs Lanes (swimlanes-by-status) view

## Summary
Add the Lanes (swimlanes-by-status) view from the design handoff to the Jobs page. Each lane is a separate `GET /v1/jobs?status=<s>&limit=<perLane>` call, capped per-lane (default 10, min 3, max 50), with a "+N more →" overflow linking to the table filtered by that status.

## Context
Deferred from the first jobs-list slice (Table view only). The hi-fi `HoloLanes` component in `design_handoff_relay_holo/hifi3-holo-pages.jsx` is the reference.

## Related
- `web/src/jobs/` feature
- `design_handoff_relay_holo/reference/screens/jobs-list.js`
- `docs/retros/2026-06-05-web-jobs-list.md`

## Resolution
The Jobs page gained a localStorage-persisted Table / Lanes switch and a Lanes view: five status lanes in lifecycle order from a JOB_STATUSES tuple the JobStatus type derives from, one useQueries hook firing a capped per-status list call on the table's 3 s cadence, per-lane loading, empty, error and Retry states with failure isolation, lane counts from each response's own total (null until it exists, never from the stats strip), a plus-N-more button that switches to the table with that status chip (adding a Cancelled chip), a keyboard-reachable horizontal scroller so the document never overflows, and a jobs-lanes surface in the browser harness. Per-lane cap is fixed at 10; the stepper, a fluid desktop grid and a shared persisted-view hook are filed separately. The item's premise that plus-N-more could link to a status-filtered table URL was refuted: the SPA carries no URL search-param state.
