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
	"github.com/stretchr/testify/assert"
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
	//
	// The condition func runs on testify's own goroutine, not the test
	// goroutine, so require.NoError (which calls t.FailNow) is documented misuse
	// inside it: a poll error would Goexit the wrong goroutine and the test would
	// instead time out 10s later reporting the unrelated "B never blocked"
	// message. The error is captured and re-asserted on the test goroutine
	// below.
	//
	// That re-assertion is why this is assert.Eventually and not
	// require.Eventually: require would FailNow on timeout and the poll error
	// would never be reported, which is the exact misdiagnosis this is here to
	// prevent. The last error is kept, not cleared on a later success, so a
	// timeout preceded by real errors names them. The mutex is not decoration -
	// the two goroutines genuinely share this variable.
	var (
		pollMu  sync.Mutex
		pollErr error
	)
	blocked := assert.Eventually(t, func() bool {
		var n int
		if err := f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock' AND state = 'active'`).Scan(&n); err != nil {
			pollMu.Lock()
			pollErr = err
			pollMu.Unlock()
			return false
		}
		return n > 0
	}, 10*time.Second, 50*time.Millisecond, "B never blocked on A's row lock")
	pollMu.Lock()
	lastPollErr := pollErr
	pollMu.Unlock()
	if !blocked {
		require.NoError(t, lastPollErr,
			"B never appeared blocked because the pg_stat_activity poll kept failing")
	}

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

// TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne is item
// criterion 3: assert the increment per row, not merely that the epoch changed.
// The two tasks start at DIFFERENT epochs so a statement that assigned a
// constant would fail.
//
// The plan seeded the second task with RequeueTaskByID on an already-terminal
// row; that statement carries `status IN ('dispatched','running')`, so it is a
// no-op there and both tasks would have started at epoch 1, making the comment
// above false. b is therefore driven running -> agent retry -> reclaimed ->
// timed out, which really does land it at a higher epoch.
//
// That agent retry is also what makes `retry_count must reset to 0` mean
// anything: seeded straight from CreateTask the column is already 0, so the
// assertion passes against a statement that never writes it. b reaches the
// operator retry with retry_count = 1.
func TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne(t *testing.T) {
	f := newRetryFixture(t)

	a := f.inStatus(t, "t-a", "failed")

	// b: running at epoch 1, one agent retry burned (retry_count 1, epoch 2),
	// reclaimed to epoch 3, then timed out - all through production statements.
	b := f.inStatus(t, "t-b", "running")
	burned, err := f.q.IncrementTaskRetryCount(f.ctx, store.IncrementTaskRetryCountParams{
		ID: b.ID, AssignmentEpoch: b.AssignmentEpoch, WorkerID: b.WorkerID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), burned.RetryCount)
	reclaimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: b.ID, WorkerID: f.w.ID,
	})
	require.NoError(t, err)
	b, err = f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: reclaimed.ID, Status: "timed_out", WorkerID: reclaimed.WorkerID,
		AssignmentEpoch: reclaimed.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
		FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.NotEqual(t, a.AssignmentEpoch, b.AssignmentEpoch,
		"the two rows must start at different epochs, or a constant-assigning "+
			"statement would satisfy this test")
	require.Equal(t, int32(1), b.RetryCount,
		"b must reach the operator retry with a non-zero retry_count, or the "+
			"reset assertion below is vacuous")

	before := map[pgtype.UUID]int32{a.ID: a.AssignmentEpoch, b.ID: b.AssignmentEpoch}

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Len(t, reopened, 2)

	for id, oldEpoch := range before {
		got := f.get(t, id)
		require.Equal(t, "pending", got.Status)
		require.False(t, got.WorkerID.Valid, "worker_id must be cleared")
		require.False(t, got.StartedAt.Valid, "started_at must be cleared")
		require.False(t, got.FinishedAt.Valid, "finished_at must be cleared")
		require.Equal(t, int32(0), got.RetryCount, "retry_count must reset to 0")
		require.Equal(t, oldEpoch+1, got.AssignmentEpoch,
			"assignment_epoch must be old+1 for every reopened row")
	}
}

// TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected is the
// second half of item criterion 3. A retried task's previous generation must be
// unable to write status, logs or a retry. Note WHY each is rejected: the epoch
// no longer matches AND worker_id is now NULL, so the plain `=` comparison fails
// closed. Both are required; neither is decoration.
func TestRetryJobTasks_PreviousGenerationIsDead_StatusLogAndRetryAllRejected(t *testing.T) {
	f := newRetryFixture(t)
	task := f.inStatus(t, "t-failed", "failed")
	oldEpoch, oldWorker := task.AssignmentEpoch, task.WorkerID

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Len(t, reopened, 1)

	_, err = f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: task.ID, Status: "done", WorkerID: oldWorker, AssignmentEpoch: oldEpoch,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a late status update from the dead generation must be dropped")

	_, err = f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: task.ID, AssignmentEpoch: oldEpoch, WorkerID: oldWorker,
		Stream: "stdout", Content: "zombie output",
		// A live cutoff, so this assertion still fails on the epoch and worker
		// predicates rather than passing for the new predicate's reason.
		MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "a trailing log chunk from the dead generation must be dropped")

	_, err = f.q.IncrementTaskRetryCount(f.ctx, store.IncrementTaskRetryCountParams{
		ID: task.ID, AssignmentEpoch: oldEpoch, WorkerID: oldWorker,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "an agent retry from the dead generation must be dropped")

	logs, err := f.q.GetTaskLogs(f.ctx, task.ID)
	require.NoError(t, err)
	require.Empty(t, logs, "no output from the dead generation may reach task_logs")

	got := f.get(t, task.ID)
	require.Equal(t, "pending", got.Status)
	require.Equal(t, int32(0), got.RetryCount)
	require.Equal(t, oldEpoch+1, got.AssignmentEpoch)
}

// TestRetryJobTasks_AllMode_WidensToTerminalSetAndNoFurther is the test that
// stops a retry from evicting a live agent. It must exist even though the
// handler's job-status gate makes a job with a running task hard to reach
// through HTTP: the statement's guarantee must not depend on the caller.
func TestRetryJobTasks_AllMode_WidensToTerminalSetAndNoFurther(t *testing.T) {
	f := newRetryFixture(t)

	pending := f.pending(t, "t-pending")
	dispatched := f.dispatched(t, "t-dispatched")
	running := f.inStatus(t, "t-running", "running")
	done := f.inStatus(t, "t-done", "done")
	failed := f.inStatus(t, "t-failed", "failed")
	timedOut := f.inStatus(t, "t-timedout", "timed_out")

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: true,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{done.ID, failed.ID, timedOut.ID}, reopened,
		"task=all must reopen exactly the three terminal statuses")

	for _, live := range []store.Task{pending, dispatched, running} {
		got := f.get(t, live.ID)
		require.Equal(t, live.Status, got.Status, "a non-terminal task must be untouched")
		require.Equal(t, live.AssignmentEpoch, got.AssignmentEpoch,
			"a non-terminal task's generation must not be ended - that would evict a live agent")
		require.Equal(t, live.WorkerID, got.WorkerID)
	}
}

// TestGetJobForUpdate_TakesARowLockThatBlocksASecondReader proves the statement
// is not just GetJob with a longer name. Without the lock, handleCancelJob and
// handleRetryJob interleave and a cancel can stamp `cancelled` over a retry's
// freshly pending tasks - and GetEligibleTasks does not consult job status.
func TestGetJobForUpdate_TakesARowLockThatBlocksASecondReader(t *testing.T) {
	f := newRetryFixture(t)

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx)
	_, err = f.q.WithTx(txA).GetJobForUpdate(f.ctx, f.job.ID)
	require.NoError(t, err)

	txB, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx)

	blocked := make(chan error, 1)
	go func() {
		_, err := f.q.WithTx(txB).GetJobForUpdate(f.ctx, f.job.ID)
		blocked <- err
	}()

	select {
	case err := <-blocked:
		t.Fatalf("second GetJobForUpdate returned immediately (%v): the row lock is missing", err)
	case <-time.After(750 * time.Millisecond):
	}

	require.NoError(t, txA.Commit(f.ctx))
	select {
	case err := <-blocked:
		require.NoError(t, err, "B must succeed once A commits")
	case <-time.After(10 * time.Second):
		t.Fatal("B never unblocked after A committed")
	}
}

// NEGATIVE control. T failed, its dependent D already ran (done) and is not in
// the selected set. Reopening T under D would reproduce route B of
// bug-2026-06-26 by design.
func TestRetryJobTasks_DependentsGuard_AlreadyRanDependentBlocks(t *testing.T) {
	f := newRetryFixture(t)
	tsk := f.inStatus(t, "t", "failed")
	d := f.inStatus(t, "d", "done")
	f.dep(t, d, tsk) // d depends on t

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened, "a task whose dependent already ran must not reopen")
	require.Equal(t, "failed", f.get(t, tsk.ID).Status)
	require.Equal(t, tsk.AssignmentEpoch, f.get(t, tsk.ID).AssignmentEpoch)
}

// POSITIVE control - the one that catches a guard that blocks everything. On any
// healthy failed job FailDependentTasks has cascade-failed the failing task's
// pending dependents, so every selected task HAS failed dependents. A dependent
// that is itself being reopened by this same request must not block.
func TestRetryJobTasks_DependentsGuard_CascadeFailedDependentDoesNotBlock(t *testing.T) {
	f := newRetryFixture(t)
	tsk := f.inStatus(t, "t", "failed")
	d := f.pending(t, "d")
	f.dep(t, d, tsk)
	require.NoError(t, f.q.FailDependentTasks(f.ctx, tsk.ID))
	require.Equal(t, "failed", f.get(t, d.ID).Status)

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{tsk.ID, d.ID}, reopened,
		"the ordinary retry case: a cascade-failed dependent is reopened alongside its dependency")
	require.Equal(t, "pending", f.get(t, tsk.ID).Status)
	require.Equal(t, "pending", f.get(t, d.ID).Status)
}

// A -> B -> C (C depends on B, B depends on A). A failed, B failed, C done.
//
// HONEST SCOPE, corrected from the plan: this does NOT prove the recursive term.
// B is itself selected, so C is a DIRECT dependent of a selected task and the
// non-recursive term alone reaches it - dropping the UNION and the recursive
// term was observed to leave this test green. What it does pin is that a chain
// blocks as a unit and that widening to include_done unblocks it.
// TestRetryJobTasks_DependentsGuard_RecursionIsRequiredForAnUnselectedIntermediate
// is the test that pins the closure.
func TestRetryJobTasks_DependentsGuard_TransitiveDescendantBlocks(t *testing.T) {
	f := newRetryFixture(t)
	a := f.inStatus(t, "a", "failed")
	b := f.inStatus(t, "b", "failed")
	c := f.inStatus(t, "c", "done")
	f.dep(t, b, a)
	f.dep(t, c, b)

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened, "a transitive descendant that already ran must block the whole request")

	withDone, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: true,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{a.ID, b.ID, c.ID}, withDone,
		"with C selected too, the closure is fully covered and all three reopen")
}

// ALL-OR-NOTHING, asserting NO row changed rather than merely that the count was
// zero. A per-row guard would reopen X and strand it: X would be pending on a
// job whose other failed task stayed terminal, and the job would sit at running.
//
// The DAG is NOT the plan's A -> B -> C chain, which cannot discriminate this:
// there every selected row has the same blocking descendant, so a correlated
// (per-row) guard blocks all of them too and the mutation was observed to leave
// the test green. Here X has no dependents at all and Y does, so an uncorrelated
// guard blocks BOTH while a per-row guard would reopen X alone.
func TestRetryJobTasks_DependentsGuard_ExclusionIsAllOrNothing(t *testing.T) {
	f := newRetryFixture(t)
	a := f.inStatus(t, "x-no-dependents", "failed")
	b := f.inStatus(t, "y-blocked", "failed")
	c := f.inStatus(t, "z-already-ran", "done")
	f.dep(t, c, b) // z depends on y; x is unencumbered

	before := map[pgtype.UUID]store.Task{a.ID: a, b.ID: b, c.ID: c}
	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened)

	for id, was := range before {
		got := f.get(t, id)
		require.Equal(t, was.Status, got.Status, "no row may change under a blocked retry")
		require.Equal(t, was.AssignmentEpoch, got.AssignmentEpoch)
		require.Equal(t, was.WorkerID, got.WorkerID)
		require.Equal(t, was.RetryCount, got.RetryCount)
	}
}

// SelectRetryableTaskIDs must NOT carry the dependents guard: if it did, the
// handler could not tell "no failed tasks" from "blocked by a dependent", and
// would report the former for a job that has several failed tasks.
func TestSelectRetryableTaskIDs_IsUnguardedAndMatchesRetryJobTasksSelection(t *testing.T) {
	f := newRetryFixture(t)
	tsk := f.inStatus(t, "t", "failed")
	d := f.inStatus(t, "d", "done")
	f.dep(t, d, tsk)

	selected, err := f.q.SelectRetryableTaskIDs(f.ctx, store.SelectRetryableTaskIDsParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{tsk.ID}, selected,
		"the selection is unguarded: it reports what the mode matched, not what the guard allowed")

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened, "the guard blocks here, and the difference is what the handler classifies")

	all, err := f.q.SelectRetryableTaskIDs(f.ctx, store.SelectRetryableTaskIDsParams{
		JobID: f.job.ID, IncludeDone: true,
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []pgtype.UUID{tsk.ID, d.ID}, all,
		"include_done widens the selection identically to RetryJobTasks's")
}

// RECURSION. The closure only needs its recursive term when an UNSELECTED,
// NON-BLOCKING task sits between a selected task and one that already ran:
// A (failed, selected) -> B (pending, not selected, does not block) -> C (done,
// not selected, blocks). Direct edges from `selected` reach only B, which is
// pending and therefore harmless, so a non-recursive `descendants` lets A reopen
// underneath a dependent that has already produced output. Only the recursive
// term reaches C.
//
// Every all-terminal chain is covered by direct edges alone, because each link
// is itself selected - which is why the plan's transitive test stayed green
// under the drop-the-recursion mutation. This shape is the discriminating one.
func TestRetryJobTasks_DependentsGuard_RecursionIsRequiredForAnUnselectedIntermediate(t *testing.T) {
	f := newRetryFixture(t)
	a := f.inStatus(t, "a", "failed")
	b := f.pending(t, "b")
	c := f.inStatus(t, "c", "done")
	f.dep(t, b, a)
	f.dep(t, c, b)

	require.Equal(t, "pending", f.get(t, b.ID).Status,
		"the intermediate must be pending, or it would block on its own status")

	reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
		JobID: f.job.ID, IncludeDone: false,
	})
	require.NoError(t, err)
	require.Empty(t, reopened,
		"a descendant reachable only through an unselected pending intermediate "+
			"must still block: reopening a under an already-done c is route B of "+
			"bug-2026-06-26 by design")
	require.Equal(t, "failed", f.get(t, a.ID).Status)
	require.Equal(t, a.AssignmentEpoch, f.get(t, a.ID).AssignmentEpoch)
	require.Equal(t, "done", f.get(t, c.ID).Status)
}
