//go:build integration

package schedrunner_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeOverBudgetSpecJSON is a spec that was VALID when it was stored and is not
// any more. retries: 50 was accepted by every relay release before the
// retry-bounds change; jobspec.Validate now refuses it, and schedrunner
// re-validates the stored spec on EVERY fire because fireOne calls
// jobspec.Validate DIRECTLY, hoisted above the overlap check, and then reaches it
// a second time inside jobcreate.CreateJobFromSpec. (This comment used to name
// only the second of those, from before the hoist.)
//
// fireOne is not the only path that re-validates a stored spec. run-now
// (handleRunScheduledJobNow) reaches the same Validate and answers ON DEMAND with
// a 400 and the untruncated per-task message, rather than at the next scheduled
// fire. See internal/api/scheduled_jobs_run_now_bounds_integration_test.go. (This
// paragraph used to say fireOne did not answer at all; since last_error landed it
// records the same message on the row, which is what the test below asserts.)
func makeOverBudgetSpecJSON(t *testing.T) []byte {
	t.Helper()
	spec, err := json.Marshal(map[string]any{
		"name":  "legacy",
		"tasks": []map[string]any{{"name": "t", "command": []string{"echo", "hi"}, "retries": 50}},
	})
	require.NoError(t, err)
	return spec
}

// TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled is the
// end-to-end proof, at the runner level, for
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md.
//
// IT USED TO ASSERT A DEFECT ON PURPOSE, under the name
// TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard.
// Bounding `retries` in jobspec.Validate is retroactive on stored
// scheduled_jobs rows, because the spec is re-validated at fire time - so a
// schedule stored with retries: 50 stopped producing jobs the instant that
// deployed, and TickOnce logged one line and advanced next_run_at, leaving
// GET /v1/scheduled-jobs/{id}, `relay schedules` and the SPA all showing a
// healthy schedule whose last_run_at had quietly stopped moving. That was the
// DISCOVERY gap: run-now could already explain a schedule you SUSPECTED, and
// nothing anywhere pointed at which one to suspect.
//
// The gap is closed. last_error and last_error_at (migration 000022) are written
// by TickOnce's failure branch and cleared by a successful fire, and every one
// of the six assertions below is now a positive statement of correct behaviour.
//
// NONE OF THE SIX INVERTED, INCLUDING `Enabled`, and that is the deliberate
// outcome of a gate decision rather than an accident:
//   - the control, "a still-valid stored spec still fires", is what stops every
//     assertion below from passing vacuously.
//   - "the poisoned schedule fires no job": a spec that does not validate must
//     not produce a job.
//   - "next_run_at still advances": what stops a poisoned schedule hot-looping.
//   - "last_run_at stays unset": no run happened.
//   - "last_job_id stays unset": no job exists to point at.
//   - "the schedule is still Enabled": relay does NOT auto-disable a failing
//     schedule. See docs/superpowers/specs/2026-08-28-unfireable-schedule-visibility.md
//     section 9.1 for the five reasons, the first of which is that the failure
//     mode this whole item exists for is "a release retroactively invalidated
//     stored data", and answering that by turning the operator's schedule off
//     compounds a server-driven change to user data with a server-driven change
//     to user configuration.
//
// WHAT IS NEW is the last two legs: the failure IS recorded with the bound's own
// message, and a subsequent SUCCESSFUL fire CLEARS it. The healthy control is
// asserted to carry NEITHER field, which is what makes "recorded" a claim about
// this schedule rather than about the column being non-null everywhere.
//
// The row's field SET is still guarded separately and OUTSIDE this file, in
// internal/schedrunner/scheduled_job_surface_test.go, which is untagged so it
// runs in the plain `go test ./...` gate rather than only under Docker. Do not
// re-add a failure-shaped-name check to this file: a deny-list of spellings
// fails open on the next addition, which is how the first version of it was
// written.
func TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "legacy-spec@example.com")

	overdue := pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true}

	// The poisoned schedule sorts FIRST (older next_run_at), so it cannot pass by
	// being skipped after the healthy one already proved the tick works.
	poisoned, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "legacy-retries", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-10 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// CONTROL, in the same tick: a schedule whose spec still validates must still
	// fire. Without it, a TickOnce that had stopped firing anything at all would
	// pass every assertion below.
	healthy, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "healthy", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: overdue,
	})
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	healthyJobs, err := h.q.ListJobsByScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	require.Len(t, healthyJobs, 1, "control: a still-valid stored spec must still fire")

	poisonedJobs, err := h.q.ListJobsByScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.Empty(t, poisonedJobs,
		"CORRECT AND PERMANENT: a stored spec that no longer validates must not produce a job")

	row, err := h.q.GetScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.True(t, row.NextRunAt.Time.After(time.Now()),
		"CORRECT AND PERMANENT: next_run_at must advance so the poisoned schedule does not hot-loop")
	assert.False(t, row.LastRunAt.Valid,
		"no run happened, so last_run_at must stay unset")
	assert.False(t, row.LastJobID.Valid,
		"no job was created, so last_job_id must stay unset")
	assert.True(t, row.Enabled,
		"RELAY DOES NOT AUTO-DISABLE A FAILING SCHEDULE. The failure is recorded, not acted on: "+
			"the schedule stays enabled and the operator decides. See the spec's section 9.1 - "+
			"auto-disable would answer a server-driven change to user data with a server-driven "+
			"change to user configuration, and it would provide no diagnosis, only a state change")

	// THE NEW HALF. The bound's own message, verbatim, is what run-now already
	// answers with, so an operator sees the same sentence from both surfaces.
	require.NotNil(t, row.LastError,
		"THE POINT OF THIS SLICE: a permanently un-fireable schedule must record WHY")
	assert.Contains(t, *row.LastError, "retries must be between 0 and 10",
		"the recorded text must be jobspec.Validate's own per-task message, not a generic string")
	assert.True(t, row.LastErrorAt.Valid,
		"last_error_at is what proves the scheduler is STILL evaluating the row, which is how an "+
			"operator tells 'failing every hour' from 'failed once in March'")

	// The healthy control carries NEITHER field. Without this the assertions
	// above would also pass if every schedule got an error stamped on it.
	healthyRow, err := h.q.GetScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	assert.Nil(t, healthyRow.LastError,
		"a schedule that fired must carry no recorded failure")
	assert.False(t, healthyRow.LastErrorAt.Valid,
		"a schedule that fired must carry no recorded failure time")

	// CLEARING. Repair the stored spec and make the row eligible again WITHOUT
	// touching the failure columns, then tick: only a successful fire may clear
	// them, and this is the statement (AdvanceScheduledJob) that does it.
	_, err = h.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET job_spec = $1, next_run_at = NOW() - INTERVAL '1 second' WHERE id = $2`,
		makeSpecJSON(t), poisoned.ID)
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	repaired, err := h.q.GetScheduledJob(ctx, poisoned.ID)
	require.NoError(t, err)
	assert.Nil(t, repaired.LastError,
		"a successful fire is the only event that proves the schedule works, and it must clear the record")
	assert.False(t, repaired.LastErrorAt.Valid,
		"the clear must take BOTH columns; one without the other is a half-cleared row nobody can read")
	assert.True(t, repaired.LastRunAt.Valid, "and the successful fire must stamp last_run_at")
}
