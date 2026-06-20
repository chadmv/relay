---
title: Stale teardown can still clobber during the finishRegister gap (needs worker connection-epoch fence)
type: bug
status: open
created: 2026-06-19
priority: medium
source: relay-verify review of bug-2026-06-10-stale-stream-teardown fix
---

# Stale teardown can still clobber during the finishRegister gap

## Summary
The identity-checked teardown fix (`UnregisterIf` + `teardownConnection`, closed
as `bug-2026-06-10-stale-stream-teardown-clobbers-registration`) closes the
common case but leaves a narrow residual race. `finishRegister` establishes
registry ownership (`registry.Register`) *last*, after it has already written
`status=online` and called `grace.Cancel`. A stale connection's teardown can pass
the ownership gate before the fresh connection registers, then land its
`markWorkerOffline` / `grace.Start` writes after the fresh connection's
online/cancel - flipping a live, connected worker offline and re-arming a grace
timer that requeues tasks the agent is actively running (duplicate execution).

The gate's *check* (`UnregisterIf`) and its *action* (`markWorkerOffline` /
grace) are not atomic with respect to the fresh registration, so reordering
`Register` earlier alone does not fix it.

## Repro / Symptoms
1. Agent's old stream is half-open; the agent reconnects (fresh connection F).
2. F runs `finishRegister`: writes `status=online` (handler.go:301), calls
   `grace.Cancel` (handler.go:314), but has not yet reached
   `registry.Register` (handler.go:344).
3. The old stream's `Recv` errors; stale teardown S runs `UnregisterIf` while S
   is still the registered sender -> `owned=true`.
4. S's `markWorkerOffline` and `grace.Start` execute after F's online/cancel.
5. F registers. Final state: registry correct, but DB says offline and a grace
   timer is armed against the live worker; grace expiry requeues its running
   tasks.

## Proposal
Mirror the existing task `assignment_epoch` fence at the worker-connection level:
- Add a `connection_epoch` (or similar) column to `workers`.
- Bump it on each successful `finishRegister`; the connection records the epoch
  it owns.
- Fence `markWorkerOffline` and the grace/requeue teardown on that epoch via a
  conditional `UPDATE ... WHERE connection_epoch = $N`, so a stale connection's
  writes no-op once a newer connection has registered.

This is DB-enforced and in-pattern with the project's epoch-fence invariant.
Needs a migration, new/changed sqlc queries, and threading the epoch through the
teardown path - its own spec/plan/verify cycle.

## Related
- `internal/worker/handler.go` `finishRegister` (online-write + grace.Cancel
  precede `registry.Register`) and `teardownConnection` (the ownership gate).
- `internal/worker/registry.go` `UnregisterIf`.
- CLAUDE.md invariants: "Identity-checked teardown", "Epoch fence".
- Closed predecessor: `docs/backlog/closed/bug-2026-06-10-stale-stream-teardown-clobbers-registration.md`.
