//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingCancelSender is a connected agent that records what it was told.
type capturingCancelSender struct{ taskIDs []string }

func (c *capturingCancelSender) Send(m *relayv1.CoordinatorMessage) error {
	if ct := m.GetCancelTask(); ct != nil {
		c.taskIDs = append(c.taskIDs, ct.TaskId)
	}
	return nil
}

// TestWatchdog_SweepsAHungTaskOnAConnectedWorker is the backlog item's headline
// criterion. The worker is REGISTERED for the whole test - connected, its stream
// healthy, its grace timer unarmed - because the disconnected case is
// GraceRegistry's and a test of it would prove nothing about this fix.
//
// The second half is the criterion that the agent's own terminal update,
// arriving after the sweep, is a silent no-op rather than a resurrection or a
// double-count. It needs no new machinery: UpdateTaskStatus's status allow-list
// already makes a terminal row unwritable.
func TestWatchdog_SweepsAHungTaskOnAConnectedWorker(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "watchdog-e2e", 3)

	// A dependent task, so the cascade has something to act on.
	dependent, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: jobID, Name: "dependent", Commands: []byte(`[["true"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: dependent.ID, DependsOnTaskID: taskID,
	}))

	// Claimed 30 hours ago, running for 29 hours, 60s timeout: both bounds blown,
	// which is what a hung agent looks like.
	_, err = pool.Exec(ctx, `UPDATE tasks SET timeout_seconds = 60 WHERE id = $1`, taskID)
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	_, err = q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "running", WorkerID: w1,
		AssignmentEpoch: claimed.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: time.Now().Add(-29 * time.Hour), Valid: true},
	})
	require.NoError(t, err)

	// The agent is CONNECTED and stays connected. It just never reports terminal.
	cancels := &capturingCancelSender{}
	registry.Register(h.UUIDStringForTest(w1), cancels)

	require.NoError(t, scheduler.
		NewWatchdog(q, registry, broker, 30*time.Minute, 24*time.Hour).
		SweepOnce(ctx))

	swept, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, "timed_out", swept.Status, "the coordinator must end the assignment with no operator action")
	assert.True(t, swept.FinishedAt.Valid, "a swept task must be stamped finished")
	assert.True(t, swept.WorkerID.Valid,
		"the assignment must OUTLIVE the task: trailing log chunks still need the fence")
	assert.Equal(t, claimed.AssignmentEpoch, swept.AssignmentEpoch,
		"a terminal transition must NOT bump the epoch - that would close the trailing-log flush")
	assert.Equal(t, int32(0), swept.RetryCount,
		"the watchdog burns no retry; recovery is POST /v1/jobs/{id}/retry")

	dep, err := q.GetTask(ctx, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", dep.Status,
		"the dependent cascade must run, exactly as for an agent-reported failure")

	job, err := q.GetJob(ctx, jobID)
	require.NoError(t, err)
	assert.Contains(t, []string{"failed", "done"}, job.Status,
		"THE HEADLINE SYMPTOM: the job must now reach a terminal status")

	require.Len(t, cancels.taskIDs, 1,
		"the agent must be told to stop, or the subprocess keeps running orphaned against its workspace")
	assert.Equal(t, h.UUIDStringForTest(taskID), cancels.taskIDs[0])

	// The agent's own terminal update, arriving late at the SAME epoch. Both other
	// fences legitimately pass; only the status allow-list rejects it.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_DONE,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "timed_out", after.Status, "a late terminal update must not resurrect or reclassify a swept task")
	assert.Equal(t, swept.FinishedAt.Time, after.FinishedAt.Time, "and must not restamp finished_at")
	assert.Equal(t, int32(0), after.RetryCount, "and must not burn a retry")

	afterDep, err := q.GetTask(ctx, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", afterDep.Status, "and must not cascade a second time")
}
