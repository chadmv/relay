---
title: Agent runner pipe-drain hang - wg.Wait() before cmd.Wait() defeats WaitDelay
type: bug
status: open
created: 2026-06-10
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

## Related
- `internal/agent/runner.go:191` (WaitDelay comment), `:216-222` (wg.Wait before cmd.Wait)
- go.dev/issue/23019
