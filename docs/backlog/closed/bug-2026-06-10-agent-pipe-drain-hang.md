---
title: Agent runner pipe-drain hang - wg.Wait() before cmd.Wait() defeats WaitDelay
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-19
resolution: fixed
priority: high
source: full-codebase review (2026-06-10)
---

# Agent runner pipe-drain hang - wg.Wait() before cmd.Wait() defeats WaitDelay

## Summary
The runner blocks on its `StdoutPipe`/`StderrPipe` readers (`wg.Wait()`) before calling `cmd.Wait()`, so `cmd.WaitDelay = 5s` never engages. Verified against Go's `os/exec` internals: the pipe read ends are force-closed only inside `Wait` (never reached while readers block) or in the WaitDelay path, which is skipped when there are no exec-owned copy goroutines (and with `StdoutPipe` there are none). A command that exits leaving a background child holding the inherited stdout/stderr (the classic go.dev/issue/23019 pattern) blocks `pipeLog` forever; the runner goroutine, its workspace handle, and a slot hang indefinitely and the task stays RUNNING.

## Proposal
Drop `StdoutPipe`/`StderrPipe` and assign custom `io.Writer`s instead, letting exec's own copy goroutines plus `WaitDelay` bound the drain. This also deletes the entire `stepPipes` machinery:

```go
cmd.Stdout = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, step: step, total: stepTotal}
cmd.Stderr = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, step: step, total: stepTotal}
waitErr := cmd.Wait() // returns ErrWaitDelay at worst, 5s after exit/cancel
```

## Resolution
Replaced `cmd.StdoutPipe()`/`StderrPipe()` + the two `pipeLog` goroutines + `wg.Wait()` with a custom `io.Writer` (`chunkWriter`) assigned to `cmd.Stdout`/`cmd.Stderr`. Now `os/exec` owns the OS pipes and the copy goroutines, so `cmd.Wait()` enforces the existing `WaitDelay = 5s`: after the process exits, if a leaked child still holds the write end, `Wait` force-closes the descriptors within 5s instead of blocking forever. The unbounded hang becomes a 5s upper bound. The dead `stepPipes` force-close machinery (`setStepPipes`/`clearStepPipes`/`closeStepPipesForForce`/`pipeLog`) was deleted, and `setupProcTree` lost its now-unused `*Runner` parameter on both platforms.

Verified red→green on Linux (the regression is Unix-gated and cannot run on the Windows dev host): `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang` hangs at 9s on pre-fix code and passes at ~5s (the WaitDelay bound) after the fix.

Spec: [docs/superpowers/specs/2026-06-19-runner-pipe-drain-hang-design.md](../../superpowers/specs/2026-06-19-runner-pipe-drain-hang-design.md). Plan: [docs/superpowers/plans/2026-06-19-runner-pipe-drain-hang.md](../../superpowers/plans/2026-06-19-runner-pipe-drain-hang.md).

Follow-up surfaced during verification: forced cancel cannot preempt a log write blocked on a full `sendCh` (`r.send` waits on the long-lived agent context, not a per-task forced signal), so forced cancel can fall back to the 5s `WaitDelay` under send backpressure. This is pre-existing (reproduces on `main`) and tracked separately in [bug-2026-06-19-forced-cancel-send-backpressure.md](../bug-2026-06-19-forced-cancel-send-backpressure.md).

## Related
- `internal/agent/runner.go:191` (WaitDelay comment), `:216-222` (wg.Wait before cmd.Wait)
- go.dev/issue/23019
