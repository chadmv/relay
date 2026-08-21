//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"relay/internal/store"
)

// TestRequeueTask_DoesNotTearOffAFreshAssignment is the SECOND instance of the
// shape bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence describes.
// The item claims RequeueTaskByID is "the only requeue statement in the tree
// whose WHERE clause names nothing but the task id and a status allow-list".
// THAT CLAIM IS FALSE: RequeueTask, three statements away in the same file, had
// exactly the same WHERE.
//
// Its only production caller is the dispatcher's send-failure path
// (internal/scheduler/dispatch.go), and the window there is up to the 5s
// sendTimeout between ClaimTaskForWorker returning `claimed` and RequeueTask
// firing on that now-stale snapshot.
//
// BE PRECISE ABOUT WHICH SEND FAILURE, because the first framing of this test
// said "a disconnected worker is what arms its own grace timer" and that is
// WRONG - the two halves cannot co-occur (internal/worker/sender.go):
//   - Worker gone from the registry: Registry.Send returns before
//     workerSender.Send is reached. No window at all.
//   - Worker disconnected: sender.closed is closed when the send loop returns,
//     so a blocked Send takes `case <-sender.closed` and returns immediately -
//     and teardownConnection closes the sender BEFORE arming the grace timer.
//   - Worker WEDGED BUT STILL REGISTERED: `case <-timeout.C` on a full queue is
//     the only branch that burns the 5s, and in that state the worker is online
//     with no grace timer armed.
//
// So the release that races the wedged goroutine comes from somewhere else. Two
// real ones, neither needing a grace timer:
//
//  1. Dispatcher claims T for W1: dispatched, epoch 1.
//  2. Send blocks: W1 is wedged but still registered.
//  3. EITHER an admin disables W1 (handleDisableWorker -> RequeueWorkerTasks),
//     OR the same agent opens a SECOND Connect whose reconcile requeues T via
//     the sibling statement - nothing serializes Connect per worker. T is
//     pending at epoch 2.
//  4. The dispatcher claims T for W2: dispatched, epoch 3.
//  5. The original goroutine finally calls RequeueTask on its epoch-1 snapshot.
//     On the id and 'dispatched' alone it MATCHED, tearing T off W2 mid-run.
//
// Same end state as the sibling bug: duplicate execution, no log line.
func TestRequeueTask_DoesNotTearOffAFreshAssignment(t *testing.T) {
	f := newRequeueFence(t)

	// The snapshot the dispatcher goroutine is holding while Send blocks.
	claimed := f.claimedBy(t, "rqt-repro", f.w1)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// The grace timer returns it to pending, carrying the epoch and worker it
	// read. This is the LEGITIMATE requeue and it must succeed.
	nB, err := f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), nB, "a current requeue must move the row")

	// The dispatcher hands it to a DIFFERENT worker.
	redispatched, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: claimed.ID, WorkerID: f.w2.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), redispatched.AssignmentEpoch)

	// Send finally fails and the original goroutine requeues its stale snapshot.
	// It must move nothing: the epoch is stale AND the task belongs to W2 now.
	nC, err := f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), nC, "a stale send-failure requeue must move zero rows")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status,
		"a stale send-failure requeue must not requeue a re-dispatched task")
	require.True(t, after.WorkerID.Valid, "the fresh assignment must survive")
	assert.Equal(t, f.w2.ID.Bytes, after.WorkerID.Bytes,
		"the task must still belong to the worker it was re-dispatched to")
	assert.Equal(t, int32(3), after.AssignmentEpoch,
		"a rejected requeue must not bump the epoch")
}

// A: THE EPOCH PREDICATE, ISOLATED. The worker predicate and the status
// predicate both PASS here - the task is still assigned to W1 and still
// 'dispatched' - so only the epoch can reject. The repro cannot distinguish
// this, because there the assignee changed too.
func TestRequeueTask_StaleEpochMovesZeroRowsEvenForTheSameWorker(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "rqt-a", f.w1)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	n, err := f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	reclaimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: claimed.ID, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), reclaimed.AssignmentEpoch)

	// The stale caller: right worker, right status, WRONG generation.
	n, err = f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a stale epoch must move zero rows")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	assert.Equal(t, int32(3), after.AssignmentEpoch, "a rejected requeue must not bump the epoch")
}

// B: THE WORKER PREDICATE, ISOLATED. The epoch MATCHES (both rows are at their
// first assignment, epoch 1) and the status predicate passes, so only the
// assignee predicate can reject. This is the "epoch establishes currency, not
// identity" case: W1 holds a perfectly current epoch for a task that is not its
// own.
func TestRequeueTask_WrongWorkerMovesZeroRows(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "rqt-b", f.w2)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	n, err := f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a worker that is not the assignee must move zero rows")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	require.True(t, after.WorkerID.Valid)
	assert.Equal(t, f.w2.ID.Bytes, after.WorkerID.Bytes, "the real assignee must be untouched")
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// C: A CALLER THAT LOST ITS IDENTITY. pgtype.UUID{} binds SQL NULL and
// `worker_id = NULL` is never true, so it fails closed. This is the shape the
// dispatcher would produce if RequeueTaskParams were ever built without its
// WorkerID field - the "ships inert" hazard - and it must move nothing rather
// than everything.
//
// READ THIS TOGETHER WITH TEST D. This case is rejected under BOTH `=` and
// `IS NOT DISTINCT FROM`, because the row's worker_id is non-NULL, so it does
// NOT pin the operator. D is the one that pins the operator.
func TestRequeueTask_ZeroValueWorkerIDMovesZeroRows(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "rqt-c", f.w1)

	n, err := f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: pgtype.UUID{},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a zero-value worker id must fail closed")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// D: THE REGRESSION TEST FOR THE COMPARISON STAYING A PLAIN `=`. Under
// IS NOT DISTINCT FROM two NULLs compare equal, the fence matches, and a caller
// with no identity at all can requeue an unassigned row.
//
// THE ROW IS PLANTED WITH RAW SQL, AND THAT IS THE POINT, not a shortcut. A
// (status='dispatched', worker_id IS NULL) row is unreachable through the
// production statements: everything that nulls worker_id also sets status to
// 'pending' or a terminal value. The status predicate is a second guarantee
// standing behind this one; this test exists so that if it ever widens, the
// operator choice is already pinned rather than rediscovered.
func TestRequeueTask_NullWorkerIDDoesNotMatchANullArgument(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Ned", "ned@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqt-null", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "wn", Hostname: "rqt-null-w", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "d", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)

	// Plant the otherwise-unreachable state: still 'dispatched', no assignee.
	_, err = pool.Exec(ctx, `UPDATE tasks SET worker_id = NULL WHERE id = $1`, claimed.ID)
	require.NoError(t, err)
	planted, err := q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	require.Equal(t, "dispatched", planted.Status, "fixture: the row must stay in the allow-list")
	require.False(t, planted.WorkerID.Valid, "fixture: the row must have no assignee")
	require.Equal(t, int32(1), planted.AssignmentEpoch)

	// NULL on both sides. `=` yields UNKNOWN and the row is left alone.
	n, err := q.RequeueTask(ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: planted.AssignmentEpoch, WorkerID: pgtype.UUID{},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "NULL worker_id must not match a NULL worker id argument")

	after, err := q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// E: THE STATUS PREDICATE, ISOLATED. The epoch AND the worker predicate both
// PASS here: a terminal transition through UpdateTaskStatus neither bumps
// assignment_epoch nor clears worker_id - deliberately, so the trailing-log
// flush still works - so the assignee's own stale requeue arrives with two
// matching fences. Only the status predicate stands between it and resurrecting
// a finished task.
func TestRequeueTask_TerminalTaskIsNotResurrected(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "rqt-e", f.w1)

	done, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "done", WorkerID: f.w1.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
		FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "done", done.Status)
	require.Equal(t, claimed.AssignmentEpoch, done.AssignmentEpoch,
		"precondition: a terminal transition must not bump the epoch")
	require.True(t, done.WorkerID.Valid,
		"precondition: a terminal transition must not clear the assignee")

	n, err := f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a terminal task must not be resurrected by a requeue")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// F: THE NARROW LIST, PINNED AGAINST BEING WIDENED. RequeueTaskByID's allow-list
// admits 'running'; this statement's deliberately does not, and until this test
// existed a mutation adding 'running' here left the whole suite green despite the
// paragraph in query/tasks.sql explaining why it must not be added.
//
// Kept as its own test rather than a line on the terminal-task one: the two pin
// opposite halves of the same predicate for different reasons, and a single test
// asserting both would not say which one broke.
func TestRequeueTask_RunningTaskIsNotRequeuedByTheSendFailurePath(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "rqt-f", f.w1)

	running, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "running", WorkerID: f.w1.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "running", running.Status)
	require.Equal(t, claimed.AssignmentEpoch, running.AssignmentEpoch,
		"precondition: reporting running must not bump the epoch")

	// Both fence predicates PASS here, so only the status predicate can reject.
	n, err := f.q.RequeueTask(f.ctx, store.RequeueTaskParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n,
		"the send-failure requeue must stay 'dispatched'-only: a running task got its dispatch, so this caller cannot be the one requeueing it")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}
