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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// reconcileTracer counts the page reads ReconcileOnStartup issues and can fire a
// hook when a statement finishes.
//
// IT COUNTS AT TraceQueryStart AND HOOKS AT TraceQueryEnd, which is why the SQL
// is stashed in the context: TraceQueryEndData carries a CommandTag and an error
// and no SQL, and the returned context is the only channel pgx gives between the
// two calls.
//
// The mutex is not decoration. pgxpool hands out a connection per acquire and
// nothing in pgx promises one goroutine.
type reconcileTracer struct {
	mu sync.Mutex
	// selects counts statements matching the reconcile's page read.
	selects int
	// widest is the largest row count any one matching page read returned.
	// pgx populates CommandTag.RowsAffected() for a SELECT from the "SELECT n"
	// tag, so this is the per-statement ceiling rather than an aggregate: a loop
	// that issued the right NUMBER of reads while asking each for the whole
	// table is invisible to selects alone.
	widest int64
	// onEnd, if set, is called with the finished statement's SQL, ON THE
	// CALLER'S GOROUTINE, after the statement has completed.
	onEnd func(sql string)
}

type reconcileTracerSQLKey struct{}

func (tr *reconcileTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if isReconcilePageRead(d.SQL) {
		tr.mu.Lock()
		tr.selects++
		tr.mu.Unlock()
	}
	return context.WithValue(ctx, reconcileTracerSQLKey{}, d.SQL)
}

func (tr *reconcileTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(reconcileTracerSQLKey{}).(string)
	tr.mu.Lock()
	hook := tr.onEnd
	if isReconcilePageRead(sql) && d.CommandTag.RowsAffected() > tr.widest {
		tr.widest = d.CommandTag.RowsAffected()
	}
	tr.mu.Unlock()
	if hook != nil {
		hook(sql)
	}
}

func (tr *reconcileTracer) setOnEnd(hook func(sql string)) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.onEnd = hook
}

func (tr *reconcileTracer) selectCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.selects
}

func (tr *reconcileTracer) widestPage() int64 {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.widest
}

// isReconcilePageRead matches the reconcile's read STRUCTURALLY, never by
// statement name. The name is what this slice changes, so a name matcher reports
// zero before the change - which is also what a test that never reached the
// function reports, making a broken instrument and a real failure
// indistinguishable.
//
// next_run_at < NOW() IS THE DISCRIMINATOR AND THIS IS NOT INTERCHANGEABLE WITH
// isSweepPageRead. That matcher's three fragments are all satisfied by this
// statement too, so it cannot attribute a read to one pass or the other. This
// needle excludes the sweep's page read, which names no next_run_at at all, and
// excludes ListEligibleScheduledJobs, whose predicate is next_run_at <= NOW() -
// a string this needle does not match, and the reason the needle carries NOW()
// rather than stopping at the operator.
func isReconcilePageRead(sql string) bool {
	return strings.Contains(sql, "FROM scheduled_jobs") &&
		strings.Contains(sql, "WHERE enabled") &&
		strings.Contains(sql, "next_run_at < NOW()")
}

// seedOverdueSchedules plants n enabled schedules that are already overdue, in
// ONE statement.
//
// cronExpr is a parameter because the termination guard needs rows
// ReconcileOnStartup cannot advance, and namePrefix is one because the
// assertions partition the table by it.
//
// THE RAW INSERT BYPASSES handleCreateScheduledJob'S VALIDATION, which is what
// lets an unparseable cron reach the row - and it is also why that input is
// reachable in production, since a migration or an older binary writes through
// the same statement. job_spec is a minimal valid object: the reconcile reads
// that column zero times.
func seedOverdueSchedules(t *testing.T, h *runnerHarness, owner pgtype.UUID, namePrefix, cronExpr string, n int) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		SELECT $4::text || g::text, $1, $5::text, 'UTC', $2::jsonb, 'skip', TRUE, NOW() - INTERVAL '25 hours'
		FROM generate_series(1, $3::int) AS g`,
		owner, `{"name":"r","tasks":[{"name":"t","command":["echo","hi"]}]}`, n, namePrefix, cronExpr)
	require.NoError(t, err)
}

// countAdvanced counts rows under namePrefix whose next_run_at is now in the
// future, through the harness's own UNTRACED pool so the assertion's own read
// cannot move the statement counter.
func countAdvanced(t *testing.T, h *runnerHarness, namePrefix string) int {
	t.Helper()
	var n int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduled_jobs WHERE name LIKE $1 || '%' AND next_run_at > NOW()`,
		namePrefix).Scan(&n))
	return n
}

// TestReconcileOnStartup_ReadsInPagesOfOneHundred pins that the reconcile reads
// the overdue enabled set in pages rather than in one unbounded statement, and
// that every row on every page - including the SHORT last one - is advanced.
//
// 250 IS THE DISCRIMINATING INPUT and each of its three properties is load
// bearing: more than one page, more than two pages, and not a multiple of the
// page size, so the final page is short. Any input below 101 rows behaves
// identically on paged and unpaged code and pins nothing.
//
// THE LITERAL 3 IS DELIBERATE AND SO IS THE ABSENCE OF A SEAM.
// reconcilePageSize is unexported and this is an external test package, so the
// expected count cannot be derived from the thing under test. 250 rows at 100
// per page is 100 + 100 + 50.
//
// IT DOES NOT PIN THE CURSOR'S DIRECTION. A cursor taking each page's MINIMUM
// id still reports 3 here, because every row this fixture reads is advanced out
// of the predicate before the next page runs.
// TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses is where that
// mutant dies.
func TestReconcileOnStartup_ReadsInPagesOfOneHundred(t *testing.T) {
	h := newRunnerHarness(t)
	owner := h.createUser(t, "paged-reconcile@example.com")
	seedOverdueSchedules(t, h, owner, "reconcile-paged-", "@hourly", 250)

	tr := &reconcileTracer{}
	q := store.New(tracedPool(t, h, tr))

	// BOUNDED FAILURE. A cursor that fails to advance is an infinite loop, and a
	// hang is indistinguishable from infrastructure trouble. Under a deadline
	// that mutant fails as a named timeout instead of consuming the package
	// clock.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, q),
		"a context deadline here means the pass did not terminate")

	require.Equal(t, 3, tr.selectCount(),
		"250 overdue rows at 100 per page is three reads: 100, 100, then a short 50. "+
			"1 means the reconcile still materializes every overdue row in one statement")

	require.Equal(t, 250, countAdvanced(t, h, "reconcile-paged-"),
		"THE POSITIVE ASSERTION. Without it a pass that dropped the final page, or stopped "+
			"after one page, would still satisfy a statement count alone")

	// THE PER-STATEMENT CEILING, which the count above does not give. Three
	// reads is also what a loop that asked each read for the whole table would
	// report if it then discarded rows in Go, and peak resident bytes is the
	// property this slice is about.
	require.LessOrEqual(t, tr.widestPage(), int64(100),
		"no single page read may return more than one page of rows")
}

// TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses pins that the
// pass terminates, and in a bounded number of reads, over rows it cannot
// advance.
//
// IT IS THE GUARD FOR THE CURSOR ITSELF. Termination here is a property of
// `id > cursor_id` alone: a row is excluded once read whatever the loop did with
// it. A design that re-queried `next_run_at < NOW()` without a cursor, relying
// on each row's own advance to remove it from the predicate, re-reads these rows
// forever - and this fixture is what makes that true rather than merely slow.
//
// 150 POISONED IS THE LOAD-BEARING NUMBER, not "at least one". A drain loop ends
// when a page comes back short, so with fewer unadvanceable rows than fit in one
// page it terminates after extra work and satisfies every assertion below. At
// 150 against a page size of 100 every page it reads is full, forever.
//
// THE 100 HEALTHY ROWS ARE THE SECOND HALF OF THE INSTRUMENT. They make the
// count assertion discriminate a cursor that takes each page's MAXIMUM id from
// one that takes its MINIMUM: with rows that stay overdue, a minimum cursor
// advances by one row per page instead of one page per page.
//
// THE DEADLINE IS THE BOUNDED FAILURE. Without it the non-terminating mutant
// hangs, and a hang is indistinguishable from infrastructure trouble.
func TestReconcileOnStartup_TerminatesWhenAStoredCronNoLongerParses(t *testing.T) {
	// One "reconcile skip for" line per poisoned row. Nothing here reads them
	// back; the capture only keeps them off the test output.
	var discarded bytes.Buffer
	captureLog(t, &discarded)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "poisoned-reconcile@example.com")
	seedOverdueSchedules(t, h, owner, "reconcile-poison-", "@nonsense-not-a-cron", 150)
	seedOverdueSchedules(t, h, owner, "reconcile-healthy-", "@hourly", 100)

	tr := &reconcileTracer{}
	q := store.New(tracedPool(t, h, tr))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, q),
		"context deadline exceeded here means the pass did not terminate: the cursor is stuck, "+
			"so the 150 rows whose cron cannot be parsed are being re-read forever")

	require.Equal(t, 3, tr.selectCount(),
		"250 overdue rows at 100 per page is three reads whatever the loop does with them. "+
			"A number far above 3 means the cursor advances by rows rather than by pages - "+
			"a page MINIMUM id instead of the maximum")

	require.Equal(t, 100, countAdvanced(t, h, "reconcile-healthy-"),
		"every parseable row must advance, including the ones on the short final page")

	require.Equal(t, 0, countAdvanced(t, h, "reconcile-poison-"),
		"an unparseable cron is skipped WITHOUT advancing next_run_at, so the row stays overdue "+
			"for ListEligibleScheduledJobs to pick up within TickInterval. A non-zero here means "+
			"the cron parsed, so the fixture is not poisoned and the test is vacuous")
}

// TestReconcileOnStartup_ACancelledPassReturnsInsteadOfLoggingEveryRow pins the
// reconcile's behaviour under a mid-pass shutdown.
//
// THE CANCELLATION MUST LAND MID-PASS OR THE TEST PROVES NOTHING. Cancelling
// before the call makes the page query itself fail, so a function with no
// ctx.Err() check also returns an error and the assertions cannot distinguish
// them. The tracer fires on the END of the first AdvanceScheduledJobNextRun,
// which pgx calls on this goroutine after the Exec completed - so row 1's write
// lands and the row loop's next iteration is the very next thing to run. No
// sleep, no poll, no second goroutine.
//
// THE HOOK MAY KEY ON THE STATEMENT NAME here, where the page-read counter may
// not: AdvanceScheduledJobNextRun is not being renamed, and the hook is a
// fixture rather than the thing under test.
//
// THE EXPOSURE IS ONE LINE PER REMAINING ROW, unconditionally, because this loop
// issues an UPDATE for every row rather than for broken rows only.
//
// THREE ROWS, NOT 250. This property is independent of the page size.
func TestReconcileOnStartup_ACancelledPassReturnsInsteadOfLoggingEveryRow(t *testing.T) {
	var logged bytes.Buffer
	captureLog(t, &logged)

	h := newRunnerHarness(t)
	owner := h.createUser(t, "cancelled-reconcile@example.com")
	seedOverdueSchedules(t, h, owner, "reconcile-cancel-", "@hourly", 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &reconcileTracer{}
	var once sync.Once
	tr.setOnEnd(func(sql string) {
		if strings.Contains(sql, "-- name: AdvanceScheduledJobNextRun") {
			once.Do(cancel)
		}
	})
	q := store.New(tracedPool(t, h, tr))

	err := schedrunner.ReconcileOnStartup(ctx, q)

	require.ErrorIs(t, err, context.Canceled,
		"a cancelled pass must return the cause once, not swallow it and report a clean pass")

	require.Equal(t, 0, strings.Count(logged.String(), "reconcile advance for "),
		"after cancellation no further row may reach AdvanceScheduledJobNextRun, so none may log "+
			"its own context-canceled line")

	// ANTI-VACUITY. Without this a pass that returned before doing any work at
	// all would satisfy both assertions above.
	require.Equal(t, 1, countAdvanced(t, h, "reconcile-cancel-"),
		"exactly the one row processed before the cancellation must be advanced")
}
