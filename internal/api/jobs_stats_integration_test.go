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

	// New bucketing after JobStatusCounts reconciliation:
	//   running    = COUNT(status = 'running')
	//   queued     = COUNT(status = 'pending')
	//   done_24h   = COUNT(status = 'done'                  within 24h)
	//   failed_24h = COUNT(status IN ('failed','cancelled') within 24h)
	// Only valid jobs.status values may be seeded now that jobs_status_check
	// exists: pending, running, done, failed, cancelled.
	seed("running", "1 hour")   // running=1
	seed("pending", "1 hour")   // queued=1
	seed("done", "1 hour")      // done_24h=1
	seed("done", "48 hours")    // outside window - not counted
	seed("failed", "1 hour")    // failed_24h += 1
	seed("cancelled", "1 hour") // failed_24h += 1 (cancelled folds into failed_24h)

	code, body := getJobStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 1, body["running"])
	require.EqualValues(t, 1, body["queued"])
	require.EqualValues(t, 1, body["done_24h"])
	require.EqualValues(t, 2, body["failed_24h"])
}
