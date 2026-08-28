//go:build integration

package schedrunner_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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
// WHAT EACH ASSERTION BECOMES WHEN THE SIBLING SHIPS:
//   - "the poisoned schedule fires no job" STAYS. It is correct behaviour: a
//     spec that does not validate must not produce a job.
//   - "next_run_at still advances" STAYS. It is what stops a poisoned schedule
//     hot-looping every tick.
//   - "the row exposes no field that could record the failure" INVERTS. Require
//     the new field to EXIST, require it to carry the validation error this tick
//     produced, and drop the _DocumentedHazard suffix from this test's name.
//
// DO NOT satisfy the last assertion by adding the sibling's new field to a
// deny-list. It is written to go RED on ANY new failure-shaped field precisely
// so that the sibling's implementer has to come here.
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

	// THE TRIPWIRE. Every user-visible read of a schedule is built from this row -
	// GET /v1/scheduled-jobs/{id}, `relay schedules`, the SPA - so asserting that
	// the row has no field capable of carrying the failure is what turns "nothing
	// user-visible records it" from prose into a checked claim.
	//
	// WHEN bug-2026-08-23-unfireable-schedule-is-invisible SHIPS ITS COLUMN AND
	// models.go IS REGENERATED, THIS GOES RED. That is the point. Invert it as
	// described in this test's header comment; do not exempt the new field.
	for _, f := range reflect.VisibleFields(reflect.TypeOf(store.ScheduledJob{})) {
		name := strings.ToLower(f.Name)
		assert.NotContains(t, name, "error",
			"store.ScheduledJob gained field %q. If that is the sibling item's failure surface, this "+
				"test's last block must INVERT - see its header comment - not grow an exemption.", f.Name)
		assert.NotContains(t, name, "fail",
			"store.ScheduledJob gained field %q. Same instruction as above.", f.Name)
	}
}
