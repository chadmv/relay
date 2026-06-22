# Design: Default Cancel and Abandon Preempt a Log Write Blocked on a Full sendCh

**Date:** 2026-06-21
**Status:** Approved (gated agent-team run; design decisions pre-resolved by the conductor, spec signed off at the spec gate)
**Backlog item:** `docs/backlog/bug-2026-06-20-default-cancel-abandon-backpressure-bound-test.md`
**Builds on:** `docs/superpowers/specs/2026-06-19-forced-cancel-send-backpressure-design.md`
(this spec closes that spec's two deferred open questions: default cancel and `Abandon()` under a wedged channel)

## Problem statement

The 2026-06-19 forced-cancel fix gave the *forced* path (`Cancel(true)`) a
preemptible escape from a wedged `sendCh`: it closes `forcedCh`, which
`sendOrAbort` honors, and it bounds the terminal-status send. It deliberately
left the *default* cancel (`Cancel(false)`) and `Abandon()` (grace-expiry
requeue) paths alone, on the stated assumption that those paths remain bounded by
exec's 5s `WaitDelay`.

A Linux/Docker repro disproved that assumption. With production `WaitDelay = 5s`
unchanged, both the default-cancel and `Abandon()` paths hang **unbounded** - not
`WaitDelay`-bounded - when `sendCh` is wedged full while the subprocess is still
producing output. This is a confirmed latent bug, not a missing test. Two facts
in `internal/agent/runner.go` combine to produce the hang:

1. **`r.ctx` is the parent (agent) context, not the per-run context.**
   `newRunner` (runner.go:44) stores `ctx: parent` and `cancel:` the cancel func
   for the *derived* `runCtx`. `Cancel(false)` and `Abandon()` call `r.cancel()`,
   which cancels `runCtx` (killing the subprocess) but leaves `r.ctx` (the parent)
   un-done. So the `<-r.ctx.Done()` escape in both `send` and `sendOrAbort` never
   fires on a per-task cancel - only on agent shutdown.

2. **`WaitDelay` cannot unblock a channel-send-parked goroutine.** Go's `os/exec`
   `WaitDelay` fires by closing the parent IO pipe descriptors, then *waits* on
   the copy goroutines. A goroutine parked on `r.sendCh <- msg` inside
   `chunkWriter.Write -> sendOrAbort` is not blocked on the pipe, so closing pipe
   fds never unblocks it; `cmd.Wait()` blocks forever.

The forced path escapes only because `Cancel(true)` closes `forcedCh`, honored by
`sendOrAbort`'s third select case. Default cancel and `Abandon()` have no
equivalent escape, so a copy goroutine parked on a full `sendCh` pins the `Run`
goroutine and the subprocess `Wait` indefinitely.

### Impact and severity (medium)

Requires a worker stream whose server-side reader has stalled (wedged `sendCh`)
AND a still-producing task that is then default-cancelled or grace-`Abandon()`ed.
The server stays authoritative (epoch fencing already failed the task and bumped
`assignment_epoch`), so task *state* is correct. The damage is an agent-side
leak: the `Run` goroutine and the subprocess `Wait` never return, the task slot
is not freed, and no terminal status is sent. Bounded blast radius, but a real
unbounded hang in the exact `internal/agent` send/drain area that has regressed
twice this month.

## Chosen approach: A - a dedicated per-task cancel signal channel

Two approaches were weighed.

- **Approach A (chosen):** add a dedicated per-task cancel signal channel
  (`cancelledCh`), closed exactly once by either `Cancel(false)` or `Abandon()`,
  and add it as a fourth select case in `sendOrAbort`. This mirrors the proven
  `forcedCh` mechanism the forced path already uses.

- **Approach B (rejected):** make `sendOrAbort`'s escape select on the *run*
  context rather than the parent, so `r.cancel()` frees parked sends.

**Why A over B.** B is fragile. The log sends happen during `cmd.Wait()` while
`runCtx` is still live; the deferred `r.cancel()` only fires after
`sendFinalStatus` at the very end of `Run`. Wiring the escape to context lifetime
would couple log-drain semantics to context teardown ordering, which is exactly
the kind of implicit coupling that made the original bug hard to see. B's *insight*
is correct and worth recording: the bug is that the existing escape is tied to the
parent context instead of to per-task cancellation. But the right fix is to make
per-task cancellation explicit, not to lean on `runCtx`'s lifetime. A dedicated
channel closed explicitly by `Cancel`/`Abandon` is the same explicit, auditable
mechanism the forced path already uses; it keeps the diff parallel to existing
code and keeps the abort signal independent of context-cancellation ordering.

We adopt A's explicit-channel form while crediting B's diagnosis.

## Mechanism

1. **New field on `Runner`:** `cancelledCh chan struct{}`, allocated in
   `newRunner` alongside `forcedCh`. It is a signal channel: never sent on, only
   closed.

2. **`Cancel(false)` AND `Abandon()` close it exactly once.** Both per-task cancel
   kinds (default cancel and grace-expiry abandon) must free a parked log send, so
   both close `cancelledCh`. The close must be guarded so concurrent, repeated, or
   mixed cancels never double-close (a double close panics). Use a dedicated
   `cancelledClose sync.Once`, or gate the close behind a `CompareAndSwap` on a new
   atomic, so only the first per-task cancel closes the channel. The existing
   `r.cancelled.Store(true)` / `r.abandoned.Store(true)` / `r.cancel()` calls stay
   unchanged; this adds the channel close beside them.

   Note on the forced path: `Cancel(true)` already closes `forcedCh` and sets
   `r.cancelled.Store(true)`. To keep the three escape signals cleanly separable
   and the guard simple, `Cancel(true)` does **not** need to also close
   `cancelledCh` - `sendOrAbort` selects on `forcedCh` independently (see step 3).
   The `cancelledClose` guard must still be safe if a forced cancel is later
   followed by a default cancel on the same runner (the `sync.Once`/CAS guard
   handles that). Keeping forced -> `forcedCh` only and default/abandon ->
   `cancelledCh` only is the minimal, non-overlapping wiring.

3. **`sendOrAbort` gains a fourth select case.** Today it selects on three:
   `r.sendCh <- msg`, `<-r.ctx.Done()`, `<-r.forcedCh`. Add `<-r.cancelledCh`.
   Any of the three escapes (agent shutdown, forced cancel, per-task cancel)
   abandons the in-flight log write and returns `false`, so `chunkWriter.Write`
   returns `errForcedAbort`, exec's `io.Copy` stops, and `cmd.Wait()` returns
   promptly. The exact select:

   ```
   select {
   case r.sendCh <- msg:
       return true
   case <-r.ctx.Done():    // agent shutdown
       return false
   case <-r.forcedCh:      // forced cancel
       return false
   case <-r.cancelledCh:   // default cancel or abandon
       return false
   }
   ```

   `chunkWriter.Write` is unchanged: it already routes through `sendOrAbort` and
   returns `errForcedAbort` on a `false` result. The sentinel name stays
   `errForcedAbort`; widening its meaning to "any abandon" is a comment-only
   clarification, not a behavior change. The empty-chunk guard at the top of
   `Write` stays.

4. **All other callers (`send`) are unchanged.** `sendStepMarker`, the prepare
   progress flush, `sendInventory`, and the prepare-phase status sends all use
   `r.send` (the two-case blocking select). They run on the runner's own
   goroutine, not exec's copy goroutine, and are not the thing blocking
   `cmd.Wait()`. Keeping them unchanged keeps the diff surgical and preserves the
   normal slow-consumer send contract.

## Terminal-status gating predicate (load-bearing)

This is the subtle part. After `chunkWriter.Write` abandons and `cmd.Wait()`
returns, `Run` proceeds to `sendFinalStatus`. If that call used the unchanged
blocking two-case `send`, it would block on the still-wedged `sendCh` (parent
context still alive) and `Run` would never return - the bug would persist on the
terminal-send half even after the log-write half is fixed. So `sendFinalStatus`
must bound the terminal send for a per-task cancel of either kind, exactly as it
already does for the forced path.

`sendFinalStatus` today gates its bounded best-effort try-send on
`r.forced.Load()`. Extend that gate to fire when **forced OR cancelled** - i.e.
on any per-task cancel. "Bounded" = the non-blocking try-send already in the
code:

```
select {
case r.sendCh <- msg:
default:
    // sendCh full and wedged; abandon best-effort. Server is authoritative.
}
```

The predicate, stated exhaustively so the planner and implementer cannot
mis-bound the normal path:

| Path | Condition in `sendFinalStatus` | Terminal send | Other behavior |
| --- | --- | --- | --- |
| **Forced** (`Cancel(true)`) | `r.forced.Load()` | Bounded try-send (deliver if room, drop if full) | Skips workspace Finalize (existing defer guard) |
| **Default cancel** (`Cancel(false)`) | `r.cancelled.Load() && !r.abandoned.Load()` | Bounded try-send (deliver if room, drop if full) | **Still runs workspace Finalize + sendInventory** |
| **Abandon** (`Abandon()`) | `r.abandoned.Load()` | **No terminal send at all** (existing early return stays) | Returns promptly; subprocess freed |
| **Normal completion** | none of the above | **UNCHANGED blocking two-case `send`** | Normal Finalize |

Implementation note on the gate expression: the early `if r.abandoned.Load() {
return }` at the top of `sendFinalStatus` already removes the abandoned case
before any send decision, so the bounded branch only needs to fire on `r.forced ||
r.cancelled` (abandon has already returned). Since `Cancel(false)` sets
`r.cancelled` and `Cancel(true)` sets both `r.forced` and `r.cancelled`, gating the
bounded branch on `r.cancelled.Load()` alone is sufficient and covers both cancel
kinds; `r.forced` need not be re-checked. The implementer may write the gate as
`r.cancelled.Load()` (simplest) or `r.forced.Load() || r.cancelled.Load()`
(explicit); both are equivalent given how `Cancel` sets the atomics. State whichever
form is chosen in the diff; do not bound the normal-completion send.

**Bounded = best-effort, room-first.** When `sendCh` has headroom (the common
case: connection fine, one slow consumer, or buffer slack) the terminal status
enqueues and the server receives it. When `sendCh` is genuinely wedged full the
terminal status is dropped and `Run` returns. The drop is safe: see the epoch
argument below.

## Default cancel stays a default cancel (Finalize)

A default cancel must NOT silently become a forced cancel. The forced path skips
workspace Finalize (`defer` guard at runner.go:117-121, gated on
`r.forced.Load()`). The default path must continue to run `handle.Finalize(r.ctx)`
and `r.sendInventory(...)`. This spec changes only the *send-blocking discipline*
of the default path; it does not touch the Finalize gate, which keys off
`r.forced` and is therefore unaffected by setting `r.cancelled` or closing
`cancelledCh`.

The per-task escape is safe for the default path specifically because
`handle.Finalize(r.ctx)` uses the **parent** context, not `runCtx` and not
`cancelledCh`. Closing `cancelledCh` frees a parked *log* send (the thing wedging
`cmd.Wait()`); it does not abort Finalize. So the default path still cleans up the
workspace and reports inventory after the subprocess is freed, exactly as today -
only the unbounded log-send park is removed.

## Invariant-compliance argument

- **One bounded sender per gRPC stream.** Served, not threatened. All stream
  writes still go through the single `sendCh` consumer in `agent.connect`. This
  change adds no new writer and no out-of-band send. It only *reduces* how long a
  runner goroutine can block on a non-reading peer: the log path gains a fourth
  "abandon" select case, and the cancelled terminal send becomes bounded. Both
  strictly shorten the worst-case park, directly serving the invariant's stated
  goal ("a peer that stops reading must never block a dispatcher or HTTP handler
  indefinitely"). It removes two previously-unbounded parks (default cancel and
  abandon).

- **Epoch fence.** Untouched. No new write to `tasks.status` or `task_logs`. The
  terminal status keeps `r.epoch`; the server's epoch-fenced consumers handle it
  exactly as before. No task is returned to `pending` and no epoch is bumped here.
  A terminal status dropped under a wedged channel carries the now-fenced-out old
  epoch, so the server's authoritative state is unaffected.

- **Identity-checked teardown / no interior pointers across locks / single
  job-spec pipeline / single JSON entry point.** Not in scope; `cancelledCh` is
  per-runner state set once at `newRunner` and only closed by `Cancel`/`Abandon`.
  No registry, sender registration, job-spec ingestion, or HTTP body handling
  changes.

## Success criteria

Regression tests are `//go:build !windows` and live in
`internal/agent/runner_cancel_test.go`, mirroring the existing forced-path tests.
`make test` on Windows silently skips them; they MUST be observed on Linux/Docker
per `feedback-platform-gated-test-verification`.

1. **Default-cancel wedged-full repro returns within the bound.** Flood an
   undrained cap `sendCh` so a copy goroutine parks on the send (wait until
   `len(sendCh) == cap(sendCh)`), start a continuous-output subprocess
   (`sh -c 'while true; do echo x; done'`), then `Cancel(false)`. Assert `Run`
   returns within the new bound (e.g. < ~8s), NOT 30s+ unbounded. This test
   asserts the **return bound only** - terminal status may legitimately be dropped
   under wedged-full, so it makes no terminal-status assertion.

2. **Non-wedged default cancel still delivers terminal FAILED.** A separate test
   with headroom on `sendCh`: default-cancel a running task and assert a terminal
   `TaskStatus=FAILED` appears on `sendCh` (mirrors
   `TestRunner_ForceCancel_StillSendsTerminalFailed`). Guards "default cancel still
   reports when it can," i.e. the bounded branch is room-first, not drop-always.

3. **`Abandon()` wedged-full repro returns within the bound.** Same flood-and-park
   setup as criterion 1, then `Abandon()`. Assert `Run` returns within the bound.
   No terminal-status assertion: terminal status is suppressed by `abandoned`
   (existing early return in `sendFinalStatus`).

4. **Existing tests stay green on Linux.**
   `TestRunner_DefaultCancel_RunsWorkspaceFinalize` (Finalize still runs - this is
   the guard that default cancel did not turn into forced cancel),
   `TestRunner_ForceCancel_SkipsWorkspaceFinalize`,
   `TestRunner_ForceCancel_ReturnsQuickly`,
   `TestRunner_ForceCancel_StillSendsTerminalFailed`, and
   `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang` all continue to
   pass.

5. **Red-before-green is provable on Linux/Docker.** Reverting the fix must make
   the bound assertions in criteria 1 and 3 FAIL (both paths hang past 30s today).
   Confirm red pre-fix and green post-fix on Linux, not on Windows where the tests
   are skipped. This is the natural RED proof now that the bug is confirmed.

## Risks

- **Terminal status dropped under a fully-wedged channel (accepted).** When
  `sendCh` is genuinely full and wedged, the default-cancel terminal `FAILED` is
  dropped best-effort. Mitigation: the server already holds authoritative `failed`
  from `CancelJobTasks` (run in the cancel transaction before the `CancelTask`
  gRPC message), and the agent's late terminal carries the now-bumped-out old
  `r.epoch`, so it is epoch-fenced out on arrival anyway. Consistent with the
  existing best-effort contract the forced path already documents. Documented here
  so it is a known, deliberate behavior, not a silent gap.

- **Scope creep into the slow-consumer / normal-completion send contract.** The
  pipe-drain fix made `chunkWriter.Write` never tear down `io.Copy` for a
  *transient* slow consumer; the forced fix reintroduced a teardown gated on
  `forcedCh`. This change extends that teardown to `cancelledCh`, still strictly
  gated. A reviewer MUST confirm that the **normal / slow-consumer non-cancel path
  is byte-for-byte unchanged**: with neither `forcedCh` nor `cancelledCh` closed,
  `sendOrAbort` still blocks on `r.sendCh <- msg` or `<-r.ctx.Done()` exactly as
  before, `Write` still returns `(len(p), nil)` on a successful enqueue, and
  `sendFinalStatus` still uses the blocking two-case `send` for normal completion.
  No new abort path may fire absent an explicit per-task cancel.

- **Residual (out of scope, pre-existing): `sendInventory` after a wedged-full
  default cancel.** The default-cancel `defer` calls `handle.Finalize(r.ctx)` then
  `r.sendInventory(...)`, and `sendInventory` uses the blocking two-case `send` on
  the parent context. If `sendCh` is still wedged after the subprocess is freed,
  that inventory send can still park until agent shutdown. This is pre-existing,
  is on the runner's own goroutine (not exec's copy goroutine, so it does not pin
  `cmd.Wait()`), and is out of scope for this fix, which targets the
  log-send/`cmd.Wait()` park that produces the unbounded `Run` hang. The
  regression tests use a fake handle whose Finalize/Inventory do not re-park, so
  the asserted bound is the subprocess-path return. Flagged so the boundary is
  explicit; if inventory-under-wedge proves to matter it is a separate item.

## Files in scope

- `internal/agent/runner.go` - add `cancelledCh` field allocated in `newRunner`;
  `Cancel(false)` and `Abandon()` close it once (guarded); `sendOrAbort` gains the
  `<-r.cancelledCh` select case; `sendFinalStatus` bounded branch fires on a
  per-task cancel (forced OR cancelled), normal completion unchanged. No new
  sender; no epoch changes; Finalize gate (keyed on `r.forced`) untouched.
- `internal/agent/runner_cancel_test.go` - add criteria 1, 2, and 3 (default-cancel
  wedged-full bound, default-cancel non-wedged terminal FAILED, abandon
  wedged-full bound), mirroring the forced-path tests.

Out of scope: proto, API, CLI, server-side handler, Windows proctree code (the fix
is platform-neutral; it lives in the shared runner), and the pre-existing
`sendInventory`-under-wedge residual noted above.
