//go:build integration

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegration_SchedulesFailureCrossesTheWire covers
// bug-2026-08-23-unfireable-schedule-is-invisible's RESPONSE half: the column
// exists -> toScheduledJobResponse maps it -> the JSON key is spelled
// last_error -> scheduleResp decodes it -> the CLI renders it. That whole
// chain is exactly what a fixture-driven default-lane test cannot see,
// because a hand-written fixture pins what a HUMAN believes the server
// sends, not what it sends.
//
// WHAT IT DOES NOT COVER: that the SCHEDRUNNER writes the record. This harness
// deliberately does not wire schedrunner (see startRelayServer's own comment),
// which is also why the planted row is stable for the length of the test.
// internal/api/scheduled_jobs_failure_visibility_integration_test.go covers
// that half, via a real TickOnce. Do not report this file as covering the
// fix; it covers the fix's RESPONSE half.
//
// THE ROW IS PLANTED WITH SQL, and that is not a shortcut: POST and PATCH both
// validate before storing, so no REST path can produce a last_error. The value
// planted is verbatim what jobspec.Validate emits for a spec over the retry
// bound, so a change to that message shows up here as a stale literal rather
// than as a silent pass.
func TestIntegration_SchedulesFailureCrossesTheWire(t *testing.T) {
	s := startRelayServer(t)
	specPath := writeSpecFile(t, laneJobSpec)

	var createOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"create", "--name", "failing-lane", "--cron", "0 3 * * *", "--spec", specPath,
	}, &createOut))
	require.Contains(t, createOut.String(), "created: failing-lane")

	// THE CONTROL, created through the same POST. Without it, the assertions
	// below would pass on an implementation that marked every row FAILING.
	var healthyOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"create", "--name", "healthy-lane", "--cron", "0 4 * * *", "--spec", specPath,
	}, &healthyOut))
	require.Contains(t, healthyOut.String(), "created: healthy-lane")

	var scheduleID string
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM scheduled_jobs WHERE name = 'failing-lane'`).Scan(&scheduleID))

	const failure = "task t: retries must be between 0 and 10"
	tag, err := s.Pool.Exec(t.Context(), `
		UPDATE scheduled_jobs
		   SET last_error = $1, last_error_at = NOW()
		 WHERE name = 'failing-lane'`, failure)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected(), "precondition: exactly one row must be planted")

	// show: the detail endpoint's body, through the real handler.
	var showOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"show", scheduleID}, &showOut))
	show := showOut.String()
	require.Contains(t, show, "Last error (from the stored job_spec")
	require.Contains(t, show, failure,
		"the exact message the server stored must survive toScheduledJobResponse, the JSON key, and the decode")
	// last_error_at IS A SECOND KEY AND DRIFTS INDEPENDENTLY of last_error. It is
	// mapped by a different branch of toScheduledJobResponse (pgtype.Timestamptz
	// .Valid, not a *string nil check), so a test that asserted only the text
	// would leave half the wire contract unpinned here.
	require.Contains(t, show, "Failed at: ",
		"last_error_at must cross the wire too, and it is mapped by its own branch")
	require.Contains(t, show, "relay schedules run-now "+scheduleID)

	// list: the DISCOVERY half, and the one the item exists for. A response-shape
	// drift here is invisible to internal/cli's httptest fixtures and reddens
	// this lane instead.
	var listOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"list"}, &listOut))
	list := listOut.String()
	require.Contains(t, list, "STATE")

	var failingLine, healthyLine string
	for _, l := range strings.Split(list, "\n") {
		if strings.Contains(l, "failing-lane") {
			failingLine = l
		}
		if strings.Contains(l, "healthy-lane") {
			healthyLine = l
		}
	}
	require.NotEmpty(t, failingLine)
	require.NotEmpty(t, healthyLine)
	require.Contains(t, failingLine, "FAILING")
	require.NotContains(t, healthyLine, "FAILING",
		"CONTROL: a healthy schedule created through the same POST must carry neither key, so the marker "+
			"is a claim about ONE row rather than about the column being non-null everywhere")
	require.Contains(t, healthyLine, "OK")

	// THE CONTROL ON THE SHOW SURFACE TOO. A server that sent "" rather than
	// omitting the key would still be absent-not-zero from the CLI's point of
	// view, and this is the only lane that can observe which one the server
	// actually does.
	var healthyShow bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"show", healthyID(t, s)}, &healthyShow))
	require.NotContains(t, healthyShow.String(), "Last error",
		"a schedule that never failed must produce no failure output at all")

	// A PATCH that supplies a valid job_spec clears the record - through the real
	// handler and the real clear_failure argument, which nothing else in CI
	// exercises.
	var updateOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"update", scheduleID, "--spec", specPath,
	}, &updateOut))

	var showAfter bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"show", scheduleID}, &showAfter))
	require.NotContains(t, showAfter.String(), "Last error",
		"a PATCH carrying a validated job_spec must clear the record: the handler validated the new value "+
			"before storing it, so any record about the old one is stale by construction")
}

// healthyID reads the control schedule's id off the database rather than parsing
// it out of the rendered table, for the same reason the sibling lane does: the
// pool cannot itself be broken by the response-shape drift this file exists to
// catch.
func healthyID(t *testing.T, s *relayServer) string {
	t.Helper()
	var id string
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM scheduled_jobs WHERE name = 'healthy-lane'`).Scan(&id))
	return id
}
