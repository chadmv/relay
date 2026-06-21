---
title: Default cancel / Abandon() hang unbounded under a wedged sendCh
type: bug
status: open
created: 2026-06-20
priority: medium
source: deferred open question in 2026-06-19-forced-cancel-send-backpressure-design.md; premise corrected 2026-06-21 (autopilot)
---

# Default cancel / Abandon() hang unbounded under a wedged sendCh

## Premise correction (2026-06-21, autopilot)
This started as a TEST-ONLY item asserting the default-cancel / `Abandon()` paths stay `WaitDelay`-bounded
under a wedged `sendCh`. Attempting the test on Linux/Docker (with production `WaitDelay = 5s` unchanged)
**disproved that premise**: both paths hang **unbounded**, not `WaitDelay`-bounded. So this is a latent
**bug**, not a missing test. It is re-typed from idea->bug; the regression test is now part of the fix's
done-criteria, not the whole task.

### Root cause
`chunkWriter.Write -> sendOrAbort` (and `sendFinalStatus -> send`) park on `r.sendCh <- msg` with only two
escapes: `<-r.ctx.Done()` and `<-r.forcedCh`. Two facts make the default/Abandon paths unbounded:
1. **`r.ctx` is the parent/root context, not the per-run context.** `newRunner` (`internal/agent/runner.go`
   ~44) stores `ctx: parent` and `cancel:` the cancel func for the *derived* `runCtx`. `Cancel(false)` and
   `Abandon()` call `r.cancel()`, which cancels `runCtx` (killing the subprocess) but leaves `r.ctx`
   (parent) un-done. So the `<-r.ctx.Done()` escape never fires on a per-task cancel - only on agent
   shutdown.
2. **`WaitDelay` cannot unblock a channel-send-parked goroutine.** Go's `os/exec` `WaitDelay` fires by
   `closeDescriptors(parentIOPipes)` then *waits* on the copy goroutines (`<-c.goroutineErr`). A goroutine
   parked on `r.sendCh <- msg` is not blocked on the pipe, so closing pipe fds never unblocks it;
   `cmd.Wait()` blocks forever.

The forced path escapes only because `Cancel(true)` closes `forcedCh`, which `sendOrAbort`'s third select
case honors. The default/Abandon paths have no equivalent.

### Reproduction (Linux/Docker)
Cap-1 undrained `sendCh`; subprocess `sh -c 'while true; do echo x; done'`; wait until `len(sendCh) ==
cap(sendCh)` (a copy goroutine is parked on the send); call `r.Cancel(false)`. `Run` does not return - a
30s probe reports "STILL HUNG after 30s (unbounded)". Same for `r.Abandon()`.

### Impact / severity (why medium, not high)
Requires a worker stream whose server-side reader has stalled (wedged `sendCh`) AND a still-producing task
that is then default-cancelled or grace-Abandoned. The server stays authoritative (epoch fencing already
fenced the task), so task *state* is correct; the damage is an agent-side leak: the `Run` goroutine and the
subprocess `Wait` never return, so the task slot is not freed and no terminal status is sent. Bounded blast
radius, but a real unbounded hang in the exact `internal/agent` send/drain area that has already regressed
twice this month.

## Original summary (test gap - now subsumed by the fix)

## Summary
The 2026-06-20 forced-cancel-send-backpressure fix gave the *forced* path a
preemptible abort (`forcedCh`) and a latency regression test
(`TestRunner_ForceCancel_ReturnsQuickly`). The *default* (non-forced) cancel and
`Abandon()` (grace-expiry requeue) paths deliberately did NOT get the abort: a
copy goroutine parked on a full `sendCh` there is bounded only by exec's 5s
`WaitDelay`. That bound is in-spec accepted behavior - this item is NOT a request
to change it. The gap is that nothing *tests* it: there is no regression test
asserting that a default cancel or an `Abandon()` of a still-producing task whose
`sendCh` is wedged full returns within `WaitDelay` rather than hanging unbounded.
A future change to the send/drain discipline (as already happened twice in
`internal/agent` this month) could silently reintroduce an unbounded hang on
these paths and `make test` would stay green.

## Why it is worth a test
The forced path is the one that got hardened and tested; the default/Abandon
paths share the same `chunkWriter.Write -> sendCh` choke point but rely entirely
on `WaitDelay` as the backstop, untested. The pipe-drain and forced-cancel
sessions both showed that an unbounded-park bug in this exact area is easy to
introduce and invisible to Windows `make test`. A `//go:build !windows`
regression test that floods an undrained `sendCh`, then issues a default
`Cancel(false)` (and a sibling for `Abandon()`), asserting the runner returns
within a margin above `WaitDelay` (e.g. < ~8s against the 5s bound), pins the
contract so a regression is loud.

## Proposal (needs design - spec -> plan -> implement, not a quick win)
Give the default-cancel and `Abandon()` paths a preemptible escape from a wedged-`sendCh` park, so a
cancelled/abandoned still-producing task's `Run` returns within a real bound. Candidate approaches (to be
weighed in design):
- Route `chunkWriter.Write` / `sendFinalStatus` through a select that also honors a per-task cancel
  signal that default cancel and Abandon close (a `cancelledCh` analogous to `forcedCh`), so the parked
  send abandons. (Note the design risk: the default path currently drains/finalizes the workspace and
  reports a terminal FAILED; an escape must not drop the terminal status when `sendCh` has headroom -
  mirror the forced path's "try-send first, abandon only when genuinely full" discipline.)
- Or make `sendOrAbort`'s escape select on the *run* context rather than the parent, so `r.cancel()`
  frees parked sends. (Verify this does not change normal-completion drain semantics.)
The bound, once it exists, must be regression-tested (below). Decide the approach in design before coding.

## Acceptance / Done When
- The default `Cancel(false)` and `Abandon()` paths return within a real bound (not the parent-context /
  agent-shutdown timescale) when `sendCh` is wedged full while the subprocess still produces output.
- A `//go:build !windows` regression test floods an undrained cap-`sendCh`, starts a continuous-output
  subprocess, then `Cancel(false)`; asserts `Run` returns within the new bound and reports a terminal
  status. A sibling test exercises `Abandon()` (assert the return bound; terminal status is suppressed by
  `abandoned`).
- Observed red-vs-green on Linux/Docker: reverting the fix must make the bound assertion FAIL (it does
  today - the paths hang past 30s), per `feedback-platform-gated-test-verification`. This is the natural
  RED proof now that the bug is confirmed.

## Related
- `internal/agent/runner.go` - `chunkWriter.Write` / `sendOrAbort` (forced path
  only), `Cancel(force bool)`, `Abandon()`, `sendFinalStatus`.
- `internal/agent/runner_cancel_test.go` - existing forced-path tests to mirror.
- `docs/superpowers/specs/2026-06-19-forced-cancel-send-backpressure-design.md` -
  "Risks and open questions" (deferred: default cancel and `Abandon()` under a
  wedged channel remain `WaitDelay`-bounded and out of scope there).
- `docs/retros/2026-06-20-forced-cancel-send-backpressure.md`
