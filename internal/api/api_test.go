//go:build integration

package api_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func newTestServer(t *testing.T) (*api.Server, *store.Queries) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	srv := api.New(pool, q, broker, registry, nil, 0, 0, 0, 0)
	return srv, q
}

func createTestUser(t *testing.T, q *store.Queries, name, email string, isAdmin bool) store.User {
	t.Helper()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpassword1"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: isAdmin, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user
}

func createTestToken(t *testing.T, q *store.Queries, userID pgtype.UUID) string {
	t.Helper()
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	hash := tokenhash.Hash(rawHex)
	_, err := q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{},
	})
	require.NoError(t, err)
	return rawHex
}

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCreateAndGetJob(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{
        "name": "render-job",
        "priority": "normal",
        "tasks": [
            {"name": "task-a", "command": ["echo", "a"], "depends_on": []},
            {"name": "task-b", "command": ["echo", "b"], "depends_on": ["task-a"]}
        ]
    }`
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	jobID, ok := resp["id"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, jobID)

	req2 := httptest.NewRequest("GET", "/v1/jobs/"+jobID, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var job map[string]any
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&job))
	assert.Equal(t, "render-job", job["name"])
	tasks := job["tasks"].([]any)
	assert.Len(t, tasks, 2)
}

func TestGetTaskAndLogs(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "alice2@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{"name":"j","tasks":[{"name":"t","command":["echo","hi"]}]}`
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var job map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&job))
	jobID := job["id"].(string)
	taskID := job["tasks"].([]any)[0].(map[string]any)["id"].(string)

	req2 := httptest.NewRequest("GET", "/v1/jobs/"+jobID+"/tasks", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	req3 := httptest.NewRequest("GET", "/v1/tasks/"+taskID, nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	rec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusOK, rec3.Code)

	req4 := httptest.NewRequest("GET", "/v1/tasks/"+taskID+"/logs", nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	rec4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec4, req4)
	require.Equal(t, http.StatusOK, rec4.Code)
}

func TestListWorkers(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Admin", "admin@example.com", true)
	token := createTestToken(t, q, user.ID)

	req := httptest.NewRequest("GET", "/v1/workers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var workers []any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&workers))
	assert.Empty(t, workers)
}

func TestSSESubscribe(t *testing.T) {
	srv, q := newTestServer(t)
	ctx := t.Context()
	user := createTestUser(t, q, "Alice", "alice3@example.com", false)
	token := createTestToken(t, q, user.ID)

	req := httptest.NewRequest("GET", "/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Handler().ServeHTTP(rec, req)
	}()

	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}
