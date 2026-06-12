---
title: Job cancellation 500s for any job whose tasks were ever claimed (epoch 0 mismatch)
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-11
priority: high
source: full-codebase review (2026-06-10)
---

# Job cancellation 500s for any job whose tasks were ever claimed (epoch 0 mismatch)

## Summary
`handleCancelJob` cancels non-terminal tasks via the epoch-fenced `UpdateTaskStatus` but never sets `AssignmentEpoch`, so it defaults to 0. Any task that was ever dispatched has epoch >= 1, so the update returns `pgx.ErrNoRows`, the handler returns 500, and the whole cancel transaction rolls back. Cancelling a job with running or previously-claimed tasks is broken in production.

## Repro / Symptoms
The test suite masks the bug: `seedRunningTask` in `internal/api/jobs_cancel_test.go:93-126` sets `status='running'` via raw SQL "without bumping epoch", a state that cannot occur in production. Seeding through `ClaimTaskForWorker` reproduces the 500.

## Proposal
Add a dedicated set-based query that bypasses the fence and bumps the epoch so in-flight agent updates are rejected afterward:

```sql
-- name: CancelJobTasks :many
UPDATE tasks
SET status = 'failed', worker_id = NULL, finished_at = NOW(),
    assignment_epoch = assignment_epoch + 1
WHERE job_id = $1 AND status IN ('pending', 'queued', 'running', 'dispatched')
RETURNING id, worker_id, status;
```

Then fix the test to seed via `ClaimTaskForWorker`.

## Related
- `internal/api/jobs.go:720-729`
- `internal/store/query/tasks.sql:12-19` (`UpdateTaskStatus`)
- `internal/api/jobs_cancel_test.go:93-126`
