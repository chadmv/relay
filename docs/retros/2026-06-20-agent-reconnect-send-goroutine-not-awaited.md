---
date: 2026-06-20
topic: agent-reconnect-send-goroutine-not-awaited
branch: claude/blissful-brown-c7780a
pr: "2026-06-20 / agent-reconnect-send-goroutine-not-awaited"
merge: "2026-06-20 / agent-reconnect-send-goroutine-not-awaited"
---

# Session Retro: 2026-06-20 - Agent send goroutine not awaited across reconnect

**TL;DR:** Closed `bug-2026-06-18-agent-reconnect-send-goroutine-not-awaited`. The agent's
`connect()` never joined the previous send goroutine before spawning a new one on
reconnect, so two goroutines briefly read the same `sendCh` - violating the
one-bounded-sender invariant and silently dropping a queued message. Fixed by joining the
previous sender via a `WaitGroup` before each connect.

## What Was Built

- `internal/agent/agent.go`: added `sendWG sync.WaitGroup` (matching the existing
  `runnerWG`). `connect()` now calls `a.sendWG.Wait()` (after the new
  `connCtx`/deferred `connCancel`, before spawning the new sender), so the previous send
  goroutine is fully joined and at most one sender reads `sendCh` at any instant. The send
  loop was extracted into a named `runSender(connCtx, connCancel, send)` method for an
  injectable test seam.
- `internal/agent/sender_test.go`: deterministic white-box tests - an atomic
  concurrency counter with a blockable injected `send` asserts at-most-one-sender across a
  simulated reconnect; a second test asserts in-order delivery of queued messages by the
  live sender after the join. Verified load-bearing-red (skipping the join makes the
  counter observe 2 concurrent senders).

## Key Decisions

- **WaitGroup over done-channel:** `connect()` is only ever called from the single `Run`
  goroutine, so exactly one `Add`/`Wait` is outstanding and the classic Add-after-Wait
  race cannot occur; reusing the `runnerWG` idiom keeps the struct consistent.
- **Bounded loss, not re-enqueue:** the join is the load-bearing fix - it eliminates the
  dual-sender steal. The only remaining loss is the single in-flight message whose `Send`
  failed on the already-dead connection (unrecoverable regardless). Re-enqueue was
  declined: a blocking re-enqueue on the full 64-cap `sendCh` would deadlock the join, a
  non-blocking one would reorder the best-effort log stream. Task status is reconciled on
  reconnect via `RunningTasks`; only log chunks aren't replayed, so the bound is "at most
  one in-flight log chunk per stream drop," documented in the `runSender` doc comment.
- **Deterministic tests, not `-race`:** the project's `make test-race` excludes
  `internal/agent` on Windows (default toolchain's race detector fails there), so the
  tests assert the at-most-one-sender property via synchronization primitives; CI `-race`
  on Linux remains the backstop.

## Backlog Triage

- A low/cosmetic verify finding noted that the already-merged `failClaimedTask`
  (dispatch-failure-paths-silent) keeps `worker_id` set on a terminally-failed row, unlike
  the requeue/cancel SQL which nulls it. Reviewer rated it "behavior correct either way,
  cosmetic"; confirmed harmless (the active-task count filters on
  `status IN ('dispatched','running')`, so no slot leak). Below the filing bar; not filed.
