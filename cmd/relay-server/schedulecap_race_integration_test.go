//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCapRacePool opens a pool on a fresh migrated database. When
// repeatableRead is set it first ALTERs that DATABASE's
// default_transaction_isolation, so every session the pool opens afterwards
// inherits it - which is how an operator turns the level up for reasons of
// their own (postgresql.conf, ALTER DATABASE, ALTER ROLE), and is the state
// pool.Begin silently adopts unless the caller pins a level.
//
// The pool is built AFTER the ALTER: a connection opened before it keeps the
// old level for its whole life, and a pool holding one would make this
// harness report a level it is not running at.
func newCapRacePool(t *testing.T, repeatableRead bool) (*pgxpool.Pool, *store.Queries) {
	t.Helper()
	dsn := pgdsn.NewIntegrationDSN(t)

	if repeatableRead {
		conn, err := pgx.Connect(t.Context(), dsn)
		require.NoError(t, err)
		_, err = conn.Exec(t.Context(),
			"ALTER DATABASE "+pgx.Identifier{conn.Config().Database}.Sanitize()+
				" SET default_transaction_isolation = 'repeatable read'")
		require.NoError(t, err)
		require.NoError(t, conn.Close(t.Context()))
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	// Two blocked handler transactions, the gate transaction and the poll all
	// hold a connection at once; the default max is the CPU count, so it is
	// pinned here rather than left to the machine.
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "cap race pgxpool.Close", pool.Close) })

	var iso string
	require.NoError(t, pool.QueryRow(t.Context(), "SHOW default_transaction_isolation").Scan(&iso))
	want := "read committed"
	if repeatableRead {
		want = "repeatable read"
	}
	require.Equal(t, want, iso,
		"this harness only means what its name says if the pool's sessions really default to %q", want)

	return pool, store.New(pool)
}

// plantSchedule inserts one row directly, which is how the owner arrives at
// cap-1 without spending a create the race is about.
func plantSchedule(t *testing.T, q *store.Queries, ownerID pgtype.UUID, name string) {
	t.Helper()
	_, err := q.CreateScheduledJob(t.Context(), store.CreateScheduledJobParams{
		Name: name, OwnerID: ownerID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec:       []byte(`{"name":"j","tasks":[{"name":"t","command":["echo","x"]}]}`),
		OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
}

// waitUntilBothCreatesAreBlockedOnTheGate replaces a sleep. Releasing the gate
// before both requests have reached the lock would let them run one after the
// other, and a sequential pair proves nothing about a concurrent one.
//
// THE WAIT IS OWNED BY THIS TEST. pgdsn CREATEs a database per call and the
// predicate is scoped to current_database(), so no sibling lane's session can
// satisfy it - the failure mode where a concurrency wait passes OPEN because
// someone else's session matched.
//
// It is assert, not require: a handler that never blocks is a real finding
// about the code under test, and the outcome assertions after the gate is
// released are what name it. FailNow here would hide them.
func waitUntilBothCreatesAreBlockedOnTheGate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var (
		mu      sync.Mutex
		pollErr error
	)
	assert.Eventually(t, func() bool {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock' AND state = 'active'`).Scan(&n); err != nil {
			mu.Lock()
			pollErr = err
			mu.Unlock()
			return false
		}
		return n >= 2
	}, 20*time.Second, 50*time.Millisecond,
		"neither create, or only one, ever blocked on the gate's users-row lock: the cap's lock is "+
			"not being taken before the count, so the two requests never overlapped inside the window "+
			"the cap has to close")
	mu.Lock()
	defer mu.Unlock()
	require.NoError(t, pollErr, "the pg_stat_activity poll itself failed")
}

// raceTwoCreatesAtCapMinusOne drives two concurrent POST /v1/scheduled-jobs
// for one owner sitting at cap-1 through the http.Server buildHTTPServer
// returned, and reports the two status codes and the owner's final row count.
//
// SEQUENCED, NOT TIMED. The test holds FOR NO KEY UPDATE on the owner's users
// row - the same lock the cap takes - so both handlers pile up on it, and the
// window the cap has to close is entered by both before either can leave it.
// Without the gate the pair is a coin toss that passes on most runs whatever
// the handler does.
func raceTwoCreatesAtCapMinusOne(t *testing.T, pool *pgxpool.Pool, srv *http.Server, ownerID pgtype.UUID, token string) ([]int, int64) {
	t.Helper()

	gate, err := pool.Begin(t.Context())
	require.NoError(t, err)
	defer gate.Rollback(context.Background()) //nolint:errcheck
	_, err = gate.Exec(t.Context(),
		`SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE`, ownerID)
	require.NoError(t, err)

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = postSchedule(t, srv, token, fmt.Sprintf("racer-%d", i)).Code
		}()
	}

	waitUntilBothCreatesAreBlockedOnTheGate(t, pool)
	require.NoError(t, gate.Rollback(t.Context()))
	wg.Wait()

	var rows int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM scheduled_jobs WHERE owner_id = $1`, ownerID).Scan(&rows))
	return codes, rows
}

// assertExactlyOneAdmitted is the shared verdict: the cap's whole contract is
// that concurrency cannot buy a row the sequential path would refuse.
func assertExactlyOneAdmitted(t *testing.T, codes []int, rows int64, why string) {
	t.Helper()
	byCode := map[int]int{}
	for _, c := range codes {
		byCode[c]++
	}
	require.Equal(t, 1, byCode[http.StatusCreated], "codes=%v rows=%d: %s", codes, rows, why)
	require.Equal(t, 1, byCode[http.StatusConflict], "codes=%v rows=%d: %s", codes, rows, why)
	require.Equal(t, int64(2), rows, "codes=%v rows=%d: %s", codes, rows, why)
}

// TestScheduleCap_TwoConcurrentCreatesAtCapMinusOneAdmitExactlyOne pins the
// handler's USE of the lock, which the sequential tests cannot see: they pass
// with the lock deleted, and they pass with it taken below the count.
//
// The property is ordering. LockOwnerForScheduleCap must be taken, and taken
// BEFORE the counting statement, or the loser's count is evaluated against a
// snapshot that predates the winner's commit and both requests are admitted at
// cap-1.
func TestScheduleCap_TwoConcurrentCreatesAtCapMinusOneAdmitExactlyOne(t *testing.T) {
	pool, q := newCapRacePool(t, false)
	user := createUserWithTestPassword(t, q, "Racer", "cap-race@example.com", false)
	token := createScheduleCapToken(t, q, user.ID)
	plantSchedule(t, q, user.ID, "already-held")

	srv := buildHTTPServer(httpServerDeps{
		addr:                 "127.0.0.1:0",
		pool:                 pool,
		q:                    q,
		maxSchedulesPerOwner: 2,
	})

	codes, rows := raceTwoCreatesAtCapMinusOne(t, pool, srv, user.ID, token)
	assertExactlyOneAdmitted(t, codes, rows,
		"two concurrent creates at cap-1 must admit exactly one. Two 201s and three rows is what "+
			"deleting the lock produces, and also what moving it below the count produces - the "+
			"loser then counts against a snapshot taken before the winner committed")
}

// TestScheduleCap_HoldsWhenTheDatabaseDefaultsToRepeatableRead pins the
// isolation level the cap's ordering argument depends on.
//
// Under REPEATABLE READ a transaction's snapshot is fixed at its FIRST
// statement, which here is the lock itself - so the loser blocks correctly,
// acquires the lock correctly, and then counts against a snapshot that predates
// the winner's row. The lock still works and the cap is off, with no error and
// no log line. default_transaction_isolation is an ordinary server, database or
// role setting, so the handler must pin READ COMMITTED on its own transaction
// rather than inherit whatever it finds.
func TestScheduleCap_HoldsWhenTheDatabaseDefaultsToRepeatableRead(t *testing.T) {
	pool, q := newCapRacePool(t, true)
	user := createUserWithTestPassword(t, q, "Racer", "cap-race-rr@example.com", false)
	token := createScheduleCapToken(t, q, user.ID)
	plantSchedule(t, q, user.ID, "already-held")

	srv := buildHTTPServer(httpServerDeps{
		addr:                 "127.0.0.1:0",
		pool:                 pool,
		q:                    q,
		maxSchedulesPerOwner: 2,
	})

	codes, rows := raceTwoCreatesAtCapMinusOne(t, pool, srv, user.ID, token)
	assertExactlyOneAdmitted(t, codes, rows,
		"the cap must not depend on the server's default_transaction_isolation. Two 201s and three "+
			"rows here means the handler's transaction inherited REPEATABLE READ, where the loser's "+
			"count is evaluated against the snapshot its lock statement took - before the winner "+
			"committed")
}
