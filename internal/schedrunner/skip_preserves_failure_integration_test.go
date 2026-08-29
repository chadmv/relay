//go:build integration

package schedrunner_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTickOnce_ASkippedFireDoesNotClearARecordedFailure pins the one row of the
// clearing table that has no other witness.
//
// WHY IT IS NOT OBVIOUS. Before this slice the skip branch and the success
// branch shared ONE statement (AdvanceScheduledJob), distinguished only by
// whether the last_job_id parameter carried a value. A "clear the failure
// whenever AdvanceScheduledJob runs" implementation would therefore have cleared
// on skip - and the skip branch returns BEFORE jobspec.Validate runs, so
// reaching it is no evidence at all that the stored spec is valid. A poisoned
// schedule with a long-running predecessor would flicker between "failing" and
// "healthy" on alternate ticks, which is worse than the invisibility this slice
// exists to fix.
//
// It also documents, in passing, that last_run_at moves on the skip path. That
// means last_run_at has always meant "the runner reached the end of a fire
// attempt", not "a job was produced". Pre-existing, filed separately, asserted
// here so the split into two statements did not silently change it.
func TestTickOnce_ASkippedFireDoesNotClearARecordedFailure(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "skip-preserve@example.com")

	sched, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "skip-preserve", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// Tick once to produce a job. It stays pending (no agent runs here), which is
	// what CountActiveJobsForSchedule counts, so the NEXT tick takes the skip
	// branch.
	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))
	jobs, err := h.q.ListJobsByScheduledJob(ctx, sched.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1, "precondition: the first tick must produce the job that makes the second one skip")

	// Plant a recorded failure and make the row eligible again. Planted rather
	// than produced, because producing one requires a poisoned spec and a
	// poisoned spec never reaches the skip branch - which is exactly why this
	// case has no other witness.
	planted := "task t: retries must be between 0 and 10"
	_, err = h.pool.Exec(ctx, `
		UPDATE scheduled_jobs
		   SET last_error = $1, last_error_at = NOW(), next_run_at = NOW() - INTERVAL '1 second'
		 WHERE id = $2`, planted, sched.ID)
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	after, err := h.q.ListJobsByScheduledJob(ctx, sched.ID)
	require.NoError(t, err)
	require.Len(t, after, 1, "the second tick must have SKIPPED, not fired, or this test proves nothing")

	row, err := h.q.GetScheduledJob(ctx, sched.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastError,
		"a SKIPPED fire must not clear the failure record: it returns before jobspec.Validate and is "+
			"therefore no evidence the stored spec is valid")
	assert.Equal(t, planted, *row.LastError)
	assert.True(t, row.LastErrorAt.Valid, "and it must not clear the timestamp either")
	assert.True(t, row.LastRunAt.Valid,
		"the skip path DOES stamp last_run_at, as it always has - recorded so the statement split "+
			"is visibly behaviour-preserving on this axis")
}
