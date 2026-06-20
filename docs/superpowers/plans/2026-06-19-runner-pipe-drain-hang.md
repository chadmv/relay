# Runner Pipe-Drain Hang Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the agent runner from hanging forever when a command exits but a background grandchild keeps the inherited stdout/stderr pipe open, by letting `os/exec`'s own copy goroutines plus `WaitDelay` bound the drain.

**Architecture:** Replace the `StdoutPipe`/`StderrPipe` + `pipeLog`-goroutine + `wg.Wait()` machinery with a custom `io.Writer` (`chunkWriter`) assigned to `cmd.Stdout`/`cmd.Stderr`. Because exec now owns the OS pipes and the copy goroutines, `cmd.Wait()` enforces the existing `WaitDelay = 5s`: an unbounded hang becomes a 5s upper bound. All the now-dead `stepPipes` force-close machinery is deleted, and `setupProcTree` loses its `*Runner` parameter on both platforms.

**Tech Stack:** Go, `os/exec`, protobuf (`relayv1`), testify. Unit-test gate is `make test` (no Docker). Relevant package: `./internal/agent/...`.

---

## Slice independence

This is a single-slice, agent-only change. There is no frontend/backend split; all work lives in `internal/agent`. Tasks are sequential because Task 1 (the red regression test) must precede the implementation, and later cleanup tasks depend on the exec-loop rewrite.

## Key decisions (resolved during planning)

1. **`setupProcTree` `*Runner` parameter is dropped on both platforms.** After removing the `if r.forced.Load() { r.closeStepPipesForForce() }` branch from the `cmd.Cancel` callback, neither `proctree_unix.go` nor `proctree_windows.go` references `r` anywhere else. The new signature is `setupProcTree(cmd *exec.Cmd) func()`. The call site in `runner.go` and both proctree tests (`runner_cancel_test.go`, `runner_cancel_windows_test.go`) are updated to drop the now-unused `r := &Runner{}` and the `r` argument.

2. **Regression test gating: `//go:build !windows`.** The new hang test relies on a POSIX shell spawning a background child that inherits and holds the stdout pipe (`sh -c 'sleep 30 & echo done'`). That pattern is Unix-shell-shaped, and on Windows the Job Object (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) changes the lifetime semantics. The test goes in the existing Unix-gated file `internal/agent/runner_cancel_test.go` (already `//go:build !windows`), so it is skipped on Windows by the build tag - no `t.Skip` needed.

## Files touched

- **Modify** `internal/agent/runner.go`
  - Remove the `stepPipes` struct field (lines ~31-39) and the `io` import (becomes unused).
  - Remove `setStepPipes` (~74-81), `clearStepPipes` (~83-90), `closeStepPipesForForce` (~92-104), `pipeLog` (~285-309).
  - Add the `chunkWriter` type and its `Write` method.
  - Rewrite the exec loop body inside `Run` (~206-262): drop `StdoutPipe`/`StderrPipe`, the `sync.WaitGroup`, the two `pipeLog` goroutines, `setStepPipes`/`clearStepPipes`; assign `cmd.Stdout`/`cmd.Stderr = &chunkWriter{...}`; call `cmd.Wait()` directly. Update the `setupProcTree(cmd, r)` call to `setupProcTree(cmd)`.
  - `sync` import stays (still used by `makePrepareProgressFn`).
- **Modify** `internal/agent/proctree_unix.go` - drop `*Runner` param and the forced-close branch.
- **Modify** `internal/agent/proctree_windows.go` - drop `*Runner` param and the forced-close branch.
- **Modify** `internal/agent/runner_cancel_test.go` - add the new regression test; update `TestSetupProcTree_Unix_SetsPgid` call to drop the `r` argument; fix the stale comment in `TestRunner_ForceCancel_ReturnsQuickly`.
- **Modify** `internal/agent/runner_cancel_windows_test.go` - update `setupProcTree` call to drop the `r` argument.

### Critical files

`internal/agent/runner.go` is the heart of the change - the exec loop and `chunkWriter`. Read lines 196-265 and 285-309 before editing.

---

## Task 1: Red regression test for the pipe-drain hang

**Files:**
- Test: `internal/agent/runner_cancel_test.go` (append; file is `//go:build !windows`)

This test must FAIL (hang/timeout) against the current code, proving the bug. It is the primary success criterion.

> Red/green correctness: the leaked child must outlive `WaitDelay` (5s) by a wide margin so the in-test 9s hang-timeout sits strictly between `WaitDelay` and the child's lifetime. With `sleep 30 &` the new code returns at ~5s (under 9s -> PASS) while the old code blocks for the full ~30s child lifetime (over 9s -> FAIL). A 5s child would let the old code also return at ~5s, making the test pass on both red and green and proving nothing. On the fixed code the leaked `sleep 30` lingers harmlessly in its own process group after the runner returns (a normal exit does not kill the process tree); it self-reaps and does not affect the assertion.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/runner_cancel_test.go`:

```go
// TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang reproduces the
// go.dev/issue/23019 pattern: the foreground command exits 0 while a background
// grandchild keeps the inherited stdout pipe open. With the old
// StdoutPipe/pipeLog/wg.Wait() machinery the runner blocked forever because
// WaitDelay never engaged. After the fix exec owns the copy goroutines, so
// cmd.Wait() force-closes the descriptors within WaitDelay (5s) and the runner
// returns with a terminal status.
//
// The leaked child sleeps 30s - far longer than WaitDelay (5s) and the in-test
// 9s hang-timeout - so the test distinguishes red from green: the fixed code
// returns at ~5s (WaitDelay), while the old code blocks until the child dies at
// ~30s and trips the timeout. After the fixed runner returns, the leaked
// sleep 30 lingers harmlessly in its own process group (a normal exit does not
// kill the tree); it self-reaps and does not affect the assertion.
//
// Unix-only: relies on a POSIX shell backgrounding a child that inherits the
// stdout pipe. The file's //go:build !windows tag skips it on Windows.
func TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4096)

	// `sleep 30 &` forks a child that inherits stdout and holds the write end
	// open for 30s; the parent shell exits 0 immediately. The runner must not
	// wait on the leaked child past WaitDelay (5s).
	task := &relayv1.DispatchTask{
		TaskId:   "t-leak",
		JobId:    "j-leak",
		Commands: singleCmd([]string{"sh", "-c", "sleep 30 & echo done"}),
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	// The shell exits immediately; the leaked child holds the pipe for 30s.
	// WaitDelay (5s) plus slack must bound the runner. The old code would hang
	// here until the leaked child dies (~30s), tripping this 9s timeout.
	select {
	case <-done:
	case <-time.After(9 * time.Second):
		t.Fatal("runner hung: did not return within WaitDelay bound after normal exit with leaked child")
	}

	// Drain sendCh and assert a terminal status was reported.
	var sawTerminal bool
	for {
		select {
		case msg := <-sendCh:
			if ts := msg.GetTaskStatus(); ts != nil {
				switch ts.Status {
				case relayv1.TaskStatus_TASK_STATUS_DONE,
					relayv1.TaskStatus_TASK_STATUS_FAILED,
					relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
					sawTerminal = true
				}
			}
		default:
			require.True(t, sawTerminal, "runner must report a terminal status")
			return
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (hangs)**

Run: `go test ./internal/agent/... -run TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang -v -timeout 30s`

Expected: FAIL. On the old (buggy) code `pipeLog` blocks on `pipe.Read` for the full ~30s child lifetime (WaitDelay never engages because exec does not own the pipes), so the runner only returns when the leaked `sleep 30` dies. That is well past the in-test 9s `time.After`, so either the `t.Fatal("runner hung: ...")` fires at 9s, or the whole `go test` aborts at the 30s `-timeout` with a goroutine dump showing `pipeLog` blocked on `pipe.Read` / the runner blocked on `wg.Wait()`. Both outcomes confirm the bug.

- [ ] **Step 3: Commit the red test**

```bash
git add internal/agent/runner_cancel_test.go
git commit -m "test(agent): add failing regression for runner pipe-drain hang"
```

---

## Task 2: Add the chunkWriter type

**Files:**
- Modify: `internal/agent/runner.go`

Introduce the writer first (does not yet wire it in), so it compiles and the existing tests still pass. `chunkWriter` carries the same fields the old `pipeLog` stamped onto each chunk.

- [ ] **Step 1: Add the chunkWriter type and its Write method**

In `internal/agent/runner.go`, add this just above the existing `pipeLog` function (the body mirrors `pipeLog`'s send block; the copy is required because exec reuses `p` between `Write` calls):

```go
// chunkWriter is the io.Writer exec copies subprocess stdout/stderr into. Each
// Write copies its slice (exec reuses the buffer between calls), wraps it in a
// TaskLogChunk stamped with the runner's stream/step/epoch, and pushes it
// through r.send (bounded on r.ctx.Done()). Write always returns (len(p), nil)
// so exec never treats the sink as broken and keeps copying until EOF.
type chunkWriter struct {
	r         *Runner
	stream    relayv1.LogStream
	stepIndex int32
	stepTotal int32
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	chunk := make([]byte, len(p))
	copy(chunk, p)
	w.r.send(&relayv1.AgentMessage{
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
	})
	return len(p), nil
}
```

- [ ] **Step 2: Run the package build/tests to verify it still compiles**

Run: `go test ./internal/agent/... -run TestRunner_Echo -v -timeout 60s`

Expected: PASS (or "no tests to run" if no matching test, but it must COMPILE - `go vet ./internal/agent/...` should be clean except for the still-unused `chunkWriter` warning, which Go does not emit for unused types). If the package fails to build, fix the addition before continuing.

> Note: an unused struct type is not a Go compile error, so the build stays green. The wiring happens in Task 3.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/runner.go
git commit -m "feat(agent): add chunkWriter for subprocess log streaming"
```

---

## Task 3: Rewrite the exec loop to use chunkWriter and make the regression test pass

**Files:**
- Modify: `internal/agent/runner.go` (the loop body in `Run`, ~206-262)

This is the green step for Task 1's regression test.

- [ ] **Step 1: Replace the per-step exec block**

In `internal/agent/runner.go`, inside the `for i, cl := range task.Commands` loop, replace the current block (from `cmd := exec.CommandContext(...)` through the `break` that ends the per-step error handling, currently lines ~206-261) with:

```go
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.WaitDelay = 5 * time.Second // bound pipe draining after process exit/kill
		cleanupProcTree := setupProcTree(cmd)
		cmd.Env = env
		if workDir != "" {
			cmd.Dir = workDir
		}

		// Hand exec custom writers instead of taking the pipes ourselves. This
		// makes exec own the OS pipes AND the copy goroutines, so cmd.Wait()
		// enforces WaitDelay: if a leaked child still holds the write end after
		// the process exits, Wait force-closes the descriptors within 5s instead
		// of blocking forever (go.dev/issue/23019).
		cmd.Stdout = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, stepIndex: step, stepTotal: stepTotal}
		cmd.Stderr = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, stepIndex: step, stepTotal: stepTotal}

		if err := cmd.Start(); err != nil {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}

		waitErr := cmd.Wait()
		cleanupProcTree()

		lastExitCode = nil
		if cmd.ProcessState != nil {
			if code := cmd.ProcessState.ExitCode(); code >= 0 {
				c := int32(code)
				lastExitCode = &c
			}
		}

		if waitErr == nil {
			continue
		}
		switch {
		case r.cancelled.Load():
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
		case ctx.Err() == context.DeadlineExceeded:
			finalStatus = relayv1.TaskStatus_TASK_STATUS_TIMED_OUT
		default:
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
		}
		break
```

> Notes for the implementer:
> - The `StdoutPipe`/`StderrPipe` error checks, `r.setStepPipes(...)`, the `var wg sync.WaitGroup` block, the two `pipeLog` goroutines, `wg.Wait()`, and `r.clearStepPipes()` are all gone.
> - Exit-code extraction, the `waitErr == nil` continue, and the status switch are unchanged - only the surrounding pipe machinery was removed.
> - `setupProcTree(cmd)` now takes one argument (the signature change lands in Task 5; until then this line will not compile against the old 2-arg signature). To keep tasks independently buildable, perform Step 1 of Task 5 in the same commit as this task if you are committing per-task. See the Step 3 verification note below.

- [ ] **Step 2: This task cannot build alone - sequence with Task 5 Step 1**

Because the new `setupProcTree(cmd)` call references the not-yet-changed 2-arg signature, the package will not compile until `proctree_unix.go` (and `proctree_windows.go`) are updated. Apply **Task 5 Step 1 and Step 2** (the signature changes) before running the verification below. Do not run `make test` between this step and the proctree edits.

- [ ] **Step 3: Run the regression test to verify it now passes**

After applying the proctree signature changes (Task 5 Steps 1-2):

Run: `go test ./internal/agent/... -run TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang -v -timeout 30s`

Expected: PASS within ~5-6s. The shell exits immediately; `WaitDelay` force-closes the descriptors at ~5s, so the runner returns and reports a terminal status well under the in-test 9s bound. The leaked `sleep 30` continues running detached in its own process group and self-reaps; it does not affect the result.

- [ ] **Step 4: Run the full agent package to check nothing regressed**

Run: `make test`

Expected: PASS. In particular `TestRunner_ForceCancel_ReturnsQuickly`, `TestRunner_ForceCancel_SkipsWorkspaceFinalize`, and `TestRunner_DefaultCancel_RunsWorkspaceFinalize` still pass (they kill the whole tree, so the writer drains promptly).

- [ ] **Step 5: Commit (combined with Task 5 proctree edits)**

```bash
git add internal/agent/runner.go internal/agent/proctree_unix.go internal/agent/proctree_windows.go
git commit -m "fix(agent): drain subprocess output via chunkWriter so WaitDelay bounds the hang"
```

---

## Task 4: Delete the dead stepPipes machinery and the io import

**Files:**
- Modify: `internal/agent/runner.go`

After Task 3, `setStepPipes`, `clearStepPipes`, `closeStepPipesForForce`, `pipeLog`, and the `stepPipes` struct field are unused. Removing the methods drops the last `io.Reader`/`io.Closer` references, so the `io` import must go too.

- [ ] **Step 1: Delete the dead methods and struct field**

In `internal/agent/runner.go`:

1. Remove the `stepPipes` struct field block from the `Runner` struct (the comment plus):

```go
	stepPipes struct {
		mu     sync.Mutex
		stdout io.Closer
		stderr io.Closer
	}
```

2. Delete the `setStepPipes` method (the `// setStepPipes records ...` doc comment plus the function).
3. Delete the `clearStepPipes` method (doc comment plus function).
4. Delete the `closeStepPipesForForce` method (doc comment plus function).
5. Delete the `pipeLog` method (`func (r *Runner) pipeLog(...)` plus its doc-less body).
6. Remove `"io"` from the import block (it is now unreferenced).

> Keep `"sync"` in the imports - `makePrepareProgressFn` still uses `sync.Mutex`. Keep `"sync/atomic"` - `cancelled`/`forced`/`abandoned` are `atomic.Bool`.

- [ ] **Step 2: Run gofmt/vet to confirm no unused imports or symbols**

Run: `go vet ./internal/agent/...`

Expected: clean (no "imported and not used", no "declared but not used"). If `go vet` reports `io` still used, you missed a reference - grep `internal/agent/runner.go` for `io.` and resolve it.

- [ ] **Step 3: Run the full agent tests**

Run: `make test`

Expected: PASS. No behavior change in this task - pure dead-code removal.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/runner.go
git commit -m "refactor(agent): remove dead stepPipes force-close machinery"
```

---

## Task 5: Drop the *Runner parameter from setupProcTree (both platforms) and update tests

**Files:**
- Modify: `internal/agent/proctree_unix.go`
- Modify: `internal/agent/proctree_windows.go`
- Modify: `internal/agent/runner_cancel_test.go`
- Modify: `internal/agent/runner_cancel_windows_test.go`

> Sequencing note: Steps 1-2 (the signature changes) must land in the same commit as Task 3 because Task 3's loop calls `setupProcTree(cmd)`. Steps 3-5 (test updates) can be a follow-up commit. If you are executing per-task, do Steps 1-2 during Task 3's commit and Steps 3-5 here.

- [ ] **Step 1: Update proctree_unix.go**

In `internal/agent/proctree_unix.go`, change the signature and drop the forced branch. Replace the function with:

```go
// setupProcTree configures cmd so that:
//   - the child starts in its own process group (Setpgid), so all descendants
//     inherit the same PGID by default.
//   - cmd.Cancel (invoked when the context tied to exec.CommandContext is
//     cancelled) sends SIGKILL to the entire process group, killing every
//     descendant in one shot.
func setupProcTree(cmd *exec.Cmd) func() {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		pid := 0
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		// Negative PID targets the process group whose PGID == |pid|.
		// We set Setpgid above so PGID == child PID.
		if pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		return nil
	}
	return func() {} // no kernel handle to release on Unix
}
```

- [ ] **Step 2: Update proctree_windows.go**

In `internal/agent/proctree_windows.go`, change the signature to `func setupProcTree(cmd *exec.Cmd) func()` and remove the forced branch from the `cmd.Cancel` callback. Specifically:

1. Change line 28 from `func setupProcTree(cmd *exec.Cmd, r *Runner) func() {` to `func setupProcTree(cmd *exec.Cmd) func() {`.
2. In the `cmd.Cancel` callback, delete:

```go
		if r.forced.Load() {
			r.closeStepPipesForForce()
		}
```

3. Update the doc comment: remove the bullet beginning `//   - if r.forced is set at cancel time, stdout/stderr pipe handles are` (3 comment lines, ~22-24).

The rest of `proctree_windows.go` (Job Object creation, `ensureAssigned`, the eager-assign goroutine, the cleanup closure) is unchanged. No imports change (`r` was the only `*Runner` use).

> Windows-platform confirmation (resolved during planning): `proctree_windows.go` referenced `r` only inside the deleted forced branch. After removal nothing references `r`, so dropping the parameter is safe and no import changes are needed.

- [ ] **Step 3: Update the Unix proctree test**

In `internal/agent/runner_cancel_test.go`, `TestSetupProcTree_Unix_SetsPgid`, remove the now-unused `r := &Runner{}` and change the call:

```go
	cmd := exec.CommandContext(context.Background(), "sleep", "30")
	setupProcTree(cmd)
```

(Delete the `r := &Runner{}` line; change `setupProcTree(cmd, r)` to `setupProcTree(cmd)`.)

While here, fix the now-stale comment in `TestRunner_ForceCancel_ReturnsQuickly` (lines ~109-113) that references `pipeLog` and `closeStepPipesForForce`, which no longer exist. Replace that comment block with:

```go
	// Spawn something that produces a steady stream of stdout. On a forced
	// cancel the whole process group is SIGKILLed, so exec's copy goroutine
	// reaches EOF promptly and cmd.Wait returns well under WaitDelay (5s).
```

> The test assertion (`require.Less(t, elapsed, 2*time.Second, ...)`) stays. Per the spec's accepted-behavior table, a forced cancel of a tree that stays in its process group still returns < 2s because the SIGKILL closes the child's write end immediately.

- [ ] **Step 4: Update the Windows proctree test**

In `internal/agent/runner_cancel_windows_test.go`, `TestSetupProcTree_Windows_AssignsJobObject`, remove the unused `r := &Runner{}` and change the call:

```go
	cmd := exec.CommandContext(context.Background(), "cmd", "/c", "ping", "127.0.0.1", "-n", "30")
	setupProcTree(cmd)
```

(Delete the `r := &Runner{}` line; change `setupProcTree(cmd, r)` to `setupProcTree(cmd)`.)

- [ ] **Step 5: Run the full agent tests (Unix)**

Run: `make test`

Expected: PASS, including `TestSetupProcTree_Unix_SetsPgid` and all `TestRunner_ForceCancel_*` / `TestRunner_DefaultCancel_*` tests.

> Windows note: `runner_cancel_windows_test.go` is `//go:build windows` and is not exercised by `make test` on the Unix CI gate. The signature change there is mechanical and verified by inspection; if a Windows runner is available, run `go test ./internal/agent/... -run TestSetupProcTree_Windows -v` there.

- [ ] **Step 6: Commit the test updates**

```bash
git add internal/agent/runner_cancel_test.go internal/agent/runner_cancel_windows_test.go
git commit -m "test(agent): drop *Runner arg from setupProcTree call sites"
```

---

## Task 6: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full unit-test gate**

Run: `make test`

Expected: PASS across the repo.

- [ ] **Step 2: Vet the whole tree**

Run: `go vet ./...`

Expected: clean. Confirms no dangling references to `pipeLog`, `setStepPipes`, `clearStepPipes`, `closeStepPipesForForce`, `stepPipes`, or the old 2-arg `setupProcTree`.

- [ ] **Step 3: Grep for orphaned references**

Run: `git grep -n "stepPipes\|pipeLog\|closeStepPipesForForce\|setStepPipes\|clearStepPipes" -- internal/agent`

Expected: no matches (all references removed). If any remain in non-test files, they are bugs.

- [ ] **Step 4: Run the regression test in isolation once more**

Run: `go test ./internal/agent/... -run TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang -v -timeout 30s`

Expected: PASS within ~6s.

---

## Success criteria

1. `TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang` fails (hangs) on the pre-fix code and passes (returns within ~WaitDelay, reports a terminal status) after Task 3.
2. `TestRunner_ForceCancel_ReturnsQuickly` still returns under 2s; `TestRunner_ForceCancel_SkipsWorkspaceFinalize` and `TestRunner_DefaultCancel_RunsWorkspaceFinalize` still pass.
3. `TestSetupProcTree_Unix_SetsPgid` passes with the 1-arg `setupProcTree`.
4. `make test` and `go vet ./...` are clean; no orphaned `stepPipes`/`pipeLog` references remain.

## Invariant check (do not violate)

- **One bounded sender per gRPC stream.** All log chunks still go through `r.send`, which selects on `r.ctx.Done()`. `chunkWriter.Write` calls `r.send` directly; exec runs two copy goroutines, matching the prior two `pipeLog` goroutines - concurrent-writer count is unchanged.
- **Epoch fence.** Every `TaskLogChunk` built by `chunkWriter.Write` carries `w.r.epoch`. Preserved.
- **Surgical changes only.** `WaitDelay` (5s), step markers (`sendStepMarker`), status mapping, exit-code extraction, and workspace finalize are untouched. No adjacent refactoring.
