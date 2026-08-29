//go:build integration

package schedrunner_test

import (
	"bytes"
	"context"
	"log"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForBlockedScheduledJobsUpdate blocks until some backend is waiting on a
// lock to run an UPDATE against scheduled_jobs.
//
// IT IS WHAT MAKES THE RACE BELOW DETERMINISTIC RATHER THAN A SLEEP. Observing
// the sweep's UPDATE actually WAITING proves its LIST has already run, which is
// the precondition the whole test depends on: if the repair landed before the
// LIST, the sweep would see a healthy row, write nothing for an ordinary reason,
// and the test would pass without exercising the fence at all.
func waitForBlockedScheduledJobsUpdate(t *testing.T, pool *pgxpool.Pool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var n int
		require.NoError(t, pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE wait_event_type = 'Lock'
			    AND query ILIKE '%UPDATE scheduled_jobs%'`).Scan(&n))
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the sweep's UPDATE never blocked on the held row lock, so this test could not establish " +
		"that its LIST ran before the repair; the race it exists to demonstrate was not reproduced")
}

// TestValidateStoredSpecsOnStartup_DoesNotStampAStaleFailureOverAConcurrentRepair
// is the multi-process half of the sweep, which the existing single-threaded
// test cannot reach.
//
// THE PLACEMENT ARGUMENT IN cmd/relay-server IS SOUND AND IS ONLY ABOUT ONE
// PROCESS. Running the sweep before the runner goroutine does stop THIS server's
// tick interleaving. It says nothing about a second replica, and README
// documents multi-replica as supported - ListEligibleScheduledJobs is
// FOR UPDATE SKIP LOCKED precisely so two schedulers can coexist.
//
// THE HAZARD IS THIS BUG'S EXACT INVERSE ON THE SAME SURFACE. The sweep takes a
// pool-backed *store.Queries, so its LIST and its UPDATEs are separate implicit
// transactions and the UPDATE had no predicate beyond id. Replica B lists
// schedule S as broken; an operator repairs S through replica A, clearing the
// record; B's UPDATE then stamps the stale failure back. The schedule reads
// FAILING until its next successful fire - up to a month on @monthly. A false
// alarm is what teaches an operator to ignore the field, which costs more than
// the invisibility this item set out to fix.
//
// THE OPERATOR'S REPAIR IS SIMULATED WITH A HELD ROW LOCK, not with a sleep. A
// transaction takes SELECT ... FOR UPDATE on the row, the sweep runs in a
// goroutine and blocks on its UPDATE, the repair lands and commits, and the
// sweep's UPDATE then re-evaluates its own WHERE clause against the committed
// new version (READ COMMITTED EvalPlanQual). With a fence the qual no longer
// matches and nothing is written; without one, WHERE id = $1 still matches and
// the stale record is stamped back.
func TestValidateStoredSpecsOnStartup_DoesNotStampAStaleFailureOverAConcurrentRepair(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "sweep-race@example.com")

	// FAR IN THE FUTURE, as in the existing sweep test: neither startup loop nor
	// TickOnce can reach this row, so anything that happens to it is the sweep's.
	future := pgtype.Timestamptz{Time: time.Now().Add(720 * time.Hour), Valid: true}

	broken, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "raced-repair", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: future,
	})
	require.NoError(t, err)

	// REPLICA A's operator. The FOR UPDATE is taken first and held, so the sweep
	// can complete its LIST (a plain SELECT, which is not blocked) and then stall
	// on its UPDATE.
	repair, err := h.pool.Begin(ctx)
	require.NoError(t, err)
	var lockedID pgtype.UUID
	require.NoError(t, repair.QueryRow(ctx,
		`SELECT id FROM scheduled_jobs WHERE id = $1 FOR UPDATE`, broken.ID).Scan(&lockedID))

	done := make(chan error, 1)
	go func() { done <- schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q) }()

	waitForBlockedScheduledJobsUpdate(t, h.pool, 30*time.Second)

	// The repair itself: exactly what a PATCH through replica A does - a new,
	// validating job_spec, and the failure record cleared with it.
	_, err = repair.Exec(ctx,
		`UPDATE scheduled_jobs SET job_spec = $1, last_error = NULL, last_error_at = NULL, updated_at = NOW()
		  WHERE id = $2`, makeSpecJSON(t), broken.ID)
	require.NoError(t, err)
	require.NoError(t, repair.Commit(ctx))

	require.NoError(t, <-done, "the sweep must not fail; a non-matching fence is not an error")

	after, err := h.q.GetScheduledJob(ctx, broken.ID)
	require.NoError(t, err)
	assert.Nil(t, after.LastError,
		"THE SWEEP VALIDATED A GENERATION THAT NO LONGER EXISTS. Its verdict was about the job_spec it "+
			"READ; the repair replaced that job_spec, so the verdict is not about this row any more and "+
			"the write must not land. Re-marking a repaired schedule FAILING for up to a month is this "+
			"bug's exact inverse, and a false alarm is what teaches an operator to ignore the field")
	assert.False(t, after.LastErrorAt.Valid,
		"and last_error_at must not be re-stamped either; one column without the other is a half-written row")
}

// TestValidateStoredSpecsOnStartup_ReRecordingAnIdenticalMessageIsANoOp is the
// second half of the same statement, and it is about a contract two committed
// documents already stated and the code contradicted.
//
// migration 000022's comment says "is it still being tried" is readable from
// last_error_at MOVING, and stored_spec_bounds_test.go says last_error_at "is
// what proves the scheduler is STILL evaluating the row".
// RecordScheduledJobFailure set last_error_at = NOW() unconditionally for every
// broken row on every boot, with no fire attempt behind it - and the sweep's
// whole audience is long-cadence schedules that are NOT being fire-attempted. So
// a @monthly schedule last attempted three weeks ago rendered "last failure 2
// minutes ago" after any restart, and a crash-looping server manufactured a
// fresh timestamp every few seconds for rows nothing was trying to fire.
//
// THE FIX IS IN THE CODE RATHER THAN IN THE PROSE because the prose is the half
// that is worth keeping: "the scheduler is still evaluating this row" is the
// question an operator actually has, and it is the only thing separating
// "failing every hour" from "failed once in March". Correcting the two documents
// to say last_error_at means nothing after a restart would leave a field with no
// remaining use.
//
// THE BACKDATE IS WHAT REMOVES ALL TIMING AMBIGUITY. Comparing two NOW() values
// taken milliseconds apart would be a test about clock resolution; a stamp
// dragged 48 hours into the past cannot be confused with a fresh one.
func TestValidateStoredSpecsOnStartup_ReRecordingAnIdenticalMessageIsANoOp(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	owner := h.createUser(t, "sweep-idempotent@example.com")

	future := pgtype.Timestamptz{Time: time.Now().Add(720 * time.Hour), Valid: true}
	broken, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name: "reboot-me", OwnerID: owner, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: makeOverBudgetSpecJSON(t), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: future,
	})
	require.NoError(t, err)

	var firstBoot bytes.Buffer
	restore := captureLog(t, &firstBoot)
	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q))
	restore()

	recorded, err := h.q.GetScheduledJob(ctx, broken.ID)
	require.NoError(t, err)
	require.NotNil(t, recorded.LastError, "precondition: the first boot must record")
	assert.Contains(t, firstBoot.String(), "reboot-me",
		"a NEW record is worth one log line; that is the job the :execrows count acquired, and it is "+
			"the only thing distinguishing a fence that said no from a write that happened")

	// Drag the record two days into the past WITHOUT touching its text, exactly
	// as if it had been recorded two days and several restarts ago.
	_, err = h.pool.Exec(ctx,
		`UPDATE scheduled_jobs SET last_error_at = NOW() - interval '48 hours' WHERE id = $1`, broken.ID)
	require.NoError(t, err)
	backdated, err := h.q.GetScheduledJob(ctx, broken.ID)
	require.NoError(t, err)

	var secondBoot bytes.Buffer
	restore = captureLog(t, &secondBoot)
	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q))
	restore()

	after, err := h.q.GetScheduledJob(ctx, broken.ID)
	require.NoError(t, err)
	require.NotNil(t, after.LastError)
	assert.Equal(t, *backdated.LastError, *after.LastError,
		"the text must be unchanged: it was identical, so there was nothing to record")
	assert.True(t, after.LastErrorAt.Time.Equal(backdated.LastErrorAt.Time),
		"EVERY BOOT MUST NOT RE-STAMP last_error_at WITH NO ATTEMPT BEHIND IT. The sweep's audience is "+
			"schedules that are NOT being fire-attempted, so a fresh timestamp here says the opposite of "+
			"what migration 000022 promises it means. got %v, want %v",
		after.LastErrorAt.Time, backdated.LastErrorAt.Time)
	assert.True(t, after.UpdatedAt.Time.Equal(backdated.UpdatedAt.Time),
		"and the row must not be touched at all: a no-op that still bumps updated_at is a write, and it "+
			"would defeat any consumer ordering schedules by updated_at")
	assert.NotContains(t, secondBoot.String(), "reboot-me",
		"and a boot that recorded nothing must say nothing, or the log line is noise on every restart "+
			"for the entire life of a broken schedule")
}

// captureLog redirects the standard logger into buf and returns the restore
// function. The sweep logs through log.Printf, which is what makes the
// :execrows branch observable at all.
func captureLog(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	prevW, prevFlags, prevPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(buf)
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		log.SetOutput(prevW)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	}
	t.Cleanup(restore)
	return restore
}
