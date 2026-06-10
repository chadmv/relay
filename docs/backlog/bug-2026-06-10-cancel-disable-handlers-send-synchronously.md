---
title: Cancel/disable handlers send synchronously to workers
type: bug
status: open
created: 2026-06-10
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
