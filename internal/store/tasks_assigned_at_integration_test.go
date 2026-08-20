//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assignedAtDownTarget is the schema version just below 000021, i.e. the state
// its down migration restores.
const assignedAtDownTarget = 20

// TestMigration000021AddsAssignedAt asserts the column exists after the full up
// migration, is nullable, and is TIMESTAMPTZ.
func TestMigration000021AddsAssignedAt(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var dataType, isNullable string
	err := pool.QueryRow(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'tasks' AND column_name = 'assigned_at'`,
	).Scan(&dataType, &isNullable)
	require.NoError(t, err, "tasks.assigned_at must exist after migration 000021")
	assert.Equal(t, "timestamp with time zone", dataType)
	assert.Equal(t, "YES", isNullable,
		"assigned_at must be nullable: NULL is how a task says it holds no assignment")
}

// TestMigration000021DownUp confirms the down migration drops the column and
// migrating back up round-trips cleanly (no duplicate-column collision).
func TestMigration000021DownUp(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()

	countColumn := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name='tasks' AND column_name='assigned_at'`,
		).Scan(&n))
		return n
	}

	require.NoError(t, store.MigrateTo(dsn, assignedAtDownTarget),
		"down migration to 000020 must succeed")
	assert.Equal(t, 0, countColumn(), "down must drop tasks.assigned_at")

	require.NoError(t, store.Migrate(dsn), "re-applying up must succeed")
	assert.Equal(t, 1, countColumn(), "up must restore tasks.assigned_at")
}

// assignedFixture is one job, one worker, and helpers that put a task into a
// state THROUGH THE PRODUCTION STATEMENTS - claim, then the fenced
// UpdateTaskStatus - so a planted row carries a real assignee, a real epoch and
// a real assigned_at.
type assignedFixture struct {
	q   *store.Queries
	ctx context.Context
	job store.Job
	w   store.Worker
}

func newAssignedFixture(t *testing.T) *assignedFixture {
	t.Helper()
	q := newTestQueries(t)
	ctx := context.Background()
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "assigned-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return &assignedFixture{q: q, ctx: ctx, job: job, w: w}
}

// claimedAt creates a task and claims it with the given assigned_at.
func (f *assignedFixture) claimedAt(t *testing.T, name string, at time.Time) store.Task {
	t.Helper()
	task, err := f.q.CreateTask(f.ctx, store.CreateTaskParams{
		JobID: f.job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 1,
	})
	require.NoError(t, err)
	claimed, err := f.q.ClaimTaskForWorker(f.ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: f.w.ID,
		AssignedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	require.NoError(t, err)
	require.True(t, claimed.AssignedAt.Valid, "precondition: the claim stamps assigned_at")
	return claimed
}

func (f *assignedFixture) get(t *testing.T, id pgtype.UUID) store.Task {
	t.Helper()
	got, err := f.q.GetTask(f.ctx, id)
	require.NoError(t, err)
	return got
}

// TestAssignedAtIsClearedWhereverWorkerIDIs pins the rule this column lives by:
// assigned_at is written exactly where worker_id is written. Correctness does not
// depend on the clearing half (ClaimTaskForWorker is the only route back into the
// scanned partition and it always overwrites), but the property "assigned_at IS
// NOT NULL means currently assigned" does, and that is what the next reader will
// rely on. Every arm asserts BOTH columns so a statement that clears one and not
// the other is a failure, not silent drift.
func TestAssignedAtIsClearedWhereverWorkerIDIs(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)

	t.Run("RequeueTask", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rq", old)
		require.NoError(t, f.q.RequeueTask(f.ctx, task.ID))
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueTask must clear assigned_at")
	})

	t.Run("RequeueTaskByID", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rqid", old)
		require.NoError(t, f.q.RequeueTaskByID(f.ctx, task.ID))
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueTaskByID must clear assigned_at")
	})

	t.Run("RequeueWorkerTasks", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rqw", old)
		ids, err := f.q.RequeueWorkerTasks(f.ctx, f.w.ID)
		require.NoError(t, err)
		require.Len(t, ids, 1)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueWorkerTasks must clear assigned_at")
	})

	t.Run("RequeueWorkerTasksIfEpoch", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "rqwe", old)
		ids, err := f.q.RequeueWorkerTasksIfEpoch(f.ctx, store.RequeueWorkerTasksIfEpochParams{
			WorkerID: f.w.ID, ConnectionEpoch: f.w.ConnectionEpoch,
		})
		require.NoError(t, err)
		require.Len(t, ids, 1)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RequeueWorkerTasksIfEpoch must clear assigned_at")
	})

	t.Run("IncrementTaskRetryCount", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "retry", old)
		_, err := f.q.IncrementTaskRetryCount(f.ctx, store.IncrementTaskRetryCountParams{
			ID: task.ID, AssignmentEpoch: task.AssignmentEpoch, WorkerID: f.w.ID,
		})
		require.NoError(t, err)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "IncrementTaskRetryCount must clear assigned_at")
	})

	t.Run("CancelJobTasks", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "cancel", old)
		require.NoError(t, f.q.CancelJobTasks(f.ctx, f.job.ID))
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid,
			"CancelJobTasks must clear assigned_at (the spec's own list omitted it)")
	})

	t.Run("RetryJobTasks", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "operator-retry", old)
		_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
			ID: task.ID, Status: "failed", WorkerID: f.w.ID,
			AssignmentEpoch: task.AssignmentEpoch,
			FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		require.NoError(t, err)
		reopened, err := f.q.RetryJobTasks(f.ctx, store.RetryJobTasksParams{
			JobID: f.job.ID, IncludeDone: false,
		})
		require.NoError(t, err)
		require.Len(t, reopened, 1)
		after := f.get(t, task.ID)
		assert.False(t, after.WorkerID.Valid)
		assert.False(t, after.AssignedAt.Valid, "RetryJobTasks must clear assigned_at")
	})

	t.Run("UpdateTaskStatus leaves assigned_at ALONE", func(t *testing.T) {
		f := newAssignedFixture(t)
		task := f.claimedAt(t, "terminal", old)
		_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
			ID: task.ID, Status: "done", WorkerID: f.w.ID,
			AssignmentEpoch: task.AssignmentEpoch,
			FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		require.NoError(t, err)
		after := f.get(t, task.ID)
		require.True(t, after.AssignedAt.Valid,
			"a terminal transition must NOT clear assigned_at: the assignment deliberately outlives the task "+
				"so trailing log chunks still pass AppendTaskLog's fence, and assigned_at is part of that record")
		assert.WithinDuration(t, old, after.AssignedAt.Time, time.Second)
		assert.True(t, after.WorkerID.Valid, "and it must not clear worker_id either")
	})
}
