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

// TestTickOnce_ATransientFaultPreservesTheRecordAndStillAdvances pins the ONE
// arm of TickOnce's fireErr branch that had no witness at all.
//
// WHAT WAS ACTUALLY UNCOVERED, and it is not what it looks like.
// failure_test.go proves recordableFailure CLASSIFIES a pgx-shaped error as
// not-recordable. Nothing proved TickOnce ROUTES on that classification. Those
// are different claims, and the gap between them is exactly one `else`: a
// reviewer replaced `r.advanceNextRun(...)` with
// `r.advanceAfterFailure(ctx, q, row, next, "MUTANT: a transient fault was
// recorded")` - so a database blip stamps garbage over an operator's real
// record, which is the one thing README promises cannot happen - and it
// survived `go test ./...`, the schedrunner integration lane AND the api
// integration lane. Of the four near-identical advance* functions it was the
// only one with no distinguishing test.
//
// HOW A REAL TRANSIENT FAULT IS PRODUCED. It has to be REAL: a fabricated error
// would only re-test recordableFailure, which is already covered, and would say
// nothing about the routing. So the test makes the schedule's spec VALID -
// getting past all three permanent classes - and then breaks the layer BELOW
// them, with a CHECK constraint that rejects the job row CreateJobFromSpec is
// about to insert. fireOne wraps that as `create job: %w` with no permanent()
// marker, which is precisely the shape of a genuine insert fault.
//
// THE THIRD ASSERTION IS WHAT MAKES THE FIRST TWO MEAN ANYTHING. "last_error is
// unchanged" and "last_error_at is unchanged" are both satisfied by a branch
// that does nothing whatsoever - including a deleted else. `next_run_at` having
// advanced is what separates "preserved" from "never reached", and it is also
// the property that stops a poisoned schedule hot-looping every ten seconds.
//
// THE CONTROL SCHEDULE FIRES IN THE SAME TICK and must have its planted record
// OVERWRITTEN. Without it, a tick in which the failure write path was broken
// outright would satisfy every preservation assertion here, and preservation
// would be a claim about a dead code path rather than about routing.
func TestTickOnce_ATransientFaultPreservesTheRecordAndStillAdvances(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "transient-preserve@example.com")

	// A spec that PASSES all three permanent checks. The job it would insert is
	// named "transient-probe", which the constraint below rejects.
	validSpec := []byte(`{"name":"transient-probe","tasks":[{"name":"t","command":["echo","hi"]}]}`)

	// The transient row sorts FIRST (older next_run_at), so it cannot pass by
	// being reached only after the control already proved the tick works.
	transient, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "transient-probe-schedule", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: validSpec, OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	control, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "permanent-control", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// PLANTED, on both rows, and dated 48 hours back so that any rewrite is
	// visible as a timestamp jump rather than only as a text change. The two
	// texts differ so neither row's outcome can be mistaken for the other's.
	const planted = "planted by the test: an operator's real, permanent failure"
	_, err = h.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET last_error = $1, last_error_at = NOW() - interval '48 hours' WHERE id = $2`,
		planted, transient.ID)
	require.NoError(t, err)
	_, err = h.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET last_error = $1, last_error_at = NOW() - interval '48 hours' WHERE id = $2`,
		"planted on the control: must be OVERWRITTEN by this tick", control.ID)
	require.NoError(t, err)

	before, err := h.q.GetScheduledJob(ctx, transient.ID)
	require.NoError(t, err)
	require.NotNil(t, before.LastError, "precondition: the record must be planted before the tick")

	// The fault. jobs.name is a plain TEXT column with no constraint of its own,
	// so this is the least invasive way to make ONE insert fail with a genuine
	// pgx error while every other statement in the tick keeps working.
	_, err = h.pool.Exec(ctx,
		`ALTER TABLE jobs ADD CONSTRAINT probe_reject_transient CHECK (name <> 'transient-probe')`)
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(h.pool, h.q).TickOnce(ctx))

	_, err = h.pool.Exec(ctx, `ALTER TABLE jobs DROP CONSTRAINT probe_reject_transient`)
	require.NoError(t, err)

	jobs, err := h.q.ListJobsByScheduledJob(ctx, transient.ID)
	require.NoError(t, err)
	require.Empty(t, jobs,
		"precondition: the constraint must actually have stopped the insert, or this test proves nothing")

	after, err := h.q.GetScheduledJob(ctx, transient.ID)
	require.NoError(t, err)
	require.NotNil(t, after.LastError,
		"a transient fault must not CLEAR the record either")
	assert.Equal(t, planted, *after.LastError,
		"A DATABASE BLIP IS NOT NEWS ABOUT THE SCHEDULE. Overwriting a real, permanent failure with an "+
			"infrastructure fault loses the only signal an operator has, and would additionally put a "+
			"wrapped pgx message - constraint names, column names, host names - into a column four "+
			"clients render")
	assert.True(t, after.LastErrorAt.Time.Equal(before.LastErrorAt.Time),
		"last_error_at must be BYTE-IDENTICAL too: re-stamping it would say the failure just happened, "+
			"which is the question last_error_at exists to answer. got %v, want %v",
		after.LastErrorAt.Time, before.LastErrorAt.Time)
	assert.True(t, after.NextRunAt.Time.After(before.NextRunAt.Time),
		"AND THE BRANCH MUST HAVE RUN. Both assertions above are also satisfied by a branch that does "+
			"nothing at all, including a deleted else; the advance is what distinguishes preserved from "+
			"never-reached, and it is what stops the schedule hot-looping every ten seconds")

	controlRow, err := h.q.GetScheduledJob(ctx, control.ID)
	require.NoError(t, err)
	require.NotNil(t, controlRow.LastError)
	assert.Contains(t, *controlRow.LastError, "retries must be between 0 and 10",
		"CONTROL: a PERMANENT failure in the same tick must overwrite its planted record. Without this, "+
			"preservation above would be a claim about a write path that was simply broken")
	assert.True(t, controlRow.LastErrorAt.Time.After(time.Now().Add(-time.Hour)),
		"CONTROL: and its last_error_at must be re-stamped, not left 48 hours back")
}
