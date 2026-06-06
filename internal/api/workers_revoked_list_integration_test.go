//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedRevokedWorker inserts a worker with status 'revoked' and a revoked_at.
func seedRevokedWorker(t *testing.T, pool *pgxpool.Pool, name string, revokedAt time.Time) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, status, revoked_at)
		 VALUES ($1, $2, 4, 16, 0, '', 'linux', 'revoked', $3)`,
		name, name+"-host", revokedAt)
	require.NoError(t, err)
}

func getRevokedWorkersPage(t *testing.T, srv interface{ Handler() http.Handler }, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/workers/revoked"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	}
	return rec.Code, resp
}

func TestListRevokedWorkers_ReturnsOnlyRevokedWithTimestamp(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "revoked-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedWorker(t, pool, "live-1", "online", nil)
	seedRevokedWorker(t, pool, "gone-1", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	code, p := getRevokedWorkersPage(t, srv, token, "limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	require.Equal(t, "gone-1", p.Items[0]["name"])
	require.NotEmpty(t, p.Items[0]["revoked_at"], "revoked_at must be present")
	require.EqualValues(t, 1, p.Total)
}

func TestListRevokedWorkers_AdminOnly(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Plain", "revoked-plain@test.com", false)
	token := createTestToken(t, q, user.ID)

	code, _ := getRevokedWorkersPage(t, srv, token, "limit=50")
	require.Equal(t, http.StatusForbidden, code)
}
