# Agent Windows Proctree / cmd.Start Data-Race Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the Windows-only data race between the proctree watcher goroutine and `cmd.Start()` in the agent Runner by replacing the eager polling goroutine with a synchronous post-Start assignment, and re-include `relay/internal/agent` in the `make test-race` target.

**Architecture:** `setupProcTree(cmd)` currently returns a single `cleanup func()` and spawns a goroutine that busy-polls `cmd.Process` (racing `cmd.Start()` writing it on Windows). The fix changes the cross-platform signature to return `(assign func(), cleanup func())`. The caller (`(*Runner).Run`) invokes `assign()` synchronously immediately after a successful `cmd.Start()`, establishing a happens-before edge (Start's write to `cmd.Process` is sequenced before the read in the same goroutine). On Windows `assign` runs the existing `ensureAssigned()` synchronously - no goroutine, no polling, no `time`/`runtime` imports. On Unix `assign` is a no-op. `cmd.Cancel` still calls `ensureAssigned()` defensively, so kill-on-job-close behavior is unchanged.

**Tech Stack:** Go 1.26.2, `os/exec`, `golang.org/x/sys/windows` (Job Objects), POSIX `syscall` (process groups), the Go race detector (cgo + gcc).

---

## Slice independence

This is **backend-only**. There is no frontend code, no API, no SQL, no protobuf. It is a single sequential slice of agent-package changes (signature change must land in both build-tagged files plus the caller and both test files in one coherent set, since the package will not compile otherwise). There is no Phase 3 frontend/backend parallelism to declare. The conductor should treat this as one linear backend slice.

## Invariant interactions

None. This touches agent subprocess lifecycle (`internal/agent`), not the epoch fence, job-spec pipeline, gRPC sender, teardown identity check, registry locking, or JSON entry point. The single-sender invariant (`agent: sendCh`) is untouched: this change removes a goroutine, it does not add a stream writer.

## Grounding facts (confirmed by reading the repo)

- `internal/agent/proctree_windows.go` (`//go:build windows`): `setupProcTree(cmd *exec.Cmd) func()`. It defines `ensureAssigned()` (idempotent via `setup` flag under `jobMu`), sets `cmd.Cancel` (which calls `ensureAssigned()` then terminates/closes the job), spawns the racing goroutine (lines 93-102: a 1-second deadline busy-loop reading `cmd.Process` then calling `ensureAssigned()`), and returns the `cleanup` closure (lines 107-115) that closes the job handle. Imports include `runtime` and `time` used ONLY by the goroutine.
- `internal/agent/proctree_unix.go` (`//go:build !windows`): `setupProcTree(cmd *exec.Cmd) func()`. Sets `cmd.SysProcAttr.Setpgid = true`, sets `cmd.Cancel` (SIGKILL the process group), returns `func() {}`. No goroutine, race-clean.
- `internal/agent/runner.go:174`: `cleanupProcTree := setupProcTree(cmd)`. `cmd.Start()` is at line 188 (inside `if err := cmd.Start(); err != nil { ... break }`). `cmd.Wait()` at 193, `cleanupProcTree()` at 194.
- Tests calling `setupProcTree`:
  - `internal/agent/runner_cancel_test.go:18` `TestSetupProcTree_Unix_SetsPgid` (`//go:build !windows`): `setupProcTree(cmd)` (return value discarded), then asserts `Setpgid`/`Cancel`, then `cmd.Start()` and a cancel smoke-test.
  - `internal/agent/runner_cancel_windows_test.go:14` `TestSetupProcTree_Windows_AssignsJobObject` (`//go:build windows`): `setupProcTree(cmd)` (return value discarded), then `cmd.Start()`, then `cmd.Cancel()` smoke-test.
- The race detector reported the Windows reads at `proctree_windows.go:96` (`if cmd.Process != nil` in the goroutine) and `:59` (`OpenProcess(... cmd.Process.Pid ...)` inside `ensureAssigned` reached from the goroutine) versus `cmd.Start()` writing `cmd.Process` from `runner.go:188`.
- Tests that previously detected the race (per the backlog item): `TestRunner_MultiStepAllSucceed`, `TestRunner_MultiStepFailFastSkipsRest`, `TestRunner_SingleCommandReportsStepOneOfOne`, `TestRunner_PrepareEmitsPreparing`, `TestRunner_done`, `TestRunner_timeout`, `TestSetupProcTree_Windows_AssignsJobObject`, `TestAgentRunnerSurvivesConnectionContextCancellation`.
- `Makefile:1` `.PHONY` includes `test-race`. `Makefile:38-53` is the `test-race` block: a multi-line comment documenting the descope, then `test-race:` whose recipe (line 53) is `go test -race -timeout 180s $(shell go list ./... | grep -v '^relay/internal/agent$$')`. The comment's `SCOPE:` paragraph (lines 38-48) explains the exclusion and ends "re-include relay/internal/agent here once it is." The `NOTE (Windows)` paragraph (lines 49-51) documents the MSYS2 gcc requirement and must be kept.

## Cross-platform signature decision (final)

`setupProcTree(cmd *exec.Cmd) (assign func(), cleanup func())` in BOTH build-tagged files.

- Windows: `assign` = a closure that calls `ensureAssigned()` synchronously. The eager goroutine and the `runtime`/`time` imports are deleted. `cmd.Cancel` keeps its defensive `ensureAssigned()` call unchanged.
- Unix: `assign` = `func() {}` (no-op); everything else unchanged.
- Caller: `(*Runner).Run` binds `assignProcTree, cleanupProcTree := setupProcTree(cmd)` before Start, and calls `assignProcTree()` on the line right after the successful `cmd.Start()` (before `cmd.Wait()`).

This preserves all current behavior: the job object is assigned before `cmd.Wait()` (now eagerly and deterministically, exactly as the goroutine intended, but on the Run goroutine after Start has populated `cmd.Process`); `cmd.Cancel` still works for the lazy path; `cleanup` still closes the handle. The 1-second deadline goroutine-leak guard is no longer needed because the call is synchronous and only happens after Start returns successfully (if Start fails we `break` and never call `assign`, so `cmd.Process` access is never attempted - matching today's behavior where the goroutine would time out and exit without assigning).

---

## Task 1: Change the Windows proctree signature to return (assign, cleanup) and drop the racing goroutine

**Files:**
- Modify: `internal/agent/proctree_windows.go` (`//go:build windows`)

- [ ] **Step 1: Rewrite `setupProcTree` to return two functions and remove the goroutine**

Replace the entire body of `internal/agent/proctree_windows.go` with the following. The only behavioral changes versus today: the signature returns `(assign, cleanup)`, the eager poll goroutine (old lines 89-102) is deleted, `assign` calls `ensureAssigned()` synchronously, and the now-unused `runtime` and `time` imports are removed. `ensureAssigned` and `cmd.Cancel` and `cleanup` are byte-for-byte unchanged from today.

```go
//go:build windows

package agent

import (
	"log"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setupProcTree configures cmd so that:
//   - the child is assigned to a Windows Job Object with
//     JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE set, so any descendant the child
//     spawns is also tied to the job and dies with it.
//   - cmd.Cancel calls TerminateJobObject, which kills every process in the
//     job atomically — direct child and any descendants.
//
// Returns two functions. assign must be called by the caller immediately after
// cmd.Start() returns successfully; it assigns the started process to the Job
// Object. Calling it after Start guarantees cmd.Process is set and avoids racing
// the Start write to cmd.Process. cleanup must be called after cmd.Wait returns
// to close the Job Object handle when the process completes without cancellation.
func setupProcTree(cmd *exec.Cmd) (assign func(), cleanup func()) {
	var (
		jobMu sync.Mutex
		job   windows.Handle
		setup bool
	)

	ensureAssigned := func() {
		jobMu.Lock()
		defer jobMu.Unlock()
		if setup {
			return
		}
		setup = true
		if cmd.Process == nil {
			return
		}
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			log.Printf("agent: CreateJobObject: %v", err)
			return
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(
			h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			log.Printf("agent: SetInformationJobObject: %v", err)
			_ = windows.CloseHandle(h)
			return
		}
		ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err != nil {
			log.Printf("agent: OpenProcess(%d): %v", cmd.Process.Pid, err)
			_ = windows.CloseHandle(h)
			return
		}
		defer windows.CloseHandle(ph)
		if err := windows.AssignProcessToJobObject(h, ph); err != nil {
			log.Printf("agent: AssignProcessToJobObject: %v", err)
			_ = windows.CloseHandle(h)
			return
		}
		job = h
	}

	cmd.Cancel = func() error {
		ensureAssigned()
		jobMu.Lock()
		h := job
		job = 0 // claim ownership so cleanup won't double-close
		jobMu.Unlock()
		if h != 0 {
			_ = windows.TerminateJobObject(h, 1)
			_ = windows.CloseHandle(h)
		} else if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil
	}

	// assign eagerly attaches the started process to the job. The caller invokes
	// it synchronously right after cmd.Start() returns, so cmd.Process is set and
	// this read is sequenced-after the Start write (no goroutine, no race).
	assign = func() {
		ensureAssigned()
	}

	// cleanup closes the Job Object handle when the process completes without
	// cancellation. cmd.Cancel zeros job before closing, so this is a no-op
	// if cancel already ran.
	cleanup = func() {
		jobMu.Lock()
		h := job
		job = 0
		jobMu.Unlock()
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}

	return assign, cleanup
}
```

- [ ] **Step 2: Verify it compiles for Windows**

Run (PowerShell or Bash, Windows host):
`go build ./internal/agent/...`
Expected: exit 0. (At this point `runner.go` still calls the old one-return form and `setupProcTree` now returns two values, so this build WILL fail with "assignment mismatch" / "single-value context" until Task 3. That is expected for the package build - do the narrower check instead.)

Narrower check that this file alone is well-formed: `go vet ./internal/agent/...` after Task 3, or rely on Task 3's build. For Task 1 in isolation, confirm there are no unused imports by inspection: `runtime` and `time` are removed; remaining imports (`log`, `os/exec`, `sync`, `unsafe`, `golang.org/x/sys/windows`) are all still used.

Note for the executor: because the signature change spans `proctree_windows.go`, `proctree_unix.go`, `runner.go`, and both test files, the package will not compile until Tasks 1-4 all land. Stage all four edits, then run the package build/vet once (Task 4 Step 2). Commit the coherent set together at the end of Task 4. Do not attempt a per-task green build for Tasks 1-3.

- [ ] **Step 3: (deferred) - commit happens in Task 4 after the whole coherent set compiles**

---

## Task 2: Change the Unix proctree signature to return (assign, cleanup)

**Files:**
- Modify: `internal/agent/proctree_unix.go` (`//go:build !windows`)

- [ ] **Step 1: Update the signature; `assign` is a no-op**

Replace the entire body of `internal/agent/proctree_unix.go` with:

```go
//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// setupProcTree configures cmd so that:
//   - the child starts in its own process group (Setpgid), so all descendants
//     inherit the same PGID by default.
//   - cmd.Cancel (invoked when the context tied to exec.CommandContext is
//     cancelled) sends SIGKILL to the entire process group, killing every
//     descendant in one shot.
//
// Returns two functions to match the Windows build's signature: assign is a
// no-op on Unix (the process group is configured before Start via SysProcAttr),
// and cleanup is a no-op (there is no kernel handle to release).
func setupProcTree(cmd *exec.Cmd) (assign func(), cleanup func()) {
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
	return func() {}, func() {} // no kernel handle to release on Unix
}
```

- [ ] **Step 2: Verify (deferred to Task 4 package build)**

This file builds on Linux/macOS. Full package compile is verified in Task 4 (after the caller and tests are updated). For now confirm by inspection: imports `os/exec` and `syscall` both still used; signature matches Task 1's `(assign func(), cleanup func())`.

- [ ] **Step 3: (deferred) - commit happens in Task 4**

---

## Task 3: Update the Runner call site to call assign after Start

**Files:**
- Modify: `internal/agent/runner.go:174,188-194`

- [ ] **Step 1: Bind both returns and call `assign()` after a successful Start**

At `internal/agent/runner.go:174`, change:

```go
		cleanupProcTree := setupProcTree(cmd)
```

to:

```go
		assignProcTree, cleanupProcTree := setupProcTree(cmd)
```

Then in the Start block (currently lines 188-191), change:

```go
		if err := cmd.Start(); err != nil {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}
```

to:

```go
		if err := cmd.Start(); err != nil {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}
		// Assign the started process to the proctree (Windows Job Object) now
		// that cmd.Start has populated cmd.Process. Calling this synchronously
		// after Start - rather than from a goroutine that polls cmd.Process -
		// avoids racing the Start write to cmd.Process. No-op on Unix.
		assignProcTree()
```

The `cmd.Wait()` (now line ~199) and `cleanupProcTree()` (now line ~200) lines are unchanged.

- [ ] **Step 2: Verify (deferred to Task 4 package build)**

- [ ] **Step 3: (deferred) - commit happens in Task 4**

---

## Task 4: Update both proctree tests for the new signature, then build and commit the coherent set

**Files:**
- Modify: `internal/agent/runner_cancel_test.go:18-37` (`TestSetupProcTree_Unix_SetsPgid`)
- Modify: `internal/agent/runner_cancel_windows_test.go:14-31` (`TestSetupProcTree_Windows_AssignsJobObject`)

- [ ] **Step 1: Update the Unix test to bind the new returns and call assign after Start**

In `internal/agent/runner_cancel_test.go`, replace the body of `TestSetupProcTree_Unix_SetsPgid` (lines 18-37) with:

```go
func TestSetupProcTree_Unix_SetsPgid(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sleep", "30")
	assign, cleanup := setupProcTree(cmd)
	require.NotNil(t, cmd.SysProcAttr, "SysProcAttr should be set")
	require.True(t, cmd.SysProcAttr.Setpgid, "Setpgid should be true")
	require.NotNil(t, cmd.Cancel, "cmd.Cancel should be set")

	// Smoke-test the cancel path against a real process.
	require.NoError(t, cmd.Start())
	assign() // no-op on Unix, but exercises the call site contract
	t.Cleanup(func() { cleanup(); _ = cmd.Process.Kill() })
	_ = cmd.Cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit within 2s after cancel")
	}
}
```

- [ ] **Step 2: Update the Windows test to bind the new returns and call assign after Start**

In `internal/agent/runner_cancel_windows_test.go`, replace the body of `TestSetupProcTree_Windows_AssignsJobObject` (lines 14-31) with:

```go
func TestSetupProcTree_Windows_AssignsJobObject(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd", "/c", "ping", "127.0.0.1", "-n", "30")
	assign, cleanup := setupProcTree(cmd)
	require.NotNil(t, cmd.Cancel, "cmd.Cancel should be set")

	require.NoError(t, cmd.Start())
	assign() // synchronously assigns the started process to the Job Object
	t.Cleanup(func() { cleanup(); _ = cmd.Process.Kill() })

	_ = cmd.Cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit within 2s after cancel")
	}
}
```

- [ ] **Step 3: Build the whole agent package on the current platform**

Run (Linux agent env): `go build ./internal/agent/... && go vet ./internal/agent/...`
Expected: exit 0. This proves the Unix build (`proctree_unix.go` + `runner.go` + `runner_cancel_test.go`) all agree on the new `(assign, cleanup)` signature.

- [ ] **Step 4: Run the agent unit tests on the current platform (correctness, no -race yet)**

Run (Linux agent env): `go test ./internal/agent/... -timeout 120s`
Expected: PASS. (Windows-tagged file/test is skipped on Linux; the Unix proctree test and all `TestRunner_*` tests pass.)

- [ ] **Step 5: Commit the coherent signature-change set**

```bash
git add internal/agent/proctree_windows.go internal/agent/proctree_unix.go internal/agent/runner.go internal/agent/runner_cancel_test.go internal/agent/runner_cancel_windows_test.go
git commit -m "fix(agent): assign proctree after cmd.Start to remove Windows data race"
```

---

## Task 5: Verify the race is gone under -race on Windows

**Files:** none (verification only).

This race is detectable only by `go test -race` on Windows, which needs cgo with the MSYS2 mingw64 gcc (the default Strawberry Perl gcc fails with exit 0xc0000139). The toolchain is at `/c/msys64/mingw64/bin/gcc.exe` (gcc 13.2.0) on this machine. **This step must be run on the Windows host (Git Bash), not the Linux container.**

- [ ] **Step 1: Run the agent suite under -race on Windows with the MSYS2 gcc**

Run (Git Bash on Windows):

```bash
CC=/c/msys64/mingw64/bin/gcc.exe PATH=/c/msys64/mingw64/bin:$PATH go test -race ./internal/agent/... -timeout 180s
```

Expected: PASS for `relay/internal/agent` with NO `WARNING: DATA RACE` lines. The previously-racing tests must all be green under the detector: `TestRunner_MultiStepAllSucceed`, `TestRunner_MultiStepFailFastSkipsRest`, `TestRunner_SingleCommandReportsStepOneOfOne`, `TestRunner_PrepareEmitsPreparing`, `TestRunner_done`, `TestRunner_timeout`, `TestSetupProcTree_Windows_AssignsJobObject`, `TestAgentRunnerSurvivesConnectionContextCancellation`.

The race detector IS the assertion here: the existing suite, run under `-race` on Windows, no longer reports a race between `cmd.Start()` and the proctree read because the read now happens synchronously after Start on the same goroutine. No new test logic is required to prove the fix. (No additional deterministic test is proposed: the happens-before edge is structural - `assign()` is unreachable before `cmd.Start()` returns - and adding a test that re-runs the same sequence would only re-exercise what the existing `-race` run already covers.)

- [ ] **Step 2: Confirm Linux is still green (unix path unchanged)**

Run (Linux agent env): `go test -race ./internal/agent/... -timeout 180s`
Expected: PASS, no `WARNING: DATA RACE`. The Unix path only added a no-op `assign`; behavior is unchanged.

---

## Task 6: Re-include internal/agent in the make test-race target

**Files:**
- Modify: `Makefile:38-53` (the `test-race` comment block and recipe)

- [ ] **Step 1: Replace the descope comment and remove the agent exclusion from the recipe**

The current `test-race` block (lines 38-53) has a `SCOPE:` paragraph documenting why `relay/internal/agent` is excluded "until the proctree race is fixed", and a recipe that greps it out. Replace the whole block (from the `# Run tests under the race detector` comment through the recipe line) with this. The `NOTE (Windows)` MSYS2 paragraph is kept verbatim; the `SCOPE:` exclusion paragraph is removed because the race is now fixed; the recipe drops the `grep -v` so the agent package is covered again. **Recipe line must be a literal TAB, not spaces.**

```makefile
# Run tests under the race detector (unit tests only - no Docker). Catches
# concurrency regressions across the agent send goroutine and Runner, the
# worker/grace registries, the scheduler, and the perforce registry race guard.
# internal/agent is included: its former Windows-only proctree race
# (internal/agent/proctree_windows.go, docs/backlog/closed/bug-2026-06-20-agent-proctree-windows-race.md)
# is fixed - the proctree is now assigned synchronously after cmd.Start instead
# of from a goroutine that polled cmd.Process.
# NOTE (Windows): -race needs cgo with a working gcc. The default Strawberry Perl
# gcc fails (exit status 0xc0000139); use MSYS2 mingw64 via
# CC=/c/msys64/mingw64/bin/gcc.exe (with its bin on PATH). Linux/CI is unaffected.
test-race:
	go test -race ./... -timeout 180s
```

(Note: the backlog path in the comment points at `docs/backlog/closed/...` because Task 7 moves it there. If you prefer to avoid a forward reference, the comment may instead omit the path and say "now fixed - see this plan". Keep it consistent with where the backlog file ends up.)

- [ ] **Step 2: Verify the target is well-formed and (on Linux) green**

Run (Linux agent env): `make test-race`
Expected: exit 0, all packages `ok` (or `[no test files]`), `relay/internal/agent` now included with no `WARNING: DATA RACE`. This proves the recipe re-includes the agent package and that the Unix race-detector run stays green. (Windows devs run the same `make test-race` with the MSYS2 `CC`/`PATH` env from Task 5 Step 1; the recipe itself is platform-neutral.)

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: re-include internal/agent in test-race target (proctree race fixed)"
```

---

## Task 7: Close the backlog item (conductor-owned, final task)

**Files:**
- Move: `docs/backlog/bug-2026-06-20-agent-proctree-windows-race.md` -> `docs/backlog/closed/`

Per the relay convention, closing a backlog item is required scope. Use the `/backlog close` command rather than hand-editing frontmatter; the steps below describe the equivalent operations the executor (or conductor) performs.

- [ ] **Step 1: Close via the backlog skill**

Preferred: run `/backlog close agent-proctree-windows-race`.

If running the moves manually instead, the `git mv` plus frontmatter stamp must match the skill:

```bash
git mv docs/backlog/bug-2026-06-20-agent-proctree-windows-race.md docs/backlog/closed/
```

then set `status: closed`, add `closed: 2026-06-20` and a `resolution:` line to the frontmatter, append a `## Resolution` section noting the fix (synchronous post-Start `assign`, test-race re-inclusion, verified under `-race` on Windows with MSYS2 gcc).

- [ ] **Step 2: Verify the move**

Run: `git status`
Expected: the bug file renamed into `docs/backlog/closed/`; nothing left for it in `docs/backlog/`.

- [ ] **Step 3: Commit (if not already committed by the close command)**

```bash
git add -A docs/backlog
git commit -m "backlog: close agent-proctree-windows-race (fixed)"
```

---

## Self-review against the spec

- **Establish happens-before so the watcher never touches cmd/cmd.Process before Start returns:** Tasks 1-3 - the goroutine is deleted; `assign()` runs synchronously after `cmd.Start()` on the Run goroutine.
- **Signature change applied to BOTH proctree files so the package compiles on every platform:** Task 1 (Windows) and Task 2 (Unix) both move to `(assign func(), cleanup func())`; Task 4 Step 3 builds the package to prove agreement.
- **Caller updated:** Task 3 - `assignProcTree, cleanupProcTree := setupProcTree(cmd)` and `assignProcTree()` after Start.
- **Preserve cmd.Cancel defensiveness, job-object-before-Wait, cleanup-closes-handle, drop the now-unneeded leak-guard goroutine:** Task 1 - `cmd.Cancel`/`ensureAssigned`/`cleanup` unchanged; goroutine + `runtime`/`time` imports removed.
- **Existing tests updated for the new return:** Task 4 - both `TestSetupProcTree_*` tests bind `(assign, cleanup)` and call `assign()` after Start.
- **Exact Windows -race verification command with MSYS2 CC, naming the previously-detecting tests:** Task 5 Step 1.
- **Race detector is the assertion; no new test logic strictly required:** Task 5 Step 1 - stated, with rationale for not adding a redundant test.
- **Re-include relay/internal/agent in make test-race; trim the stale "excludes ... until fixed" comment; keep the Windows-gcc NOTE:** Task 6.
- **Linux go test -race ./... still green:** Task 5 Step 2 and Task 6 Step 2.
- **Backend-only, no frontend, Invariant interaction flagged (none):** "Slice independence" and "Invariant interactions" sections.
- **Backlog item closed via git mv to closed/:** Task 7.
- **Placeholder scan:** every code step shows literal file content; no TBD/TODO.
- **Type/name consistency:** `(assign func(), cleanup func())` is identical across both proctree files; the caller and both tests use `assign`/`cleanup` (caller locals `assignProcTree`/`cleanupProcTree`) consistently.
- **Coherent-commit note:** because the signature spans 5 files, Tasks 1-4 are staged together and committed once in Task 4 Step 5 (a per-task green build is impossible mid-change); this is called out in Task 1 Step 2.
