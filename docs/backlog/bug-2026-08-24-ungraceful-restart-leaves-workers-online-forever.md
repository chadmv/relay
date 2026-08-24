---
title: After an ungraceful relay-server restart, every worker that was online stays online forever
type: bug
status: open
created: 2026-08-24
priority: medium
source: 2026-08-24 finishregister-strand retro; verified by enumerating every production writer of workers.status
---

# After an ungraceful restart, every worker that was online stays online forever

## Summary

Exactly two production sites move `workers.status` off online:

- `MarkWorkerOfflineIfEpoch`, reachable only from `releaseWorkerGeneration`, which requires a live
  connection teardown at a matching `connection_epoch`; and
- `SetWorkerStatus`, called by the metrics liveness sweeper (`internal/metrics/sweep.go:90`), which
  **skips any worker the in-memory Metrics store is not tracking**.

After an ungraceful restart (SIGKILL, OOM, host reboot) neither can fire for a worker that was online
before the crash: there is no connection to tear down, and the new process's Metrics map is empty, so
the sweeper walks straight past it. `seedGraceTimersFromActiveTasks` runs at startup and arms grace
timers, but writes no `workers.status` at all.

So `GET /v1/workers` reports a fleet of healthy agents connected to nothing, until each agent happens
to reconnect. There is no bound.

## Repro / Symptoms

1. Connect an agent; confirm its status is online.
2. SIGKILL relay-server (not a graceful shutdown).
3. Restart it, and do **not** restart the agent.
4. `GET /v1/workers` still reports the worker online, indefinitely.

The tasks are handled - `seedGraceTimersFromActiveTasks` requeues them - but the **worker row** is not.

## Context

Surfaced while reviewing the 2026-08-24 finishregister-strand slice, whose plan established that the
stale-task watchdog never writes `workers.status` either. That slice closed the case where a failed
registration stranded the row; this is the same end state reached by a different route, and the fix
there does not help, because it needs a live `finishRegister` to have run.

## Proposal

Reconcile `workers.status` at startup, where the grace timers are already seeded. The natural shape is
a single statement marking every worker offline whose `connection_epoch` has no live connection in this
process - which at startup is all of them, since the registry is empty.

**Ordering matters and the remedy must state it.** The reconcile has to run before the gRPC listener
accepts, or an agent reconnecting during startup can be marked offline immediately after it registers.
It also must not disturb `seedGraceTimersFromActiveTasks`, which reads `disconnected_at` and
`connection_epoch` to decide each timer's remaining window.

## Acceptance / Done When

- An integration test kills and restarts the server with a worker online and asserts the row is offline
  without the agent reconnecting.
- A reconnect racing startup is not marked offline after it registers.
- Grace-timer seeding behaviour is unchanged.

## Related

- `cmd/relay-server/main.go` - `seedGraceTimersFromActiveTasks`
- `internal/metrics/sweep.go` - the sweeper that skips untracked workers
- `internal/worker/handler.go` - `markWorkerOffline` / `releaseWorkerGeneration`
- [[bug-2026-08-23-failed-finishregister-strands-worker-online]] - the same end state by another route
