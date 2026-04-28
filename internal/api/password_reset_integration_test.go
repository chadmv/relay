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
	"golang.org/x/crypto/bcrypt"
)

// loginAsAdmin creates an admin user directly in the store (since
// /v1/auth/register always produces non-admin users), logs them in via the
// public login endpoint, and returns the resulting bearer token.
func loginAsAdmin(t *testing.T, srv *api.Server, q *store.Queries, email, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	_, err = q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: email, Email: email, IsAdmin: true, PasswordHash: string(hash),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["token"].(string)
}

func TestAdminPasswordReset_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	targetToken := registerAndLogin(t, srv, q, "target@test.com", "oldpassword")

	body, _ := json.Marshal(map[string]string{
		"email": "target@test.com", "new_password": "resetpass1",
	})
	req := httptest.NewRequest("POST", "/v1/users/password-reset", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Target's old token is now 401.
	probe := httptest.NewRequest("GET", "/v1/jobs", nil)
	probe.Header.Set("Authorization", "Bearer "+targetToken)
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probe)
	assert.Equal(t, http.StatusUnauthorized, probeRec.Code)

	// Target can log in with new password.
	loginBody, _ := json.Marshal(map[string]string{
		"email": "target@test.com", "password": "resetpass1",
	})
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRec, loginReq)
	assert.Equal(t, http.StatusCreated, loginRec.Code)

	// Admin's own token still works.
	adminProbe := httptest.NewRequest("GET", "/v1/jobs", nil)
	adminProbe.Header.Set("Authorization", "Bearer "+adminToken)
	adminProbeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(adminProbeRec, adminProbe)
	assert.Equal(t, http.StatusOK, adminProbeRec.Code)
}

func TestAdminPasswordReset_NonAdminCaller_Returns403(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")
	_ = registerAndLogin(t, srv, q, "target@test.com", "targetpassword")

	body, _ := json.Marshal(map[string]string{
		"email": "target@test.com", "new_password": "resetpass1",
	})
	req := httptest.NewRequest("POST", "/v1/users/password-reset", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAdminPasswordReset_UnknownEmail_Returns404(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body, _ := json.Marshal(map[string]string{
		"email": "nobody@test.com", "new_password": "resetpass1",
	})
	req := httptest.NewRequest("POST", "/v1/users/password-reset", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestAdminPasswordReset_TokenDeleteFails_RollsBackPassword verifies that if
// the session-revocation step fails mid-handler, the password update is rolled
// back. Forces failure with a BEFORE DELETE trigger on api_tokens.
func TestAdminPasswordReset_TokenDeleteFails_RollsBackPassword(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	_ = registerAndLogin(t, srv, q, "target@test.com", "oldpassword")

	installFailDeleteTrigger(t, pool, "api_tokens")

	body, _ := json.Marshal(map[string]string{
		"email": "target@test.com", "new_password": "resetpass1",
	})
	req := httptest.NewRequest("POST", "/v1/users/password-reset", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	target, err := q.GetUserByEmail(t.Context(), "target@test.com")
	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(target.PasswordHash), []byte("oldpassword")),
		"password should not have been updated when the tx rolled back")
}

func TestAdminPasswordReset_PasswordTooShort_Returns400(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body, _ := json.Marshal(map[string]string{
		"email": "target@test.com", "new_password": "short",
	})
	req := httptest.NewRequest("POST", "/v1/users/password-reset", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "password must be at least 8 characters", resp["error"])
}
