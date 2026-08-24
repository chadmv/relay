---
title: An agent that re-registers and fails faster than the grace window defers its own requeue indefinitely
type: bug
status: open
created: 2026-08-24
priority: medium
source: Phase 4 correctness lens of the 2026-08-24 finishregister-strand slice; reported rather than fixed, as out of that slice's scope
---

# An agent that re-registers and fails faster than the grace window defers its own requeue indefinitely

## Summary

`GraceRegistry.Start` resets to the full `RELAY_WORKER_GRACE_WINDOW` (2m default) on every call. A
worker that reconnects and then fails registration - or connects and disconnects - faster than that
window gets `grace.Cancel` followed by a fresh `grace.Start` on every cycle, so the requeue timer is
pushed out on each iteration and **never fires**. Its `dispatched`/`running` tasks stay assigned to a
worker that is not doing anything with them until the coordinator stale-task watchdog reaches them at
`RELAY_TASK_MAX_ASSIGNMENT` (24h default) - and that watchdog marks them `timed_out` rather than
requeueing, so the work fails a day later instead of being re-run elsewhere.

## Context

Found while reviewing the 2026-08-24 `finishregister-strand` slice, which closed the case where a
registration failing after `RegisterWorkerConnection` left the worker `online` with no teardown at
all. That fix is correct and this is not a regression from it - before the fix the same crash-loop
produced a strictly worse outcome (no release attempt of any kind). But the slice's framing, "the
strand is closed", holds for the **single-shot** case only, and the comment in
`internal/worker/handler.go` beside `grace.Cancel` now says so explicitly.

The shape is the one [[reference_recovery_bound_must_be_time_based]] describes: a bound expressed as
"one timer per disconnect event" caps nothing when the event itself re-arms the timer.

## Repro / Symptoms

Not yet driven end to end. Expected shape:

1. Give a worker one or more `dispatched`/`running` tasks.
2. Have the agent connect and fail registration (or disconnect) on a loop with a period shorter than
   `RELAY_WORKER_GRACE_WINDOW` - a crash-looping agent, a bad P4 ticket causing an early exit, or a
   supervisor restarting it tightly.
3. Observe the tasks are never requeued: each cycle's `grace.Cancel` discards the pending timer and
   the fresh `grace.Start` restarts the full window.
4. The tasks remain assigned until the stale-task watchdog stamps them `timed_out` at 24h.

## Proposal

Bound the deferral by time rather than by activity, so the reset cannot be driven by the event it is
meant to survive. Options, roughly in increasing cost:

- Track a `firstDisconnectAt` per worker alongside the timer; on `Start`, keep the ORIGINAL deadline
  rather than extending it when an entry already exists for the same worker within the window.
- Cap the number of consecutive resets before firing regardless.
- Fire on `min(existing deadline, now + window)` so a reconnect can only ever bring the requeue
  forward, never push it out.

The first is closest to the invariant's intent and is a small change inside `GraceRegistry`. Note the
registry became epoch-monotonic on 2026-08-24 (a stale epoch no longer displaces a live entry), so
any change here must preserve that and must keep `Start`'s same-epoch window-reset semantics, which
`TestGraceRegistry_StartAtTheSameEpochStillResetsTheWindow` pins.

## Acceptance / Done When

- A test drives N reconnect-and-fail cycles at a period shorter than the grace window and asserts the
  requeue still fires - i.e. the deadline is not extended indefinitely.
- The epoch-monotonicity rule and the same-epoch reset semantics both still hold.
- The comment beside `grace.Cancel` in `internal/worker/handler.go`, which currently documents this as
  a known limitation, is updated to describe the new bound.

## Related

- `internal/worker/grace.go` - `Start` / `StartWithDuration` / `Cancel`
- `internal/worker/handler.go` - `finishRegister`'s `grace.Cancel`, and `releaseWorkerGeneration`
- [[bug-2026-08-23-failed-finishregister-strands-worker-online]] - the slice that surfaced this
- [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]] - the 24h watchdog that is the only current backstop, and which fails rather than requeues
