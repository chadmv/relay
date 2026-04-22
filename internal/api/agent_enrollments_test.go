//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fmtUUID formats a pgtype.UUID as a canonical UUID string.
func fmtUUID(u pgtype.UUID) string {
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func TestCreateAgentEnrollment_AdminOnly(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Regular User", "enroll-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)
	admin := createTestUser(t, q, "Admin User", "enroll-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	body := `{"ttl_seconds": 3600}`

	// Non-admin should get 403
	req := httptest.NewRequest("POST", "/v1/agent-enrollments", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Admin should get 201 with token and expires_at
	req2 := httptest.NewRequest("POST", "/v1/agent-enrollments", strings.NewReader(body))
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusCreated, rec2.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&resp))
	token, ok := resp["token"].(string)
	assert.True(t, ok)
	assert.NotEmpty(t, token)
	expiresAt, ok := resp["expires_at"].(string)
	assert.True(t, ok)
	assert.NotEmpty(t, expiresAt)
}

func TestCreateAgentEnrollment_RejectsInvalidTTL(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Admin TTL", "enroll-admin-ttl@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	cases := []struct {
		body string
		desc string
	}{
		{`{"ttl_seconds": 30}`, "too short"},
		{`{"ttl_seconds": 1000000}`, "too long"},
		{`{"ttl_seconds": -1}`, "negative"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/agent-enrollments", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+adminToken)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", tc.body)
		})
	}
}

func TestListAgentEnrollments_AdminOnly(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "List User", "list-enroll-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)
	admin := createTestUser(t, q, "List Admin", "list-enroll-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	// Seed one enrollment via admin POST
	seedBody := `{"ttl_seconds": 3600}`
	req0 := httptest.NewRequest("POST", "/v1/agent-enrollments", strings.NewReader(seedBody))
	req0.Header.Set("Authorization", "Bearer "+adminToken)
	req0.Header.Set("Content-Type", "application/json")
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	require.Equal(t, http.StatusCreated, rec0.Code)

	// Non-admin should get 403
	req := httptest.NewRequest("GET", "/v1/agent-enrollments", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Admin should get 200 with array of enrollments
	req2 := httptest.NewRequest("GET", "/v1/agent-enrollments", nil)
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var items []map[string]any
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&items))
	assert.GreaterOrEqual(t, len(items), 1)

	// Items must NOT have "token" or "token_hash" keys
	for _, item := range items {
		_, hasToken := item["token"]
		assert.False(t, hasToken, "response item should not contain 'token'")
		_, hasTokenHash := item["token_hash"]
		assert.False(t, hasTokenHash, "response item should not contain 'token_hash'")
	}
}

func TestDeleteWorkerToken_AdminOnly(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Del User", "del-token-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)
	admin := createTestUser(t, q, "Del Admin", "del-token-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	// Create a test worker via the store
	ctx := t.Context()
	row, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "test-worker", Hostname: "test-worker", CpuCores: 4, RamGb: 16,
		GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	workerID := fmtUUID(row.ID)

	// Non-admin should get 403
	req := httptest.NewRequest("DELETE", "/v1/workers/"+workerID+"/token", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Admin should get 204
	req2 := httptest.NewRequest("DELETE", "/v1/workers/"+workerID+"/token", nil)
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusNoContent, rec2.Code)

	// Idempotent — second call also 204
	req3 := httptest.NewRequest("DELETE", "/v1/workers/"+workerID+"/token", nil)
	req3.Header.Set("Authorization", "Bearer "+adminToken)
	rec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusNoContent, rec3.Code)
}
