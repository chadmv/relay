---
title: One wedged agent can halt task dispatch for the entire farm
type: bug
status: open
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

# One wedged agent can halt task dispatch for the entire farm

## Summary
`workerSender.Send` blocks until buffer space frees or the sender closes. If an agent's connection is wedged (peer not reading; no keepalive configured to detect it), the send goroutine blocks inside `stream.Send` on gRPC flow control, the 64-slot buffer fills, and the next `Registry.Send` blocks forever. The dispatch loop is a single goroutine, so one stuck send freezes dispatch farm-wide; HTTP handlers calling `Registry.Send` (job cancel, worker disable, workspace evict) hang with no timeout.

## Proposal
- Bound the wait in `workerSender.Send` (e.g. 5s timer select alongside the `done` channel) and return an `ErrSendTimeout` so the existing requeue path handles it.
- Configure `grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second})` in `cmd/relay-server/main.go` so dead peers are detected and `stream.Send` unblocks.

## Related
- `internal/worker/sender.go:52-65`
- `internal/scheduler/dispatch.go:262` (`sendTask`)
- `internal/api/jobs.go:755`, `internal/api/workers.go:502`, `internal/api/workspaces.go:79`
- Same keepalive fix as bug-2026-06-10-stale-stream-teardown-clobbers-registration
