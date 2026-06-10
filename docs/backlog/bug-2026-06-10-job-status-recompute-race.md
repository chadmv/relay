---
title: Job status recompute race can leave a job stuck in running forever
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

# Job status recompute race can leave a job stuck in running forever

## Summary
`updateJobStatusFromTasks` in the worker handler is a read-modify-write: list tasks, compute status, write it. Two agents finishing the last two tasks of a job concurrently can interleave so the stale `running` write lands last. The job is then permanently `running` with all tasks done: no further task events recompute it, the terminal SSE `job` event never fires, and `CountActiveJobsForSchedule` counts it as active, so an `overlap_policy = "skip"` schedule never fires again.

## Proposal
Make the recompute atomic in one statement so the last writer always sees current task state:

```sql
-- name: RecomputeJobStatus :one
UPDATE jobs j SET status = sub.next FROM (
  SELECT CASE
    WHEN COUNT(*) FILTER (WHERE status NOT IN ('done','failed','timed_out')) > 0 THEN 'running'
    WHEN COUNT(*) FILTER (WHERE status = 'done') = COUNT(*) THEN 'done'
    ELSE 'failed' END AS next
  FROM tasks WHERE job_id = $1
) sub
WHERE j.id = $1
RETURNING j.status;
```

Note: an orphaned copy of `updateJobStatusFromTasks` also exists unused in `internal/api/jobs.go:776-804`; delete it while here.

## Related
- `internal/worker/handler.go:573-600`
- `internal/store/query/scheduled_jobs.sql:69-72` (`CountActiveJobsForSchedule`)
- `internal/api/jobs.go:776-804` (dead copy)
