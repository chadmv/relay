//go:build integration

package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Revoked workers must not appear in GET /v1/workers, and must not be counted
// in the list total. Mirrors the exclusion the stats endpoint already applies.
func TestListWorkers_ExcludesRevoked(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "RevList", "revlist-user@test.com", false)
	token := createTestToken(t, q, user.ID)

	seedWorker(t, pool, "live-1", "online", nil)
	seedWorker(t, pool, "live-2", "offline", nil)
	seedWorker(t, pool, "gone-1", "revoked", nil)

	code, p := getWorkersPage(t, srv, token, "limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 2, "revoked worker must be excluded from the page")
	require.EqualValues(t, 2, p.Total, "revoked worker must be excluded from total")
	for _, w := range p.Items {
		require.NotEqual(t, "gone-1", w["name"], "revoked worker leaked into the list")
	}
}
