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

// TestHandleTaskStatus_APreparingReportMovesTheRowAndLeavesStartedAtNull is the
// feature's headline property and its central obligation, in one test.
//
// The started_at half is the one with the blast radius. ListOverdueAssignedTasks'
// execution arm keys on started_at IS NOT NULL together with timeout_seconds, and
// README's RELAY_TASK_WATCHDOG_MARGIN row states the contract in operator-facing
// words: it applies only to tasks with timeout_sec > 0 that have reported
// running. PREPARING arrives BEFORE the sync starts, so any clock the coordinator
// starts on that message is a clock that runs for the whole sync - a task with a
// 30-minute timeout and a two-hour sync would be swept timed_out mid-sync, with
// no way for the agent to object.
func TestHandleTaskStatus_APreparingReportMovesTheRowAndLeavesStartedAtNull(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "preparing-basic", 0)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.False(t, claimed.StartedAt.Valid, "precondition: a dispatched row has no start time")

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	got, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "preparing", got.Status,
		"the assignee's own PREPARING report at its own epoch must move the row. With no case for it "+
			"the enum switch falls to `default: return` and the row stays dispatched for the whole sync")
	assert.False(t, got.StartedAt.Valid,
		"started_at must stay NULL through preparing. Stamping it here starts the watchdog's "+
			"EXECUTION bound during the workspace sync, so a task with a 30-minute timeout_sec and a "+
			"two-hour sync is swept timed_out mid-sync")
	assert.False(t, got.FinishedAt.Valid, "preparing is not terminal")
	assert.Equal(t, claimed.AssignmentEpoch, got.AssignmentEpoch,
		"a preparing transition ends no generation and must not bump the epoch")
	assert.True(t, got.WorkerID.Valid, "and must not clear the assignee")
}

// TestHandleTaskStatus_APreparingReportAfterRunningDoesNotClearStartedAt pins the
// bound on a backward transition. UpdateTaskStatus's allow-list admits `running`,
// so a SECOND PREPARING message from a task's own assignee at its own current
// epoch moves a running row back to preparing. That is unreachable for a
// well-behaved agent - the runner sends PREPARING once, before Prepare, and never
// after RUNNING - and reachable for a misbehaving one.
//
// It is ACCEPTED and BOUNDED rather than forbidden, because forbidding it needs a
// second writer of tasks.status. The bound is what this test asserts: started_at
// survives via the COALESCE, so the execution arm is unaffected; assigned_at and
// the epoch are untouched, so the absolute arm is unaffected; the row stays in the
// assigned partition everywhere. The damage is a misleading status string on one
// row, driven by that row's own assignee - the "identity is not honesty" shape the
// fence counters already document.
func TestHandleTaskStatus_APreparingReportAfterRunningDoesNotClearStartedAt(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "preparing-backward", 0)
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
	mid, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.True(t, mid.StartedAt.Valid, "precondition: RUNNING stamps a real start time")

	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "preparing", after.Status, "the backward transition is accepted, not refused")
	require.True(t, after.StartedAt.Valid,
		"UpdateTaskStatus COALESCEs started_at, so a backward transition cannot clear the clock the "+
			"execution arm is measured from - which is what bounds this capability")
	assert.WithinDuration(t, mid.StartedAt.Time, after.StartedAt.Time, time.Millisecond)
	assert.Equal(t, claimed.AssignmentEpoch, after.AssignmentEpoch)
}
