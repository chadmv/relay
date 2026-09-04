//go:build integration

package store_test

import (
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preparingAt claims a task at `at` and then moves it to 'preparing' through the
// production status writer, so the row carries a real assignee and a real epoch -
// the same construction runningAt uses for 'running'.
func preparingAt(t *testing.T, f *assignedFixture, name string, at time.Time) store.Task {
	t.Helper()
	task := f.claimedAt(t, name, at)
	got, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID:              task.ID,
		Status:          "preparing",
		WorkerID:        f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
	})
	require.NoError(t, err)
	require.False(t, got.StartedAt.Valid, "a preparing row has no start time")
	return got
}

// TestAppendTaskLog_APreparingTaskAcceptsLogChunks is the site where omission is
// catastrophic and silent. A preparing row has finished_at IS NULL, so it fails
// the recency arm too; leaving `preparing` out of the FIRST arm discards every
// workspace-sync log line the system produces, with no error and no log line -
// and the agent already streams that output as LOG_STREAM_PREPARE chunks.
func TestAppendTaskLog_APreparingTaskAcceptsLogChunks(t *testing.T) {
	f := newAssignedFixture(t)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	prep := preparingAt(t, f, "syncing", base)

	row, err := f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: prep.ID, AssignmentEpoch: prep.AssignmentEpoch, WorkerID: f.w.ID,
		Stream: "stdout", Content: "//depot/... - syncing",
		MinFinishedAt: pgtype.Timestamptz{Time: base.Add(-15 * time.Minute), Valid: true},
	})
	require.NoError(t, err,
		"a preparing task's own assignee must be able to append. pgx.ErrNoRows here means the first "+
			"arm of AppendTaskLog's disjunction omits `preparing`, which drops every workspace-sync "+
			"log line in the system silently")
	require.Equal(t, prep.JobID, row.JobID)
}

// Reconcile's view. A task absent here is never compared against the agent's
// running_tasks report and so is never requeued when the two disagree.
func TestGetActiveTasksForWorker_IncludesAPreparingTask(t *testing.T) {
	f := newAssignedFixture(t)
	prep := preparingAt(t, f, "syncing", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	rows, err := f.q.GetActiveTasksForWorker(f.ctx, f.w.ID)
	require.NoError(t, err)
	var found bool
	for _, r := range rows {
		if r.ID == prep.ID {
			found = true
		}
	}
	assert.True(t, found, "reconcile must see a preparing task, or nothing ever requeues it")
}

// The startup grace seed. A worker absent here gets no grace timer, so a
// disconnect during a workspace sync releases nothing.
func TestListGraceCandidates_AWorkerWithOnlyAPreparingTaskIsACandidate(t *testing.T) {
	f := newAssignedFixture(t)
	preparingAt(t, f, "syncing", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	rows, err := f.q.ListGraceCandidates(f.ctx)
	require.NoError(t, err)
	var found bool
	for _, r := range rows {
		if r.ID == f.w.ID {
			found = true
		}
	}
	assert.True(t, found,
		"a worker whose only assigned task is preparing still holds an assignment, so a disconnect "+
			"must arm its grace timer")
}

// The reconcile-driven single-task requeue, at its assignee's own epoch.
func TestRequeueTaskByID_RequeuesAPreparingTaskForItsAssignee(t *testing.T) {
	f := newAssignedFixture(t)
	prep := preparingAt(t, f, "syncing", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: prep.ID, AssignmentEpoch: prep.AssignmentEpoch, WorkerID: f.w.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "a preparing task must be requeueable by its own assignee")

	after := f.get(t, prep.ID)
	assert.Equal(t, "pending", after.Status)
	assert.Equal(t, prep.AssignmentEpoch+1, after.AssignmentEpoch,
		"returning a task to pending must bump the epoch")
}

// The disconnect and admin-disable requeue.
func TestRequeueWorkerTasks_RequeuesAPreparingTask(t *testing.T) {
	f := newAssignedFixture(t)
	prep := preparingAt(t, f, "syncing", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	ids, err := f.q.RequeueWorkerTasks(f.ctx, f.w.ID)
	require.NoError(t, err)
	assert.Contains(t, ids, prep.ID,
		"a preparing task must be released when its worker disconnects or is disabled; left out it "+
			"holds the slot and the job forever")
}

// The grace-window requeue, fenced on the worker's connection epoch.
func TestRequeueWorkerTasksIfEpoch_RequeuesAPreparingTask(t *testing.T) {
	f := newAssignedFixture(t)
	prep := preparingAt(t, f, "syncing", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	ids, err := f.q.RequeueWorkerTasksIfEpoch(f.ctx, store.RequeueWorkerTasksIfEpochParams{
		WorkerID: f.w.ID, ConnectionEpoch: f.w.ConnectionEpoch,
	})
	require.NoError(t, err)
	assert.Contains(t, ids, prep.ID)
}

// The dispatcher's slot accounting. An uncounted task reads as a free slot, so
// the dispatcher overcommits the worker.
func TestCountActiveTasksByAllWorkers_CountsAPreparingTask(t *testing.T) {
	f := newAssignedFixture(t)
	preparingAt(t, f, "syncing", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	rows, err := f.q.CountActiveTasksByAllWorkers(f.ctx)
	require.NoError(t, err)
	var active int64
	for _, r := range rows {
		if r.WorkerID == f.w.ID {
			active = r.Active
		}
	}
	assert.Equal(t, int64(1), active,
		"a preparing task holds a slot; counted as free the dispatcher overcommits the worker")
}

// The job cancel. A preparing task left live keeps syncing against the workspace
// while the job reads cancelled.
func TestCancelJobTasks_FailsAPreparingTask(t *testing.T) {
	f := newAssignedFixture(t)
	prep := preparingAt(t, f, "syncing", time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	require.NoError(t, f.q.CancelJobTasks(f.ctx, f.job.ID))

	after := f.get(t, prep.ID)
	assert.Equal(t, "failed", after.Status,
		"a cancelled job must fail its preparing tasks, or the job reads cancelled while the task "+
			"is still live with its agent syncing")
	assert.False(t, after.WorkerID.Valid, "and the cancel ends the assignment")
	assert.Equal(t, prep.AssignmentEpoch+1, after.AssignmentEpoch, "which means bumping the epoch")
}
