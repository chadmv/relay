# Design: sendInventory Escapes a Wedged sendCh on the Cancelled/Abandoned Cleanup Path

**Date:** 2026-06-25
**Status:** Approved (autonomous agent-team run; design pre-resolved against the parent fix's established discipline)
**Backlog item:** `docs/backlog/bug-2026-06-21-sendinventory-blocking-send-under-wedge.md`
**Builds on:** `docs/superpowers/specs/2026-06-21-default-cancel-abandon-hang-design.md`
(this spec closes that spec's "Risks" residual #3, explicitly scoped out there)

## Problem statement

The default-cancel cleanup `defer` in `Runner.Run` (`internal/agent/runner.go:134-143`)
runs `handle.Finalize(r.ctx)` then `r.sendInventory(handle.Inventory())`.
`sendInventory` (runner.go:414) uses the blocking two-case `r.send` (runner.go:342):

```
select {
case r.sendCh <- msg:
case <-r.ctx.Done():
}
```

`r.ctx` is the parent/agent context, not the per-run context. If `sendCh` is still
wedged full after the subprocess is freed, this inventory send parks until agent
shutdown - the only event that closes `r.ctx`. A per-task cancel never frees it.

This is the exact `r.ctx`-vs-`runCtx` coupling the parent fix diagnosed for the log
path. It survived that fix because `sendInventory` is not the thing pinning
`cmd.Wait()`:

- It runs on the runner's OWN goroutine inside the deferred cleanup, NOT exec's copy
  goroutine. The subprocess and the `cmd.Wait()` park are already freed by the time
  the defer runs.
- So it is not the unbounded `Run`-hang the parent fix targeted; it is a separate,
  lower-severity goroutine-park-until-shutdown leak.

### Impact and severity (low)

Same precondition as the parent bug: a worker stream whose server-side reader has
stalled (wedged `sendCh`), on the cancelled/abandoned cleanup path. Blast radius is
one leaked runner-cleanup goroutine parked until the agent reconnects or shuts down.
The workspace `Finalize` has already run, so workspace state is correct. No task-state
correctness impact: the server is authoritative via epoch fencing
(`CancelJobTasks` already failed the task and bumped `assignment_epoch`). Strictly an
agent-side resource leak, narrower than the parent fix's `cmd.Wait()` hang.

## Chosen approach: A - bounded best-effort try-send on the cancelled path

Two options were weighed (both raised in the backlog item).

- **Approach A (chosen):** route the cleanup-phase inventory send through a bounded
  best-effort try-send when the task was cancelled/abandoned, mirroring
  `sendFinalStatus`'s existing bounded branch. Gate on `r.cancelled.Load()` (set by
  both `Cancel(false)` and `Cancel(true)`) OR `r.abandoned.Load()` (set by
  `Abandon()`). Normal completion keeps the unchanged blocking `send`.

- **Approach B (rejected):** add a `<-r.cancelledCh` select case to the inventory send
  path (or factor a shared cancel-aware send).

**Why A over B.** The parent fix chose the explicit-channel escape (`cancelledCh`,
fourth select case) specifically for `sendOrAbort`, because that path runs on exec's
copy goroutine during a live `cmd.Wait()` and the abort must propagate as
`errForcedAbort` to stop `io.Copy`. `sendInventory` has none of that machinery: it is
a plain runner-goroutine send with no `cmd.Wait()` to release and no `io.Copy` to tear
down. The decision the runner needs at this point is simply "the task was cancelled, do
not block forever delivering best-effort cleanup metadata" - which is exactly the
predicate `sendFinalStatus` already encodes one line earlier in the same teardown.
Reusing the `r.cancelled` / `r.abandoned` atomic gate keeps the diff parallel to the
terminal-status send the implementer just read, requires no new wiring into the
inventory path, and keeps `cancelledCh` reserved for its single documented purpose
(freeing a `cmd.Wait()`-pinning log write). B would widen `cancelledCh`'s contract for
a path that does not pin `cmd.Wait()`, blurring the clean "channel frees the copy
goroutine, atomic gates the runner goroutine" split the parent fix established.

A also matches the existing best-effort contract verbatim. The bounded send is the
same room-first try-send `sendFinalStatus` uses:

```
select {
case r.sendCh <- msg:
default:
    // sendCh full and wedged; abandon best-effort. Server is authoritative.
}
```

### Abandon note

`Abandon()` sets `r.abandoned` but NOT `r.cancelled` (see `Runner.Abandon`,
runner.go:83-87). `sendFinalStatus` early-returns on `r.abandoned` so it never reaches
its `r.cancelled` gate. `sendInventory` has no such early return and is reached on the
abandon path too (the Finalize defer is gated only on `r.forced`, not `r.abandoned`).
So the inventory gate must fire on `r.cancelled.Load() || r.abandoned.Load()` to cover
the abandon-under-wedge case the acceptance criteria require. The forced path skips the
Finalize defer entirely (`r.forced.Load()` guard at runner.go:135), so it never reaches
`sendInventory`; including forced in the gate is harmless but moot.

## Normal-completion inventory stays blocking

This is the load-bearing boundary. On normal completion (none of `r.cancelled`,
`r.forced`, `r.abandoned` set) `sendInventory` MUST keep the unchanged blocking
two-case `r.send`. Inventory is workspace-reuse metadata the server wants delivered;
under a merely-slow-but-live consumer it must still wait and deliver, not drop. Only
the cancelled/abandoned cleanup path - where the runner is tearing down a failed task
and the server is already authoritative - gets the bounded escape. Dropping inventory
there is safe: the entry describes a workspace that Finalize already reconciled
locally, and a re-sync recomputes it on next use.

## Mechanism

In `sendInventory`, branch on the per-task-cancel gate before the send:

```
if r.cancelled.Load() || r.abandoned.Load() {
    select {
    case r.sendCh <- msg:
    default:
        // sendCh full and wedged; abandon best-effort. Cleanup path only;
        // server is authoritative and Finalize already reconciled the workspace.
    }
    return
}
r.send(msg) // normal completion: blocking, delivered under a slow consumer
```

No new field, no new channel, no change to `send`, `sendOrAbort`, `sendFinalStatus`,
the Finalize defer, `Cancel`, or `Abandon`. The change is local to `sendInventory`.

## Invariant-compliance argument

- **One bounded sender per gRPC stream.** Served, not threatened. No new writer and no
  out-of-band send: the bounded branch still writes only to `sendCh`, consumed by the
  single sender in `agent.connect`. The change strictly *shortens* the worst-case park
  of a runner goroutine on a non-reading peer, directly serving the invariant's goal
  ("a peer that stops reading must never block ... indefinitely"). It removes one
  previously-unbounded park (cleanup-phase inventory on the cancelled/abandoned path).

- **Epoch fence.** Untouched and not relevant to correctness. Inventory is a
  `WorkspaceInventoryUpdate`, not a write to `tasks.status` or `task_logs`; it carries
  no epoch. The server stays authoritative; a dropped inventory entry under a wedged
  channel is recomputed on next workspace use. No task is returned to `pending` and no
  epoch is bumped here.

- **Identity-checked teardown / no interior pointers across locks / single job-spec
  pipeline / single JSON entry point.** Not in scope. No registry, sender registration,
  job-spec ingestion, or HTTP body handling changes.

## Success criteria

Regression tests are `//go:build !windows` and live alongside the parent fix's tests
in `internal/agent/runner_cancel_test.go`, mirroring its wedged-full setup. `make test`
on Windows silently skips them; they MUST be observed on Linux/Docker per
`feedback-platform-gated-test-verification`.

1. **Cleanup-goroutine bound under a wedged sendCh after default cancel.** Using a fake
   workspace provider/handle whose `Inventory()` returns an entry (so the defer reaches
   `sendInventory`), flood an undrained cap `sendCh` so it is full
   (`len(sendCh) == cap(sendCh)`), run a continuous-output subprocess, then
   `Cancel(false)`. Assert `Run` returns within a real bound (e.g. < ~8s), NOT 30s+
   unbounded. This asserts the return bound only - inventory may legitimately be dropped
   under wedged-full, so it makes no inventory-delivery assertion.

   Note: with the parent fix in place, criterion 1 must isolate the inventory park as
   the residual hang. The parent fix freed the log-send/`cmd.Wait()` park, so the test
   must use a handle whose Finalize does not re-park and whose Inventory returns a
   non-empty entry, with `sendCh` still wedged when the defer runs, so the ONLY thing
   that could still park `Run` is `sendInventory`. The bound proves it no longer does.

2. **`Abandon()` under a wedged sendCh returns within the bound.** Same wedged-full
   setup, then `Abandon()`. Assert `Run` returns within the bound. The Finalize defer
   still runs on abandon (gated only on `r.forced`), so this exercises the
   `r.abandoned.Load()` arm of the inventory gate.

3. **Normal-completion inventory still delivers (blocking) under a slow consumer.** With
   headroom on `sendCh` and a fake handle returning an inventory entry, run a task to
   normal completion and assert a `WorkspaceInventory` message appears on `sendCh`.
   Guards that the normal path is unchanged blocking delivery, not drop-on-cancel
   leaking into normal completion.

4. **Existing tests stay green on Linux.** All parent-fix tests
   (`TestRunner_DefaultCancel_RunsWorkspaceFinalize`,
   `TestRunner_ForceCancel_SkipsWorkspaceFinalize`,
   `TestRunner_ForceCancel_ReturnsQuickly`,
   `TestRunner_ForceCancel_StillSendsTerminalFailed`, the default-cancel/abandon bound
   tests, and `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang`) continue to
   pass; the change is additive within `sendInventory`.

5. **Red-before-green is provable on Linux/Docker.** Reverting the `sendInventory`
   change must make criteria 1 and 2 FAIL (the cleanup goroutine hangs past 30s on the
   inventory park). Confirm RED pre-fix and GREEN post-fix on Linux, not on Windows
   where the tests are skipped.

## Risks

- **Inventory dropped under a fully-wedged channel (accepted).** When `sendCh` is
  genuinely full and wedged on the cancelled/abandoned cleanup path, the inventory
  entry is dropped best-effort. Mitigation: Finalize already reconciled the workspace
  locally, the server is authoritative, and the entry is recomputed on next workspace
  use. Consistent with the best-effort terminal-status contract the parent fix already
  documents. Documented here so it is a known, deliberate behavior, not a silent gap.

- **Scope creep into the normal-completion send contract.** A reviewer MUST confirm
  the normal / slow-consumer non-cancel inventory path is byte-for-byte unchanged: with
  none of `r.cancelled` / `r.abandoned` set, `sendInventory` still calls the blocking
  two-case `r.send` exactly as before. No new drop path may fire absent an explicit
  per-task cancel or abandon.

## Files in scope

- `internal/agent/runner.go` - `sendInventory` gains a per-task-cancel bounded
  best-effort branch (gated on `r.cancelled.Load() || r.abandoned.Load()`); normal
  completion keeps the blocking `r.send`. No new field, channel, or sender; no epoch
  changes; `Cancel`, `Abandon`, `send`, `sendOrAbort`, `sendFinalStatus`, and the
  Finalize defer untouched.
- `internal/agent/runner_cancel_test.go` - add criteria 1, 2, and 3, mirroring the
  parent fix's wedged-full setup, with a fake handle whose `Inventory()` returns a
  non-empty entry so the defer reaches `sendInventory`.

Out of scope: proto, API, CLI, server-side handler, Windows proctree code (the fix is
platform-neutral; it lives in the shared runner), and any change to the `cancelledCh`
log-abort mechanism the parent fix established.
