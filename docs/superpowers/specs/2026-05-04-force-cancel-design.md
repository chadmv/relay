# Design: Force Cancel & Process-Tree Kill on `relay cancel`

**Date:** 2026-05-04
**Status:** Approved (pending implementation plan)

## Background

`relay cancel <job-id>` today marks all non-terminal tasks as `failed` in the database, sets the job to `cancelled`, and sends a `CancelTask` gRPC message to each agent owning a running task. The agent calls `runner.Cancel()`, which cancels the runner's `context.Context`. Because the runner uses `exec.CommandContext`, Go's stdlib then calls `Process.Kill()` — `SIGKILL` on Unix, `TerminateProcess` on Windows.

Two problems with the current behavior:

1. **The README is inaccurate.** It says "running tasks complete their current execution," which suggests a graceful shutdown. The implementation immediately hard-kills the direct child subprocess with no grace period.

2. **Only the direct child is killed; grandchildren leak.** A task such as `python train.py` whose script forks a data-loader subprocess, or a Make target that forks a compiler, leaves orphaned grandchildren consuming the worker's CPU/RAM/GPU after cancel. There is no relay handle to clean them up; they survive until the worker process itself exits or the OS reaps them on shutdown.

## Goals

- Make `relay cancel` kill the entire process tree by default, eliminating the grandchild leak.
- Add a `--force` flag for callers who want to free the worker as quickly as possible, accepting some loss of log tail and skipping workspace cleanup.
- Bring the README in line with actual behavior.

## Non-goals

- A graceful `SIGTERM` → wait → `SIGKILL` ladder. Tasks today have no contract for handling termination signals; introducing one is a separable feature.
- Synchronous server-side waiting for the agent to ACK the cancel. The API stays fire-and-forget; the database transition to `failed` is the source of truth, and existing reconciliation (the `CancelTaskIds` field in `RegisterResponse` after a reconnect) handles the disconnected-agent case.
- Changing how multi-step tasks behave on cancel. The break-out-of-loop logic at [runner.go:181](../../../internal/agent/runner.go#L181) already stops subsequent steps from running.

## Behavior matrix

| Step | Default cancel | `--force` cancel |
|---|---|---|
| API marks tasks `failed` in DB | yes | yes |
| API sets job `cancelled` | yes | yes |
| API returns to client | immediately | immediately |
| gRPC `CancelTask{force=false}` sent to agent | yes | — |
| gRPC `CancelTask{force=true}` sent to agent | — | yes |
| Agent kills direct subprocess | yes | yes |
| Agent kills entire process tree | **yes (new)** | yes |
| Agent waits up to 5s `WaitDelay` for pipe drain | yes | no (negligible WaitDelay) |
| Agent runs workspace `Finalize` | yes | no |
| Agent sends final `TaskStatus=FAILED` | yes | yes (best-effort) |

The `failed` DB transition occurs before the gRPC kill is delivered. If the agent never receives the message (disconnected at cancel time), the existing `RegisterResponse.CancelTaskIds` mechanism cleans up on reconnect; no change to that path.

`--force` skipping `Finalize` may leave a Perforce workspace partially synced or with uncommitted local state. The next dispatch on that worker treats it as a cold-sync target. This is an accepted cost for the impatient case.

## Process-tree kill mechanism

Implemented in `internal/agent/runner.go` plus a new `proctree_unix.go` / `proctree_windows.go` pair for the platform-specific bits.

**Linux/macOS:**
- Set `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` before `cmd.Start()`. The child becomes the leader of its own process group (PGID = child PID); descendants inherit that PGID by default.
- Override `cmd.Cancel` (Go 1.20+) to call `syscall.Kill(-pid, syscall.SIGKILL)` — negative PID kills the whole group.

**Windows:**
- Create a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.
- After `cmd.Start()`, `AssignProcessToJobObject(job, cmd.Process.Handle)`.
- Override `cmd.Cancel` to call `TerminateJobObject(job, 1)`.
- Close the Job Object handle in `defer` after `cmd.Wait()`. `KILL_ON_JOB_CLOSE` is a safety net if the agent itself crashes.
- Win32 calls via `golang.org/x/sys/windows`.

**Why process groups / Job Objects rather than `pkill -P` / `taskkill /T`:**
Both alternatives walk parent-PID metadata, which is racy: a grandchild that has already re-parented (double-fork, daemonization, detached subprocess) is invisible to the walk. Process groups (Unix) and Job Objects (Windows) are kernel-tracked and capture every descendant regardless of re-parenting.

**Force vs. default in the Cancel override:**
`cmd.WaitDelay` stays at 5s for both modes (it's set at `Cmd` construction time, before we know whether cancel will be forced). The `cmd.Cancel` override:
- Always issues the tree-kill (group kill or job terminate).
- If `r.forced.Load()`: additionally calls `Close()` on the `stdout` and `stderr` pipe handles the runner already holds (the values returned by `cmd.StdoutPipe()` / `cmd.StderrPipe()` at [runner.go:146-155](../../../internal/agent/runner.go#L146)). This unblocks the pipe-reader goroutines instantly with an EOF/closed-pipe error, so `wg.Wait()` returns within milliseconds and the kernel's pipe-buffer drain is bypassed.
- If not forced: pipe handles are left alone; the goroutines drain naturally as the kernel closes the pipes when the killed process is reaped, bounded by the 5s `WaitDelay`.

The pipe-handle references are passed into the runner state (or captured in a closure attached to `cmd.Cancel`) at the per-step exec site — they live for the lifetime of one `cmd` and are nil between steps.

## Wire format & API surface

**Proto change** (`proto/relayv1/relay.proto`):

```proto
message CancelTask {
  string task_id = 1;
  bool force = 2;  // skip workspace finalize and pipe drain; tree-kill is always on
}
```

Backwards-compatible: old agents ignore the new field and do today's direct-child kill (no regression). New agents receiving `force=false` from any server perform tree-kill with cleanup. Run `make generate` after editing `relay.proto`.

**REST API** (`internal/api/jobs.go`):
- Route unchanged: `DELETE /v1/jobs/{id}`.
- New optional query param: `?force=true`. Parsed via `strconv.ParseBool` (accepts `1`, `t`, `T`, `true`, `TRUE`, `True`); parse errors and missing param both yield `false`.
- `handleCancelJob` propagates the value into the `CancelTask.Force` field for each outgoing message. DB transaction, broker publish, and response shape are unchanged.

**CLI** (`internal/cli/jobs.go`, `cmd/relay/main.go`):
- `relay cancel <job-id>` — default cancel, no flag needed.
- `relay cancel <job-id> --force` — adds `?force=true` to the `DELETE` URL.
- Stdlib `flag` package, consistent with the rest of the CLI.
- Flag help: `force termination: kills the entire process tree immediately, skips workspace cleanup. May leave workspaces in a dirty state.`

**Agent** (`internal/agent/agent.go`, `internal/agent/runner.go`):
- `handleCancel(msg *relayv1.CancelTask)` calls `r.Cancel(msg.Force)`.
- `Runner.Cancel(force bool)` stores `force` in a new `forced atomic.Bool` field, then cancels the context.
- The deferred workspace `Finalize` at [runner.go:88](../../../internal/agent/runner.go#L88) checks `r.forced.Load()` and skips `Finalize` if true.
- The `cmd.Cancel` override (above) consults `r.forced.Load()` to choose `WaitDelay`.

## Testing

**Unit tests** (no Docker; majority of coverage):

`internal/agent/runner_cancel_test.go` (new):
- `TestRunner_DefaultCancel_KillsProcessTree` — spawns a parent that forks a child sleeping 60s; default cancel; assert both die within 2s.
- `TestRunner_ForceCancel_SkipsWorkspaceFinalize` — fake provider increments a counter in `Finalize`; assert counter == 0 after force cancel, == 1 after default cancel.
- `TestRunner_ForceCancel_ReturnsQuickly` — long-running subprocess writing constant output; force cancel returns within 500ms (vs. ~5s for default).
- `TestRunner_DefaultCancel_DrainsPipeBeforeReturn` — subprocess emits a final log line before death (e.g. via shell trap); assert default cancel delivers that line via `sendCh`.

`internal/agent/agent_test.go`:
- `TestAgent_HandleCancel_PropagatesForceFlag` — fake runner records the `force` value passed to `Cancel`; assert both true and false paths deliver correctly.

`internal/api/jobs_cancel_test.go` (new or extension of existing):
- `TestCancelJob_Default_SendsForceFalse` — fake registry captures the outgoing `CoordinatorMessage`; assert `CancelTask.Force == false`.
- `TestCancelJob_Force_SendsForceTrue` — `DELETE /v1/jobs/{id}?force=true`; assert `CancelTask.Force == true`.
- `TestCancelJob_Force_QueryParamParsing` — confirm `strconv.ParseBool` semantics: `?force=true`, `?force=1`, `?force=TRUE` → true; missing or junk value → false (no error).

`internal/cli/jobs_test.go`:
- `TestCancel_FlagPlumbedToRequest` — fake HTTP server; `relay cancel <id> --force` produces a `DELETE` whose URL includes `?force=true`.

**Integration tests** (Docker required, `//go:build integration`):

`internal/agent/runner_cancel_integration_test.go` (new):
- `TestRunner_TreeKill_RealSubprocesses` — spawns a real shell that forks a real grandchild; polls for the grandchild PID; asserts gone within 2s. One body per platform behind `runtime.GOOS` checks; skip on the others.

**Out of scope for tests:**
- Agent-crash → Job Object cleanup (`KILL_ON_JOB_CLOSE` safety net): defense-in-depth path, hard to test meaningfully; covered by code review of the `defer`.
- `Finalize` error suppression on `--force`: `Finalize` simply isn't called.

## Documentation

- README §`relay cancel`: rewrite to describe actual behavior — default cancel hard-kills the entire process tree but waits briefly for log drain and runs workspace cleanup; `--force` skips both.
- README REST API table: add note about `?force=true` query param on `DELETE /v1/jobs/{id}`.

## Files touched

- `proto/relayv1/relay.proto` — add `force` field to `CancelTask`.
- `internal/proto/relayv1/relay.pb.go` — regenerated.
- `internal/api/jobs.go` — parse `?force=true`, propagate into outgoing `CancelTask` messages.
- `internal/api/jobs_cancel_test.go` — new or extended test file.
- `internal/agent/agent.go` — `handleCancel` passes `msg.Force` through.
- `internal/agent/runner.go` — `Cancel(force bool)`, new `forced atomic.Bool`, conditional `Finalize` skip, `cmd.Cancel` override.
- `internal/agent/proctree_unix.go` (new) — `Setpgid` setup, group-kill helper.
- `internal/agent/proctree_windows.go` (new) — Job Object setup, terminate helper.
- `internal/agent/runner_cancel_test.go` (new) — unit tests.
- `internal/agent/runner_cancel_integration_test.go` (new) — integration tests.
- `internal/agent/agent_test.go` — propagation test.
- `internal/cli/jobs.go`, `cmd/relay/main.go` — `--force` flag, query param.
- `internal/cli/jobs_test.go` — flag plumbing test.
- `README.md` — accurate prose, `?force=true` documented.
