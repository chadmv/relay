# Design: Forced Cancel Preempts a Log Write Blocked on a Full sendCh

**Date:** 2026-06-19
**Status:** Approved (autonomous autopilot run; conductor selected Option 1)
**Backlog item:** `docs/backlog/bug-2026-06-19-forced-cancel-send-backpressure.md`
**Builds on:** `docs/superpowers/specs/2026-05-04-force-cancel-design.md`,
`docs/superpowers/specs/2026-06-19-runner-pipe-drain-hang-design.md`

## Problem statement

A forced cancel (`relay cancel <job> --force`) exists to free the worker slot and
workspace in well under exec's 5s `WaitDelay`. That promise breaks under the exact
condition force-cancel is meant for: a wedged or slow coordinator connection.

The mechanics, traced in code:

- `Runner.send` (runner.go) selects on two cases only: `r.sendCh <- msg` and
  `<-r.ctx.Done()`. `r.ctx` is the long-lived **agent** context (`a.runCtx`),
  stored verbatim at `newRunner` (runner.go:42). It is NOT the per-task run
  context.
- `Cancel(force)` calls `r.cancel()`, which cancels only the per-task `runCtx`
  (a child of `r.ctx`). It does not cancel `r.ctx`. So a task-level cancel,
  forced or not, cannot make a parked `send` return.
- Subprocess stdout/stderr is copied by exec's own copy goroutine into
  `chunkWriter.Write`, which calls `r.send`. When `sendCh` is full (the send
  goroutine is not draining because the stream is wedged), that goroutine parks
  inside `send`.
- A forced cancel fires `cmd.Cancel` -> SIGKILL of the process group. The
  subprocess dies, but the copy goroutine is blocked inside `Write` -> `send`,
  not on the pipe. `io.Copy` cannot observe EOF until `Write` returns, so
  `cmd.Wait()` waits out the full 5s `WaitDelay` before force-closing the
  descriptors. The forced cancel does not return promptly.

This is pre-existing (reproduces on `main`) and was surfaced by the 2026-06-19
pipe-drain fix's Linux test run. `TestRunner_ForceCancel_ReturnsQuickly` floods
an undrained cap-4096 `sendCh`, then force-cancels; the runner cannot return
under the asserted 2s and the test's 3s safety net fires.

## Chosen approach: Option 1 - per-runner forced-abort signal for in-flight log writes

The conductor selected Option 1 over Option 2 (accept a 5s bound and just make
the test drain `sendCh`). Rationale: the entire point of force-cancel is to work
when the connection is wedged. Option 2 defeats the feature's documented promise
("free the worker as quickly as possible") in precisely the scenario operators
reach for `--force`. Option 1 restores the promise with a surgical concurrency
change and no new send path.

### Mechanism

Add a per-runner forced-abort channel, closed exactly once when a forced cancel
arrives, and have the **log-write path** abandon in-flight sends on it. The
terminal-status path uses a different, bounded send (see next section) so a
forced cancel still reports terminal `FAILED`.

1. **New field on `Runner`:** `forcedCh chan struct{}`, allocated in `newRunner`.
   It is a signal channel: never sent on, only closed.

2. **`Cancel(force bool)` closes it once.** Guard the close so concurrent or
   repeated cancels cannot double-close (panic). Use `sync.Once`, or gate the
   close behind the existing `forced atomic.Bool` CompareAndSwap so only the
   first forced cancel closes the channel. The existing `r.cancel()` call stays
   (it still drives `cmd.Cancel` -> SIGKILL and per-task context teardown).

3. **`chunkWriter.Write` becomes abort-aware.** It performs the send through a
   variant that selects on three cases: `r.sendCh <- msg`, `<-r.ctx.Done()`
   (agent shutdown, unchanged), and `<-r.forcedCh`. On `<-r.forcedCh` it
   abandons the chunk and returns a sentinel error (e.g. `errForcedAbort`) so
   exec's `io.Copy` stops copying and `cmd.Wait()` returns promptly. Returning a
   non-nil error from `Write` is the documented way to make `io.Copy` stop; exec
   treats the writer as done and unblocks `Wait`.

   This is a deliberate, scoped reversal of the pipe-drain fix's "always return
   `(len(p), nil)`" contract: that contract exists so a *transient* slow consumer
   never tears down the copy, but a *forced cancel* is exactly the case where we
   want the copy torn down immediately. The abort is gated on `forcedCh`, so the
   normal slow-consumer path is unchanged.

4. **The log-streaming `send` keeps its current two-case select** for callers
   other than `chunkWriter` (step markers, prepare progress, inventory). Those
   run on the runner's own goroutine, not exec's copy goroutine, and are not the
   thing blocking `cmd.Wait`. Keeping them unchanged keeps the diff surgical. The
   abort is introduced only on the path that gates `cmd.Wait`.

### Concrete shape (descriptive, not prescriptive on naming)

- `errForcedAbort` is a package-level sentinel `error`.
- `chunkWriter.Write` calls a helper (e.g. `r.sendOrAbort(msg, w-owned-by-exec)`),
  or inlines a three-case select, returning `(0, errForcedAbort)` on the
  `forcedCh` branch and `(len(p), nil)` on a successful enqueue, preserving the
  empty-chunk guard at the top.
- `Cancel` closes `forcedCh` only on the forced path, exactly once.

The implementer owns final naming and whether the three-case select is a new
`Runner` method or inlined in `Write`. This spec fixes the behavior, not the
identifiers.

## How terminal FAILED still sends

This is the load-bearing constraint and the subtle part. Two facts make it
correct and bounded:

**Fact 1 - the forced abort must not touch the terminal-status send.**
`sendFinalStatus` must not consult `forcedCh`. If it did, a forced cancel would
abandon the terminal `FAILED` itself. The `forcedCh` branch lives only in the
`chunkWriter.Write` log path. `sendFinalStatus` calls its own send.

**Fact 2 - the terminal-status send must still be bounded, or `Run` never
returns.** After `chunkWriter.Write` aborts and `cmd.Wait()` returns, `Run`
proceeds to `sendFinalStatus(FAILED)`. If that call used the unchanged two-case
`send`, it would block on the still-full `sendCh` (agent ctx still alive) and
`Run` would never return - the test waits on `Run` returning, so it would still
fail, and a real wedged connection would still pin the runner goroutine. So the
terminal send on a forced cancel must be **bounded**: attempt the enqueue, but
do not block indefinitely.

The terminal send is safe to bound (best-effort) because the server already holds
the authoritative terminal state:

- `handleCancelJob` (api/jobs.go) runs `CancelJobTasks` inside its transaction
  **before** the `CancelTask` gRPC message is sent. That statement fails every
  non-terminal task AND bumps `assignment_epoch`. The DB is already `failed`.
- The agent's terminal `TaskStatus=FAILED` carries the runner's old `r.epoch`,
  which the epoch bump has now fenced out. The server's status/log consumers are
  epoch-fenced, so the late terminal message for a force-cancelled task is
  rejected on arrival regardless. It is genuinely best-effort and, in the cancel
  case, redundant with the server's own authoritative transition.
- The 2026-05-04 force-cancel design already specifies "Agent sends final
  `TaskStatus=FAILED`: yes (best-effort)" in its behavior matrix. Bounding the
  send is consistent with that contract, not a weakening of it.

**Bounding mechanism for the terminal send (forced path only):** `sendFinalStatus`
attempts a non-blocking-with-fallback enqueue when `r.forced.Load()` is true -
select on `r.sendCh <- msg` and `<-r.forcedCh` (already closed), taking the
`forcedCh` branch as "best-effort enqueue failed, return without blocking." When
`sendCh` has room (the common force-cancel case: connection is fine, just one
slow consumer, or there is headroom in the 64/4096 buffer) the message enqueues
normally and the server receives it. When `sendCh` is genuinely full and wedged,
the terminal send is dropped and the runner returns; the server's authoritative
`failed` transition (Fact 1 above) covers correctness.

The non-forced path of `sendFinalStatus` is unchanged (blocking two-case send),
preserving today's "default cancel drains and reports" behavior.

Net effect on the two terminal cases the success criteria require:

- **sendCh has room (typical):** terminal `FAILED` enqueues and is delivered.
- **sendCh wedged full (the pathological repro):** terminal `FAILED` is dropped
  best-effort; `Run` returns promptly; the server already shows `failed`. The
  worker slot/workspace are freed under 2s.

## Invariant-compliance argument

- **One bounded sender per gRPC stream.** Unchanged. All stream writes still go
  through the single `sendCh` consumer in `agent.connect`. Option 1 adds no new
  writer and no out-of-band send. It only changes the *blocking discipline* of
  enqueue attempts: the log path gains a third "abandon" select case and the
  forced terminal send becomes bounded. Both strictly *reduce* how long a runner
  goroutine can block on a non-reading peer - directly serving the invariant's
  stated goal ("a peer that stops reading must never block a dispatcher or HTTP
  handler indefinitely"). The forced abort removes a previously-unbounded park.

- **Epoch fence.** Untouched. No new write to `tasks.status` or `task_logs`. The
  terminal `FAILED` keeps `r.epoch`; the server's epoch-fenced consumers handle
  it exactly as before (and, post-`CancelJobTasks`, fence it out). We do not
  return a task to `pending` or bump any epoch here.

- **Identity-checked teardown.** Untouched. No registry/sender registration or
  teardown changes. `forcedCh` is per-runner state owned by the runner; closing
  it tears down only that runner's in-flight log copy.

- **No interior pointers across locks.** Untouched. `forcedCh` is set once at
  `newRunner` and never mutated; `Cancel` only closes it. No getter returns a
  pointer into shared registry state.

- **Single job-spec pipeline / single JSON entry point.** Not in scope; no
  job-spec ingestion or HTTP body handling changes.

## Soundness check (halt-signal gate)

The conductor asked for a loud halt if Option 1 is technically unsound or breaks
an invariant. It is not unsound, but design surfaced one correction that the
backlog proposal omitted: **aborting only the in-flight log write is necessary
but not sufficient.** The terminal-status send blocks on the same full `sendCh`,
so without also bounding `sendFinalStatus` on the forced path, `Run` never
returns and the test still fails. This spec incorporates that as a first-class
part of the mechanism (the "How terminal FAILED still sends" section). This is a
design refinement, not a halt: Option 1 remains sound and invariant-respecting.

## Success criteria

1. **`TestRunner_ForceCancel_ReturnsQuickly` passes under the constrained
   Linux/Docker repro.** Run the `internal/agent` package in a Linux container
   (`golang:<ver>` + mounted worktree, `MSYS_NO_PATHCONV=1` for Docker volume
   paths on Git Bash; see `feedback-platform-gated-test-verification`). The test
   is `//go:build !windows` and is silently skipped by `make test` on Windows -
   it MUST be observed green on Linux, and observed red on Linux pre-fix to
   confirm the change is load-bearing.

2. **A forced cancel still yields terminal `FAILED` when the channel has room.**
   Add or extend a unit test: force-cancel a running task with a `sendCh` that
   has capacity; assert a `TaskStatus=FAILED` (the runner's terminal status)
   appears on `sendCh`. This guards Fact 1 (the terminal send is not routed
   through the abort path) and proves the common case still reports FAILED.

3. **Existing forced/default cancel tests stay green.**
   `TestRunner_ForceCancel_SkipsWorkspaceFinalize`,
   `TestRunner_DefaultCancel_RunsWorkspaceFinalize`,
   `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang`, and the
   pipe-drain regression test all continue to pass on Linux. The forced-abort
   must not alter default-cancel drain behavior.

4. **Red-before-green is provable.** Per the pipe-drain retro's lesson, confirm
   the repro test fails pre-fix on Linux with margin (it currently fails ~3.3s
   against a 2s assertion / 3s safety net), then green post-fix.

## Risks and open questions

- **Risk: terminal FAILED dropped under a fully-wedged channel.** When `sendCh`
  is genuinely full and the connection wedged, the forced terminal send is
  dropped. Mitigation: the server already holds authoritative `failed` from
  `CancelJobTasks`, and the agent's terminal message would be epoch-fenced out
  anyway. Accepted, and consistent with the existing "best-effort" contract.
  Documented here so it is a known, deliberate behavior, not a silent gap.

- **Risk: scope creep into the log-streaming `send` contract.** The pipe-drain
  fix deliberately made `chunkWriter.Write` never tear down `io.Copy`. This
  change reintroduces a teardown path, but strictly gated on `forcedCh`. Keep the
  diff surgical: only the `forcedCh` branch returns an error; the slow-consumer
  (non-forced) path still returns `(len(p), nil)`. A reviewer must confirm the
  non-forced behavior is byte-for-byte unchanged.

- **Open question (deferred, not blocking): default (non-forced) cancel under a
  wedged channel.** A default cancel of a still-producing task whose copy
  goroutine is parked on a full `sendCh` is also not preemptible by the per-task
  context; it is bounded only by the 5s `WaitDelay`. That is the *default*
  contract ("waits briefly for log drain", up to `WaitDelay`), so it is in-spec
  for default cancel and out of scope here. Flagging it so the boundary is
  explicit: this fix changes only the *forced* path.

- **Open question: `Abandon()` interaction.** `Abandon()` (grace-expiry requeue)
  sets `abandoned` and cancels the context but is never forced, so it does not
  close `forcedCh`. A copy goroutine parked on a full `sendCh` during an
  abandon is still bounded only by `WaitDelay`. This matches today's behavior
  and is out of scope; noted for completeness.

## Files in scope

- `internal/agent/runner.go` - `forcedCh` field, `Cancel` closes it once,
  `chunkWriter.Write` forced-abort select + sentinel, `sendFinalStatus` bounded
  on the forced path. No new sender; no epoch changes.
- `internal/agent/runner_cancel_test.go` - the repro already exists
  (`TestRunner_ForceCancel_ReturnsQuickly`); add the terminal-FAILED-still-sends
  assertion (criterion 2).

Out of scope: proto, API, CLI, server-side handler, default/abandon cancel paths,
Windows-specific proctree code (the fix is platform-neutral; it lives in the
shared runner).
