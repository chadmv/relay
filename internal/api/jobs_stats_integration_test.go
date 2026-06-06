//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func getJobStats(t *testing.T, srv interface {
	Handler() http.Handler
}, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/jobs/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	}
	return rec.Code, body
}

func TestJobStats_BucketsAndWindow(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Stats", "job-stats-user@test.com", false)
	token := createTestToken(t, q, user.ID)

	seed := func(status string, updatedAgo string) {
		var id pgtype.UUID
		err := pool.QueryRow(t.Context(),
			`INSERT INTO jobs (name, priority, submitted_by, status)
			 VALUES ('j', 'normal', $1, $2) RETURNING id`, user.ID, status).Scan(&id)
		require.NoError(t, err)
		_, err = pool.Exec(t.Context(),
			`UPDATE jobs SET updated_at = NOW() - $2::interval WHERE id = $1`, id, updatedAgo)
		require.NoError(t, err)
	}

	// running=2 (running + dispatched), queued=2 (queued + pending),
	// done_24h=1 (a second done is 48h old, outside the window),
	// failed_24h=2 (failed + timed_out within 24h).
	seed("running", "1 hour")
	seed("dispatched", "1 hour")
	seed("queued", "1 hour")
	seed("pending", "1 hour")
	seed("done", "1 hour")
	seed("done", "48 hours") // outside window - not counted
	seed("failed", "1 hour")
	seed("timed_out", "1 hour")
	seed("cancelled", "1 hour") // in no bucket

	code, body := getJobStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 2, body["running"])
	require.EqualValues(t, 2, body["queued"])
	require.EqualValues(t, 1, body["done_24h"])
	require.EqualValues(t, 2, body["failed_24h"])
}
