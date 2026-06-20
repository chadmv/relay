---
title: Stale stream teardown clobbers a fresh registration for the same worker
type: bug
status: closed
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

# Stale stream teardown clobbers a fresh registration for the same worker

## Summary
`Registry.Unregister` deletes by worker ID without checking which sender it removes, and the `Connect` defers (unregister, mark offline, start grace timer) run unconditionally. A half-open old connection's teardown can therefore unregister a freshly reconnected worker, mark it offline, and arm a grace timer the new connection never cancels. After the grace window, tasks the agent is actively running get requeued and can be claimed by another worker, producing duplicate execution.

## Repro / Symptoms
1. Agent's connection black-holes (NAT timeout); no gRPC keepalive is configured (`cmd/relay-server/main.go:176`), so the old `Connect` handler stays alive for minutes.
2. Agent reconnects; `finishRegister` cancels grace and registers the new sender.
3. Old stream's `Recv` errors; its defers remove the new sender, flip the live worker to offline, and start a grace timer.
4. Grace expires; `RequeueWorkerTasks` requeues tasks still running on the connected agent.

## Proposal
- Add `Registry.UnregisterIf(workerID, sender)` that only deletes when the registered sender matches.
- In `Connect`, make the offline/grace teardown conditional on `UnregisterIf` returning true (a newer connection owns the worker otherwise).
- Configure `keepalive.ServerParameters` (e.g. Time 30s, Timeout 10s) so half-open streams die quickly.
- Add a registry test covering replace-then-stale-teardown.

## Related
- `internal/worker/registry.go:34` (`Unregister`)
- `internal/worker/handler.go:105-112` (Connect defers)
- `cmd/relay-server/main.go:176` (no keepalive)
