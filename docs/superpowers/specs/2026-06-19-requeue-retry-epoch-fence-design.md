---
date: 2026-06-19
topic: requeue-retry-epoch-fence
status: approved
backlog: bug-2026-06-10-requeue-paths-skip-epoch-bump
---

# Design: Close the requeue/retry epoch-fence gap

## Problem

Several queries return a task to `pending` (or re-pend it for a retry) while
leaving `assignment_epoch` unchanged. Until the next `ClaimTaskForWorker` bumps
the epoch, a late status update or log chunk from the *previous* assignment
still carries a matching epoch and slips past the epoch fence. The consequences:

- A pending, unassigned task can be flipped to `done`/`failed` by a zombie
  update from the prior generation.
- A late terminal update can burn an extra retry via `IncrementTaskRetryCount`.

This violates the documented **epoch fence** invariant (CLAUDE.md): *"never
return a task to `pending` without bumping the epoch."* The codebase already
acknowledges the hazard - `CancelJobTasks` and `RequeueWorkerTasksWithEpoch`
bump the epoch precisely to close it. The queries below are the stragglers.

The original backlog item named five queries. Investigation refined the scope:
one of them (`RequeueAllActiveTasks`) has no caller and one
(`RequeueWorkerTasks`) becomes a redundant near-duplicate once fixed.

## Scope of changes

All SQL changes live in `internal/store/query/tasks.sql`; run `make generate`
to regenerate `internal/store/tasks.sql.go` (never edit the generated file by
hand).

### 1. Add the epoch bump to the three live queries that remain

Add `assignment_epoch = assignment_epoch + 1` to each:

- **`IncrementTaskRetryCount`** - retry path at
  `internal/worker/handler.go:444`. This races a *still-connected* agent that
  may emit further updates for the old generation, so it is the sharpest case.
- **`RequeueTask`** - dispatch revert on bad-commands JSON and on registry send
  failure, `internal/scheduler/dispatch.go:232` and `:266`.
- **`RequeueTaskByID`** - reconcile path when the server has a task the agent
  did not report, `internal/worker/handler.go:395`.

### 2. Consolidate the worker-requeue queries

After the fix, `RequeueWorkerTasks` would differ from the existing
`RequeueWorkerTasksWithEpoch` only by `RETURNING id`, and both of its callers
discard the result. Therefore:

- **Delete `RequeueWorkerTasks`.**
- **Rename `RequeueWorkerTasksWithEpoch` -> `RequeueWorkerTasks`** and update its
  now-stale comment (it currently says "Unlike RequeueWorkerTasks, this
  bumps...", which no longer makes sense once there is a single worker-requeue
  query and every requeue path bumps the epoch). It keeps `RETURNING id`.
- **Repoint the two callers** that used the non-returning variant at the
  surviving query, discarding the returned IDs:
  - `internal/worker/handler.go:567` (`requeueWorkerTasks`, disconnect path).
  - `cmd/relay-server/main.go:128` (grace-timer expiry callback).
- **Update the disable-worker caller** `internal/api/workers.go:484` for the new
  name (it already uses the epoch-bumping query and consumes the returned IDs to
  send agent cancel signals - behavior unchanged).
- **Update the CLAUDE.md Epoch-fence invariant.** Its bullet cites
  `RequeueWorkerTasksWithEpoch` as an exemplar that ends an assignment by
  bumping the epoch; that symbol no longer exists by that name. Change the
  reference to `RequeueWorkerTasks`, leaving a dangling symbol name out of the
  canonical invariants doc.

### 3. Delete `RequeueAllActiveTasks`

It has no caller anywhere in the Go code (only references are in old specs/plans
and the backlog item). It was superseded by grace-timer seeding
(`seedGraceTimersFromActiveTasks` in `cmd/relay-server/main.go`). Remove the
query rather than fixing a path that never runs.

## Testing

Test-driven, at the store layer, as integration tests (`//go:build
integration`, real Postgres via testcontainers). Mirror the existing
`TestRequeueWorkerTasksWithEpoch_BumpsEpochAndFencesStaleUpdates` in
`internal/store/workers_disabled_test.go`:

For each of the three fixed queries (`IncrementTaskRetryCount`, `RequeueTask`,
`RequeueTaskByID`):

1. Create a job + task, claim it via `ClaimTaskForWorker` (epoch 0 -> 1).
2. Run the requeue/retry query.
3. Assert the task is back to `pending` (or, for retry, `pending` with
   `retry_count` incremented) and `assignment_epoch` is bumped to 2.
4. Assert a stale `UpdateTaskStatusEpoch` at the old epoch (1) is rejected with
   `pgx.ErrNoRows` and does not mutate the task.

For `IncrementTaskRetryCount` specifically, also assert the "burn an extra
retry" failure mode is closed: after the epoch bump, the stale
`UpdateTaskStatusEpoch` at the old epoch returns `pgx.ErrNoRows` **and**
`retry_count` is unchanged (the stale generation cannot drive a second retry).

Update the existing tests:

- `internal/store/store_test.go:182-189` - this is a **modified-query** change,
  not a rename. `RequeueTask` now bumps the epoch between the two claims, so the
  second-claim assertion at line 189 changes from `int32(2)` to `int32(3)`, and
  the comment at line 184 ("epoch goes 1 -> 2") must be corrected accordingly.
- `internal/scheduler/dispatch_test.go:273-279` and
  `internal/store/store_test.go:346-357` - status/worker_id assertions only;
  no epoch value change. Confirm they still compile and pass after the rename.
- `internal/store/workers_disabled_test.go:48,81` - rename the calls for the
  renamed query, and rename the test function itself from
  `TestRequeueWorkerTasksWithEpoch_BumpsEpochAndFencesStaleUpdates` to drop the
  now-meaningless `WithEpoch` (e.g. `TestRequeueWorkerTasks_...`) so the test
  name does not drift from the symbol.

Verify with `make test` and `make test-integration`.

## Out of scope

- No change to the epoch-fence semantics elsewhere (`UpdateTaskStatus`,
  `AppendTaskLog`, `ClaimTaskForWorker`, `CancelJobTasks` are already correct).
- No new columns or migrations - `assignment_epoch` already exists.

## Housekeeping

On completion, `git mv` `docs/backlog/bug-2026-06-10-requeue-paths-skip-epoch-bump.md`
to `docs/backlog/closed/` and update its frontmatter (`status: closed`,
`closed: 2026-06-19`).
