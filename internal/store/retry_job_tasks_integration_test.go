//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// retryFixture is one job plus one worker. Its helpers put tasks into a status
// the way production does - claim (bumping assignment_epoch to 1 and setting
// worker_id), then write through the fenced UpdateTaskStatus - so a planted task
// carries a real assignee and a real epoch. That is what makes the "previous
// generation is dead" assertions mean anything.
type retryFixture struct {
	q   *store.Queries
	ctx context.Context
	job store.Job
	w   store.Worker
}

func newRetryFixture(t *testing.T) *retryFixture {
	t.Helper()
	q := newTestQueries(t)
	ctx := context.Background()
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "retry-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return &retryFixture{q: q, ctx: ctx, job: job, w: w}
}

// pending creates a task and leaves it at epoch 0 with worker_id NULL.
func (f *retryFixture) pending(t *testing.T, name string) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 0,
	})
	require.NoError(t, err)
	return task
}

// dispatched creates a task and claims it: status 'dispatched', epoch 1.
func (f *retryFixture) dispatched(t *testing.T, name string) store.Task {
	t.Helper()
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: f.pending(t, name).ID, WorkerID: f.w.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	return claimed
}

// inStatus drives a task to status through the production path. Valid for
// 'running', 'done', 'failed', 'timed_out'. Ends at epoch 1 with worker_id set.
//
// started_at and finished_at are stamped the way handleTaskStatus stamps them
// (internal/worker/handler.go: started_at on 'running', finished_at on any
// terminal status). Without that a planted terminal row has both columns NULL,
// and every "the reopened row cleared finished_at" / "the unmatched row kept
// finished_at" assertion in this file would be vacuous.
func (f *retryFixture) inStatus(t *testing.T, name, status string) store.Task {
	t.Helper()
	claimed := f.dispatched(t, name)
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	params := store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: status, WorkerID: claimed.WorkerID,
		AssignmentEpoch: claimed.AssignmentEpoch, StartedAt: now,
	}
	if status != "running" {
		params.FinishedAt = now
	}
	updated, err := f.q.UpdateTaskStatus(f.ctx, params)
	require.NoError(t, err)
	require.Equal(t, status, updated.Status)
	require.True(t, updated.StartedAt.Valid)
	require.Equal(t, status != "running", updated.FinishedAt.Valid)
	return updated
}

func (f *retryFixture) get(t *testing.T, id pgtype.UUID) store.Task {
	t.Helper()
	got, err := f.q.GetTask(f.ctx, id)
	require.NoError(t, err)
	return got
}

func (f *retryFixture) dep(t *testing.T, task, dependsOn store.Task) {
	t.Helper()
	require.NoError(t, f.q.CreateTaskDependency(f.ctx, store.CreateTaskDependencyParams{
		TaskID: task.ID, DependsOnTaskID: dependsOn.ID,
	}))
}

// TestRetryJobTasks_StatusAllowList_DoneTaskIsNotReopenedByFailedMode is the
// item's criterion "proven RED against a version without the status allow-list:
// a done task must not be reopened by ?task=failed".
//
// The assertion set is wider than "status is still done" on purpose: a mutation
// dropping the allow-list would also clear the done task's assignee, wipe its
// finished_at and bump its epoch - which is what would make a trailing log chunk
// from its own successful run vanish.
func TestRetryJobTasks_StatusAllowList_DoneTaskIsNotReopenedByFailedMode(t *testing.T) {
	f := newRetryFixture(t)

	doneTask := f.inStatus(t, "t-done", "done")
	failedTask := f.inStatus(t, "t-failed", "failed")

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Len(t, reopened, 1, "task=failed must reopen exactly the failed task")
	require.Equal(t, failedTask.ID, reopened[0])

	gotDone := f.get(t, doneTask.ID)
	require.Equal(t, "done", gotDone.Status, "a done task must not be reopened by task=failed")
	require.Equal(t, doneTask.AssignmentEpoch, gotDone.AssignmentEpoch,
		"an unmatched row must not have its generation ended")
	require.True(t, gotDone.WorkerID.Valid, "an unmatched row must keep its assignee")
	require.True(t, gotDone.FinishedAt.Valid, "an unmatched row must keep finished_at")

	require.Equal(t, "pending", f.get(t, failedTask.ID).Status)
}
