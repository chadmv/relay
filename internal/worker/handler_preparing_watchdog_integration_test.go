//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm pins the whole point of
// not stamping started_at at `preparing`, end to end: the row is produced by the
// REAL handler from a REAL PREPARING message, not planted.
//
// THE REGRESSION IT CATCHES is handler.go's `if statusStr == "running"` widened
// to accept `preparing` as well: that starts the execution arm's clock at the
// beginning of the workspace sync.
//
// THE ABSOLUTE-ARM LEG IS NOT DECORATION AND MUST NOT BE DELETED. Without it,
// this test is satisfied by ListOverdueAssignedTasks' STATUS predicate excluding
// `preparing` entirely - the opposite regression - and would then stay green
// under the started_at mutation as well. The leg is what proves the row is inside
// the scanned partition, so that the execution arm's silence is a statement about
// started_at rather than about membership.
func TestWatchdog_APreparingTaskIsInvisibleToTheExecutionArm(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "preparing-exec-arm", 0)
	// A one-second timeout: about as hostile to the execution arm as a task can be.
	_, err := pool.Exec(ctx, `UPDATE tasks SET timeout_seconds = 1 WHERE id = $1`, taskID)
	require.NoError(t, err)

	assignedAt := time.Now().Add(-30 * time.Hour)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: assignedAt, Valid: true},
	})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	row, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "preparing", row.Status, "precondition: the handler moved the row")

	// The scan's clock is bound as a parameter, so this test owns it: two days
	// past the claim, with a ZERO margin. Nothing about elapsed time can excuse a
	// row from the execution arm here; only started_at IS NULL can.
	execOnly := store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: false,
		ExecEnabled:     true,
		Now:             pgtype.Timestamptz{Time: time.Now().Add(48 * time.Hour), Valid: true},
		MarginSeconds:   0,
		MaxRows:         100,
	}
	got, err := q.ListOverdueAssignedTasks(ctx, execOnly)
	require.NoError(t, err)
	for _, r := range got {
		assert.NotEqual(t, taskID, r.ID,
			"a preparing task must be invisible to the EXECUTION arm at any margin, because a "+
				"workspace sync legitimately outruns any timeout_sec the task carries. Stamping "+
				"started_at at the preparing transition is what breaks this")
	}

	// POSITIVE CONTROL, and the reason this test is not vacuous: the same row IS
	// in the scanned partition, and the ABSOLUTE arm does reach it.
	absOnly := execOnly
	absOnly.ExecEnabled = false
	absOnly.AbsoluteEnabled = true
	absOnly.AbsoluteCutoff = pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true}
	abs, err := q.ListOverdueAssignedTasks(ctx, absOnly)
	require.NoError(t, err)
	var found bool
	for _, r := range abs {
		if r.ID == taskID {
			found = true
		}
	}
	require.True(t, found,
		"the absolute arm MUST still sweep a preparing task. This leg is what makes the assertion "+
			"above a statement about started_at rather than about the status predicate: without it, "+
			"a ListOverdueAssignedTasks that excluded `preparing` entirely would satisfy this test "+
			"while silently reopening the unbounded-assignment hole for the whole workspace sync")
}

// TestWatchdog_ARunningThenPreparingTaskIsStillSweptByTheExecutionArm composes
// the two halves the sibling tests pin separately: one proves started_at
// survives running -> preparing without asking the scan anything, and the other
// proves a NEVER-RAN preparing row is invisible to the execution arm.
//
// A row that HAS reported running must stay swept. Otherwise a misbehaving
// assignee escapes its own timeout_sec for as long as it likes by sending one
// more PREPARING - which is the unbounded-assignment hole the execution arm
// exists to close, re-opened by a message the fence legitimately accepts.
func TestWatchdog_ARunningThenPreparingTaskIsStillSweptByTheExecutionArm(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "preparing-after-running-exec", 0)
	_, err := pool.Exec(ctx, `UPDATE tasks SET timeout_seconds = 1 WHERE id = $1`, taskID)
	require.NoError(t, err)

	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	row, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "preparing", row.Status, "precondition: the backward transition was accepted")
	require.True(t, row.StartedAt.Valid,
		"precondition: the run's start time survived it. started_at is guarded TWICE - "+
			"handleTaskStatus carries the row's own value forward for every non-running status AND "+
			"UpdateTaskStatus COALESCEs - so either guard alone keeps this assertion green and only "+
			"losing both turns it red")

	// The scan's clock is a parameter, so this test owns it: an hour past a
	// one-second timeout, at zero margin.
	got, err := q.ListOverdueAssignedTasks(ctx, store.ListOverdueAssignedTasksParams{
		AbsoluteEnabled: false,
		ExecEnabled:     true,
		Now:             pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		MarginSeconds:   0,
		MaxRows:         100,
	})
	require.NoError(t, err)
	var found bool
	for _, r := range got {
		if r.ID == taskID {
			found = true
		}
	}
	require.True(t, found,
		"a task that reported running and then went back to preparing must STILL be swept by the "+
			"execution arm, or a misbehaving assignee buys an unbounded assignment for the price of "+
			"one extra PREPARING message. Two independent things hold it up: `preparing` in "+
			"ListOverdueAssignedTasks' status predicate, and started_at surviving the transition. "+
			"Reaching this line means started_at survived - the precondition above covers that half - "+
			"so the status predicate is what moved")
}
