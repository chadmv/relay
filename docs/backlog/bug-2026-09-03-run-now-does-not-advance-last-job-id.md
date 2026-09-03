---
title: Run now never advances a schedule's last_job_id, so LAST JOB points at the previous scheduled fire
type: bug
status: open
created: 2026-09-03
priority: medium
source: fan-in of the 2026-09-02 web-frontend batch
---

# Run now never advances a schedule's last_job_id, so LAST JOB points at the previous scheduled fire

## Summary
handleRunScheduledJobNow creates the job through the shared pipeline but does not write scheduled_jobs.last_job_id, while the schedrunner does on a scheduled fire. After a manual run the schedules list's LAST JOB cell (PR #182) links to the previous scheduled job and its status, and the stats strip's failed_runs_24h counts the manual run's failure against a schedule whose last job says otherwise.

## Context
From the SB spec during PR #180; left out of that lane's scope.

## Proposal
Write last_job_id in the run-now handler under the same statement the schedrunner uses, and decide whether a manual run belongs in failed_runs_24h.

## Related
- [[bug-2026-08-28-run-now-neither-clears-nor-records-the-failure]]
- internal/api/scheduled_jobs.go, internal/schedrunner
