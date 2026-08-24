package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fenceStore is a failClaimedStore that fails UpdateTaskStatus with a chosen
// error. Its cascade methods panic: failClaimedTask must not reach them when the
// write did not land, and a plausible zero value would hide that.
type fenceStore struct{ err error }

func (f fenceStore) UpdateTaskStatus(context.Context, store.UpdateTaskStatusParams) (store.Task, error) {
	return store.Task{}, f.err
}

func (fenceStore) FailDependentTasks(context.Context, pgtype.UUID) error {
	panic("fenceStore: a rejected write must not cascade")
}

func (fenceStore) RecomputeJobStatus(context.Context, pgtype.UUID) (string, error) {
	panic("fenceStore: a rejected write must not recompute the job")
}

func claimedFixture() store.Task {
	return store.Task{
		ID:              makeUUID(1),
		JobID:           makeUUID(99),
		WorkerID:        makeUUID(200),
		AssignmentEpoch: 7,
		StartedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

// TestFailClaimedTask_AFenceRejectionIsNotLoggedAsAnError.
//
// failClaimedTask is the FIFTH of relay's Go-side fence-rejection sites and was
// the only one that did not distinguish pgx.ErrNoRows. The other four are
// handleTaskLog's AppendTaskLog arm, handleTaskStatus's IncrementTaskRetryCount
// and UpdateTaskStatus arms, and Watchdog.SweepOnce.
//
// ErrNoRows here means another writer ended this assignment between the claim
// and this write. That is the correct outcome, not a failure, and logging it
// reported a database error for a race the design expects.
//
// NOTHING IS LOST BY THE SILENCE, and that was checked rather than assumed:
// failClaimedTask's FIRST statement is an unconditional, unbudgeted
// "dispatch: failing task ... terminally: ..." line emitted BEFORE the write on
// every attempt, so a poison task being re-claimed in a loop is still visible
// through that line whatever the write does. Both legs assert it is present, so
// a fix that silenced the whole function would be RED.
func TestFailClaimedTask_AFenceRejectionIsNotLoggedAsAnError(t *testing.T) {
	logged := captureLog(t)
	broker := events.NewBroker()

	t.Run("a fence rejection is silent", func(t *testing.T) {
		failClaimedTask(context.Background(), fenceStore{err: pgx.ErrNoRows},
			broker, claimedFixture(), "bad commands JSON")

		out := logged()
		assert.Contains(t, out, "failing task",
			"the attempt line is unconditional and must stay - it is what keeps a re-claim loop visible")
		assert.NotContains(t, out, "UpdateTaskStatus(failed)",
			"a fence rejection is the correct outcome and must not be reported as a database error")
	})

	t.Run("a real error still speaks", func(t *testing.T) {
		before := len(logged())
		failClaimedTask(context.Background(), fenceStore{err: errors.New("connection reset")},
			broker, claimedFixture(), "bad source JSON")

		out := logged()[before:]
		require.Contains(t, out, "UpdateTaskStatus(failed)")
		assert.Contains(t, out, "connection reset",
			"suppressing ErrNoRows must not suppress a genuine failure - asserted through the error's "+
				"own text, which no other arm of this function produces")
	})
}
