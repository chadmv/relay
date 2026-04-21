# Major Concurrency & Scaling Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let long-running tasks survive brief network blips and clean server restarts; fix the scheduler's N×M query pattern; switch dispatch wake-up to LISTEN/NOTIFY; make pgxpool sizing configurable.

**Architecture:** Introduce a per-assignment `assignment_epoch` that increments on every claim. The agent sends its list of running tasks in `RegisterRequest`; the coordinator diffs against the DB and replies with `cancel_task_ids` for stale assignments. On disconnect (or server startup with in-flight tasks) the coordinator starts a per-worker grace timer rather than immediately requeueing. Every task-related proto message carries the epoch; SQL enforces it via epoch-guarded WHERE clauses. Separately: scheduler computes per-worker counts in one aggregate query, NOTIFY replaces 5s polling as the primary wake signal (polling relaxes to 30s), and the pgxpool is configurable.

**Tech Stack:** Go 1.26, pgx/v5, sqlc, gRPC/proto3, golang-migrate, testcontainers-go, testify.

**Spec:** `docs/superpowers/specs/2026-04-20-major-concurrency-fixes-design.md`

---

## File structure

**New files:**
- `internal/store/migrations/000004_assignment_epoch.up.sql` + `.down.sql`
- `internal/worker/grace.go` + `internal/worker/grace_test.go`
- `internal/scheduler/notify.go` + `internal/scheduler/notify_test.go`

**Modified files:**
- `proto/relayv1/relay.proto` — new fields
- `internal/store/query/tasks.sql` — epoch-guarded queries + new reconciliation queries
- `internal/scheduler/dispatch.go` — N×M fix, 30s polling, epoch on DispatchTask
- `internal/scheduler/dispatch_test.go` — update for epoch + N×M
- `internal/worker/handler.go` — reconcile-on-register, epoch checks, grace timer
- `internal/worker/handler_test.go` — new integration tests
- `internal/agent/agent.go` — runCtx, RegisterRequest.RunningTasks, cancel_task_ids handling, drop drain
- `internal/agent/runner.go` — epoch field, Abandon method
- `internal/agent/runner_test.go` — new tests
- `internal/api/jobs.go` (or whichever file hosts task insert) — NOTIFY on insert
- `cmd/relay-server/main.go` — grace registry wiring, startup seed, pool config, NotifyListener wiring

---

## Phase 1 — Foundation (migration + proto + plumbing)

### Task 1: Add `assignment_epoch` migration

**Files:**
- Create: `internal/store/migrations/000004_assignment_epoch.up.sql`
- Create: `internal/store/migrations/000004_assignment_epoch.down.sql`
- Test: `internal/store/store_test.go` (extend with a migration check)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
func TestAssignmentEpochColumnExists(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Eve", "eve@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "epoch-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// Freshly created tasks must start at epoch 0.
	assert.Equal(t, int32(0), task.AssignmentEpoch)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestAssignmentEpochColumnExists -v -timeout 120s
```

Expected: FAIL with a compilation error (`task.AssignmentEpoch undefined`) or migration error.

- [ ] **Step 3: Write the up migration**

Create `internal/store/migrations/000004_assignment_epoch.up.sql`:

```sql
ALTER TABLE tasks ADD COLUMN assignment_epoch INT NOT NULL DEFAULT 0;
```

- [ ] **Step 4: Write the down migration**

Create `internal/store/migrations/000004_assignment_epoch.down.sql`:

```sql
ALTER TABLE tasks DROP COLUMN assignment_epoch;
```

- [ ] **Step 5: Regenerate sqlc**

```bash
make generate
```

Expected: `internal/store/models.go` now has an `AssignmentEpoch int32` field on `Task`.

- [ ] **Step 6: Run the test to verify it passes**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestAssignmentEpochColumnExists -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 7: Run the full store test suite to catch any regressions**

```bash
go test -tags integration -p 1 ./internal/store/... -v -timeout 120s
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/store/migrations/000004_assignment_epoch.up.sql \
        internal/store/migrations/000004_assignment_epoch.down.sql \
        internal/store/models.go \
        internal/store/store_test.go
git commit -m "feat(store): add assignment_epoch column to tasks"
```

---

### Task 2: Proto additions for epoch and reconciliation

**Files:**
- Modify: `proto/relayv1/relay.proto`
- Regenerate: `internal/proto/relayv1/relay.pb.go`, `internal/proto/relayv1/relay_grpc.pb.go`

- [ ] **Step 1: Edit `proto/relayv1/relay.proto`**

Replace the body with:

```proto
syntax = "proto3";

package relay.v1;

option go_package = "relay/internal/proto/relayv1";

service AgentService {
  rpc Connect(stream AgentMessage) returns (stream CoordinatorMessage);
}

message AgentMessage {
  oneof payload {
    RegisterRequest  register    = 1;
    TaskStatusUpdate task_status = 2;
    TaskLogChunk     task_log    = 3;
  }
}

// Sent once when the stream opens. worker_id is empty on first registration.
// running_tasks is the agent's list of currently-executing tasks at reconnect
// time (empty on first connect). The coordinator diffs against DB state and
// replies with RegisterResponse.cancel_task_ids for any stale assignments.
message RegisterRequest {
  string worker_id                 = 1;
  string hostname                  = 2;
  int32  cpu_cores                 = 3;
  int32  ram_gb                    = 4;
  int32  gpu_count                 = 5;
  string gpu_model                 = 6;
  string os                        = 7;
  repeated RunningTask running_tasks = 8;
}

// A task the agent believes it is currently running, with the epoch assigned
// at dispatch time. The coordinator uses this to detect stale assignments.
message RunningTask {
  string task_id = 1;
  int64  epoch   = 2;
}

message TaskStatusUpdate {
  string         task_id       = 1;
  TaskStatus     status        = 2;
  optional int32 exit_code     = 3;
  string         error_message = 4;
  int64          epoch         = 5;
}

message TaskLogChunk {
  string    task_id = 1;
  LogStream stream  = 2;
  bytes     content = 3;
  int64     epoch   = 4;
}

message CoordinatorMessage {
  oneof payload {
    RegisterResponse register_response = 1;
    DispatchTask     dispatch_task     = 2;
    CancelTask       cancel_task       = 3;
  }
}

// Sent in response to RegisterRequest. cancel_task_ids lists tasks the agent
// reported as running that the coordinator considers stale (reassigned during
// grace expiry, or unknown). The agent must abandon these without sending a
// final status update.
message RegisterResponse {
  string          worker_id       = 1;
  repeated string cancel_task_ids = 2;
}

message DispatchTask {
  string              task_id         = 1;
  string              job_id          = 2;
  repeated string     command         = 3;
  map<string, string> env             = 4;
  int32               timeout_seconds = 5;
  int64               epoch           = 6;
}

message CancelTask {
  string task_id = 1;
}

enum TaskStatus {
  TASK_STATUS_UNSPECIFIED = 0;
  TASK_STATUS_RUNNING     = 1;
  TASK_STATUS_DONE        = 2;
  TASK_STATUS_FAILED      = 3;
  TASK_STATUS_TIMED_OUT   = 4;
}

enum LogStream {
  LOG_STREAM_UNSPECIFIED = 0;
  LOG_STREAM_STDOUT      = 1;
  LOG_STREAM_STDERR      = 2;
}
```

- [ ] **Step 2: Regenerate protobuf**

```bash
make generate
```

Expected: `internal/proto/relayv1/relay.pb.go` is rewritten with new fields. No compile errors yet because nothing reads these fields.

- [ ] **Step 3: Verify the whole module still compiles**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 4: Run the full test suite to confirm no regression**

```bash
make test
```

Expected: all existing unit tests pass.

- [ ] **Step 5: Commit**

```bash
git add proto/relayv1/relay.proto \
        internal/proto/relayv1/relay.pb.go \
        internal/proto/relayv1/relay_grpc.pb.go
git commit -m "feat(proto): add epoch fields and reconcile messages"
```

---

### Task 3: `ClaimTaskForWorker` increments epoch

**Files:**
- Modify: `internal/store/query/tasks.sql:81-88` (ClaimTaskForWorker)
- Regenerate: `internal/store/tasks.sql.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
func TestClaimTaskForWorker_IncrementsEpoch(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Frank", "frank@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "w1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.Equal(t, int32(0), task.AssignmentEpoch)

	// First claim: epoch goes 0 -> 1.
	claimed1, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), claimed1.AssignmentEpoch)

	// Requeue so we can claim again.
	require.NoError(t, q.RequeueTask(ctx, task.ID))

	// Second claim: epoch goes 1 -> 2. Epoch never decreases.
	claimed2, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), claimed2.AssignmentEpoch)
}
```

Add `"github.com/jackc/pgx/v5/pgtype"` to imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestClaimTaskForWorker_IncrementsEpoch -v -timeout 120s
```

Expected: FAIL — epoch remains 0 after claim (current query doesn't touch it).

- [ ] **Step 3: Modify the query**

In `internal/store/query/tasks.sql`, replace the `ClaimTaskForWorker` block (lines 81-88) with:

```sql
-- name: ClaimTaskForWorker :one
-- Atomically transition a pending task to 'dispatched' on the given worker.
-- Increments assignment_epoch so subsequent status updates from prior
-- generations can be rejected. Returns pgx.ErrNoRows if the task is no longer
-- pending (another dispatcher already claimed it, or the row vanished).
UPDATE tasks
SET status = 'dispatched',
    worker_id = $2,
    assignment_epoch = assignment_epoch + 1
WHERE id = $1 AND status = 'pending'
RETURNING *;
```

- [ ] **Step 4: Regenerate sqlc**

```bash
make generate
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestClaimTaskForWorker_IncrementsEpoch -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 6: Run all store tests**

```bash
go test -tags integration -p 1 ./internal/store/... -v -timeout 120s
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go internal/store/store_test.go
git commit -m "feat(store): increment assignment_epoch on claim"
```

---

### Task 4: Plumb epoch from dispatcher through to outgoing agent messages

**Files:**
- Modify: `internal/scheduler/dispatch.go:160-170` (sendTask)
- Modify: `internal/agent/runner.go:16-36, 79-86, 120-161` (Runner, newRunner, send paths)
- Modify: `internal/agent/agent.go:179-192` (handleDispatch)
- Test: new `internal/agent/runner_epoch_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/runner_epoch_test.go`:

```go
package agent

import (
	"context"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
)

func TestRunnerTagsOutgoingMessagesWithEpoch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendCh := make(chan *relayv1.AgentMessage, 8)
	runner, runCtx := newRunner("task-123", 42, sendCh, ctx, 0)

	go runner.Run(runCtx, &relayv1.DispatchTask{
		TaskId:  "task-123",
		Command: []string{"true"},
	})

	// Collect all messages until channel drains.
	var msgs []*relayv1.AgentMessage
	for i := 0; i < 2; i++ {
		select {
		case m := <-sendCh:
			msgs = append(msgs, m)
		case <-ctx.Done():
			t.Fatal("timed out waiting for messages")
		}
	}

	// Every outgoing TaskStatusUpdate must carry epoch=42.
	for _, m := range msgs {
		if ts := m.GetTaskStatus(); ts != nil {
			assert.Equal(t, int64(42), ts.Epoch)
		}
		if tl := m.GetTaskLog(); tl != nil {
			assert.Equal(t, int64(42), tl.Epoch)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/agent/ -run TestRunnerTagsOutgoingMessagesWithEpoch -v -timeout 30s
```

Expected: FAIL — `newRunner` signature doesn't take an epoch; `ts.Epoch` references a field that, while now in proto, isn't populated by runner.

- [ ] **Step 3: Add `epoch` field to Runner and parameter to `newRunner`**

Modify `internal/agent/runner.go` lines 15-36:

```go
// Runner manages the execution of a single dispatched task as a subprocess.
type Runner struct {
	taskID    string
	epoch     int64
	sendCh    chan *relayv1.AgentMessage
	ctx       context.Context // parent (connection) context — cancelled only when the connection drops
	cancel    context.CancelFunc
	cancelled atomic.Bool
}

// newRunner creates a Runner and its execution context.
// If timeoutSec > 0, the context carries a deadline; otherwise it inherits
// only the parent's cancellation.
func newRunner(taskID string, epoch int64, sendCh chan *relayv1.AgentMessage, parent context.Context, timeoutSec int32) (*Runner, context.Context) {
	var runCtx context.Context
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		runCtx, cancel = context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
	} else {
		runCtx, cancel = context.WithCancel(parent)
	}
	return &Runner{taskID: taskID, epoch: epoch, sendCh: sendCh, ctx: parent, cancel: cancel}, runCtx
}
```

- [ ] **Step 4: Tag outgoing TaskStatusUpdate with epoch**

Modify `internal/agent/runner.go` line ~79-86 (TASK_STATUS_RUNNING message):

```go
	r.send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId: r.taskID,
				Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
				Epoch:  r.epoch,
			},
		},
	})
```

Modify `sendFinalStatus` (line ~143):

```go
func (r *Runner) sendFinalStatus(status relayv1.TaskStatus, exitCode *int32) {
	upd := &relayv1.TaskStatusUpdate{
		TaskId:   r.taskID,
		Status:   status,
		ExitCode: exitCode,
		Epoch:    r.epoch,
	}
	r.send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{TaskStatus: upd},
	})
}
```

- [ ] **Step 5: Tag outgoing TaskLogChunk with epoch**

Modify `pipeLog` (line ~120-141):

```go
func (r *Runner) pipeLog(pipe io.Reader, stream relayv1.LogStream) {
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			r.send(&relayv1.AgentMessage{
				Payload: &relayv1.AgentMessage_TaskLog{
					TaskLog: &relayv1.TaskLogChunk{
						TaskId:  r.taskID,
						Stream:  stream,
						Content: chunk,
						Epoch:   r.epoch,
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

- [ ] **Step 6: Update `handleDispatch` to pass epoch**

Modify `internal/agent/agent.go` line ~179-192:

```go
func (a *Agent) handleDispatch(ctx context.Context, task *relayv1.DispatchTask) {
	runner, runCtx := newRunner(task.TaskId, task.Epoch, a.sendCh, ctx, task.TimeoutSeconds)
	a.mu.Lock()
	a.runners[task.TaskId] = runner
	a.mu.Unlock()
	a.runnerWG.Add(1)
	go func() {
		defer a.runnerWG.Done()
		runner.Run(runCtx, task)
		a.mu.Lock()
		delete(a.runners, task.TaskId)
		a.mu.Unlock()
	}()
}
```

- [ ] **Step 7: Update `sendTask` in dispatcher to include epoch in DispatchTask**

Modify `internal/scheduler/dispatch.go` line ~160 (inside the `&relayv1.DispatchTask{...}` literal):

```go
	msg := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{
				TaskId:         uuidStr(claimed.ID),
				JobId:          uuidStr(claimed.JobID),
				Command:        claimed.Command,
				Env:            env,
				TimeoutSeconds: timeoutSecs,
				Epoch:          int64(claimed.AssignmentEpoch),
			},
		},
	}
```

- [ ] **Step 8: Run the new runner test**

```bash
go test ./internal/agent/ -run TestRunnerTagsOutgoingMessagesWithEpoch -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 9: Run the full agent and scheduler test suites**

```bash
go test ./internal/agent/... ./internal/scheduler/... -v -timeout 60s
go test -tags integration -p 1 ./internal/scheduler/... -v -timeout 120s
```

Expected: all pass. (Existing dispatcher tests may compile-fail because they construct `DispatchTask` or use runner helpers — fix the constructions to include the new `Epoch` field or updated `newRunner` arity as the compiler points you to them.)

- [ ] **Step 10: Commit**

```bash
git add internal/agent/runner.go internal/agent/agent.go internal/agent/runner_epoch_test.go \
        internal/scheduler/dispatch.go
git commit -m "feat(agent): plumb assignment_epoch through dispatch to outgoing messages"
```

---

### Task 5: Epoch-guard `UpdateTaskStatus` + early-check in handler

**Files:**
- Modify: `internal/store/query/tasks.sql:12-16` (UpdateTaskStatus)
- Regenerate: `internal/store/tasks.sql.go`
- Modify: `internal/worker/handler.go:125-206` (handleTaskStatus)
- Test: `internal/store/store_test.go`, `internal/worker/handler_test.go`

- [ ] **Step 1: Write the failing store test**

Append to `internal/store/store_test.go`:

```go
func TestUpdateTaskStatus_EpochGuarded(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Gina", "gina@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "w1-epoch", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// Update with MATCHING epoch should succeed.
	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:     task.ID,
		Status: "running",
		Epoch:  1,
	})
	require.NoError(t, err)

	// Update with STALE epoch should return pgx.ErrNoRows (0 rows affected).
	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:     task.ID,
		Status: "done",
		Epoch:  0, // stale
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows)

	// Task should still be "running" — the stale update did nothing.
	fetched, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", fetched.Status)
}
```

Add `"github.com/jackc/pgx/v5"` import if absent.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestUpdateTaskStatus_EpochGuarded -v -timeout 120s
```

Expected: FAIL — `store.UpdateTaskStatusParams` has no `Epoch` field yet.

- [ ] **Step 3: Modify the SQL query**

In `internal/store/query/tasks.sql`, replace the `UpdateTaskStatus` block (lines 12-16) with:

```sql
-- name: UpdateTaskStatus :one
-- Updates a task's status only if the caller's epoch matches the current
-- assignment. Returns pgx.ErrNoRows if the caller's epoch is stale (zombie
-- status update from a prior assignment).
UPDATE tasks
SET status = $2, worker_id = $3, started_at = $4, finished_at = $5
WHERE id = $1 AND assignment_epoch = $6
RETURNING *;
```

- [ ] **Step 4: Regenerate sqlc**

```bash
make generate
```

Expected: `UpdateTaskStatusParams` gains an `Epoch int32` field.

- [ ] **Step 5: Run the store test to verify it passes**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestUpdateTaskStatus_EpochGuarded -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 6: Update handler to pass epoch and add early epoch check**

The existing callers of `UpdateTaskStatus` will now fail to compile because of the new required param. Fix `handleTaskStatus` in `internal/worker/handler.go`:

Replace lines 125-206 (`handleTaskStatus` entire function) with:

```go
// handleTaskStatus processes a TaskStatusUpdate from an agent.
func (h *Handler) handleTaskStatus(ctx context.Context, upd *relayv1.TaskStatusUpdate) {
	var taskID pgtype.UUID
	if err := taskID.Scan(upd.TaskId); err != nil {
		return
	}

	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		return
	}

	// Epoch gate: reject any status update whose epoch doesn't match the
	// current assignment. Retry logic below must not run on stale updates.
	if int64(task.AssignmentEpoch) != upd.Epoch {
		return
	}

	// Map proto enum to string status.
	var statusStr string
	switch upd.Status {
	case relayv1.TaskStatus_TASK_STATUS_RUNNING:
		statusStr = "running"
	case relayv1.TaskStatus_TASK_STATUS_DONE:
		statusStr = "done"
	case relayv1.TaskStatus_TASK_STATUS_FAILED:
		statusStr = "failed"
	case relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
		statusStr = "timed_out"
	default:
		return
	}

	terminal := statusStr == "failed" || statusStr == "timed_out"

	// Retry if applicable. Epoch guard above ensures we don't double-retry.
	if terminal && task.RetryCount < task.Retries {
		if _, err := h.q.IncrementTaskRetryCount(ctx, taskID); err == nil {
			updateJobStatusFromTasks(ctx, h.q, task.JobID)
			go h.triggerDispatch()
		}
		return
	}

	// Determine timestamps.
	startedAt := task.StartedAt
	if statusStr == "running" {
		startedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	var finishedAt pgtype.Timestamptz
	if terminal || statusStr == "done" {
		finishedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}

	updated, err := h.q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:         taskID,
		Status:     statusStr,
		WorkerID:   task.WorkerID,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Epoch:      int32(upd.Epoch),
	})
	if err != nil {
		return
	}

	if terminal {
		_ = h.q.FailDependentTasks(ctx, taskID)
	}

	jobStatus := updateJobStatusFromTasks(ctx, h.q, updated.JobID)

	h.broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(updated.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(taskID), statusStr)),
	})

	if jobStatus == "done" || jobStatus == "failed" {
		h.broker.Publish(events.Event{
			Type:  "job",
			JobID: uuidStr(updated.JobID),
			Data:  []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, uuidStr(updated.JobID), jobStatus)),
		})
	}

	if statusStr == "done" {
		go h.triggerDispatch()
	}
}
```

- [ ] **Step 7: Build and run all existing tests**

```bash
go build ./...
go test ./... -v -timeout 60s
go test -tags integration -p 1 ./... -v -timeout 300s
```

Expected: all pass. Existing handler integration tests still send epoch=0 for tasks that started at epoch=0 pre-claim (since TaskStatusUpdate is constructed manually in tests). Any test that now fails because it constructs a status update without epoch must be updated to supply the matching epoch. Fix as the compiler/test failures indicate.

- [ ] **Step 8: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go internal/store/store_test.go \
        internal/worker/handler.go internal/worker/handler_test.go
git commit -m "feat(store,worker): epoch-guard UpdateTaskStatus and drop stale status in handler"
```

---

### Task 6: Epoch-guard `AppendTaskLog`

**Files:**
- Modify: `internal/store/query/tasks.sql:44-45` (AppendTaskLog)
- Regenerate: `internal/store/tasks.sql.go`
- Modify: `internal/worker/handler.go:209-225` (handleTaskLog)
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing store test**

Append to `internal/store/store_test.go`:

```go
func TestAppendTaskLog_EpochGuarded(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Hal", "hal@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "w1-logs", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// Log with matching epoch.
	err = q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "hello\n", Epoch: 1,
	})
	require.NoError(t, err)

	// Log with stale epoch.
	err = q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "from zombie\n", Epoch: 0,
	})
	require.NoError(t, err) // :exec returns nil even when 0 rows inserted

	logs, err := q.GetTaskLogs(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "hello\n", logs[0].Content)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestAppendTaskLog_EpochGuarded -v -timeout 120s
```

Expected: FAIL — `AppendTaskLogParams` has no `Epoch` field; both inserts succeed so log count is 2.

- [ ] **Step 3: Modify the query**

In `internal/store/query/tasks.sql`, replace `AppendTaskLog` (lines 44-45) with:

```sql
-- name: AppendTaskLog :exec
-- Inserts a log chunk only if the caller's epoch matches the task's current
-- assignment. Stale chunks (from a reassigned generation) silently insert
-- zero rows.
INSERT INTO task_logs (task_id, stream, content)
SELECT $1, $2, $3
WHERE EXISTS (
    SELECT 1 FROM tasks WHERE id = $1 AND assignment_epoch = $4
);
```

- [ ] **Step 4: Regenerate sqlc**

```bash
make generate
```

- [ ] **Step 5: Update handler caller**

Modify `internal/worker/handler.go` `handleTaskLog` (line ~209-225):

```go
// handleTaskLog appends a log chunk from an agent.
func (h *Handler) handleTaskLog(ctx context.Context, chunk *relayv1.TaskLogChunk) {
	var taskID pgtype.UUID
	if err := taskID.Scan(chunk.TaskId); err != nil {
		return
	}

	stream := "stdout"
	if chunk.Stream == relayv1.LogStream_LOG_STREAM_STDERR {
		stream = "stderr"
	}

	_ = h.q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID:  taskID,
		Stream:  stream,
		Content: string(chunk.Content),
		Epoch:   int32(chunk.Epoch),
	})
}
```

- [ ] **Step 6: Run tests**

```bash
go build ./...
go test -tags integration -p 1 ./internal/store/... -run TestAppendTaskLog_EpochGuarded -v -timeout 120s
go test -tags integration -p 1 ./internal/worker/... -v -timeout 120s
```

Expected: all pass. Existing handler tests may break because they send log chunks without epoch matching; fix by supplying epoch that matches the claim.

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go internal/store/store_test.go \
        internal/worker/handler.go internal/worker/handler_test.go
git commit -m "feat(store,worker): epoch-guard AppendTaskLog with INSERT SELECT WHERE EXISTS"
```

---

## Phase 2 — Server-side reconciliation

### Task 7: Reconciliation queries

**Files:**
- Modify: `internal/store/query/tasks.sql` (add three queries)
- Regenerate: `internal/store/tasks.sql.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
func TestReconciliationQueries(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Ivy", "ivy@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w1, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "recon-1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w2, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w2", Hostname: "recon-2", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	// Task A: dispatched to w1
	taskA, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "a", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskA.ID, WorkerID: pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Task B: also dispatched to w1
	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "b", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskB.ID, WorkerID: pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Task C: dispatched to w2, then left dispatched
	taskC, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "c", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskC.ID, WorkerID: pgtype.UUID{Bytes: w2.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// GetActiveTasksForWorker: w1 should return A and B.
	w1Active, err := q.GetActiveTasksForWorker(ctx, pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true})
	require.NoError(t, err)
	require.Len(t, w1Active, 2)

	// ListWorkersWithActiveTasks: should return both w1 and w2.
	workerIDs, err := q.ListWorkersWithActiveTasks(ctx)
	require.NoError(t, err)
	assert.Len(t, workerIDs, 2)

	// RequeueTaskByID: requeue task A; it should be pending with worker_id cleared.
	require.NoError(t, q.RequeueTaskByID(ctx, taskA.ID))
	a, err := q.GetTask(ctx, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", a.Status)
	assert.False(t, a.WorkerID.Valid)

	// After requeue, GetActiveTasksForWorker(w1) should only return B.
	w1Active, err = q.GetActiveTasksForWorker(ctx, pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true})
	require.NoError(t, err)
	require.Len(t, w1Active, 1)
	assert.Equal(t, taskB.ID, w1Active[0].ID)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestReconciliationQueries -v -timeout 120s
```

Expected: FAIL — new query methods don't exist.

- [ ] **Step 3: Add the queries**

Append to `internal/store/query/tasks.sql`:

```sql
-- name: GetActiveTasksForWorker :many
-- Returns all non-terminal tasks currently assigned to a given worker.
-- Used at reconcile time to compare server's view with the agent's
-- running_tasks report.
SELECT id, assignment_epoch
FROM tasks
WHERE worker_id = $1 AND status IN ('dispatched', 'running')
ORDER BY id;

-- name: ListWorkersWithActiveTasks :many
-- Returns the distinct set of worker IDs with at least one non-terminal
-- task assigned. Used at server startup to seed grace timers.
SELECT DISTINCT worker_id
FROM tasks
WHERE worker_id IS NOT NULL AND status IN ('dispatched', 'running');

-- name: RequeueTaskByID :exec
-- Revert a single task back to 'pending' regardless of current status.
-- Used by the reconcile path when the coordinator has a task assigned
-- that the agent didn't report as running.
UPDATE tasks
SET status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    finished_at = NULL
WHERE id = $1 AND status IN ('dispatched', 'running');
```

- [ ] **Step 4: Regenerate sqlc**

```bash
make generate
```

- [ ] **Step 5: Run the test**

```bash
go test -tags integration -p 1 ./internal/store/... -run TestReconciliationQueries -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go internal/store/store_test.go
git commit -m "feat(store): add reconciliation queries for worker reconnect"
```

---

### Task 8: `GraceRegistry`

**Files:**
- Create: `internal/worker/grace.go`
- Create: `internal/worker/grace_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/worker/grace_test.go`:

```go
package worker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGraceRegistry_StartFiresAfterWindow(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string) {
		if workerID == "w1" {
			fired.Add(1)
		}
	})
	defer g.Stop()

	g.Start("w1")
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(1), fired.Load())
}

func TestGraceRegistry_CancelPreventsFire(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(50*time.Millisecond, func(workerID string) {
		fired.Add(1)
	})
	defer g.Stop()

	g.Start("w1")
	time.Sleep(10 * time.Millisecond)
	g.Cancel("w1")
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load())
}

func TestGraceRegistry_StartIsIdempotent(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(40*time.Millisecond, func(workerID string) {
		fired.Add(1)
	})
	defer g.Stop()

	// Rapid re-starts: timer should reset each time and ultimately fire once.
	g.Start("w1")
	time.Sleep(15 * time.Millisecond)
	g.Start("w1")
	time.Sleep(15 * time.Millisecond)
	g.Start("w1")
	time.Sleep(90 * time.Millisecond)
	assert.Equal(t, int32(1), fired.Load())
}

func TestGraceRegistry_StopPreventsAllFires(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string) {
		fired.Add(1)
	})

	g.Start("w1")
	g.Start("w2")
	g.Stop()
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load())
}

func TestGraceRegistry_CancelNonexistentIsSafe(t *testing.T) {
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string) {})
	defer g.Stop()

	// Should not panic.
	g.Cancel("never-started")
}

func TestGraceRegistry_ConcurrentStartCancelStop(t *testing.T) {
	g := NewGraceRegistry(5*time.Millisecond, func(workerID string) {})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); g.Start("w1") }()
		go func() { defer wg.Done(); g.Cancel("w1") }()
		go func() { defer wg.Done(); g.Start("w2") }()
	}
	wg.Wait()
	g.Stop()
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/worker/ -run TestGraceRegistry -v -timeout 30s
```

Expected: FAIL — `NewGraceRegistry`, `Start`, `Cancel`, `Stop` are undefined.

- [ ] **Step 3: Implement `grace.go`**

Create `internal/worker/grace.go`:

```go
package worker

import (
	"sync"
	"time"
)

// GraceRegistry tracks per-worker grace timers. When a worker disconnects,
// Start schedules its onExpire callback to fire after window. If the worker
// reconnects before expiry, Cancel stops the timer. Stop cancels all pending
// timers without firing any of them (used on server shutdown).
//
// GraceRegistry is safe for concurrent use.
type GraceRegistry struct {
	mu       sync.Mutex
	timers   map[string]*time.Timer
	window   time.Duration
	onExpire func(workerID string)
	stopped  bool
}

// NewGraceRegistry returns a registry configured with the given grace window
// and expiry callback.
func NewGraceRegistry(window time.Duration, onExpire func(workerID string)) *GraceRegistry {
	return &GraceRegistry{
		timers:   make(map[string]*time.Timer),
		window:   window,
		onExpire: onExpire,
	}
}

// Start schedules onExpire(workerID) to fire after window. If a timer already
// exists for workerID, it is reset to the full window (idempotent).
func (g *GraceRegistry) Start(workerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	if t, ok := g.timers[workerID]; ok {
		t.Stop()
	}
	g.timers[workerID] = time.AfterFunc(g.window, func() {
		g.mu.Lock()
		// Re-check: the timer we're running under may have been replaced
		// or cancelled between expiry and our lock acquisition.
		if cur, ok := g.timers[workerID]; !ok || cur == nil {
			g.mu.Unlock()
			return
		}
		delete(g.timers, workerID)
		g.mu.Unlock()
		g.onExpire(workerID)
	})
}

// Cancel stops any pending timer for workerID. Safe to call if no timer exists.
func (g *GraceRegistry) Cancel(workerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.timers[workerID]; ok {
		t.Stop()
		delete(g.timers, workerID)
	}
}

// Stop cancels all pending timers without firing any of them. After Stop,
// subsequent Start calls are no-ops.
func (g *GraceRegistry) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	for id, t := range g.timers {
		t.Stop()
		delete(g.timers, id)
	}
}
```

- [ ] **Step 4: Run tests (with race detector)**

```bash
go test -race ./internal/worker/ -run TestGraceRegistry -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/grace.go internal/worker/grace_test.go
git commit -m "feat(worker): add GraceRegistry for per-worker requeue timers"
```

---

### Task 9: Reconcile logic in `registerWorker`

**Files:**
- Modify: `internal/worker/handler.go:17-122` (Handler struct, NewHandler, registerWorker)
- Test: `internal/worker/handler_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `internal/worker/handler_test.go` (below existing tests):

```go
func TestRegisterWorker_ReconcilesRunningTasks(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStoreWithPool(t) // helper below; uses same pattern as dispatch_test.go
	_ = pool                           // silence unused
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	grace := worker.NewGraceRegistry(1*time.Minute, func(string) {})
	h := worker.NewHandlerWithGrace(q, registry, broker, func() {}, grace)

	// Seed: job, two workers (w1 distinct, the reconnecting one), a task
	// claimed to the reconnecting worker, and a task claimed to a different
	// worker (to verify we only touch the reconnecting worker's tasks).
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "recon@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	workerRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "recon", Hostname: "recon-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	tMatch, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "match", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	tMatchClaimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: tMatch.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	tStale, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "stale", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: tStale.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	tServerOnly, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "server-only", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: tServerOnly.ID, WorkerID: pgtype.UUID{Bytes: workerRow.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Agent reports: tMatch with matching epoch (should keep), tStale with
	// stale epoch (should cancel), tServerOnly NOT reported (should requeue).
	matchIDStr := uuidStr(tMatchClaimed.ID)
	staleIDStr := uuidStr(tStale.ID)
	serverOnlyIDStr := uuidStr(tServerOnly.ID)

	stream := &fakeStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "recon-host",
					CpuCores: 1, RamGb: 1, Os: "linux",
					RunningTasks: []*relayv1.RunningTask{
						{TaskId: matchIDStr, Epoch: int64(tMatchClaimed.AssignmentEpoch)},
						{TaskId: staleIDStr, Epoch: 999}, // stale
					},
				},
			},
		}},
		sentCh: make(chan struct{}, 1),
		hold:   make(chan struct{}),
	}

	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()

	// Wait for RegisterResponse to be sent.
	select {
	case <-stream.sentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("RegisterResponse never sent")
	}

	// Close stream.
	close(stream.hold)
	<-done

	// Assert RegisterResponse.cancel_task_ids contains tStale (stale) only.
	// tServerOnly is NOT in cancel — the agent wasn't told about it.
	require.Len(t, stream.sent, 1)
	resp := stream.sent[0].GetRegisterResponse()
	require.NotNil(t, resp)
	assert.ElementsMatch(t, []string{staleIDStr}, resp.CancelTaskIds)

	// Assert tServerOnly was requeued (agent didn't report it).
	fetchedServerOnly, err := q.GetTask(ctx, tServerOnly.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", fetchedServerOnly.Status)

	// Assert tMatch still in dispatched state.
	fetchedMatch, err := q.GetTask(ctx, tMatch.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", fetchedMatch.Status)

	// Assert tStale still in dispatched state (agent will abandon it; server
	// just signals via cancel_task_ids — doesn't requeue yet).
	fetchedStale, err := q.GetTask(ctx, tStale.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", fetchedStale.Status)

	_ = metadata.MD{}
	_ = strings.Contains
}

// newTestStoreWithPool is a helper; use the existing pattern from other tests.
// If not present, factor out of the existing newTestStore/newTestQueries
// helpers in this package.
func newTestStoreWithPool(t *testing.T) (*store.Queries, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return store.New(pool), pool
}

// uuidStr is needed in test package — copy the pattern from dispatch_test.go
// if not already present.
func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
```

Add imports as needed: `"fmt"`, `"github.com/jackc/pgx/v5/pgtype"`.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcilesRunningTasks -v -timeout 120s
```

Expected: FAIL — `NewHandlerWithGrace` does not exist; reconcile not implemented.

- [ ] **Step 3: Extend Handler with grace registry and reconcile logic**

Modify `internal/worker/handler.go`. Change `Handler` struct and constructors (lines 17-28):

```go
// Handler implements relayv1.AgentServiceServer.
type Handler struct {
	relayv1.UnimplementedAgentServiceServer
	q               *store.Queries
	registry        *Registry
	broker          *events.Broker
	triggerDispatch func()
	grace           *GraceRegistry
}

// NewHandler returns a Handler wired to the given dependencies, without a
// grace registry (tests that don't care about reconnect grace can use this).
func NewHandler(q *store.Queries, r *Registry, b *events.Broker, triggerDispatch func()) *Handler {
	return &Handler{q: q, registry: r, broker: b, triggerDispatch: triggerDispatch}
}

// NewHandlerWithGrace is like NewHandler but also wires in a GraceRegistry so
// that agent disconnects start a grace timer instead of immediately requeueing.
func NewHandlerWithGrace(q *store.Queries, r *Registry, b *events.Broker, triggerDispatch func(), g *GraceRegistry) *Handler {
	return &Handler{q: q, registry: r, broker: b, triggerDispatch: triggerDispatch, grace: g}
}
```

Replace `registerWorker` (lines 75-122) with:

```go
// registerWorker upserts the worker, marks it online, reconciles the agent's
// running-task report against DB state, sends the RegisterResponse, publishes
// an SSE event, and triggers the dispatch loop.
func (h *Handler) registerWorker(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	w, err := h.q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name:     reg.Hostname,
		Hostname: reg.Hostname,
		CpuCores: reg.CpuCores,
		RamGb:    reg.RamGb,
		GpuCount: reg.GpuCount,
		GpuModel: reg.GpuModel,
		Os:       reg.Os,
	})
	if err != nil {
		return "", nil, fmt.Errorf("upsert worker: %w", err)
	}

	w, err = h.q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         w.ID,
		Status:     "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return "", nil, fmt.Errorf("update worker status: %w", err)
	}

	workerID := uuidStr(w.ID)

	// Agent reconnected within its grace window — stop the requeue timer.
	if h.grace != nil {
		h.grace.Cancel(workerID)
	}

	// Reconcile the agent's running-task report against DB state.
	cancelIDs, err := h.reconcileRunningTasks(ctx, w.ID, reg.RunningTasks)
	if err != nil {
		return "", nil, fmt.Errorf("reconcile: %w", err)
	}

	// Send RegisterResponse on the raw stream. At this point the worker is not
	// yet in the registry, so no other goroutine can race us on stream.Send.
	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{
				WorkerId:      workerID,
				CancelTaskIds: cancelIDs,
			},
		},
	}); err != nil {
		return "", nil, fmt.Errorf("send register response: %w", err)
	}

	// From here on, all sends go through the serializing wrapper.
	sender := NewWorkerSender(stream)
	h.registry.Register(workerID, sender)

	h.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":"online"}`, workerID)),
	})

	go h.triggerDispatch()

	return workerID, sender, nil
}

// reconcileRunningTasks compares the agent's reported running tasks against
// the coordinator's DB state. Returns the list of task IDs the agent should
// cancel (stale epoch or unknown to coordinator). Any task the coordinator
// has assigned to this worker but the agent didn't report is requeued.
func (h *Handler) reconcileRunningTasks(ctx context.Context, workerID pgtype.UUID, reported []*relayv1.RunningTask) ([]string, error) {
	serverTasks, err := h.q.GetActiveTasksForWorker(ctx, workerID)
	if err != nil {
		return nil, err
	}

	serverSet := make(map[string]int64, len(serverTasks))
	for _, t := range serverTasks {
		serverSet[uuidStr(t.ID)] = int64(t.AssignmentEpoch)
	}

	var cancelIDs []string
	agentSet := make(map[string]bool, len(reported))
	for _, rt := range reported {
		agentSet[rt.TaskId] = true
		srvEpoch, ok := serverSet[rt.TaskId]
		if !ok || srvEpoch != rt.Epoch {
			cancelIDs = append(cancelIDs, rt.TaskId)
		}
	}

	// Anything server has but agent didn't report → requeue.
	for taskIDStr := range serverSet {
		if agentSet[taskIDStr] {
			continue
		}
		var tID pgtype.UUID
		if err := tID.Scan(taskIDStr); err != nil {
			continue
		}
		_ = h.q.RequeueTaskByID(ctx, tID)
	}

	return cancelIDs, nil
}
```

- [ ] **Step 4: Run the test**

```bash
go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_ReconcilesRunningTasks -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Run the full worker package**

```bash
go test ./internal/worker/... -v -timeout 30s
go test -tags integration -p 1 ./internal/worker/... -v -timeout 300s
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_test.go
git commit -m "feat(worker): reconcile agent running tasks on register"
```

---

### Task 10: Server startup grace seeding

**Files:**
- Modify: `cmd/relay-server/main.go:49-76` (pool setup, startup requeue, wiring)
- Test: new integration test (or extend existing startup test if present)

- [ ] **Step 1: Write the failing integration test**

Create `cmd/relay-server/startup_reconcile_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupPgForStartup(t *testing.T) (*store.Queries, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, store.Migrate("pgx5"+dsn[len("postgres"):]))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return store.New(pool), pool
}

func TestStartupReconcile_SeedsGraceTimersForActiveWorkers(t *testing.T) {
	ctx := context.Background()
	q, _ := setupPgForStartup(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "startup@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w", Hostname: "startup-h", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Command: []string{"true"},
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Run the startup reconcile logic (to be implemented).
	var fired []string
	grace := worker.NewGraceRegistry(30*time.Millisecond, func(workerID string) {
		fired = append(fired, workerID)
	})
	require.NoError(t, seedGraceTimersFromActiveTasks(ctx, q, grace))

	// The task is still "dispatched" — we did NOT blanket-requeue.
	fetched, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", fetched.Status)

	// After the grace window with no reconnect, the timer fires.
	time.Sleep(80 * time.Millisecond)
	grace.Stop()
	require.Len(t, fired, 1)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./cmd/relay-server/... -run TestStartupReconcile_SeedsGraceTimersForActiveWorkers -v -timeout 120s
```

Expected: FAIL — `seedGraceTimersFromActiveTasks` is undefined.

- [ ] **Step 3: Implement startup reconcile helper and wire into main**

Modify `cmd/relay-server/main.go`. Add imports for `strconv`, `relay/internal/worker` (already imported). Replace lines 49-76 (pool setup + RequeueAllActiveTasks call + existing wiring through httpServer construction) with:

```go
	dbMaxConns := 25
	if v := os.Getenv("RELAY_DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dbMaxConns = n
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = int32(dbMaxConns)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
		bootstrapPassword := os.Getenv("RELAY_BOOTSTRAP_PASSWORD")
		if bootstrapPassword == "" {
			log.Fatalf("RELAY_BOOTSTRAP_PASSWORD must be set when RELAY_BOOTSTRAP_ADMIN is set")
		}
		if err := bootstrapAdmin(ctx, q, bootstrapEmail, bootstrapPassword); err != nil {
			log.Fatalf("bootstrap admin: %v", err)
		}
	}

	broker := events.NewBroker()
	registry := worker.NewRegistry()
	dispatcher := scheduler.NewDispatcher(q, registry, broker)

	graceWindow := 2 * time.Minute
	if v := os.Getenv("RELAY_WORKER_GRACE_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			graceWindow = d
		}
	}
	grace := worker.NewGraceRegistry(graceWindow, func(workerID string) {
		var id pgtype.UUID
		if err := id.Scan(workerID); err != nil {
			return
		}
		_ = q.RequeueWorkerTasks(context.Background(), id)
		dispatcher.Trigger()
	})
	defer grace.Stop()

	// Seed grace timers for any workers with active tasks. If agents reconnect
	// within the window they reconcile normally; if not, their tasks requeue.
	if err := seedGraceTimersFromActiveTasks(ctx, q, grace); err != nil {
		log.Printf("warn: seed grace timers: %v", err)
	}

	agentHandler := worker.NewHandlerWithGrace(q, registry, broker, dispatcher.Trigger, grace)
	httpServer := api.New(pool, q, broker, registry, dispatcher.Trigger)
```

Add imports to `cmd/relay-server/main.go`: `"strconv"`, `"github.com/jackc/pgx/v5/pgtype"`.

Also add this function (below `main`):

```go
// seedGraceTimersFromActiveTasks enumerates workers that have non-terminal
// tasks in the DB at startup and starts a grace timer for each. Agents that
// reconnect within the window reconcile; agents that don't will have their
// tasks requeued.
func seedGraceTimersFromActiveTasks(ctx context.Context, q *store.Queries, grace *worker.GraceRegistry) error {
	workerIDs, err := q.ListWorkersWithActiveTasks(ctx)
	if err != nil {
		return err
	}
	for _, wID := range workerIDs {
		grace.Start(uuidStrMain(wID))
	}
	return nil
}

// uuidStrMain converts a pgtype.UUID to its canonical string representation.
// Named with Main suffix to avoid collision with any other helper in this file.
func uuidStrMain(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
```

Add imports `"fmt"` if not present.

**Remove** the old lines 57-60 that called `RequeueAllActiveTasks`.

- [ ] **Step 4: Run the startup test**

```bash
go test -tags integration -p 1 ./cmd/relay-server/... -run TestStartupReconcile_SeedsGraceTimersForActiveWorkers -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Build binary and run any existing cmd tests**

```bash
go build ./cmd/relay-server
go test ./cmd/relay-server/... -v -timeout 30s
go test -tags integration -p 1 ./cmd/relay-server/... -v -timeout 300s
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/relay-server/main.go cmd/relay-server/startup_reconcile_test.go
git commit -m "feat(cmd): seed grace timers at startup instead of blanket-requeue"
```

---

### Task 11: Agent disconnect uses grace timer

**Files:**
- Modify: `internal/worker/handler.go:31-71` (Connect method defer chain)
- Test: extend `internal/worker/handler_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/handler_test.go`:

```go
func TestConnect_DisconnectStartsGraceTimer(t *testing.T) {
	ctx := context.Background()
	q, _ := newTestStoreWithPool(t)
	broker := events.NewBroker()
	registry := worker.NewRegistry()

	var fired []string
	grace := worker.NewGraceRegistry(50*time.Millisecond, func(workerID string) {
		fired = append(fired, workerID)
	})
	defer grace.Stop()

	h := worker.NewHandlerWithGrace(q, registry, broker, func() {}, grace)

	stream := &fakeStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "grace-host", CpuCores: 1, RamGb: 1, Os: "linux",
				},
			},
		}},
		sentCh: make(chan struct{}, 1),
	}

	done := make(chan error, 1)
	go func() { done <- h.Connect(stream) }()

	select {
	case <-stream.sentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("RegisterResponse never sent")
	}
	<-done

	// Before the grace window elapses, no requeue.
	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, fired)

	// After the window elapses, the timer fires once for this worker.
	time.Sleep(60 * time.Millisecond)
	assert.Len(t, fired, 1)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/worker/... -run TestConnect_DisconnectStartsGraceTimer -v -timeout 120s
```

Expected: FAIL — current defer chain calls `requeueWorkerTasks` immediately; no grace timer is started.

- [ ] **Step 3: Modify `Connect` defer chain**

In `internal/worker/handler.go`, replace lines 49-52 (the four defers after `registerWorker` succeeds) with:

```go
	// Defer cleanup in reverse order:
	// 1. Unregister from the in-memory registry (stop new dispatches to this worker).
	// 2. Close the send goroutine.
	// 3. Mark offline in DB (SSE event).
	// 4. If a grace registry is wired, schedule a delayed requeue. Otherwise
	//    fall back to the old behavior of immediate requeue.
	if h.grace != nil {
		defer h.grace.Start(workerID)
	} else {
		defer h.requeueWorkerTasks(workerID)
	}
	defer h.markWorkerOffline(workerID)
	defer sender.Close()
	defer h.registry.Unregister(workerID)
```

- [ ] **Step 4: Run the test**

```bash
go test -tags integration -p 1 ./internal/worker/... -run TestConnect_DisconnectStartsGraceTimer -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Run the full worker test suite**

```bash
go test -tags integration -p 1 ./internal/worker/... -v -timeout 300s
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_test.go
git commit -m "feat(worker): schedule delayed requeue via grace timer on disconnect"
```

---

## Phase 3 — Agent-side reconnect behavior

### Task 12: Agent `runCtx` field; runners live for agent lifetime

**Files:**
- Modify: `internal/agent/agent.go:17-68, 72-177, 179-192` (Agent struct, Run, connect, handleDispatch)
- Modify: `internal/agent/runner.go:16-22, 79-86, 154-161` (Runner.ctx semantics, doc)
- Test: new `internal/agent/lifetime_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/lifetime_test.go`:

```go
package agent

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
)

// Verifies that cancelling the connection-scoped context passed into
// handleDispatch does NOT kill the runner. The runner should continue until
// the agent's long-lived context (runCtx) is cancelled.
func TestRunnerSurvivesConnectionContextCancellation(t *testing.T) {
	// Long-lived agent context.
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	// Short-lived connection context.
	connCtx, cancelConn := context.WithCancel(runCtx)

	sendCh := make(chan *relayv1.AgentMessage, 8)
	// newRunner uses `parent` as its long-lived ctx; handleDispatch must pass
	// runCtx here, NOT connCtx.
	runner, rCtx := newRunner("task-lifetime", 1, sendCh, runCtx, 0)

	// Kick off a long-running process.
	go runner.Run(rCtx, &relayv1.DispatchTask{
		TaskId: "task-lifetime", Command: []string{"sleep", "2"},
	})

	// Wait briefly, then cancel the connection context.
	time.Sleep(100 * time.Millisecond)
	cancelConn()

	// Runner should NOT have exited. Check that sendCh hasn't received a
	// final-status message in the next 100ms.
	sawFinal := false
	deadline := time.After(100 * time.Millisecond)
loop:
	for {
		select {
		case m := <-sendCh:
			if ts := m.GetTaskStatus(); ts != nil &&
				(ts.Status == relayv1.TaskStatus_TASK_STATUS_DONE ||
					ts.Status == relayv1.TaskStatus_TASK_STATUS_FAILED ||
					ts.Status == relayv1.TaskStatus_TASK_STATUS_TIMED_OUT) {
				sawFinal = true
			}
		case <-deadline:
			break loop
		}
	}
	assert.False(t, sawFinal, "runner should still be running after connCtx cancel")

	// Cleanup.
	cancelRun()
	_ = connCtx
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/agent/ -run TestRunnerSurvivesConnectionContextCancellation -v -timeout 30s
```

Expected: this test would PASS with the old code when `connCtx == runCtx` is passed as `parent` — because it IS passed `runCtx` directly in the test. We need to test against the actual `handleDispatch` path.

Replace the test with one that exercises `handleDispatch`:

```go
package agent

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
)

func TestAgentRunnerSurvivesConnectionContextCancellation(t *testing.T) {
	a := NewAgent("nowhere:0", Capabilities{
		Hostname: "test", CPUCores: 1, RAMGB: 1, OS: "linux",
	}, "", func(string) error { return nil })

	// Simulate entering Run: set the long-lived runCtx.
	runCtx, cancelRun := context.WithCancel(context.Background())
	a.runCtx = runCtx
	defer cancelRun()

	// Simulate the recv loop handling a dispatch with a connection-scoped ctx.
	connCtx, cancelConn := context.WithCancel(runCtx)
	a.handleDispatch(connCtx, &relayv1.DispatchTask{
		TaskId:  "long-task",
		Command: []string{"sleep", "2"},
		Epoch:   1,
	})

	// Wait for the runner to start.
	time.Sleep(100 * time.Millisecond)
	a.mu.Lock()
	r, ok := a.runners["long-task"]
	a.mu.Unlock()
	if !ok {
		t.Fatal("runner was not registered")
	}

	// Cancel connCtx (simulates stream drop). Runner should NOT exit.
	cancelConn()
	time.Sleep(150 * time.Millisecond)

	// Check that cancelled flag is NOT set (runner is still running).
	assert.False(t, r.cancelled.Load(), "runner should still be alive after conn drop")

	// Clean shutdown.
	cancelRun()
	a.runnerWG.Wait()
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/agent/ -run TestAgentRunnerSurvivesConnectionContextCancellation -v -timeout 30s
```

Expected: FAIL — `a.runCtx` field does not exist; `handleDispatch` passes `connCtx` to `newRunner` as parent, so connCtx cancellation propagates.

- [ ] **Step 4: Add `runCtx` field to Agent and update `Run`**

Modify `internal/agent/agent.go` lines 17-28:

```go
// Agent manages the gRPC connection to the coordinator, dispatches tasks to
// Runners, and reconnects automatically on stream failure.
type Agent struct {
	coord    string
	caps     Capabilities
	workerID string // only accessed from the single reconnect goroutine in Run; no mutex needed
	sendCh   chan *relayv1.AgentMessage // buffered 64; shared across reconnects
	runCtx   context.Context            // long-lived parent; set in Run, lives across reconnects
	mu       sync.Mutex
	runners  map[string]*Runner
	runnerWG sync.WaitGroup // tracks active runner goroutines; waited on agent shutdown
	saveID   func(string) error
}
```

Modify `Run` (lines 44-68) to set `a.runCtx`:

```go
// Run connects to the coordinator and reconnects with exponential backoff until
// ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	a.runCtx = ctx
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			// Wait for any still-running runners to finish before we return.
			a.runnerWG.Wait()
			return
		}
		if err := a.connect(ctx); err != nil {
			if ctx.Err() != nil {
				a.runnerWG.Wait()
				return
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				a.runnerWG.Wait()
				return
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		backoff = time.Second
	}
}
```

- [ ] **Step 5: Update `handleDispatch` to use `a.runCtx`**

Modify `internal/agent/agent.go` line ~179:

```go
func (a *Agent) handleDispatch(connCtx context.Context, task *relayv1.DispatchTask) {
	// Runners bind to the long-lived runCtx, NOT the connection ctx. This is
	// what lets subprocesses survive brief disconnects.
	runner, runCtx := newRunner(task.TaskId, task.Epoch, a.sendCh, a.runCtx, task.TimeoutSeconds)
	a.mu.Lock()
	a.runners[task.TaskId] = runner
	a.mu.Unlock()
	a.runnerWG.Add(1)
	go func() {
		defer a.runnerWG.Done()
		runner.Run(runCtx, task)
		a.mu.Lock()
		delete(a.runners, task.TaskId)
		a.mu.Unlock()
	}()
	_ = connCtx // connCtx is no longer used by the runner
}
```

- [ ] **Step 6: Remove the recv-loop runner cancellation**

Modify `internal/agent/agent.go` lines 150-168. Replace the recv loop with:

```go
	// Recv loop.
	for {
		msg, err := stream.Recv()
		if err != nil {
			// Stream dropped. Runners survive (they bind to runCtx, not connCtx).
			// Coordinator will start a grace timer; reconnect will reconcile.
			connCancel()
			return err
		}

		switch p := msg.Payload.(type) {
		case *relayv1.CoordinatorMessage_DispatchTask:
			a.handleDispatch(connCtx, p.DispatchTask)
		case *relayv1.CoordinatorMessage_CancelTask:
			a.handleCancel(p.CancelTask)
		}
	}
```

- [ ] **Step 7: Run the test**

```bash
go test ./internal/agent/ -run TestAgentRunnerSurvivesConnectionContextCancellation -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 8: Run the full agent test suite**

```bash
go test ./internal/agent/... -v -timeout 60s
```

Expected: all pass. Tests that relied on "runner cancelled on stream drop" will need updating; the new behavior is that runners persist.

- [ ] **Step 9: Commit**

```bash
git add internal/agent/agent.go internal/agent/runner.go internal/agent/lifetime_test.go
git commit -m "feat(agent): bind runner lifetime to agent context, not connection"
```

---

### Task 13: Agent sends `running_tasks` in `RegisterRequest`

**Files:**
- Modify: `internal/agent/agent.go:99-115` (RegisterRequest construction)
- Test: extend `internal/agent/lifetime_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/lifetime_test.go`:

```go
func TestAgent_BuildRegisterRequest_IncludesRunningTasks(t *testing.T) {
	a := NewAgent("nowhere:0", Capabilities{
		Hostname: "test", CPUCores: 1, RAMGB: 1, OS: "linux",
	}, "worker-xyz", func(string) error { return nil })

	// Simulate two active runners.
	a.runners["task-1"] = &Runner{taskID: "task-1", epoch: 3}
	a.runners["task-2"] = &Runner{taskID: "task-2", epoch: 7}

	req := a.buildRegisterRequest()
	assert.Equal(t, "worker-xyz", req.WorkerId)
	assert.Len(t, req.RunningTasks, 2)

	byID := map[string]int64{}
	for _, rt := range req.RunningTasks {
		byID[rt.TaskId] = rt.Epoch
	}
	assert.Equal(t, int64(3), byID["task-1"])
	assert.Equal(t, int64(7), byID["task-2"])
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/agent/ -run TestAgent_BuildRegisterRequest_IncludesRunningTasks -v -timeout 30s
```

Expected: FAIL — `buildRegisterRequest` does not exist.

- [ ] **Step 3: Extract and update the RegisterRequest construction**

In `internal/agent/agent.go`, add a new method (anywhere in the file):

```go
// buildRegisterRequest constructs the RegisterRequest sent on (re)connect.
// Includes the caller's capabilities AND the list of currently-executing
// tasks with their epochs, so the coordinator can reconcile.
func (a *Agent) buildRegisterRequest() *relayv1.RegisterRequest {
	a.mu.Lock()
	running := make([]*relayv1.RunningTask, 0, len(a.runners))
	for _, r := range a.runners {
		running = append(running, &relayv1.RunningTask{
			TaskId: r.taskID,
			Epoch:  r.epoch,
		})
	}
	a.mu.Unlock()

	return &relayv1.RegisterRequest{
		WorkerId:     a.workerID,
		Hostname:     a.caps.Hostname,
		CpuCores:     a.caps.CPUCores,
		RamGb:        a.caps.RAMGB,
		GpuCount:     a.caps.GPUCount,
		GpuModel:     a.caps.GPUModel,
		Os:           a.caps.OS,
		RunningTasks: running,
	}
}
```

Replace the RegisterRequest construction in `connect` (lines ~99-115):

```go
	// Send RegisterRequest.
	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: a.buildRegisterRequest(),
		},
	}); err != nil {
		return err
	}
```

- [ ] **Step 4: Run the test**

```bash
go test ./internal/agent/ -run TestAgent_BuildRegisterRequest_IncludesRunningTasks -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/agent/lifetime_test.go
git commit -m "feat(agent): include running_tasks in RegisterRequest"
```

---

### Task 14: Agent handles `cancel_task_ids` via `Abandon`

**Files:**
- Modify: `internal/agent/runner.go:16-22, 38-42, 143-152` (Runner struct, Abandon, sendFinalStatus)
- Modify: `internal/agent/agent.go:117-132` (RegisterResponse handling)
- Test: extend `internal/agent/runner_epoch_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/runner_epoch_test.go`:

```go
func TestRunnerAbandon_SuppressesFinalStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendCh := make(chan *relayv1.AgentMessage, 8)
	runner, runCtx := newRunner("task-abandon", 1, sendCh, ctx, 0)

	// Start a subprocess that would normally report DONE.
	done := make(chan struct{})
	go func() {
		runner.Run(runCtx, &relayv1.DispatchTask{
			TaskId: "task-abandon", Command: []string{"sleep", "1"},
		})
		close(done)
	}()

	// Give it time to start, then abandon.
	time.Sleep(100 * time.Millisecond)
	runner.Abandon()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not exit after Abandon")
	}

	// Collect all messages on sendCh.
	var msgs []*relayv1.AgentMessage
	for {
		select {
		case m := <-sendCh:
			msgs = append(msgs, m)
		default:
			goto check
		}
	}
check:
	// Should see the TASK_STATUS_RUNNING (start) but NOT any final status.
	sawFinal := false
	for _, m := range msgs {
		if ts := m.GetTaskStatus(); ts != nil {
			if ts.Status == relayv1.TaskStatus_TASK_STATUS_DONE ||
				ts.Status == relayv1.TaskStatus_TASK_STATUS_FAILED ||
				ts.Status == relayv1.TaskStatus_TASK_STATUS_TIMED_OUT {
				sawFinal = true
			}
		}
	}
	assert.False(t, sawFinal, "Abandon must suppress final status message")
}
```

Add `"time"` import.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/agent/ -run TestRunnerAbandon_SuppressesFinalStatus -v -timeout 30s
```

Expected: FAIL — `Runner.Abandon` is undefined.

- [ ] **Step 3: Add `abandoned` field and `Abandon` method**

Modify `internal/agent/runner.go` line 15-22 (Runner struct):

```go
type Runner struct {
	taskID    string
	epoch     int64
	sendCh    chan *relayv1.AgentMessage
	ctx       context.Context // agent-lifetime context — cancelled only on agent shutdown
	cancel    context.CancelFunc
	cancelled atomic.Bool
	abandoned atomic.Bool
}
```

Add below `Cancel`:

```go
// Abandon is like Cancel but suppresses the final status message. Used when
// the coordinator's RegisterResponse.cancel_task_ids indicates this task was
// reassigned to another worker during a grace-expiry requeue.
func (r *Runner) Abandon() {
	r.abandoned.Store(true)
	r.cancel()
}
```

Modify `sendFinalStatus` (line ~143):

```go
func (r *Runner) sendFinalStatus(status relayv1.TaskStatus, exitCode *int32) {
	if r.abandoned.Load() {
		return // coordinator reassigned this task; suppress final status
	}
	upd := &relayv1.TaskStatusUpdate{
		TaskId:   r.taskID,
		Status:   status,
		ExitCode: exitCode,
		Epoch:    r.epoch,
	}
	r.send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{TaskStatus: upd},
	})
}
```

- [ ] **Step 4: Handle `cancel_task_ids` in agent connect**

Modify `internal/agent/agent.go` lines ~117-132 (the RegisterResponse handling). Replace with:

```go
	// First response must be RegisterResponse.
	resp, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := resp.GetRegisterResponse()
	if reg == nil {
		return fmt.Errorf("agent: expected RegisterResponse, got %T", resp.Payload)
	}
	if reg.WorkerId != a.workerID {
		a.workerID = reg.WorkerId
		if err := a.saveID(a.workerID); err != nil {
			fmt.Fprintf(os.Stderr, "relay-agent: warning: failed to persist worker ID: %v\n", err)
		}
	}

	// Coordinator may tell us to abandon some tasks (reassigned during grace
	// expiry, or unknown to the coordinator). Abandon them silently.
	for _, tid := range reg.CancelTaskIds {
		a.mu.Lock()
		r, ok := a.runners[tid]
		a.mu.Unlock()
		if ok {
			r.Abandon()
		}
	}

	log.Printf("connected to coordinator %s (worker ID: %s)", a.coord, a.workerID)
```

- [ ] **Step 5: Run the test**

```bash
go test ./internal/agent/ -run TestRunnerAbandon_SuppressesFinalStatus -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 6: Run full agent tests**

```bash
go test ./internal/agent/... -v -timeout 60s
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/runner.go internal/agent/agent.go internal/agent/runner_epoch_test.go
git commit -m "feat(agent): add Runner.Abandon and handle cancel_task_ids on reconnect"
```

---

### Task 15: Remove `sendCh` drain and `runnerWG.Wait` on reconnect

**Files:**
- Modify: `internal/agent/agent.go:72-87` (connect prelude)
- Test: rely on existing tests from Task 12 + 13

- [ ] **Step 1: Identify the code to remove**

In `internal/agent/agent.go` lines 76-87, the connect prelude currently waits for prior runners and drains the channel:

```go
	// Wait for all runner goroutines from the previous connection to finish before
	// draining sendCh. Runners were cancelled when the previous stream closed;
	// waiting here ensures no runner is still writing to sendCh during the drain.
	a.runnerWG.Wait()

	// Discard any messages queued from the previous connection. They were never
	// sent on the old stream and sending them on a new stream would confuse the
	// coordinator with stale task IDs.
	for len(a.sendCh) > 0 {
		<-a.sendCh
	}
```

With runner lifetime now agent-scoped (Task 12), runners from the previous connection are still alive and writing to sendCh. Waiting or draining would either hang or throw away legitimate messages. Remove both.

- [ ] **Step 2: Delete the block**

Replace lines 72-87 of `connect` with:

```go
// connect dials the coordinator, registers, and runs the recv loop until the
// stream closes or ctx is cancelled. Runners from any previous connection
// continue in the background; their buffered messages in sendCh flush on the
// new stream. Stale-epoch messages are dropped at the DB layer on the
// coordinator side.
func (a *Agent) connect(ctx context.Context) error {
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	conn, err := grpc.NewClient(a.coord, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
```

- [ ] **Step 3: Build and run the agent tests**

```bash
go build ./...
go test ./internal/agent/... -v -timeout 60s
```

Expected: all pass. The previously-written `TestAgentRunnerSurvivesConnectionContextCancellation` continues to prove runners survive.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/agent.go
git commit -m "refactor(agent): remove sendCh drain and runnerWG.Wait on reconnect"
```

---

## Phase 4 — LISTEN/NOTIFY

### Task 16: `NotifyListener` implementation

**Files:**
- Create: `internal/scheduler/notify.go`
- Create: `internal/scheduler/notify_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/scheduler/notify_test.go`:

```go
//go:build integration

package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"relay/internal/scheduler"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyListener_TriggersOnNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Reuse the existing dispatch test helpers to spin up Postgres.
	q := newTestStore(t)
	_ = q // not used directly; pool is the thing we need

	// Grab the pool via a reflection-free helper. We need to refactor
	// newTestStore to also return *pgxpool.Pool, or add a sibling helper.
	pool := newTestPoolFromQueries(t)

	var triggered atomic.Int32
	l := scheduler.NewNotifyListener(pool, func() {
		triggered.Add(1)
	})

	go l.Run(ctx)

	// Give LISTEN time to attach.
	time.Sleep(200 * time.Millisecond)

	// Fire a NOTIFY directly.
	_, err := pool.Exec(ctx, "SELECT pg_notify('relay_task_submitted', '')")
	require.NoError(t, err)

	// Should trigger within 500ms.
	require.Eventually(t, func() bool {
		return triggered.Load() >= 1
	}, 2*time.Second, 20*time.Millisecond)

	// Fire the other channel.
	_, err = pool.Exec(ctx, "SELECT pg_notify('relay_task_completed', '')")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return triggered.Load() >= 2
	}, 2*time.Second, 20*time.Millisecond)

	// Unrelated channel should be ignored.
	before := triggered.Load()
	_, err = pool.Exec(ctx, "SELECT pg_notify('some_other_channel', '')")
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, before, triggered.Load())
}
```

You'll need a `newTestPoolFromQueries` helper (or refactor `newTestStore` to return `(q, pool)`). Refactor: update the top of `dispatch_test.go`:

```go
func newTestStoreWithPool(t *testing.T) (*store.Queries, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, store.Migrate("pgx5"+dsn[len("postgres"):]))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return store.New(pool), pool
}

// newTestStore is kept for backward compatibility; discards the pool.
func newTestStore(t *testing.T) *store.Queries {
	q, _ := newTestStoreWithPool(t)
	return q
}

func newTestPoolFromQueries(t *testing.T) *pgxpool.Pool {
	_, pool := newTestStoreWithPool(t)
	return pool
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -tags integration -p 1 ./internal/scheduler/ -run TestNotifyListener_TriggersOnNotify -v -timeout 120s
```

Expected: FAIL — `scheduler.NewNotifyListener` is undefined.

- [ ] **Step 3: Implement the listener**

Create `internal/scheduler/notify.go`:

```go
package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NotifyListener subscribes to Postgres NOTIFY on the relay dispatch channels
// and invokes trigger() on each notification. It holds one dedicated pool
// connection for the duration of each listen session; on error it releases
// and reconnects with exponential backoff.
type NotifyListener struct {
	pool    *pgxpool.Pool
	trigger func()
}

// NewNotifyListener constructs a listener that calls trigger() on every
// notification from any of the relay dispatch channels.
func NewNotifyListener(pool *pgxpool.Pool, trigger func()) *NotifyListener {
	return &NotifyListener{pool: pool, trigger: trigger}
}

// Run blocks until ctx is cancelled. It holds a dedicated connection from
// the pool and loops on WaitForNotification.
func (n *NotifyListener) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := n.session(ctx); err != nil && ctx.Err() == nil {
			log.Printf("notify listener: %v (backoff %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		backoff = time.Second
	}
}

// session acquires a connection, LISTENs, and loops on WaitForNotification
// until an error occurs or ctx is cancelled.
func (n *NotifyListener) session(ctx context.Context) error {
	conn, err := n.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	raw := conn.Conn()
	if _, err := raw.Exec(ctx, "LISTEN relay_task_submitted"); err != nil {
		return err
	}
	if _, err := raw.Exec(ctx, "LISTEN relay_task_completed"); err != nil {
		return err
	}

	for {
		_, err := raw.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		n.trigger()
	}
}
```

- [ ] **Step 4: Run the test**

```bash
go test -tags integration -p 1 ./internal/scheduler/ -run TestNotifyListener_TriggersOnNotify -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/notify.go internal/scheduler/notify_test.go internal/scheduler/dispatch_test.go
git commit -m "feat(scheduler): add NotifyListener that wakes dispatcher on pg_notify"
```

---

### Task 17: NOTIFY firing + wire listener into main + relax polling to 30s

**Files:**
- Modify: `internal/store/query/tasks.sql` (add notify helpers)
- Regenerate: `internal/store/tasks.sql.go`
- Modify: `internal/api/jobs.go` (fire NOTIFY after task insert; remove direct trigger)
- Modify: `internal/worker/handler.go` (fire NOTIFY on terminal status; remove direct trigger)
- Modify: `internal/scheduler/dispatch.go:43-58` (polling 5s → 30s)
- Modify: `cmd/relay-server/main.go` (start NotifyListener)

- [ ] **Step 1: Add notify helper queries**

Append to `internal/store/query/tasks.sql`:

```sql
-- name: NotifyTaskSubmitted :exec
-- Wakes any LISTENers on relay_task_submitted. Payload is empty; listeners
-- coalesce into a single dispatch trigger.
SELECT pg_notify('relay_task_submitted', '');

-- name: NotifyTaskCompleted :exec
-- Wakes any LISTENers on relay_task_completed.
SELECT pg_notify('relay_task_completed', '');
```

- [ ] **Step 2: Regenerate sqlc**

```bash
make generate
```

- [ ] **Step 3: Find the task-insert site in the API**

```bash
grep -rn "CreateTask\|InsertTask" internal/api/
```

Identify the file that calls `q.CreateTask` when a job is submitted (likely `internal/api/jobs.go`).

- [ ] **Step 4: Fire NOTIFY after task insert**

In the job-submission handler, after each successful `q.CreateTask(...)` (there may be one call per task in the DAG), add:

```go
_ = q.NotifyTaskSubmitted(ctx)
```

Then **remove** the in-process `dispatcher.Trigger()` call at the end of the handler (it's replaced by NOTIFY).

*(Exact line numbers depend on the current state of the file — open it and find where `CreateTask` is called.)*

- [ ] **Step 5: Fire NOTIFY on task completion in handler**

In `internal/worker/handler.go` `handleTaskStatus` (near the bottom, where `go h.triggerDispatch()` is currently called on `statusStr == "done"`):

```go
	if statusStr == "done" {
		_ = h.q.NotifyTaskCompleted(ctx)
	}
```

Remove the old `go h.triggerDispatch()` line that was there. The worker-online trigger at the bottom of `registerWorker` stays as a direct `dispatcher.Trigger()`.

Also update the retry path (at ~line 155) to fire NOTIFY since a retry puts the task back to pending:

```go
	if terminal && task.RetryCount < task.Retries {
		if _, err := h.q.IncrementTaskRetryCount(ctx, taskID); err == nil {
			updateJobStatusFromTasks(ctx, h.q, task.JobID)
			_ = h.q.NotifyTaskSubmitted(ctx)
		}
		return
	}
```

(Remove the old `go h.triggerDispatch()` that followed `IncrementTaskRetryCount`.)

- [ ] **Step 6: Relax polling to 30s**

Modify `internal/scheduler/dispatch.go` line 45:

```go
// Run blocks until ctx is cancelled; fires on Trigger(), on NOTIFY (via
// NotifyListener), or every 30s as a safety-net poll.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
```

- [ ] **Step 7: Wire `NotifyListener` into `main.go`**

Add after dispatcher construction in `cmd/relay-server/main.go`:

```go
	notifyListener := scheduler.NewNotifyListener(pool, dispatcher.Trigger)
	go notifyListener.Run(ctx)
```

- [ ] **Step 8: Build and run tests**

```bash
go build ./...
go test ./... -v -timeout 60s
go test -tags integration -p 1 ./... -v -timeout 600s
```

Expected: all pass. Any existing test that relied on the 5s poll granularity must either trigger dispatch explicitly via `d.RunOnce(ctx)` or `d.Trigger()`.

- [ ] **Step 9: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/api/jobs.go internal/worker/handler.go \
        internal/scheduler/dispatch.go cmd/relay-server/main.go
git commit -m "feat(scheduler): wire LISTEN/NOTIFY into dispatch path; relax poll to 30s"
```

---

## Phase 5 — N×M query fix & pool sizing

### Task 18: N×M query fix

**Files:**
- Modify: `internal/store/query/tasks.sql` (new aggregate query; retire CountActiveTasksForWorker)
- Regenerate: `internal/store/tasks.sql.go`
- Modify: `internal/scheduler/dispatch.go:65-134` (dispatch, selectWorker, sendTask)
- Modify: `internal/scheduler/dispatch_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/scheduler/dispatch_test.go`:

```go
func TestDispatcher_UsesAggregateCountQuery(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "aggr@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)

	// Create two workers, each with max_slots=2.
	w1, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "w1", Hostname: "aggr-1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	_, err = q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: w1.ID, Status: "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Set max_slots = 2 via direct SQL (CreateWorker defaults to 1).
	_, err = q.(*store.Queries) /* no-op cast */, nil
	// Use a direct pool exec if needed; otherwise rely on default max_slots=1
	// and exercise the in-cycle increment via 3 tasks, 1 worker.

	// Create 3 tasks.
	var tasks []store.Task
	for i := 0; i < 3; i++ {
		tk, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: job.ID, Name: fmt.Sprintf("t%d", i),
			Command: []string{"true"}, Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		tasks = append(tasks, tk)
	}

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w1.ID), sender)
	broker := events.NewBroker()
	d := scheduler.NewDispatcher(q, registry, broker)
	d.RunOnce(ctx)

	// With max_slots=1, only the first task should be dispatched this cycle
	// (the in-cycle count increment prevents double-dispatch even without
	// re-querying the DB).
	var dispatchedCount int
	for _, tk := range tasks {
		fetched, err := q.GetTask(ctx, tk.ID)
		require.NoError(t, err)
		if fetched.Status == "dispatched" {
			dispatchedCount++
		}
	}
	assert.Equal(t, 1, dispatchedCount,
		"with max_slots=1 and one worker, only one task should be dispatched per cycle")
	assert.Equal(t, 1, len(sender.sent))
}
```

Replace the placeholder broken cast lines with nothing — the test uses default `max_slots=1` so the aggregate-count + in-cycle-increment logic is what caps dispatch at 1.

- [ ] **Step 2: Run test to verify it fails or passes**

```bash
go test -tags integration -p 1 ./internal/scheduler/ -run TestDispatcher_UsesAggregateCountQuery -v -timeout 120s
```

If it already passes under the current code (per-iteration `CountActiveTasksForWorker` also enforces max_slots), the test still serves to lock in the behavior after refactoring. Continue.

- [ ] **Step 3: Add the aggregate query**

Append to `internal/store/query/tasks.sql`:

```sql
-- name: CountActiveTasksByAllWorkers :many
-- Per-worker count of non-terminal tasks. Used by the dispatcher to compute
-- available slots in one query rather than N per cycle.
SELECT worker_id, count(*)::bigint AS active
FROM tasks
WHERE worker_id IS NOT NULL
  AND status IN ('dispatched', 'running')
GROUP BY worker_id;
```

- [ ] **Step 4: Regenerate sqlc**

```bash
make generate
```

- [ ] **Step 5: Refactor `dispatch.go`**

Replace `internal/scheduler/dispatch.go` lines 65-134 with:

```go
func (d *Dispatcher) dispatch(ctx context.Context) {
	tasks, err := d.q.GetEligibleTasks(ctx)
	if err != nil || len(tasks) == 0 {
		return
	}

	workers, err := d.q.ListWorkers(ctx)
	if err != nil {
		return
	}

	reservations, err := d.q.ListActiveReservations(ctx)
	if err != nil {
		return
	}

	counts, err := d.q.CountActiveTasksByAllWorkers(ctx)
	if err != nil {
		return
	}
	activeByWorker := make(map[pgtype.UUID]int64, len(counts))
	for _, c := range counts {
		activeByWorker[c.WorkerID] = c.Active
	}

	for _, task := range tasks {
		w := d.selectWorker(task, workers, reservations, activeByWorker)
		if w != nil {
			if d.sendTask(ctx, task, *w) {
				activeByWorker[w.ID]++
			}
		}
	}
}

func (d *Dispatcher) selectWorker(
	task store.Task,
	workers []store.Worker,
	reservations []store.Reservation,
	activeByWorker map[pgtype.UUID]int64,
) *store.Worker {
	reservedIDs := make(map[string]bool)
	for _, res := range reservations {
		for _, wid := range res.WorkerIds {
			reservedIDs[uuidStr(wid)] = true
		}
	}

	var best *store.Worker
	var bestFree int64 = -1

	for i := range workers {
		w := &workers[i]
		if w.Status != "online" {
			continue
		}
		if reservedIDs[uuidStr(w.ID)] {
			continue
		}
		ok, err := LabelMatch(task.Requires, w.Labels)
		if err != nil || !ok {
			continue
		}
		active := activeByWorker[w.ID]
		free := int64(w.MaxSlots) - active
		if free <= 0 {
			continue
		}
		if free > bestFree {
			bestFree = free
			best = w
		}
	}

	return best
}

// sendTask claims the task and dispatches it. Returns true if the dispatch
// succeeded so the caller can update its in-cycle active-count map.
func (d *Dispatcher) sendTask(ctx context.Context, task store.Task, w store.Worker) bool {
	var env map[string]string
	if len(task.Env) > 0 {
		if err := json.Unmarshal(task.Env, &env); err != nil {
			env = nil
		}
	}

	var timeoutSecs int32
	if task.TimeoutSeconds != nil {
		timeoutSecs = *task.TimeoutSeconds
	}

	claimed, err := d.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID:       task.ID,
		WorkerID: w.ID,
	})
	if err != nil {
		return false
	}

	msg := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{
				TaskId:         uuidStr(claimed.ID),
				JobId:          uuidStr(claimed.JobID),
				Command:        claimed.Command,
				Env:            env,
				TimeoutSeconds: timeoutSecs,
				Epoch:          int64(claimed.AssignmentEpoch),
			},
		},
	}

	if err := d.registry.Send(uuidStr(w.ID), msg); err != nil {
		_ = d.q.RequeueTask(ctx, claimed.ID)
		return false
	}

	d.broker.Publish(events.Event{
		Type:  "task",
		JobID: uuidStr(claimed.JobID),
		Data:  []byte(fmt.Sprintf(`{"id":%q,"status":"dispatched","worker_id":%q}`, uuidStr(claimed.ID), uuidStr(w.ID))),
	})
	return true
}
```

- [ ] **Step 6: Run dispatch tests**

```bash
go test -tags integration -p 1 ./internal/scheduler/... -v -timeout 300s
```

Expected: all pass. Existing tests that call `selectWorker` directly (if any) will need the new signature.

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go \
        internal/scheduler/dispatch.go internal/scheduler/dispatch_test.go
git commit -m "perf(scheduler): replace N×M CountActiveTasksForWorker with aggregate query"
```

---

### Task 19: `pgxpool.MaxConns` via `RELAY_DB_MAX_CONNS`

**Files:**
- Already modified in Task 10 (`cmd/relay-server/main.go`). If Task 10 skipped this (or if this plan is being executed out-of-order), apply it now.

- [ ] **Step 1: Verify current state**

```bash
grep -n "RELAY_DB_MAX_CONNS\|MaxConns" cmd/relay-server/main.go
```

If you see `RELAY_DB_MAX_CONNS` referenced and `cfg.MaxConns` being set, this task is already done as part of Task 10. Skip to Step 4.

- [ ] **Step 2: If not present, apply the pool sizing change**

Replace the `pool, err := pgxpool.New(ctx, dsn)` block in `cmd/relay-server/main.go` with:

```go
	dbMaxConns := 25
	if v := os.Getenv("RELAY_DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dbMaxConns = n
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = int32(dbMaxConns)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
```

Add `"strconv"` to imports.

- [ ] **Step 3: Run a quick smoke check**

```bash
go build ./cmd/relay-server
RELAY_DB_MAX_CONNS=10 go test -tags integration -p 1 ./cmd/relay-server/... -v -timeout 60s
```

Expected: binary builds; any existing cmd tests still pass.

- [ ] **Step 4: Commit if changes were made**

```bash
git add cmd/relay-server/main.go
git commit -m "feat(cmd): make pgxpool MaxConns configurable via RELAY_DB_MAX_CONNS"
```

---

## Final verification

- [ ] **Step 1: Run the full test suite**

```bash
go build ./...
go test ./... -v -timeout 60s
go test -tags integration -p 1 ./... -v -timeout 900s
```

Expected: all pass.

- [ ] **Step 2: Run with the race detector on unit tests**

```bash
go test -race ./... -v -timeout 120s
```

Expected: no data races.

- [ ] **Step 3: Smoke test the server**

```bash
RELAY_DATABASE_URL=postgres://relay:relay@localhost:5432/relay?sslmode=disable \
RELAY_WORKER_GRACE_WINDOW=30s \
RELAY_DB_MAX_CONNS=20 \
./bin/relay-server
```

Expected: server starts, logs show grace window and pool sizing honored.

- [ ] **Step 4: Final commit if any cleanup is needed**

Rare — most changes should be committed per-task. If any files need formatting:

```bash
go fmt ./...
git status
git add -A && git commit -m "style: go fmt"
```

---
