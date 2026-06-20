---
date: 2026-06-19
topic: nil-provider-source-task-guard
status: approved
backlog: bug-2026-06-10-source-tasks-run-without-workspace
---

# Guard source-bearing tasks against a nil workspace provider

## Problem

`Runner.Run` gates the prepare phase on `task.Source != nil && r.provider != nil`
(`internal/agent/runner.go:113`). When the provider is nil - p4 preflight failed,
or `RELAY_WORKSPACE_ROOT` is unset - a task that carries a source spec skips the
prepare block entirely and falls straight through to command execution in the
agent's current working directory: no sync, no `P4CLIENT` env. The build
"succeeds" against whatever happens to be on disk, silently producing wrong
results.

Nothing upstream catches this. The dispatcher's `selectWorker`
(`internal/scheduler/dispatch.go:157-202`) has no provider-capability filter -
warm-workspace affinity is only a score bonus (lines 182-194) - so a
source-bearing task can and does land on a providerless worker. The comment at
`cmd/relay-agent/main.go:77-80` claims such tasks "will fail at dispatch with the
existing 'no source provider' path," but no such path exists. The comment is
stale and misleading.

## Fix

One behavioral change plus one comment correction. Scope is the agent-side guard
only; the dispatch-side provider-capability filter is explicitly deferred (see
Out of Scope).

### 1. Nil-provider guard in `Runner.Run`

Before the prepare block in `internal/agent/runner.go` (immediately ahead of the
existing `if task.Source != nil && r.provider != nil {` at line 113), add:

```go
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

This converts a silent wrong-success into a loud, correct `PREPARE_FAILED` - the
same status the existing prepare-error path already emits (`runner.go:124-134`).
The guard returns before any command runs.

**Correction (post-review):** an earlier draft of this spec claimed the server
already handles `PREPARE_FAILED` "with no other changes." That was wrong.
`handleTaskStatus` in `internal/worker/handler.go` maps only RUNNING/DONE/FAILED/
TIMED_OUT and drops everything else via `default: return`, so a `PREPARE_FAILED`
update leaves the task non-terminal (no retry, no dependent cascade, slot freed
only on disconnect). The fix therefore also includes a server-side change (item 3
below). This gap is pre-existing - the provider-error `PREPARE_FAILED` path has
always hit it - so the server-side fix corrects both paths.

The existing prepare block keeps its `r.provider != nil` half of the condition
unchanged; the new guard sits ahead of it and handles the nil-provider case
exclusively, so the two are mutually exclusive and the existing path is untouched
for the provider-present case.

### 2. Fix the stale comment in `cmd/relay-agent/main.go`

Replace the comment at `main.go:77-80` so it accurately describes the runtime
behavior: when the provider is disabled, source-bearing tasks are rejected by the
agent at run time with `PREPARE_FAILED` (not "at dispatch"); non-source tasks
still run. Keep it terse and matched to the surrounding comment style.

### 3. Handle `PREPARE_FAILED` server-side (post-review addition)

In `internal/worker/handler.go`, add a `case TASK_STATUS_PREPARE_FAILED:` to the
`handleTaskStatus` status switch mapping it to the existing `"failed"` string -
no new DB status value, no migration. This routes prepare failures through the
established terminal path (retry if configured, `FailDependentTasks` cascade,
job-status rollup, SSE events, slot release). Verified by an integration test
asserting the task transitions to `"failed"` with `finished_at` set.

## Testing

A unit test in `internal/agent/runner_test.go` mirroring
`TestRunner_PrepareFailureEmitsPrepareFailed` (line 101), but with **no provider
set** (so `r.provider` stays nil - `newRunner` leaves it nil by default; do not
call `SetProviderForTest`):

- Build a `DispatchTask` with a non-nil `Source` and a real echo command in
  `Commands`.
- Run it.
- Assert the emitted status sequence is exactly
  `[TASK_STATUS_PREPARE_FAILED]` - i.e. no `PREPARING`, no `RUNNING`, no `DONE`.
- Assert the echo command's stdout ("hello") never appears in any log chunk,
  proving no command executed.

This is a fast, Docker-free unit test. No new harness is required.

## Out of Scope

The longer-term dispatch-side fix - a provider-capability requirement in
`selectWorker` so source-bearing tasks are never dispatched to providerless
workers - is deferred. It needs workers to report provider capability over gRPC
and a new "unschedulable" handling path; it deserves its own spec. The backlog
item retains this as the documented follow-up direction.

## Files

- `internal/agent/runner.go` - add the nil-provider guard.
- `cmd/relay-agent/main.go` - correct the stale comment.
- `internal/agent/runner_test.go` - add the guard unit test.
- `internal/worker/handler.go` - map `PREPARE_FAILED` to terminal `"failed"`.
- `internal/worker/handler_test.go` - integration test for terminal PREPARE_FAILED.
- `docs/backlog/bug-2026-06-10-source-tasks-run-without-workspace.md` ->
  `docs/backlog/closed/` on completion.
