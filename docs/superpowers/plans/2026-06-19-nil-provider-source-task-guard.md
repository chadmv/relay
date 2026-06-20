# Nil-Provider Source-Task Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Runner.Run` reject a source-bearing task with `TASK_STATUS_PREPARE_FAILED` when the worker has no workspace provider, instead of silently running its commands without a synced workspace.

**Architecture:** Add a guard at the top of `Runner.Run`'s prepare phase that fires when `task.Source != nil && r.provider == nil`, emitting `PREPARE_FAILED` (the same terminal status the existing prepare-error path uses) and returning before any command runs. Correct the stale comment in the agent entrypoint that claims dispatch rejects these tasks.

**Tech Stack:** Go, gRPC/protobuf (`relayv1`), testify.

---

### Task 1: Guard `Runner.Run` against a nil provider for source-bearing tasks

**Files:**
- Modify: `internal/agent/runner.go` (insert ahead of the prepare block at line 113)
- Test: `internal/agent/runner_test.go`

- [ ] **Step 1: Write the failing test**

Add this test to `internal/agent/runner_test.go`. It builds a source-bearing task but never calls `SetProviderForTest`, so `r.provider` stays nil. It asserts the only status emitted is `PREPARE_FAILED` and that the command never ran (no "hello" in any log chunk).

```go
func TestRunner_SourceTaskWithNilProviderFailsPrepare(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 16)

	task := &relayv1.DispatchTask{
		TaskId:   "t1",
		JobId:    "j1",
		Commands: singleCmd(echoTaskCmd()),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}

	// Note: no SetProviderForTest call — r.provider is nil.
	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	r.Run(runCtx, task)

	var phases []relayv1.TaskStatus
	var sawCmdOutput bool
	for {
		select {
		case m := <-sendCh:
			if ts := m.GetTaskStatus(); ts != nil {
				phases = append(phases, ts.Status)
			}
			if log := m.GetTaskLog(); log != nil && strings.Contains(string(log.Content), "hello") {
				sawCmdOutput = true
			}
		default:
			goto done
		}
	}
done:
	require.Equal(t, []relayv1.TaskStatus{
		relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
	}, phases)
	require.False(t, sawCmdOutput, "command must not run when the provider is nil")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/... -run TestRunner_SourceTaskWithNilProviderFailsPrepare -v -timeout 30s`
Expected: FAIL — without the guard, the task falls through to command execution, so `phases` is `[RUNNING DONE]` (not `[PREPARE_FAILED]`) and `sawCmdOutput` is true.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/runner.go`, insert the guard immediately before the existing prepare block (the `if task.Source != nil && r.provider != nil {` line, currently line 113), right after the `var workDir string` / `var extraEnv map[string]string` declarations:

```go
	// A source-bearing task requires a workspace provider. If the agent has
	// none (p4 preflight failed, or RELAY_WORKSPACE_ROOT is unset), reject the
	// task loudly instead of silently running its commands without a synced
	// workspace. Dispatch does not filter on provider capability, so this is the
	// agent's last line of defense.
	if task.Source != nil && r.provider == nil {
		r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId:       r.taskID,
				Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
				ErrorMessage: "task has a source spec but this worker has no workspace provider (check p4 preflight / RELAY_WORKSPACE_ROOT)",
				Epoch:        r.epoch,
			},
		}})
		return
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/... -run TestRunner_SourceTaskWithNilProviderFailsPrepare -v -timeout 30s`
Expected: PASS

- [ ] **Step 5: Run the full agent package tests to confirm no regression**

Run: `go test ./internal/agent/... -timeout 120s`
Expected: PASS (including the existing `TestRunner_PrepareEmitsPreparing` and `TestRunner_PrepareFailureEmitsPrepareFailed`, which still set a provider and are unaffected by the new guard).

- [ ] **Step 6: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_test.go
git commit -m "fix(agent): reject source tasks when workspace provider is nil"
```

---

### Task 2: Correct the stale comment in the agent entrypoint

**Files:**
- Modify: `cmd/relay-agent/main.go:77-80`

- [ ] **Step 1: Replace the stale comment**

The comment currently at `cmd/relay-agent/main.go:77-80` reads:

```go
			// Non-fatal: log loudly and run without the workspace provider.
			// Source-bearing tasks will fail at dispatch with the existing
			// "no source provider" path; non-source tasks still run.
```

There is no dispatch-side rejection path. Replace it with an accurate description of the runtime behavior:

```go
			// Non-fatal: log loudly and run without the workspace provider.
			// Source-bearing tasks are rejected by the runner at run time with
			// TASK_STATUS_PREPARE_FAILED (see Runner.Run); non-source tasks still run.
```

- [ ] **Step 2: Verify the package still builds**

Run: `go build ./cmd/relay-agent/...`
Expected: builds with no errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-agent/main.go
git commit -m "docs(agent): correct stale comment about provider-disabled behavior"
```

---

### Task 3: Close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-06-10-source-tasks-run-without-workspace.md` -> `docs/backlog/closed/`

- [ ] **Step 1: git mv the backlog item to closed**

```bash
git mv docs/backlog/bug-2026-06-10-source-tasks-run-without-workspace.md docs/backlog/closed/bug-2026-06-10-source-tasks-run-without-workspace.md
```

- [ ] **Step 2: Update the item's status frontmatter**

Change the frontmatter `status: open` to `status: closed` in the moved file, and append a short closing note under the `## Related` section recording that the agent-side guard shipped and the dispatch-side capability filter remains the documented follow-up.

- [ ] **Step 3: Commit**

```bash
git add docs/backlog/closed/bug-2026-06-10-source-tasks-run-without-workspace.md
git commit -m "backlog: close source-tasks-run-without-workspace (agent guard shipped)"
```

---

## Self-Review

**Spec coverage:**
- Nil-provider guard in `Runner.Run` -> Task 1. ✓
- `PREPARE_FAILED` with the specified message + epoch fence -> Task 1, Step 3 (carries `r.epoch`, matching the existing prepare-error path). ✓
- Stale-comment fix in `main.go` -> Task 2. ✓
- Unit test asserting only `PREPARE_FAILED` and no command output -> Task 1, Steps 1-2. ✓
- Backlog closure with dispatch-filter follow-up retained -> Task 3. ✓

**Placeholder scan:** No TBD/TODO; all code steps show complete code and exact commands. ✓

**Type consistency:** `TaskStatusUpdate` fields (`TaskId`, `Status`, `ErrorMessage`, `Epoch`) and the `AgentMessage_TaskStatus` wrapper match the existing prepare-error path in `runner.go:124-134`. `r.send`, `r.taskID`, `r.epoch`, `r.provider` are all existing `Runner` members. `singleCmd`/`echoTaskCmd` are existing test helpers in `runner_test.go`. ✓

**Invariants check:** Epoch fence — the emitted status carries `r.epoch`, the runner's assigned epoch, consistent with every other status this runner sends; the guard does not touch `tasks.status` writes server-side (those remain epoch-fenced in the worker handler). No new gRPC senders, no teardown, no SQL. ✓
