# Forced Cancel Preempts a Log Write Blocked on a Full sendCh - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a forced cancel (`relay cancel <job> --force`) free the worker slot in well under exec's 5s `WaitDelay` even when the gRPC `sendCh` is full, by adding a per-runner `forcedCh` that lets the log-write path abandon an in-flight send and bounds the terminal FAILED send on the forced path.

**Architecture:** Add a per-runner signal channel `forcedCh chan struct{}`, closed exactly once on a forced `Cancel`. `chunkWriter.Write` (the path exec's `io.Copy` drives and that gates `cmd.Wait`) selects on `forcedCh` and returns a package-level sentinel error so `io.Copy` stops and `cmd.Wait()` returns promptly. `sendFinalStatus` becomes bounded on the forced path (select on `sendCh <-` vs the already-closed `forcedCh`) so `Run` returns even when `sendCh` is wedged - safe because the server already holds the authoritative `failed` state (handleCancelJob runs `CancelJobTasks` + epoch bump before the gRPC cancel) and the stale terminal message is epoch-fenced out. The non-forced log path and the non-forced terminal send are byte-for-byte unchanged.

**Tech Stack:** Go 1.26, standard library (`sync/atomic`, `context`, `os/exec`), testify for assertions, Docker (`golang:1.26.2`) for running the `//go:build !windows` repro on this Windows host.

**Slice declaration:** This is **backend-only**. There is a **single engineer slice** (the `internal/agent` runner). There is **no frontend slice**. Phase 3 is therefore **NOT parallel** - tasks run sequentially in one slice.

**Files in scope:**
- Modify: `internal/agent/runner.go` - `forcedCh` field, allocation in `newRunner`, close-once in `Cancel`, `errForcedAbort` sentinel, `chunkWriter.Write` forced-abort select, `sendFinalStatus` bounded forced send.
- Modify: `internal/agent/runner_cancel_test.go` - add the terminal-FAILED-still-sends test (spec criterion 2). The repro `TestRunner_ForceCancel_ReturnsQuickly` already exists; no change to it.

No other file genuinely must change. `internal/agent/export_test.go` already exports `SetProviderForTest`; no new test hook is needed (the new test does not need the abort channel exported - it drives `Cancel(true)` and reads `sendCh`). If during implementation a test genuinely needs to observe `forcedCh` directly, prefer a behavioral assertion over exporting internal state; only add an export hook if behavior cannot be asserted otherwise, and justify it in the commit message.

---

## Platform-gated verification (READ FIRST - applies to several tasks below)

`TestRunner_ForceCancel_ReturnsQuickly` and the sibling forced/default/leaked-child tests live in `internal/agent/runner_cancel_test.go`, which is tagged `//go:build !windows`. On this Windows dev host, `make test` and `go test ./internal/agent/...` **silently skip these tests** (they report `no tests to run` / green) - so red-before-green and green-after CANNOT be proven with `make test` on Windows. They MUST be run in a Linux Docker container against the mounted worktree.

The worktree is at `D:\dev\relay\.claude\worktrees\suspicious-beaver-5f66ef`. In Git Bash that absolute path is `/d/dev/relay/.claude/worktrees/suspicious-beaver-5f66ef`. The `MSYS_NO_PATHCONV=1` prefix is required or Git Bash mangles the Docker volume path.

**Canonical command to run a single test in the container (use Git Bash, not PowerShell):**

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/suspicious-beaver-5f66ef:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_ForceCancel_ReturnsQuickly -v -timeout 60s"
```

The `relay-gomod` named volume caches the module download across runs; the first run will populate it (slower). Docker Desktop must be running.

---

## Task 1: Prove the repro is red on Linux (pre-fix baseline)

This is the failing-test (red) step. The repro test already exists in the worktree, so no code is written here - this task establishes that the bug reproduces before any fix, on the platform that can actually run it. Per the platform-gated-test-verification memory and spec criterion 4, red-before-green must be observed, not asserted by inspection.

**Files:**
- Run (no edit): `internal/agent/runner_cancel_test.go::TestRunner_ForceCancel_ReturnsQuickly`

- [ ] **Step 1: Confirm the Windows skip (negative control)**

Run (PowerShell or Bash on the Windows host):

```
go test ./internal/agent/ -run TestRunner_ForceCancel_ReturnsQuickly -v -timeout 60s
```

Expected: PASS or `no tests to run` for the `!windows`-gated test - i.e. it is skipped. This is the trap the memory warns about; it confirms Windows cannot prove anything here.

- [ ] **Step 2: Run the repro in the Linux container at the current (pre-fix) state**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/suspicious-beaver-5f66ef:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_ForceCancel_ReturnsQuickly -v -timeout 60s"
```

Expected: **FAIL.** The runner cannot return under the asserted 2s; the test's 3s safety net (`time.After(3 * time.Second)` then `t.Fatal("forced cancel did not return within 3s")`) fires, or the `require.Less(t, elapsed, 2*time.Second, ...)` assertion fails at ~3.3s. Capture the elapsed time / failure message; it documents the margin (criterion 4 wants the failure to have margin against the 2s assertion).

- [ ] **Step 3: No commit**

This task writes no code. Do not commit. Record the observed failure output in the execution log / task notes so the green run in Task 5 can be compared against it.

---

## Task 2: Add the `forcedCh` field, allocate it, and close it once in `Cancel`

Introduce the per-runner forced-abort signal. It is allocated in `newRunner` and closed exactly once on the forced path of `Cancel`. The close must be guarded so repeated or concurrent cancels - and a forced cancel after a non-forced cancel, or vice versa - cannot double-close the channel (a double close panics).

**Close-once discipline:** `forcedCh` is closed only on the `force == true` path, and only once. Use the existing `r.forced atomic.Bool` with `CompareAndSwap(false, true)`: the goroutine that wins the swap is the unique first forced caller and is the one that closes the channel. A non-forced `Cancel(false)` never closes it. `Abandon()` never closes it (it is never forced - see spec open question). This makes the close idempotent under any interleaving of `Cancel(true)`, `Cancel(false)`, repeated `Cancel(true)`, and `Abandon()`.

**Files:**
- Modify: `internal/agent/runner.go:19-29` (struct), `:34-43` (`newRunner`), `:48-54` (`Cancel`)

- [ ] **Step 1: This task has no standalone failing test of its own**

The behavior introduced here is exercised by the repro test (Task 1 / Task 5) and the new terminal-FAILED test (Task 4). Adding an unused channel and a guarded close is a structural prerequisite with no independently observable behavior yet, so we proceed directly to the implementation and rely on `go build` / `go vet` plus the downstream tests. (Granularity note: per writing-plans, a no-op structural change does not get a fabricated unit test - the verify step is `go build` + `go vet`.)

- [ ] **Step 2: Add the `forcedCh` field to the `Runner` struct**

In `internal/agent/runner.go`, add the field to the `Runner` struct (after `abandoned atomic.Bool`):

```go
// Runner manages the execution of a single dispatched task as a subprocess.
type Runner struct {
	taskID    string
	epoch     int64
	sendCh    chan *relayv1.AgentMessage
	ctx       context.Context // parent (agent) context — lives for the agent lifetime, not the connection
	cancel    context.CancelFunc
	cancelled atomic.Bool
	forced    atomic.Bool
	abandoned atomic.Bool
	forcedCh  chan struct{} // closed exactly once by Cancel(force=true); signals in-flight log writes to abandon
	provider  source.Provider
}
```

- [ ] **Step 3: Allocate `forcedCh` in `newRunner`**

In `internal/agent/runner.go`, change the `Runner` literal returned by `newRunner` to allocate the channel:

```go
	return &Runner{
		taskID:   taskID,
		epoch:    epoch,
		sendCh:   sendCh,
		ctx:      parent,
		cancel:   cancel,
		forcedCh: make(chan struct{}),
	}, runCtx
```

- [ ] **Step 4: Close `forcedCh` exactly once on the forced path in `Cancel`**

In `internal/agent/runner.go`, replace the body of `Cancel`:

```go
// Cancel signals the subprocess to stop. The task is reported as FAILED.
// If force is true, the runner skips workspace finalize, bypasses pipe drain
// when killing the subprocess, and closes forcedCh so in-flight log writes
// abandon instead of parking on a full sendCh.
func (r *Runner) Cancel(force bool) {
	if force {
		// CompareAndSwap guarantees exactly one forced caller closes forcedCh,
		// even under concurrent or repeated Cancel(true) / mixed forced and
		// non-forced cancels. Closing a channel twice panics; this gate prevents it.
		if r.forced.CompareAndSwap(false, true) {
			close(r.forcedCh)
		}
	}
	r.cancelled.Store(true)
	r.cancel()
}
```

Note: this replaces the previous `if force { r.forced.Store(true) }`. The `CompareAndSwap` subsumes that store - on the winning path `forced` is set true and the channel is closed atomically with respect to other forced callers.

- [ ] **Step 5: Verify it builds and vets**

Run:

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output. (At this point `forcedCh` is allocated and closed but not yet read; Go does not warn on an unread struct field, so this is clean.)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/runner.go
git commit -m "agent: add per-runner forcedCh, closed once on forced cancel"
```

---

## Task 3: Make `chunkWriter.Write` abort on `forcedCh` via a sentinel error

The path that gates `cmd.Wait` is exec's copy goroutine calling `chunkWriter.Write` -> `r.send`. Give the log-write path a three-case select so a forced cancel makes the in-flight send abandon and `Write` returns a non-nil sentinel error, which stops `io.Copy` and unblocks `cmd.Wait()`.

**Sentinel definition and handling:** `errForcedAbort` is a package-level sentinel `error` declared in `internal/agent/runner.go`. It is returned ONLY from `chunkWriter.Write`, and exec's `io.Copy` is its only consumer - exec uses a non-nil `Write` error solely to stop copying and unblock `Wait`; it does not propagate that error into `cmd.Wait()`'s return as a task-level failure. The runner's final status is decided separately in `Run` (the `r.cancelled.Load()` branch already yields `TASK_STATUS_FAILED` on a cancel). So `errForcedAbort` does not leak as a spurious task failure: it produces exactly the intended FAILED that a forced cancel already produces, no more. The sentinel must NOT be returned from any non-forced branch of `Write` - the slow-consumer path still returns `(len(p), nil)` byte-for-byte as today (spec risk: "non-forced behavior is byte-for-byte unchanged").

**Surgical-changes note:** Do NOT change `r.send` (the shared two-case helper). Step markers, prepare progress, and inventory keep using it unchanged. The abort is a NEW dedicated path used only by `chunkWriter.Write`. This keeps the one-bounded-sender invariant intact (no new writer; all writes still go through `r.sendCh`) and the diff surgical.

**Files:**
- Modify: `internal/agent/runner.go` - add `errForcedAbort` sentinel and `sendOrAbort` helper near `send`; rewrite `chunkWriter.Write`.

- [ ] **Step 1: This task is verified by the repro test, not a new unit test**

The forced-abort behavior is exactly what `TestRunner_ForceCancel_ReturnsQuickly` (Task 1 / Task 5) measures end-to-end: with the abort wired in, the forced cancel returns under 2s. Rather than fabricate a narrow `chunkWriter.Write`-returns-error unit test (which would duplicate the integration-level proof and couple the test to the sentinel's identity), we rely on the repro test flipping red -> green in Task 5. Proceed to implementation.

- [ ] **Step 2: Declare the sentinel error**

In `internal/agent/runner.go`, add an `errors` import and a package-level sentinel. Place the sentinel declaration just above the `chunkWriter` type definition:

```go
// errForcedAbort is returned by chunkWriter.Write when a forced cancel closes
// r.forcedCh while a log send is in flight. A non-nil Write error makes exec's
// io.Copy stop copying so cmd.Wait() returns promptly instead of waiting out
// WaitDelay. It is consumed only by exec's copy loop; the runner's terminal
// status is decided independently in Run (the cancelled branch yields FAILED),
// so this sentinel never leaks as an extra task failure.
var errForcedAbort = errors.New("relay: forced cancel aborted in-flight log write")
```

Add `"errors"` to the import block in `internal/agent/runner.go` (it is not currently imported).

- [ ] **Step 3: Add the `sendOrAbort` helper**

In `internal/agent/runner.go`, add a new method next to `send`. It is the three-case variant used only by the log-copy path:

```go
// sendOrAbort enqueues a log chunk like send, but additionally abandons the
// enqueue if a forced cancel has closed r.forcedCh. It returns true on a
// successful enqueue and false if it abandoned (agent shutdown or forced abort).
// Only chunkWriter.Write uses this; all other callers use send so their
// blocking discipline is unchanged.
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
	}
}
```

- [ ] **Step 4: Rewrite `chunkWriter.Write` to use `sendOrAbort` and return the sentinel**

In `internal/agent/runner.go`, replace `chunkWriter.Write`. Keep the empty-chunk guard at the top exactly as-is. Return `(0, errForcedAbort)` only when the abort branch was taken:

```go
func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil // match the old pipeLog n>0 guard: never emit an empty chunk
	}
	chunk := make([]byte, len(p))
	copy(chunk, p)
	if !w.r.sendOrAbort(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId:    w.r.taskID,
				Stream:    w.stream,
				Content:   chunk,
				Epoch:     w.r.epoch,
				StepIndex: w.stepIndex,
				StepTotal: w.stepTotal,
			},
		},
	}) {
		// Abandoned. On a forced cancel this stops io.Copy so cmd.Wait returns.
		// On agent shutdown (ctx.Done) returning the sentinel is equally fine:
		// the runner is tearing down regardless.
		return 0, errForcedAbort
	}
	return len(p), nil
}
```

Update the doc comment on the `chunkWriter` type (currently lines ~231-235) to reflect the new behavior. Replace the trailing sentence "Write always returns (len(p), nil) so exec never treats the sink as broken and keeps copying until EOF." with:

```go
// pushes it through r.sendOrAbort. On a successful enqueue Write returns
// (len(p), nil) so exec keeps copying until EOF (unchanged slow-consumer
// behavior). If a forced cancel has closed r.forcedCh (or the agent context is
// done), the enqueue is abandoned and Write returns errForcedAbort so exec's
// io.Copy stops and cmd.Wait() returns promptly instead of waiting out WaitDelay.
```

- [ ] **Step 5: Verify it builds and vets**

Run:

```
go build ./internal/agent/...
go vet ./internal/agent/...
```

Expected: both succeed with no output.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/runner.go
git commit -m "agent: abort in-flight log write on forced cancel via forcedCh sentinel"
```

---

## Task 4: Bound the terminal FAILED send on the forced path + add its guard test

After `chunkWriter.Write` aborts and `cmd.Wait()` returns, `Run` calls `sendFinalStatus(FAILED)`. With an unchanged two-case `send`, that would block on the still-full `sendCh` (agent ctx still alive) and `Run` would never return. So the terminal send must be bounded on the forced path. This is the load-bearing refinement from the spec's "How terminal FAILED still sends" section.

**Correctness of bounding (Fact 1 + Fact 2 from the spec):** `sendFinalStatus` must NOT consult `forcedCh` to *decide whether to send* the terminal status (that would abandon the FAILED itself). Instead, on the forced path it uses a bounded enqueue: select on `r.sendCh <- msg` (succeeds when there is headroom - the common case) and `<-r.forcedCh` (already closed; taken only when `sendCh` is genuinely full, meaning "best-effort enqueue failed, return without blocking"). Dropping a wedged-channel terminal message is safe because the server already holds authoritative `failed` from `CancelJobTasks` and the agent's terminal message carries the now-fenced `r.epoch`, so it is epoch-fenced out anyway. The non-forced path stays the blocking two-case `send` - default-cancel drain-and-report behavior is unchanged.

This task includes spec criterion 2's test: a forced cancel with a `sendCh` that has room still produces a terminal `FAILED` on `sendCh`.

**Files:**
- Modify: `internal/agent/runner.go:264-277` (`sendFinalStatus`)
- Test: `internal/agent/runner_cancel_test.go` (add `TestRunner_ForceCancel_StillSendsTerminalFailed`)

- [ ] **Step 1: Write the failing test**

Add this test to `internal/agent/runner_cancel_test.go` (the file is already `//go:build !windows`; the test relies on `sh`, so it belongs there). It force-cancels a running task whose `sendCh` has ample room and asserts a terminal `FAILED` is enqueued:

```go
// TestRunner_ForceCancel_StillSendsTerminalFailed guards spec Fact 1: the forced
// abort path must not be routed through the terminal-status send. With a sendCh
// that has room, a forced cancel must still report a terminal FAILED, proving the
// common force-cancel case (healthy connection, just one slow/wedged consumer or
// buffer headroom) still surfaces the terminal status to the server.
func TestRunner_ForceCancel_StillSendsTerminalFailed(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4096)

	task := &relayv1.DispatchTask{
		TaskId:   "t-term",
		JobId:    "j-term",
		Commands: singleCmd([]string{"sleep", "30"}),
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	time.Sleep(200 * time.Millisecond) // let the subprocess start
	r.Cancel(true)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not return within 3s after force cancel")
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
				"forced cancel with room on sendCh must still report terminal FAILED")
			return
		}
	}
}
```

- [ ] **Step 2: Run the new test in the container and verify it fails (or check why it passes)**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/suspicious-beaver-5f66ef:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_ForceCancel_StillSendsTerminalFailed -v -timeout 60s"
```

Expected: With Tasks 2-3 applied but `sendFinalStatus` not yet bounded, this test should PASS on its own (the channel has room, so the unchanged blocking `send` enqueues FAILED fine). That is acceptable: this test's role is the regression guard for Fact 1, not a red-driver. If it FAILS here, stop and investigate - it would mean the forced abort is incorrectly reaching the terminal send (a Fact-1 violation introduced in Task 3) and must be fixed before bounding. Record the observed result.

- [ ] **Step 3: Bound the forced-path terminal send in `sendFinalStatus`**

In `internal/agent/runner.go`, replace `sendFinalStatus`:

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
	if r.forced.Load() {
		// Forced cancel: best-effort, bounded enqueue so Run returns even when
		// sendCh is wedged full. forcedCh is already closed (Cancel closed it),
		// so this never blocks. Dropping the message is safe: the server's
		// CancelJobTasks already set the task failed and bumped assignment_epoch,
		// so this terminal message (carrying the old r.epoch) is epoch-fenced out.
		select {
		case r.sendCh <- msg:
		case <-r.forcedCh:
			// sendCh full and wedged; abandon best-effort. Server is authoritative.
		}
		return
	}
	r.send(msg)
}
```

Note: this preserves the non-forced path (`r.send(msg)`, the blocking two-case send) byte-for-byte in behavior. Only the `r.forced.Load()` branch is new.

- [ ] **Step 4: Run the terminal-FAILED test in the container and verify it passes**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/suspicious-beaver-5f66ef:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_ForceCancel_StillSendsTerminalFailed -v -timeout 60s"
```

Expected: **PASS.** A forced cancel on a roomy `sendCh` still reports terminal FAILED.

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
git commit -m "agent: bound forced-path terminal FAILED send; add Fact-1 guard test"
```

---

## Task 5: Prove the repro is green on Linux (green-after)

Close the loop on spec criterion 1 and 4: the repro that was red in Task 1 must now be green on Linux, with the forced cancel returning under the 2s assertion. This is the load-bearing proof that the fix works.

**Files:**
- Run (no edit): `internal/agent/runner_cancel_test.go::TestRunner_ForceCancel_ReturnsQuickly`

- [ ] **Step 1: Run the repro in the Linux container at the fixed state**

Run (Git Bash):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/suspicious-beaver-5f66ef:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -run TestRunner_ForceCancel_ReturnsQuickly -v -timeout 60s"
```

Expected: **PASS.** The forced cancel returns well under 2s (compare the elapsed time against the ~3.3s failure captured in Task 1 Step 2 to confirm the change is load-bearing - the margin should be large).

- [ ] **Step 2: No commit**

This task writes no code. Record the green output and elapsed time alongside the Task 1 red output for the retro / PR description.

---

## Task 6: Full agent cancel suite + build + vet (final verification)

Confirm the whole forced/default/leaked-child cancel suite stays green on Linux (spec criterion 3), and the tree builds and vets. The default-cancel drain behavior and the leaked-child WaitDelay behavior must not have regressed.

**Files:**
- Run (no edit): the `internal/agent` test package.

- [ ] **Step 1: Run the full `internal/agent` package on Linux**

Run (Git Bash). This runs every test in the package, including the Unix-gated cancel tests (`TestRunner_ForceCancel_SkipsWorkspaceFinalize`, `TestRunner_DefaultCancel_RunsWorkspaceFinalize`, `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang`, the pipe-drain regression, the new `TestRunner_ForceCancel_StillSendsTerminalFailed`, and `TestRunner_ForceCancel_ReturnsQuickly`):

```bash
MSYS_NO_PATHCONV=1 docker run --rm \
  -v "/d/dev/relay/.claude/worktrees/suspicious-beaver-5f66ef:/src" \
  -v relay-gomod:/go/pkg/mod \
  -w /src golang:1.26.2 \
  bash -c "go test ./internal/agent/ -v -timeout 120s"
```

Expected: **PASS** for the entire package - all cancel tests green, no regression in default-cancel finalize or leaked-child WaitDelay behavior.

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

Expected: PASS. Note: this does NOT exercise the Unix-gated cancel tests (they are skipped on Windows) - the Linux run in Step 1 is the authoritative proof for those. This step only confirms nothing else in the tree broke.

- [ ] **Step 5: No code commit; this is verification only**

If Steps 1-4 all pass, the implementation is complete. No new commit unless a fix was required (in which case re-run the relevant container test before committing the fix).

---

## Self-Review

**Spec coverage:**
- Forced-abort `forcedCh` field + close-once -> Task 2. (spec section "Mechanism" steps 1-2)
- `chunkWriter.Write` three-case select + `errForcedAbort` sentinel -> Task 3. (spec "Mechanism" step 3, "Concrete shape")
- Log-streaming `send` for other callers unchanged -> Task 3 surgical note (helper is dedicated to `chunkWriter`; `send` untouched). (spec "Mechanism" step 4)
- Terminal FAILED not routed through abort (Fact 1) + bounded on forced path (Fact 2) -> Task 4. (spec "How terminal FAILED still sends")
- Non-forced terminal send unchanged -> Task 4 Step 3 note. (spec)
- Red-before-green on Linux/Docker, Windows-skip called out, exact commands -> Tasks 1 and 5, plus the "Platform-gated verification" preamble. (spec criteria 1, 4; platform-gated-test-verification memory)
- Terminal-FAILED-still-sends test (criterion 2) -> Task 4 Step 1.
- Existing forced/default/leaked-child suite stays green (criterion 3) -> Task 6 Step 1.
- Invariant: one bounded sender per gRPC stream -> no new writer; all writes still go through `r.sendCh`; both changes strictly reduce blocking (noted in Task 3 surgical note and spec "Invariant-compliance").
- Epoch fence untouched -> no `tasks.status`/`task_logs` query change; terminal keeps `r.epoch` (noted Task 4).
- Files limited to `runner.go` + `runner_cancel_test.go`; no other file required (header "Files in scope").

**Placeholder scan:** No TBD/TODO/"handle edge cases"/"similar to Task N". Every code step shows real code. The two no-new-unit-test steps (Task 2 Step 1, Task 3 Step 1) are explicitly justified by the integration-level repro and are not placeholders - they state the verification mechanism (build/vet + downstream test) rather than fabricating a coupled unit test.

**Type/name consistency:** `forcedCh chan struct{}`, `errForcedAbort`, `sendOrAbort`, `r.forced` (existing `atomic.Bool`), `r.cancelled`, `r.abandoned`, `sendFinalStatus`, `chunkWriter.Write`, `r.send` - all consistent across Tasks 2-4 and match the existing identifiers in `internal/agent/runner.go`. The `CompareAndSwap(false, true)` close gate in Task 2 is the same `r.forced` read by `sendFinalStatus` in Task 4, so the forced-path branch there is reached exactly when the channel was closed.

---

## Execution Handoff

Plan complete. This is an unattended autopilot run, so execute via **subagent-driven-development** (REQUIRED SUB-SKILL: superpowers:subagent-driven-development): a fresh subagent per task with two-stage review between tasks. Tasks run sequentially (single backend slice, no parallelism).
