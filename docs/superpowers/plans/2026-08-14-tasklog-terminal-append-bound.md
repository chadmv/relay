# Task-Log Terminal Append Bound Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound how long a finished task accepts log appends, so an agent holding worker W's token can no longer write rows into a task W finished at epoch N forever, while the deliberate trailing-log flush that arrives just after a terminal status still lands.

**Architecture:** One extra predicate in the `fence` CTE of `AppendTaskLog` (`internal/store/query/tasks.sql`), spelled as a two-arm **disjunction**: a status allow-list covering live tasks, OR `finished_at > <cutoff>` covering recently-finished ones. The cutoff is an absolute `timestamptz` computed in Go from a new `Handler.TrailingLogWindow` (default 15m, env `RELAY_TASKLOG_TRAILING_WINDOW`), never `NOW() - interval`, so the comparison stays in one clock domain. No migration, no proto change, no new query, no new round trip, no change to the rejection path (a rejected chunk still surfaces as `pgx.ErrNoRows` and is still dropped silently, before the publish).

**Tech Stack:** Go, PostgreSQL, sqlc v1.30.0, pgx/v5, gRPC, testcontainers-go (integration tests), testify.

**Slice independence:** This is **backend-only**. Every file touched is Go, SQL or Markdown. **Zero files under `web/`.** The conductor should dispatch `relay-backend-engineer` only and must NOT allocate a frontend slice for Phase 3. There is no FE/BE ordering question because there is no FE. The tasks below are **strictly sequential** (each depends on the previous one's tree state); do not parallelize them. Tasks 6, 7 and 8 are order-independent among themselves but all depend on Task 3.

---

## Spec

Approved spec: `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`

Backlog item being closed: `docs/backlog/bug-2026-08-12-tasklog-terminal-task-append-unbounded.md`

**Out of scope, do not touch.** If you find yourself editing any of these, stop and report:

- `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` (the logging rate limiter). Separate slice, scheduled next. Do not touch `taskLogErrLimiter` or `handleTaskStatus`'s log lines.
- `bug-2026-08-12-tasklog-epoch-int32-truncation`. You will be editing the exact struct literal that contains `AssignmentEpoch: int32(chunk.Epoch)` in `internal/worker/handler.go`. **Leave that line exactly as it is.** It is a different defect with its own filed item; folding it in blurs attribution. Flag it in your report, do not fix it.
- `task_logs` retention and any per-task volume cap. Both proposed as new items in the spec (section 10), neither filed, neither in scope.

---

## Verification findings: where the spec is wrong or incomplete

The planner re-derived every claim in the spec against the tree at `ee88de0`. **The spec is materially accurate** - the SQL it quotes is byte-accurate, the pinning test says what it says it says, `finished_at` exists at `internal/store/migrations/000001_initial.up.sql:64`, `RetryJobTasks` really does reopen terminal rows, `DeleteJob` really has no caller, and the single-clock-domain argument holds (see finding 8). These are the deltas the implementer must carry:

1. **Section 6.1's claim that the item's spelling "fails open on all three" is overstated, and the plan's mutation battery depends on getting this right.** The spec lists three fail-closed properties (a terminal row with a NULL `finished_at`, a caller that omits the cutoff, a status added later) and asserts the item's `t.finished_at IS NULL OR t.finished_at > cutoff` spelling fails open on all of them. It fails open on **one**: the NULL `finished_at` row. For "the caller omits the cutoff", the item's spelling still rejects a terminal row whose `finished_at` is set, because `finished_at IS NULL` is false and `finished_at > NULL` is NULL - so that property is **not** discriminated by the `IS NULL` mutation and needs a different one (`COALESCE(cutoff, '-infinity')`). Task 4 Step 6 and Appendix A both use the corrected pairing. Getting this wrong would have shipped a plan whose predicted RED never appears, which is precisely the "plan-supplied tests are untrusted" failure mode.

2. **The call-site count is wrong in two places, and this plan uses the corrected figure.** Spec section 2.1 says "nine test call sites in `internal/store` and one in `internal/store/retry_job_tasks_integration_test.go`", and section 8.4 says "the eight other ... and the one in `retry_job_tasks_integration_test.go`". Both double-count: `retry_job_tasks_integration_test.go` **is** in `internal/store`. The real inventory is **9 test call sites total**, enumerated exhaustively in Task 4.

3. **Spec 8.2's boundary test would be flaky and is weaker than it needs to be.** It proposes `h.TrailingLogWindow = 50 * time.Millisecond`, an immediate append, a 150 ms sleep and a second append. A 50 ms budget spanning two testcontainer round trips will flake on a loaded machine, and a sleep is not needed at all. Task 5 replaces it with a sleep-free two-leg test: backdate `finished_at` 2 seconds, run the same chunk through the same handler twice with `TrailingLogWindow` = 1 s then 1 h. The two legs differ in **nothing but the field**, which is a stronger wiring proof than a sleep.

4. **Spec 8.2 and 8.3 backdate with `NOW() - interval '1 hour'`, i.e. the database clock**, in a design whose entire justification is that the comparison stays in one clock domain. Every test in this plan writes `finished_at` from the Go clock (via `UpdateTaskStatus`, exactly as production does). Small, but it is precisely the failure shape the design argues against, and `bug-2026-08-13-token-expiry-two-clocks` is the same shape elsewhere in this codebase.

5. **The spec names `TASK_STATUS_PREPARING` as the trap but not its terminal twin.** `proto/relayv1/relay.proto:139` also declares `TASK_STATUS_PREPARE_FAILED = 6`. If both ever become persisted statuses they need **opposite** treatment at this site (`preparing` IN, `prepare_failed` OUT). Both are named in the statement comment and in the vocabulary guard.

6. **Two stale-prose hazards the spec does not mention**, both created by this change:
   - `internal/store/store_test.go:314-318` hard-codes the fence's current parameter numbering ("`$3,$4 -> $4,$5` for stream and content"). After this change stream/content are `$5,$6`. Task 4 updates it.
   - `internal/store/tasks_status_vocabulary_lockstep_test.go:25` says "three places in this repo hard-code a partition of that vocabulary" while the list below it already names **five**. Adding a sixth makes the stale count worse. Task 7 fixes it.

7. **The spec's mutation matrix has two blind spots it does not name.** Both are recorded in the mutation battery below rather than papered over: rewriting the first arm as the equivalent deny-list is behaviourally undetectable against today's vocabulary, and deleting `agentHandler.TrailingLogWindow = ...` from `main.go` is covered by no test at all.

8. **The single-clock-domain claim (spec 2.4) is CONFIRMED, and it is the load-bearing one.** Re-derived independently:
   - The only two production writers of a non-NULL `finished_at` that the fence can reach are `internal/worker/handler.go:582-599` (`handleTaskStatus`) and `internal/scheduler/dispatch.go:355-362` (`Dispatcher.failClaimedTask`). Both write `time.Now()` from relay-server's Go clock.
   - `CancelJobTasks` (`query/tasks.sql:340-345`) writes `finished_at = NOW()` from the **database** clock, but sets `worker_id = NULL` in the same UPDATE, so `t.worker_id = $3` can never match it.
   - `FailDependentTasks` (`query/tasks.sql:221-224`) writes `finished_at = NOW()` from the database clock, but only on `status = 'pending'` rows. Every statement that writes `status = 'pending'` (`IncrementTaskRetryCount`, `RequeueTask`, `RequeueTaskByID`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`, `RetryJobTasks`) sets `worker_id = NULL` in the same UPDATE, and `CreateTask`/`CreateTaskWithSource` never set one, so a pending row always has `worker_id IS NULL` and the fence can never match it either.
   - Therefore **no database-clock `finished_at` is reachable through this fence**, and computing the cutoff in Go is sound. If a future change makes one reachable, this design needs revisiting.

---

## Critical files

Read these before starting. They are the entire blast radius.

- `internal/store/query/tasks.sql:170-206` - `AppendTaskLog`, the SQL source of truth. The only behavior change lives here. Lines 196-206 are the statement body; 170-195 the comment block.
- `internal/store/query/tasks.sql:12-93` - `UpdateTaskStatus`. Read only. Lines 41-60 are the canonical statement of the allow-list rule and of why a terminal transition must not bump the epoch.
- `internal/store/tasks.sql.go:14-90` - GENERATED by sqlc. **Never hand-edit.** Regenerated in Task 3.
- `internal/worker/handler.go:57-85` - the `Handler` struct plus `NewHandler`/`NewHandlerWithGrace`. The new field goes beside `Metrics` and `AllowAutoEnroll`.
- `internal/worker/handler.go:713-772` - `handleTaskLog`, the **only** production caller of `AppendTaskLog`. The `AppendTaskLogParams` literal is at :737-743; the `pgx.ErrNoRows` drop at :744-772; the publish strictly after, at :774 onward.
- `internal/store/store_test.go:256-378` - `TestAppendTaskLog_EpochGuarded`, 7 call sites.
- `internal/store/store_test.go:380-449` - `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`, the pinned flush test. **Its `AppendTaskLog` call at :439-442 is a KEYED struct literal**, so a new `AppendTaskLogParams` field defaults to the zero `pgtype.Timestamptz`, which binds SQL NULL, which the new predicate rejects. That is why it needs exactly one added line - verified, not assumed.
- `internal/store/retry_job_tasks_integration_test.go:330-334` - the ninth call site.
- `internal/store/tasks_status_vocabulary_lockstep_test.go:21-87` - the lockstep guard. Five sites listed at :35-62; this change adds a sixth with **inverted** guidance.
- `internal/worker/handler_tasklog_integration_test.go` - `seedClaimedTask` at :33-62 (leaves the task `dispatched` at epoch 1), and the house pattern for an exposure test at :372-438 (non-fatal asserts on both halves, `t.FailNow()`, then a positive control).
- `internal/worker/export_test.go:24-29` - the `HandleTaskLog` shim; `:106-111` - `UUIDStringForTest`.
- `cmd/relay-server/main.go:99-122` - the `RELAY_TELEMETRY_WINDOW` / `RELAY_WORKER_GRACE_WINDOW` env idiom; `:142-151` - where `agentHandler.Metrics` and `agentHandler.AllowAutoEnroll` are set; `:299-304` - `envOrDefault`, where the new parse helper goes.
- `README.md:266-283` - the server env table. The new row goes after `RELAY_WORKER_GRACE_WINDOW` at :276.

---

## Conventions and gotchas (read once, apply everywhere)

1. **SQL is the source of truth.** Edit `internal/store/query/tasks.sql`, then regenerate. **Never hand-edit `internal/store/tasks.sql.go` or `internal/store/models.go`.**

2. **`make generate` CRLF hazard, and the standing lesson attached to it.** `make generate` runs `sqlc generate` **and** `buf generate`. sqlc emits LF; this repo is checked out CRLF, so sqlc rewrites line endings across every file it emits, not just the one whose content changed. Procedure, every time:
   ```
   git status --short
   git diff --ignore-all-space
   ```
   - The **only** file expected to show a real content change is `internal/store/tasks.sql.go`.
   - `internal/store/models.go` must not change at all: no column, no table, no type is added. If `git diff --ignore-all-space internal/store/models.go` is empty but `git status` lists it, that is pure line-ending churn - `git checkout -- internal/store/models.go`.
   - Same for every other `internal/store/*.sql.go` and anything under `internal/proto/`: if `git diff --ignore-all-space <file>` is empty, `git checkout -- <file>`.
   - You may run `sqlc generate` alone instead of `make generate` to skip the `buf generate` churn entirely. **That is the recommended route here**; no `.proto` file changes in this plan.
   - **The revert dance has silently discarded a regeneration in this repo before**, leaving a generated doc comment contradicting its own source. Task 3 Step 4 is a mandatory read-back of the regenerated file's **body and doc comment**. Do not skip it and do not replace it with "the diff looked right".

3. **Almost everything new here is integration-test-only.** All the store and worker tests carry `//go:build integration`. `make test` (`go test ./... -timeout 120s`) compiles and runs **none** of them and will stay green even if they are broken. It is a no-regression gate, never evidence. The one exception is `cmd/relay-server/trailing_log_window_test.go` in Task 6, which is untagged and does run under `make test`. Real verification for everything else is the tagged runs given in each task. Docker Desktop must be running; `-p 1` is mandatory.

4. **`go vet -tags integration ./...` (= `make vet-integration`) is the compile gate for integration-tagged code.** Run it after any signature or struct change; `go build ./...` alone does not compile `//go:build integration` files.

5. **HARD GATE - the pinned flush test may gain one parameter line and NOTHING else.** `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` gets exactly one `MinFinishedAt:` line added to its `AppendTaskLogParams` literal. **No assertion in it may change.** If any assertion needs adjusting to go green, **STOP and report it as a finding** - that means the design broke the trailing flush and the whole approach is wrong. Do not adjust it. Same rule for `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`, which must stay green with **no edit at all**.

6. **Plan-supplied test bodies are SKETCHES, not verified code.** Every Go snippet below was written by a planner reading the tree, not by running it. Treat each one as a guess to be checked: compile it, run it, and for every assertion ask "what mutation makes this fail?" before accepting it. If an assertion cannot be made to fail by any mutation in the battery, it is decoration - say so rather than keeping it for shape. **Never treat "it matches the plan" as verification.** The plan's own self-review already caught one wrong RED prediction in this document (finding 1); assume there is another.

7. **Commit cadence.** Commit at each task boundary except Task 2, which deliberately ends RED and uncommitted. Every commit must leave the tree compiling and green.

8. **No em dashes or en dashes** anywhere - code comments, test messages, commit messages, docs. Use regular hyphens.

---

## Why the Task 1 / Task 2 / Task 3 staging must not be collapsed

Adding a field to `AppendTaskLogParams` makes nine keyed call sites bind SQL NULL at once. If the SQL lands before the exposure test exists, the acceptance evidence for this whole change is destroyed: spec criterion 1 asks specifically for **recorded RED output showing a chunk from a long-finished task being stored and published**, and there is no way to produce it after the fact without reverting the fix.

- **Task 1** adds the constant and the field. Nothing reads them. The full suite stays green.
- **Task 2** adds three handler-layer tests that reference **no new parameter**, so they compile against today's code. One of them is RED **behaviorally**. Record the output verbatim. Do not commit.
- **Task 3** changes the SQL, regenerates, wires the cutoff, and adds the single required parameter line. The same three tests, unmodified, go GREEN.

Do not collapse them. Do not "save time" by editing the SQL early.

---

## Task 1: Add DefaultTrailingLogWindow and the Handler field (declaration only)

Pure declaration. Nothing reads either symbol yet, so behavior is provably unchanged. Go permits an unused exported const and an unused struct field.

**Files:**
- Modify: `internal/worker/handler.go:57-74` (the `Handler` struct and the block above it)

- [ ] **Step 1: Add the constant above the Handler struct**

In `internal/worker/handler.go`, immediately before `// Handler implements relayv1.AgentServiceServer.` (currently line 57), insert:

```go
// DefaultTrailingLogWindow is how long after a task's finished_at its assignee
// may still append log chunks to it. It bounds a window that used to be
// infinite: a terminal transition deliberately keeps worker_id and
// assignment_epoch so a trailing chunk still lands (see UpdateTaskStatus), and
// with no third predicate that stayed true for as long as the row existed.
//
// 15m is roughly 8x the worst case for a legitimately late chunk, which composes
// from three independent agent-side timers: cmd.WaitDelay (5s, internal/agent/
// runner.go), gRPC keepalive ping + timeout (30s + 10s, cmd/relay-server/
// main.go) and the reconnect backoff cap (60s, internal/agent). Under 2 minutes
// in total. Large enough that no realistic agent-side delay truncates real
// output, small enough that "forever" is genuinely closed. Override with
// RELAY_TASKLOG_TRAILING_WINDOW.
const DefaultTrailingLogWindow = 15 * time.Minute
```

- [ ] **Step 2: Add the field to the Handler struct**

In the same file, inside the `Handler` struct, immediately after the `AllowAutoEnroll` field (currently line 73), add:

```go

	// TrailingLogWindow bounds how long after a task's finished_at its assignee
	// may still append log chunks. Non-positive means DefaultTrailingLogWindow,
	// which is what keeps every existing NewHandler/NewHandlerWithGrace call site
	// correct with no edit and lets a test narrow the window to prove the wiring.
	// Set by cmd/relay-server after construction, from
	// RELAY_TASKLOG_TRAILING_WINDOW. Read-only after startup.
	TrailingLogWindow time.Duration
```

- [ ] **Step 3: Verify the tree still builds and the unit gate is green**

Run:
```
go build ./...
go vet -tags integration ./...
go test ./... -timeout 120s
```
Expected: all three succeed. `time` is already imported in `handler.go` (line 14), so no import change is needed.

- [ ] **Step 4: Commit**

```bash
git add internal/worker/handler.go
git commit -m "feat(worker): declare DefaultTrailingLogWindow and Handler.TrailingLogWindow

Declaration only - nothing reads either symbol yet. The fence predicate that
consumes them lands in the next commit, after the exposure test proves the
window is currently unbounded."
```

---

## Task 2: The RED exposure test plus its two controls (no SQL change, DO NOT COMMIT)

Three handler-layer tests. None of them names the new parameter, so all three compile against today's code. One is RED behaviorally; the other two are GREEN today and GREEN after, which is what proves the fixture is sound rather than broken.

**Files:**
- Modify: `internal/worker/handler_tasklog_integration_test.go` (append all three at the end of the file)

- [ ] **Step 1: Write the exposure test**

Append to `internal/worker/handler_tasklog_integration_test.go`:

```go
// A terminal transition deliberately keeps worker_id and assignment_epoch so a
// trailing chunk from the agent that just finished still lands (see
// TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist).
// Without a third predicate that is true FOREVER: anything holding this worker's
// agent token can keep appending rows to a task it finished for as long as the
// row exists, and nothing in the repo prunes task_logs.
//
// This test sends the CORRECT epoch as the GENUINE assignee on purpose. The
// other two fence predicates match, so only a time bound can reject this chunk -
// a wrong-worker or stale-epoch variant would be green today and therefore
// vacuous.
func TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win1@example.com", "w-logs-win1")
	taskIDStr := h.UUIDStringForTest(taskID)

	// Finish it an hour ago, on THIS process's clock. Production writes
	// finished_at from the same clock (handleTaskStatus), and the cutoff is
	// computed from it too, so this test never straddles two clocks. Do NOT
	// rewrite this as NOW() - interval '1 hour': that would compare the container
	// clock against the Go clock and reintroduce exactly the skew the design
	// eliminates.
	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: workerID, AssignmentEpoch: epoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("long after the task finished\n"), Epoch: int64(epoch),
	})

	// HandleTaskLog is synchronous and Publish delivers into the subscriber's
	// buffer before returning, so a non-blocking receive is exact here - no
	// wall-clock window is needed to decide "nothing was published".
	var published []byte
	select {
	case e := <-ch:
		published = e.Data
	default:
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	// Non-fatal asserts so a RED run reports BOTH halves of the exposure - the
	// chunk is stored AND fanned out live - rather than stopping at one. The
	// publish half is not decoration: it is what makes this test able to catch a
	// publish that is not gated on the fence having matched.
	assert.Empty(t, rows, "a chunk for a task that finished outside the window must not be stored")
	assert.Nil(t, published, "a chunk for a task that finished outside the window must not be published")
	if t.Failed() {
		t.FailNow() // the window is open; the rest of the run is moot
	}
}
```

- [ ] **Step 2: Write the trailing-flush control**

Append:

```go
// The regression control for the trailing flush, at the layer the flush actually
// happens. A chunk arriving just after the terminal status is a real and common
// ordering, and it MUST still be stored and published: that flush is the REASON
// the assignment outlives the task. This test goes red the moment the fence's
// two arms are conjoined instead of disjoined.
func TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win2@example.com", "w-logs-win2")
	taskIDStr := h.UUIDStringForTest(taskID)

	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: workerID, AssignmentEpoch: epoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("trailing after done\n"), Epoch: int64(epoch),
	})

	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "trailing after done")
	case <-time.After(5 * time.Second):
		t.Fatal("a trailing chunk from the assignee must still be published after a terminal status")
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a trailing chunk from the assignee must still be stored")
	require.Equal(t, "trailing after done\n", rows[0].Content)
}
```

- [ ] **Step 3: Write the live-task control**

Append:

```go
// The positive control on a LIVE task, and the test that guards the trap this
// predicate creates. A running task has finished_at IS NULL, so it can only pass
// on the status arm of the disjunction - the finished_at arm rejects it (NULL >
// anything is NULL). This is therefore the test that goes red if a non-terminal
// status is ever dropped from that allow-list, which would silently drop 100% of
// that state's log output with no error anywhere.
func TestHandleTaskLog_LiveTaskWithNoFinishedAtIsStillStoredAndPublished(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win3@example.com", "w-logs-win3")
	taskIDStr := h.UUIDStringForTest(taskID)

	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "running", WorkerID: workerID, AssignmentEpoch: epoch,
		StartedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err, "fixture: the running transition must land")
	fresh, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.False(t, fresh.FinishedAt.Valid,
		"fixture: a live task must have a NULL finished_at, or this test proves nothing about the status arm")

	ch, cancel := broker.Subscribe(events.Filter{TaskID: taskIDStr})
	defer cancel()

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("from a live task\n"), Epoch: int64(epoch),
	})

	select {
	case e := <-ch:
		require.Contains(t, string(e.Data), "from a live task")
	case <-time.After(5 * time.Second):
		t.Fatal("a chunk from a live task must be published")
	}
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a chunk from a live task must be stored")
	require.Equal(t, "from a live task\n", rows[0].Content)
}
```

- [ ] **Step 4: Run the three tests and capture the RED output verbatim**

Run:
```
go test -tags integration -p 1 ./internal/worker/... -run 'TestHandleTaskLog_(RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow|TrailingChunkJustAfterATerminalStatusIsStillStored|LiveTaskWithNoFinishedAtIsStillStoredAndPublished)' -v -timeout 300s
```

Expected: **exactly one FAIL**, and it is `TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow`. Both non-fatal assertions in it fire, in this order:

1. `assert.Empty(t, rows, ...)` fails. Message: `a chunk for a task that finished outside the window must not be stored`. Value: `rows` has **len 1**, holding the `task_logs` row with `Content: "long after the task finished\n"` - testify renders it as `Should be empty, but was [{...}]`.
2. `assert.Nil(t, published, ...)` fails. Message: `a chunk for a task that finished outside the window must not be published`. Value: `published` is a non-nil `[]byte` containing the marshalled `taskLogEvent` JSON, including `"content":"long after the task finished\n"` - testify renders it as `expected nil, but got: []byte{...}`.

Then `t.FailNow()`.

The other two tests must PASS. **If either control fails at this point, the fixture is wrong and the RED is not evidence** - stop and report, do not proceed.

Paste the full `-v` output into your task report. This is spec acceptance criterion 1 and it cannot be reproduced after Task 3.

- [ ] **Step 5: Do NOT commit**

Leave the tree dirty. These tests are committed as part of Task 3, where they are green.

---

## Task 3: The fence predicate, the regeneration, and the cutoff at the call site

This is the behavior change. It is one task because it is the smallest unit that leaves the tree green: adding the parameter without wiring the handler would leave the flush broken, and wiring the handler without the parameter would not compile.

**Files:**
- Modify: `internal/store/query/tasks.sql:170-206` (comment block and fence CTE)
- Regenerate: `internal/store/tasks.sql.go` (via sqlc; never by hand)
- Modify: `internal/worker/handler.go:713-743` (doc comment, window resolution, the params literal)
- Modify: `internal/store/store_test.go:439-442` (one line, the pinned flush test)

- [ ] **Step 1: Add the predicate and its comment to the SQL**

In `internal/store/query/tasks.sql`, the comment block currently ends at line 195 with:

```sql
-- The tasks alias and the qualified column references are load-bearing: without
-- them sqlc's analyzer cannot resolve "id" across the two CTEs and fails with
-- 'column reference "id" is ambiguous'. Only job_id is selected because that is
-- all the publish needs; the fence's job is to yield exactly one row, or none.
```

Immediately after that line and before `WITH fence AS (`, insert:

```sql
-- THE TRAILING WINDOW - the third predicate, and the one with a trap in it.
-- A terminal transition deliberately keeps worker_id and assignment_epoch (see
-- UpdateTaskStatus), so without a bound the two predicates above keep matching
-- for the agent that finished the task FOREVER: anything holding worker W's
-- agent token can append rows to a task W finished at epoch N for as long as the
-- row exists, and nothing in this repo prunes task_logs. This predicate closes
-- that window without closing the flush.
--   * It is a DISJUNCTION and must NEVER become a conjunction. A live task
--     always accepts logs (first arm); a finished task accepts them only while
--     its finished_at is inside the caller's window (second arm). Conjoining the
--     arms - a bare `AND status IN (...)` - would reject the trailing chunk that
--     arrives just after the terminal status, which is a real and common
--     ordering, and would silently truncate the tail of every task's output in
--     production. THE ASSIGNMENT OUTLIVING THE TASK IS LOAD-BEARING, NOT AN
--     OVERSIGHT. Pinned by
--     TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist
--     and by TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored.
--   * The first arm is an ALLOW-LIST for the same reason UpdateTaskStatus's is,
--     but READ THE GUIDANCE BACKWARDS AT THIS SITE. Everywhere else a new status
--     must usually stay OUT, and the omission fails closed harmlessly. Here the
--     omission is catastrophic and silent: a new NON-TERMINAL status left out of
--     this arm drops 100% of that state's log output, because a non-terminal row
--     has finished_at IS NULL and therefore fails the second arm too, and the
--     drop produces no error and no log line anywhere. That is not hypothetical:
--     TASK_STATUS_PREPARING already exists in proto/relayv1/relay.proto and the
--     agent already streams prepare progress as LOG_STREAM_PREPARE chunks
--     (internal/agent/runner.go, makePrepareProgressFn) while the row is still
--     `dispatched`. The day `preparing` becomes a persisted status and is not
--     added here, every workspace-sync log line in the system disappears. Its
--     twin TASK_STATUS_PREPARE_FAILED needs the OPPOSITE treatment: a new
--     TERMINAL status stays OUT and is then bounded by finished_at like
--     done/failed/timed_out. TestTasksStatusVocabularyIsExactly names this site.
--   * min_finished_at is an ABSOLUTE cutoff computed in Go as
--     time.Now().Add(-window), never NOW() - interval. Every finished_at
--     reachable through this fence was written by relay-server's own Go clock
--     (handleTaskStatus and Dispatcher.failClaimedTask). CancelJobTasks and
--     FailDependentTasks do write finished_at from the database clock, but the
--     first nulls worker_id and the second only touches `pending` rows, which
--     always have a NULL worker_id - so neither is reachable through the
--     worker_id predicate above. Keeping both sides of the comparison on one
--     clock means app/database skew cannot move the window at all.
--   * The pair fails CLOSED on a missing value, which is why it is spelled this
--     way and not the other. A terminal row with a NULL finished_at (a row from
--     an older schema, or a future terminal writer that forgets the timestamp)
--     fails both arms, because `NULL > cutoff` is NULL, not true. A caller that
--     omits the cutoff binds SQL NULL and every terminal append is rejected. DO
--     NOT rewrite the second arm as
--     `finished_at IS NULL OR finished_at > cutoff`: that spelling admits every
--     terminal row that has no timestamp, which is the fail-OPEN direction. Same
--     rule as the plain `=` on worker_id above.
--   * No EvalPlanQual reasoning applies here, and do not import it from
--     RetryJobTasks. That lesson is about an UPDATE whose row-level qual is
--     re-checked after it unblocks. This statement performs no UPDATE and takes
--     no row lock; its fence is a plain non-locking SELECT.
```

Then change the fence CTE. Lines 196-200 currently read:

```sql
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id)
      AND t.assignment_epoch = sqlc.arg(assignment_epoch)
      AND t.worker_id = sqlc.arg(worker_id)
), ins AS (
```

Replace with:

```sql
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id)
      AND t.assignment_epoch = sqlc.arg(assignment_epoch)
      AND t.worker_id = sqlc.arg(worker_id)
      AND (t.status IN ('pending', 'dispatched', 'running')
           OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
), ins AS (
```

Leave the `ins` CTE and the final `SELECT` exactly as they are.

- [ ] **Step 2: Regenerate the store layer**

Run:
```
sqlc generate
```
(`make generate` also works but additionally runs `buf generate`, whose output you would then have to revert; no `.proto` changes here.)

- [ ] **Step 3: Do the CRLF revert dance**

Run:
```
git status --short
git diff --ignore-all-space
```

Revert every generated file whose `git diff --ignore-all-space` output is empty:
```
git checkout -- internal/store/models.go
git checkout -- <each other internal/store/*.sql.go with no real change>
```

Only `internal/store/tasks.sql.go` may remain modified. Confirm with:
```
git status --short internal/store/
```
Expected: exactly one line, `M internal/store/tasks.sql.go`.

- [ ] **Step 4: Read the regenerated file back - mandatory, not optional**

The CRLF revert has silently discarded a regeneration in this repo before, leaving a generated doc comment contradicting its own source. Open `internal/store/tasks.sql.go` and confirm **all five** of these by reading, not by inference:

1. The `appendTaskLog` const's fence CTE contains the line
   `AND (t.status IN ('pending', 'dispatched', 'running')` followed by
   `OR t.finished_at > $4::timestamptz)`.
2. The `INSERT` line inside the const now reads `SELECT $1, $5, $6 FROM fence` - stream and content shifted from `$4,$5` to `$5,$6` because `min_finished_at` took `$4`.
3. `AppendTaskLogParams` has the field `MinFinishedAt pgtype.Timestamptz` (**not** `interface{}`, **not** `*time.Time`), positioned after `WorkerID` and before `Stream`.
4. The `QueryRow` argument list in `func (q *Queries) AppendTaskLog` has **six** arguments, with `arg.MinFinishedAt` fourth.
5. The Go doc comment above `func (q *Queries) AppendTaskLog` contains the new prose - grep for the literal strings `THE TRAILING WINDOW`, `READ THE GUIDANCE BACKWARDS AT THIS SITE` and `the fail-OPEN direction` in `internal/store/tasks.sql.go`. If any is absent, the regeneration was reverted; re-run Steps 2-3.

If item 3 comes back as anything other than `pgtype.Timestamptz`, **stop and report** - the `::timestamptz` cast did not take. (Precedent that it does: `sqlc.arg(cursor_ts)::timestamptz` in `query/jobs.sql:30` emits `CursorTs pgtype.Timestamptz` at `internal/store/jobs.sql.go:359`.)

- [ ] **Step 5: Resolve the window and pass the cutoff in handleTaskLog**

In `internal/worker/handler.go`, the doc comment block at lines 713-725 ends with:

```go
// This runs synchronously on the Connect recv goroutine, which also carries that
// worker's status, inventory and telemetry messages, so everything below is
// deliberately cheap: exactly one DB round trip (the insert itself returns the
// job id and seq), one map lookup when nobody is watching, and a non-blocking
// Publish. Do not add a query, a goroutine, or a queue here.
```

Insert this paragraph immediately before that one (after the `workerID is the connection's own authenticated worker` paragraph):

```go
// The trailing window is resolved per call and passed to the fence as an
// absolute cutoff. It costs one time.Now() and one bound parameter - still one
// round trip, still no allocation on the quiet path. See AppendTaskLog's comment
// for why the cutoff is computed here rather than as NOW() - interval in SQL.
//
```

Then replace the params literal at lines 737-743:

```go
	row, err := h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:          taskID,
		Stream:          stream,
		Content:         string(chunk.Content),
		AssignmentEpoch: int32(chunk.Epoch),
		WorkerID:        workerID,
	})
```

with:

```go
	// Resolved per call, never cached: a test moves the field between two calls
	// on the same handler to prove this call site actually reads it. Non-positive
	// means the default, which is what keeps every existing NewHandler call site
	// correct with no edit.
	window := h.TrailingLogWindow
	if window <= 0 {
		window = DefaultTrailingLogWindow
	}

	row, err := h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:          taskID,
		Stream:          stream,
		Content:         string(chunk.Content),
		AssignmentEpoch: int32(chunk.Epoch),
		WorkerID:        workerID,
		MinFinishedAt:   pgtype.Timestamptz{Time: time.Now().Add(-window), Valid: true},
	})
```

**Do not touch `AssignmentEpoch: int32(chunk.Epoch)`.** That truncation is `bug-2026-08-12-tasklog-epoch-int32-truncation`, a separate filed item. Flag it in your report; do not fix it here.

Do not change anything below the literal: the `pgx.ErrNoRows` branch and the publish stay exactly as they are. The rejection path needs **zero Go changes** - a closed window joins the existing silent-drop case and returns before the publish, which is what keeps the side effect gated on the fence having matched.

- [ ] **Step 6: Add the one required parameter line to the pinned flush test**

In `internal/store/store_test.go`, lines 439-442 currently read:

```go
	_, err = q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "trailing after done\n",
		AssignmentEpoch: done.AssignmentEpoch, WorkerID: done.WorkerID,
	})
```

Replace with:

```go
	_, err = q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "trailing after done\n",
		AssignmentEpoch: done.AssignmentEpoch, WorkerID: done.WorkerID,
		// The trailing-window arm. A caller that omits this binds SQL NULL and
		// every terminal append is rejected, which is the fence failing closed.
		MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
```

**One line plus its comment. Change nothing else in this test.** If any assertion in it needs adjusting to go green, stop and report it as a finding (conventions rule 5).

- [ ] **Step 7: Compile gates**

Run:
```
go build ./...
go vet -tags integration ./...
go test ./... -timeout 120s
```
Expected: all succeed.

- [ ] **Step 8: Run the three Task 2 tests - the RED must now be GREEN**

Run:
```
go test -tags integration -p 1 ./internal/worker/... -run 'TestHandleTaskLog_(RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow|TrailingChunkJustAfterATerminalStatusIsStillStored|LiveTaskWithNoFinishedAtIsStillStoredAndPublished)' -v -timeout 300s
```
Expected: **3 PASS, 0 FAIL.** The test that was RED in Task 2 Step 4 is now green with **no edit to the test**.

- [ ] **Step 9: Run the whole worker and store integration suites**

Run:
```
go test -tags integration -p 1 ./internal/worker/... -timeout 900s
go test -tags integration -p 1 ./internal/store/... -timeout 900s
```
Expected: all PASS. In particular:
- `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` - green with the one added line.
- `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` - green with **no edit**. Its NUL-bearing chunks fail during bind-parameter decode, before the fence is evaluated, so the new predicate is unreachable from it. If it needed an edit, the understanding in spec section 5 is wrong - report it.
- `TestAppendTaskLog_EpochGuarded` - green with **no edit**. Its 7 call sites all target `pending`/`dispatched` tasks, which pass on the status arm regardless of the NULL cutoff. (Task 4 adds the cutoff to them anyway, for hygiene.)
- `TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected` - green with no edit; its task is `pending` after the retry.
- Both tests in `handler_tasklog_e2e_integration_test.go` - green with no edit; both operate on live tasks.

- [ ] **Step 10: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go internal/worker/handler.go internal/worker/handler_tasklog_integration_test.go internal/store/store_test.go
git commit -m "fix(store): bound how long a finished task accepts log appends

AppendTaskLog's fence gains a third predicate, spelled as a disjunction: a
live task always accepts logs (status allow-list), a finished task accepts
them only while finished_at is inside the caller's window. Disjunctive so the
trailing flush that arrives just after a terminal status still lands; an
allow-list rather than the equivalent deny-list so a status added later fails
closed; an absolute Go-computed cutoff rather than NOW() - interval so the
comparison stays in one clock domain.

The rejection path is unchanged: a closed window joins the existing
pgx.ErrNoRows drop, silently and before the publish."
```

---

## Task 4: The remaining store call sites, the stale numbering comment, and the predicate specification test

Nine `AppendTaskLog` call sites exist in `internal/store`. Task 3 handled one. This task handles the other eight and adds the table-driven specification of the new predicate.

**Exhaustive call-site inventory** (verified at `ee88de0`; the spec miscounts these - see finding 2):

| # | File:line | Task status at the call | Needs the line to stay green? |
|---|---|---|---|
| 1 | `internal/store/store_test.go:283` | `dispatched` | No (hygiene) |
| 2 | `internal/store/store_test.go:293` | `dispatched` | No (hygiene) |
| 3 | `internal/store/store_test.go:303` | `dispatched` | No (hygiene) |
| 4 | `internal/store/store_test.go:330` | `dispatched` | No (hygiene) |
| 5 | `internal/store/store_test.go:339` | `dispatched` | No (hygiene) |
| 6 | `internal/store/store_test.go:354` | `pending` (never claimed) | No (hygiene) |
| 7 | `internal/store/store_test.go:365` | `pending` (never claimed) | No (hygiene) |
| 8 | `internal/store/store_test.go:439` | `done` | **Yes** - done in Task 3 |
| 9 | `internal/store/retry_job_tasks_integration_test.go:330` | `pending` (reopened) | No (hygiene) |

A missed call site is not a subtle bug - it binds SQL NULL and rejects **every** terminal append from that path - but here all eight remaining sites target non-terminal tasks, so none of them will fail loudly. Set the field anyway, so no call site in the tree models a caller that omits it. The production caller is `internal/worker/handler.go:737`, handled in Task 3, and it is the **only** one.

**Files:**
- Modify: `internal/store/store_test.go:283-369` (7 call sites + one comment)
- Modify: `internal/store/retry_job_tasks_integration_test.go:330-334` (1 call site)
- Modify: `internal/store/store_test.go` (append the new test)

- [ ] **Step 1: Add the cutoff to the seven call sites in TestAppendTaskLog_EpochGuarded**

In `internal/store/store_test.go`, add `MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},` as an extra keyed field to each of the seven `AppendTaskLogParams` literals at lines 283, 293, 303, 330, 339, 354 and 365. For example, the first becomes:

```go
	first, err := q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "hello\n", AssignmentEpoch: 1, WorkerID: w.ID,
		MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
```

**Change no assertion in this test.** Every case here passes or fails on the epoch and worker predicates; the cutoff is inert for all seven because every task involved is `pending` or `dispatched`.

- [ ] **Step 2: Fix the now-stale parameter-numbering comment**

The comment at `internal/store/store_test.go:314-318` currently reads:

```go
	// The fence's parameter numbering shifted when the worker_id predicate was
	// added ($3,$4 -> $4,$5 for stream and content). Read stream back so a future
	// renumbering that swapped these two cannot pass unnoticed.
```

The numbering moved again in Task 3. Replace with:

```go
	// The fence's parameter numbering has shifted twice as predicates were added:
	// stream and content were $3,$4, then $4,$5 (worker_id), and are now $5,$6
	// (min_finished_at). Read stream back so a future renumbering that swapped
	// these two cannot pass unnoticed.
```

- [ ] **Step 3: Add the cutoff to the retry-job-tasks call site**

In `internal/store/retry_job_tasks_integration_test.go`, lines 330-334 currently read:

```go
	_, err = f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: task.ID, AssignmentEpoch: oldEpoch, WorkerID: oldWorker,
		Stream: "stdout", Content: "zombie output",
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a trailing log chunk from the dead generation must be dropped")
```

Replace the literal with:

```go
	_, err = f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: task.ID, AssignmentEpoch: oldEpoch, WorkerID: oldWorker,
		Stream: "stdout", Content: "zombie output",
		// A live cutoff, so this assertion still fails on the epoch and worker
		// predicates rather than passing for the new predicate's reason.
		MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a trailing log chunk from the dead generation must be dropped")
```

Check the imports at the top of that file: it needs `time` and `pgtype`. Add whichever is missing.

- [ ] **Step 4: Write the predicate specification test**

Append to `internal/store/store_test.go`:

```go
// TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow specifies the
// third fence predicate at the statement, with an explicit MinFinishedAt in every
// case so nothing sleeps and nothing depends on wall-clock timing.
//
// The last two cases are the ones that discriminate this spelling from the naive
// `finished_at IS NULL OR finished_at > cutoff`, and they discriminate it against
// DIFFERENT mutations - see the plan's mutation battery. Do not drop either for
// brevity: together they are the whole reason the predicate uses a status
// allow-list as the live-task arm.
func TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Tess", "tess-window@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-window", Hostname: "w-window", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	// Every timestamp in this test comes from THIS process's clock, exactly as
	// production does: the fence compares a Go-written finished_at against a
	// Go-computed cutoff. Never introduce NOW() here.
	now := time.Now()
	ts := func(d time.Duration) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: now.Add(d), Valid: true}
	}
	finish := func(t *testing.T, taskID pgtype.UUID, epoch int32, at pgtype.Timestamptz) {
		t.Helper()
		_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
			ID: taskID, Status: "done", WorkerID: w.ID, AssignmentEpoch: epoch, FinishedAt: at,
		})
		require.NoError(t, err, "fixture: the terminal transition must land")
	}

	cases := []struct {
		name    string
		prepare func(t *testing.T, taskID pgtype.UUID, epoch int32)
		cutoff  pgtype.Timestamptz
		wantErr error // nil means the chunk must be stored
	}{
		{
			name:    "dispatched task passes on the status arm",
			prepare: func(*testing.T, pgtype.UUID, int32) {},
			cutoff:  ts(0),
		},
		{
			name: "running task passes on the status arm",
			prepare: func(t *testing.T, taskID pgtype.UUID, epoch int32) {
				_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
					ID: taskID, Status: "running", WorkerID: w.ID, AssignmentEpoch: epoch,
					StartedAt: ts(-time.Minute),
				})
				require.NoError(t, err, "fixture: the running transition must land")
			},
			cutoff: ts(0),
		},
		{
			name:    "live task with NO cutoff at all - the status arm must not depend on one",
			prepare: func(*testing.T, pgtype.UUID, int32) {},
			cutoff:  pgtype.Timestamptz{},
		},
		{
			name: "finished one minute ago, fifteen minute window - the trailing flush",
			prepare: func(t *testing.T, taskID pgtype.UUID, epoch int32) {
				finish(t, taskID, epoch, ts(-time.Minute))
			},
			cutoff: ts(-15 * time.Minute),
		},
		{
			name: "finished one hour ago, fifteen minute window - the window is closed",
			prepare: func(t *testing.T, taskID pgtype.UUID, epoch int32) {
				finish(t, taskID, epoch, ts(-time.Hour))
			},
			cutoff:  ts(-15 * time.Minute),
			wantErr: pgx.ErrNoRows,
		},
		{
			name: "terminal with a NULL finished_at must fail CLOSED",
			prepare: func(t *testing.T, taskID pgtype.UUID, epoch int32) {
				finish(t, taskID, epoch, ts(-time.Minute))
				// Raw SQL: no statement in the repo produces this state today, but
				// an older row or a future terminal writer that forgets the stamp
				// would. `NULL > cutoff` is NULL, not true, so both arms reject.
				// This is the case that rejects the naive `finished_at IS NULL OR
				// ...` spelling, which would ADMIT this row.
				_, err := pool.Exec(ctx, `UPDATE tasks SET finished_at = NULL WHERE id = $1`, taskID)
				require.NoError(t, err)
			},
			cutoff:  ts(-15 * time.Minute),
			wantErr: pgx.ErrNoRows,
		},
		{
			name: "terminal and the caller omits the cutoff must fail CLOSED",
			prepare: func(t *testing.T, taskID pgtype.UUID, epoch int32) {
				finish(t, taskID, epoch, ts(-time.Minute))
			},
			// A zero pgtype.Timestamptz binds SQL NULL, and `finished_at > NULL`
			// is NULL, not true. This is the case that rejects any future
			// COALESCE-the-cutoff "convenience" fix.
			cutoff:  pgtype.Timestamptz{},
			wantErr: pgx.ErrNoRows,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, err := q.CreateTask(ctx, store.CreateTaskParams{
				JobID: job.ID, Name: tc.name, Commands: []byte(`[["true"]]`),
				Env: []byte(`{}`), Requires: []byte(`{}`),
			})
			require.NoError(t, err)
			claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
				ID: task.ID, WorkerID: w.ID,
			})
			require.NoError(t, err)
			tc.prepare(t, task.ID, claimed.AssignmentEpoch)

			_, err = q.AppendTaskLog(ctx, store.AppendTaskLogParams{
				TaskID: task.ID, Stream: "stdout", Content: "chunk\n",
				AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w.ID,
				MinFinishedAt: tc.cutoff,
			})
			logs, lerr := q.GetTaskLogs(ctx, task.ID)
			require.NoError(t, lerr)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Empty(t, logs, "a rejected append must insert nothing")
				return
			}
			require.NoError(t, err)
			require.Len(t, logs, 1)
			require.Equal(t, "chunk\n", logs[0].Content)
		})
	}
}
```

Note: `newTestPool` lives at `internal/store/testhelper_test.go:20` and is in the same `store_test` package. `store.New(pool)` gives Queries over the **same** container, which the raw `pool.Exec` in one case needs.

- [ ] **Step 5: Run it**

Run:
```
go test -tags integration -p 1 ./internal/store/... -run 'TestAppendTaskLog' -v -timeout 600s
```
Expected: `TestAppendTaskLog_EpochGuarded` PASS and all seven subtests of `TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow` PASS.

- [ ] **Step 6: Prove the two fail-closed subtests discriminate - TWO different mutations**

Do not accept these on the strength of "they match the plan". They guard different holes and **no single mutation reddens both**. The planner's first draft of this plan predicted that one mutation would redden both; that was wrong, and it is exactly the failure mode conventions rule 6 warns about. Run both.

**Mutation A - the naive spelling the backlog item proposed.** Rewrite the predicate in `internal/store/query/tasks.sql` as:

```sql
      AND (t.status IN ('pending', 'dispatched', 'running')
           OR t.finished_at IS NULL
           OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
```

Run `sqlc generate`, do the CRLF dance, re-run the test. Expected: **exactly ONE subtest fails** -
- `terminal_with_a_NULL_finished_at_must_fail_CLOSED`: `require.ErrorIs(t, err, tc.wantErr)` fails with `Target error should be in err chain: expected pgx.ErrNoRows, got <nil>`; the follow-on `require.Empty(t, logs, "a rejected append must insert nothing")` would report len **1**.

`terminal_and_the_caller_omits_the_cutoff_must_fail_CLOSED` **stays GREEN** under Mutation A, and that is correct, not a defect: its row has a non-NULL `finished_at`, so `IS NULL` is false and `finished_at > NULL` is NULL, and the row is still rejected. Revert Mutation A (`git checkout -- internal/store/query/tasks.sql`, `sqlc generate`, CRLF dance) and confirm green before continuing.

**Mutation B - the "convenience" cutoff default.** Rewrite the second arm as:

```sql
           OR t.finished_at > COALESCE(sqlc.arg(min_finished_at)::timestamptz, '-infinity'::timestamptz))
```

Run `sqlc generate`, do the CRLF dance, re-run the test. Expected: **exactly ONE subtest fails** -
- `terminal_and_the_caller_omits_the_cutoff_must_fail_CLOSED`: `require.ErrorIs` fails with `expected pgx.ErrNoRows, got <nil>`, and `require.Empty(logs)` reports len **1**.

Revert Mutation B and confirm green.

If either mutation reddens a different set of subtests than predicted, **report it** - that is a finding about this plan's model of the predicate, and it must be resolved before the change is trusted.

- [ ] **Step 7: Full store suite and commit**

Run:
```
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/store/... -timeout 900s
```
Expected: all PASS.

```bash
git add internal/store/store_test.go internal/store/retry_job_tasks_integration_test.go
git commit -m "test(store): specify the trailing-window predicate at the statement

Seven cases over the fence with an explicit cutoff, so nothing sleeps. The
last two are the fail-closed cases, and they guard different holes: a
terminal row with no timestamp rejects the naive finished_at IS NULL
spelling, and a caller that omits the cutoff rejects any future COALESCE
default. No single mutation reddens both.

Also threads the cutoff through the remaining eight AppendTaskLog call sites
so none of them models a caller that omits it, and corrects the now-twice-
stale parameter-numbering note in TestAppendTaskLog_EpochGuarded."
```

---

## Task 5: Prove the knob is wired to the predicate

Two handler-layer tests. The first is the only test in the suite that can tell "the window is read from `Handler.TrailingLogWindow` at the call site" from "the window is a constant". The second pins the non-positive-means-default rule that keeps every existing construction site correct.

Neither sleeps. Spec section 8.2 proposed a 50 ms window plus a 150 ms sleep; that is flaky across two testcontainer round trips and is a weaker proof than running the **same chunk through the same handler twice with only the field changed**.

**Files:**
- Modify: `internal/worker/handler_tasklog_integration_test.go` (append both)

- [ ] **Step 1: Write the wiring test**

Append:

```go
// The only test that proves TrailingLogWindow is wired to the predicate rather
// than merely existing. Asserting the exported constant would prove nothing
// about the code consuming it; the mutation this catches is a call site that
// hard-codes DefaultTrailingLogWindow and ignores the field.
//
// No sleep. The two legs run the SAME chunk through the SAME handler against the
// SAME task, and differ in NOTHING but the field's value. If the window were read
// from anywhere else, both legs would agree.
func TestHandleTaskLog_TheWindowIsReadFromTheHandlerFieldAtEveryCall(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win4@example.com", "w-logs-win4")
	taskIDStr := h.UUIDStringForTest(taskID)

	// Finished two seconds ago on this process's clock - the same clock the
	// cutoff is computed from.
	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: workerID, AssignmentEpoch: epoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Second), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	// Leg A: a one-second window. Two seconds have passed, so the window is shut.
	// A call site that ignored the field and used DefaultTrailingLogWindow (15m)
	// would store this chunk.
	h.TrailingLogWindow = 1 * time.Second
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("outside\n"), Epoch: int64(epoch),
	})
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Empty(t, rows, "with a 1s window, a task that finished 2s ago must reject the chunk")

	// Leg B: same handler, same task, same epoch, same assignee - only the field
	// moved.
	h.TrailingLogWindow = 1 * time.Hour
	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("inside\n"), Epoch: int64(epoch),
	})
	rows, err = q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "with a 1h window the very same chunk must land")
	require.Equal(t, "inside\n", rows[0].Content)
}
```

- [ ] **Step 2: Write the non-positive-default test**

Append:

```go
// Non-positive means the default, NOT a zero-length window. That rule is what
// keeps every existing NewHandler and NewHandlerWithGrace call site correct with
// no edit - and every one of them leaves this field at its zero value. Without
// it, a zero field would set the cutoff to time.Now() and every terminal append
// in the system, including the trailing flush, would be rejected.
func TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})
	require.Zero(t, h.TrailingLogWindow,
		"fixture: this test is about the field's ZERO value, so it must not be set")

	_, taskID, workerID, epoch := seedClaimedTask(t, ctx, q, "logs-win5@example.com", "w-logs-win5")
	taskIDStr := h.UUIDStringForTest(taskID)

	// A minute ago: comfortably inside the 15m default, comfortably outside a
	// zero-length window.
	_, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "done", WorkerID: workerID, AssignmentEpoch: epoch,
		FinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})
	require.NoError(t, err, "fixture: the terminal transition must land")

	h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
		TaskId: taskIDStr, Content: []byte("inside the default\n"), Epoch: int64(epoch),
	})
	rows, err := q.GetTaskLogs(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "an unset window must behave as DefaultTrailingLogWindow, not as zero")
	require.Equal(t, "inside the default\n", rows[0].Content)
}
```

- [ ] **Step 3: Run both**

Run:
```
go test -tags integration -p 1 ./internal/worker/... -run 'TestHandleTaskLog_(TheWindowIsReadFromTheHandlerFieldAtEveryCall|AZeroWindowMeansTheDefaultNotAZeroLengthWindow)' -v -timeout 300s
```
Expected: 2 PASS.

- [ ] **Step 4: Prove each one discriminates**

Do not skip this. Two temporary mutations in `internal/worker/handler.go`, each reverted before the next:

1. Replace `MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-window), Valid: true},` with `MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-DefaultTrailingLogWindow), Valid: true},` (ignore the field). Re-run. Expected: `TestHandleTaskLog_TheWindowIsReadFromTheHandlerFieldAtEveryCall` FAILS at `require.Empty(t, rows, "with a 1s window, a task that finished 2s ago must reject the chunk")` with `rows` of **len 1** holding `"outside\n"`. `TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow` still PASSES. Revert.
2. Change `if window <= 0` to `if window < 0`. Re-run. Expected: `TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow` FAILS at `require.Len(t, rows, 1, ...)` with `rows` of **len 0**. Revert.

If either mutation leaves both tests green, the test is not discriminating - report it rather than keeping it.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_tasklog_integration_test.go
git commit -m "test(worker): prove TrailingLogWindow reaches the fence predicate

Two legs, same handler, same chunk, same task - only the field moves. An
assertion on the exported constant would prove nothing about the code that
consumes it. Plus the non-positive-means-default rule, which is what keeps
every existing NewHandler call site correct with no edit."
```

---

## Task 6: RELAY_TASKLOG_TRAILING_WINDOW

**Files:**
- Modify: `cmd/relay-server/main.go:142-151` (wiring) and `:299-304` (the helper, beside `envOrDefault`)
- Create: `cmd/relay-server/trailing_log_window_test.go` (untagged - this one runs under `make test`)

- [ ] **Step 1: Add the parse helper**

In `cmd/relay-server/main.go`, immediately after `envOrDefault` (which ends at line 304), add:

```go

// parseTrailingLogWindow resolves RELAY_TASKLOG_TRAILING_WINDOW's raw value into
// the window handed to worker.Handler. An unset variable is the ordinary case
// and resolves to the default silently. A set-but-unusable value - unparseable,
// zero or negative - ALSO resolves to the default, but reports ok=false so the
// caller can log exactly one startup warning. That warning is the point: this is
// a security-relevant knob, and a silently-ignored typo would leave an operator
// believing they had tightened something they had not.
//
// Deliberately not a log.Fatalf, unlike RELAY_ALLOW_AUTO_ENROLL: an unparseable
// duration must not stop a server booting when a safe default exists. Follows
// the `d > 0 or keep the default` idiom of RELAY_TELEMETRY_WINDOW above, plus
// the warning.
func parseTrailingLogWindow(raw string) (time.Duration, bool) {
	if raw == "" {
		return worker.DefaultTrailingLogWindow, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return worker.DefaultTrailingLogWindow, false
	}
	return d, true
}
```

- [ ] **Step 2: Wire it in main**

In `cmd/relay-server/main.go`, line 143 currently reads `agentHandler.Metrics = metricsStore`. Immediately after it, insert:

```go

	trailingLogWindow, trailingLogWindowOK := parseTrailingLogWindow(os.Getenv("RELAY_TASKLOG_TRAILING_WINDOW"))
	if !trailingLogWindowOK {
		log.Printf("WARNING: RELAY_TASKLOG_TRAILING_WINDOW=%q is not a positive Go duration; using %s",
			os.Getenv("RELAY_TASKLOG_TRAILING_WINDOW"), trailingLogWindow)
	}
	agentHandler.TrailingLogWindow = trailingLogWindow
```

Keep the assignment adjacent to `agentHandler.Metrics`. **No test covers this assignment** (nothing constructs `main()`), so its only protection is that it sits with its two siblings - see the mutation battery's known blind spots.

`time`, `log`, `os` and `relay/internal/worker` are all already imported in this file.

- [ ] **Step 3: Write the unit test**

Create `cmd/relay-server/trailing_log_window_test.go`:

```go
package main

import (
	"testing"
	"time"

	"relay/internal/worker"

	"github.com/stretchr/testify/require"
)

// No build tag: this is a pure function and it runs under `make test`, unlike
// every other test file in this package.
func TestParseTrailingLogWindow(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   time.Duration
		wantOK bool
	}{
		{"unset keeps the default and does NOT warn", "", worker.DefaultTrailingLogWindow, true},
		{"a valid duration is used", "45m", 45 * time.Minute, true},
		{"the documented escape hatch is honoured", "8760h", 8760 * time.Hour, true},
		{"unparseable keeps the default and warns", "fifteen minutes", worker.DefaultTrailingLogWindow, false},
		{"zero keeps the default and warns", "0s", worker.DefaultTrailingLogWindow, false},
		{"negative keeps the default and warns", "-5m", worker.DefaultTrailingLogWindow, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseTrailingLogWindow(tc.raw)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.wantOK, ok,
				"ok drives whether main logs a startup warning; warning on the ordinary unset case is as wrong as staying silent on a typo")
		})
	}
}
```

- [ ] **Step 4: Run it**

Run:
```
go test ./cmd/relay-server/... -run TestParseTrailingLogWindow -v -timeout 60s
```
Expected: 6 subtests PASS.

- [ ] **Step 5: Prove it discriminates**

Two temporary mutations in `parseTrailingLogWindow`, each reverted:

1. Change `if err != nil || d <= 0` to `if err != nil`. Re-run. Expected: `zero_keeps_the_default_and_warns` FAILS (`want 15m0s, got 0s`) and `negative_keeps_the_default_and_warns` FAILS (`want 15m0s, got -5m0s`). Revert.
2. Delete the `if raw == "" { return worker.DefaultTrailingLogWindow, true }` early return. Re-run. Expected: `unset_keeps_the_default_and_does_NOT_warn` FAILS on the `ok` assertion (`want true, got false`) - i.e. the server would print a warning on every boot with the variable unset. Revert.

- [ ] **Step 6: Full unit gate and commit**

Run:
```
go build ./...
go test ./... -timeout 120s
```
Expected: PASS.

```bash
git add cmd/relay-server/main.go cmd/relay-server/trailing_log_window_test.go
git commit -m "feat(server): RELAY_TASKLOG_TRAILING_WINDOW

Follows the RELAY_TELEMETRY_WINDOW idiom (positive or keep the default) plus
one startup warning when the variable is set and unusable: a silently ignored
typo on a security-relevant knob would leave an operator believing they had
tightened something they had not. Not a Fatalf - an unparseable duration must
not stop a boot when a safe default exists."
```

---

## Task 7: The vocabulary guard gains a sixth site, with INVERTED guidance

The guard whose guidance is backwards from its five neighbours. A future reader will get this wrong unless the inversion is stated in capitals at the site.

**Files:**
- Modify: `internal/store/tasks_status_vocabulary_lockstep_test.go:21-67` (comment only; no assertion changes)

- [ ] **Step 1: Fix the stale site count**

Line 25-26 currently reads:

```go
// It exists because three places in this repo hard-code a partition of that
// vocabulary, and adding a seventh status silently desynchronizes all three at
// once. A task-level `cancelled` is the concrete near-term candidate:
```

The list below it already names five sites, and this change makes six. Replace with:

```go
// It exists because six statements in this repo hard-code a slice of that
// vocabulary, and adding a seventh status silently desynchronizes all of them at
// once. A task-level `cancelled` is the concrete near-term candidate:
```

- [ ] **Step 2: Add the sixth site to the list, after SelectRetryableTaskIDs**

The list currently ends at line 62 with the `SelectRetryableTaskIDs` bullet. Immediately after it (before the blank comment line at :63), insert:

```go
//   - AppendTaskLog (query/tasks.sql) - `status IN ('pending','dispatched',
//     'running')` as the FIRST ARM of a disjunction with a finished_at window.
//     READ THIS SITE BACKWARDS FROM THE FIVE ABOVE. There, a new status must
//     usually stay OUT and the omission fails closed harmlessly - an unwritable
//     status, an unretryable task. HERE THE OMISSION IS CATASTROPHIC AND SILENT:
//     a new NON-TERMINAL status left out of this arm drops 100% of that state's
//     log output, because a non-terminal row has finished_at IS NULL and so
//     fails the second arm too, and the drop produces no error and no log line
//     anywhere. A new NON-TERMINAL status MUST BE ADDED here.
//     TASK_STATUS_PREPARING already exists in proto/relayv1/relay.proto and the
//     agent already streams prepare progress as LOG_STREAM_PREPARE chunks
//     (internal/agent/runner.go), so it is the concrete candidate - and its twin
//     TASK_STATUS_PREPARE_FAILED needs the OPPOSITE call. A new TERMINAL status
//     must stay OUT and is then bounded by finished_at like done/failed/
//     timed_out. Never conjoin this arm with the rest of the fence: that closes
//     the trailing flush.
```

Change no assertion and no `want` slice. The vocabulary is unchanged by this task.

- [ ] **Step 3: Run the guard**

Run:
```
go test -tags integration -p 1 ./internal/store/... -run TestTasksStatusVocabularyIsExactly -v -timeout 300s
```
Expected: PASS. (It reads `pg_get_constraintdef`; this change adds no status, so the assertion is untouched.)

- [ ] **Step 4: Commit**

```bash
git add internal/store/tasks_status_vocabulary_lockstep_test.go
git commit -m "docs(store): name AppendTaskLog in the status vocabulary guard

Sixth site, and the only one whose guidance is inverted: at the other five a
new status must usually stay OUT, here a new NON-TERMINAL status must be
ADDED or 100% of that state's log output is silently dropped. Also corrects
the intro's site count, which said three while listing five."
```

---

## Task 8: README

**Files:**
- Modify: `README.md:276` (insert one row after `RELAY_WORKER_GRACE_WINDOW`)

- [ ] **Step 1: Add the env-table row**

In `README.md`, line 276 currently reads:

```
| `RELAY_WORKER_GRACE_WINDOW` | `2m` | How long to wait before requeueing tasks from a disconnected agent |
```

Insert immediately after it:

```
| `RELAY_TASKLOG_TRAILING_WINDOW` | `15m` | How long after a task finishes its assigned agent may still append log chunks for it. A legitimately late chunk is under 2 minutes late in the worst case, so the default carries about 8x margin. **Set it too small and real trailing output is silently truncated** - a rejected chunk is dropped with no error to the agent and no line in the server log, exactly like a stale-epoch chunk. An unparseable, zero or negative value keeps the default and logs one warning at startup. Set a very large value (`8760h`) to restore the old unbounded behaviour. |
```

- [ ] **Step 2: Check the rendering**

Confirm the table still has three columns per row and that no em dash or en dash was introduced. Run:
```
grep -n "RELAY_TASKLOG_TRAILING_WINDOW" README.md
```
Expected: one line, 277.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document RELAY_TASKLOG_TRAILING_WINDOW

Names what a too-small value does silently, since a truncated tail has no
signal anywhere, and names the escape hatch back to the old behaviour."
```

---

## Task 9: Full gates, the mutation battery, and closing the backlog item

- [ ] **Step 1: Run every gate**

Run, in order:
```
go build ./...
go vet -tags integration ./...
go test ./... -timeout 120s
go test -tags integration -p 1 ./internal/store/... -timeout 900s
go test -tags integration -p 1 ./internal/worker/... -timeout 900s
go test -tags integration -p 1 ./internal/scheduler/... -timeout 900s
go test -tags integration -p 1 ./internal/api/... -timeout 900s
```
Expected: all PASS. Record the pass counts. If anything is red, **diagnose it and get a number both with and without the change** before calling it pre-existing.

- [ ] **Step 2: Run the mutation battery**

Work through the table in Appendix A. For each mutation: apply it, regenerate if it touches SQL, run the named test, confirm the named assertion fails with the named value, then revert and confirm green again. Report any mutation whose prediction was wrong - **a mutation that does not redden the predicted assertion is a finding about the test, not about the plan.** (Mutations A and B from Task 4 Step 6 are rows P4 and P5; if you already ran them there, say so rather than repeating them.)

- [ ] **Step 3: Verify the working tree is exactly the intended file set**

Run:
```
git status --short
git diff --stat origin/main...HEAD
```
Expected file set, and nothing else:
```
README.md
cmd/relay-server/main.go
cmd/relay-server/trailing_log_window_test.go
internal/store/query/tasks.sql
internal/store/tasks.sql.go
internal/store/store_test.go
internal/store/retry_job_tasks_integration_test.go
internal/store/tasks_status_vocabulary_lockstep_test.go
internal/worker/handler.go
internal/worker/handler_tasklog_integration_test.go
docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md
docs/superpowers/plans/2026-08-14-tasklog-terminal-append-bound.md
```
Plus, after Step 4, the `git mv` of the backlog item. **`internal/store/models.go` must NOT appear.** Neither must anything under `internal/proto/` or `web/`.

- [ ] **Step 4: Close the backlog item**

Run the command; do not hand-edit the item's frontmatter. The `git mv` into `docs/backlog/closed/` is required scope, not cleanup.

```
/backlog close tasklog-terminal-task-append-unbounded
```

Confirm afterwards:
```
git status --short docs/backlog/
```
Expected: the item now lives at `docs/backlog/closed/bug-2026-08-12-tasklog-terminal-task-append-unbounded.md` with `status: closed`, a `closed:` and `resolution:` stamp, and a `## Resolution` section.

- [ ] **Step 5: Final report**

Report, in this order:
1. The verbatim RED output from Task 2 Step 4 (spec acceptance criterion 1).
2. The mutation battery results, including any prediction that was wrong.
3. Confirmation that the pinned flush test gained exactly one line and no assertion changed, and that `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` needed no edit at all.
4. Confirmation of the five read-backs from Task 3 Step 4.
5. The `int32(chunk.Epoch)` truncation you deliberately did not fix, flagged for `bug-2026-08-12-tasklog-epoch-int32-truncation`.
6. Anything that surprised you.

---

## Appendix A: Mutation battery

**Enumerated by property, not by function.** A battery organized by function covers the half of a pair that the function writes and misses the half it reads; that is exactly how a slice earlier in this batch shipped a seven-mutation battery that could not detect the bug in its own test's title. Each row below names a **behavioral property**, and for the properties with a read half and a write half (P1, P8) the same mutation must redden both.

SQL mutations require `sqlc generate` plus the CRLF dance before running, and again on revert.

| # | Property under test | Mutation | Test + subtest that must go RED | Predicted assertion and value |
|---|---|---|---|---|
| P1a | A terminal task outside the window is **not stored** | Delete the entire third predicate from the fence (revert to HEAD's two-predicate fence) | `TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow` | `assert.Empty(t, rows, "...must not be stored")` - `rows` has len **1**, content `"long after the task finished\n"` |
| P1b | A terminal task outside the window is **not published** | Same mutation as P1a | Same test | `assert.Nil(t, published, "...must not be published")` - `published` is a non-nil `[]byte` of the marshalled `taskLogEvent` |
| P1c | Same property at the statement | Same mutation as P1a | `TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow/finished_one_hour_ago...` | `require.ErrorIs(err, pgx.ErrNoRows)` - got `<nil>`; then `require.Empty(logs)` - len **1** |
| P2a | A terminal task **inside** the window is still stored and published (the flush survives) | Change the `OR` between the two arms to `AND` | `TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored` | `t.Fatal("a trailing chunk from the assignee must still be published after a terminal status")` after the 5 s timeout; if the publish check is removed, `require.Len(t, rows, 1)` - len **0** |
| P2b | Same property at the statement | Same mutation as P2a | `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` | `require.NoError(t, err, "a trailing chunk from the assignee must still persist after a terminal status")` - got `pgx.ErrNoRows` |
| P2c | Same property, table-driven | Same mutation as P2a | `TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow/finished_one_minute_ago...` | `require.NoError(t, err)` - got `pgx.ErrNoRows` |
| P3a | A **live** task is accepted regardless of finished_at and cutoff | Delete the status allow-list arm, leaving only `AND t.finished_at > $4::timestamptz` | `TestHandleTaskLog_LiveTaskWithNoFinishedAtIsStillStoredAndPublished` | `t.Fatal("a chunk from a live task must be published")` after 5 s; and `require.Len(t, rows, 1)` - len **0** |
| P3b | Same property, plus every pre-existing live-task test | Same mutation as P3a | `TestHandleTaskLog_PublishesToATaskScopedSubscriber`, `..._NoSubscriberSkipsMarshalButStillPersists`, `..._RejectsAChunkFromANonAssignee` (its positive control), both e2e tests, and `TestAppendTaskLog_EpochGuarded` | first failure is `t.Fatal("no task_log event was published")` in `..._PublishesToATaskScopedSubscriber`, then `require.Len(t, page, 1)` - len **0** |
| P3c | Same property, table-driven, including the no-cutoff case | Same mutation as P3a | `TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow/{dispatched...,running...,live_task_with_NO_cutoff...}` | `require.NoError(t, err)` - got `pgx.ErrNoRows` in all three |
| P4 | A terminal row with a **NULL finished_at** fails closed | Rewrite the second arm as `t.finished_at IS NULL OR t.finished_at > $4::timestamptz` (Task 4 Mutation A) | `TestAppendTaskLog_TerminalTaskAcceptsOnlyInsideTheTrailingWindow/terminal_with_a_NULL_finished_at_must_fail_CLOSED` **only** | `require.ErrorIs(err, pgx.ErrNoRows)` - got `<nil>`; then `require.Empty(logs)` - len **1**. The omitted-cutoff subtest stays GREEN here and that is correct: its row's `finished_at` is non-NULL |
| P5 | A caller that **omits the cutoff** fails closed | `t.finished_at > COALESCE(sqlc.arg(min_finished_at)::timestamptz, '-infinity'::timestamptz)` (Task 4 Mutation B) | `.../terminal_and_the_caller_omits_the_cutoff_must_fail_CLOSED` **only** | `require.ErrorIs(err, pgx.ErrNoRows)` - got `<nil>`; then `require.Empty(logs)` - len **1** |
| P6 | The window is **read from the field** at the call site | `MinFinishedAt: ...Add(-DefaultTrailingLogWindow)...` (ignore `h.TrailingLogWindow`) | `TestHandleTaskLog_TheWindowIsReadFromTheHandlerFieldAtEveryCall` | `require.Empty(t, rows, "with a 1s window, a task that finished 2s ago must reject the chunk")` - len **1**, content `"outside\n"` |
| P7 | Non-positive means the **default**, not zero | `if window < 0` instead of `if window <= 0` | `TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow` | `require.Len(t, rows, 1, "an unset window must behave as DefaultTrailingLogWindow, not as zero")` - len **0** |
| P8a | The publish is **gated on the fence having matched** (the read half of the pair) | Move the `HasLogSubscriber`/`Publish` block above the `if err != nil` check | `TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow` | `assert.Nil(t, published, ...)` - non-nil, while `assert.Empty(t, rows, ...)` still PASSES. **This is why the exposure test asserts both halves**; a battery that only checked "not stored" would miss it entirely |
| P8b | Same property against a stale epoch | Same mutation as P8a | `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished` | `t.Fatalf("a stale-epoch chunk must not be published: %s", e.Data)` |
| P9 | The rejection stays **silent** | Add `log.Printf("worker: handleTaskLog AppendTaskLog %s: window closed", chunk.TaskId)` to the `pgx.ErrNoRows` branch | `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` | `assert.Equal(t, 2, countLines(logged(), marker), "a stale-epoch drop must stay silent")` - got **3** |
| P10a | The env value **resolves** correctly | `if err != nil` instead of `if err != nil \|\| d <= 0` | `TestParseTrailingLogWindow/{zero...,negative...}` | `require.Equal(tc.want, got)` - want `15m0s`, got `0s` and `-5m0s` |
| P10b | The **unset** case does not warn | Delete the `raw == ""` early return | `TestParseTrailingLogWindow/unset_keeps_the_default_and_does_NOT_warn` | `require.Equal(tc.wantOK, ok)` - want `true`, got `false` |

### Known blind spots - state these in the report, do not pretend they are covered

1. **Rewriting the first arm as the equivalent deny-list** (`t.status NOT IN ('done','failed','timed_out')`) reddens **nothing**. The two spellings are exactly equivalent against today's six-value vocabulary; they diverge only when a seventh status is added, and then the deny-list fails **open** while the allow-list fails closed. The only controls are review and `TestTasksStatusVocabularyIsExactly`'s guidance, which is why Task 7 is a named task and not a footnote.
2. **Deleting `agentHandler.TrailingLogWindow = trailingLogWindow` from `main.go`** reddens **nothing**. No test constructs `main()`. The handler would fall back to `DefaultTrailingLogWindow` and the env var would become a no-op while the startup warning still printed for bad values - a silent knob. Its only protection is adjacency to `agentHandler.Metrics` and `agentHandler.AllowAutoEnroll`, which have the same exposure. Do not invent a test for it; report it.
3. **Two relay-server instances with skewed clocks** shift the effective window by that skew. Recorded in the spec (section 6.2) and not mitigated. Untestable here.

---

## Appendix B: Constraint checks (state these in the Phase 4 report)

- **Epoch fence.** `AppendTaskLog` satisfies the invariant's **first branch**: it fences on `assignment_epoch` matching the caller's epoch. That is unchanged - the new predicate is additive and changes no branch, since it neither ends a generation nor is a terminal-only writer. The identity predicate on `worker_id` stays a NULL-rejecting plain `=`. The side effect (the `Publish` in `handleTaskLog`) stays gated on the fence having actually matched: no fence row means no inserted row means zero result rows means `pgx.ErrNoRows`, and `handleTaskLog` returns at line 771 before reaching the publish. **Enumerate what runs before it:** `taskID.Scan(chunk.TaskId)` (silent return on failure), the stream enum mapping, and now the `window` resolution - three cheap in-process steps, no database access, and `workerID` is the connection's authenticated identity resolved once in `Connect` (`handler.go:115-119`), never read off the wire. Nothing new runs before the fence.
- **Status predicates are allow-lists.** The first arm is one, in the same spelling as `UpdateTaskStatus` and `IncrementTaskRetryCount`, and the vocabulary guard gains it as a named site with inverted guidance.
- **One bounded sender per gRPC stream.** Untouched. No send is added; this is entirely on the recv path.
- **Identity-checked teardown.** Untouched.
- **No interior pointers across locks.** Untouched. The new state is a `time.Duration` **value** on `Handler`, written once at startup and read-only thereafter.
- **Single JSON entry point / single job-spec pipeline / `tokenhash.Hash`.** Not implicated.
- **Never edit `*.sql.go` or `models.go`.** The change is in `query/tasks.sql` plus `sqlc generate`, with the read-back in Task 3 Step 4.
- **CLAUDE.md is not amended.** This does not change the epoch-fence invariant; it adds a predicate underneath it. Recorded as a decision because the last three slices in this family each amended that bullet, so silence here should read as deliberate.
- **Load.** No new query, statement, index, goroutine or queue. The predicate is evaluated on a row already located by primary key, so the plan does not change and no index is needed. Per chunk: one `time.Now()` and one extra bound parameter.
