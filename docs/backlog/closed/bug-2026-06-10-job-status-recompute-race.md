---
title: Job status recompute race can leave a job stuck in running forever
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
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

## Resolution
Fixed in PR #36 (merge commit cda9a3a), 2026-06-20. Added an atomic `RecomputeJobStatus :one` query in `internal/store/query/jobs.sql` (single `UPDATE jobs ... FROM (SELECT aggregate over tasks)`), rewrote `updateJobStatusFromTasks` as a thin wrapper preserving its signature and `""`-on-error contract so the terminal SSE event still fires, and deleted the dead zero-caller copy from `internal/api/jobs.go`. Covered by the store-layer integration test `TestRecomputeJobStatus`, whose concurrent-completion sub-test claims both tasks then races two goroutines (mark-done + recompute) so it genuinely reproduces the interleaving and fails against the pre-fix read-modify-write code.
