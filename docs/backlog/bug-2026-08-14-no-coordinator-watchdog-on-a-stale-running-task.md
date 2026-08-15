---
title: A task's timeout is enforced only by the agent, so a connected agent that never reports terminal holds its assignment forever
type: bug
status: open
created: 2026-08-14
priority: medium
source: Phase 4 security lens of the 2026-08-14-tasklog-terminal-append-bound slice, while establishing the honest scope of that fix
---

# A task's timeout is enforced only by the agent, so a connected agent that never reports terminal holds its assignment forever

## Summary

`tasks.timeout_sec` is carried end to end - validated in `internal/jobspec`, stored, and sent to the
agent on dispatch - and is enforced in exactly one place: `internal/agent/runner.go`'s `newRunner`,
which wraps the subprocess context in a `context.WithTimeout`. The **only** writer of the
`timed_out` status is `handleTaskStatus`, translating a `TaskStatusUpdate` the agent chose to send.

**The coordinator has no timer of its own.** A task that goes `running` and never receives a terminal
status update stays `running` indefinitely. Nothing in `internal/scheduler` scans for stale
assignments; the `Dispatcher` only polls *eligible* (pending) tasks. The two mechanisms that look
like they cover this do not:

- **`GraceRegistry`** requeues a **disconnected** worker's tasks after `RELAY_WORKER_GRACE_WINDOW`.
  It is armed by connection teardown, so an agent whose stream stays healthy is invisible to it.
- **`Dispatcher.failClaimedTask`** covers a task that fails to *dispatch*, not one that fails to
  *finish*.

So a task held by a connected agent that hangs, that has its timer disabled (`timeout_sec = 0` means
no deadline, by design), or that is simply lying, never terminates. The job never reaches a terminal
status, the worker slot is never released, and the assignment - `worker_id` plus `assignment_epoch` -
stays live.

## Repro / Symptoms

1. Submit a task with `timeout_sec = 0` (or run a patched agent that never sends a terminal status).
2. The agent claims it, reports `running`, and holds the stream open with keepalive traffic.
3. Observed: the task is `running` forever, its job never completes, the worker's slot stays
   consumed, and `GET /v1/jobs/{id}` reports work in progress that will never end. Expected: some
   coordinator-side bound.

The operator's only recovery today is to cancel the job (`CancelJobTasks` bumps the epoch and nulls
`worker_id`) or to disable/revoke the worker so its connection tears down and the grace path fires.
Both are manual and require somebody to notice.

## Context

Found while establishing the honest scope of the 2026-08-14 trailing-window fix
(`docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`). That slice bounded how long a
**terminal** task accepts log appends, and the lens asked what the fix does *not* buy. This is the
largest remaining answer: a live assignment is unbounded in duration as well as in volume, so a
principal holding a worker token keeps one open write channel per task it never finishes - see
[[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]].

Filed as a **bug**, not an idea, because `timeout_sec` is an advertised product feature whose
enforcement lives entirely on the untrusted side of the boundary. A timeout the agent is free not to
honour is not a timeout; it is a suggestion. That is the framing to keep, and it is why the item is
worth scheduling even in a deployment with no adversary: a wedged agent process produces the same
symptom as a malicious one, and this project has already shipped fixes for wedged agents
(`docs/retros/2026-06-10-wedged-agent-dispatch-fix.md`,
`docs/retros/2026-06-25-sendinventory-wedge-escape.md`).

## Proposal

A coordinator-side sweeper that ends the assignment of a task that has been non-terminal for too
long. Design questions:

- **What "too long" means.** `timeout_sec` plus a generous margin is the obvious answer for tasks
  that set one, and it needs a separate absolute cap for `timeout_sec = 0`. Both env-configurable per
  the standing rule on operational timeouts, and generous by default - P4 syncs can legitimately run
  for hours.
- **The clock to measure from.** `tasks.started_at` is written by `handleTaskStatus` from the
  relay-server Go clock, which makes it comparable with a Go-computed cutoff, exactly as the
  trailing-window fix does. Do not reach for `NOW() - interval`; see `AppendTaskLog`'s comment for
  the argument.
- **What the sweeper writes, and this is the hard part.** Two shapes:
  - **Fail it** (`timed_out`) - matches operator expectation and lets the retry and dependent-cascade
    paths run, but introduces a **server-side terminal writer** competing with the agent's own
    terminal update. It must therefore fence like every other writer: `AND status IN
    ('pending','dispatched','running')` so it cannot flip an already-terminal row, and it must decide
    what happens when the agent's real terminal update arrives one second later (the allow-list makes
    that a silent no-op, which is correct).
  - **Requeue it** (`RequeueTaskByID`, epoch bumped, `worker_id` nulled) - reuses the eviction path
    the codebase already trusts and ends the assignment cleanly, but silently re-runs work that may
    still be executing on the agent. Requires the dispatch-side duplicate-execution question to be
    answered.
  Pick one, and write down why, because the reader will assume the other.
- **The interaction with `GraceRegistry`.** Both mechanisms end an assignment on a timer. Two timers
  that can fire on the same row need an explicit ordering argument, and the epoch fence is what makes
  it safe - state that in the comment rather than leaving it to be re-derived.
- **Whether the agent should be told.** A cancel message on the stream would let the agent kill the
  subprocess rather than leaving an orphan running against a workspace. That is the difference between
  a bookkeeping fix and a real one, and it touches `Connect`'s send path, so it may be a second slice.

**Invariant constraints this design must respect** (from CLAUDE.md, and non-negotiable): every write
to `tasks.status` must fence on `assignment_epoch` or *conditionally* end the assignment by bumping
it; a status predicate must be an **allow-list**, never the equivalent deny-list; and any new send to
an agent goes through that stream's single bounded sender.

## Acceptance / Done When

- A task that has been `running` past its bound is ended by the coordinator with no operator action,
  proven by an integration test with a backdated `started_at` and a **connected** worker - the
  disconnected case is already covered by `GraceRegistry` and would be a vacuous test.
- A task within its bound is untouched, including a long-running one that is still streaming logs.
- The agent's own terminal update arriving after the sweeper has acted is a silent no-op, not a
  resurrection or a double-count, proven by a test.
- A task with `timeout_sec = 0` is still bounded by the absolute cap.
- The bound(s) are env-configurable with documented defaults and a README row.
- The write fences on the epoch (or bumps it) and carries a status allow-list;
  `TestTasksStatusVocabularyIsExactly` gains the new site if a new hard-coded partition is introduced.

## Related

- Source: `internal/agent/runner.go` (`newRunner`, the only enforcement), `internal/agent/agent.go`
  (the dispatch carrying `timeout_sec`), `internal/worker/handler.go` (`handleTaskStatus`, the only
  writer of `timed_out`), `internal/worker/grace.go` (`GraceRegistry`, the disconnect-only path),
  `internal/scheduler/dispatch.go` (`failClaimedTask`, the dispatch-failure-only path)
- The question that produced it: `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`
  section 4, `docs/retros/2026-08-14-tasklog-terminal-append-bound.md` ("The honest scope")
- The other unbounded arm of the same exposure: [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]]
- Prior art on wedged agents: `docs/retros/2026-06-10-wedged-agent-dispatch-fix.md`,
  `docs/retros/2026-06-25-sendinventory-wedge-escape.md`
- The eviction path a requeue-shaped fix would reuse: [[bug-2026-08-12-tasklog-terminal-task-append-unbounded]]
  (closed), whose Resolution explains why ending an assignment closes a write channel

## Notes

Not scope creep on the trailing-window slice: that fix is a predicate in one existing statement, and
this is a new periodic writer with a duplicate-execution question attached. They share only the
observation that produced them - **that the trailing window bounds the post-terminal arm and nothing
else** - which is exactly the kind of finding that should leave an item behind rather than a sentence
in a retro nobody greps.
