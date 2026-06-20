---
title: No regression test bounds default cancel / Abandon() under a wedged sendCh
type: idea
status: open
created: 2026-06-20
priority: low
source: deferred open question in 2026-06-19-forced-cancel-send-backpressure-design.md
---

# No regression test bounds default cancel / Abandon() under a wedged sendCh

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

## Acceptance / Done When
- A `//go:build !windows` test floods an undrained cap-`sendCh`, starts a
  long-running subprocess, then `Cancel(false)`; asserts `Run` returns within a
  bound comfortably above `WaitDelay` (proving it is `WaitDelay`-bounded, not
  unbounded), and reports a terminal status.
- A sibling test exercises `Abandon()` under the same backpressure (asserting
  bounded return; terminal status is suppressed by `abandoned`, so assert the
  return bound, not a FAILED).
- Both are observed red-vs-green construction on Linux/Docker (a deliberately
  unbounded variant must fail the bound), per
  `feedback-platform-gated-test-verification`.
- No change to runtime behavior of the default/Abandon paths - this is
  test-only coverage of the existing accepted bound.

## Related
- `internal/agent/runner.go` - `chunkWriter.Write` / `sendOrAbort` (forced path
  only), `Cancel(force bool)`, `Abandon()`, `sendFinalStatus`.
- `internal/agent/runner_cancel_test.go` - existing forced-path tests to mirror.
- `docs/superpowers/specs/2026-06-19-forced-cancel-send-backpressure-design.md` -
  "Risks and open questions" (deferred: default cancel and `Abandon()` under a
  wedged channel remain `WaitDelay`-bounded and out of scope there).
- `docs/retros/2026-06-20-forced-cancel-send-backpressure.md`
