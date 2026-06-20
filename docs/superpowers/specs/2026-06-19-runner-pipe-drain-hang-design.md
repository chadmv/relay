# Runner pipe-drain hang fix - design

- **Date:** 2026-06-19
- **Status:** approved
- **Backlog item:** [bug-2026-06-10-agent-pipe-drain-hang.md](../../backlog/bug-2026-06-10-agent-pipe-drain-hang.md)

## Problem

In `internal/agent/runner.go`, the per-step exec loop reads the subprocess
output through `cmd.StdoutPipe()` / `cmd.StderrPipe()` and drains them with two
`pipeLog` goroutines. The loop blocks on `wg.Wait()` (both readers reaching EOF)
**before** it calls `cmd.Wait()`:

```go
stdout, _ := cmd.StdoutPipe()
stderr, _ := cmd.StderrPipe()
cmd.Start()
// ... two pipeLog goroutines ...
wg.Wait()        // <-- blocks here
waitErr := cmd.Wait()
```

`cmd.WaitDelay = 5s` is set, but it never engages. Go's `os/exec` force-closes
the pipe read ends only inside `Wait` (never reached while the readers block) or
in the `WaitDelay` path - and that path only fires when exec owns copy
goroutines. With `StdoutPipe`/`StderrPipe` there are none (the caller owns the
read end), so `WaitDelay` is dead code.

Consequence: a command that exits normally while a background grandchild still
holds the inherited stdout/stderr write end (the classic
[go.dev/issue/23019](https://go.dev/issue/23019) pattern) blocks `pipeLog`
forever. The runner goroutine, its workspace handle, and a worker slot leak, and
the task stays `RUNNING` indefinitely. No timeout, cancel, or `WaitDelay`
rescues it.

## Fix

Assign custom `io.Writer`s to `cmd.Stdout` / `cmd.Stderr` instead of taking the
pipes ourselves. Now exec owns the OS pipes **and** the copy goroutines, so
`cmd.Wait()` enforces `WaitDelay`: after the process exits, if the copy
goroutines have not finished within 5s, `Wait` force-closes the pipe descriptors
and returns (`ErrWaitDelay` at worst). The infinite hang becomes a 5s bound.

### Components

- **New `chunkWriter` type** implementing `io.Writer`. Its `Write([]byte)` copies
  the slice (exec reuses its internal buffer between `Write` calls, so the bytes
  must be copied before they are handed to `r.send`), wraps it in a
  `TaskLogChunk` carrying `stream` / `step_index` / `step_total` / `epoch`, and
  pushes it via `r.send`. This is the body of today's `pipeLog`, restructured as
  a writer. `Write` always returns `(len(p), nil)` so exec never treats the sink
  as broken and keeps copying.

- **`Run` exec loop.** Drop `StdoutPipe`/`StderrPipe`, the `sync.WaitGroup`, and
  the two `pipeLog` goroutines. Assign `cmd.Stdout` / `cmd.Stderr =
  &chunkWriter{...}` after `setupProcTree` and before `cmd.Start()`, then call
  `cmd.Wait()` directly. Exit-code extraction, status mapping, and
  `cleanupProcTree()` are unchanged.

- **Delete dead machinery.** `stepPipes` struct field, `setStepPipes`,
  `clearStepPipes`, `closeStepPipesForForce`, and `pipeLog` all become unused and
  are removed.

- **`setupProcTree` (both platforms).** The `cmd.Cancel` callback drops its
  `if r.forced.Load() { r.closeStepPipesForForce() }` branch - it just kills the
  process group (Unix SIGKILL to `-pid`) or job (Windows `TerminateJobObject`).
  Whether `setupProcTree` still needs its `*Runner` parameter after this is a
  plan-level detail; if `r` is no longer referenced, the signature is simplified
  and the proctree tests updated to match.

### What does NOT change

- The single-bounded-sender invariant: `r.send` still selects on `r.ctx.Done()`,
  so a stalled connection cannot block the copy goroutines indefinitely.
- Epoch stamping on every `TaskLogChunk`.
- Step markers (`sendStepMarker`) - still emitted separately before each step.
- Forced-cancel of a well-behaved tree: we still SIGKILL / `TerminateJobObject`
  the whole process group/job, so the child's write end closes and exec's copy
  goroutine reaches EOF promptly.

## Behavioral changes (accepted)

| Scenario | Before | After |
|---|---|---|
| Normal exit, leaked child holds stdout | infinite hang, task stuck `RUNNING` | bounded at `WaitDelay` (5s), task reaches terminal status |
| Forced cancel, tree stays in group/job | < 2s (force-close fast-path) | < 2s (whole tree killed, write end closes) |
| Forced cancel, child escapes group/job | < 2s (force-close fast-path) | up to 5s (`WaitDelay`) |

The escaped-child forced-cancel regression (sub-2s -> up to 5s) is accepted in
exchange for deleting the `stepPipes` force-close machinery. Both paths remain
bounded; the previous behavior for the normal-exit hang was unbounded.

## Testing

- **New regression test** (the one that currently cannot pass): run a command
  that exits 0 while a background child keeps the inherited stdout open; assert
  the runner returns within roughly `WaitDelay` (i.e. does not hang) and reports
  a terminal status. Bounds the test timeout above 5s but well below "forever".
- **Existing forced-cancel tests** must still pass unchanged:
  `TestRunner_ForceCancel_ReturnsQuickly`,
  `TestRunner_ForceCancel_SkipsWorkspaceFinalize`,
  `TestRunner_DefaultCancel_RunsWorkspaceFinalize` - they kill the whole tree so
  the writer drains promptly.
- **Proctree tests** (`TestSetupProcTree_Unix_SetsPgid` and the Windows variant)
  assert `cmd.Cancel` is set and kills the process; still valid, updated only if
  the `setupProcTree` signature changes.

## Invariant check

- **One bounded sender per gRPC stream.** Writes still funnel through `r.send`,
  which is bounded on `r.ctx.Done()`. The number of concurrent writers is
  unchanged (exec runs two copy goroutines; we ran two `pipeLog` goroutines).
- **Epoch fence.** No change to `tasks.status` / `task_logs` write paths; chunks
  still carry the runner's epoch.

## Out of scope

- No change to `WaitDelay` duration (stays 5s).
- No change to step-marker emission, status mapping, or workspace finalize.
- No new force-cancel fast-path to recover the sub-2s escaped-child guarantee.
