//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/api"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedReservationForWorkers inserts a reservation naming an explicit worker_ids
// array, which seedReservation does not expose.
func seedReservationForWorkers(t *testing.T, pool *pgxpool.Pool, name string, workerIDs []string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO reservations (name, selector, worker_ids)
		 VALUES ($1, '{}', $2::uuid[])
		 RETURNING id`,
		name, workerIDs,
	).Scan(&id)
	require.NoError(t, err, "seedReservationForWorkers %s", name)
	return id
}

// TestListReservations_WorkerFilterArms_FirstPage mirrors the schedules guard.
// EVERY ARM IS EXERCISED ON THE FIRST PAGE WITH NO CURSOR, for the same reason:
// an unparenthesised cursor disjunction only misbehaves when cursor_set is
// false.
func TestListReservations_WorkerFilterArms_FirstPage(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "resfilter-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	const wantedWorker = "3f0a1b2c-4d5e-4f60-8192-a3b4c5d6e7f8"
	const otherWorker = "11111111-2222-4333-8444-555555555555"

	// The matching reservation names TWO workers, so a predicate that required
	// equality of the whole array rather than containment would fail here.
	keepID := seedReservationForWorkers(t, pool, "keeper", []string{otherWorker, wantedWorker})
	seedReservationForWorkers(t, pool, "other", []string{otherWorker})

	for _, arm := range sortArms(api.ReservationsSortSpec) {
		t.Run(arm, func(t *testing.T) {
			code, p := getReservationsPage(t, srv, adminToken, arm+"&worker_id="+wantedWorker)
			require.Equal(t, http.StatusOK, code)
			require.Len(t, p.Items, 1,
				"%s on the FIRST PAGE must return exactly the reservation naming the "+
					"worker; two rows means the filter was dropped", arm)
			assert.Equal(t, keepID, p.Items[0]["id"])
			assert.Equal(t, int64(1), p.Total,
				"total must count the filtered set, not the table")
		})
	}
}

// An id no reservation targets is an EMPTY PAGE with total 0, not a 404.
// worker_ids has no foreign key, so this endpoint cannot distinguish "never
// existed" from "deleted" and must not claim to.
func TestListReservations_UnknownWorkerIsAnEmptyPage(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "resunknown-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	seedReservationForWorkers(t, pool, "only", []string{"11111111-2222-4333-8444-555555555555"})

	code, p := getReservationsPage(t, srv, adminToken,
		"worker_id=99999999-8888-4777-8666-555555555555")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, p.Items)
	assert.Equal(t, int64(0), p.Total)
}

// A cursor carries no record of the filter that was active when it was issued,
// and the server does not reject a mismatched one. Filter correctness is
// nevertheless cursor-independent: the predicate is applied in SQL alongside the
// keyset comparison, so a stale cursor can start a page at a surprising position
// but can NEVER return a row that fails the current predicate.
func TestListReservations_StaleCursorNeverReturnsAFailingRow(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "rescur-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	const wantedWorker = "3f0a1b2c-4d5e-4f60-8192-a3b4c5d6e7f8"
	const otherWorker = "11111111-2222-4333-8444-555555555555"

	for i := 0; i < 4; i++ {
		seedReservationForWorkers(t, pool, "wanted-"+string(rune('a'+i)), []string{wantedWorker})
		seedReservationForWorkers(t, pool, "other-"+string(rune('a'+i)), []string{otherWorker})
	}

	code, p := getReservationsPage(t, srv, adminToken, "worker_id="+otherWorker+"&limit=2")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor)

	code, p = getReservationsPage(t, srv, adminToken,
		"worker_id="+wantedWorker+"&limit=2&cursor="+p.NextCursor)
	require.Equal(t, http.StatusOK, code)
	for _, it := range p.Items {
		ids, _ := it["worker_ids"].([]any)
		assert.Contains(t, ids, wantedWorker,
			"a cursor issued under one worker filter must never carry a row failing the new one")
	}
}

// The parser's 400s are reached THROUGH THE HANDLER. The default-lane tests pin
// the bodies; this pins that the parser is actually on the request path, and
// that the arity refusal covers worker_id.
func TestListReservations_FilterErrorsAreReachedThroughTheHandler(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "reserr-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"not a uuid", "worker_id=nope", "invalid worker_id; expected a UUID"},
		{"repeated", "worker_id=11111111-2222-4333-8444-555555555555&worker_id=3f0a1b2c-4d5e-4f60-8192-a3b4c5d6e7f8",
			`query parameter "worker_id" must appear at most once`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/reservations?"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			var body struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
			assert.Equal(t, tc.want, body.Error)
		})
	}
}
