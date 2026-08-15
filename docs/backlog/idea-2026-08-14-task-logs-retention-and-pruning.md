---
title: task_logs has no retention, no pruning and no operator-reachable delete path
type: idea
status: open
created: 2026-08-14
priority: medium
source: spec section 2.5 and section 10 of the 2026-08-14-tasklog-terminal-append-bound slice; deliberately out of scope there
---

# task_logs has no retention, no pruning and no operator-reachable delete path

## Summary

`task_logs` grows monotonically for the life of a deployment and nothing ever removes a row.
Verified at `ee88de0`, not inferred:

- `grep -n "DELETE FROM" internal/store/query/*.sql` returns eleven statements. **None names
  `task_logs`.**
- The janitor loop in `cmd/relay-server/main.go` deletes only from `agent_enrollments`.
- No migration prunes the table. `idx_task_logs_task_id_id` (migration 000018) is the only
  maintenance it has ever received.
- There is exactly one indirect path: `task_logs.task_id` is `REFERENCES tasks(id) ON DELETE CASCADE`
  and `tasks.job_id` is `REFERENCES jobs(id) ON DELETE CASCADE`, so `DeleteJob`
  (`internal/store/query/jobs.sql`) would cascade all the way down. **`DeleteJob` has no production
  caller** - the only reference in the tree is its own generated method in
  `internal/store/jobs.sql.go`.

So there is no operator-reachable way to reclaim `task_logs` storage at all, short of raw SQL against
the database.

## Context

Found while writing `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md` (section
2.5), which needed to establish whether the bug it was fixing produced *permanent* rows. It does, and
the reason is this item.

The trailing-window bound and the volume cap
([[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]]) both limit what a single task can write.
Neither touches the aggregate: a farm running thousands of jobs a day accumulates every line of every
task's output forever, and the SPA's task-log view plus `GET /v1/tasks/{id}/logs` keep them all
addressable. Retention is the only control that bounds the total.

This pairs naturally with [[idea-2026-08-13-reap-expired-invites-and-tokens]] - same shape (a table
with no reaper), same likely home (the existing janitor loop in `cmd/relay-server/main.go` that
already prunes `agent_enrollments`), and the two together would justify a single well-tested reaper
harness rather than two ad-hoc goroutines.

## Proposal

A retention policy with a deliberately boring implementation. Points to settle at spec time:

- **The key must not be `created_at` on the log row.** A reaper keyed on `task_logs.created_at`
  deletes the early output of a long-running task **that is still writing**, producing a log with a
  hole in the middle and no signal anywhere. Key on the **task's** terminality and age
  (`tasks.finished_at` older than N), so a live task is never touched. This is the trap that must be
  written into the statement's comment when it lands.
- **Where it runs.** The `agent_enrollments` janitor in `cmd/relay-server/main.go` is the existing
  precedent and the obvious home. Confirm what happens under multiple replicas - two janitors
  deleting the same rows is harmless here, unlike a reaper that also writes.
- **How it deletes.** A single unbounded `DELETE` against a table that may hold hundreds of millions
  of rows will hold locks and bloat WAL. Batch it (`DELETE ... WHERE ctid IN (SELECT ... LIMIT n)` or
  a keyset loop) with a per-tick cap, and make the cadence and batch size configurable.
- **Whether `DeleteJob` should get a caller** while the cascade is being reasoned about anyway, or be
  deleted as dead code. Either is defensible; leaving a fully-working destructive statement in the
  tree with no caller and no test is not.
- **Whether the read side needs to say anything.** A task whose logs have been reaped currently
  renders as a task with no output, indistinguishable from a task that produced none. Consider
  whether the API should distinguish "reaped" from "empty", or whether that is over-engineering for
  a retention window measured in months.

**The constraint that must survive from the epoch-fence family:** a reaper that *writes* to `tasks`
(for example, stamping a `logs_reaped_at`) is a **server-side writer to a terminal task's row**, and
the 2026-08-12 retry-resurrect slice deliberately eliminated those. If the design wants such a stamp,
it has to be argued against that decision explicitly, not slipped in - and it must respect the status
allow-lists on `UpdateTaskStatus` and `IncrementTaskRetryCount` rather than introducing a fourth
writer with its own rules. A delete-only reaper avoids the question entirely and is the strongly
preferred shape.

## Acceptance / Done When

- Log rows belonging to tasks that finished more than the configured retention ago are removed on a
  schedule, proven by an integration test with a backdated `finished_at`.
- **A live (`pending`/`dispatched`/`running`) task's logs are never removed**, proven by a test whose
  task has old log rows and no `finished_at`. This is the discriminating case, not a nice-to-have.
- Deletion is batched and bounded per tick, with the cadence, batch size and retention window
  env-configurable and documented in the README env table.
- The reaper writes to no table other than `task_logs`, or the exception is argued against the
  retry-resurrect decision in the spec.
- `DeleteJob` either gains a caller and a test, or is removed.

## Related

- Source: `internal/store/query/tasks.sql` (`AppendTaskLog`, `GetTaskLogs`),
  `internal/store/query/jobs.sql` (`DeleteJob`, the callerless cascade), `cmd/relay-server/main.go`
  (the `agent_enrollments` janitor, the precedent), `internal/store/migrations/000001_initial.up.sql`
  (`task_logs`)
- Pairs with: [[idea-2026-08-13-reap-expired-invites-and-tokens]]
- Bounds a different axis: [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]]
- Evidence and the verified inventory above: `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`
  section 2.5, `docs/retros/2026-08-14-tasklog-terminal-append-bound.md`
- The write path that made this worth checking: closed
  [[bug-2026-08-12-tasklog-terminal-task-append-unbounded]]
- Read side of the same table: [[idea-2026-08-09-task-log-tail-and-paging-improvements]]

## Notes

Deliberately filed separately from the volume cap even though both are about `task_logs` size. They
are different mechanisms (a predicate on the hot recv path versus a background batch delete),
different risk profiles (a cap that is too tight truncates one task's output; a reaper that is wrong
destroys history irreversibly), and different owners in a spec. The last slice in this family found
that combining two items on the strength of "same file" produced a scheduling recommendation that was
wrong on both halves; same file is not the same mechanism.
