//go:build integration

package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// runLaneLogs submits the lane job, seeds n log rows for its only task, cancels
// the job so doLogs can terminate, then runs doLogs and returns its stdout
// lines.
//
// THE CANCEL MUST COME BEFORE doLogs. handleEvents holds the SSE connection
// open with no heartbeat and no server-side timeout, and nothing in this
// harness runs a task, so a non-terminal job makes doLogs wait forever -
// bounded only by testCtx's explicit 30s deadline, which is exactly what that
// deadline exists to convert into a named failure.
func runLaneLogs(t *testing.T, s *relayServer, n int) []string {
	t.Helper()
	jobID := submitLaneJob(t, s)
	seedLogRows(t, s, firstTaskID(t, s, jobID), n)

	var cancelOut bytes.Buffer
	require.NoError(t, doCancelJob(testCtx(t), s.adminCfg(), []string{jobID}, &cancelOut))
	require.Contains(t, cancelOut.String(), "cancelled")

	var out, errOut bytes.Buffer
	err := doLogs(testCtx(t), s.adminCfg(), []string{jobID}, &out, &errOut)

	// NOT NoError. CancelJobTasks makes the job `cancelled`, and
	// watchOutcomeError returns silentError{} for any complete-output run whose
	// final status is not "done". A cancelled job's logs printing IN FULL is
	// exactly this outcome, so silentError IS the pass condition here.
	var se silentError
	require.True(t, errors.As(err, &se),
		"want silentError (job cancelled, output complete), got %T: %v", err, err)
	require.Empty(t, errOut.String(), "no per-task incompleteness diagnostic expected")

	return strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
}

// TestIntegration_Logs_PagesARealLogAcrossThePageBoundary. 201 rows forces two
// requests at relayclient.PageRequestLimit == 200, so this is the only test in
// the repo that exercises the cursor protocol against the real handler:
// since_seq is EXCLUSIVE server-side (`WHERE task_id = $1 AND id > $2`), so the
// CLI must pass next_seq VERBATIM and never lastSeq+1 - task_logs.id is a
// global BIGSERIAL, so +1 skips the next row when one task is logging alone.
//
// The LINE COUNT is the load-bearing assertion. A CLI that stops one page early
// still returns silentError{} and still reports the log as drained; only the
// count tells the two apart.
func TestIntegration_Logs_PagesARealLogAcrossThePageBoundary(t *testing.T) {
	s := startRelayServer(t)
	lines := runLaneLogs(t, s, 201)

	require.Len(t, lines, 201)
	// Row 1 carries the discriminating stream value, deliberately NOT last.
	require.Equal(t, "[t1 stderr] line-1", lines[0])
	require.Equal(t, "[t1 stdout] line-2", lines[1])
	// Row 200 is the last row of page 1 and row 201 the only row of page 2, so
	// this pair straddles the page boundary itself.
	require.Equal(t, "[t1 stdout] line-200", lines[199])
	require.Equal(t, "[t1 stdout] line-201", lines[200])
}

// TestIntegration_Logs_ExactPageMultiple_TerminatesOnTheEmptyFinalPage. With
// exactly 200 rows, page 1 is FULL and therefore carries a non-zero next_seq,
// and page 2 comes back empty with next_seq = 0. That is the real handler's
// drain rule (`if int32(len(items)) < limit { nextSeq = 0 }`) under test rather
// than a fixture's imitation of it, and it is the input on which
// printTaskLogs' "empty page without reporting drained" error arm must NOT
// fire.
func TestIntegration_Logs_ExactPageMultiple_TerminatesOnTheEmptyFinalPage(t *testing.T) {
	s := startRelayServer(t)
	lines := runLaneLogs(t, s, 200)

	require.Len(t, lines, 200)
	require.Equal(t, "[t1 stderr] line-1", lines[0])
	require.Equal(t, "[t1 stdout] line-200", lines[199])
}
