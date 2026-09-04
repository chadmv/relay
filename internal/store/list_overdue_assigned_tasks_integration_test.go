//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ts is pgtype.Timestamptz sugar.
func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func i32(v int32) *int32 { return &v }

// overdueFixture drives tasks into states through the production statements. It
// holds the pool as well as the queries because created_at is the one column no
// statement in the repo writes after insert, so backdating it needs raw SQL.
type overdueFixture struct {
	q    *store.Queries
	pool *pgxpool.Pool
	ctx  context.Context
	job  store.Job
	w    store.Worker
	now  time.Time
}

func newOverdueFixture(t *testing.T) *overdueFixture {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := context.Background()
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "overdue-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return &overdueFixture{q: q, pool: pool, ctx: ctx, job: job, w: w, now: time.Now()}
}

// backdateCreatedAt rewrites tasks.created_at directly - the only way to
// represent "this job was submitted a month ago".
func (f *overdueFixture) backdateCreatedAt(t *testing.T, id pgtype.UUID, at time.Time) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx, `UPDATE tasks SET created_at = $2 WHERE id = $1`, id, at)
	require.NoError(t, err)
}

// dispatched creates a task with the given timeout and claims it at assignedAt.
// The row never reaches `running`: started_at stays NULL, which is the state a
// task sits in for the whole workspace sync.
func (f *overdueFixture) dispatched(t *testing.T, name string, timeoutSec *int32, assignedAt time.Time) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), TimeoutSeconds: timeoutSec,
	})
	require.NoError(t, err)
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: f.w.ID, AssignedAt: ts(assignedAt),
	})
	require.NoError(t, err)
	return claimed
}

// preparing drives a claimed row to 'preparing'. started_at stays NULL, which is
// the state a task sits in for the whole workspace sync.
func (f *overdueFixture) preparing(t *testing.T, name string, timeoutSec *int32, assignedAt time.Time) store.Task {
	t.Helper()
	claimed := f.dispatched(t, name, timeoutSec, assignedAt)
	updated, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "preparing", WorkerID: f.w.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	require.NoError(t, err)
	require.False(t, updated.StartedAt.Valid, "precondition: preparing stamps no start time")
	return updated
}

// running additionally drives the row to `running` with the given started_at.
func (f *overdueFixture) running(t *testing.T, name string, timeoutSec *int32, assignedAt, startedAt time.Time) store.Task {
	t.Helper()
	claimed := f.dispatched(t, name, timeoutSec, assignedAt)
	updated, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "running", WorkerID: f.w.ID,
		AssignmentEpoch: claimed.AssignmentEpoch, StartedAt: ts(startedAt),
	})
	require.NoError(t, err)
	return updated
}

// terminal drives a claimed row all the way to a terminal status.
func (f *overdueFixture) terminal(t *testing.T, name, status string, timeoutSec *int32, assignedAt, startedAt time.Time) store.Task {
	t.Helper()
	r := f.running(t, name, timeoutSec, assignedAt, startedAt)
	updated, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: r.ID, Status: status, WorkerID: f.w.ID,
		AssignmentEpoch: r.AssignmentEpoch, StartedAt: r.StartedAt,
		FinishedAt: ts(startedAt.Add(time.Minute)),
	})
	require.NoError(t, err)
	return updated
}

// bothArms is the ordinary production parameter set: 30m margin, 24h cap.
//
// MaxRows is NOT optional and omitting it is not harmless: an unset int32 binds
// 0, which is LIMIT 0, which returns nothing at all. That is fail-closed - the
// watchdog sweeps nobody rather than sweeping the wrong rows - but it is silent,
// so the production caller is guarded separately by
// TestWatchdog_ScanIsBounded, which asserts the bound it binds is
// WatchdogMaxRowsPerSweep.
func (f *overdueFixture) bothArms() store.ListOverdueAssignedTasksParams {
	return store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: true,
		AbsoluteCutoff:  ts(f.now.Add(-24 * time.Hour)),
		ExecEnabled:     true,
		Now:             ts(f.now),
		MarginSeconds:   int64((30 * time.Minute) / time.Second),
		MaxRows:         100,
	}
}

func (f *overdueFixture) list(t *testing.T, p store.ListOverdueAssignedTasksParams) map[pgtype.UUID]bool {
	t.Helper()
	rows, err := f.q.ListOverdueAssignedTasks(f.ctx, p)
	require.NoError(t, err)
	got := make(map[pgtype.UUID]bool, len(rows))
	for _, r := range rows {
		got[r.ID] = true
	}
	return got
}

// TestListOverdueAssignedTasks_ExecutionArm covers the timeout+margin bound.
func TestListOverdueAssignedTasks_ExecutionArm(t *testing.T) {
	f := newOverdueFixture(t)

	// 60s timeout, started 2h ago: 2h > 60s + 30m. Overdue.
	over := f.running(t, "over", i32(60), f.now.Add(-3*time.Hour), f.now.Add(-2*time.Hour))
	// 60s timeout, started 5m ago: 5m < 60s + 30m. Within bound - and this is the
	// long-running-but-healthy control the backlog item asks for.
	under := f.running(t, "under", i32(60), f.now.Add(-3*time.Hour), f.now.Add(-5*time.Minute))

	got := f.list(t, f.bothArms())
	assert.True(t, got[over.ID], "a task past timeout+margin must be returned")
	assert.False(t, got[under.ID], "a task inside timeout+margin must be left alone")
}

// TestListOverdueAssignedTasks_ActivityDoesNotCount is a TRIPWIRE, not coverage,
// and saying so is the point of this comment. No predicate in the statement
// consults task_logs, so deleting the AppendTaskLog call below leaves this test
// reading exactly the same - it does not exercise the execution arm, which
// _ExecutionArm covers.
//
// It is kept deliberately, because "use last activity as the liveness signal" is
// the single most tempting wrong turn available here: a MAX(task_logs.created_at)
// bound is AGENT-CONTROLLED, so a hung-but-chatty agent would look healthy
// forever - the same shape of hole as the started_at re-stamp that COALESCE now
// closes, and the volume needed to exploit it is itself still unbounded. This
// test goes RED the moment somebody wires activity into the scan.
func TestListOverdueAssignedTasks_ActivityDoesNotCount(t *testing.T) {
	f := newOverdueFixture(t)

	chatty := f.running(t, "chatty", i32(60), f.now.Add(-3*time.Hour), f.now.Add(-2*time.Hour))
	_, err := f.q.AppendTaskLog(f.ctx, store.AppendTaskLogParams{
		TaskID: chatty.ID, AssignmentEpoch: chatty.AssignmentEpoch, WorkerID: f.w.ID,
		Stream: "stdout", Content: "still here", MinFinishedAt: ts(f.now.Add(-15 * time.Minute)),
	})
	require.NoError(t, err, "precondition: the assignment still accepts logs")

	got := f.list(t, f.bothArms())
	assert.True(t, got[chatty.ID],
		"a task past its bound is overdue no matter how recently it wrote a log line")
}

// TestListOverdueAssignedTasks_AbsoluteArm covers timeout_sec = 0 and NULL, which
// have no execution bound at all, and the `dispatched` row that never reached
// `running` - the 2026-08-20 amendment's orphaned-assignment case.
func TestListOverdueAssignedTasks_AbsoluteArm(t *testing.T) {
	f := newOverdueFixture(t)

	zero := f.running(t, "zero-timeout", i32(0), f.now.Add(-30*time.Hour), f.now.Add(-29*time.Hour))
	null := f.running(t, "null-timeout", nil, f.now.Add(-30*time.Hour), f.now.Add(-29*time.Hour))
	// AMENDMENT CASE 1: a stale-epoch reconcile put this task into cancelIDs AND
	// marked it reported, so it was neither cancelled server-side nor requeued. It
	// is `dispatched`, worker_id set, epoch non-zero, started_at NULL - and
	// nothing but the absolute arm can ever see it.
	orphan := f.dispatched(t, "never-ran", i32(0), f.now.Add(-30*time.Hour))
	require.False(t, orphan.StartedAt.Valid, "precondition: the orphan never went running")
	require.True(t, orphan.WorkerID.Valid, "precondition: the orphan still names its worker")
	require.NotZero(t, orphan.AssignmentEpoch, "precondition: the orphan has a real epoch")
	fresh := f.dispatched(t, "fresh", i32(0), f.now.Add(-time.Minute))

	got := f.list(t, f.bothArms())
	assert.True(t, got[zero.ID], "timeout_sec=0 must still be bounded by the absolute cap")
	assert.True(t, got[null.ID], "a NULL timeout must still be bounded by the absolute cap")
	assert.True(t, got[orphan.ID], "a dispatched row that never ran must be recovered by the absolute arm")
	assert.False(t, got[fresh.ID], "a freshly dispatched row must be left alone")

	// With the absolute arm OFF, only the execution arm can speak - and it must
	// stay silent about all of them, because `timeout_seconds > 0` is what makes
	// that arm applicable at all. Deleting that predicate would make a zero or
	// NULL timeout swept by the EXECUTION arm the moment `now - started_at > 0 +
	// margin`, which the both-arms run above cannot distinguish from the absolute
	// arm doing its job.
	execOnly := f.bothArms()
	execOnly.AbsoluteEnabled = false
	got = f.list(t, execOnly)
	assert.False(t, got[zero.ID],
		"timeout_sec=0 means NO execution bound; only the absolute arm may sweep it")
	assert.False(t, got[null.ID],
		"a NULL timeout means NO execution bound; only the absolute arm may sweep it")
	assert.False(t, got[orphan.ID], "a row that never went running has no execution bound either")
}

// TestListOverdueAssignedTasks_KeysOnAssignedAtNotCreatedAt is the single row
// that pins the whole reason migration 000021 exists. Without it, a future editor
// "simplifying" the absolute arm to created_at breaks nothing visible.
func TestListOverdueAssignedTasks_KeysOnAssignedAtNotCreatedAt(t *testing.T) {
	f := newOverdueFixture(t)

	// Submitted 30 days ago, queued behind a busy fleet, dispatched one minute
	// ago. Perfectly healthy.
	task := f.dispatched(t, "long-queued", i32(0), f.now.Add(-time.Minute))
	f.backdateCreatedAt(t, task.ID, f.now.Add(-30*24*time.Hour))

	got := f.list(t, f.bothArms())
	assert.False(t, got[task.ID],
		"the absolute bound is measured from assigned_at, never created_at: created_at is JOB SUBMISSION time, "+
			"so keying on it would kill healthy, just-dispatched work that queued behind a busy fleet")
}

// TestListOverdueAssignedTasks_StatusAndAssigneeGuards covers the two predicates
// that keep the scan inside the assigned partition.
func TestListOverdueAssignedTasks_StatusAndAssigneeGuards(t *testing.T) {
	f := newOverdueFixture(t)
	old, older := f.now.Add(-29*time.Hour), f.now.Add(-30*time.Hour)

	done := f.terminal(t, "done", "done", i32(60), older, old)
	failed := f.terminal(t, "failed", "failed", i32(60), older, old)
	timedOut := f.terminal(t, "timed-out", "timed_out", i32(60), older, old)
	// Never claimed: pending, worker_id NULL, epoch 0. created_at is backdated so
	// age is not what excludes it.
	unclaimed, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: "unclaimed", Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), TimeoutSeconds: i32(60),
	})
	require.NoError(t, err)
	f.backdateCreatedAt(t, unclaimed.ID, older)

	// An ASSIGNED-looking row whose worker_id is NULL, planted with raw SQL
	// because no production PATH produces it: the only route is workers'
	// ON DELETE SET NULL, and since 2026-08-26 DeleteWorker does exist - but
	// handleDeleteWorker runs RequeueWorkerTasks first in the same transaction, so
	// every assignment is ended with an epoch bump before the row is released and
	// this state is never reached. It is planted
	// anyway because `worker_id IS NOT NULL` in the scan is otherwise untestable -
	// every row the status allow-list admits happens to carry a worker id, so the
	// predicate can be deleted with the whole suite still green. That row is the
	// one state this watchdog cannot recover (UpdateTaskStatus's worker predicate
	// is a plain, NULL-rejecting `=`), so returning it would buy a guaranteed
	// zero-row round trip every single sweep.
	var orphanNoWorker pgtype.UUID
	require.NoError(t, f.pool.QueryRow(f.ctx, `
		INSERT INTO tasks (job_id, name, commands, env, requires, timeout_seconds, status, worker_id, assigned_at, started_at)
		VALUES ($1, 'no-worker', '[["echo","x"]]'::jsonb, '{}'::jsonb, '{}'::jsonb, 60, 'running', NULL, $2, $3)
		RETURNING id`,
		f.job.ID, older, old).Scan(&orphanNoWorker))

	got := f.list(t, f.bothArms())
	assert.False(t, got[done.ID], "a terminal row is never overdue, however old")
	assert.False(t, got[failed.ID], "a terminal row is never overdue, however old")
	assert.False(t, got[timedOut.ID], "a terminal row is never overdue, however old")
	assert.False(t, got[unclaimed.ID], "a never-claimed pending row is outside the assigned partition")
	assert.False(t, got[orphanNoWorker],
		"a row with a NULL worker_id can never be written by UpdateTaskStatus's plain `=`, so selecting it "+
			"would buy a guaranteed zero-row round trip every sweep")
}

// TestListOverdueAssignedTasks_ArmsAreIndependentlyDisablable proves each arm's
// enable flag switches off exactly its own arm.
func TestListOverdueAssignedTasks_ArmsAreIndependentlyDisablable(t *testing.T) {
	f := newOverdueFixture(t)

	execOnly := f.running(t, "exec-only", i32(60), f.now.Add(-time.Hour), f.now.Add(-50*time.Minute))
	absOnly := f.dispatched(t, "abs-only", i32(0), f.now.Add(-30*time.Hour))

	both := f.list(t, f.bothArms())
	require.True(t, both[execOnly.ID], "precondition")
	require.True(t, both[absOnly.ID], "precondition")

	p := f.bothArms()
	p.ExecEnabled = false
	got := f.list(t, p)
	assert.False(t, got[execOnly.ID], "disabling the execution arm must silence it")
	assert.True(t, got[absOnly.ID], "and must leave the absolute arm firing")

	p = f.bothArms()
	p.AbsoluteEnabled = false
	got = f.list(t, p)
	assert.True(t, got[execOnly.ID], "disabling the absolute arm must leave the execution arm firing")
	assert.False(t, got[absOnly.ID], "and must silence the absolute arm")
}

// TestListOverdueAssignedTasks_LimitTruncatesOldestFirst proves the LIMIT is
// actually wired and that the ORDER BY decides WHICH rows a truncated sweep
// takes. The cap exists to bound the scan-to-write window as much as the batch
// size, and taking the oldest assignments first is what makes the leftovers
// drain deterministically on the next tick instead of starving.
func TestListOverdueAssignedTasks_LimitTruncatesOldestFirst(t *testing.T) {
	f := newOverdueFixture(t)

	oldest := f.dispatched(t, "oldest", i32(0), f.now.Add(-40*time.Hour))
	middle := f.dispatched(t, "middle", i32(0), f.now.Add(-35*time.Hour))
	newest := f.dispatched(t, "newest", i32(0), f.now.Add(-30*time.Hour))

	all := f.list(t, f.bothArms())
	require.Len(t, all, 3, "precondition: all three are overdue")

	p := f.bothArms()
	p.MaxRows = 2
	got := f.list(t, p)
	require.Len(t, got, 2, "the LIMIT must actually bind")
	assert.True(t, got[oldest.ID], "a truncated sweep must take the oldest assignment first")
	assert.True(t, got[middle.ID], "and then the next oldest")
	assert.False(t, got[newest.ID], "and leave the newest for the next tick")
}

// TestListOverdueAssignedTasks_ZeroLimitReturnsNothing pins the failure DIRECTION
// of a caller that forgets MaxRows. LIMIT 0 returns nothing, so the watchdog
// sweeps nobody - fail-closed, which is the right direction, but silent, which is
// why the production call site has its own assertion.
func TestListOverdueAssignedTasks_ZeroLimitReturnsNothing(t *testing.T) {
	f := newOverdueFixture(t)
	f.dispatched(t, "overdue", i32(0), f.now.Add(-30*time.Hour))

	require.Len(t, f.list(t, f.bothArms()), 1, "precondition")

	p := f.bothArms()
	p.MaxRows = 0
	assert.Empty(t, f.list(t, p),
		"an unset MaxRows binds LIMIT 0 and sweeps nobody: fail-closed, but silent")
}

// TestListOverdueAssignedTasks_AbsoluteArmSweepsAPreparingTask is the watchdog
// spec's R3 obligation at the statement level: a workspace sync must remain
// bounded by exactly one arm, the absolute one. Omitting `preparing` from this
// partition reopens the unbounded-assignment hole this statement exists to close,
// for exactly the state that most needs it.
func TestListOverdueAssignedTasks_AbsoluteArmSweepsAPreparingTask(t *testing.T) {
	f := newOverdueFixture(t)

	// timeout_seconds = 0 and started_at NULL: the execution arm cannot see this
	// row at all, so a positive result can only come from the absolute arm.
	syncing := f.preparing(t, "syncing", i32(0), f.now.Add(-30*time.Hour))
	fresh := f.preparing(t, "fresh", i32(0), f.now.Add(-time.Minute))

	got := f.list(t, f.bothArms())
	assert.True(t, got[syncing.ID],
		"a preparing task past RELAY_TASK_MAX_ASSIGNMENT must still be swept. Left out of this "+
			"partition it holds its worker slot and its job forever, unswept, with no log line")
	assert.False(t, got[fresh.ID], "a freshly-preparing row must be left alone")
}
