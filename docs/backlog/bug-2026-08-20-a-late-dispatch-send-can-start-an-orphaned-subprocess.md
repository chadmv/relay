---
title: A wedged dispatch send that eventually SUCCEEDS starts a subprocess for an assignment that was already ended
type: bug
status: open
created: 2026-08-20
priority: low
source: Phase 4 invariants lens of the 2026-08-20-requeue-task-by-id-fence slice; explicitly out of scope there
---

# A wedged dispatch send that eventually SUCCEEDS starts a subprocess for an assignment that was already ended

## Summary

The 2026-08-20 requeue fences close this window on the **write** side. They cannot close it on the
**execution** side.

`dispatchOne` (`internal/scheduler/dispatch.go`) claims a task for W1 and calls `registry.Send`. On a
wedged-but-still-registered sender that call can block for up to the 5s `sendTimeout`. If it **times
out**, the fence now stops the stale requeue from tearing off whatever assignment exists by then -
that is the shipped fix. If it instead **succeeds** near the end of that window, `dispatchOne` returns
true, nothing is requeued, and the `DispatchTask` is delivered to W1 - possibly after some other
writer has already ended that assignment.

The other writer is the same pair of releases the fence work documented: an **admin disable**
(`handleDisableWorker` -> `RequeueWorkerTasks`) or a **second `Connect` from the same agent** whose
reconcile requeues the task (nothing serializes `Connect` per worker). Either way the task is
re-dispatched to W2 while W1 receives, and starts, the original dispatch.

## Repro / Symptoms

1. Dispatcher claims T for W1: `dispatched`, epoch 1. `registry.Send` blocks - W1's 64-slot `sendCh`
   is full but its send loop is alive inside `stream.Send`.
2. An admin disables W1 (or the agent opens a second `Connect`). `RequeueWorkerTasks` returns T to
   `pending` at epoch 2.
3. The dispatcher claims T for W2: `dispatched`, epoch 3. W2 starts executing it.
4. W1's queue drains at 4.9s. The `DispatchTask` for T is written to the stream, `Send` returns nil,
   and `dispatchOne` returns true without requeueing anything (correctly - it succeeded).
5. W1's `handleDispatch` registers a runner for T and starts the subprocess.

**T is now executing on two machines.** The coordinator's own state stays consistent: W1's status
updates and log chunks arrive at epoch 1 and are fenced out silently by `UpdateTaskStatus` and
`AppendTaskLog`, and the next reconcile on W1 sees T at a stale epoch and cancels it. So this is a
resource and duplicate-execution residue, not a data-integrity hole - but the subprocess is real, it
consumes a slot, and for a non-idempotent task (a build, a publish, a P4 submit) so are its side
effects.

## Context

**Be precise about the cancel ordering, because the first framing of this was wrong and the correction
is the useful part.** The draft claimed `sendCancelSignals` (`internal/api/workers.go`) may deliver
the cancel **ahead** of the delayed dispatch, so the agent drops it (`handleCancel` looks up
`a.runners[msg.TaskId]` and does nothing when the id is unknown - `internal/agent/agent.go`) and then
starts the task orphaned.

That cannot happen on **one** stream. `Registry.SendCancel` and the dispatcher's `Registry.Send` both
go through the same worker's single bounded sender into one FIFO `sendCh`, so a cancel enqueued after
the dispatch is delivered after it, finds the runner, and cancels it. The cancel-first variant needs
the two messages on **different senders**, which is the second-`Connect` case: the cancel goes out on
the new registry entry while the dispatch is still parked in the old stream's queue, and both streams
feed the same agent process and the same `a.runners` map.

So there are two distinct residues, and only the second involves the cancel:

- **Always:** a subprocess starts for an assignment the coordinator no longer considers W1's. Bounded
  by the next reconcile's cancel, and by the coordinator watchdog.
- **Second-`Connect` case only:** the cancel that was supposed to prevent exactly this arrives before
  the runner exists and is dropped with no log line, so the only remaining stop is the next reconcile.

The shipped Go agent maintains a single stream, so the second variant needs a re-implemented agent, a
reconnect that leaves the old server-side stream alive and readable, or a deliberate client. The
first variant needs neither.

**Not scope creep:** the requeue-fence slice is what establishes that no requeue predicate can reach
this. Both statements now reject the stale writer, which is correct and which is precisely why the
execution side is now the only remaining exposure.

## Proposal

No requeue predicate can fix this. Three shapes, in increasing order of cost, to be settled rather
than assumed:

1. **An ordering guarantee between the dispatch and the cancel.** Only helps the second variant, and
   only if cross-stream ordering is made explicit (e.g. a disable/reconcile release refuses to
   complete until the prior sender for that worker is closed). Interacts with identity-checked
   teardown; do not weaken that invariant to buy it.
2. **A per-assignment fencing token the agent checks before starting.** `DispatchTask` already carries
   `epoch`; `handleDispatch` ignores it for admission and only stores it on the runner. The agent
   cannot validate an epoch on its own, so this needs the coordinator to re-assert currency - which is
   a round trip on the dispatch path. **This is a named non-goal today**, in `tasks.sql`'s own comment
   and in the watchdog spec, so taking it is a decision to reverse a shipped one, not a gap to fill.
3. **Bound the delivery window instead of the state.** A `DispatchTask` that has been queued longer
   than some fraction of `sendTimeout` is very likely stale; dropping it at the sender rather than
   writing it to the stream would close the common case without any protocol change. Cheapest of the
   three, and it fails in the safe direction (the task is simply not dispatched, and the dispatcher's
   requeue path handles that today).

Whichever is chosen, the log line matters as much as the fix: today the agent starting a doomed
subprocess and the agent dropping the cancel that would have stopped it are both **completely
silent**.

## Acceptance / Done When

- The chosen shape is written down with the two variants above distinguished, and the decision says
  explicitly which one it closes.
- A test seeds the state - a dispatch delivered after the assignment was ended - and asserts the
  agent does not start the subprocess (or that it is stopped without waiting for the next reconcile).
- The dropped-cancel path is no longer silent, within the budget rules: `handleCancel`'s miss is on
  the agent, which has its own logging, not inside the coordinator's `ingestLogLimiter` constraint.
- The coordinator's fences are unchanged. This item must not add or relax a status, epoch or worker
  predicate on any write path.

## Related

- Source: `internal/scheduler/dispatch.go` (`dispatchOne`'s `registry.Send` branch),
  `internal/worker/registry.go` (`Send`, `SendCancel`), `internal/worker/sender.go` (the two error
  values and the 5s `sendTimeout`), `internal/agent/agent.go` (`handleDispatch`, `handleCancel`),
  `internal/api/workers.go` (`sendCancelSignals`)
- The slice that closed the write side and established this as the remainder:
  [[bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence]],
  `docs/retros/2026-08-20-requeue-task-by-id-fence.md`
- The same missing-token shape from the other direction - `CancelTask` carries no epoch, so a late
  cancel can kill a **fresh** run of the same task id:
  [[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]] (closed), and the comment on
  `SweepOnce`'s per-row cancel in `internal/scheduler/watchdog.go`
- The mechanism that bounds how long the orphan runs: the coordinator watchdog, same closed item
- The unbounded-admission item that bounds how many concurrent streams one agent can open:
  [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]

## Notes

Filed at **low**, deliberately. It needs a wedged sender that unwedges inside the timeout **and** a
concurrent release, the coordinator's own state remains consistent throughout, the watchdog bounds the
duration, and the second variant additionally needs an agent that runs two streams at once. Against
that: the consequence is real duplicate execution of a user's task, and it is currently invisible from
both ends.

The transferable observation is the one that corrected this item's own draft: **a "message A overtakes
message B" hazard is a claim about which queues they travel through.** Two sends to the same worker
are ordered when they share a sender and unordered when they do not, and the difference is invisible
at the call site - both are `registry.Send(uuidStr(w.ID), ...)`.
