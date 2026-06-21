---
date: 2026-06-21
topic: cancel-disable-handlers-send-synchronously
branch: claude/sad-feistel-4bc73c
pr: "2026-06-21 / cancel-disable-handlers-send-synchronously"
merge: "2026-06-21 / cancel-disable-handlers-send-synchronously"
---

# Session Retro: 2026-06-21 - Cancel/disable handlers send synchronously

**TL;DR:** Closed `bug-2026-06-10-cancel-disable-handlers-send-synchronously`. The cancel
(`handleCancelJob`) and disable (`handleDisableWorker`) handlers looped over a job's/worker's
running tasks and called `registry.Send` sequentially after the DB commit. With the bounded ~5s
send timeout, N tasks all on one wedged worker cost up to N x 5s in the request path. Extracted a
shared `(*Server).sendCancelSignals` helper that fans the best-effort `CancelTask` signals out
concurrently via a `WaitGroup`, bounding the caller to ~one send timeout. Autopilot batch item 1.

## What Was Built

- `internal/api/cancel_signals.go` (new) - `cancelSignal` struct + `(*Server).sendCancelSignals`,
  which spawns one goroutine per send (each calling `s.registry.Send`, return value still ignored -
  best-effort) and `wg.Wait()`s.
- `internal/api/jobs.go` - `handleCancelJob` builds a `[]cancelSignal` and calls the helper instead
  of the inline sequential loop; dropped the now-orphaned `relayv1` import.
- `internal/api/workers.go` - `handleDisableWorker` same conversion; dropped its `relayv1` import.
- `internal/api/cancel_signals_test.go` (new) - `TestSendCancelSignals_FanOutIsConcurrent`: registers
  N=5 blocking senders (200ms each) in a real `worker.Registry` and asserts elapsed `< (N-1)*block`,
  which only the concurrent fan-out satisfies.

## Key Decisions

- **Shared helper over inlining.** The two sites are concrete (`*worker.Registry` is not an interface)
  and the handlers do unavoidable DB work, so pulling the send loop out is the clean unit-test seam.
  The per-site signal-construction loops stay separate because the sources differ (jobs iterates
  `[]store.Task` with per-task `force`; workers iterates `[]pgtype.UUID` all on one worker, `force=false`).
- **Concurrent-and-wait, not fire-and-forget.** Waiting keeps goroutine lifetime inside the request
  and bounds it to ~one send timeout; fire-and-forget would return faster but leak goroutines past the
  response. The proposal allowed either; bounded-wait is the lower-risk read.

## Verification

- Engineer proved the test RED against a sequential helper (1.0s elapsed vs 800ms threshold) and GREEN
  after the WaitGroup fan-out (0.20s).
- `go build ./...` clean; `go test ./internal/api/... ./internal/worker/...` green.
- Adversarial code review re-verified `registry.Send` / `workerSender.Send` concurrency safety against
  source (not comments), behavior preservation, and the "one bounded sender per gRPC stream" invariant.
  No high/medium/low findings.

## Notes / Limitations

- A handler-level wall-clock test would be integration-only (the cancel list comes from committed DB
  state), so the regression property is tested at the extracted-helper seam. Faithful to the property
  (concurrent vs sequential fan-out), no Docker needed.
- The race detector could not run in this Windows environment (TSan allocation error), so concurrency
  safety rests on static review of the two send paths rather than a `-race` pass.
