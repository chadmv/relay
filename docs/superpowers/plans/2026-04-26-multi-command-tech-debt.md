# Multi-Command Tech-Debt Cleanup — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close three follow-up items from the 2026-04-25 multi-command-tasks retro: remove the deprecated `DispatchTask.command` proto field, add structured `step_index`/`step_total` fields to `TaskLogChunk`, and close the down-migration data-loss item as `wontfix`.

**Architecture:** Three logically-independent changes shipped as three sequential commits on the current worktree branch (`claude/reverent-panini-059ccf`), opened as a single PR. Order: proto field removal → structured log chunk fields (proto + agent runner + tests) → backlog admin.

**Tech Stack:** Go 1.22+, protobuf via `buf generate`, agent subprocess runner, testify for assertions.

**Spec:** [docs/superpowers/specs/2026-04-26-multi-command-tech-debt-design.md](../specs/2026-04-26-multi-command-tech-debt-design.md)

---

## File Map

**Task 1 (proto field removal):**
- Modify: `proto/relayv1/relay.proto` (remove field, add `reserved`)
- Regenerate: `internal/proto/relayv1/relay.pb.go` (via `buf generate`)

**Task 2 (structured step fields):**
- Modify: `proto/relayv1/relay.proto` (add two `int32` fields to `TaskLogChunk`)
- Regenerate: `internal/proto/relayv1/relay.pb.go`
- Modify: `internal/agent/runner.go` (`sendStepMarker`, `pipeLog`, two call sites in `Run`)
- Modify: `internal/agent/runner_multistep_test.go` (extend two existing tests + add a third)

**Task 3 (backlog closure):**
- Move: `docs/backlog/bug-2026-04-25-down-migration-loses-multi-command.md` → `docs/backlog/closed/`
- Append `## Resolution` section to the moved file.

---

## Task 1: Remove deprecated `DispatchTask.command` proto field

**Files:**
- Modify: `proto/relayv1/relay.proto:88`
- Regenerate: `internal/proto/relayv1/relay.pb.go`

### - [ ] Step 1.1: Edit the proto

In `proto/relayv1/relay.proto`, locate the `DispatchTask` message (line 85). Remove this line (currently line 88):

```proto
  repeated string     command         = 3 [deprecated = true]; // superseded by commands; kept for one release
```

Add these two lines inside the same message body (anywhere — placement before the `commands` field is natural):

```proto
  reserved 3;
  reserved "command";
```

The full message after edit should look like:

```proto
message DispatchTask {
  string              task_id         = 1;
  string              job_id          = 2;
  reserved 3;
  reserved "command";
  map<string, string> env             = 4;
  int32               timeout_seconds = 5;
  int64               epoch           = 6;
  SourceSpec          source          = 7;
  repeated CommandLine commands       = 8;
}
```

### - [ ] Step 1.2: Regenerate the Go bindings

Run:
```bash
buf generate
```

Expected: command exits 0; `internal/proto/relayv1/relay.pb.go` is rewritten. The generated `Command []string` field on the `DispatchTask` struct disappears.

If `buf` is not on PATH on Windows, fall back to running it from the relay root directly (it's installed via the project's tooling). Do **not** run `make generate` here — it also runs `sqlc generate`, which would touch unrelated `*.sql.go` files; that's acceptable noise but unnecessary for this task.

### - [ ] Step 1.3: Verify no stale references

Run:
```bash
grep -rn 'dispatchTask\.Command\b\|\.Command\b' --include='*.go' | grep -v '_test.go\|\.pb\.go\|exec\.Command\|CommandLine\|JSON\|TaskSpec\|taskSpec\|schedrunner'
```

Expected: empty output. (The legacy JSON-spec `Command` field on `TaskSpec` in `internal/api/{jobs,job_spec}.go` and `internal/schedrunner/runner.go` is intentional back-compat and lives on different types — not the proto `DispatchTask.Command`.)

If anything else turns up, stop and investigate before proceeding.

### - [ ] Step 1.4: Build and run all unit tests

Run:
```bash
go build ./...
go test ./...
```

Expected: build succeeds; all unit tests pass. Any compile error here means a non-test caller of the proto field was missed; fix and re-run.

### - [ ] Step 1.5: Commit

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go
git commit -m "$(cat <<'EOF'
proto: remove deprecated DispatchTask.command field

Server stopped populating it and agent stopped reading it in the
multi-command-tasks change. Field number 3 and the name "command" are
now reserved so they can never be reused.

Closes the "Deprecated `command` proto field still defined" backlog item
from the 2026-04-25 multi-command-tasks retro.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

Verify with `git log -1 --oneline` and `git status` (should be clean).

---

## Task 2: Add structured `step_index`/`step_total` to `TaskLogChunk`

**Files:**
- Modify: `proto/relayv1/relay.proto:58` (the `TaskLogChunk` message)
- Regenerate: `internal/proto/relayv1/relay.pb.go`
- Modify: `internal/agent/runner.go` (`sendStepMarker`, `pipeLog`, the two call sites in `Run`)
- Modify: `internal/agent/runner_multistep_test.go` (extend two tests, add a third)

### - [ ] Step 2.1: Add the proto fields

In `proto/relayv1/relay.proto`, replace the `TaskLogChunk` message (currently lines 58–63) with:

```proto
message TaskLogChunk {
  string    task_id    = 1;
  LogStream stream     = 2;
  bytes     content    = 3;
  int64     epoch      = 4;
  int32     step_index = 5; // 1-indexed step number; 0 = not part of a numbered step (e.g. PREPARE chunks)
  int32     step_total = 6; // total step count for the task; 0 when step_index == 0
}
```

### - [ ] Step 2.2: Regenerate

Run:
```bash
buf generate
```

Expected: `internal/proto/relayv1/relay.pb.go` rewritten. The `TaskLogChunk` struct gains `StepIndex int32` and `StepTotal int32` fields.

### - [ ] Step 2.3: Write the failing test — extend the 3-step success test

Open `internal/agent/runner_multistep_test.go`. Replace `TestRunner_MultiStepAllSucceed` (lines 34–61) with this version, which adds structural assertions on every captured `TaskLogChunk`:

```go
func TestRunner_MultiStepAllSucceed(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	runner, runCtx := newRunner("multi-ok", 0, sendCh, context.Background(), 0)
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "multi-ok",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("alpha")},
			{Argv: echoArgv("bravo")},
			{Argv: echoArgv("charlie")},
		},
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_DONE, last.Status)
	require.NotNil(t, last.ExitCode)
	assert.Equal(t, int32(0), *last.ExitCode)

	logs := collectStdoutLogs(msgs)
	assert.Equal(t, 3, strings.Count(logs, "=== relay step"),
		"expected one step marker per command, logs:\n%s", logs)
	for _, want := range []string{"step 1/3", "step 2/3", "step 3/3", "alpha", "bravo", "charlie"} {
		assert.Contains(t, logs, want)
	}

	// Every TaskLogChunk emitted during a step must carry step_total=3 and a
	// step_index matching the step that produced it. Chunks with step_index=0
	// would only appear from PREPARE-phase output, which this task does not have.
	stepCounts := map[int32]int{}
	for _, m := range msgs {
		l := m.GetTaskLog()
		if l == nil {
			continue
		}
		assert.Equal(t, int32(3), l.StepTotal, "every chunk should carry step_total=3, got chunk %q", string(l.Content))
		assert.GreaterOrEqual(t, l.StepIndex, int32(1), "every chunk in this task should belong to a numbered step")
		assert.LessOrEqual(t, l.StepIndex, int32(3))
		stepCounts[l.StepIndex]++
	}
	assert.Greater(t, stepCounts[1], 0, "expected at least one chunk for step 1")
	assert.Greater(t, stepCounts[2], 0, "expected at least one chunk for step 2")
	assert.Greater(t, stepCounts[3], 0, "expected at least one chunk for step 3")
}
```

### - [ ] Step 2.4: Extend the fail-fast test

Replace `TestRunner_MultiStepFailFastSkipsRest` (lines 63–90) with this version:

```go
func TestRunner_MultiStepFailFastSkipsRest(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	runner, runCtx := newRunner("multi-fail", 0, sendCh, context.Background(), 0)
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "multi-fail",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("first-ok")},
			{Argv: failArgv()},
			{Argv: echoArgv("must-not-run")},
		},
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_FAILED, last.Status)
	require.NotNil(t, last.ExitCode, "failing step's exit code must be reported")
	assert.Equal(t, int32(7), *last.ExitCode)

	logs := collectStdoutLogs(msgs)
	assert.Contains(t, logs, "first-ok", "step 1 stdout should be present")
	assert.Contains(t, logs, "step 1/3")
	assert.Contains(t, logs, "step 2/3")
	assert.NotContains(t, logs, "step 3/3", "step 3 must not have run after step 2 failed")
	assert.NotContains(t, logs, "must-not-run", "step 3 stdout must not be present")

	// No chunk with step_index=3 should have been emitted (step 3 never ran).
	for _, m := range msgs {
		l := m.GetTaskLog()
		if l == nil {
			continue
		}
		assert.NotEqual(t, int32(3), l.StepIndex, "no chunk should claim to belong to step 3 (it never ran); chunk: %q", string(l.Content))
		// And every emitted chunk should report step_total=3.
		if l.StepIndex != 0 {
			assert.Equal(t, int32(3), l.StepTotal, "chunk in step %d should report step_total=3", l.StepIndex)
		}
	}
}
```

### - [ ] Step 2.5: Add a single-command test

Append this new test function to the bottom of `internal/agent/runner_multistep_test.go` (before `collectStdoutLogs`):

```go
func TestRunner_SingleCommandReportsStepOneOfOne(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	runner, runCtx := newRunner("single", 0, sendCh, context.Background(), 0)
	runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "single",
		Commands: []*relayv1.CommandLine{
			{Argv: echoArgv("only")},
		},
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	last := msgs[len(msgs)-1].GetTaskStatus()
	require.NotNil(t, last)
	assert.Equal(t, relayv1.TaskStatus_TASK_STATUS_DONE, last.Status)

	sawChunk := false
	for _, m := range msgs {
		l := m.GetTaskLog()
		if l == nil {
			continue
		}
		sawChunk = true
		assert.Equal(t, int32(1), l.StepIndex, "single-command task must report step_index=1, chunk: %q", string(l.Content))
		assert.Equal(t, int32(1), l.StepTotal, "single-command task must report step_total=1, chunk: %q", string(l.Content))
	}
	assert.True(t, sawChunk, "expected at least one TaskLogChunk (the marker)")
}
```

### - [ ] Step 2.6: Run the tests and verify they fail

Run:
```bash
go test ./internal/agent/... -run TestRunner_MultiStep -v -timeout 30s
go test ./internal/agent/... -run TestRunner_SingleCommandReportsStepOneOfOne -v -timeout 30s
```

Expected: all three test functions FAIL with assertion errors of the form "every chunk should carry step_total=3, got chunk ..." (because the runner currently leaves both fields at their zero value of 0).

If any test compiles-but-passes here, the implementation is already in place — go re-read `internal/agent/runner.go` to figure out why before continuing.

If a test fails to compile, you missed Step 2.2 — re-run `buf generate`.

### - [ ] Step 2.7: Implement — update `sendStepMarker`

In `internal/agent/runner.go`, replace `sendStepMarker` (lines 193–205) with:

```go
// sendStepMarker writes a synthetic delimiter line into the stdout stream so
// the consolidated log can be split per step. step_index and step_total are
// also stamped onto the chunk for structured consumers; the text marker is
// retained for log-tailing tools that don't (yet) read the structured fields.
func (r *Runner) sendStepMarker(step, total int32, argv []string) {
	line := []byte("=== relay step " + strconv.Itoa(int(step)) + "/" + strconv.Itoa(int(total)) + " === " + strings.Join(argv, " ") + "\n")
	r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskLog{
		TaskLog: &relayv1.TaskLogChunk{
			TaskId:    r.taskID,
			Stream:    relayv1.LogStream_LOG_STREAM_STDOUT,
			Content:   line,
			Epoch:     r.epoch,
			StepIndex: step,
			StepTotal: total,
		},
	}})
}
```

(Signature change: `step, total int` → `step, total int32`. The previous `strconv.Itoa(step)` becomes `strconv.Itoa(int(step))`.)

### - [ ] Step 2.8: Implement — update `pipeLog`

In `internal/agent/runner.go`, replace `pipeLog` (lines 207–229) with:

```go
func (r *Runner) pipeLog(pipe io.Reader, stream relayv1.LogStream, stepIndex, stepTotal int32) {
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			r.send(&relayv1.AgentMessage{
				Payload: &relayv1.AgentMessage_TaskLog{
					TaskLog: &relayv1.TaskLogChunk{
						TaskId:    r.taskID,
						Stream:    stream,
						Content:   chunk,
						Epoch:     r.epoch,
						StepIndex: stepIndex,
						StepTotal: stepTotal,
					},
				},
			})
		}
		if err != nil {
			return
		}
	}
}
```

### - [ ] Step 2.9: Implement — update the two `pipeLog` call sites in `Run`

In `internal/agent/runner.go`, the per-step loop (lines 129–164) computes step number from the loop index. Update the loop body so the step variables are visible to the marker call and to the pipe goroutines.

Replace lines 129–164 (the `for i, cl := range task.Commands { ... }` body up to and including `wg.Wait()` and the `waitErr := cmd.Wait()` line) with:

```go
	for i, cl := range task.Commands {
		if cl == nil || len(cl.Argv) == 0 {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}
		argv := cl.Argv
		step := int32(i + 1)
		stepTotal := int32(total)
		r.sendStepMarker(step, stepTotal, argv)

		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.WaitDelay = 5 * time.Second // bound pipe draining after process kill
		cmd.Env = env
		if workDir != "" {
			cmd.Dir = workDir
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}

		if err := cmd.Start(); err != nil {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); r.pipeLog(stdout, relayv1.LogStream_LOG_STREAM_STDOUT, step, stepTotal) }()
		go func() { defer wg.Done(); r.pipeLog(stderr, relayv1.LogStream_LOG_STREAM_STDERR, step, stepTotal) }()
		wg.Wait()

		waitErr := cmd.Wait()
```

(Note: `total` is already declared as `total := len(task.Commands)` at line 126 — leave that line alone. `step` and `stepTotal` are new local variables in the loop scope; `stepTotal` is just an `int32` view of the existing `total` so it can be passed to the `int32`-typed `pipeLog` and `sendStepMarker` parameters.)

The rest of the loop (`lastExitCode = nil` through `break`) is unchanged.

### - [ ] Step 2.10: Run the tests and verify they pass

Run:
```bash
go test ./internal/agent/... -run TestRunner_MultiStep -v -timeout 30s
go test ./internal/agent/... -run TestRunner_SingleCommandReportsStepOneOfOne -v -timeout 30s
```

Expected: all three pass.

Then run the full agent test suite to confirm no regression:
```bash
go test ./internal/agent/... -timeout 60s
```

Expected: PASS. (The PREPARE-phase progress chunks naturally have `StepIndex=0, StepTotal=0` since `makePrepareProgressFn` was untouched — that matches the spec.)

### - [ ] Step 2.11: Commit

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go internal/agent/runner.go internal/agent/runner_multistep_test.go
git commit -m "$(cat <<'EOF'
agent: structured step_index/step_total on TaskLogChunk

Adds two int32 fields to TaskLogChunk so log consumers can attribute each
chunk to its step without parsing the synthetic '=== relay step N/M ==='
marker line. The marker line itself is retained so existing log-tailing
tools see no behavioral change.

step_index is 1-indexed; step_index=0 means the chunk is not part of a
numbered step (e.g. PREPARE-phase progress output). step_total is set on
every per-step chunk so SSE consumers joining mid-stream can render
"step N of M" without prior context. Single-command tasks emit
step_index=1, step_total=1.

Closes the "Synthetic step markers aren't structured" backlog item from
the 2026-04-25 multi-command-tasks retro.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

Verify with `git log -1 --oneline` and `git status` (should be clean).

---

## Task 3: Close down-migration backlog item as `wontfix`

**Files:**
- Move: `docs/backlog/bug-2026-04-25-down-migration-loses-multi-command.md` → `docs/backlog/closed/bug-2026-04-25-down-migration-loses-multi-command.md`
- Modify: the moved file (append `## Resolution` section)

### - [ ] Step 3.1: Move the file with `git mv`

```bash
git mv docs/backlog/bug-2026-04-25-down-migration-loses-multi-command.md docs/backlog/closed/
```

Expected: command exits 0; `git status` shows the file as `renamed`.

### - [ ] Step 3.2: Append the Resolution section

Open `docs/backlog/closed/bug-2026-04-25-down-migration-loses-multi-command.md` and append (after the existing `## Summary` section):

```markdown

## Resolution

Closed `wontfix` on 2026-04-26. Down-migration data fidelity for a feature
being rolled back is not worth the engineering cost. Behavior is documented
in `internal/store/migrations/000008_task_commands.down.sql` (multi-command
rows fail loudly during down-migration). If a multi-command row ever needs
to survive a downgrade, revisit then.
```

### - [ ] Step 3.3: Commit

```bash
git add docs/backlog/closed/bug-2026-04-25-down-migration-loses-multi-command.md
git commit -m "$(cat <<'EOF'
backlog: close down-migration-loses-multi-command as wontfix

Down-migration data fidelity for a feature being rolled back is not worth
the engineering cost. The 000008_task_commands.down.sql script is honest
about the limitation (it fails loudly on multi-command rows). If anyone
ever needs a multi-command row to survive a downgrade, revisit then.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

Verify with `git log --oneline -4` (should show: this commit, structured-step-fields commit, proto-removal commit, spec commit).

---

## Final Verification

### - [ ] Step F.1: Full unit test suite

Run:
```bash
go test ./... -timeout 120s
```

Expected: PASS across all packages. No package should fail; if one does, investigate before opening the PR.

### - [ ] Step F.2: Confirm branch state

Run:
```bash
git log --oneline master..HEAD
git status
```

Expected: four commits ahead of `master` (spec + three implementation commits). Working tree clean.

### - [ ] Step F.3: Hand off for PR

The plan stops here. The user will open the PR (or invoke `superpowers:finishing-a-development-branch` to handle that step).

---

## Notes for the Implementer

- **Do not run `make generate`** for these tasks. It runs both `sqlc generate` and `buf generate`; sqlc on Windows will rewrite `*.sql.go` files purely for line-ending churn (a known annoyance flagged in the prior session retro). Just run `buf generate` directly.
- **Integration tests are NOT required** for this work — none of the changed code paths have integration coverage today, and the unit tests in `internal/agent/runner_multistep_test.go` exercise the real subprocess execution end-to-end. Skip `make test-integration`.
- **Generated files (`internal/proto/relayv1/relay.pb.go`) ARE checked in** — include them in the same commit as the corresponding `.proto` change.
- **`git mv` (Task 3) is required** rather than `mv` + `git add`, so git tracks the file as a rename and history is preserved.
