---
title: LAST JOB column as a link with run-status dot
type: idea
status: open
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
