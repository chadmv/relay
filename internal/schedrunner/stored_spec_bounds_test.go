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
// jobcreate.CreateJobFromSpec, which calls jobspec.Validate.
//
// fireOne is not the only path that re-validates a stored spec. run-now
// (handleRunScheduledJobNow) reaches the same Validate, and unlike this one it
// ANSWERS: it refuses with 400 and the per-task message, which is what makes it
// the operator's remedy for the hazard below. See
// internal/api/scheduled_jobs_run_now_bounds_integration_test.go.
func makeOverBudgetSpecJSON(t *testing.T) []byte {
	t.Helper()
	spec, err := json.Marshal(map[string]any{
		"name":  "legacy",
		"tasks": []map[string]any{{"name": "t", "command": []string{"echo", "hi"}, "retries": 50}},
	})
	require.NoError(t, err)
	return spec
}

// TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard
// ASSERTS A DEFECT ON PURPOSE. Read this paragraph before reading the code.
//
// WHAT IT PINS IS WRONG, AND IT IS PINNED SO THAT IT IS IN THE SUITE RATHER THAN
// IN PROSE. Bounding `retries` in jobspec.Validate is retroactive on stored
// scheduled_jobs rows, because the spec is re-validated at fire time. A schedule
// stored with retries: 50 stops producing jobs the instant this deploys, and
// TickOnce logs one line and calls advanceNextRun - so next_run_at keeps
// marching and GET /v1/scheduled-jobs/{id}, `relay schedules` and the SPA all
// show a healthy schedule whose last_run_at has quietly stopped moving.
//
// WHAT IS NOT PINNED HERE, BECAUSE IT IS NOT BROKEN: an operator who already
// SUSPECTS a given schedule can ask `relay schedules run-now <id>` and get the
// validation error back verbatim. That closes the DIAGNOSIS, not the DISCOVERY:
// nothing points at which schedule to suspect, which is the hazard.
//
// THAT IS THE SUBJECT OF docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md,
// which ROADMAP.md pairs with the retry-bounds item. The human's gate decision
// for this slice was to ship the bounds alone, with this test plus an upgrade
// note in the PR body as the agreed mitigation.
//
// WHAT EACH ASSERTION BECOMES WHEN THE SIBLING SHIPS. All six are listed,
// because an assertion left out of this list is the one that reddens later as a
// mystery whose failure message reads like a product claim:
//   - the control, "a still-valid stored spec still fires", STAYS. It is what
//     stops every assertion below from passing vacuously.
//   - "the poisoned schedule fires no job" STAYS. It is correct behaviour: a
//     spec that does not validate must not produce a job.
//   - "next_run_at still advances" STAYS. It is what stops a poisoned schedule
//     hot-looping every tick.
//   - "last_run_at stays unset" STAYS. No run happened. A design that stamped it
//     on a failed fire would make "when did this last run" mean two things.
//   - "last_job_id stays unset" STAYS, for the same reason: no job exists to
//     point at.
//   - "the schedule is still Enabled" MAY INVERT, and is the one to think about
//     hardest. Auto-disabling after N consecutive validation failures is a
//     perfectly reasonable alternative to a last_error column, and it would turn
//     this line RED with a message that reads like a claim about how relay ought
//     to behave. If the sibling chooses that design, this assertion becomes
//     "the schedule is disabled AND something says why", and the
//     _DocumentedHazard suffix comes off this test's name.
//
// The row's FAILURE SURFACE is guarded separately and OUTSIDE this file, in
// internal/schedrunner/scheduled_job_surface_test.go, which is untagged so it
// runs in the plain `go test ./...` gate rather than only under Docker. It
// asserts store.ScheduledJob's whole field SET, so any new column - whatever it
// is called - sends the sibling's implementer here. Do not re-add a
// failure-shaped-name check to this file: a deny-list of spellings fails open on
// the next addition, which is how the first version of it was written.
func TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard(t *testing.T) {
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
		"THE HAZARD, STATED POSITIVELY: nothing disables the schedule either, so it stays in every "+
			"'enabled' listing forever while producing nothing")
}
