//go:build integration

package schedrunner_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// seedHealthySchedules plants n enabled schedules whose stored spec still
// validates, in ONE statement, beside seedBrokenSchedules.
//
// THE MIXED FIXTURE IS WHAT MAKES THE TWO COUNTS SEPARABLE. With broken rows
// only, Checked and Invalid are equal and a mutant returning one for both
// survives - a degenerate fixture value is a guard that cannot fail.
//
// next_run_at is far in the future for the reason seedBrokenSchedules gives:
// neither ListEligibleScheduledJobs nor ListOverdueScheduledJobsForCatchupPage
// can reach these rows, so a pass is attributable to the sweep.
func seedHealthySchedules(t *testing.T, h *runnerHarness, owner pgtype.UUID, n int) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		SELECT 'healthy-' || g::text, $1, '@hourly', 'UTC', $2::jsonb, 'skip', TRUE, NOW() + INTERVAL '720 hours'
		FROM generate_series(1, $3::int) AS g`,
		owner, string(makeSpecJSON(t)), n)
	require.NoError(t, err)
}

// sweepWithGenerousBudget runs one pass with a budget generous enough that it
// cannot fire, so a test about counts is never about timing.
func sweepWithGenerousBudget(t *testing.T, h *runnerHarness) (schedrunner.SweepResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 60*time.Second)
}

// TestValidateStoredSpecsOnStartup_AnExpiredBudgetChecksNothingRatherThanRunningUnbounded
// is the guard for "a zero budget is an EXPIRED deadline, not an absent one".
//
// THE DISCRIMINATING INPUT IS THE BUDGET ITSELF, and the recorded-failure count
// is what discriminates: against a `budget <= 0 means unbounded` branch - the
// kind-looking branch a later reader will reach for - three rows are recorded
// instead of none. Fully deterministic: no timing and no sleep.
//
// IT ALSO PINS THAT THE FAILURE IS PROMPT. The whole guard depends on
// WithTimeout(ctx, 0) producing an error from the first page query rather than a
// hang, and a hang under mutation is indistinguishable from infrastructure
// trouble - so the parent deadline bounds it and the elapsed assertion names the
// cause.
//
// THE ERROR ASSERTION IS ON THE SENTINEL AND NEVER ON A MESSAGE. It holds
// because pgx returns the context's own error rather than wrapping one of its
// own when the context is already expired before the acquire, so this is the
// stdlib value and not another component's prose. If a pgx upgrade ever makes it
// a wrapped pgx error, errors.Is is still the right instrument and a substring of
// the new message is still the wrong one: res.Truncated below is what pins the
// verdict, and it comes from the child context either way.
func TestValidateStoredSpecsOnStartup_AnExpiredBudgetChecksNothingRatherThanRunningUnbounded(t *testing.T) {
	h := newRunnerHarness(t)
	owner := h.createUser(t, "expired-budget@example.com")
	seedBrokenSchedules(t, h, owner, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	res, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, h.q, 0)
	elapsed := time.Since(start)

	require.Error(t, err,
		"an expired budget must be reported, not swallowed: a silent empty pass reads exactly like a "+
			"healthy fleet")
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"the budget's own deadline is what stopped this, so the sentinel must survive the page query's "+
			"error path")

	require.True(t, res.Truncated,
		"Truncated comes from the child context, so it must be set even when the budget expired before "+
			"the first page returned - which is exactly this case")
	require.Equal(t, 0, res.Checked,
		"THE EXACT SIBLING AT THE ZERO END, bracketing the mid-pass test's range assertion")
	require.Equal(t, 0, res.Invalid)
	require.Equal(t, 0, countRecordedFailures(t, h),
		"THE DISCRIMINATOR. A `budget <= 0 means unbounded` branch records all three planted rows and "+
			"restores the unbounded boot from a zero value")
	require.Less(t, elapsed, 10*time.Second,
		"an expired budget must fail PROMPTLY from the first page query. A slow failure here means the "+
			"child deadline is not reaching pgx, and the only reason this assertion is loose is that its "+
			"job is to separate prompt from hung, not to benchmark")
}

// TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation is the guard for
// "Truncated comes from the child context, never from the returned error".
//
// IT KILLS THE LIKELIEST WRONG IMPLEMENTATION, `Truncated: err != nil`. A
// mid-boot SIGTERM cancels the parent, so the child's Err() is
// context.Canceled and not DeadlineExceeded, and a pass that conflated them would
// print a line advertising a deadline that did not fire and prescribing a knob
// that is not the problem.
//
// THE BUDGET IS GENEROUS AND CANNOT FIRE, which is what makes the cancellation
// the only possible cause. The instrument is the existing cancellation test's: the
// hook fires on the END of the first RecordScheduledJobFailure, which pgx calls on
// this goroutine after the Exec has completed, so row 1's write lands and the row
// loop's next iteration is the very next thing to run. No sleep, no poll, no
// second goroutine.
//
// IT IS A SEPARATE TEST FROM ...ACancelledSweepReturnsInsteadOfLoggingEveryRow
// because that test's subject is log volume, and a second subject in one test
// makes a red ambiguous.
func TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation(t *testing.T) {
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "shutdown-not-truncation@example.com")
	seedBrokenSchedules(t, h, owner, 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &sweepTracer{}
	var once sync.Once
	tr.setOnEnd(func(sql string) {
		if strings.Contains(sql, "-- name: RecordScheduledJobFailure") {
			once.Do(cancel)
		}
	})
	q := store.New(tracedPool(t, h, tr))

	res, err := schedrunner.ValidateStoredSpecsOnStartup(ctx, q, 60*time.Second)

	require.ErrorIs(t, err, context.Canceled,
		"a parent cancellation must come back as Canceled; if this is DeadlineExceeded the 60s budget "+
			"fired, and this test is no longer about a shutdown")
	require.False(t, res.Truncated,
		"THE MUTANT THIS KILLS is `Truncated: err != nil`. A shutdown stopped this pass, not the "+
			"budget, and claiming the deadline fired sends the operator to the wrong knob")
	require.Equal(t, 1, res.Checked,
		"EXACTLY the one row validated before the cancellation. 2 means the counter sits ABOVE the "+
			"ctx.Err() fence and is counting a row the pass never checked")
	require.Equal(t, 1, res.Invalid)
	require.Equal(t, 1, countRecordedFailures(t, h),
		"ANTI-VACUITY: without this, a sweep that returned before doing any work satisfies everything above")
}

// TestValidateStoredSpecsOnStartup_ADeadlineMidPassReportsPartialCountsAsFloors
// is the guard that the pass actually STOPS EARLY, not merely that it returns.
//
// A TEST THAT PASSES WHEN THE DEADLINE NEVER FIRES PINS NOTHING, which is why
// `res.Checked < planted` is asserted alongside Truncated: the planted set is more
// than one page, so a pass that ran to exhaustion is plainly distinguishable from
// one the budget stopped.
//
// THE TIMING IS ARRANGED WITHOUT A POLL OR A SECOND GOROUTINE. The hook runs on
// the caller's goroutine after the first RecordScheduledJobFailure's Exec
// completes, so a single sleep past the remaining budget means the row loop's next
// ctx.Err() check is the very next thing to run and it sees an expired deadline.
//
// THE Checked ASSERTION IS A RANGE, DELIBERATELY, AND IT HAS EXACT SIBLINGS ON
// BOTH SIDES. An exact 1 would go red on a machine where the page read itself
// outlives the budget, and a flaky guard for a real property gets deleted.
// ...AnExpiredBudget... pins 0 exactly, ...AShutdownIsNotATruncation pins 1
// exactly, and ...ReadsInPagesOfOneHundred pins the full count, so the loosened
// middle is bracketed on both sides.
func TestValidateStoredSpecsOnStartup_ADeadlineMidPassReportsPartialCountsAsFloors(t *testing.T) {
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	// More than one page, so a complete pass is far from a truncated one.
	const planted = 150
	// The sleep must exceed the budget. Both are orders of magnitude above this
	// fixture's measured page read, so the deadline fires inside the sleep and
	// nowhere else.
	const budget = 10 * time.Second
	const sleep = 12 * time.Second

	h := newRunnerHarness(t)
	owner := h.createUser(t, "deadline-midpass@example.com")
	seedBrokenSchedules(t, h, owner, planted)

	tr := &sweepTracer{}
	var once sync.Once
	tr.setOnEnd(func(sql string) {
		if strings.Contains(sql, "-- name: RecordScheduledJobFailure") {
			once.Do(func() { time.Sleep(sleep) })
		}
	})
	q := store.New(tracedPool(t, h, tr))

	res, err := schedrunner.ValidateStoredSpecsOnStartup(context.Background(), q, budget)

	require.True(t, res.Truncated,
		"the budget expired mid-pass, so the result must say so; the flag is what stops the boot line "+
			"presenting a floor as a total")
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"this error comes from the row loop's own ctx.Err(), so it is the stdlib sentinel and does not "+
			"depend on how pgx wraps anything")
	require.Greater(t, res.Checked, 0,
		"the pass must have checked SOMETHING before the deadline. 0 means this machine's first page "+
			"read outlived the %s budget, which is a too-tight budget for this lane rather than a "+
			"defect in the sweep - raise budget and sleep together", budget)
	require.Less(t, res.Checked, planted,
		"THE REFUSAL IS THE POINT. A pass that reached all %d planted rows was never stopped by its "+
			"budget, and a test that passes when the deadline never fires pins nothing", planted)
	require.Equal(t, res.Invalid, countRecordedFailures(t, h),
		"every verdict this pass formed was a fresh message on a row nobody had touched, so each one "+
			"landed; a mismatch means the counter and the write site disagree")
}

// TestValidateStoredSpecsOnStartup_CountsCheckedAndInvalidSeparately pins that
// the two counts are two different quantities, and that Invalid counts VERDICTS
// rather than writes.
//
// THE MIXED FIXTURE IS THE FIRST HALF. seedBrokenSchedules plants only broken
// rows, so the existing paging test gives Checked == Invalid == 250 and a mutant
// returning Checked for both fields survives. 5 healthy and 3 broken separates
// them.
//
// THE SECOND PASS IS THE SECOND HALF, and it is what the field's own comment is
// about. On the second pass the three broken rows already carry the identical
// message, so RecordScheduledJobFailure's `last_error IS DISTINCT FROM` predicate
// makes every UPDATE a no-op: three verdicts, zero writes. A mutant that counts
// writes reports Invalid 0 there and 3 on the first pass, and only the second
// pass can tell them apart.
func TestValidateStoredSpecsOnStartup_CountsCheckedAndInvalidSeparately(t *testing.T) {
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "counts-separately@example.com")
	seedHealthySchedules(t, h, owner, 5)
	seedBrokenSchedules(t, h, owner, 3)

	first, err := sweepWithGenerousBudget(t, h)
	require.NoError(t, err)
	require.Equal(t, 8, first.Checked, "every enabled row is checked, healthy or not")
	require.Equal(t, 3, first.Invalid, "only the broken ones form a verdict")
	require.False(t, first.Truncated, "a generous budget must not report truncation")
	require.Equal(t, 3, countRecordedFailures(t, h))

	second, err := sweepWithGenerousBudget(t, h)
	require.NoError(t, err)
	require.Equal(t, 8, second.Checked)
	require.Equal(t, 3, second.Invalid,
		"INVALID COUNTS VERDICTS, NOT WRITES. Every UPDATE on this pass was refused by the "+
			"identical-message predicate, so a counter placed after that branch reports 0 here while "+
			"staying green on the first pass")
	require.False(t, second.Truncated)
	require.Equal(t, 3, countRecordedFailures(t, h),
		"and nothing new was written, which is what makes the 3 above a count of verdicts")
}
