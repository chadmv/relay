//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// seedUser creates a non-admin user directly via the store. Returns the row.
func seedUser(t *testing.T, q *store.Queries, email, name string) store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("placeholder"), bcrypt.MinCost)
	require.NoError(t, err)
	u, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: false, PasswordHash: string(hash),
	})
	require.NoError(t, err)
	return u
}

func getUsers(t *testing.T, srv *api.Server, token, query string) (int, []map[string]any, map[string]any) {
	t.Helper()
	url := "/v1/users"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	body := rec.Body.Bytes()
	// Try array first (success), then object (error).
	var arr []map[string]any
	if err := json.Unmarshal(body, &arr); err == nil {
		return rec.Code, arr, nil
	}
	var obj map[string]any
	_ = json.Unmarshal(body, &obj)
	return rec.Code, nil, obj
}

func TestListUsers_AdminSeesAll(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	seedUser(t, q, "alice@test.com", "Alice")
	seedUser(t, q, "bob@test.com", "Bob")

	code, users, _ := getUsers(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 3)

	emails := []string{users[0]["email"].(string), users[1]["email"].(string), users[2]["email"].(string)}
	assert.ElementsMatch(t, []string{"admin@test.com", "alice@test.com", "bob@test.com"}, emails)

	for _, u := range users {
		_, hasHash := u["password_hash"]
		assert.False(t, hasHash, "response must not include password_hash")
		assert.NotEmpty(t, u["id"])
		assert.NotEmpty(t, u["email"])
		assert.NotEmpty(t, u["created_at"])
		_, hasAdmin := u["is_admin"]
		assert.True(t, hasAdmin)
	}
}

func TestListUsers_NonAdminForbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, _, errBody := getUsers(t, srv, userToken, "")
	assert.Equal(t, http.StatusForbidden, code)
	assert.NotEmpty(t, errBody["error"])
}

func TestListUsers_FilterByEmailHit(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	seedUser(t, q, "alice@test.com", "Alice")
	seedUser(t, q, "bob@test.com", "Bob")

	code, users, _ := getUsers(t, srv, adminToken, "email=alice@test.com")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 1)
	assert.Equal(t, "alice@test.com", users[0]["email"])
	assert.Equal(t, "Alice", users[0]["name"])
	assert.Equal(t, false, users[0]["is_admin"])
	_, hasHash := users[0]["password_hash"]
	assert.False(t, hasHash)
}

func TestListUsers_FilterByEmailMiss(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, users, _ := getUsers(t, srv, adminToken, "email=nobody@test.com")
	require.Equal(t, http.StatusOK, code)
	assert.NotNil(t, users, "response must be a JSON array, not null")
	assert.Len(t, users, 0)
}

func TestListUsers_OrderedByCreatedAt(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	// Sleep 10ms between inserts to guarantee distinct created_at values
	// (Postgres timestamptz has microsecond precision but sequential inserts
	// can collide on some systems).
	seedUser(t, q, "alice@test.com", "Alice")
	time.Sleep(10 * time.Millisecond)
	seedUser(t, q, "bob@test.com", "Bob")
	time.Sleep(10 * time.Millisecond)
	seedUser(t, q, "carol@test.com", "Carol")

	code, users, _ := getUsers(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 4)

	expected := []string{"admin@test.com", "alice@test.com", "bob@test.com", "carol@test.com"}
	for i, want := range expected {
		assert.Equal(t, want, users[i]["email"], "users[%d]", i)
	}
}
