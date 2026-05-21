//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"relay/internal/store"
)

func TestWorkerDisableEnable_RoundTrip(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	w := newTestWorker(t, q)
	require.False(t, w.DisabledAt.Valid, "a new worker must start enabled")

	n, err := q.DisableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "first disable must affect one row")

	got, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.True(t, got.DisabledAt.Valid, "worker must be disabled")

	// Idempotent: a second disable affects zero rows and does not re-stamp.
	n, err = q.DisableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "second disable must affect zero rows")

	n, err = q.EnableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "enable must affect one row")

	got, err = q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, got.DisabledAt.Valid, "worker must be enabled again")

	// Idempotent: a second enable affects zero rows.
	n, err = q.EnableWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "second enable must affect zero rows")
}

func TestRequeueWorkerTasksWithEpoch_BumpsEpochAndFencesStaleUpdates(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:        "requeue-job",
		Priority:    "normal",
		SubmittedBy: user.ID,
		Labels:      []byte("{}"),
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "requeue-task",
		Commands: []byte(`[["echo","hi"]]`),
		Env:      []byte("{}"),
		Requires: []byte("{}"),
		Retries:  0,
	})
	require.NoError(t, err)

	// Claim the task onto the worker: status -> 'dispatched', epoch 0 -> 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID:       task.ID,
		WorkerID: w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	ids, err := q.RequeueWorkerTasksWithEpoch(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, ids, 1, "the one active task must be requeued")
	require.Equal(t, task.ID, ids[0])

	got, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got.Status, "task must be back to pending")
	require.False(t, got.WorkerID.Valid, "worker_id must be cleared")
	require.Equal(t, int32(2), got.AssignmentEpoch, "epoch must be bumped to 2")

	// A stale status update at the old epoch (1) must be rejected.
	_, err = q.UpdateTaskStatusEpoch(ctx, store.UpdateTaskStatusEpochParams{
		ID:     task.ID,
		Status: "done",
		Epoch:  1,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "stale update at epoch 1 must be rejected after requeue")

	got2, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", got2.Status, "stale update must not have changed task status")
}
