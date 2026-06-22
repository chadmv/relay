# Default Cancel and Abandon Preempt a Log Write Blocked on a Full sendCh - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a default cancel (`Cancel(false)`) and a grace-expiry `Abandon()` free the worker slot promptly (well under 8s) even when the gRPC `sendCh` is wedged full, by adding a per-runner `cancelledCh` that lets the log-write path abandon an in-flight send and bounding the terminal-status send on the cancelled path.

**Architecture:** This closes the two deferred open questions from the 2026-06-19 forced-cancel fix. Today only the *forced* path escapes a wedged `sendCh` (it closes `forcedCh`, honored by `sendOrAbort`, and bounds its terminal send). The *default* and *abandon* paths have no equivalent escape, so a copy goroutine parked on `r.sendCh <- msg` inside `chunkWriter.Write` pins `cmd.Wait()` and `Run` indefinitely (the `<-r.ctx.Done()` escape is tied to the parent agent context, which `Cancel`/`Abandon` never cancel). The fix mirrors the proven `forcedCh` mechanism: add `cancelledCh chan struct{}`, closed exactly once by either `Cancel(false)` or `Abandon()` (guarded so concurrent/repeated/mixed cancels never double-close), add it as a fourth select case in `sendOrAbort`, and extend `sendFinalStatus`'s bounded best-effort try-send to fire on any per-task cancel (gated on `r.cancelled.Load()`). The forced path stays on `forcedCh` only; the normal-completion send and the Finalize gate (keyed on `r.forced`) are byte-for-byte unchanged.

**Tech Stack:** Go 1.26, standard library (`sync`, `sync/atomic`, `context`, `os/exec`), testify for assertions, Docker (`golang:1.26.2`) for running the `//go:build !windows` repro on this Windows host.

**Slice declaration:** This is **backend-only**. There is a **single engineer slice** (the `internal/agent` runner). There is **no frontend slice**. Phase 3 is therefore **NOT parallel** - tasks run sequentially in one slice by one backend engineer.

**Files in scope:**
- Modify: `internal/agent/runner.go` - add `cancelledCh chan struct{}` field + a `cancelledClose sync.Once`; allocate the channel in `newRunner`; close it once (guarded) in `Cancel(false)` and `Abandon()`; add the `<-r.cancelledCh` select case to `sendOrAbort`; extend `sendFinalStatus`'s bounded try-send to fire on `r.cancelled.Load()`.
- Modify: `internal/agent/runner_cancel_test.go` - add the default-cancel wedged-full bound test (criterion 1), the non-wedged default-cancel terminal-FAILED test (criterion 2), and the abandon wedged-full bound test (criterion 3). Existing tests are unchanged.

No other file must change. `internal/agent/export_test.go` already exports `SetProviderForTest`; `singleCmd`, `fakeHandle`, and `fakeProvider` already exist in `internal/agent/runner_test.go`. No new test hook is needed - the new tests drive `Cancel`/`Abandon` and read `sendCh` behaviorally. If a test genuinely needs to observe `cancelledCh` directly, prefer a behavioral assertion over exporting internal state; only add an export hook if behavior cannot be asserted otherwise, and justify it in the commit message.

---

## Platform-gated verification (READ FIRST - applies to several tasks below)

The cancel regression tests live in `internal/agent/runner_cancel_test.go`, which is tagged `//go:build !windows`. On this Windows dev host, `make test` and `go test ./internal/agent/...` **silently skip these tests** (they report `no tests to run` / green) - so red-before-green and green-after CANNOT be proven with `make test` on Windows. They MUST be run in a Linux Docker container against the mounted worktree (per the `feedback-platform-gated-test-verification` memory).

The worktree is at `D:\dev\relay\.claude\worktrees\dazzling-chaplygin-1f400f`. In Git Bash that absolute path is `/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f`. The `MSYS_NO_PATHCONV=1` prefix is required or Git Bash mangles the Docker volume path.

**Canonical command to run a single test in the container (use Git Bash, not PowerShell):**

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly -v -timeout 60s"
```

The `relay-gomod` named volume caches the module download across runs; the first run will populate it (slower). Docker Desktop must be running.

**On the race detector:** A `-race` run **IS a required gate** for this fix (conductor decision at the plan gate: this send/drain area has regressed twice this month, so the concurrency change gets belt-and-suspenders coverage). The race detector cannot run in this Windows env (per `reference-race-detector-toolchain`, it needs an MSYS2 mingw64 gcc toolchain that the default Strawberry Perl gcc breaks), so the `-race` pass runs inside the same Linux `golang:1.26.2` container (the image ships a working gcc). It is a required step in Task 6 (Step 1b). The concurrency surface added here (one extra `sync.Once`-guarded `close` and one extra select case on an already-closed channel) is small and structurally identical to the proven `forcedCh` path, so a clean `-race` run is expected.

---

## Sequencing rationale (read before executing)

The wedged-full default-cancel repro (criterion 1) needs **two** production changes before it can go green: the `sendOrAbort` escape (so the parked log send abandons and `cmd.Wait()` returns) AND the bounded terminal send in `sendFinalStatus` (so `Run` does not then re-park on the wedged channel sending the terminal FAILED). A test that depends on two changes cannot prove a clean single RED->GREEN cycle against either change alone.

To keep each test mapping cleanly to a verifiable transition, the tasks are ordered so the change with an independently observable effect lands first with its own test, and the two-change repro lands last:

1. **Task 2** adds the `cancelledCh` field, allocation, and guarded close in `Cancel(false)`/`Abandon()`. Structural; verified by build/vet (no standalone behavior yet).
2. **Task 3** extends `sendFinalStatus`'s bounded branch to `r.cancelled.Load()` and adds the **non-wedged** terminal-FAILED test (criterion 2). This test needs ONLY the `sendFinalStatus` change - on a roomy channel the bounded try-send delivers FAILED. It is the clean single-change guard that the bounded branch is room-first, not drop-always.
3. **Task 4** adds the `<-r.cancelledCh` case to `sendOrAbort`, then turns the **wedged-full default-cancel** repro (criterion 1) green. By now both required changes are present, so the repro flips RED (Task 4 Step 2, pre-change baseline) -> GREEN (Task 4 Step 5).
4. **Task 5** adds the **abandon wedged-full** repro (criterion 3). All production changes are already in place after Task 4, so this is purely a new test plus a RED-baseline demonstration via `git stash` of the `sendOrAbort` change. (Abandon's terminal send is already suppressed by the existing `if r.abandoned.Load() { return }` early return, so abandon needs only the `sendOrAbort` escape, which Task 4 added.)

The RED baselines for criteria 1 and 3 are captured by temporarily reverting the relevant production change in the container (instructions inline), because the bug is now confirmed and the spec (criterion 5) requires red-before-green to be *provable* on Linux, not merely asserted.

---

## Task 1: Confirm the Windows skip and the pre-fix Linux baseline hang

Establish the negative control (Windows skips the gated tests) and confirm the bug reproduces on Linux before any code is written. No test code exists for the new repros yet, so this task confirms the *existing* default-cancel test still passes and documents the trap.

**Files:**
- Run only (no edit).

- [ ] **Step 1: Confirm the Windows skip (negative control)**

Run (PowerShell or Bash on the Windows host):

```
go test ./internal/agent/ -run TestRunner_DefaultCancel_RunsWorkspaceFinalize -v -timeout 60s
```

Expected: PASS or `no tests to run` for the `!windows`-gated test - i.e. it is skipped. This is the trap the memory warns about; it confirms Windows cannot prove anything about the cancel tests.

- [ ] **Step 2: Confirm the existing default-cancel test is green on Linux (pre-fix)**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_DefaultCancel_RunsWorkspaceFinalize -v -timeout 60s"
```

Expected: **PASS.** This existing test uses a fake handle whose Finalize does not re-park, and a roomy 256-cap `sendCh`, so it exercises the non-wedged default-cancel path and is green today. It must stay green through the whole plan (it is the guard that default cancel does not become a forced cancel - Finalize still runs).

- [ ] **Step 3: No commit**

This task writes no code. Record the outputs for the retro / PR description.

---

## Task 2: Add `cancelledCh` + guarded close in `Cancel(false)` and `Abandon()`

Introduce the per-task cancel signal channel. It is allocated in `newRunner` alongside `forcedCh` and closed exactly once by either default cancel or abandon. The close must be guarded so repeated/concurrent/mixed cancels (e.g. a forced cancel followed by a default cancel on the same runner) cannot double-close it (a double close panics). Use a dedicated `cancelledClose sync.Once`.

**Wiring discipline (from the spec):** Forced cancel stays on `forcedCh` only and does NOT close `cancelledCh` (`sendOrAbort` selects on `forcedCh` independently). Default cancel and abandon close `cancelledCh` only. The `sync.Once` guard makes the close idempotent under any interleaving. The existing `r.cancelled.Store(true)` / `r.abandoned.Store(true)` / `r.cancel()` calls stay unchanged; this adds the channel close beside them.

**Files:**
- Modify: `internal/agent/runner.go:19-31` (struct), `:36-45` (`newRunner`), `:51-70` (`Cancel` and `Abandon`).

- [ ] **Step 1: This task has no standalone failing test of its own**

Adding the channel, the `sync.Once`, the allocation, and the guarded close is a structural prerequisite with no independently observable behavior yet (`cancelledCh` is closed but not yet read). Per writing-plans, a no-op structural change does not get a fabricated unit test - the verify step is `go build` + `go vet`, and the behavior is exercised by the downstream tests in Tasks 3-5. Proceed to implementation.

- [ ] **Step 2: Add the `cancelledCh` field and `cancelledClose` guard to the `Runner` struct**

In `internal/agent/runner.go`, add the two fields to the `Runner` struct (after `forcedCh`):

```go
// Runner manages the execution of a single dispatched task as a subprocess.
type Runner struct {
	taskID         string
	epoch          int64
	sendCh         chan *relayv1.AgentMessage
	ctx            context.Context // parent (agent) context — lives for the agent lifetime, not the connection
	cancel         context.CancelFunc
	cancelled      atomic.Bool
	forced         atomic.Bool
	abandoned      atomic.Bool
	forcedCh       chan struct{} // closed exactly once by Cancel(force=true); signals in-flight log writes to abandon
	cancelledCh    chan struct{} // closed exactly once by Cancel(false) or Abandon(); signals in-flight log writes to abandon on a per-task cancel
	cancelledClose sync.Once     // guards the single close of cancelledCh across mixed/repeated cancels
	provider       source.Provider
}
```

`"sync"` is already imported in `internal/agent/runner.go` (used by `makePrepareProgressFn`), so no import change is needed.

- [ ] **Step 3: Allocate `cancelledCh` in `newRunner`**

In `internal/agent/runner.go`, change the `Runner` literal returned by `newRunner` to allocate the new channel alongside `forcedCh`:

```go
	return &Runner{
		taskID:      taskID,
		epoch:       epoch,
		sendCh:      sendCh,
		ctx:         parent,
		cancel:      cancel,
		forcedCh:    make(chan struct{}),
		cancelledCh: make(chan struct{}),
	}, runCtx
```

`cancelledClose` is a zero-value `sync.Once` and needs no explicit initialization.

- [ ] **Step 4: Close `cancelledCh` once in `Cancel(false)` and `Abandon()`**

In `internal/agent/runner.go`, replace the bodies of `Cancel` and `Abandon`. `Cancel` closes `cancelledCh` on its non-forced exit (and the `sync.Once` keeps it safe even if a forced cancel already ran or a later default cancel arrives). `Abandon` closes it too:

```go
// Cancel signals the subprocess to stop. The task is reported as FAILED.
// If force is true, the runner skips workspace finalize, bypasses pipe drain
// when killing the subprocess, and closes forcedCh so in-flight log writes
// abandon instead of parking on a full sendCh. A non-forced (default) cancel
// closes cancelledCh, which gives in-flight log writes the same per-task escape
// without skipping workspace finalize.
func (r *Runner) Cancel(force bool) {
	if force {
		// CompareAndSwap guarantees exactly one forced caller closes forcedCh,
		// even under concurrent or repeated Cancel(true) / mixed forced and
		// non-forced cancels. Closing a channel twice panics; this gate prevents it.
		if r.forced.CompareAndSwap(false, true) {
			close(r.forcedCh)
		}
	}
	// Both cancel kinds free a parked log send via cancelledCh. The sync.Once
	// makes this safe under repeated, concurrent, or mixed forced/default/abandon
	// calls on the same runner.
	r.cancelledClose.Do(func() { close(r.cancelledCh) })
	r.cancelled.Store(true)
	r.cancel()
}

// Abandon is like Cancel but suppresses the final status message. Used when
// the coordinator's RegisterResponse.cancel_task_ids indicates this task was
// reassigned to another worker during a grace-expiry requeue.
func (r *Runner) Abandon() {
	r.abandoned.Store(true)
	r.cancelledClose.Do(func() { close(r.cancelledCh) })
	r.cancel()
}
```

Note: closing `cancelledCh` from `Cancel(true)` as well as `Cancel(false)` is harmless (the `sync.Once` guarantees one close) and keeps the close unconditional and simple. The spec's "forced -> `forcedCh` only" wiring is about which signal `sendOrAbort` *selects* on for the forced escape; closing `cancelledCh` on the forced path too does not change forced behavior because the forced terminal-send gate and the Finalize-skip gate both key off `r.forced`, not `cancelledCh`. Keeping the close unconditional avoids a second guarded branch and is the minimal diff. Abandon does not set `r.cancelled` (it sets `r.abandoned`); the existing `if r.abandoned.Load() { return }` early return in `sendFinalStatus` already handles abandon's no-terminal-send behavior.

- [ ] **Step 5: Verify it builds and vets**

Run:

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output. (`cancelledCh` is allocated and closed but not yet read; Go does not warn on an unread struct field, so this is clean.)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/runner.go
git commit -m "agent: add per-runner cancelledCh, closed once on default cancel and abandon"
```

---

## Task 3: Bound the terminal send on a per-task cancel + non-wedged terminal-FAILED test

After a default cancel, `Run` calls `sendFinalStatus(FAILED)`. With the unchanged blocking two-case `send`, that would block on a still-wedged `sendCh` (parent context still alive) and `Run` would never return - the bug would persist on the terminal-send half. So `sendFinalStatus`'s bounded best-effort try-send must fire on a per-task cancel, not only on the forced path.

This task adds criterion 2's test (non-wedged default cancel still delivers terminal FAILED) because that test needs ONLY this change: on a roomy channel the bounded try-send succeeds and FAILED is delivered. This is the clean single-change RED->GREEN guard that the bounded branch is room-first, not drop-always.

**Gate predicate (from the spec, load-bearing):** The early `if r.abandoned.Load() { return }` already removes the abandon case before any send decision. `Cancel(false)` sets `r.cancelled`; `Cancel(true)` sets both `r.forced` and `r.cancelled`. So gating the bounded branch on `r.cancelled.Load()` alone covers both cancel kinds, and the existing forced behavior is preserved (a forced cancel still takes the bounded branch because it sets `r.cancelled`). The normal-completion send (`r.send(msg)`) stays the blocking two-case send, byte-for-byte. Do NOT bound the normal path.

**Files:**
- Modify: `internal/agent/runner.go:293-323` (`sendFinalStatus`).
- Test: `internal/agent/runner_cancel_test.go` (add `TestRunner_DefaultCancel_NonWedged_SendsTerminalFailed`).

- [ ] **Step 1: Write the failing test**

Add this test to `internal/agent/runner_cancel_test.go` (already `//go:build !windows`; it uses `sleep`, available in the container). It default-cancels a running task whose `sendCh` has ample room and asserts a terminal `FAILED` is enqueued. It uses a fake provider/handle so the default-cancel Finalize path runs without re-parking, exactly like `TestRunner_DefaultCancel_RunsWorkspaceFinalize`:

```go
// TestRunner_DefaultCancel_NonWedged_SendsTerminalFailed guards spec criterion 2:
// the bounded terminal send is room-first, not drop-always. With a sendCh that has
// room, a default cancel must still report a terminal FAILED, mirroring
// TestRunner_ForceCancel_StillSendsTerminalFailed for the default path.
func TestRunner_DefaultCancel_NonWedged_SendsTerminalFailed(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4096)
	fh := &fakeHandle{dir: t.TempDir()}
	prov := &fakeProvider{handle: fh}

	task := &relayv1.DispatchTask{
		TaskId:   "t-default-term",
		JobId:    "j-default-term",
		Commands: singleCmd([]string{"sleep", "30"}),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	r.SetProviderForTest(prov)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	time.Sleep(200 * time.Millisecond) // let the subprocess start
	r.Cancel(false)

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runner did not return within 8s after default cancel")
	}

	// Drain sendCh and assert a terminal FAILED was reported.
	var sawFailed bool
	for {
		select {
		case msg := <-sendCh:
			if ts := msg.GetTaskStatus(); ts != nil &&
				ts.Status == relayv1.TaskStatus_TASK_STATUS_FAILED {
				sawFailed = true
			}
		default:
			require.True(t, sawFailed,
				"default cancel with room on sendCh must still report terminal FAILED")
			return
		}
	}
}
```

- [ ] **Step 2: Run the new test in the container at the pre-change state**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_DefaultCancel_NonWedged_SendsTerminalFailed -v -timeout 60s"
```

Expected: With Task 2 applied but `sendFinalStatus` not yet changed, this test should **PASS** on its own (the channel has room, so the unchanged blocking `send` enqueues FAILED fine, and the fake handle's Finalize does not re-park). That is acceptable: this test's role is the room-first regression guard, not a red-driver - the bounded change in Step 3 must keep it green while NOT regressing the wedged case (covered by Task 4). Record the observed result. If it FAILS here, stop and investigate before changing `sendFinalStatus`.

- [ ] **Step 3: Extend the bounded branch in `sendFinalStatus` to per-task cancel**

In `internal/agent/runner.go`, replace `sendFinalStatus`. Change the gate from `r.forced.Load()` to `r.cancelled.Load()` (which covers both forced and default cancel; abandon already returned at the top). The bounded try-send becomes the `r.sendCh <- msg` / `default` non-blocking form (it must not select on `forcedCh`, because a default cancel does not close `forcedCh`):

```go
func (r *Runner) sendFinalStatus(status relayv1.TaskStatus, exitCode *int32) {
	if r.abandoned.Load() {
		return // coordinator reassigned this task; suppress final status
	}
	msg := &relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{TaskStatus: &relayv1.TaskStatusUpdate{
			TaskId:   r.taskID,
			Status:   status,
			ExitCode: exitCode,
			Epoch:    r.epoch,
		}},
	}
	// Per-task cancel (forced OR default): best-effort, bounded enqueue so Run
	// returns even when sendCh is wedged full. Cancel(true) sets r.forced AND
	// r.cancelled; Cancel(false) sets r.cancelled; Abandon set r.abandoned and
	// already returned above. So r.cancelled covers both cancel kinds. Try the
	// enqueue first and only abandon when sendCh is genuinely full; dropping the
	// message is safe because the server's CancelJobTasks already set the task
	// failed and bumped assignment_epoch, so this terminal message (carrying the
	// old r.epoch) is epoch-fenced out.
	if r.cancelled.Load() {
		select {
		case r.sendCh <- msg:
		default:
			// sendCh full and wedged; abandon best-effort. Server is authoritative.
		}
		return
	}
	r.send(msg)
}
```

Note: the previous forced-path branch used `select { case r.sendCh <- msg: case <-r.forcedCh: }` (relying on `forcedCh` being already closed). The `default:` form is equivalent for the wedged case and is required now because the default-cancel path does not close `forcedCh`. The forced path still takes this branch (it sets `r.cancelled`), so forced behavior is preserved - `TestRunner_ForceCancel_StillSendsTerminalFailed` and `TestRunner_ForceCancel_ReturnsQuickly` must stay green (verified in Task 6).

- [ ] **Step 4: Run the non-wedged terminal-FAILED test in the container and verify it passes**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_DefaultCancel_NonWedged_SendsTerminalFailed -v -timeout 60s"
```

Expected: **PASS.** A default cancel on a roomy `sendCh` still reports terminal FAILED.

- [ ] **Step 5: Verify build and vet**

Run:

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_cancel_test.go
git commit -m "agent: bound terminal send on per-task cancel; add default-cancel room-first guard test"
```

---

## Task 4: Add the `cancelledCh` escape to `sendOrAbort` + wedged-full default-cancel repro

This is the load-bearing fix for the unbounded hang. `sendOrAbort` (the path exec's `io.Copy` drives via `chunkWriter.Write`, gating `cmd.Wait()`) gains a fourth select case on `<-r.cancelledCh`, so a default cancel or abandon frees a log send parked on a full `sendCh`. With Task 3's bounded terminal send already in place, the default-cancel wedged-full repro (criterion 1) can now go green.

**Surgical-changes note:** Do NOT change `r.send` (the shared two-case helper). Step markers, prepare progress, and inventory keep using it. The abort is added only to the dedicated `sendOrAbort` path used by `chunkWriter.Write`. This keeps the one-bounded-sender invariant intact (no new writer; all writes still go through `r.sendCh`) and strictly shortens the worst-case park. The non-cancel slow-consumer path is byte-for-byte unchanged: with neither `forcedCh` nor `cancelledCh` closed, `sendOrAbort` still blocks on `r.sendCh <- msg` or `<-r.ctx.Done()` exactly as before.

**Files:**
- Modify: `internal/agent/runner.go:338-349` (`sendOrAbort`), plus the `errForcedAbort` doc comment (`:244-250`) and the `chunkWriter` doc comment (`:252-259`) to widen "forced cancel" to "any per-task cancel" (comment-only).
- Test: `internal/agent/runner_cancel_test.go` (add `TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly`).

- [ ] **Step 1: Write the wedged-full default-cancel repro test**

Add this test to `internal/agent/runner_cancel_test.go`. It floods an undrained `sendCh` until `len == cap` so a copy goroutine parks on the send, runs a continuous-output subprocess, then `Cancel(false)`, and asserts `Run` returns within the bound. It asserts the **return bound only** - terminal status may legitimately be dropped under wedged-full, so it makes no terminal-status assertion. No fake provider is needed (no `Source` on the task), so the default-cancel `defer` Finalize block is not entered and cannot re-park - this isolates the asserted bound to the subprocess/log-send path per the spec's out-of-scope `sendInventory`-under-wedge note:

```go
// TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly is spec criterion 1: with
// sendCh wedged full and the subprocess still producing output, a default cancel
// must free Run within the bound instead of hanging unbounded on a parked log
// send. Return-bound assertion only; terminal status may be dropped when wedged.
func TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly(t *testing.T) {
	// Small cap so we can wedge it full quickly. No consumer ever drains it.
	sendCh := make(chan *relayv1.AgentMessage, 4)

	task := &relayv1.DispatchTask{
		TaskId:   "t-default-wedge",
		JobId:    "j-default-wedge",
		Commands: singleCmd([]string{"sh", "-c", "while true; do echo x; done"}),
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	// Wait until sendCh is wedged full: the continuous-output subprocess fills the
	// buffer and a copy goroutine parks on r.sendCh <- msg inside chunkWriter.Write.
	deadline := time.Now().Add(5 * time.Second)
	for len(sendCh) < cap(sendCh) {
		if time.Now().After(deadline) {
			t.Fatal("sendCh never filled to capacity; cannot reproduce the wedged-full park")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	r.Cancel(false)

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("default cancel hung: Run did not return within 8s with sendCh wedged full")
	}
	require.Less(t, time.Since(start), 8*time.Second,
		"default cancel should free Run well under the unbounded hang; took %s", time.Since(start))
}
```

- [ ] **Step 2: Establish the RED baseline on Linux (escape not yet added)**

The production `sendOrAbort` change is not yet applied (only Tasks 2-3 are committed), so run the new test now to confirm it is RED at this state. Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly -v -timeout 60s"
```

Expected: **FAIL** - the runner hangs and the test's `time.After(8 * time.Second)` fires with "default cancel hung". This proves the bug reproduces and that Task 3's bounded terminal send alone is NOT sufficient (the log send is still parked, pinning `cmd.Wait()`). Capture the failure output as the RED proof for criterion 1 / criterion 5.

- [ ] **Step 3: Add the `<-r.cancelledCh` select case to `sendOrAbort`**

In `internal/agent/runner.go`, replace `sendOrAbort`:

```go
// sendOrAbort enqueues a log chunk like send, but additionally abandons the
// enqueue if a forced cancel (forcedCh) or a per-task default cancel / abandon
// (cancelledCh) has signalled, or the agent context is done. It returns true on a
// successful enqueue and false if it abandoned. Only chunkWriter.Write uses this;
// all other callers use send so their blocking discipline is unchanged.
func (r *Runner) sendOrAbort(msg *relayv1.AgentMessage) bool {
	select {
	case r.sendCh <- msg:
		return true
	case <-r.ctx.Done():
		// Agent shutdown; will be redelivered when the agent reconnects.
		return false
	case <-r.forcedCh:
		// Forced cancel in progress; abandon this chunk so cmd.Wait can return.
		return false
	case <-r.cancelledCh:
		// Default cancel or abandon in progress; abandon this chunk so cmd.Wait
		// can return instead of parking unbounded on a wedged sendCh.
		return false
	}
}
```

- [ ] **Step 4: Widen the comment-only references from "forced cancel" to "per-task cancel"**

In `internal/agent/runner.go`, update two doc comments (behavior already changed in Step 3; this is clarification only, not a code change):

In the `errForcedAbort` declaration comment, change the opening so it no longer reads as forced-only:

```go
// errForcedAbort is returned by chunkWriter.Write when a per-task cancel
// (forced via forcedCh, or default/abandon via cancelledCh) signals while a log
// send is in flight, or the agent context is done. A non-nil Write error makes
// exec's io.Copy stop copying so cmd.Wait() returns promptly instead of waiting
// out WaitDelay. It is consumed only by exec's copy loop; the runner's terminal
// status is decided independently in Run (the cancelled branch yields FAILED),
// so this sentinel never leaks as an extra task failure.
var errForcedAbort = errors.New("relay: forced cancel aborted in-flight log write")
```

In the `chunkWriter` type doc comment, change the trailing sentence about forced cancel to:

```go
// (len(p), nil) so exec keeps copying until EOF (unchanged slow-consumer
// behavior). If a per-task cancel has closed r.forcedCh or r.cancelledCh (or the
// agent context is done), the enqueue is abandoned and Write returns
// errForcedAbort so exec's io.Copy stops and cmd.Wait() returns promptly instead
// of waiting out WaitDelay.
```

The sentinel variable name stays `errForcedAbort` (widening its meaning is a comment-only clarification, not a rename - per the spec). The empty-chunk guard at the top of `Write` stays.

- [ ] **Step 5: Run the wedged-full repro in the container and verify it passes**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_DefaultCancel_WedgedFull_ReturnsQuickly -v -timeout 60s"
```

Expected: **PASS.** With both the `sendOrAbort` escape (this task) and the bounded terminal send (Task 3) present, `Run` returns well under 8s. Compare the elapsed time against the hang captured in Step 2 to confirm the margin.

- [ ] **Step 6: Verify build and vet**

Run:

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_cancel_test.go
git commit -m "agent: abort in-flight log write on default cancel/abandon via cancelledCh; add wedged-full repro"
```

---

## Task 5: Abandon wedged-full repro (criterion 3)

`Abandon()` closes `cancelledCh` (Task 2) and its terminal send is already suppressed by the existing `if r.abandoned.Load() { return }` early return in `sendFinalStatus`. So all production changes abandon needs are already present after Task 4. This task adds criterion 3's test and demonstrates its RED baseline.

**Files:**
- Test: `internal/agent/runner_cancel_test.go` (add `TestRunner_Abandon_WedgedFull_ReturnsQuickly`). No production change.

- [ ] **Step 1: Write the abandon wedged-full repro test**

Add this test to `internal/agent/runner_cancel_test.go`. Same flood-and-park setup as Task 4's test, but calls `Abandon()` and makes no terminal-status assertion (abandon suppresses terminal status):

```go
// TestRunner_Abandon_WedgedFull_ReturnsQuickly is spec criterion 3: with sendCh
// wedged full and the subprocess still producing output, Abandon() (grace-expiry
// requeue) must free Run within the bound instead of hanging unbounded on a
// parked log send. No terminal-status assertion: abandon suppresses terminal
// status via the r.abandoned early return in sendFinalStatus.
func TestRunner_Abandon_WedgedFull_ReturnsQuickly(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4)

	task := &relayv1.DispatchTask{
		TaskId:   "t-abandon-wedge",
		JobId:    "j-abandon-wedge",
		Commands: singleCmd([]string{"sh", "-c", "while true; do echo x; done"}),
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	deadline := time.Now().Add(5 * time.Second)
	for len(sendCh) < cap(sendCh) {
		if time.Now().After(deadline) {
			t.Fatal("sendCh never filled to capacity; cannot reproduce the wedged-full park")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	r.Abandon()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("abandon hung: Run did not return within 8s with sendCh wedged full")
	}
	require.Less(t, time.Since(start), 8*time.Second,
		"abandon should free Run well under the unbounded hang; took %s", time.Since(start))
}
```

- [ ] **Step 2: Demonstrate the RED baseline by temporarily reverting the `sendOrAbort` escape**

Criterion 5 requires red-before-green to be provable. The production fix is already committed, so to show RED, temporarily revert ONLY the `<-r.cancelledCh` case from `sendOrAbort` in the worktree (comment it out or delete the two lines), then run the new test. Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_Abandon_WedgedFull_ReturnsQuickly -v -timeout 60s"
```

Expected: **FAIL** - "abandon hung". This proves the test exercises the escape. Then restore the `<-r.cancelledCh` case (revert the temporary edit) before Step 3:

```bash
git checkout -- internal/agent/runner.go
```

(Use `git checkout -- internal/agent/runner.go` only because the `sendOrAbort` change is already committed; this restores the committed fix. Do NOT discard the new test - it is unstaged in `runner_cancel_test.go` and `git checkout -- runner.go` leaves the test file untouched.)

- [ ] **Step 3: Run the abandon repro at the fixed state and verify it passes**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_Abandon_WedgedFull_ReturnsQuickly -v -timeout 60s"
```

Expected: **PASS.** `Abandon()` frees `Run` well under 8s.

- [ ] **Step 4: Verify build and vet**

Run:

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner_cancel_test.go
git commit -m "agent: add abandon wedged-full bound regression test"
```

---

## Task 6: Full agent cancel suite + build + vet (final verification)

Confirm the whole forced/default/abandon/leaked-child cancel suite stays green on Linux (spec criterion 4), and the tree builds and vets. The forced path, the default-cancel Finalize path, and the leaked-child WaitDelay behavior must not have regressed.

**Files:**
- Run only (no edit).

- [ ] **Step 1: Run the full `internal/agent` package on Linux**

Run (Git Bash). This runs every test in the package, including all Unix-gated cancel tests - `TestRunner_ForceCancel_SkipsWorkspaceFinalize`, `TestRunner_DefaultCancel_RunsWorkspaceFinalize`, `TestRunner_ForceCancel_ReturnsQuickly`, `TestRunner_ForceCancel_StillSendsTerminalFailed`, `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang`, and the three new tests:

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -v -timeout 180s"
```

Expected: **PASS** for the entire package - all cancel tests green, no regression in forced cancel, default-cancel finalize, or leaked-child WaitDelay behavior.

- [ ] **Step 1b: Run the `internal/agent` package under the race detector on Linux (REQUIRED gate)**

Per the conductor's plan-gate decision, a clean `-race` pass is required for this concurrency change. Run (Git Bash); `-race` needs cgo, which the `golang:1.26.2` image's gcc supports:

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/dazzling-chaplygin-1f400f:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "CGO_ENABLED=1 go test -race ./internal/agent/ -timeout 300s"
```

Expected: **PASS with no DATA RACE reports.** This exercises the new `cancelledCh` close/select under the detector. If any race is reported, stop and route it back to the engineer before proceeding - do not treat a race report as flaky.

- [ ] **Step 2: Build the whole tree**

Run (Windows host is fine for build/vet):

```
go build ./...
```

Expected: success, no output.

- [ ] **Step 3: Vet the whole tree**

Run:

```
go vet ./...
```

Expected: success, no output.

- [ ] **Step 4: Run the Windows unit suite (sanity, no Docker)**

Run:

```
make test
```

Expected: PASS. Note: this does NOT exercise the Unix-gated cancel tests (skipped on Windows) - the Linux run in Step 1 is the authoritative proof for those. This step only confirms nothing else in the tree broke.

- [ ] **Step 5: No code commit; this is verification only**

If Steps 1-4 all pass, the implementation is complete. No new commit unless a fix was required (in which case re-run the relevant container test before committing the fix).

---

## Self-Review

**Spec coverage:**
- New `cancelledCh` field + allocation in `newRunner` -> Task 2 Steps 2-3. (spec Mechanism step 1)
- `Cancel(false)` AND `Abandon()` close it exactly once, guarded against double-close -> Task 2 Step 4 (`sync.Once`). (spec Mechanism step 2)
- Forced path keeps `forcedCh` only; the guard is safe under mixed forced-then-default cancels -> Task 2 Step 4 note. (spec Mechanism step 2 note)
- `sendOrAbort` gains the 4th `<-r.cancelledCh` select case -> Task 4 Step 3. (spec Mechanism step 3)
- All other callers (`send`) unchanged -> Task 4 surgical note; `send` is never edited. (spec Mechanism step 4)
- `sendFinalStatus` bounded branch fires on per-task cancel (gated `r.cancelled.Load()`), normal completion unchanged, abandon early-returns, default still runs Finalize -> Task 3 Step 3 + the predicate table. (spec "Terminal-status gating predicate")
- Finalize gate (keyed on `r.forced`) untouched -> not edited in any task; default cancel keeps Finalize (guarded by `TestRunner_DefaultCancel_RunsWorkspaceFinalize` staying green, Task 1 Step 2 + Task 6 Step 1). (spec "Default cancel stays a default cancel")
- Criterion 1 (default-cancel wedged-full bound) -> Task 4 Steps 1-5 (RED at Step 2, GREEN at Step 5).
- Criterion 2 (non-wedged default cancel delivers FAILED) -> Task 3 Steps 1-4.
- Criterion 3 (abandon wedged-full bound) -> Task 5.
- Criterion 4 (existing suite stays green) -> Task 6 Step 1.
- Criterion 5 (red-before-green provable on Linux/Docker) -> Task 4 Step 2 (default RED) and Task 5 Step 2 (abandon RED via temporary revert), green confirmations at Task 4 Step 5 / Task 5 Step 3. Windows-skip trap called out in the Platform-gated preamble and Task 1 Step 1.
- Invariant - one bounded sender per gRPC stream -> no new writer; all writes still go through `r.sendCh`; both changes strictly shorten the worst-case park (Task 4 surgical note, spec "Invariant-compliance").
- Invariant - epoch fence untouched -> no `tasks.status` / `task_logs` query change; terminal keeps `r.epoch`; dropped wedged terminal is epoch-fenced out (noted Task 3 Step 3 comment).

**Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to Task N". Every code step shows real code. The single no-new-unit-test step (Task 2 Step 1) is explicitly justified by the integration-level repros and downstream tests, and states its verification mechanism (build/vet), so it is not a placeholder.

**Type/name consistency:** `cancelledCh chan struct{}`, `cancelledClose sync.Once`, `r.cancelled` / `r.forced` / `r.abandoned` (existing `atomic.Bool`), `forcedCh`, `errForcedAbort`, `sendOrAbort`, `sendFinalStatus`, `chunkWriter.Write`, `r.send`, `singleCmd`, `fakeHandle`, `fakeProvider`, `SetProviderForTest` - all consistent across Tasks 2-5 and match existing identifiers in `internal/agent/runner.go`, `runner_test.go`, and `export_test.go`. The `r.cancelled.Load()` gate in Task 3 is set by `Cancel` (both kinds) but NOT by `Abandon` (which sets `r.abandoned` and is removed by the early return), so the bounded branch reaches exactly the forced-and-default cancels, never abandon - matching the predicate table.

---

## Execution Handoff

Plan complete. This is a single backend slice (no frontend, no parallelism). Execute via **subagent-driven-development** (REQUIRED SUB-SKILL: superpowers:subagent-driven-development): a fresh subagent per task with two-stage review between tasks. Tasks run sequentially in the order above; the sequencing rationale section explains why Task 3 (terminal-send bound + non-wedged test) precedes Task 4 (sendOrAbort escape + wedged repro).
