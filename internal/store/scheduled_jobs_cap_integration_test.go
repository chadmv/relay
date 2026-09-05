//go:build integration

package store_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capFixture is one owner plus helpers that insert and count that owner's
// schedules through the production statements.
type capFixture struct {
	pool  *pgxpool.Pool
	q     *store.Queries
	ctx   context.Context
	owner store.User
}

func newCapFixture(t *testing.T) *capFixture {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	return &capFixture{pool: pool, q: q, ctx: context.Background(), owner: newTestUser(t, q, false)}
}

func (f *capFixture) insert(t *testing.T, q *store.Queries, name string) {
	t.Helper()
	_, err := q.CreateScheduledJob(f.ctx, store.CreateScheduledJobParams{
		Name: name, OwnerID: f.owner.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec:       []byte(`{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}`),
		OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
}

func (f *capFixture) countUpTo(t *testing.T, q *store.Queries, ceiling int64) int64 {
	t.Helper()
	n, err := q.CountScheduledJobsForOwnerUpTo(f.ctx, store.CountScheduledJobsForOwnerUpToParams{
		OwnerID: f.owner.ID, Ceiling: ceiling,
	})
	require.NoError(t, err)
	return n
}

func (f *capFixture) rows(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM scheduled_jobs WHERE owner_id = $1`, f.owner.ID).Scan(&n))
	return n
}

// waitUntilOneSessionIsBlockedOnALock replaces a sleep: if B has not reached the
// lock, committing A would let B run against a post-A snapshot and the test
// would prove nothing.
//
// THE WAIT IS OWNED BY THIS TEST. pgdsn gives every test its own freshly CREATEd
// database and the predicate is scoped to current_database(), so no sibling lane
// can satisfy it - the failure mode where a concurrency wait passes OPEN because
// someone else's session matched.
//
// The condition runs on testify's goroutine, so a poll error is captured and
// re-asserted on the test goroutine; assert.Eventually rather than require,
// because require would FailNow on timeout and the poll error would never be
// reported.
func (f *capFixture) waitUntilOneSessionIsBlockedOnALock(t *testing.T) {
	t.Helper()
	var (
		pollMu  sync.Mutex
		pollErr error
	)
	ok := assert.Eventually(t, func() bool {
		var n int
		if err := f.pool.QueryRow(f.ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock' AND state = 'active'`).Scan(&n); err != nil {
			pollMu.Lock()
			pollErr = err
			pollMu.Unlock()
			return false
		}
		return n > 0
	}, 10*time.Second, 50*time.Millisecond,
		"B never blocked on A's users-row lock, so this test would prove nothing about ordering")
	pollMu.Lock()
	defer pollMu.Unlock()
	require.NoError(t, pollErr, "the pg_stat_activity poll itself failed")
	require.True(t, ok)
}

// TestScheduleCapLock_TwoConcurrentCreatesAtCapMinusOneInsertExactlyOne
// sequences the pair deterministically rather than by timing.
//
// THE PROPERTY: because the lock is its OWN statement, B's COUNTING STATEMENT
// does not begin until the lock is granted, which is after A commits - so B's
// snapshot includes A's row and B counts the cap.
//
// BOUND THE FAILURE. B's lock acquisition runs under a context deadline, so a
// mutant that never blocks or never releases fails BY NAME instead of hanging;
// a hang is indistinguishable from infrastructure trouble.
func TestScheduleCapLock_TwoConcurrentCreatesAtCapMinusOneInsertExactlyOne(t *testing.T) {
	f := newCapFixture(t)
	const capValue = int64(2)
	f.insert(t, f.q, "already-held") // the owner is now at cap - 1

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx) //nolint:errcheck
	qa := f.q.WithTx(txA)

	_, err = qa.LockOwnerForScheduleCap(f.ctx, f.owner.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), f.countUpTo(t, qa, capValue), "A sees one row and is admitted")

	txB, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx) //nolint:errcheck
	qb := f.q.WithTx(txB)

	lockCtx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()

	var (
		wg        sync.WaitGroup
		lockErrB  error
		nB        int64
		countErrB error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, lockErrB = qb.LockOwnerForScheduleCap(lockCtx, f.owner.ID); lockErrB != nil {
			return
		}
		nB, countErrB = qb.CountScheduledJobsForOwnerUpTo(lockCtx,
			store.CountScheduledJobsForOwnerUpToParams{OwnerID: f.owner.ID, Ceiling: capValue})
	}()

	f.waitUntilOneSessionIsBlockedOnALock(t)

	f.insert(t, qa, "a-wins")
	require.NoError(t, txA.Commit(f.ctx))

	wg.Wait()
	require.NoError(t, lockErrB,
		"B's lock must be granted once A commits; a deadline error here means it never was")
	require.NoError(t, countErrB)
	require.Equal(t, capValue, nB,
		"B's counting statement must take a snapshot AFTER the lock was granted, so it sees A's "+
			"committed row and refuses. A count of 1 here means the lock was dropped, merged into "+
			"the counting statement, or replaced by a single conditional INSERT - and B would write "+
			"a third row over a cap of two.")

	require.NoError(t, txB.Rollback(f.ctx))
	require.Equal(t, capValue, f.rows(t), "exactly one insert survives")
}

// TestScheduleCapLock_WithoutTheLockBothTransactionsInsert is THE CONTROL, and
// without it a green sibling is indistinguishable from a test whose sessions
// never overlapped.
//
// Under READ COMMITTED a count is evaluated against the snapshot taken when its
// statement began, which cannot see a concurrent uncommitted insert - so two
// requests at cap-1 both pass and both commit. This is the same overshoot
// internal/worker/handler.go's auto-enroll ceiling documents for the same shape.
func TestScheduleCapLock_WithoutTheLockBothTransactionsInsert(t *testing.T) {
	f := newCapFixture(t)
	const capValue = int64(2)
	f.insert(t, f.q, "already-held")

	txA, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txA.Rollback(f.ctx) //nolint:errcheck
	txB, err := f.pool.Begin(f.ctx)
	require.NoError(t, err)
	defer txB.Rollback(f.ctx) //nolint:errcheck
	qa, qb := f.q.WithTx(txA), f.q.WithTx(txB)

	// NO LockOwnerForScheduleCap in either transaction. Neither count blocks.
	require.Equal(t, int64(1), f.countUpTo(t, qa, capValue))
	require.Equal(t, int64(1), f.countUpTo(t, qb, capValue),
		"B's count does not block and does not see A: this is the race the lock exists to close")

	f.insert(t, qa, "a")
	f.insert(t, qb, "b")
	require.NoError(t, txA.Commit(f.ctx))
	require.NoError(t, txB.Commit(f.ctx))

	require.Equal(t, int64(3), f.rows(t),
		"both transactions were admitted at cap-1 and both committed, so the owner holds three rows "+
			"over a cap of two")
}

// TestCreateScheduledJob_TheStoreDoesNotEnforceTheCap pins that the cap is an
// ADMISSION policy: no constraint and no trigger enforces it, and
// ValidateStoredSchedule never learns about it.
//
// A CHECK constraint or a trigger would be retroactively hostile in the way a
// validator tightening usually is: it would make an over-cap owner's rows
// unwritable by ANY statement, including the boot sweep's own
// RecordScheduledJobFailure. It would also break fixtures that plant rows
// directly.
//
// 105 is the shipped default plus five, spelled as a literal because
// internal/store must not import internal/api - that direction is the cycle.
func TestCreateScheduledJob_TheStoreDoesNotEnforceTheCap(t *testing.T) {
	f := newCapFixture(t)
	const overDefaultCap = 105
	for i := 0; i < overDefaultCap; i++ {
		f.insert(t, f.q, "direct-"+strconv.Itoa(i))
	}
	require.Equal(t, int64(overDefaultCap), f.rows(t),
		"every direct CreateScheduledJob must succeed: enforcing the cap in SQL would make an "+
			"over-cap owner's rows unwritable by the sweep and would break every planting fixture")

	// AND THE BOUNDED COUNT SATURATES rather than reporting the true total.
	require.Equal(t, int64(100), f.countUpTo(t, f.q, 100),
		"the count saturates at its ceiling; anything that reads it as a census is reading a number "+
			"the statement cannot support")
}
