---
title: LAST JOB column as a link with run-status dot
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
created: 2026-06-05
source: deferred from web Schedules list slice (retro 2026-06-05-web-schedules-list)
---

# LAST JOB column as a link with run-status dot

## Summary
The LAST JOB column shows a plain short id; making it a link with a run-status dot needs the Jobs detail page plus a last-job-status field on the response.

## Proposal
Once the Jobs detail page exists, link the short id to /jobs/:id. Add a last_job_status (or similar) field to the scheduled-job list response so the row can render a colored status dot like the Holo design.

## Related
- web/src/schedules/SchedulesTable.tsx (LAST JOB cell)
- internal/api/scheduled_jobs.go (scheduledJobResponse)
- docs/retros/2026-06-05-web-schedules-list.md

## Resolution
Shipped across lane SB (backend: last_job_status on the scheduled-job list, get, create and PATCH responses, present exactly when last_job_id is present) and lane SF (frontend) of the 2026-09-02 web-frontend batch. The LAST JOB cell is a Link to the job carrying a dot, the short id and the status word as text, so colour is not the only carrier (the item's colour-only dot was refuted: the row already has a second dot meaning enabled). Four states are pinned: absent key, present, present beside last_error, and the unreachable unpaired one. Known gap carried into the roadmap: run-now does not advance last_job_id, so the cell can lag an interactive run.
