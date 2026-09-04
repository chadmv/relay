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

// preparingDownTarget is the schema version just below 000023, i.e. the state
// its down migration restores.
const preparingDownTarget = 22

// TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint pins the
// ORDER of the statements in 000023_task_preparing_status.down.sql, which is the
// whole correctness argument of that file.
//
// THE SEEDED ROW IS WHAT MAKES THE ORDER OBSERVABLE. Against an empty tasks
// table the down migration runs whichever way its statements are ordered; with a
// `preparing` row present, a narrowed ADD CONSTRAINT placed before the demoting
// UPDATE makes the migration simply unrunnable. The version of the file that
// "looks right" is the version that has never been run with data.
//
// Demotion is to `dispatched`, not to `pending`, and that is deliberate: the row
// still has a live agent, a worker_id and an assignment_epoch, and `dispatched`
// is what described it truthfully in the narrower vocabulary. Demoting to
// `pending` would end a live assignment without bumping the epoch - an
// epoch-fence violation performed by a migration.
func TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()
	q := store.New(pool)

	// Seed a real `preparing` row through the production statements - create,
	// claim, then the fenced status writer - so the row carries a real assignee
	// and a real epoch rather than being planted.
	user := newTestUser(t, q, false)
	w := newTestWorker(t, q)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "down-23", Priority: "normal", SubmittedBy: user.ID, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "syncing", Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"),
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: w.ID,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	prep, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: "preparing", WorkerID: w.ID,
		AssignmentEpoch: claimed.AssignmentEpoch,
	})
	require.NoError(t, err, "precondition: 000023's widened constraint must admit `preparing`")
	require.Equal(t, "preparing", prep.Status, "precondition")

	require.NoError(t, storeMigrateTo(dsn, preparingDownTarget),
		"the down migration must RUN against a database containing a preparing row. If this fails "+
			"with a check_violation on tasks_status_check, the narrowed ADD CONSTRAINT is placed "+
			"before the demoting UPDATE in 000023_task_preparing_status.down.sql")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "dispatched", after.Status,
		"a preparing row must be demoted to dispatched, which is what described it in the narrower vocabulary")
	assert.True(t, after.WorkerID.Valid, "the demotion must not end the assignment")
	assert.Equal(t, claimed.AssignmentEpoch, after.AssignmentEpoch,
		"and must not bump the epoch: ending an assignment is not this migration's job")

	// The constraint is narrow again.
	_, err = pool.Exec(ctx,
		`INSERT INTO tasks (job_id, name, status) VALUES ($1, 'post-down', 'preparing')`, job.ID)
	require.Error(t, err, "after the down migration tasks_status_check must reject 'preparing'")

	// And so is the index.
	var pred string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT pg_get_expr(i.indpred, i.indrelid)
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'idx_tasks_worker_active'`).Scan(&pred))
	assert.NotContains(t, pred, "preparing",
		"the down migration must restore the NARROW index predicate too; 000023's up widened it, "+
			"so a down that only narrows the constraint leaves the two disagreeing")

	// Re-up must be clean: no duplicate-name collision on the index, constraint back.
	require.NoError(t, store.Migrate(dsn), "re-applying up after down must succeed")
	_, err = pool.Exec(ctx,
		`INSERT INTO tasks (job_id, name, status) VALUES ($1, 'post-up', 'preparing')`, job.ID)
	require.NoError(t, err, "after the re-up tasks_status_check must accept 'preparing' again")
}
