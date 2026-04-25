//go:build integration

package store_test

import (
	"context"
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

	// Second claim: epoch goes 1 -> 2. Epoch never decreases.
	claimed2, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: pgtype.UUID{Bytes: w.ID.Bytes, Valid: true},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), claimed2.AssignmentEpoch)
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
