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

// TestRequeueTaskByID_DoesNotTearOffAFreshAssignment is the repro from
// bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence, at the layer
// where the bug actually lives.
//
// Two overlapping registrations B and C of the same worker W1 both read
// {task: epoch 1} from GetActiveTasksForWorker. B requeues, the dispatcher hands
// the task to W2, and then C writes - still walking its OWN snapshot. Before the
// fence, C's write matched on nothing but the id and a status allow-list that
// admits 'dispatched', so it tore a task off a worker that had only just been
// given it, bumped the epoch, and left W2's subprocess running with every
// subsequent message it sent fenced out in silence.
//
// The handler is not involved: it contributes no predicate C could fail. What
// makes C distinguishable from B is the two values it carries, and that is a
// property of the statement.
func TestRequeueTaskByID_DoesNotTearOffAFreshAssignment(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Rex", "rex@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-fence", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)

	w1, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "rqid-fence-1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w2, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w2", Hostname: "rqid-fence-2", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "t", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// The state both B and C read: assigned to W1 at epoch 1.
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// B requeues first, carrying the epoch and worker it read. This is the
	// LEGITIMATE call and it must succeed.
	nB, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: task.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), nB, "B's requeue is current and must move the row")

	// The dispatcher claims it for a DIFFERENT worker.
	redispatched, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w2.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), redispatched.AssignmentEpoch)

	// C writes, still holding epoch 1 and worker W1 from its own snapshot. It must
	// move nothing: the epoch is stale AND the task belongs to somebody else now.
	nC, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: task.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: w1.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), nC, "a stale reconcile must move zero rows")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status,
		"a stale reconcile must not requeue a task that has been re-dispatched")
	require.True(t, after.WorkerID.Valid, "the fresh assignment must survive")
	assert.Equal(t, w2.ID.Bytes, after.WorkerID.Bytes,
		"the task must still belong to the worker it was re-dispatched to")
	assert.Equal(t, int32(3), after.AssignmentEpoch,
		"a rejected requeue must not bump the epoch")
}

// requeueFence is one job, two real workers, and a pool, so a test can both use
// the production statements and plant a state they cannot reach.
type requeueFence struct {
	q   *store.Queries
	ctx context.Context
	job store.Job
	w1  store.Worker
	w2  store.Worker
}

func newRequeueFence(t *testing.T) *requeueFence {
	t.Helper()
	q := newTestQueries(t)
	ctx := context.Background()
	user := makeTestUser(t, q, ctx, "Fen", "fen@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-fence", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w1, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w1", Hostname: "rqid-w1", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w2, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "w2", Hostname: "rqid-w2", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	return &requeueFence{q: q, ctx: ctx, job: job, w1: w1, w2: w2}
}

// claimedBy creates a task and claims it for the given worker, THROUGH the
// production statement, so the row carries a real assignee and a real epoch.
func (f *requeueFence) claimedBy(t *testing.T, name string, w store.Worker) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
	})
	require.NoError(t, err)
	return claimed
}

// A: THE EPOCH PREDICATE, ISOLATED. The worker predicate and the status
// allow-list both PASS here - the task is still assigned to W1 and still
// 'dispatched' - so only the epoch can reject. This is the case the repro cannot
// distinguish, because there the assignee changed too.
func TestRequeueTaskByID_StaleEpochMovesZeroRowsEvenForTheSameWorker(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "a", f.w1)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// A legitimate requeue by the assignee, then a re-claim by the SAME worker.
	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
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
	n, err = f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
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
// first assignment, epoch 1) and the status allow-list passes, so only the
// assignee predicate can reject. This is the "the epoch establishes currency,
// not identity" case: W1 holds a perfectly current epoch for a task that is not
// its own.
func TestRequeueTaskByID_WrongWorkerMovesZeroRows(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "b", f.w2)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
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
// `worker_id = NULL` is never true, so it fails closed.
//
// READ THIS TOGETHER WITH TEST D. This case is rejected under BOTH `=` and
// `IS NOT DISTINCT FROM`, because the row's worker_id is non-NULL, so it does
// NOT pin the operator. It pins the behaviour a real caller would hit. D is the
// one that pins the operator.
func TestRequeueTaskByID_ZeroValueWorkerIDMovesZeroRows(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "c", f.w1)

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
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
// 'pending' or a terminal value, and the schema reaches it only through workers'
// ON DELETE SET NULL, which nothing in this repo triggers because nothing DELETEs
// a worker. So the status allow-list is a second guarantee standing behind this
// one. This test exists so that if the allow-list ever widens, the operator
// choice is already pinned rather than rediscovered.
//
// AppendTaskLog's equivalent (store_test.go, "NULL matching NULL") gets the state
// for free from a never-claimed task, because its allow-list admits 'pending'.
// This statement's does not.
func TestRequeueTaskByID_NullWorkerIDDoesNotMatchANullArgument(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Nel", "nel@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "rqid-null", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
	})
	require.NoError(t, err)
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: "wn", Hostname: "rqid-null-w", CpuCores: 1, RamGb: 1, Os: "linux",
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
	n, err := q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: planted.AssignmentEpoch, WorkerID: pgtype.UUID{},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "NULL worker_id must not match a NULL worker id argument")

	after, err := q.GetTask(ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// E: THE STATUS ALLOW-LIST, ISOLATED. The epoch AND the worker predicate both
// PASS here, which is the whole point: a terminal transition through
// UpdateTaskStatus neither bumps assignment_epoch nor clears worker_id -
// deliberately, so the trailing-log flush still works - so the assignee's own
// stale reconcile arrives with two matching fences. Only the allow-list stands
// between it and resurrecting a finished task.
func TestRequeueTaskByID_TerminalTaskIsNotResurrected(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "e", f.w1)

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

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a terminal task must not be resurrected by a requeue")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", after.Status)
	assert.Equal(t, int32(1), after.AssignmentEpoch)
}

// F: THE POSITIVE ARM OF THE STATUS ALLOW-LIST, AND RECONCILE'S DOMINANT CASE.
// A through E all pin what the fence REJECTS. Nothing pinned what it must
// ACCEPT, and the hole was in the arm that matters most: narrowing the emitted
// predicate from IN ('dispatched','running') to IN ('dispatched') left the
// store, worker, scheduler and api suites ALL GREEN.
//
// That is not a cosmetic gap. GetActiveTasksForWorker returns dispatched AND
// running, so a task the agent was genuinely executing and no longer reports on
// reconnect is 'running' in the database - the single most common thing this
// statement exists to requeue. Drop 'running' and every one of them silently
// stays 'running' against a worker that is not executing it: no requeue, no log
// line, left for the watchdog, with CI green.
//
// TestRegisterWorker_ReconcilesRunningTasks does NOT cover this despite its
// name - all three of its fixture tasks are left 'dispatched' by
// ClaimTaskForWorker and none is ever transitioned to 'running'.
func TestRequeueTaskByID_RequeuesARunningTaskForItsAssignee(t *testing.T) {
	f := newRequeueFence(t)

	claimed := f.claimedBy(t, "rqid-f", f.w1)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	// The agent picked it up and reported running. Note this bumps NOTHING: the
	// epoch and the assignee are unchanged, so the assignee's own reconcile still
	// carries two matching fences and only the status arm decides.
	running, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "running", WorkerID: f.w1.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "running", running.Status)
	require.Equal(t, claimed.AssignmentEpoch, running.AssignmentEpoch,
		"precondition: reporting running must not bump the epoch")

	n, err := f.q.RequeueTaskByID(f.ctx, store.RequeueTaskByIDParams{
		ID: claimed.ID, AssignmentEpoch: claimed.AssignmentEpoch, WorkerID: f.w1.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), n,
		"a RUNNING task the agent no longer reports must still be requeued - this is reconcile's dominant case")

	after, err := f.q.GetTask(f.ctx, claimed.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", after.Status)
	assert.False(t, after.WorkerID.Valid, "the assignment must be released")
	assert.Equal(t, int32(2), after.AssignmentEpoch, "a matched requeue bumps the epoch")
}
