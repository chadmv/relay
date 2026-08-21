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
	assert.Equal(t, "failed", job.Status,
		"THE HEADLINE SYMPTOM: the job must now reach a terminal status - and `failed` specifically. "+
			"RecomputeJobStatus returns `done` only when EVERY task is done, and these are timed_out and failed, "+
			"so accepting `done` here would green-light a recompute bug reporting a job of timed-out tasks as a success")

	require.Len(t, cancels.taskIDs, 1,
		"the agent must be told to stop, or the subprocess keeps running orphaned against its workspace")
	assert.Equal(t, h.UUIDStringForTest(taskID), cancels.taskIDs[0])

	// The agent's own terminal update, arriving late at the SAME epoch. Both other
	// fences legitimately pass; only the status allow-list rejects it.
	//
	// FAILED, not DONE, and the difference is the whole point of the retry
	// assertion below. handler.go computes `terminal := statusStr == "failed" ||
	// statusStr == "timed_out"`, so a DONE never reaches the retry branch at all -
	// RetryCount would read 0 whether or not the fence worked, and the assertion
	// would be vacuous. FAILED is also what a real agent sends after the
	// force=false cancel this sweep just issued. It DOES reach the retry branch,
	// where IncrementTaskRetryCount's own status allow-list rejects the now-
	// terminal row, which is the property worth pinning: the task carries
	// Retries: 3 and must still burn none.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_FAILED,
		Epoch:  int64(claimed.AssignmentEpoch),
	})

	after, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "timed_out", after.Status,
		"a late terminal update must not resurrect or reclassify a swept task - the row must stay timed_out, not "+
			"become the failed the agent just reported")
	assert.Equal(t, swept.FinishedAt.Time, after.FinishedAt.Time, "and must not restamp finished_at")
	assert.Equal(t, int32(0), after.RetryCount,
		"and must not burn a retry: the task has Retries: 3 and a FAILED report DOES reach the retry branch, "+
			"so this only reads 0 because IncrementTaskRetryCount's status allow-list rejects the terminal row")

	afterDep, err := q.GetTask(ctx, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", afterDep.Status, "and must not cascade a second time")
}

// TestWatchdog_SweepsADispatchedOrphan is the 2026-08-20 amendment's headline
// case and an explicit acceptance criterion of the backlog item: a stale-epoch
// reconcile puts a task into cancelIDs AND marks it reported, so it is neither
// cancelled server-side nor requeued, and sits `dispatched` with worker_id
// pointing at a worker that has been told to abandon it. Nothing else in the tree
// sweeps it - grace needs a disconnect, and this row's agent may still be
// connected and simply no longer holding the task.
//
// It never reached `running`, so started_at is NULL and the EXECUTION arm cannot
// see it at all: only the absolute arm recovers this state. Every other watchdog
// test drives a `running` row, which is why this one has to exist rather than
// being asserted in a comment.
//
// The worker is deliberately NOT registered. SendCancel fails for a disconnected
// worker and the sweep must not care: it is best-effort by construction, and the
// watchdog is registry-blind when deciding to write.
func TestWatchdog_SweepsADispatchedOrphan(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	jobID, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "watchdog-orphan", 3)

	// timeout_seconds = 0 is the documented "no deadline", so even if this row had
	// reached `running` the execution arm would decline it. Nothing but the
	// absolute arm can end this assignment.
	_, err := pool.Exec(ctx, `UPDATE tasks SET timeout_seconds = 0 WHERE id = $1`, taskID)
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "dispatched", claimed.Status, "precondition: the orphan never went running")
	require.False(t, claimed.StartedAt.Valid, "precondition: a dispatched row has no start time")
	require.NotZero(t, claimed.AssignmentEpoch, "precondition: the orphan has a real epoch")

	require.NoError(t, scheduler.
		NewWatchdog(q, registry, broker, 30*time.Minute, 24*time.Hour).
		SweepOnce(ctx),
		"a cancel that cannot be delivered to a disconnected worker is best-effort, not a sweep failure")

	swept, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "timed_out", swept.Status,
		"a dispatched row with no holder must be recovered by the absolute arm; nothing else in the tree sweeps it")
	assert.True(t, swept.FinishedAt.Valid)
	assert.False(t, swept.StartedAt.Valid,
		"a task that never ran must stay without a start time: UpdateTaskStatus COALESCEs started_at, so the "+
			"watchdog's NULL neither writes nor invents one")
	assert.Equal(t, claimed.AssignmentEpoch, swept.AssignmentEpoch,
		"a terminal transition must NOT bump the epoch")

	job, err := q.GetJob(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, "failed", job.Status, "the job must reach a terminal status")
	_ = h
}

// TestWatchdog_DoesNotClobberAStartedAtStampedInsideItsWindow is item 2 driven
// end to end through the production statements rather than at the statement
// level. The watchdog reads a `dispatched` row (started_at NULL), the agent wins
// the race and reports RUNNING - which legitimately passes all three fences,
// because 'running' does not bump the epoch - and the watchdog's write lands
// afterwards carrying the NULL it read. The real start time must survive, or the
// row ends up `timed_out` with a finished_at and no start time, and every
// MIN(started_at) job-enrichment query renders the job as never started.
func TestWatchdog_DoesNotClobberAStartedAtStampedInsideItsWindow(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	registry := worker.NewRegistry()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, registry, broker, func() {})

	_, taskID, w1, _ := seedTaskAndTwoWorkers(t, ctx, q, "watchdog-window", 0)
	_, err := pool.Exec(ctx, `UPDATE tasks SET timeout_seconds = 0 WHERE id = $1`, taskID)
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: taskID, WorkerID: w1,
		AssignedAt: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	require.False(t, claimed.StartedAt.Valid)

	// The agent reports RUNNING through the real handler, inside what would be the
	// watchdog's scan-to-write window.
	h.HandleTaskStatus(ctx, w1, &relayv1.TaskStatusUpdate{
		TaskId: h.UUIDStringForTest(taskID),
		Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
		Epoch:  int64(claimed.AssignmentEpoch),
	})
	mid, err := q.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.True(t, mid.StartedAt.Valid, "precondition: the agent stamped a real start time")

	// The sweep now writes, still carrying the NULL its scan read. SweepOnce
	// re-scans, so drive UpdateTaskStatus directly with the pre-race row - that IS
	// the stale value the watchdog would hold.
	swept, err := q.UpdateTaskStatus(ctx, store.UpdateTaskStatusParams{
		ID: taskID, Status: "timed_out", WorkerID: w1,
		AssignmentEpoch: claimed.AssignmentEpoch,
		StartedAt:       claimed.StartedAt,
		FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.True(t, swept.StartedAt.Valid,
		"the agent's real start time must survive the watchdog's stale NULL")
	assert.WithinDuration(t, mid.StartedAt.Time, swept.StartedAt.Time, time.Millisecond)
	assert.True(t, swept.StartedAt.Time.Before(swept.FinishedAt.Time),
		"and it must precede finished_at, which is why the sweep reads its clock per row")
}
