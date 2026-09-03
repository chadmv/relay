---
title: Persist the `preparing` task status so a workspace sync is distinguishable from a wedged agent
type: feature
status: open
created: 2026-09-03
priority: high
source: SDNM fork divergence analysis (relay_updates.md, PR-8), evaluated 2026-09-03; CLAUDE.md already names `preparing` as the live candidate
---

# Persist the `preparing` task status so a workspace sync is distinguishable from a wedged agent

## Summary

`TASK_STATUS_PREPARING` is in the proto and the agent sends it before every workspace sync, but
`handleTaskStatus` has no case for it, so the row sits at `dispatched` for the whole sync - hours
on a large stream. An operator cannot tell a live sync from a wedged agent, and the worker-tasks
panel shows `dispatched` for both. A fork of relay implemented the status; this item is the
upstream slice. **It must be written spec-first against the lockstep tests, not ported**, because
the fork's version carries a watchdog regression and the tree has grown more sites since the fork
branched.

## Context

Two findings from evaluating the fork's implementation, both of which the port must not repeat:

- **The fork stamps `started_at` at the `preparing` transition.** The coordinator watchdog's
  execution arm in `ListOverdueAssignedTasks` keys on `started_at IS NOT NULL` together with
  `timeout_seconds`, and README documents `started_at` as that arm's anchor ("applies only to tasks
  with `timeout_sec > 0` that have reported `running`"). Stamping it at `preparing` starts the
  timeout clock during the sync: a task with a 30-minute timeout and a two-hour sync is swept
  `timed_out` mid-sync. The fork would not see this if its render tasks carry `timeout_sec: 0`.
  Upstream keeps `started_at` stamped at `running` only. The fork also added a Go-side
  `!task.StartedAt.Valid` guard, mislabelled as pre-existing; it duplicates the SQL `COALESCE` and
  is not wanted.
- **The site count is thirteen, not eleven.** `tasks.sql` gained `ListActiveTasksForWorkerPage`
  and `CountActiveTasksForWorker` after the fork's base. Beyond SQL, `internal/api/jobs.go`'s
  cancel handler collects only `running` and `dispatched` tasks for agent cancel signals, so a
  `preparing` task would be cancelled in the database and never told to stop.

## Proposal

Run this through the full lifecycle (spec, plan, implement, verify). The spec should enumerate the
sites by making the guards red first: `TestTasksStatusVocabularyIsExactly` names every SQL site,
and `TestTaskStatusWritableSetMatchesTheSQLAllowList` pins the Go side.

1. **Migration `000023_task_preparing_status`.** Up: drop and re-add `tasks_status_check` with
   `preparing` between `dispatched` and `running`. Down: `UPDATE tasks SET status = 'dispatched'
   WHERE status = 'preparing'` first, then narrow the constraint. A down-migration test in the
   store integration lane, like the existing `000020` one.
2. **`handleTaskStatus`**: `case TASK_STATUS_PREPARING: statusStr = "preparing"`. The `started_at`
   block stays `if statusStr == "running"`. Add a handler test that a `PREPARING` report moves the
   row and leaves `started_at` NULL, and a watchdog test that a `preparing` task with
   `timeout_seconds = 1` and `started_at` NULL is not swept by the execution arm after the margin.
3. **SQL allow-lists** in `internal/store/query/tasks.sql` that admit the currently-assigned set
   gain `preparing`: `UpdateTaskStatus`, `IncrementTaskRetryCount`, `AppendTaskLog` (first arm of
   the disjunction only; never conjoin it with the recency arm), `GetActiveTasksForWorker`,
   `ListGraceCandidates`, `RequeueTaskByID`, `CountActiveTasksByAllWorkers`,
   `ListOverdueAssignedTasks`, `CancelJobTasks`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`,
   `ListActiveTasksForWorkerPage`, `CountActiveTasksForWorker`. `RequeueTask` (the
   `status = 'dispatched'` revert for a dispatch that never reached the agent) is a deliberate
   decision the spec records either way: a task that reported `preparing` was received. Regenerate
   with `make generate` and follow the CRLF revert procedure in CLAUDE.md; verify the regenerated
   `tasks.sql.go` survived the revert.
4. **Go sites**: `taskStatusIsWritable` in `internal/worker/taskstatus_fence_counters.go`; the
   cancel-signal collection in `internal/api/jobs.go` (add a test that a `preparing` task receives
   the cancel signal); confirm `RecomputeJobStatus` treats it as non-terminal (it should by
   construction, but say so in the plan).
5. **The migration-parsing guard** `tasksStatusVocabulary` in `taskstatus_fence_counters_test.go`
   requires exactly one `ADD CONSTRAINT tasks_status_check` across up-migrations. A drop-and-re-add
   makes it two. The fork's rewrite - take the lexically last definition, after stripping `--`
   comment lines so a quoted prior definition cannot be mistaken for a real one - is acceptable.
   Keep a `require.NotEmpty` on the match list.
6. **Clients**: `web/src/jobs/api.ts` `TaskStatus` union and `taskStatusColor` (accent, like
   `dispatched`); the worker-tasks panel displays it unchanged; its elapsed figure, if added, uses
   `assigned_at`. `python/src/relay/models.py` `TaskStatus` enum - check whether an unknown value
   raises on parse, because an old SDK against a new server is the compatibility case. The CLI's
   `taskIsTerminal` is unaffected; the `logs.go` comment that names `CancelJobTasks` as omitting
   `preparing` becomes stale and is rewritten.
7. **Docs**: README's `RELAY_TASK_MAX_ASSIGNMENT` row ("spends its entire workspace sync in
   `dispatched`"), the `GET /v1/workers/{id}/tasks` row ("`dispatched` or `running`"), and any
   task-status vocabulary listing. CLAUDE.md's Invariants paragraph that calls `preparing` "the live
   candidate" is rewritten to describe the current partition. The watchdog spec is a record of a
   moment and stays as written.
8. **Compatibility**: agents already send `PREPARING`, so no agent change; an older server ignores
   it as today.

## Acceptance / Done When

- A source-bearing task shows `preparing` from the agent's first report until `running`, in the
  API, the CLI, the SPA and the worker-tasks panel.
- `started_at` stays NULL through `preparing`; the watchdog's execution arm does not fire during a
  sync; the assignment arm still bounds it.
- Cancelling a job with a `preparing` task sends that task's agent a cancel signal.
- Every lockstep and vocabulary guard is green with `preparing` in the set, and each went red first.
- The down migration demotes `preparing` rows and restores the old constraint.

## Related

- `internal/worker/handler.go`, `internal/store/query/tasks.sql`,
  `internal/worker/taskstatus_fence_counters.go`, `internal/api/jobs.go`
- `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md` - R3 is the ordering
  argument this slice must preserve
- [[feature-2026-07-01-per-task-timing]] - the `started_at` semantics this item must not change
- [[feature-2026-09-03-p4-sync-progress-heartbeat]] - what the panel shows during `preparing`
- [[bug-2026-09-03-prepare-failure-error-message-is-discarded]]
