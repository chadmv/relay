---
title: Cancel/disable handlers send synchronously to workers
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-21
resolution: fixed
priority: low
source: surfaced by the 2026-06-10 wedged-agent-halts-dispatch fix
---

# Cancel/disable handlers send synchronously to workers

## Summary
The cancel and disable HTTP handlers loop over a job's running tasks and call `registry.Send` synchronously. After the bounded-send fix (`ErrSendTimeout`, ~5s), a multi-task job whose tasks all sit on one wedged worker can take up to N x ~5s in the request path before returning. It is bounded (previously unbounded) and runs after the DB commit so state is durable, but a busy cancel is not instant.

## Repro / Symptoms
1. A job has N running tasks all assigned to the same worker.
2. That worker's gRPC stream wedges (peer stops reading).
3. Cancel (or disable) the job/worker.
4. Each `registry.Send` blocks ~5s (`sendTimeout`) before returning `ErrSendTimeout`; the handler returns only after all N sends, so worst case is ~N x 5s.

## Proposal
Dispatch the per-task sends concurrently, or fire-and-forget them, since they are best-effort signals and the worker's eventual disconnect handles cleanup regardless.

## Related
- `internal/api/jobs.go` (~755, cancel)
- `internal/api/workers.go` (~502, disable)
- `internal/worker/sender.go` (`sendTimeout`, `ErrSendTimeout`)
- `docs/retros/2026-06-10-wedged-agent-dispatch-fix.md`
- `docs/superpowers/specs/2026-06-10-wedged-agent-halts-dispatch-design.md`

## Resolution
Fixed 2026-06-21 (autopilot batch, item cancel-disable-handlers-send-synchronously). Extracted a
shared `(*Server).sendCancelSignals` helper (`internal/api/cancel_signals.go`) that dispatches the
best-effort `CancelTask` signals concurrently via a `sync.WaitGroup` and waits for all, bounding the
cancel/disable handlers to ~one send timeout instead of N x it. Both call sites (`handleCancelJob`,
`handleDisableWorker`) now build a `[]cancelSignal` and call the helper. Behavior otherwise unchanged
(same messages, same Force values, return still ignored). New unit test
`TestSendCancelSignals_FanOutIsConcurrent` proven RED against a sequential helper and GREEN after the
fan-out; adversarial code review re-verified `registry.Send`/`workerSender.Send` concurrency safety.
