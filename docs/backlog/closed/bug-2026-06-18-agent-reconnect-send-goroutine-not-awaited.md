---
title: Agent send goroutine not awaited across reconnect (transient dual-sender drops a queued message)
type: bug
status: closed
created: 2026-06-18
closed: 2026-06-20
resolution: fixed
priority: medium
source: 2026-06-18 /roadmap deep Gaps review agent, confirmed by direct code read
---

# Agent send goroutine not awaited across reconnect (transient dual-sender drops a queued message)

## Summary
Invariant: one bounded sender per gRPC stream. On the agent, each `connect()` spawns a
send goroutine bound to `connCtx` that owns all writes to `sendCh`. When the recv loop
breaks it calls `connCancel()` and returns immediately, never awaiting the send goroutine;
`Run()` then loops and calls `connect()` again, spawning a second send goroutine on the
same shared `sendCh`. For the overlap window two goroutines read one channel, and a
message the old goroutine already pulled off `sendCh` is silently dropped when its
`stream.Send` fails on the dead connection.

## Repro / Symptoms
- Force a stream drop while messages are queued on `sendCh` (e.g. block `stream.Send` then
  kill the connection): the in-flight message is lost. Task status is reconciled on
  reconnect via the register/RunningTasks path, but task-log chunks are not replayed, so a
  forced reconnect can drop log output.
- During the reconnect overlap, two send goroutines select on the same `a.sendCh`,
  violating the "one bounded sender per gRPC stream" invariant.

## Proposal
Join the previous send goroutine before the next connect: have it signal a `WaitGroup`
(or close a done channel) on exit, and make `connect()` wait for it after `connCancel()`,
so at most one sender exists at a time. Optionally re-enqueue or otherwise account for a
message whose `Send` failed instead of dropping it silently.

## Acceptance / Done When
- At most one send goroutine reads `sendCh` at any instant across a reconnect (assert via a
  test with an injected slow/blocked `Send`).
- A reconnect with a queued message does not silently lose it, or the loss is explicitly
  bounded and documented.

## Related
- `internal/agent/agent.go:62-95` (Run reconnect loop), `:100-208` (`connect`: send goroutine `:163-175`, recv-loop `connCancel()` at `:183` with no join before the next `connect()`)
- [[bug-2026-06-10-reconnect-backoff-never-resets]] - same agent reconnect path, different defect
- [[bug-2026-06-10-stale-stream-teardown-clobbers-registration]] - server-side counterpart to stream-lifecycle teardown races
- CLAUDE.md Invariants: "One bounded sender per gRPC stream"

## Notes
Surfaced by the 2026-06-18 `/roadmap deep` Gaps review agent and confirmed by direct code read.

## Resolution
fixed - added a `sendWG sync.WaitGroup` to `Agent` (matching the existing `runnerWG`
pattern). `connect()` now calls `a.sendWG.Wait()` before spawning the next sender, so the
previous send goroutine is joined after `connCancel()` and at most one goroutine reads
`sendCh` at any instant across a reconnect. The send loop was extracted into a named
`runSender` method for testability. The single in-flight message whose `Send` fails on
the already-dead connection is a documented bounded loss (at most one log chunk per stream
drop) - deliberately not re-enqueued (a blocking re-enqueue on the full 64-cap `sendCh`
would deadlock the join; non-blocking would reorder the best-effort log stream; task
status is already reconciled on reconnect via `RunningTasks`). Covered by deterministic
white-box tests (atomic concurrency counter + real join) asserting at-most-one-sender
across a reconnect and in-order delivery of queued messages by the live sender; verified
load-bearing-red. Plan:
`docs/superpowers/plans/2026-06-20-agent-reconnect-send-goroutine-not-awaited.md`.
