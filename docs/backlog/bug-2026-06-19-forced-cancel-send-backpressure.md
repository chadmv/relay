---
title: Forced cancel cannot preempt a log write blocked on a full sendCh
type: bug
status: open
created: 2026-06-19
priority: medium
source: verification of bug-2026-06-10-agent-pipe-drain-hang
---

# Forced cancel cannot preempt a log write blocked on a full sendCh

## Summary
A forced cancel is meant to release the worker slot and workspace in well under the 5s `WaitDelay` (that is the entire point of the 2026-05-04 force-cancel feature). But the runner's log-streaming send (`Runner.send`) selects only on `r.sendCh <-` and `<-r.ctx.Done()`, where `r.ctx` is the long-lived **agent** context, not the per-task run context. A task cancel - forced or not - does not cancel `r.ctx`. So if `sendCh` is full at cancel time (a wedged or slow coordinator connection - exactly when an operator force-cancels), the subprocess copy goroutine parks inside `send` and the forced cancel cannot preempt it. It falls back to exec's 5s `WaitDelay`.

This is **pre-existing**, not introduced by the pipe-drain fix: it reproduces identically on `main`. The old `closeStepPipesForForce` closed the *read* pipe, which also could not unblock a *send*-blocked reader. The pipe-drain fix (chunkWriter) did not change this property.

## Repro
`TestRunner_ForceCancel_ReturnsQuickly` (Unix-gated) fails at ~3.3s in a constrained Linux/Docker env: the test floods `sh -c 'while true; do echo line; done'` into a cap-4096 `sendCh` that it never drains. Once the channel fills, the copy goroutine blocks in `send`; the forced cancel cannot return under the asserted 2s, and the test's own 3s safety net fires. Verified failing on both `main` and the pipe-drain branch.

## Proposal (to design)
Give forced cancel a way to abandon in-flight log writes without waiting on `sendCh` or the agent context. Options to weigh:
- Add a per-runner `forcedCh` (closed once by `Cancel(force=true)`); have `chunkWriter.Write` select on it and return a sentinel error so exec's `io.Copy` stops and `cmd.Wait()` returns promptly. Must NOT route the final-status send through the same abort, so a forced cancel still reports terminal `FAILED`.
- Or make the test realistic (drain `sendCh`) if the team decides the full-channel forced-cancel latency (bounded at 5s) is acceptable and the <2s guarantee only needs to hold when the connection is healthy.

Pick one deliberately - the first preserves the force-cancel feature's promise under backpressure; the second accepts a 5s bound there and just unbreaks the test.

## Related
- `internal/agent/runner.go` - `Runner.send` (selects on `r.ctx.Done()`, the agent ctx), `chunkWriter.Write`, `Cancel(force bool)`.
- `internal/agent/runner_cancel_test.go` - `TestRunner_ForceCancel_ReturnsQuickly`.
- Force-cancel feature: [docs/superpowers/specs/2026-05-04-force-cancel-design.md](../superpowers/specs/2026-05-04-force-cancel-design.md).
- Discovered closing [bug-2026-06-10-agent-pipe-drain-hang.md](closed/bug-2026-06-10-agent-pipe-drain-hang.md).
