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

func newTestServer(t *testing.T) (*api.Server, *store.Queries) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	srv := api.New(pool, q, broker, registry, func() {})
	return srv, q
}

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCreateToken(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"email":"bob@example.com","name":"Bob"}`
	req := httptest.NewRequest("POST", "/v1/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	token, ok := resp["token"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, token)
}
