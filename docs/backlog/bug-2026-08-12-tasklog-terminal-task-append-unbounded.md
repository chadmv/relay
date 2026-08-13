---
title: AppendTaskLog has no terminality or time bound, so a finished task's log stream never closes
type: bug
status: open
created: 2026-08-12
priority: medium
source: Phase 4 review of the retry-resurrect status-guard iteration (2026-08-12); raised independently by two lenses
---

# AppendTaskLog has no terminality or time bound, so a finished task's log stream never closes

## Summary

`AppendTaskLog` (`internal/store/query/tasks.sql`) fences on two predicates and only two:

```sql
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id)
      AND t.assignment_epoch = sqlc.arg(assignment_epoch)
      AND t.worker_id = sqlc.arg(worker_id)
), ...
```

There is no terminality predicate and no time bound. That is deliberate today, and this item is not
asking to add a status predicate - see Proposal. What is missing is a **bound of any kind**: an agent
authenticated as worker W can append log rows to a task W finished, at the same epoch, forever. There
is no row cap, no rate limit on the recv goroutine, and no retention anywhere in the schema -
`task_logs` is created in migration `000001` and no statement in the repo ever deletes from it.

The 2026-08-12 retry-resurrect iteration is what makes this worth filing now, in two ways.

**It reaffirmed and pinned the property this depends on.** A terminal transition deliberately does
**not** bump `assignment_epoch` and does **not** clear `worker_id`, so a task's assignment survives
its completion. That is stated in three comment blocks and pinned by
`TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`, because
trailing chunks arriving just after a terminal status must still be stored - an agent's last few
lines of output would otherwise be silently discarded on every task in the system. The trailing-log
flush is the *reason* the assignment outlives the task, and it is correct.

**It closed every other write to a terminal task's row.** After that change no production statement
can modify a terminal task at all: `UpdateTaskStatus` and `IncrementTaskRetryCount` both carry
`AND status IN ('pending','dispatched','running')`. So the row is frozen and its log stream is not.
The asymmetry is now the only remaining way an authenticated agent can keep writing to a task the
server considers finished.

## Repro / Symptoms

No user-visible symptom; this is unbounded storage growth by an authenticated principal.

1. A task is claimed by worker W at epoch N and completed (`done`, `failed` or `timed_out`). The row
   keeps `worker_id = W` and `assignment_epoch = N` by design.
2. W (or anything holding W's agent token) continues sending `TaskLogChunk` messages naming that
   task id at epoch N.
3. Every chunk passes both fence predicates and is inserted. `GET /v1/tasks/{id}/logs` and the SSE
   tail keep growing; the task detail view keeps receiving output for work that ended arbitrarily
   long ago.

Observed: a terminal task's log stream stays writable indefinitely. Expected: some bound - a grace
interval after `finished_at`, a row or byte cap, or both.

## Context

Raised independently by two Phase 4 lenses on the retry-resurrect status-guard iteration
(`docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md`). One assessed it as low-medium
on the grounds that it needs an authenticated agent and produces no correctness error. Filed at
**medium** here for three reasons, recorded so the call can be re-argued rather than re-derived:

- The adjacent, already-accepted item `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` is
  **medium** for the same principal on the same goroutine, and its cost is log-file volume. This
  one's cost is durable database rows with no retention path, which is strictly harder to recover
  from.
- There is no mitigating control anywhere: no cap in the statement, no rate limit on the gRPC
  message loop, no retention job, no `task_logs` pruning in any migration.
- The threat model that the three fence iterations converged on is precisely "a compromised but
  enrolled agent, over its own assigned tasks" (see the arc section of
  `docs/retros/2026-08-12-retry-resurrect-status-guard.md`). This is one of the few remaining things
  that principal can still do at unbounded scale.

It is **not** a privilege-boundary bug: an attacker in this position already owns the task's log
content while the task is running, and the 2026-08-12 assignee fence removed every *other* task from
its reach. The defect is the absence of a bound, not the absence of a check.

## Proposal

**Bound it on time, not on status.** A status predicate (`AND t.status IN
('pending','dispatched','running')`, matching the two writers hardened on 2026-08-12) is the obvious
symmetry and it is the wrong fix: it would reject the trailing flush that
`TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` pins,
which is the exact regression the retry-resurrect iteration argued out at length. The flush arrives
*after* the terminal status by construction, so any predicate keyed on status closes the window it
needs.

Sketch, to be argued at spec time rather than adopted as written:

```sql
AND (t.finished_at IS NULL OR t.finished_at > NOW() - <grace interval>)
```

Design questions that belong in that spec:

- **The interval, and where it is configured.** Generous by default, and env-configurable per the
  project's standing rule on operational timeouts. It must comfortably exceed the agent's own flush
  window plus any reconnect delay, or it silently truncates real output - the same failure mode a
  status predicate would cause, just less often and harder to reproduce.
- **A row or byte cap as the second half, or instead.** A time bound stops the *indefinite* case; it
  does not stop an agent writing gigabytes inside the grace interval. A cap is a different statement
  shape (a count subquery, or a counter column) and has its own cost on the recv path, where the
  current statement is deliberately one round trip.
- **What happens on rejection.** Almost certainly the same silent drop as the existing fence miss -
  the caller cannot distinguish stale from forged today and adding a log line here would hand back
  the flood vector `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` is about.
- **Retention, separately.** Even with a bound, `task_logs` grows without limit across a farm's
  lifetime and nothing prunes it. That is arguably its own item; note it here so it is a decision.

## Acceptance / Done When

- A chunk sent by a task's genuine assignee at the correct epoch, more than the grace interval after
  `finished_at`, is rejected and stored nowhere - proven by a test that is RED against today's code.
- **The trailing flush still works**, proven by a test in which a chunk arrives just after the
  terminal status and IS stored, and by
  `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` passing
  with no edit.
- A positive control on a live task: a chunk from the assignee at the current epoch on a
  non-terminal task is still stored and still published.
- `handleTaskLog` still performs exactly one DB round trip and one statement; no new query,
  goroutine or queue on the recv goroutine.
- No new log line on the rejection path.

## Related

- Source: `internal/store/query/tasks.sql` (`AppendTaskLog`'s fence CTE), `internal/worker/handler.go`
  (`handleTaskLog`), `internal/store/migrations/000001_initial.up.sql:76` (`task_logs`, no retention)
- The property this depends on: `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md`
  (the two-predicate fence) and
  `docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md` (why a terminal transition
  keeps the assignee)
- The change that made this the last unbounded write to a terminal task:
  `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md`, and
  [[bug-2026-06-26-retry-resurrects-cancelled-task]] (closed 2026-08-12,
  `docs/backlog/closed/bug-2026-06-26-retry-resurrects-cancelled-task.md`)
- Same principal, same goroutine, different cost: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]]
- Adjacent: [[bug-2026-08-12-tasklog-epoch-int32-truncation]] (same call site),
  [[idea-2026-08-09-task-log-tail-and-paging-improvements]] (the read side of the same table)

## Notes

The useful thing to preserve from this item even if it is never scheduled: the assignment outliving
the task is **load-bearing**, not an oversight, and anybody who "fixes" this with a status predicate
will pass every existing test except one and will silently truncate the tail of every task's output
in production. That is why the bound has to be time-based, and why that sentence belongs in the code
when the fix lands rather than only here.
</content>
