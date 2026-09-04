//go:build integration

package api_test

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newServerOverPool builds an api.Server the way the other helpers here do, but
// over a caller-supplied pool. Local to this file rather than in the shared
// helper, so its blast radius is this one test.
func newServerOverPool(pool *pgxpool.Pool) (*api.Server, *store.Queries) {
	q := store.New(pool)
	return api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0), q
}

// The end-to-end half of the timeout decision: the mechanism reaches a real ?q=
// request, the endpoint's existing 500 body is unchanged, and the SQLSTATE lands
// in the log.
//
// It also gives listQueryError its first look at a REAL PRODUCER. Every other
// test of that helper feeds it a *pgconn.PgError built in a test file; this one
// gets its 57014 from Postgres.
//
// BOUNDED FAILURE: the request runs in a goroutine and the test fails on a
// deadline. A statement that is never cancelled would otherwise hang, and a hang
// is indistinguishable from container trouble.
func TestListJobs_AStatementTimeoutAnswers500AndLogsTheSQLState(t *testing.T) {
	ctx := context.Background()
	dsn := pgdsn.NewIntegrationDSN(t)

	// Seed through a pool with NO timeout: the seeding statement would otherwise
	// be the first thing the timeout killed.
	seedPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer seedPool.Close()
	_, seedQ := newServerOverPool(seedPool)

	user := createTestUser(t, seedQ, "Seeder", "seeder@timeout.test", false)
	token := createTestToken(t, seedQ, user.ID)

	// One statement, so seeding is fast. The row count and the timeout are a
	// pair: 100k rows of unindexable strpos against a 50ms budget. The budget is
	// not 1ms because BearerAuth's own token lookup runs on this same pool and
	// must not be the thing that trips.
	_, err = seedPool.Exec(ctx, `
		INSERT INTO jobs (name, priority, status, submitted_by)
		SELECT 'job-' || g, 'normal', 'pending', $1
		FROM generate_series(1, 100000) g`, user.ID)
	require.NoError(t, err)
	_, err = seedPool.Exec(ctx, "ANALYZE jobs")
	require.NoError(t, err)

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.Config.RuntimeParams["statement_timeout"] = "50"
	timedPool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer timedPool.Close()
	timedSrv, _ := newServerOverPool(timedPool)

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	type result struct {
		code int
		body string
	}
	done := make(chan result, 1)
	go func() {
		req := httptest.NewRequest("GET", "/v1/jobs?q=zzqqxx-no-match", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		timedSrv.Handler().ServeHTTP(rec, req)
		done <- result{rec.Code, rec.Body.String()}
	}()

	select {
	case got := <-done:
		require.Equal(t, http.StatusInternalServerError, got.code,
			"a search the server cancelled must answer the endpoint's existing 500; mapping 57014 to "+
				"a distinguishable response is explicitly out of scope. body=%s", got.body)
		assert.JSONEq(t, `{"error":"list jobs failed"}`, got.body,
			"the default branch writes one 500 for the count and the list alike, because "+
				"listJobsBySort returns its error up")
	case <-time.After(60 * time.Second):
		t.Fatal("the request did not return inside 60s. A statement_timeout the backend does not " +
			"enforce is the failure this whole decision is built to avoid, and a hang here would be " +
			"indistinguishable from container trouble.")
	}

	assert.Contains(t, buf.String(), "57014",
		"the SQLSTATE must reach the log, or a tripped timeout is indistinguishable from every "+
			"other database failure")
	assert.NotContains(t, buf.String(), "zzqqxx-no-match",
		"and the needle must not: it is caller-supplied text and this line goes to an operator's pipeline")
}
