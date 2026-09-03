// internal/store/jobs_status_vocabulary_lockstep_test.go
//go:build integration

package store_test

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJobsStatusVocabularyIsExactly is the second LOCKSTEP GUARD, over the OTHER
// vocabulary migration 000019 pinned.
//
// It exists because the entry above added internal/cli/logs.go's jobIsTerminal to
// a list guarded entirely by a test that reads tasks_status_check. jobIsTerminal
// slices jobs.status, so that registration was prose with nothing behind it: a
// seventh JOB status would leave the guard green while the consequence the entry
// itself spells out - relay logs never recognising the job as finished, hanging
// until the connection drops, then reporting "connection lost" about a job that
// finished long ago - shipped with zero signal. The only other candidate,
// TestStatusVocabularyConstraints_Reject, spot-checks that 'dispatched' is
// refused; it does not pin the set.
//
// WHEN THIS TEST GOES RED, visit every site below and decide, per site, which
// side of the partition the new status belongs on. Note how the fail directions
// differ from the tasks list: there the allow-list default is fail-closed and
// seven sites invert it. Here the two sites that matter most are a Go map and a
// Go deny-list, and BOTH fail open.
//
//   - jobIsTerminal (internal/cli/logs.go) - ('done','failed','cancelled'). Read
//     by the subscribe-time snapshot, by the SSE job frame, and by emitSnapshot
//     to decide whether a still-non-terminal task is owed a log print. A new
//     terminal status omitted means relay logs hangs, as above. FAILS OPEN.
//   - terminalStatuses (internal/mcp/wait.go) - the same three values, as a map,
//     and an exact twin of jobIsTerminal that neither function knows about. It is
//     what wait_for_job polls against, so a new terminal status omitted there
//     means the tool polls a finished job until its timeout and answers as though
//     it never finished. FAILS OPEN, in the same way, at a second site.
//   - handleCancelJob's gate (internal/api/jobs.go) - `job.Status == "cancelled"
//     || job.Status == "done"`, a DENY-list of the states too late to cancel. A
//     new TERMINAL status omitted here lets an operator cancel a job that has
//     already finished, which runs CancelJobTasks over its tasks. FAILS OPEN, and
//     this is the one site on either list where the fix is to widen a deny-list
//     rather than an allow-list, because the predicate authorizes REFUSING.
//   - handleRetryJob's switch (internal/api/jobs.go) - `case "done", "failed"`
//     with an explicit "cancelled" arm and a 409 default. Fail-CLOSED by
//     construction: a new status lands in the default and is refused with "job is
//     not finished". Revisit it to decide whether that refusal is right, not
//     because it is dangerous.
//   - RecomputeJobStatus (query/jobs.sql) - the WRITER, and the reason this
//     vocabulary has an asymmetry worth remembering: it emits only 'running',
//     'done' and 'failed'. 'cancelled' and 'pending' are written elsewhere, so
//     the set of statuses a job can HOLD is strictly larger than the set this
//     statement can PRODUCE. Do not read a new status into it by symmetry.
//   - CountFailedScheduledRuns24h (query/scheduled_jobs.sql) - `status = 'failed'`
//     alone, windowed on updated_at, behind the schedules summary strip's
//     failed_runs_24h. It deliberately EXCLUDES 'cancelled', which is why the
//     field is not spelled failed_24h like the one above. A new FAILURE-LIKE
//     status belongs in it and omission is quiet: the strip under-reports broken
//     schedules while still looking authoritative. A new status that is not a
//     failure must stay out. FAILS OPEN, quietly.
//   - GetJobStats (query/jobs.sql) - `status IN ('failed','cancelled')` for
//     failed_24h, plus singleton filters on 'running', 'pending' and 'done'. A
//     new status omitted from all of them is simply invisible in the dashboard
//     counts; that is quiet rather than dangerous, but it is the site an operator
//     notices first.
//   - JOB_STATUSES (web/src/jobs/api.ts) - the SPA's copy of this vocabulary,
//     which JobStatus, LANE_LABELS and LANE_CHIP_KEY all derive from. Go cannot
//     see it and nothing on the TypeScript side compares the two, so a new
//     status omitted there has no lane in the jobs board and no chip in the
//     table: its jobs are simply absent from a view that looks complete. FAILS
//     OPEN, and quietly.
func TestJobsStatusVocabularyIsExactly(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var def string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'jobs_status_check'`,
	).Scan(&def), "jobs_status_check must exist; migration 000019 adds it")

	var got []string
	for _, m := range literalRe.FindAllStringSubmatch(def, -1) {
		got = append(got, m[1])
	}
	sort.Strings(got)

	want := []string{"cancelled", "done", "failed", "pending", "running"}
	require.Equal(t, want, got,
		"jobs.status vocabulary changed - read this test's comment before updating it. These sites slice this "+
			"set: jobIsTerminal (internal/cli/logs.go), terminalStatuses (internal/mcp/wait.go), "+
			"handleCancelJob's gate and handleRetryJob's switch (internal/api/jobs.go), and RecomputeJobStatus "+
			"and GetJobStats (internal/store/query/jobs.sql), CountFailedScheduledRuns24h "+
			"(internal/store/query/scheduled_jobs.sql), and JOB_STATUSES (web/src/jobs/api.ts), from "+
			"which the SPA's JobStatus, LANE_LABELS and LANE_CHIP_KEY all derive. Revisit ALL OF THEM. FOUR "+
			"FAIL OPEN. A new "+
			"TERMINAL status omitted from jobIsTerminal makes relay logs hang on a finished job until the "+
			"connection drops and then report 'connection lost'; omitted from terminalStatuses it makes the MCP "+
			"wait_for_job tool poll a finished job until its timeout; omitted from handleCancelJob's DENY-list "+
			"it lets an operator cancel an already-finished job and run CancelJobTasks over its tasks. "+
			"omitted from CountFailedScheduledRuns24h the schedules strip under-reports broken schedules "+
			"while still looking authoritative; "+
			"omitted from JOB_STATUSES the SPA gives it no lane and no chip, so its jobs vanish from a view "+
			"that looks complete. handleRetryJob fails closed into its 409 default. RecomputeJobStatus emits only running/done/"+
			"failed, so the statuses a job can HOLD are strictly more than the ones it can PRODUCE - do not add "+
			"one there by symmetry")
}
