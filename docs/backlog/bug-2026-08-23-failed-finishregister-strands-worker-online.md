---
title: A registration that fails after RegisterWorkerConnection leaves the worker online with grace cancelled and no teardown
type: bug
status: open
created: 2026-08-23
priority: medium
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
