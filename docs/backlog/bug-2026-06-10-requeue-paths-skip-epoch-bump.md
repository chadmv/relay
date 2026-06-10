---
title: Requeue/retry paths return tasks to pending without bumping assignment_epoch
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

# Requeue/retry paths return tasks to pending without bumping assignment_epoch

## Summary
`IncrementTaskRetryCount`, `RequeueWorkerTasks`, `RequeueAllActiveTasks`, `RequeueTask`, and `RequeueTaskByID` all reset status to `pending` while leaving `assignment_epoch` unchanged. Until the next `ClaimTaskForWorker`, a late status update or log chunk from the previous assignment still carries a matching epoch and passes the fence, e.g. flipping a pending, unassigned task to `done` or consuming an extra retry. The codebase already acknowledges the hazard: `RequeueWorkerTasksWithEpoch` exists precisely to close it for the disable-worker path. `RequeueTask` (dispatch send failure) and `IncrementTaskRetryCount` race a still-connected agent.

## Proposal
Add `assignment_epoch = assignment_epoch + 1` to all five requeue/retry statements. It is free and makes the fence airtight. Requeueing is the end of an assignment; the fence should treat it that way.

## Related
- `internal/store/query/tasks.sql:21-25, 75-85, 99-104, 124-133` (the five queries)
- `internal/store/query/tasks.sql:177-188` (`RequeueWorkerTasksWithEpoch`, the existing precedent)
- `internal/worker/handler.go:443-449` (retry path)
- bug-2026-06-10-job-cancel-epoch-zero
