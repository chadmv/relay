# Session Retro: 2026-04-22 — Major Concurrency Fixes

## What Was Built

A comprehensive set of correctness and scalability fixes for the relay-server's task dispatch and worker management pipeline, implemented across 19 tasks driven by a formal design spec:

- **Assignment epoch** (`assignment_epoch` column + DB migration): every `ClaimTaskForWorker` increments the epoch; `UpdateTaskStatus` and `AppendTaskLog` are epoch-gated, silently discarding stale zombie updates from prior worker assignments.
- **Per-worker serialized gRPC sends** (`internal/worker/sender.go`): a dedicated send goroutine per worker eliminates the concurrent `stream.Send` race; a `SenderRegistry` wraps it.
- **GraceRegistry** (`internal/worker/grace.go`): per-worker countdown timers give disconnected agents a configurable window (`RELAY_WORKER_GRACE_WINDOW`, default 2 min) to reconnect before their tasks are requeued.
- **Startup reconciliation**: on server start, grace timers are seeded from any workers with active tasks so an unclean-shutdown crash gap is covered.
- **Reconnect reconciliation**: the agent sends its running task list (task ID + epoch) in every `RegisterRequest`; the server diffs this against the DB and returns `cancel_task_ids` for tasks it no longer owns; unknown tasks are requeued.
- **Runner lifetime decoupled from connection** (`runCtx` field on `Agent`): runners bind to the agent's lifetime context, not the per-connection context, so subprocesses survive brief stream drops without being killed.
- **Runner.Abandon()**: suppresses the final status message when the coordinator's `RegisterResponse` signals a task was reassigned during grace expiry — prevents a stale DONE/FAILED from clobbering the new assignment.
- **LISTEN/NOTIFY dispatch** (`internal/scheduler/notify.go`): a `NotifyListener` holds a dedicated Postgres connection, LISTENs on `relay_task_submitted` / `relay_task_completed`, and calls `dispatcher.Trigger()` on each notification. The safety-net ticker was relaxed from 5s to 30s.
- **N×M query elimination**: the per-task×per-worker `CountActiveTasksForWorker` call in the dispatch loop was replaced by a single `CountActiveTasksByAllWorkers` aggregate query; an in-cycle `activeByWorker` map is incremented on each successful dispatch, preventing over-assignment of single-slot workers within one cycle.
- **Pool sizing**: `RELAY_DB_MAX_CONNS` env var (default 25) wires into `pgxpool.Config.MaxConns`; documented in `CLAUDE.md` alongside `RELAY_WORKER_GRACE_WINDOW`.
- **Slow SSE subscriber eviction** and **backpressure on runner send**: pre-existing silent-drop bugs fixed as prerequisites.

## Key Decisions

- **`runCtx` vs `connCtx` for runners**: runners store the agent's lifetime context, set once in `Agent.Run()`. `handleDispatch` is called from the recv goroutine in `connect()`, which shares the same `Run()` call frame — `a.runCtx` is safe to read without a mutex. The alternative (passing `runCtx` explicitly) would have required changing multiple function signatures.
- **`pg_notify` inside the job-submission transaction**: firing `NotifyTaskSubmitted` inside the DB transaction in `handleCreateJob` is correct — Postgres delivers NOTIFY only on commit, suppresses on rollback. No separate post-commit callback needed.
- **`triggerDispatch` removed from `Server`**: after the NOTIFY migration landed, `Server.triggerDispatch` had no callers. Removing the field + updating `api.New()` to 4 parameters was necessary — and cascaded to 19+ test call sites. Caught in a Task 17 code review.
- **`CountActiveTasksForWorker` retired**: once `CountActiveTasksByAllWorkers` landed, the old per-worker query had zero callers. Retired from `tasks.sql` + regenerated, to prevent future accidental use.
- **Worktree-based development**: all 19 tasks executed in a dedicated git worktree (`major-concurrency-fixes`), keeping `master` clean throughout.

## Problems Encountered

- **Task 11 data race**: `fired []string` written by a timer goroutine, read by the test goroutine — replaced with `atomic.Int32`. Caught by the race detector.
- **Task 11 timing fragility**: hard `time.Sleep(60ms)` assertion with a 30ms margin against a 50ms timer was flaky under load — replaced with `require.Eventually(t, ..., 500ms, 5ms)`.
- **Task 12 test flakiness**: `time.Sleep(100ms)` waiting for a subprocess to register in the `a.runners` map — replaced with `require.Eventually` polling the map.
- **Task 13 mutex invariant**: test wrote to `a.runners` without holding `a.mu` — required adding `a.mu.Lock()/Unlock()` wrappers in the test setup.
- **Task 17 terminal failure path**: the `NOTIFY` call was only wired for `status == "done"`, missing `"failed"` and `"timed_out"` — changed to `if terminal || statusStr == "done"` to cover all terminal statuses. Caught in code review.
- **Task 18 plan steps skipped**: the subagent skipped writing `TestDispatcher_UsesAggregateCountQuery` and retiring `CountActiveTasksForWorker`. Both were completed in a follow-up fix commit.
- **`"true"` not available on Windows**: `TestRunnerTagsOutgoingMessagesWithEpoch` and `TestAgent_dispatchAndReceiveLogs` both used `"true"` or bare `"echo"` as test commands — `exec.Command` fails silently on Windows, causing the tests to hang or timeout. Fixed by using the existing `echoCmd()` / `echoTaskCmd()` cross-platform helpers.

## Known Limitations

- **Grace window is per-server process**: if the server restarts (not just the agent), grace timers are re-seeded from the DB but the in-memory state is lost. Tasks assigned to workers that were online at crash time get a fresh grace window rather than the remaining time.
- **Single NotifyListener connection**: if the dedicated LISTEN connection drops, `NotifyListener.session()` reconnects with backoff. During the gap, the 30s safety-net poll provides coverage, but there is a window where a task submission is not immediately dispatched.

## What We Did Well

- **Code review after every task**: catching the `triggerDispatch` dead field, the terminal NOTIFY gap, and the skipped test steps before they accumulated into hard-to-debug state.
- **TDD discipline throughout**: every behaviour change was anchored by a failing test written first.
- **Cross-platform awareness**: the `echoCmd()` / `sleepCmd()` helpers were already established; catching Windows incompatibilities in unit tests before they reached CI.
- **Commit granularity**: 38 commits across 19 tasks made individual changes easy to reason about and review independently.

## What We Did Not Do Well

- **Plan step compliance**: two plan-required steps (regression test + query retirement) were silently skipped by the subagent in Task 18 and only caught via code review. The review process saved it, but the skipped steps shouldn't have happened.
- **Windows test coverage discovered late**: the `"true"` and bare `"echo"` issues in test commands were only caught at the integration test verification step of finishing-a-development-branch, not during task implementation.

## Files Most Touched

| File | Notes |
|---|---|
| `internal/worker/handler.go` | Added epoch-gating, reconcile logic, grace timer integration, NOTIFY on terminal status |
| `internal/worker/handler_test.go` | Full integration test suite for epoch gate, reconcile, grace timer disconnect |
| `internal/agent/agent.go` | `runCtx` field, `buildRegisterRequest()`, reconnect reconciliation, `Abandon` wiring |
| `internal/store/query/tasks.sql` | New queries: epoch-gated update/log, reconcile, aggregate count, NOTIFY, retired `CountActiveTasksForWorker` |
| `internal/store/tasks.sql.go` | Regenerated store layer for all new/modified queries |
| `internal/scheduler/dispatch.go` | Aggregate count query, in-cycle map tracking, 30s ticker, `sendTask` returns bool |
| `internal/proto/relayv1/relay.pb.go` | Generated from epoch + reconcile proto additions |
| `internal/worker/grace.go` | New: GraceRegistry with per-worker countdown timers |
| `internal/scheduler/notify.go` | New: NotifyListener LISTEN/NOTIFY wakeup |
| `internal/store/store_test.go` | New: epoch, reconcile, and aggregate count store-layer integration tests |

## Commit Range

`0f15a91..8660339`
