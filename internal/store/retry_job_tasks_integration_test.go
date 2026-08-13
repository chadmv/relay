//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// retryFixture is one job plus one worker. Its helpers put tasks into a status
// the way production does - claim (bumping assignment_epoch to 1 and setting
// worker_id), then write through the fenced UpdateTaskStatus - so a planted task
// carries a real assignee and a real epoch. That is what makes the "previous
// generation is dead" assertions mean anything.
type retryFixture struct {
	q    *store.Queries
	pool *pgxpool.Pool
	ctx  context.Context
	job  store.Job
	w    store.Worker
}

func newRetryFixture(t *testing.T) *retryFixture {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := context.Background()
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "retry-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return &retryFixture{q: q, pool: pool, ctx: ctx, job: job, w: w}
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

// TestRetryJobTasks_RowLevelPredicate_ConcurrentSecondRetryDoesNotDoubleBumpEpoch
// pins spec decision 9: the status allow-list must live in the UPDATE's own
// row-level WHERE, not only in the `selected` CTE.
//
// Under READ COMMITTED a blocked UPDATE re-evaluates its ROW-LEVEL qual against
// the updated tuple (EvalPlanQual); it does not re-execute CTEs, which were
// materialized from the statement's original snapshot. So the interleave has to
// be real: B's statement must START (materializing `selected` from a snapshot in
// which the tasks are still terminal) and then BLOCK on A's row locks, and A
// must commit while B is blocked.
//
// A sequential second call proves nothing here: it recomputes `selected` from a
// fresh snapshot in which the task is already `pending`, so the mutated
// statement also returns zero rows.
func TestRetryJobTasks_RowLevelPredicate_ConcurrentSecondRetryDoesNotDoubleBumpEpoch(t *testing.T) {
	f := newRetryFixture(t)
	task := f.inStatus(t, "t-failed", "failed")
	require.Equal(t, int32(1), task.AssignmentEpoch)

	args := store.RetryJobTasksParams{JobID: f.job.ID, IncludeDone: false}

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx)
	idsA, err := f.q.WithTx(txA).RetryJobTasks(f.ctx, args)
	require.NoError(t, err)
	require.Len(t, idsA, 1, "A must reopen the failed task")

	var (
		wg   sync.WaitGroup
		idsB []pgtype.UUID
		errB error
		txB  pgx.Tx
	)
	txB, err = f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx)

	wg.Add(1)
	go func() {
		defer wg.Done()
		idsB, errB = f.q.WithTx(txB).RetryJobTasks(f.ctx, args)
	}()

	// Wait until B is genuinely blocked on a row lock. This replaces a sleep:
	// if B has not reached the lock, committing A would let B run against a
	// post-A snapshot and the test would prove nothing.
	require.Eventually(t, func() bool {
		var n int
		require.NoError(t, f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock' AND state = 'active'`).Scan(&n))
		return n > 0
	}, 10*time.Second, 50*time.Millisecond, "B never blocked on A's row lock")

	require.NoError(t, txA.Commit(f.ctx))
	wg.Wait()
	require.NoError(t, errB)
	require.NoError(t, txB.Commit(f.ctx))

	require.Empty(t, idsB,
		"B must reopen nothing: EvalPlanQual re-checks the row-level status "+
			"predicate against the updated tuple, which is now 'pending'")

	got := f.get(t, task.ID)
	require.Equal(t, "pending", got.Status)
	require.Equal(t, int32(2), got.AssignmentEpoch,
		"exactly ONE epoch bump may result from two concurrent retries; a second "+
			"bump would end a generation another operator's retry may already be running")
}
