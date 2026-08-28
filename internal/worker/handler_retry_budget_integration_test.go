//go:build integration

package worker_test

import (
	"context"
	"testing"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claimRunAndFail drives one full generation of a task through the handler:
// claim it for w, report RUNNING, then report FAILED. Returns the task row as it
// stands afterwards.
//
// It goes through ClaimTaskForWorker and HandleTaskStatus rather than planting
// rows, so every epoch, every started_at and every retry_count in these tests was
// produced by the production path.
func claimRunAndFail(t *testing.T, ctx context.Context, q *store.Queries, h *worker.Handler,
	taskID, w pgtype.UUID) store.Task {
	t.Helper()
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{ID: taskID, WorkerID: w})
	require.NoError(t, err)
	idStr := h.UUIDStringForTest(taskID)

	h.HandleTaskStatus(ctx, w, &relayv1.TaskStatusUpdate{
		TaskId: idStr, Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch: int64(claimed.AssignmentEpoch),
	})
	running, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "running", running.Status, "fixture: the assignee's RUNNING must land")

	h.HandleTaskStatus(ctx, w, &relayv1.TaskStatusUpdate{
		TaskId: idStr, Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch: int64(claimed.AssignmentEpoch),
	})
	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	return after
}

// TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection
// is the permanent guard on `task.RetryCount < task.Retries` in
// internal/worker/handler.go.
//
// WHAT IT PROTECTS, AND WHY THE SQL PREDICATE DOES NOT MAKE THE GATE REDUNDANT.
// IncrementTaskRetryCount now carries `AND retry_count < retries`, so deleting
// the Go term does NOT let an exhausted task burn another retry. What it does is
// send every budget-exhausted terminal report INTO the retry branch, where the
// refusal arrives as pgx.ErrNoRows and the branch RETURNS - before
// UpdateTaskStatus. The task is then never marked failed, never stamped
// finished_at, never cascades through FailDependentTasks and never recomputes its
// job; it sits `running` until the coordinator watchdog stamps `timed_out` at
// RELAY_TASK_MAX_ASSIGNMENT, 24 hours later by default. Because `retries`
// defaults to 0, that is every ordinary failing task in the system.
//
// THE COUNTER ASSERTION IS THE DISCRIMINATING ONE. The three assertions above it
// are also killed by several existing tests in this package (any of them whose
// positive control drives a retries=0 task to `failed`), so on their own they
// would not tell you this test had done anything the tree did not already do.
// The fence counters are different: classifyStatusFenceRejection labels a row
// that was still writable at T0 `raced`, and a budget-exhausted task is
// `running` - so a gate-less handler puts a steady, deterministic,
// agent-driven, unbudgeted increment onto raced_total, whose published meaning
// (internal/api/server_counters.go) is "a concurrent writer ended the generation
// inside this handler's own read-to-write window" and whose whole operator value
// is that it sits near zero. Nothing else in the tree asserts that.
func TestHandleTaskStatus_AnExhaustedBudgetStillEndsTheTaskAndCountsNoFenceRejection(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	// retries = 1: one generation gets retried, the second is at budget.
	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "budget-gate", 1)

	// GENERATION 1 - the retry is legitimate and must be taken.
	retried := claimRunAndFail(t, ctx, q, h, taskID, w1)
	require.Equal(t, "pending", retried.Status, "fixture: an in-budget failure must requeue the task")
	require.Equal(t, int32(1), retried.RetryCount, "fixture: it must burn exactly one retry")
	require.Equal(t, int32(2), retried.AssignmentEpoch, "fixture: the retry ends the generation")

	// GENERATION 2 - the budget is spent. THE SUBJECT.
	final := claimRunAndFail(t, ctx, q, h, taskID, w1)

	// Non-fatal asserts so a RED run reports every part of the exposure.
	assert.Equal(t, "failed", final.Status,
		"THE HEADLINE: a task whose retry budget is spent must END TERMINAL. Not 'retry_count stopped "+
			"moving' - the SQL predicate alone gives you that while the row sits running for 24 hours.")
	assert.True(t, final.FinishedAt.Valid,
		"a terminal transition must stamp finished_at, which is what every duration and every 'when did "+
			"this end' read in the product depends on")
	assert.Equal(t, int32(1), final.RetryCount,
		"and it must not have burned a retry it does not have")
	assert.Equal(t, int32(3), final.AssignmentEpoch,
		"a terminal transition must NOT bump the epoch - that would close the trailing-log flush")

	assert.Equal(t, worker.TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"THE DISCRIMINATING ASSERTION. A budget exhaustion is the NORMAL end of a task's life: "+
			"deterministic, single-writer, and nothing to do with a concurrent writer. It must never "+
			"reach a fence counter. Without the Go gate it lands on raced_total, which "+
			"GET /v1/server/counters publishes as a FLOOR on concurrent-writer activity - a signal whose "+
			"entire value is that it sits near zero, driven steadily by every failing task in the fleet.")
}
