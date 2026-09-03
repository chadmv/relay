//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getScheduledJobStats(t *testing.T, srv interface {
	Handler() http.Handler
}, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var m map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&m))
	}
	return rec.Code, m
}

// seedSpawnedJob inserts a job attributed to a schedule with an explicit status
// and updated_at, which is the window column failed_runs_24h reads.
func seedSpawnedJob(t *testing.T, pool *pgxpool.Pool, ownerID, schedID, status string, updatedAt time.Time) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, submitted_by, status, scheduled_job_id, updated_at)
		 VALUES ('spawned', $1::uuid, $2, $3::uuid, $4) RETURNING id`,
		ownerID, status, schedID, updatedAt).Scan(&id))
	return id
}

// seedStandaloneJob inserts a job with NO scheduled_job_id.
func seedStandaloneJob(t *testing.T, pool *pgxpool.Pool, ownerID, status string, updatedAt time.Time) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, submitted_by, status, updated_at)
		 VALUES ('standalone', $1::uuid, $2, $3) RETURNING id`,
		ownerID, status, updatedAt).Scan(&id))
	return id
}

// TestScheduledJobStats_Buckets uses a fixture where EVERY bucket has at least
// one row belonging to no other bucket, and where each of the three exclusions
// is represented by its own row, so a mutant that dropped one predicate fails.
//
// enabled and paused are deliberately UNEQUAL (3 and 2), because equal buckets
// make a transposition invisible.
func TestScheduledJobStats_Buckets(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Owner", "sjstats-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)
	oid := uuidString(owner.ID)

	inWindow := time.Now().Add(-2 * time.Hour)
	outOfWindow := time.Now().Add(-30 * time.Hour)

	// 3 enabled, 2 paused.
	sFailed := seedFilterSchedule(t, pool, "s-failed", oid, "@daily", true)
	sCancelled := seedFilterSchedule(t, pool, "s-cancelled", oid, "@daily", true)
	sOld := seedFilterSchedule(t, pool, "s-old", oid, "@daily", true)
	sPausedFailing := seedFilterSchedule(t, pool, "s-paused-failing", oid, "@daily", false)
	seedFilterSchedule(t, pool, "s-paused-clean", oid, "@daily", false)

	// Counts in failed_runs_24h.
	seedSpawnedJob(t, pool, oid, sFailed, "failed", inWindow)
	// Must NOT count: cancelled is an operator action, not a schedule fault.
	seedSpawnedJob(t, pool, oid, sCancelled, "cancelled", inWindow)
	// Must NOT count: failed but outside the 24h window.
	seedSpawnedJob(t, pool, oid, sOld, "failed", outOfWindow)
	// Must NOT count: failed inside the window but not schedule-spawned.
	seedStandaloneJob(t, pool, oid, "failed", inWindow)

	// failing is CURRENT STATE and is NOT windowed. This schedule's own job is
	// old and successful, so it belongs to failing and to no other bucket.
	seedSpawnedJob(t, pool, oid, sPausedFailing, "done", outOfWindow)
	_, err := pool.Exec(t.Context(),
		`UPDATE scheduled_jobs SET last_error = 'parse cron: nope', last_error_at = NOW()
		 WHERE id = $1::uuid`, sPausedFailing)
	require.NoError(t, err)

	code, m := getScheduledJobStats(t, srv, ownerToken)
	require.Equal(t, http.StatusOK, code)

	assert.Equal(t, float64(3), m["enabled"])
	assert.Equal(t, float64(2), m["paused"])
	assert.Equal(t, float64(5), m["total"])
	assert.Equal(t, float64(1), m["failed_runs_24h"],
		"exactly one job is failed, schedule-spawned and inside the window")
	assert.Equal(t, float64(1), m["failing"],
		"failing counts schedules carrying last_error and is not windowed")
}

// The key set is CLOSED and every key is always present: there is no omitempty
// anywhere in this response. Asserted against hand-written names.
func TestScheduledJobStats_ExactKeySet(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Owner", "sjkeys-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	code, m := getScheduledJobStats(t, srv, ownerToken)
	require.Equal(t, http.StatusOK, code)

	want := []string{"enabled", "paused", "total", "failed_runs_24h", "failing"}
	for _, k := range want {
		v, present := m[k]
		require.True(t, present, "key %q must always be present, even at zero", k)
		n, ok := v.(float64)
		require.True(t, ok, "key %q must be a number, got %T", k, v)
		assert.GreaterOrEqual(t, n, float64(0), "key %q must be non-negative", k)
	}
	assert.Len(t, m, len(want), "the response must carry exactly these keys, got %#v", m)
}

// Two owners get different numbers from the IDENTICAL request, and each one's
// total equals the total their own unfiltered list returns.
func TestScheduledJobStats_OwnerScoped(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjscoped-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	a := createTestUser(t, q, "A", "a@sjscoped.test", false)
	b := createTestUser(t, q, "B", "b@sjscoped.test", false)
	aToken := createTestToken(t, q, a.ID)
	bToken := createTestToken(t, q, b.ID)

	seedFilterSchedule(t, pool, "a-1", uuidString(a.ID), "@daily", true)
	seedFilterSchedule(t, pool, "a-2", uuidString(a.ID), "@daily", false)
	seedFilterSchedule(t, pool, "b-1", uuidString(b.ID), "@daily", true)

	for _, tc := range []struct {
		name      string
		token     string
		wantTotal float64
	}{
		{"a", aToken, 2},
		{"b", bToken, 1},
		{"admin", adminToken, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, m := getScheduledJobStats(t, srv, tc.token)
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, tc.wantTotal, m["total"])
			assert.Equal(t, m["total"], m["enabled"].(float64)+m["paused"].(float64),
				"total must equal enabled + paused exactly")

			listCode, p := getScheduledJobsPage(t, srv, tc.token, "limit=50")
			require.Equal(t, http.StatusOK, listCode)
			assert.Equal(t, int64(tc.wantTotal), p.Total,
				"stats.total must equal the unfiltered list's total for the same caller")
		})
	}
}

// Auth-only, not admin-only, and NOT shadowed by the {id} route. Go's ServeMux
// prefers the literal segment, but "stats" is also not a UUID, so a regression
// would surface as a 400 invalid id rather than a 404.
func TestScheduledJobStats_ReachableByANonAdminAndNotShadowed(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Owner", "sjroute-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	code, _ := getScheduledJobStats(t, srv, ownerToken)
	assert.Equal(t, http.StatusOK, code,
		"a non-admin must reach /stats; a 400 here means the {id} route matched")

	req := httptest.NewRequest("GET", "/v1/scheduled-jobs/stats", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the route must still require auth")
}

// /stats accepts NO filters: it is always the whole in-scope census, so
// stats.total and the list's total agree only when no filter is active.
func TestScheduledJobStats_IgnoresFilters(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Owner", "sjnofilter-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	seedFilterSchedule(t, pool, "live", uuidString(owner.ID), "@daily", true)
	seedFilterSchedule(t, pool, "paused", uuidString(owner.ID), "@daily", false)

	req := httptest.NewRequest("GET", "/v1/scheduled-jobs/stats?enabled=true&q=nothing", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var m map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&m))
	assert.Equal(t, float64(2), m["total"], "/stats is the whole census; filters do not apply")

	// The list under the same filters returns one row, so the two numbers are
	// genuinely different here and the assertion above is not vacuous.
	code, p := getScheduledJobsPage(t, srv, ownerToken, "enabled=true")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(1), p.Total)
}
