//go:build integration

package cli

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// nextColumn matches a rendered NEXT cell, which doSchedulesList formats as
// "2006-01-02 15:04". An empty NEXT means next_run_at decoded to nil, which is
// the failure this test is for.
var nextColumn = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)

func TestIntegration_SchedulesCreateListShow(t *testing.T) {
	s := startRelayServer(t)
	specPath := writeSpecFile(t, laneJobSpec)

	var createOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{
		"create",
		"--name", "nightly-lane",
		"--cron", "0 3 * * *",
		"--tz", "America/New_York",
		"--spec", specPath,
	}, &createOut))
	require.Contains(t, createOut.String(), "created: nightly-lane")

	var listOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(), []string{"list"}, &listOut))
	list := listOut.String()
	require.Contains(t, list, "Total: 1")
	require.Contains(t, list, "nightly-lane")
	require.Contains(t, list, "0 3 * * *")
	require.Contains(t, list, "America/New_York")
	require.Contains(t, list, "true") // enabled
	// The pairing a struct-encoded fixture cannot test: the server sends
	// next_run_at as a bare time.Time, the client decodes it into a
	// *time.Time with ,omitempty. A nil pointer renders an EMPTY cell.
	require.Regexp(t, nextColumn, list,
		"NEXT must be rendered, so next_run_at decoded non-nil")

	// show: read the id off the database rather than parsing the table.
	var scheduleID string
	require.NoError(t, s.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM scheduled_jobs WHERE name = 'nightly-lane'`).Scan(&scheduleID))

	var showOut bytes.Buffer
	require.NoError(t, doSchedules(testCtx(t), s.adminCfg(),
		[]string{"show", scheduleID}, &showOut))
	show := showOut.String()
	require.Contains(t, show, "ID:       "+scheduleID)
	require.Contains(t, show, "Name:     nightly-lane")
	require.Contains(t, show, "Cron:     0 3 * * *")
	require.Contains(t, show, "Timezone: America/New_York")
	require.Contains(t, show, "Enabled:  true")
	require.Contains(t, show, "Next:     ")
}
