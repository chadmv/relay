//go:build integration

package store_test

import (
	"context"
	"testing"

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
		Name:        "test-job",
		Priority:    "normal",
		SubmittedBy: user.ID,
		Labels:      []byte(`{}`),
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
	})
	require.NoError(t, err)

	taskA, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:   job.ID,
		Name:    "task-a",
		Command: []string{"echo", "a"},
		Env:     []byte(`{}`),
		Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	taskB, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:   job.ID,
		Name:    "task-b",
		Command: []string{"echo", "b"},
		Env:     []byte(`{}`),
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
