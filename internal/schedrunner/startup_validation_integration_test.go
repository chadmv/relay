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

// TestValidateStoredSpecsOnStartup covers the hole this slice would otherwise
// leave aimed precisely at its own audience.
//
// ReconcileOnStartup advances next_run_at past missed triggers (never-catch-up),
// so without this sweep a schedule broken by a retroactive validation change
// records NOTHING until its next scheduled fire - up to a day for @daily, up to
// a month for @monthly. The population most likely to be broken right now is
// exactly the population of long-cadence schedules nobody has looked at
// recently.
//
// FOUR PROPERTIES, and the last two are the ones an implementation gets wrong:
//   - a broken enabled schedule that is NOT overdue is recorded. This is the
//     whole point: ListEligibleScheduledJobs and ListOverdueScheduledJobsForCatchup
//     both require next_run_at to have passed, so neither loop sees this row.
//   - a healthy schedule is left alone.
//   - next_run_at DOES NOT MOVE. ReconcileOnStartup owns never-catch-up; a
//     second statement advancing it would skip a fire the operator was entitled
//     to.
//   - the sweep NEVER CLEARS. A spec that validates at boot has not been proven
//     to FIRE - the insert could still fail - so clearing here would assert
//     something the sweep did not observe. Clearing stays the exclusive job of a
//     successful fire and of a PATCH.
//
// THREE OF THE FOUR ARE "SOMETHING MUST NOT HAPPEN", which is the shape that
// passes vacuously when the sweep does nothing at all. The single assertion that
// stops that is `require.NotNil(t, brokenRow.LastError)` - it is the only
// positive statement in the test and every negative one below is worthless
// without it. Do not weaken it to an `assert`.
//
// THE PLANTED TEXT ON THE HEALTHY ROW IS DELIBERATELY NOT A MESSAGE THE SWEEP
// COULD EVER PRODUCE. An earlier draft planted the exact string the broken row
// records, which cannot distinguish "the sweep left this row alone" from "the
// sweep rewrote this row with an identical message" - and the planted
// last_error_at is dated 48 hours ago for the same reason, since
// RecordScheduledJobFailure always stamps NOW(). Between them, any touch of the
// healthy row is visible.
func TestValidateStoredSpecsOnStartup(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "startup-sweep@example.com")

	// FAR IN THE FUTURE, deliberately: neither existing startup loop nor
	// TickOnce can reach this row, so a pass here is attributable to the sweep.
	future := pgtype.Timestamptz{Time: time.Now().Add(720 * time.Hour), Valid: true}

	broken, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "monthly-broken", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: future,
	})
	require.NoError(t, err)

	fine, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "monthly-fine", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: future,
	})
	require.NoError(t, err)

	// A DISABLED broken schedule. It is out of scope: the sweep lists enabled
	// schedules only, because a disabled schedule is not trying to fire and a
	// failure record about it would be noise the operator did not ask for.
	disabled, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "disabled-broken", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: false,
		NextRunAt: future,
	})
	require.NoError(t, err)

	// A row carrying a record whose spec is now FINE. The sweep must leave it.
	planted := "planted by the test: a failure recorded before this boot"
	_, err = h.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET last_error = $1, last_error_at = NOW() - interval '48 hours' WHERE id = $2`,
		planted, fine.ID)
	require.NoError(t, err)

	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q))

	brokenRow, err := h.q.GetScheduledJob(ctx, broken.ID)
	require.NoError(t, err)
	require.NotNil(t, brokenRow.LastError,
		"a broken enabled schedule that is NOT overdue must be recorded at startup: no other loop sees it")
	assert.Contains(t, *brokenRow.LastError, "retries must be between 0 and 10")
	assert.True(t, brokenRow.LastErrorAt.Valid)
	assert.WithinDuration(t, future.Time, brokenRow.NextRunAt.Time, time.Second,
		"the sweep must NOT move next_run_at: ReconcileOnStartup owns never-catch-up")
	assert.True(t, brokenRow.Enabled,
		"the sweep is RECORD-ONLY: relay does not auto-disable a failing schedule")

	fineRow, err := h.q.GetScheduledJob(ctx, fine.ID)
	require.NoError(t, err)
	require.NotNil(t, fineRow.LastError,
		"THE SWEEP NEVER CLEARS. A spec that validates at boot has not been proven to FIRE, so clearing "+
			"here would assert something the sweep did not observe")
	assert.Equal(t, planted, *fineRow.LastError)
	assert.True(t, fineRow.LastErrorAt.Time.Before(time.Now().Add(-time.Hour)),
		"the healthy row must not be rewritten at all: RecordScheduledJobFailure stamps last_error_at = NOW(), "+
			"so a stamp inside the last hour means the sweep touched a row it had no failure for")

	disabledRow, err := h.q.GetScheduledJob(ctx, disabled.ID)
	require.NoError(t, err)
	assert.Nil(t, disabledRow.LastError,
		"a DISABLED schedule is not trying to fire, so the sweep must not record anything about it")
}
