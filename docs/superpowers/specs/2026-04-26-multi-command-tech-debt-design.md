# Multi-Command Tech-Debt Cleanup — Design

**Date:** 2026-04-26
**Source backlog items:**
- [bug-2026-04-25-deprecated-command-proto-field](../../backlog/bug-2026-04-25-deprecated-command-proto-field.md)
- [bug-2026-04-25-down-migration-loses-multi-command](../../backlog/bug-2026-04-25-down-migration-loses-multi-command.md)
- [bug-2026-04-25-synthetic-step-markers-unstructured](../../backlog/bug-2026-04-25-synthetic-step-markers-unstructured.md)
**Source retro:** [2026-04-25-multi-command-tasks](../../retros/2026-04-25-multi-command-tasks.md)

## Summary

Three follow-up items from the multi-command-tasks retro. Two are code changes (remove a deprecated proto field; add structured step fields to log chunks); one is a backlog closure (the down-migration data-loss item is `wontfix` by design and should be moved to `closed/`). Shipped as a single PR with three logically-independent commits.

## Goals

- Retire the legacy `DispatchTask.command` proto field cleanly while preventing its number from ever being reused.
- Let log consumers (CLI, future UIs) attribute each `TaskLogChunk` to its step without parsing synthetic text markers.
- Acknowledge the down-migration limitation as accepted-by-design and remove it from the open backlog.

## Non-Goals

- Removing the legacy JSON `command:` field on the public job-spec — intentionally retained for back-compat with stored scheduled-job specs.
- Removing the synthetic `=== relay step N/M ===` text marker line. Kept for one release so existing log-tailing tools see no behavioral change (decision **3b/X** in brainstorm).
- Down-migration data fidelity. The `000008_task_commands.down.sql` script remains as-is; multi-command rows fail loudly when the down migration runs.
- Surfacing `step_index`/`step_total` in HTTP API responses or CLI output. The proto field flows through the existing SSE passthrough; downstream UI work is a separate task.
- Per-step `continue_on_error`, task-level `on_success`/`on_error`, or any new step semantics.

## Design

### 1. Remove deprecated `command` proto field

**File:** `proto/relayv1/relay.proto` (line 88, inside `message DispatchTask`).

Remove:
```proto
repeated string     command         = 3 [deprecated = true]; // superseded by commands; kept for one release
```

Add inside the message body (placement: just before the `commands` field for adjacency, or at the end — either is fine):
```proto
reserved 3;
reserved "command";
```

**Regeneration:** `buf generate`. The generated `Command []string` field on `DispatchTask` disappears.

**Verification:** `grep -rn '\.Command\b\|Command:' --include='*.go'` should show only:
- The legacy JSON-spec `Command` field on `TaskSpec` / `taskSpec` in `internal/api/{jobs,job_spec}.go` (intentional)
- The same field re-declared in `internal/schedrunner/runner.go` (intentional)
- `os/exec.Command` and `pgx` matches (unrelated)
- `CommandLine` matches (different type)

No references to `DispatchTask.Command` in non-generated code.

**Wire compatibility:** None broken. proto3 readers ignore unknown fields. Server stopped populating field 3 in the multi-command-tasks PR; agent stopped reading it. A hypothetical mixed-version situation has no behavior change.

**Tests:** No new tests. Existing suites must keep passing.

### 2. Close down-migration backlog item

Move [docs/backlog/bug-2026-04-25-down-migration-loses-multi-command.md](../../backlog/bug-2026-04-25-down-migration-loses-multi-command.md) → `docs/backlog/closed/`.

Append a `## Resolution` section to the moved file:

```markdown
## Resolution

Closed wontfix on 2026-04-26. Down-migration data fidelity for a feature
being rolled back is not worth the engineering cost. Behavior is documented
in `internal/store/migrations/000008_task_commands.down.sql` (multi-command
rows fail loudly during down-migration). If a multi-command row ever needs
to survive a downgrade, revisit then.
```

No code change.

### 3. Structured step fields on `TaskLogChunk`

**Proto change** (`proto/relayv1/relay.proto:58`):

```proto
message TaskLogChunk {
  string    task_id    = 1;
  LogStream stream     = 2;
  bytes     content    = 3;
  int64     epoch      = 4;
  int32     step_index = 5; // 1-indexed; 0 = not part of a numbered step (e.g. PREPARE phase)
  int32     step_total = 6; // total step count for the task; 0 when step_index == 0
}
```

Both fields are present on every chunk (decision **3a/B**: self-describing chunks let SSE consumers join mid-stream without prior context). Zero values mean "not in a numbered step" and are the correct value for `LOG_STREAM_PREPARE` chunks emitted before the per-step loop.

**Agent runner changes** (`internal/agent/runner.go`):

- Inside the `for i, cl := range task.Commands` loop in `Run`, compute `step := int32(i + 1)` and `total := int32(len(task.Commands))`.
- `sendStepMarker` gains the structured fields:
  ```go
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
  The text marker line is unchanged (decision **3b/X**).
- `pipeLog` gains two `int32` parameters and stamps them onto every chunk:
  ```go
  func (r *Runner) pipeLog(pipe io.Reader, stream relayv1.LogStream, stepIndex, stepTotal int32) {
      // ... existing read loop, with StepIndex: stepIndex, StepTotal: stepTotal added to TaskLogChunk
  }
  ```
  The two `go func() { ... r.pipeLog(...) }()` call sites in the loop pass the current `step, total`.
- `makePrepareProgressFn`'s `LOG_STREAM_PREPARE` chunks omit the new fields (default 0). No change to that function's signature.

**Single-command tasks:** The retro's "permissive input" normalization means even legacy single-command jobs reach the runner with `task.Commands` containing one `CommandLine`. They will emit `step_index=1, step_total=1` chunks — slightly more chatty than nothing, but consistent and harmless.

**Tests** (`internal/agent/runner_multistep_test.go`):

Three tests, extending what's already there:

1. **Existing 3-step success test** — extend assertions: every captured `TaskLogChunk` carries `step_total=3`, and `step_index` matches the step that produced it (1 for the marker + stdout chunks of step 1, then 2, then 3).
2. **Existing step-2-fails test** — assert chunks for steps 1 and 2 carry the right indices; no chunks with `step_index=3` ever appear.
3. **New single-command test** — a one-command task emits a marker and at least one stdout chunk, all with `step_index=1, step_total=1`.

No server-side or API tests needed; `TaskLogChunk` is passthrough through the SSE broker.

## Sequencing

Single PR off `master`, three commits in this order on the worktree branch `claude/reverent-panini-059ccf`:

1. `proto: remove deprecated DispatchTask.command field` — bug #1
2. `agent: structured step_index/step_total on TaskLogChunk` — bug #3
3. `backlog: close down-migration-loses-multi-command as wontfix` — bug #2

Order is deliberate: proto changes first (they touch generated files independently), then the runner change (uses `TaskLogChunk` so depends on the regenerated proto), then the backlog admin commit (zero code dependencies).

## Risks

- **Proto regeneration touches `relay.pb.go` line endings on Windows.** Same noise as the prior multi-command PR. Commit anyway; it's how the user's tooling behaves.
- **`pipeLog` signature change ripples to any caller.** Currently exactly two call sites in `Run`'s per-step loop. No external callers.

## Out of Scope (Separate Backlog)

- HTTP / CLI surfacing of `step_index` (e.g. `relay logs --by-step`).
- Removal of the synthetic text marker line in a future release once external tooling has migrated.
- Step-level error semantics (`continue_on_error`, hooks).
