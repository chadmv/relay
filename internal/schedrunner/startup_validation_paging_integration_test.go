//go:build integration

package schedrunner_test

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// sweepTracer counts the statements the sweep issues and can fire a hook when
// one of them finishes.
//
// IT COUNTS AT TraceQueryStart AND HOOKS AT TraceQueryEnd, which is why the SQL
// is stashed in the context: TraceQueryEndData carries a CommandTag and an error
// and no SQL, and the returned context is the only channel pgx gives between the
// two calls.
//
// The mutex is not decoration. pgxpool hands out a connection per acquire and
// nothing in pgx promises one goroutine.
type sweepTracer struct {
	mu sync.Mutex
	// selects counts statements matching the sweep's page read.
	selects int
	// onEnd, if set, is called with the finished statement's SQL, ON THE
	// CALLER'S GOROUTINE, after the statement has completed.
	onEnd func(sql string)
}

type sweepTracerSQLKey struct{}

func (tr *sweepTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if isSweepPageRead(d.SQL) {
		tr.mu.Lock()
		tr.selects++
		tr.mu.Unlock()
	}
	return context.WithValue(ctx, sweepTracerSQLKey{}, d.SQL)
}

func (tr *sweepTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(sweepTracerSQLKey{}).(string)
	tr.mu.Lock()
	hook := tr.onEnd
	tr.mu.Unlock()
	if hook != nil {
		hook(sql)
	}
}

func (tr *sweepTracer) setOnEnd(hook func(sql string)) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.onEnd = hook
}

func (tr *sweepTracer) selectCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.selects
}

// isSweepPageRead matches the sweep's read STRUCTURALLY, never by statement
// name. The name is what this slice changes, and a matcher keyed on the new name
// reports zero before the change - which is also what a test that never reached
// the sweep reports, so a broken instrument and a real failure would be
// indistinguishable. Matching on the shape reports one before and three after.
//
// The three fragments together exclude the other statements the sweep's own pool
// issues: RecordScheduledJobFailure is an UPDATE and names no FROM clause.
func isSweepPageRead(sql string) bool {
	return strings.Contains(sql, "FROM scheduled_jobs") &&
		strings.Contains(sql, "WHERE enabled") &&
		strings.Contains(sql, "ORDER BY id")
}

// tracedPool builds a second pool onto the SAME database the harness migrated,
// with tr attached.
//
// h.pool.Config() returns a copy whose ConnConfig is deep-copied, carrying the
// unexported flag pgxpool.NewWithConfig panics without - so this needs no DSN
// and no change to runner_test.go. It also stays correct if the harness ever
// moves from a container per test to one database per test on a shared server,
// because the pool's own config names the per-test database by construction.
func tracedPool(t *testing.T, h *runnerHarness, tr *sweepTracer) *pgxpool.Pool {
	t.Helper()
	cfg := h.pool.Config()
	cfg.ConnConfig.Tracer = tr
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	return p
}

// seedBrokenSchedules plants n enabled schedules whose stored spec no longer
// validates, in ONE statement.
//
// next_run_at is far in the future for the reason TestValidateStoredSpecsOnStartup
// gives: neither ListEligibleScheduledJobs nor ListOverdueScheduledJobsForCatchup
// can reach these rows, so a pass is attributable to the sweep.
func seedBrokenSchedules(t *testing.T, h *runnerHarness, owner pgtype.UUID, n int) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
		INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		SELECT 'paged-' || g::text, $1, '@hourly', 'UTC', $2::jsonb, 'skip', TRUE, NOW() + INTERVAL '720 hours'
		FROM generate_series(1, $3::int) AS g`,
		owner, string(makeOverBudgetSpecJSON(t)), n)
	require.NoError(t, err)
}

// countRecordedFailures reads the failure surface through the harness's own
// UNTRACED pool, so the assertion's own read cannot move the statement counter.
func countRecordedFailures(t *testing.T, h *runnerHarness) int {
	t.Helper()
	var n int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scheduled_jobs WHERE last_error IS NOT NULL`).Scan(&n))
	return n
}

// TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred pins that the sweep
// reads the enabled set in pages rather than in one unbounded statement, and
// that every row on every page - including the SHORT last one - is recorded.
//
// 250 IS THE DISCRIMINATING INPUT and each of its three properties is load
// bearing: it is more than one page, more than two pages, and not a multiple of
// the page size, so the final page is short. Any input below 101 rows behaves
// identically on paged and unpaged code and pins nothing.
//
// THE LITERAL 3 IS DELIBERATE AND SO IS THE ABSENCE OF A SEAM. sweepPageSize is
// unexported and this is an external test package, so the expected count cannot
// be derived from the thing under test. 250 rows at 100 per page is 100 + 100 +
// 50.
//
// IT ALSO PINS THE TERMINATION CONDITION. A short page ends the loop and gives
// 3; ending on an empty page gives 4.
func TestValidateStoredSpecsOnStartup_ReadsInPagesOfOneHundred(t *testing.T) {
	// One log line per recorded failure, and there are 250 of them.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	h := newRunnerHarness(t)
	owner := h.createUser(t, "paged-sweep@example.com")
	seedBrokenSchedules(t, h, owner, 250)

	tr := &sweepTracer{}
	q := store.New(tracedPool(t, h, tr))

	// BOUNDED FAILURE. A cursor that fails to advance is an infinite loop, and a
	// hang is indistinguishable from infrastructure trouble. Under a deadline
	// that mutant fails as a named timeout instead of consuming the package
	// clock.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, schedrunner.ValidateStoredSpecsOnStartup(ctx, q))

	require.Equal(t, 3, tr.selectCount(),
		"250 enabled rows at 100 per page is three reads: 100, 100, then a short 50. "+
			"1 means the sweep still materializes the whole enabled set in one statement")

	require.Equal(t, 250, countRecordedFailures(t, h),
		"THE POSITIVE ASSERTION. Without it a sweep that dropped the final page, or stopped "+
			"after one page, would still satisfy a statement count alone")
}
