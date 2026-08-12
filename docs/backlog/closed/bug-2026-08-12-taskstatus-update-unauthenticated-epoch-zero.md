---
title: Any enrolled agent can set any task's status via the unauthenticated epoch fence, permanently wedging it
type: bug
status: closed
created: 2026-08-12
closed: 2026-08-12
priority: high
source: Spec and Phase 4 review of the task-log assignee-fence iteration (2026-08-12)
resolution: fixed
---

# Any enrolled agent can set any task's status via the unauthenticated epoch fence, permanently wedging it

## Summary
`handleTaskStatus` (`internal/worker/handler.go:417-520`) is the status-path twin of the task-log
hole just closed by `bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero`, and it is worse. It
reads the task, compares `int64(task.AssignmentEpoch) != upd.Epoch` (line 433), and proceeds. It
never checks that the sending connection is the task's assignee, and `UpdateTaskStatus`
(`internal/store/query/tasks.sql:26-29`) fences on `assignment_epoch` alone:

```sql
UPDATE tasks
SET status = $2, worker_id = $3, started_at = $4, finished_at = $5
WHERE id = $1 AND assignment_epoch = $6
```

A never-claimed task sits at `assignment_epoch = 0`, so `Epoch: 0` is a free guess. As with the log
fence, "never claimed" is not the boundary - the fence compares an integer, and epochs advance by one
per requeue/retry/cancel, so any task's current epoch is a handful of small integers probed one
stream message at a time with no rate limit on the recv loop.

**The worst consequence is the third one below, and it was found only during review**: a forged
`RUNNING` permanently wedges the task and its entire downstream DAG.

## Repro / Symptoms
An enrolled agent (any valid long-lived agent token, which is all `Connect` requires) sends
`TaskStatusUpdate{TaskId: <any pending task>, Epoch: 0, Status: ...}` on its own stream. Three
distinct outcomes, all traced through the code and confirmed by two independent reviewers:

1. **`TASK_STATUS_DONE` - silent false success.** `done` is not in the `terminal` set
   (`terminal := statusStr == "failed" || statusStr == "timed_out"`, line 458), so the retry branch
   is skipped and `UpdateTaskStatus` writes `status = 'done'` for work that never ran.
   `GetEligibleTasks` (`tasks.sql:38-48`) unblocks a dependent when `dep.status = 'done'`, so the
   rest of the DAG dispatches against work that did not happen, and `RecomputeJobStatus` reports the
   job green. Nothing anywhere records that the task produced no output.
2. **`TASK_STATUS_FAILED` - one-message DoS against any job.** `tasks.retries` defaults to `0`
   (`internal/store/migrations/000001_initial.up.sql:59`), so on any task that did not opt into
   retries `task.RetryCount < task.Retries` is false, the retry branch is skipped, and
   `FailDependentTasks` (`tasks.sql:99-112`) walks the recursive CTE and marks the whole transitive
   downstream `failed`.
3. **`TASK_STATUS_RUNNING` - permanent unrecoverable wedge.** `handleTaskStatus` passes
   `WorkerID: task.WorkerID` (line 484), which for a never-claimed task is NULL, so the row becomes
   `status = 'running'` with `worker_id` NULL. That row is now invisible to every path that could
   move it:
   - `GetEligibleTasks` requires `status = 'pending'`, so the dispatcher will never claim it.
   - `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`, `GetActiveTasksForWorker` and
     `ListGraceCandidates` are all keyed `WHERE worker_id = $1` (or `JOIN tasks t ON t.worker_id =
     w.id`), and NULL never matches any worker id, so no disconnect, grace-expiry, disable or
     reconcile path can ever see it. `RequeueTaskByID` is keyed by task id but is only ever called
     from `reconcileRunningTasks`, whose candidate set comes from `GetActiveTasksForWorker`.
   - Nothing sweeps `running` tasks by age. There is no timeout reaper for this state.

   The task therefore stays `running` forever, and every task that depends on it stays `pending`
   forever, because `GetEligibleTasks` will not release a dependent until `dep.status = 'done'`.
   The job never reaches a terminal status.

   **Correction to the framing this item was proposed with:** the only recovery is *cancelling the
   job*, not deleting it. `CancelJobTasks` (`tasks.sql:208-219`) matches
   `status IN ('pending','queued','running','dispatched')` and is reachable from
   `internal/api/jobs.go:740`, so cancel does clear the wedge - by marking the task and every
   non-terminal sibling `failed`. There is no path that returns the wedged task to a runnable state,
   and no per-task recovery at all; a manual DB write is the only alternative.

## Context
Found while specifying `bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero` and deliberately
excluded from that change's scope: same root cause, different query, worse impact, and folding it in
would have tripled the blast radius of a fix that needed to be trivially reviewable. See
`docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` sections 10 and 11. Consequence
3 was **not** in that write-up; it came out of the Phase 4 security lens, which is itself an argument
for having split the work.

Threat model is identical to the closed log item: any principal holding a long-lived agent token
(a host-local 0600 file on a machine that by design runs untrusted job payloads), or anyone who can
reach `:9090` when `RELAY_ALLOW_AUTO_ENROLL` is on. Task UUIDs are not guessable but are not secret
either - `GET /v1/tasks/{id}` is `auth(...)`-only with no per-owner gate, so any authenticated user
can enumerate every task id in the system. Unlike the log hole, which is an integrity bug, this is a
data-integrity **and availability** bug.

## Proposal
Fence the status write on the sending connection's authenticated worker identity, exactly as
`AppendTaskLog` now does: `handleTaskStatus` gains the `pgtype.UUID` that `Connect` already computes
at `handler.go:115` and passes it down, so the identity comes from registration and never from the
wire.

**The fix is not a copy-paste of the log one.** `UpdateTaskStatus` has a second caller:
`Dispatcher.failClaimedTask` (`internal/scheduler/dispatch.go:353-362`), which is server-internal.
Verify before designing - it does in fact have a worker id available (`claimed.WorkerID`, non-NULL
by construction because `claimed` came from `ClaimTaskForWorker`), so a single query with a
`worker_id = $N` predicate is viable and would be a tautological but harmless check there. What must
**not** happen is a sentinel meaning "server-internal, skip the check": a zero-value `pgtype.UUID`
binds SQL NULL and any opt-out shaped like that can be reached by a caller that simply failed to
resolve its identity, which fails **open**. The log fence's whole NULL-rejection argument exists to
prevent that. A separate query for the dispatcher path is the conservative option; decide
deliberately and write the reasoning into the SQL comment.

Also decide what `handleTaskStatus` should do about the `WorkerID: task.WorkerID` pass-through. Note
the standing constraint already recorded in `UpdateTaskStatus`'s comment: it writes `worker_id`
without bumping `assignment_epoch`, so clearing `worker_id` there would leave the task at its current
epoch with no assignee and silently kill the running agent's log ingest forever.

Sequence the implementation so RED is behavioral rather than a compile error, the same way the log
fix was staged: thread the identity first with no SQL change (suite stays green), then add the
exposure tests (behaviorally RED), then change the query.

## Acceptance / Done When
- An agent that is not a task's assignee cannot change that task's status, proven by a test that is
  RED against today's code with the RED output captured, sending the task's **current** epoch so the
  epoch predicate matches and only an identity check can reject it.
- A never-claimed task at epoch 0 rejects `DONE`, `FAILED` and `RUNNING` from every worker, each with
  its own named test.
- A positive control on the same code path: the real assignee's status update still lands, including
  the retry branch and the `FailDependentTasks` cascade.
- A caller that loses its identity fails closed (zero-value worker id rejected), pinned at the store
  layer with a mutation proof, as `TestAppendTaskLog_EpochGuarded` case 4 is.
- The existing stale-epoch rejection still works, with its fixture arranged so the epoch remains the
  only failing predicate.
- `Dispatcher.failClaimedTask` still terminally fails a poison-payload task, with a test, and the
  chosen approach for it is documented in the query comment.
- Every other existing test in `internal/worker`, `internal/scheduler` and `internal/store` passes
  with only a mechanical argument addition. Any test needing an assertion changed is reported as a
  finding, not adjusted.

## Related
- Sibling, just fixed:
  `docs/backlog/closed/bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero.md` and
  `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` (sections 10, 11 and the
  section 5 threat model, which applies here unchanged)
- Source: `internal/worker/handler.go` (`handleTaskStatus`, `Connect`),
  `internal/store/query/tasks.sql` (`UpdateTaskStatus`, `GetEligibleTasks`, `FailDependentTasks`,
  `CancelJobTasks`, `RequeueWorkerTasks*`, `GetActiveTasksForWorker`, `ListGraceCandidates`),
  `internal/scheduler/dispatch.go` (`failClaimedTask`)
- Adjacent: [[feature-2026-06-26-audit-log-admin-console-actions]] is where detection of "an agent
  tried to write to a task that is not its own" belongs, for this path and the log path together
- Adjacent: [[bug-2026-08-12-auto-enroll-hostname-takeover]], which widens who can hold an identity

## Notes
Consequence 3 deserves separate emphasis when this is prioritized: consequences 1 and 2 corrupt a
job's record, which is bad but recoverable by resubmitting. Consequence 3 creates a row that no code
path in the system can move, and the only remedy - cancelling the job - destroys the rest of that
job's in-flight work. It is also the cheapest to trigger accidentally, since `RUNNING` is the first
status a real agent sends.

The epoch fence is doing exactly what it was designed for here, as on the log path. This is a
missing second check, not a defect in the fence, and the fix should be framed that way so the
invariant's purpose does not get muddled.

## Resolution

Fixed in two places, deliberately not one. `handleTaskStatus` gained an identity gate in Go,
immediately after its `GetTask` and *before* the epoch gate, comparing the task's `worker_id`
against the connection's authenticated worker (resolved at registration, never taken from the
wire). That placement is the fix: this item's Proposal assumed a SQL predicate would be enough,
but the retry branch calls `IncrementTaskRetryCount` - a bare `WHERE id = $1` - and returns before
`UpdateTaskStatus` is ever reached, so a forged FAILED on a task with retries would have sailed
past an SQL-only fence, burning a retry and evicting the agent legitimately running it. Both
`.Valid` checks in the Go comparison are load-bearing: `pgtype.UUID` is a comparable struct, so a
bare `!=` is the Go form of `IS NOT DISTINCT FROM` and fails open when both sides are zero-valued.

`UpdateTaskStatus` additionally gained `AND worker_id = sqlc.arg(worker_id)` as a structural
backstop for both callers - one fenced statement, no sentinel, no second un-fenced query - and lost
`worker_id` from its SET list, so the statement can no longer clear the column and strand a live
agent at all. `Dispatcher.failClaimedTask` passes `claimed.WorkerID`, where the predicate is
tautological by design and fails closed and loudly.

`bug-2026-06-26-retry-resurrects-cancelled-task` stays open; this change narrows its remaining
exposure to the cancel-during-retry race alone. See
`docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md`.
