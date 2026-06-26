---
title: sendInventory in the default-cancel defer can park unbounded under a wedged sendCh
type: bug
status: closed
created: 2026-06-21
closed: 2026-06-25
resolution: fixed
priority: low
source: residual flagged during 2026-06-21-default-cancel-abandon-hang-design (out of scope there)
---

# sendInventory in the default-cancel defer can park unbounded under a wedged sendCh

## Summary
The default-cancel path's `defer` in `Runner.Run` (`internal/agent/runner.go`) calls
`handle.Finalize(r.ctx)` then `r.sendInventory(...)`. `sendInventory` uses the blocking
two-case `r.send` (select on `r.sendCh <- msg` and `<-r.ctx.Done()`), and `r.ctx` is the
parent/agent context. If `sendCh` is still wedged full after the subprocess is freed, that
inventory send can park until agent shutdown.

## Relationship to the default-cancel/Abandon hang fix
This was surfaced and explicitly scoped OUT during the design of
`docs/superpowers/specs/2026-06-21-default-cancel-abandon-hang-design.md`. That fix gives the
log-write path and the terminal-status send a per-task cancel escape (`cancelledCh`), which
unblocks `cmd.Wait()` and lets `Run` return - the unbounded-hang bug it targets. `sendInventory`
is different:
- It runs on the runner's OWN goroutine (in the deferred cleanup), NOT exec's copy goroutine, so
  it does not pin `cmd.Wait()`. The subprocess and the `cmd.Wait()` park are already freed by the
  time the defer runs.
- It is therefore not the unbounded `Run`-hang the 2026-06-21 fix is about; it is a separate,
  lower-severity goroutine-park-until-shutdown leak.

## Severity (low)
Same precondition as the parent bug (wedged server-side reader). Blast radius is one leaked
runner-cleanup goroutine parked until the agent reconnects/shuts down; the workspace Finalize has
already run, so workspace state is correct. No task-state correctness impact (server is
authoritative via epoch fencing). Strictly an agent-side resource leak, narrower than the
`cmd.Wait()` hang.

## Proposal (needs sizing)
Give `sendInventory` (and any other deferred-cleanup sends on the parent context that can park
under a wedge) a bounded or per-task-cancel-aware escape, consistent with the discipline the
2026-06-21 fix establishes. Options to weigh in design:
- Route the cleanup-phase inventory send through a bounded best-effort try-send when the task was
  cancelled/abandoned (mirror `sendFinalStatus`'s bounded branch).
- Or honor `cancelledCh` in the inventory send path.
Decide whether normal-completion inventory should stay blocking (it should, to preserve delivery
under a merely-slow consumer) - only the cancelled/abandoned cleanup path needs the escape.

## Acceptance / Done When
- A default-cancel (or abandon) of a task whose `sendCh` is wedged full does not leave the
  deferred `sendInventory` parked until agent shutdown.
- Normal-completion inventory delivery is unchanged (still blocking, still delivered under a slow
  consumer).
- A `//go:build !windows` regression test (mirroring the parent fix's wedged-full setup) asserts
  the cleanup goroutine returns within a real bound after a default cancel with a wedged channel.

## Related
- `internal/agent/runner.go` - `Run` default-cancel `defer` (Finalize + `sendInventory`),
  `sendInventory`, `send`.
- `docs/superpowers/specs/2026-06-21-default-cancel-abandon-hang-design.md` - "Risks" section,
  residual #3 (this item).
- `docs/backlog/bug-2026-06-20-default-cancel-abandon-backpressure-bound-test.md` - the parent bug.

## Resolution
Fixed 2026-06-25 (fix commit 8d816a0). `sendInventory` in `internal/agent/runner.go` now uses
a room-first bounded best-effort try-send (`select { case r.sendCh <- msg: default: }`, mirroring
`sendFinalStatus`'s cancelled branch) when `r.cancelled.Load() || r.abandoned.Load()`; normal
completion keeps the blocking two-case `r.send`. The `|| r.abandoned.Load()` arm is load-bearing
because `Abandon()` sets only `r.abandoned` yet still reaches the Finalize-and-inventory defer. No
new field, channel, or sender, so the "one bounded sender per gRPC stream" invariant is preserved
and the previously-unbounded cleanup park is bounded. Three `//go:build !windows` tests
(`TestRunner_DefaultCancel_WedgedFull_InventoryDoesNotPark`,
`TestRunner_Abandon_WedgedFull_InventoryDoesNotPark`,
`TestRunner_NormalCompletion_DeliversInventory`) proven RED-vs-GREEN on Linux/Docker; full
`internal/agent` suite and `go vet` clean. Adversarial review found no high/medium/low findings.
Closes residual #3 of the 2026-06-21 default-cancel/Abandon hang design.
