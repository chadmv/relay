//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"relay/internal/store"
)

func makeTestUser(t *testing.T, q *store.Queries, ctx context.Context, name, email string) store.User {
	t.Helper()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: false, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user
}

func TestCreateAndGetJob(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Alice", "alice@example.com")

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:           "test-job",
		Priority:       "normal",
		SubmittedBy:    user.ID,
		Labels:         []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	assert.Equal(t, "test-job", job.Name)
	assert.Equal(t, "pending", job.Status)

	fetched, err := q.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, job.ID, fetched.ID)
}

func TestCreateAndGetWorker(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	worker, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name:     "node-1",
		Hostname: "render-01",
		CpuCores: 16,
		RamGb:    64,
		GpuCount: 2,
		GpuModel: "RTX 4090",
		Os:       "linux",
	})
	require.NoError(t, err)
	assert.Equal(t, "render-01", worker.Hostname)
	assert.Equal(t, int32(1), worker.MaxSlots)
	assert.Equal(t, "offline", worker.Status)

	fetched, err := q.GetWorkerByHostname(ctx, "render-01")
	require.NoError(t, err)
	assert.Equal(t, worker.ID, fetched.ID)
}

func TestTaskDependencyAndEligibility(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Bob", "bob@example.com")

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "dag-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	taskA, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "task-a",
		Commands: []byte(`[["echo","a"]]`),
		Env:      []byte(`{}`),
		Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "task-b",
		Commands: []byte(`[["echo","b"]]`),
		Env:      []byte(`{}`),
		Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// B depends on A
	err = q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: taskB.ID, DependsOnTaskID: taskA.ID,
	})
	require.NoError(t, err)

	// Only A should be eligible (B has unfinished dependency)
	eligible, err := q.GetEligibleTasks(ctx)
	require.NoError(t, err)
	require.Len(t, eligible, 1)
	assert.Equal(t, taskA.ID, eligible[0].ID)

	// Mark A done — WorkerID, StartedAt, FinishedAt are zero-value pgtype structs (Valid: false)
	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:     taskA.ID,
		Status: "done",
	})
	require.NoError(t, err)

	// Now B should be eligible
	eligible, err = q.GetEligibleTasks(ctx)
	require.NoError(t, err)
	require.Len(t, eligible, 1)
	assert.Equal(t, taskB.ID, eligible[0].ID)
}

func TestAssignmentEpochColumnExists(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Eve", "eve@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "epoch-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// Freshly created tasks must start at epoch 0.
	assert.Equal(t, int32(0), task.AssignmentEpoch)
}

func TestClaimTaskForWorker_IncrementsEpoch(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Frank", "frank@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "w1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
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

	// RequeueTask bumped 1 -> 2; second claim: epoch goes 2 -> 3. Epoch never decreases.
	claimed2, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), claimed2.AssignmentEpoch)
}

func TestUpdateTaskStatus_EpochGuarded(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Gina", "gina@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "w1-epoch", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
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
		ID:              task.ID,
		Status:          "running",
		AssignmentEpoch: 1,
	})
	require.NoError(t, err)

	// Update with STALE epoch should return pgx.ErrNoRows (0 rows affected).
	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID:              task.ID,
		Status:          "done",
		AssignmentEpoch: 0, // stale
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows)

	// Task should still be "running" — the stale update did nothing.
	fetched, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", fetched.Status)
}

func TestAppendTaskLog_EpochGuarded(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Hal", "hal@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "w1-logs", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
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
		TaskID: task.ID, Stream: "stdout", Content: "hello\n", AssignmentEpoch: 1,
	})
	require.NoError(t, err)

	// Log with stale epoch.
	err = q.AppendTaskLog(ctx, store.AppendTaskLogParams{
		TaskID: task.ID, Stream: "stdout", Content: "from zombie\n", AssignmentEpoch: 0,
	})
	require.NoError(t, err) // :exec returns nil even when 0 rows inserted

	logs, err := q.GetTaskLogs(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "hello\n", logs[0].Content)
}

func TestReconciliationQueries(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Ivy", "ivy@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
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
		JobID: job.ID, Name: "a", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskA.ID, WorkerID: pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Task B: also dispatched to w1
	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "b", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskB.ID, WorkerID: pgtype.UUID{Bytes: w1.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)

	// Task C: dispatched to w2, then left dispatched
	taskC, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "c", Commands: []byte(`[["true"]]`),
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

	// ListGraceCandidates: should return both w1 and w2.
	candidates, err := q.ListGraceCandidates(ctx)
	require.NoError(t, err)
	assert.Len(t, candidates, 2)

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

func TestWorkerWorkspacesAndSourceColumn(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Wendy", "w@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "ws-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)

	// tasks.source must be a nullable JSONB column
	src := []byte(`{"type":"perforce","stream":"//streams/X/main"}`)
	task, err := q.CreateTaskWithSource(ctx, store.CreateTaskWithSourceParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Source: src,
	})
	require.NoError(t, err)
	require.JSONEq(t, string(src), string(task.Source))

	// status must accept new enum values
	_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
		ID: task.ID, Status: "preparing", Epoch: 0,
	})
	require.NoError(t, err)
	_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
		ID: task.ID, Status: "prepare_failed", Epoch: 0,
	})
	require.NoError(t, err)

	// worker_workspaces upsert + list round-trip
	worker := newTestWorker(t, q)
	err = q.UpsertWorkerWorkspace(ctx, store.UpsertWorkerWorkspaceParams{
		WorkerID:     worker.ID,
		SourceType:   "perforce",
		SourceKey:    "//streams/X/main",
		ShortID:      "abcdef",
		BaselineHash: "deadbeefdeadbeef",
		LastUsedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	rows, err := q.ListWorkerWorkspaces(ctx, worker.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "abcdef", rows[0].ShortID)
}

func TestSelfDependencyConstraintRejected(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Cyl", "cyl@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "self-dep", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "a", Commands: []byte(`[["echo","a"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// A task depending on itself must violate the no_self_dep CHECK constraint.
	err = q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: task.ID, DependsOnTaskID: task.ID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no_self_dep")
}

func TestFailDependentTasksTerminatesOnChain(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Chain", "chain@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "chain", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	mk := func(name string) store.Task {
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: job.ID, Name: name, Commands: []byte(`[["echo"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		return task
	}
	a, b, c := mk("a"), mk("b"), mk("c")

	// b depends on a, c depends on b.
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: b.ID, DependsOnTaskID: a.ID,
	}))
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: c.ID, DependsOnTaskID: b.ID,
	}))

	// Failing a must transitively fail b and c, and must terminate.
	require.NoError(t, q.FailDependentTasks(ctx, a.ID))

	gb, err := q.GetTask(ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", gb.Status)
	gc, err := q.GetTask(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", gc.Status)
}

func TestRequeueTask_BumpsEpochAndFencesStaleUpdates(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rq-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "rq-task", Commands: []byte(`[["echo","hi"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 0,
	})
	require.NoError(t, err)

	// Claim: status -> 'dispatched', epoch 0 -> 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// Requeue: status -> 'pending', epoch 1 -> 2.
	require.NoError(t, q.RequeueTask(ctx, task.ID))

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status, "task must be back to pending")
	require.False(t, got.WorkerID.Valid, "worker_id must be cleared")
	require.Equal(t, int32(2), got.AssignmentEpoch, "epoch must be bumped to 2")

	// A stale status update at the old epoch (1) must be rejected.
	_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
		ID: task.ID, Status: "done", Epoch: 1,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "stale update at epoch 1 must be rejected after requeue")

	got2, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got2.Status, "stale update must not have changed task status")
}

func TestRequeueTaskByID_BumpsEpochAndFencesStaleUpdates(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "rqid-task", Commands: []byte(`[["echo","hi"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 0,
	})
	require.NoError(t, err)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	require.NoError(t, q.RequeueTaskByID(ctx, task.ID))

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status, "task must be back to pending")
	require.False(t, got.WorkerID.Valid, "worker_id must be cleared")
	require.Equal(t, int32(2), got.AssignmentEpoch, "epoch must be bumped to 2")

	_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
		ID: task.ID, Status: "done", Epoch: 1,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "stale update at epoch 1 must be rejected after requeue")

	got2, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got2.Status, "stale update must not have changed task status")
}

func TestIncrementTaskRetryCount_BumpsEpochAndFencesStaleRetry(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "retry-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)

	// Allow one retry so IncrementTaskRetryCount is a valid transition.
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "retry-task", Commands: []byte(`[["echo","hi"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 1,
	})
	require.NoError(t, err)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// Retry: status -> 'pending', retry_count 0 -> 1, epoch 1 -> 2.
	retried, err := q.IncrementTaskRetryCount(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", retried.Status, "task must be back to pending")
	require.Equal(t, int32(1), retried.RetryCount, "retry_count must be incremented to 1")
	require.Equal(t, int32(2), retried.AssignmentEpoch, "epoch must be bumped to 2")

	// A stale terminal update at the old epoch (1) must be rejected AND must not
	// be able to drive a second retry (the "burn an extra retry" failure mode).
	_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
		ID: task.ID, Status: "failed", Epoch: 1,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "stale terminal update at epoch 1 must be rejected after retry")

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status, "stale update must not have changed task status")
	require.Equal(t, int32(1), got.RetryCount, "stale generation must not drive a second retry")
}

func TestRecomputeJobStatus(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()
	user := makeTestUser(t, q, ctx, "Rita", "rita@example.com")

	mkJob := func(name string) store.Job {
		job, err := q.CreateJob(ctx, store.CreateJobParams{
			Name: name, Priority: "normal", SubmittedBy: user.ID,
			Labels: []byte(`{}`), ScheduledJobID: pgtype.UUID{},
		})
		require.NoError(t, err)
		return job
	}
	mkTask := func(job store.Job, status string) {
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		if status != "pending" {
			_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
				ID: task.ID, Status: status, Epoch: 0,
			})
			require.NoError(t, err)
		}
	}

	// All tasks done -> job done.
	allDone := mkJob("all-done")
	mkTask(allDone, "done")
	mkTask(allDone, "done")
	got, err := q.RecomputeJobStatus(ctx, allDone.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", got)

	// One still active -> job running.
	oneActive := mkJob("one-active")
	mkTask(oneActive, "done")
	mkTask(oneActive, "running")
	got, err = q.RecomputeJobStatus(ctx, oneActive.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", got)

	// All terminal but one failed -> job failed (timed_out also terminal-failure).
	mixedFail := mkJob("mixed-fail")
	mkTask(mixedFail, "done")
	mkTask(mixedFail, "failed")
	mkTask(mixedFail, "timed_out")
	got, err = q.RecomputeJobStatus(ctx, mixedFail.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", got)

	// No tasks -> pgx.ErrNoRows, mirroring the old "" return.
	empty := mkJob("empty")
	_, err = q.RecomputeJobStatus(ctx, empty.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// Concurrent completion: two goroutines each finish one task then recompute.
	// This reproduces the original read-modify-write race: if goroutine A reads
	// the task list before goroutine B has marked its task done, the old code
	// would see one active task, compute "running", and overwrite a "done" that
	// B had already committed - stranding the job. The atomic SQL prevents this
	// because the UPDATE re-reads task state at write time inside Postgres.
	//
	// Both goroutines use q (backed by a pgxpool.Pool); each query call acquires
	// its own connection from the pool, so they genuinely execute concurrently.
	w := newTestWorker(t, q)
	for i := 0; i < 20; i++ {
		race := mkJob("race")

		// Create two tasks and claim them so they are 'dispatched' (epoch=1).
		// 'dispatched' is active in the CASE expression (not in done/failed/timed_out),
		// so a goroutine that reads before the other has marked done will see an
		// active task and would compute "running" under the old implementation.
		task1, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: race.ID, Name: "t1", Commands: []byte(`[["true"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		task2, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: race.ID, Name: "t2", Commands: []byte(`[["true"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)

		_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: task1.ID, WorkerID: w.ID})
		require.NoError(t, err)
		_, err = q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: task2.ID, WorkerID: w.ID})
		require.NoError(t, err)

		// ready signals that both goroutines have started and are about to race.
		ready := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-ready
			_, _ = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
				ID: task1.ID, Status: "done", Epoch: 1,
			})
			_, _ = q.RecomputeJobStatus(ctx, race.ID)
		}()
		go func() {
			defer wg.Done()
			<-ready
			_, _ = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
				ID: task2.ID, Status: "done", Epoch: 1,
			})
			_, _ = q.RecomputeJobStatus(ctx, race.ID)
		}()

		close(ready) // release both goroutines simultaneously
		wg.Wait()

		final, err := q.GetJob(ctx, race.ID)
		require.NoError(t, err)
		assert.Equal(t, "done", final.Status, "iteration %d stranded job", i)
	}
}

func TestMarkWorkerOfflineIfEpoch_StaleEpochIsNoOp(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-off", Hostname: "w-off-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	// Connection S registers: connection_epoch 0 -> 1, status online.
	s, err := q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:         w.ID,
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), s.ConnectionEpoch)

	// Connection F reconnects: connection_epoch 1 -> 2, status stays online.
	f, err := q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID:         w.ID,
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), f.ConnectionEpoch)

	// S's stale teardown tries to mark offline at epoch 1: fence holds, 0 rows.
	now := time.Now()
	rows, err := q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:              w.ID,
		LastSeenAt:      pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), rows, "stale-epoch offline must affect zero rows")

	after, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, "online", after.Status, "live worker must stay online")

	// Current-epoch offline (epoch 2) applies: positive control.
	rows, err = q.MarkWorkerOfflineIfEpoch(ctx, store.MarkWorkerOfflineIfEpochParams{
		ID:              w.ID,
		LastSeenAt:      pgtype.Timestamptz{Time: now, Valid: true},
		DisconnectedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		ConnectionEpoch: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows, "current-epoch offline must apply")
	after, err = q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, "offline", after.Status)
}

func TestRequeueWorkerTasksIfEpoch_StaleEpochIsNoOp(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Req-Stale", "req-stale@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-req-stale", Hostname: "w-req-stale-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// Worker comes online at epoch 1, task claimed (assignment_epoch 0 -> 1, dispatched).
	_, err = q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID: w.ID, LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "dispatched", claimed.Status)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// Reconnect bumps connection_epoch 1 -> 2 (grace timer was armed at epoch 1).
	_, err = q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID: w.ID, LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	// Stale grace fire at epoch 1: EXISTS guard fails, zero tasks move.
	ids, err := q.RequeueWorkerTasksIfEpoch(ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID: w.ID, ConnectionEpoch: 1,
	})
	require.NoError(t, err)
	assert.Empty(t, ids, "stale-epoch requeue must move zero tasks")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status, "task must stay dispatched")
	assert.Equal(t, int32(1), after.AssignmentEpoch, "assignment_epoch must not be bumped")
	assert.Equal(t, w.ID, after.WorkerID, "task must remain assigned")
}

func TestRequeueWorkerTasksIfEpoch_CurrentEpochRequeues(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Req-Cur", "req-cur@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "j", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w-req-cur", Hostname: "w-req-cur-host", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// Worker online at epoch 1, task claimed (assignment_epoch -> 1).
	_, err = q.RegisterWorkerConnection(ctx, store.RegisterWorkerConnectionParams{
		ID: w.ID, LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// No reconnect: requeue at the current epoch 1 moves the task.
	ids, err := q.RequeueWorkerTasksIfEpoch(ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID: w.ID, ConnectionEpoch: 1,
	})
	require.NoError(t, err)
	require.Len(t, ids, 1)

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", after.Status, "task must be requeued to pending")
	assert.Equal(t, int32(2), after.AssignmentEpoch, "assignment_epoch must be bumped 1 -> 2")
	assert.False(t, after.WorkerID.Valid, "worker_id must be cleared")
}
