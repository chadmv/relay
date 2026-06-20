---
date: 2026-06-19
topic: pipe-drain-hang
branch: claude/elegant-darwin-6d7747
range: ab6fed6..2ccc548
---

# Session Retro: 2026-06-19 - Runner Pipe-Drain Hang Fix

**TL;DR:** Closed `bug-2026-06-10-agent-pipe-drain-hang` by replacing the runner's `StdoutPipe`/`pipeLog`/`wg.Wait()` machinery with `io.Writer`s on `cmd.Stdout`/`cmd.Stderr` so `os/exec` owns the copy goroutines and `cmd.Wait()` enforces the 5s `WaitDelay`; the headline lesson is that the work was nearly shipped unverified because the primary test is `//go:build !windows` and `make test` on the Windows host silently skipped it - running it in a Linux Docker container proved red→green and surfaced a pre-existing force-cancel failure.

## What Was Built

The agent runner no longer hangs forever when a command exits while a background grandchild keeps the inherited stdout/stderr pipe open (go.dev/issue/23019).

- **chunkWriter + exec-owned drain** (`internal/agent/runner.go`) - dropped `cmd.StdoutPipe()`/`StderrPipe()`, the two `pipeLog` goroutines, and `wg.Wait()`. A new `chunkWriter` `io.Writer` is assigned to `cmd.Stdout`/`cmd.Stderr`; exec now owns the OS pipes and copy goroutines, so `cmd.Wait()` enforces the existing `WaitDelay = 5s`. The unbounded hang becomes a 5s upper bound. `Write` copies its slice (exec reuses the buffer), stamps stream/step/epoch, routes through `r.send`, and returns `(len(p), nil)`; an `if len(p) == 0` guard preserves the old `pipeLog` `n>0` behavior.
- **Dead-machinery deletion** - `stepPipes` struct field, `setStepPipes`/`clearStepPipes`/`closeStepPipesForForce`, and `pipeLog` removed; `io` import dropped.
- **setupProcTree signature** (`proctree_unix.go`, `proctree_windows.go`) - the `*Runner` param existed only to feed the deleted `closeStepPipesForForce` branch; dropped on both platforms, all three call sites updated.
- **Unix-gated regression test** (`runner_cancel_test.go`) - `sh -c "sleep 30 & echo done"` exits the shell immediately while a 30s child holds stdout; asserts the runner returns within a 9s in-test bound and reports a terminal status.

## Key Decisions

- **Accepted the 5s bound for forced cancel of a tree-escaping child.** The proposal deleted `closeStepPipesForForce`, which had given forced cancel a sub-2s return even when a child held the pipe. With the writer approach the common case stays fast (SIGKILL kills the whole tree, closing the write end), but a child that escapes the process group/job now waits the 5s `WaitDelay`. Surfaced as an explicit choice; user picked the simplest option (delete the machinery).
- **The backlog proposal was not a contract.** It said "deletes the entire stepPipes machinery" without noting that machinery is the heart of the May force-cancel fast-path. Caught that interaction before writing the spec, which reframed the whole task around a real tradeoff rather than a mechanical swap.
- **Fixed the red test's threshold during plan review.** The planner's first regression test used `sleep 5 &` against the 5s `WaitDelay` - both old and new code return at ~5s, so it never distinguished red from green. Corrected to `sleep 30 &` so the leaked child outlives `WaitDelay` by a wide margin (child 30s >> 9s in-test timeout >> 5s WaitDelay).
- **Applied the empty-chunk guard from review as exact parity, not new behavior.** The one low-severity review note (unconditional send vs old `n>0` guard) was restored verbatim rather than argued away - surgical, matches the deleted code's contract.

## Problems Encountered

- **Windows `make test` is blind to `//go:build !windows` tests.** The implementer ran `make test` on Windows and reported green - but the primary regression test, `TestRunner_ForceCancel_*`, and `TestSetupProcTree_Unix_*` are all Unix-gated and were silently skipped (`no tests to run`). The TDD red→green for a *hang* bug was never actually observed on the dev host. Closing the gap required running the agent package in a `golang:1.26` Docker container against the mounted worktree.
- **A pre-existing force-cancel failure only visible on Linux.** Running the full agent package on Linux, `TestRunner_ForceCancel_ReturnsQuickly` failed at 3.3s. A goroutine dump (forcing a panic at 2s) showed exec's copy goroutine parked in `chunkWriter.Write -> r.send`: the test floods an undrained cap-4096 `sendCh`, and `r.send` waits on the long-lived agent context (not a per-task forced signal), so forced cancel can't preempt it. Verified the same test fails identically on `main`, confirming it is pre-existing, not a regression. Filed as `bug-2026-06-19-forced-cancel-send-backpressure`.
- **Git Bash mangled Docker volume paths.** `-v "/d/...:/src"` became `C:/Program Files/Git/src` until prefixed with `MSYS_NO_PATHCONV=1`.

## Known Limitations

- See [`bug-2026-06-19-forced-cancel-send-backpressure`](../backlog/bug-2026-06-19-forced-cancel-send-backpressure.md) - forced cancel cannot preempt a log write blocked on a full `sendCh`, because `r.send` selects on the agent context rather than a per-task forced signal; it falls back to the 5s `WaitDelay` under send backpressure. Pre-existing (reproduces on `main`); partly defeats the 2026-05-04 force-cancel feature's promise.
- `ROADMAP.md` still lists the now-closed pipe-drain bug as open work; it is generated, so left for `/roadmap` to refresh rather than hand-edited.

## Improvement Goals

- **Run platform-gated tests on a platform that can execute them before claiming done.** A `//go:build !windows` test is invisible to `make test` on Windows - it reports green while skipping the very test that proves the fix. For a hang/concurrency bug this is the difference between "verified" and "asserted." Use a Linux Docker container (`golang:<ver>` + mounted worktree, `MSYS_NO_PATHCONV=1`) to actually observe red→green. New this session (promoted to [[feedback-platform-gated-test-verification]]).
- **A red regression test must be constructed so it provably fails on the unfixed code.** The first draft's timing collided with the `WaitDelay` it was testing, making old and new code indistinguishable. Derive thresholds so the test fails pre-fix with margin, then confirm the red before implementing. New this session.
- **A "delete the entire X machinery" proposal requires enumerating everything X does, not just the path the bug is about.** `stepPipes` looked like dead drain-plumbing but was the force-cancel fast-path. New this session; a sharper edge of the "proposal is not a contract" rule ([[feedback-backlog-proposal-not-contract]]).
- **Treat a backlog proposal as a starting point, not a contract** (carried, already promoted to [[feedback-backlog-proposal-not-contract]]). Applied: caught the force-cancel interaction the proposal omitted.
- **Trace the full lifecycle of any status/event you emit before claiming "no other changes."** (carried from prior retro). Honored: the code reviewer traced `chunkWriter`'s chunk to the server's epoch-fenced `AppendTaskLog` consumer.
- **Match commit here-string syntax to the tool's shell** (carried, already promoted to [[feedback-commit-heredoc-shell]]). Applied: bash heredocs / inline `-m` throughout.

## Files Most Touched

- `internal/agent/runner.go` - the core change: `chunkWriter`, exec-owned drain via `cmd.Wait()`, deletion of `stepPipes`/`pipeLog` machinery, empty-chunk guard.
- `internal/agent/runner_cancel_test.go` - new Unix-gated hang regression test; `setupProcTree` call updated; stale `pipeLog`/`closeStepPipesForForce` comment fixed.
- `internal/agent/proctree_unix.go` / `proctree_windows.go` - dropped the `*Runner` param and the forced-close branch from the `cmd.Cancel` callback.
- `internal/agent/runner_cancel_windows_test.go` - one-line `setupProcTree` call-site update.
- `docs/superpowers/specs/2026-06-19-runner-pipe-drain-hang-design.md` - design with the accepted-behavior tradeoff table.
- `docs/superpowers/plans/2026-06-19-runner-pipe-drain-hang.md` - 6-task TDD plan (later corrected for the red-test threshold).
