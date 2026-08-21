//go:build integration

package store_test

import (
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateTaskStatus_RunningDoesNotRestampStartedAt closes the hole that
// defeated the watchdog's execution arm. handleTaskStatus stamps
// startedAt = time.Now() on EVERY TASK_STATUS_RUNNING, and this statement's
// allow-list admits 'running', so a running -> running write used to re-stamp
// the column. Both other fences pass trivially for the assignee - it is its own
// worker id, at its own epoch - and AgentMessage_TaskStatus is dispatched
// unbudgeted, so an agent with timeout_seconds=60 emitting one RUNNING every ten
// minutes kept `now - started_at` under the bound forever. The value was always a
// server clock; the TRIGGER was the agent's, which is what the arm could not
// survive.
//
// started_at is now write-once per assignment: COALESCE keeps the first value
// and every statement that legitimately needs a fresh one NULLs it in its own
// SET clause first (RequeueTask, RequeueTaskByID, RequeueWorkerTasks,
// RequeueWorkerTasksIfEpoch, IncrementTaskRetryCount, RetryJobTasks), so no
// caller ever needs to clear it through here.
func TestUpdateTaskStatus_RunningDoesNotRestampStartedAt(t *testing.T) {
	f := newAssignedFixture(t)
	task := f.claimedAt(t, "restamp", time.Now().Add(-3*time.Hour))

	first := time.Now().Add(-2 * time.Hour)
	running, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: task.ID, Status: "running", WorkerID: f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: first, Valid: true},
	})
	require.NoError(t, err)
	require.True(t, running.StartedAt.Valid, "precondition: the first running transition stamps started_at")
	assert.WithinDuration(t, first, running.StartedAt.Time, time.Second,
		"the FIRST running transition must set the column - COALESCE must not make it write-never")

	// The same assignee, at the same epoch, says RUNNING again ten minutes later.
	// This is the exact message a wedged agent replays to stay under its bound.
	again, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: task.ID, Status: "running", WorkerID: f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err, "the write still matches - only the started_at column is protected")
	assert.WithinDuration(t, first, again.StartedAt.Time, time.Second,
		"a second RUNNING must NOT re-stamp started_at: the execution arm measures now - started_at, so an agent "+
			"that can reset that clock on demand is not bounded by it at all")
}

// TestUpdateTaskStatus_DoesNotClobberAStartedAtItDidNotRead is the same root
// cause in the opposite direction. The watchdog reads a `dispatched` row whose
// started_at is NULL, and writes with that NULL bound. If the agent reports
// running inside the scan-to-write window, that transition legitimately passes
// all three fences (same epoch - running does not bump it - same worker,
// non-terminal status) and stamps a real started_at. The stale NULL used to
// overwrite it, leaving a timed_out row with a finished_at and no start time,
// which renders as a never-started job with no duration everywhere MIN(started_at)
// is read.
func TestUpdateTaskStatus_DoesNotClobberAStartedAtItDidNotRead(t *testing.T) {
	f := newAssignedFixture(t)
	task := f.claimedAt(t, "clobber", time.Now().Add(-30*time.Hour))
	require.False(t, task.StartedAt.Valid, "precondition: a dispatched row has no start time")

	// The watchdog's scan happens HERE, reading started_at = NULL.
	staleStartedAt := task.StartedAt

	// The agent wins the race and reports running.
	agentStart := time.Now().Add(-time.Minute)
	_, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: task.ID, Status: "running", WorkerID: f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
		StartedAt:       pgtype.Timestamptz{Time: agentStart, Valid: true},
	})
	require.NoError(t, err)

	// The watchdog's write lands with the value it read a tick earlier.
	swept, err := f.q.UpdateTaskStatus(f.ctx, store.UpdateTaskStatusParams{
		ID: task.ID, Status: "timed_out", WorkerID: f.w.ID,
		AssignmentEpoch: task.AssignmentEpoch,
		StartedAt:       staleStartedAt,
		FinishedAt:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
	require.True(t, swept.StartedAt.Valid,
		"a stale NULL read before the agent's running transition must not clobber the real start time")
	assert.WithinDuration(t, agentStart, swept.StartedAt.Time, time.Second)
}
