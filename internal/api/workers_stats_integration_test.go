//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func getWorkerStats(t *testing.T, srv interface {
	Handler() http.Handler
}, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/workers/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	}
	return rec.Code, body
}

func TestWorkerStats_BucketsAndTotal(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	// Non-admin user: the endpoint is not admin-only.
	user := createTestUser(t, q, "Stats", "stats-user@test.com", false)
	token := createTestToken(t, q, user.ID)

	// Buckets we expect: online=2, stale=1, offline=1, disabled=2, total=6.
	// revoked-only worker is excluded; the disabled+revoked worker counts as disabled.
	seedWorker(t, pool, "on-1", "online", nil)
	seedWorker(t, pool, "on-2", "online", nil)
	seedWorker(t, pool, "st-1", "stale", nil)
	seedWorker(t, pool, "off-1", "offline", nil)
	seedWorker(t, pool, "rev-1", "revoked", nil) // excluded entirely

	// disabled with an internal online status -> counts as disabled
	dis := seedWorker(t, pool, "dis-1", "online", nil)
	_, err := pool.Exec(t.Context(), "UPDATE workers SET disabled_at = NOW() WHERE id = $1", dis)
	require.NoError(t, err)

	// disabled AND revoked -> overlay wins, counts as disabled
	disRev := seedWorker(t, pool, "dis-rev-1", "revoked", nil)
	_, err = pool.Exec(t.Context(), "UPDATE workers SET disabled_at = NOW() WHERE id = $1", disRev)
	require.NoError(t, err)

	code, body := getWorkerStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 2, body["online"])
	require.EqualValues(t, 1, body["stale"])
	require.EqualValues(t, 1, body["offline"])
	require.EqualValues(t, 2, body["disabled"])
	// total excludes the revoked-only worker: 2+1+1+2 = 6 (not 7).
	require.EqualValues(t, 6, body["total"])
}

func TestWorkerStats_EmptyFleet(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Empty", "stats-empty@test.com", false)
	token := createTestToken(t, q, user.ID)

	code, body := getWorkerStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["online"])
	require.EqualValues(t, 0, body["disabled"])
	require.EqualValues(t, 0, body["total"])
}
