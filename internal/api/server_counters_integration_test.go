//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerCounters_Gating is the behavioural half of the admin gate. The AST
// guard in server_counters_test.go can see that the route is spelled
// auth(admin(...)); only this can see that spelling produce a 403.
func TestServerCounters_Gating(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Counters Admin", "counters-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	user := createTestUser(t, q, "Counters User", "counters-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)

	get := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/server/counters", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	assert.Equal(t, http.StatusUnauthorized, get("").Code, "no bearer token must be 401")
	assert.Equal(t, http.StatusForbidden, get(userToken).Code,
		"these numbers describe adversary activity and internal control state; a non-admin must not read them")

	rec := get(adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "an admin must be able to read the counters: %s", rec.Body.String())

	// newTestServer wires no counter sources, so this also proves the
	// absent-not-zero rule through the whole real stack.
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.Contains(t, top, "started_at")
	assert.NotContains(t, top, "grpc_admission",
		"an unwired section must be ABSENT: a section of zeros would say the control ran and stopped "+
			"nothing, which is a different fact")

	var startedAt time.Time
	require.NoError(t, json.Unmarshal(top["started_at"], &startedAt))
	assert.False(t, startedAt.IsZero(), "started_at must be a real timestamp: api.New records it")
}
