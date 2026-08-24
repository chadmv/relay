---
title: A registration that fails after RegisterWorkerConnection leaves the worker online with grace cancelled and no teardown
type: bug
status: closed
created: 2026-08-23
priority: medium
closed: 2026-08-24
resolution: fixed
source: 2026-08-23 deep roadmap refresh - backend invariants lens finding N2, rated HIGH by the reviewer; filed at medium pending independent reproduction
---

# A registration that fails after RegisterWorkerConnection leaves the worker online with grace cancelled and no teardown

## Summary
`Connect` arms `defer h.teardownConnection(workerID, sender)` only after `authenticateAndRegister`
returns successfully (`internal/worker/handler.go:254-256`), but by then `finishRegister`
(`:565-624`) has already run `RegisterWorkerConnection` - which sets `status = 'online'`, bumps
`connection_epoch` and clears `disconnected_at` - and then `h.grace.Cancel(workerID)` at `:579`,
discarding the pending requeue timer from the previous disconnect. Two later statements can still
fail: `reconcileRunningTasks` (`:583`) and the `RegisterResponse` `stream.Send` (`:594`). On either
path no sender is registered and no teardown ever runs: `markWorkerOffline` is never called, no
grace timer is re-armed, and the worker sits `online` in `GET /v1/workers` with tasks assigned to a
connection that does not exist.

## Context
This is CLAUDE.md Invariant 1 read backwards: state was acquired before the release was made
unconditional. The code itself names the failing path without asking what state it leaves behind -
the wake-gate comment at `:757-764` says "the RegisterResponse send fails and `finishRegister`
returns early". The only backstop today is the coordinator stale-task watchdog at
`RELAY_TASK_MAX_ASSIGNMENT` (default 24h), so the practical effect is a worker that appears healthy
for up to a day while its tasks are stranded.

Found by the 2026-08-23 deep roadmap review (backend lens), which rated it HIGH; it has not been
independently reproduced, which is why it is filed at medium - the reproduction is the first
acceptance step.

## Repro / Symptoms
Not yet reproduced. Expected repro shape: make `reconcileRunningTasks` or the `RegisterResponse`
send fail during a reconnect (e.g. kill the stream between `RegisterWorkerConnection` and the
response send, or force a store error in reconcile), then observe the worker remains `online`, its
grace timer is cancelled, and no requeue happens until the assignment watchdog fires.

## Proposal
Arm the teardown at the point `RegisterWorkerConnection` succeeds, not at the point
`finishRegister` returns - the defer must become unconditional the moment state is acquired. The
identity-checked-teardown invariant still applies: the teardown must verify ownership before
tearing down, so a failed registration's cleanup cannot clobber a concurrent successful one.

## Acceptance / Done When
- A test reproduces the strand: registration fails after `RegisterWorkerConnection`, and at HEAD
  the worker stays `online` with no grace timer (RED), while after the fix it is marked offline (or
  its grace timer is armed) without waiting for the assignment watchdog.
- The teardown remains identity-checked - a stale registration failure cannot tear down a fresh
  successful registration (the existing `connEpoch` fencing must still hold).
- The wake-gate comment at `internal/worker/handler.go:757-764` is re-read against the new
  behavior and corrected if its description of the early-return path changed.

## Related
- `internal/worker/handler.go` - `Connect` (`:241`), `finishRegister` (`:565-624`), `teardownConnection` (`:1309-1324`)
- CLAUDE.md Invariants: "End the generation before releasing the resource", "Identity-checked teardown"
- [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]] - different defect in the same registration window (log budget allocation order), do not merge
- [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]] - the watchdog that is currently the only backstop

## Resolution

Fixed 2026-08-24 (finishregister-strand).

`finishRegister` now arms a deferred release of the worker generation in the same breath as
`RegisterWorkerConnection` acquires it, guarded by a `handedOff` flag flipped immediately after
`registry.Register`. The two releases partition the window rather than overlapping it: `finishRegister`'s
covers acquisition-to-handoff, `Connect`'s `teardownConnection` covers handoff-onward. The shared body
is extracted as `releaseWorkerGeneration(workerID, epoch)` so the two paths cannot drift.

Ownership on the new path is the epoch alone - `MarkWorkerOfflineIfEpoch`'s `connection_epoch = $4`,
compared against this call's `updated.ConnectionEpoch`. There is deliberately **no** registry gate: a
failed registration has no sender to identity-check, and unregistering one it never registered would
be the clobber the identity-checked-teardown invariant forbids.

Both acceptance criteria on reproduction are met, and the third (re-read the wake-gate comment) is
done - that sentence turned out still literally true, so it gained a paragraph rather than a rewrite.

### The item understated the damage, three ways

The plan refuted six of its supporting claims. The material ones:

- The 24h assignment watchdog **marks tasks `timed_out` rather than requeueing them**, so the work
  failed a day later instead of being re-run.
- The watchdog **never writes `workers.status`**, so for the worker row there was no backstop at all -
  not even at 24h.
- **The metrics liveness sweeper, the one mechanism that would have noticed, was disabled by the same
  missing line**: it skips any worker `Metrics` is not tracking, `Metrics.Activate` sits below both
  failure points, and the previous disconnect had already cleared the entry. It walked past the
  stranded worker by construction.

### What verification added, beyond the item's scope

- **A database fault made the fix itself a silent no-op.** `markWorkerOffline` returned 0 for three
  distinct causes - unparseable id, query error, and the genuine fence miss - and the caller read all
  three as "superseded". Since the reconcile arm fails either on a cancelled peer context *or* a
  database fault, the strand was re-created in one of its own two trigger scenarios. Reproduced live
  against real Postgres. It now returns `(int64, error)` and **proceeds** on an error: both
  continuations are independently epoch-fenced, so a genuinely-superseded release costs a fenced
  no-op, where before it cost a permanent strand.
- **`GraceRegistry` was not epoch-monotonic.** A delayed superseded release could `Stop()` and replace
  a live generation's timer with a stale one, whose own fence then matched zero rows - so that
  worker's tasks were never requeued. Fixed here rather than filed, since this slice's own comment
  had asserted the opposite.
- **The one line that decides which release owns the generation had no CI enforcement.** Deleting
  `handedOff = true` left all 21 packages green, and that mutant makes every *successful* registration
  mark its own worker offline and requeue a healthy agent's tasks. The cause is structural: every test
  in `internal/worker` that drives a successful registration is `//go:build integration`, and CI runs
  no tags. Closed with a parser-level guard that pins the flip's position and the release closure's
  shape. That guard was itself evaded twice during review before it held.

Known limitation, filed rather than fixed:
[[bug-2026-08-24-crash-looping-agent-defers-requeue-indefinitely]] - an agent failing faster than
`RELAY_WORKER_GRACE_WINDOW` gets Cancel-then-Start every cycle, so the requeue is deferred
indefinitely. Not a regression; the pre-fix outcome was identical and worse. This slice closes the
single-shot case.
