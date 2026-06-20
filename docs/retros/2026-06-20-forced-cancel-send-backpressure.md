---
date: 2026-06-20
topic: forced-cancel-send-backpressure
branch: 2026-06-20 / forced-cancel-send-backpressure
range: 61e0513..a317f9a
---

# Session Retro: 2026-06-20 - Forced Cancel Preempts a Send Blocked on a Full sendCh

**TL;DR:** Closed `bug-2026-06-19-forced-cancel-send-backpressure` so a forced
cancel frees the worker slot in well under exec's 5s `WaitDelay` even when the
gRPC `sendCh` is wedged full. A per-runner `forcedCh` (closed once via
`forced.CompareAndSwap`) lets the log-write path abandon a parked send and return
a sentinel that stops exec's `io.Copy`; the forced-path terminal `FAILED` send is
bounded so `Run` returns promptly. The headline lesson: the plan specified the
terminal send as a two-case `select { case sendCh <- msg: case <-forcedCh: }`,
but `forcedCh` is *already closed* by the time `sendFinalStatus` runs, so that
select had two ready arms and Go's uniform-random choice dropped the terminal
FAILED ~50% of the time even with channel headroom. Corrected to a non-blocking
try-send (`select { case sendCh <- msg: default: }`). Caught only because the
guard test ran in a Linux container - Windows `make test` silently skips these
`//go:build !windows` tests.

## What Was Built

A forced cancel (`relay cancel <job> --force`) now returns in well under 2s even
when the agent's gRPC `sendCh` is full and the coordinator connection is wedged -
the exact condition force-cancel exists to handle. Previously the subprocess copy
goroutine parked inside the log send (which selects only on the long-lived agent
context, not a per-task signal) and the forced cancel fell back to the 5s
`WaitDelay`.

- **`forcedCh` forced-abort signal** (`internal/agent/runner.go`) - a per-runner
  `forcedCh chan struct{}`, allocated in `newRunner`, closed exactly once on the
  `force == true` path of `Cancel` via `r.forced.CompareAndSwap(false, true)` so
  repeated or mixed forced/non-forced/`Abandon()` cancels cannot double-close
  (which panics).
- **`sendOrAbort` + `errForcedAbort`** - a dedicated three-case send helper used
  only by `chunkWriter.Write` (the path exec's `io.Copy` drives and that gates
  `cmd.Wait`): it selects on `sendCh <-`, `ctx.Done()`, and `forcedCh`, and on
  abort `Write` returns the package sentinel `errForcedAbort` so `io.Copy` stops
  and `cmd.Wait()` returns promptly. The shared `r.send` (step markers, prepare
  progress, inventory) is untouched - the abort is introduced only on the path
  that pins `cmd.Wait`.
- **Bounded forced-path terminal send** (`sendFinalStatus`) - on the forced path
  the terminal `FAILED` enqueue is a non-blocking try-send so `Run` returns even
  when `sendCh` is wedged; the non-forced path keeps the blocking `r.send`.
- **Guard test** (`internal/agent/runner_cancel_test.go`) -
  `TestRunner_ForceCancel_StillSendsTerminalFailed`: a forced cancel on a roomy
  `sendCh` must still report terminal `FAILED` (spec Fact 1). The pre-existing
  `TestRunner_ForceCancel_ReturnsQuickly` is the red->green repro for the latency.

## Key Decisions

- **Forced-path terminal send is a non-blocking try-send, not a closed-channel
  select.** The plan prescribed `select { case sendCh <- msg: case <-forcedCh: }`
  to bound the terminal send. That is wrong precisely because `forcedCh` is
  *already closed* when `sendFinalStatus` runs on the forced path: a receive from
  a closed channel is *always ready*, so Go's uniform-random selection between two
  ready cases dropped the terminal FAILED about half the time even when `sendCh`
  had room. The correct shape is `select { case sendCh <- msg: default: }`: it
  *prefers* delivery whenever the channel has headroom and abandons only on a
  genuinely full channel. The intent "succeed if possible, else abandon" must be
  expressed with `default`, never gated on an already-closed-channel arm.
- **The non-forced terminal path was deliberately left blocking** (`r.send`).
  Dropping a terminal status is only safe on a forced cancel, where the server
  has already run `CancelJobTasks` (sets `failed` + bumps `assignment_epoch`)
  before the gRPC cancel, so a stale agent terminal message is epoch-fenced out
  server-side. A default cancel or normal completion has no such authoritative
  server-side transition, so it must never best-effort-drop the terminal status -
  hence the bound is forced-path-only and the normal path is byte-for-byte
  unchanged.
- **Abort lives only on `chunkWriter.Write`, via a dedicated `sendOrAbort`.** The
  shared `r.send` was left untouched so step markers / prepare progress /
  inventory keep their blocking discipline, and the one-bounded-sender invariant
  is preserved (no new writer; all writes still go through `r.sendCh`). The change
  only *reduces* how long a runner blocks on a non-reading peer.

## Problems Encountered

- **A closed-channel select arm made terminal delivery a coin flip.** After Tasks
  2-3 landed, the new `TestRunner_ForceCancel_StillSendsTerminalFailed` failed
  intermittently *in the Linux container*. Systematic debugging root-caused it to
  the plan's two-case terminal select: by the time `sendFinalStatus` runs on the
  forced path, `forcedCh` is closed, so `<-r.forcedCh` is permanently ready;
  paired with a `sendCh <-` that also had room, `select` chose between two ready
  arms uniformly at random and abandoned the FAILED ~50% of the time. The fix was
  a non-blocking try-send with `default`. Lesson, worth recording verbatim: **a
  `select` arm that reads from an already-closed channel is always ready, so
  pairing it with a real send turns delivery into a 50/50 gamble. For "deliver if
  possible, otherwise abandon," use `default`, not a closed-channel case.** The
  plan's prescription looked symmetric and correct on paper and still shipped a
  silent 50% drop - the executing engineer must validate concurrency primitives
  against the *runtime channel state*, not just transcribe the plan.
- **Windows `make test` would have hidden the 50% drop entirely.** The repro and
  guard tests are `//go:build !windows`; on the Windows host `make test` reports
  green while skipping them. The flake was only observable by running the
  `internal/agent` package in a `golang:1.26.2` Docker container against the
  mounted worktree (`MSYS_NO_PATHCONV=1` for the volume path). This reinforces the
  standing rule (`feedback-platform-gated-test-verification`): a `//go:build
  !windows` test must be observed on a platform that can execute it, and stability
  must be confirmed with `-count=N` / `-race` - a single green run would not have
  surfaced a 50%-probability drop.

## Improvement Goals

- **Treat the plan's concurrency primitives as proposals to validate, not gospel
  to transcribe.** The two-case select was specified in the plan and still wrong.
  When implementing a `select`, reason explicitly about which arms can be ready
  simultaneously at the moment the code runs (especially closed channels and
  already-cancelled contexts) before accepting the plan's shape. New this session.
- **For "best-effort enqueue, else abandon," reach for `default`, not a
  signal-channel arm.** Closed/cancelled signal channels are always-ready and will
  steal a real send under `select`'s random choice. New this session; a concrete
  sharpening of correct non-blocking-send idiom.
- **Confirm probabilistic correctness with repetition, not a single pass.** A
  50%-drop bug passes a single run half the time; `-count=10` (used here) and
  `-race` are the floor for any concurrency guard test. Carried and applied.
- **Run platform-gated tests on a platform that can execute them**
  (`feedback-platform-gated-test-verification`). Carried; load-bearing again -
  this is the second consecutive `internal/agent` session where Windows `make
  test` would have shipped a broken concurrency fix.

## Files Most Touched

- `internal/agent/runner.go` - `forcedCh` field + close-once `Cancel`,
  `errForcedAbort` sentinel, `sendOrAbort` three-case helper, abort-aware
  `chunkWriter.Write`, bounded forced-path `sendFinalStatus` (non-blocking
  try-send).
- `internal/agent/runner_cancel_test.go` - new
  `TestRunner_ForceCancel_StillSendsTerminalFailed` guard; the repro
  `TestRunner_ForceCancel_ReturnsQuickly` was unchanged.
- `docs/superpowers/specs/2026-06-19-forced-cancel-send-backpressure-design.md` -
  design (Option 1; "How terminal FAILED still sends").
- `docs/superpowers/plans/2026-06-19-forced-cancel-send-backpressure.md` - 6-task
  TDD plan (its Task 4 Step 3 prescribed the buggy two-case terminal select).
- `docs/backlog/closed/bug-2026-06-19-forced-cancel-send-backpressure.md` -
  closed with a Resolution section.
