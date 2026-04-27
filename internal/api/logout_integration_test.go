//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogout_CurrentToken_RevokesOnlyThatToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	tokenA := registerAndLogin(t, srv, q, "alice@test.com", "alicepassword")

	// Issue a second token by logging in again.
	loginBody, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "alicepassword",
	})
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRec, loginReq)
	require.Equal(t, http.StatusCreated, loginRec.Code)
	var loginResp map[string]any
	require.NoError(t, json.NewDecoder(loginRec.Body).Decode(&loginResp))
	tokenB := loginResp["token"].(string)

	// Log out tokenA.
	logoutReq := httptest.NewRequest("DELETE", "/v1/auth/token", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+tokenA)
	logoutRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(logoutRec, logoutReq)
	assert.Equal(t, http.StatusNoContent, logoutRec.Code)

	// tokenA is now 401.
	probeA := httptest.NewRequest("GET", "/v1/jobs", nil)
	probeA.Header.Set("Authorization", "Bearer "+tokenA)
	probeARec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeARec, probeA)
	assert.Equal(t, http.StatusUnauthorized, probeARec.Code)

	// tokenB still works.
	probeB := httptest.NewRequest("GET", "/v1/jobs", nil)
	probeB.Header.Set("Authorization", "Bearer "+tokenB)
	probeBRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeBRec, probeB)
	assert.Equal(t, http.StatusOK, probeBRec.Code)
}

func TestLogout_NoToken_Returns401(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	req := httptest.NewRequest("DELETE", "/v1/auth/token", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLogout_AllTokens_RevokesEverySessionForUser(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	tokenA := registerAndLogin(t, srv, q, "alice@test.com", "alicepassword")

	// Second login = second token.
	loginBody, _ := json.Marshal(map[string]string{
		"email": "alice@test.com", "password": "alicepassword",
	})
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRec, loginReq)
	require.Equal(t, http.StatusCreated, loginRec.Code)
	var loginResp map[string]any
	require.NoError(t, json.NewDecoder(loginRec.Body).Decode(&loginResp))
	tokenB := loginResp["token"].(string)

	// Logout-all using tokenA.
	req := httptest.NewRequest("DELETE", "/v1/auth/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Both tokens are now 401.
	for _, tok := range []string{tokenA, tokenB} {
		probe := httptest.NewRequest("GET", "/v1/jobs", nil)
		probe.Header.Set("Authorization", "Bearer "+tok)
		probeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(probeRec, probe)
		assert.Equal(t, http.StatusUnauthorized, probeRec.Code)
	}
}
