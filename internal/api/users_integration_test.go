//go:build integration

package api_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"github.com/jackc/pgx/v5/pgxpool"
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
	// Try envelope (success path), then object (error path).
	var env pageEnvelope[map[string]any]
	if err := json.Unmarshal(body, &env); err == nil && rec.Code == http.StatusOK {
		if env.Items == nil {
			env.Items = []map[string]any{}
		}
		return rec.Code, env.Items, nil
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
	time.Sleep(10 * time.Millisecond)
	seedUser(t, q, "alice@test.com", "Alice")
	time.Sleep(10 * time.Millisecond)
	seedUser(t, q, "bob@test.com", "Bob")
	time.Sleep(10 * time.Millisecond)
	seedUser(t, q, "carol@test.com", "Carol")

	code, users, _ := getUsers(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 4)

	// Paginated query orders by created_at DESC, so newest users appear first.
	expected := []string{"carol@test.com", "bob@test.com", "alice@test.com", "admin@test.com"}
	for i, want := range expected {
		assert.Equal(t, want, users[i]["email"], "users[%d]", i)
	}
}

func TestListUsers_FilterByEmailHit_NoPasswordHash(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	seedUser(t, q, "alice@test.com", "Alice")

	code, users, _ := getUsers(t, srv, adminToken, "email=alice@test.com")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 1)
	_, hasHash := users[0]["password_hash"]
	assert.False(t, hasHash, "email-filter hit must not include password_hash")
	// Public columns still present.
	assert.Equal(t, "alice@test.com", users[0]["email"])
	assert.Equal(t, "Alice", users[0]["name"])
	assert.Equal(t, false, users[0]["is_admin"])
	assert.NotEmpty(t, users[0]["id"])
	assert.NotEmpty(t, users[0]["created_at"])
}

// patchJSON sends a PATCH with a JSON body and returns (status code, parsed response body).
// The body map is non-nil for any response that decodes as a JSON object, including error
// envelopes (which have an "error" key).
func patchJSON(t *testing.T, srv *api.Server, token, path string, body any) (int, map[string]any) {
	t.Helper()
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("PATCH", path, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var obj map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)
	return rec.Code, obj
}

func TestUpdateMe_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "New Name"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "user@test.com", body["email"])
	assert.Equal(t, "New Name", body["name"])
	assert.Equal(t, false, body["is_admin"])
	assert.NotEmpty(t, body["id"])
	assert.NotEmpty(t, body["created_at"])
	_, hasHash := body["password_hash"]
	assert.False(t, hasHash)
}

func TestUpdateMe_EmptyName(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": ""})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "name is required")
}

func TestUpdateMe_WhitespaceOnlyName(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "   "})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "name is required")
}

func TestUpdateMe_TrimsSurroundingWhitespace(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "  Padded Name  "})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Padded Name", body["name"])
}

func TestUpdateMe_MissingNameField(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, body := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "name is required")
}

func TestUpdateMe_InvalidJSON(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	req := httptest.NewRequest("PATCH", "/v1/users/me", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateMe_NoToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	code, _ := patchJSON(t, srv, "", "/v1/users/me", map[string]any{"name": "x"})
	require.Equal(t, http.StatusUnauthorized, code)
}

func TestUpdateMe_PersistsAcrossList(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")

	code, _ := patchJSON(t, srv, userToken, "/v1/users/me", map[string]any{"name": "Persisted"})
	require.Equal(t, http.StatusOK, code)

	code, users, _ := getUsers(t, srv, adminToken, "email=user@test.com")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 1)
	assert.Equal(t, "Persisted", users[0]["name"])
}

// uuidString converts a pgtype.UUID into the canonical hyphenated string form
// (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx) used in URL paths.
func uuidString(id pgtype.UUID) string {
	b := id.Bytes
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	for i, j := 0, 0; i < 16; i++ {
		if j == 8 || j == 13 || j == 18 || j == 23 {
			out[j] = '-'
			j++
		}
		out[j] = hex[b[i]>>4]
		out[j+1] = hex[b[i]&0x0f]
		j += 2
	}
	return string(out)
}

func TestAdminUpdateUser_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	target := seedUser(t, q, "alice@test.com", "Alice")

	path := "/v1/users/" + uuidString(target.ID)
	code, body := patchJSON(t, srv, adminToken, path, map[string]any{"name": "Alice Anderson"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "alice@test.com", body["email"])
	assert.Equal(t, "Alice Anderson", body["name"])
	assert.Equal(t, false, body["is_admin"])
	assert.NotEmpty(t, body["id"])
	assert.NotEmpty(t, body["created_at"])
}

func TestAdminUpdateUser_NonAdminForbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	userToken := registerAndLogin(t, srv, q, "user@test.com", "userpassword")
	target := seedUser(t, q, "alice@test.com", "Alice")

	path := "/v1/users/" + uuidString(target.ID)
	code, body := patchJSON(t, srv, userToken, path, map[string]any{"name": "x"})
	require.Equal(t, http.StatusForbidden, code)
	assert.NotEmpty(t, body["error"])
}

func TestAdminUpdateUser_NotFound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	path := "/v1/users/00000000-0000-0000-0000-000000000000"
	code, body := patchJSON(t, srv, adminToken, path, map[string]any{"name": "Nobody"})
	require.Equal(t, http.StatusNotFound, code)
	assert.Contains(t, body["error"], "user not found")
}

func TestAdminUpdateUser_InvalidUUID(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := patchJSON(t, srv, adminToken, "/v1/users/not-a-uuid", map[string]any{"name": "x"})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body["error"], "invalid user id")
}

func TestAdminUpdateUser_AdminUpdatesSelfViaAdminPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	admin, err := q.GetUserByEmail(t.Context(), "admin@test.com")
	require.NoError(t, err)

	path := "/v1/users/" + uuidString(admin.ID)
	code, body := patchJSON(t, srv, adminToken, path, map[string]any{"name": "Renamed Admin"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Renamed Admin", body["name"])
	assert.Equal(t, true, body["is_admin"])
}

// loginAs logs in as any user and returns the bearer token.
func loginAs(t *testing.T, srv *api.Server, email, password string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["token"]
}

// postJSON sends a POST with a JSON body and returns (status code, parsed response body).
func postJSON(t *testing.T, srv *api.Server, token, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp map[string]any
	if rec.Body.Len() > 0 {
		_ = json.NewDecoder(rec.Body).Decode(&resp)
	}
	return rec.Code, resp
}

func TestAdminCreateUser_HappyPath(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body := map[string]any{
		"email":    "newhire@test.com",
		"name":     "New Hire",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	require.Equal(t, http.StatusCreated, code)

	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, "newhire@test.com", resp["email"])
	assert.Equal(t, "New Hire", resp["name"])
	assert.Equal(t, false, resp["is_admin"])
	assert.NotEmpty(t, resp["created_at"])
	assert.Nil(t, resp["password_hash"], "password_hash must never be returned")

	// New user can log in.
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "newhire@test.com",
		"password": "securepass1",
	})
	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(loginBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestAdminCreateUser_CreatesAdmin(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body := map[string]any{
		"email":    "admin2@test.com",
		"name":     "Second Admin",
		"password": "securepass1",
		"is_admin": true,
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	require.Equal(t, http.StatusCreated, code)
	assert.Equal(t, true, resp["is_admin"])

	// Sanity-check via the store: row truly has is_admin=true.
	u, err := q.GetUserByEmail(t.Context(), "admin2@test.com")
	require.NoError(t, err)
	assert.True(t, u.IsAdmin)
}

func TestAdminCreateUser_NonAdminForbidden(t *testing.T) {
	srv, q := newTestServer(t)
	createTestUser(t, q, "Plain User", "user@test.com", false)
	userToken := loginAs(t, srv, "user@test.com", "testpassword1")

	body := map[string]any{
		"email":    "shouldfail@test.com",
		"password": "securepass1",
	}
	code, _ := postJSON(t, srv, userToken, "/v1/users", body)
	assert.Equal(t, http.StatusForbidden, code)
}

func TestAdminCreateUser_Unauthenticated(t *testing.T) {
	srv, _ := newTestServer(t)
	body := map[string]any{
		"email":    "x@test.com",
		"password": "securepass1",
	}
	code, _ := postJSON(t, srv, "", "/v1/users", body)
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestAdminCreateUser_DuplicateEmail(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	seedUser(t, q, "dup@test.com", "Existing")

	body := map[string]any{
		"email":    "dup@test.com",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "email already registered", resp["error"])
}

func TestAdminCreateUser_InvalidEmail(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body := map[string]any{
		"email":    "not-an-email",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid email address", resp["error"])
}

func TestAdminCreateUser_WeakPassword(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body := map[string]any{
		"email":    "weak@test.com",
		"password": "short",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "password must be at least 8 characters", resp["error"])
}

func TestAdminCreateUser_MissingPassword(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body := map[string]any{
		"email": "nopw@test.com",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "password must be at least 8 characters", resp["error"])
}

func TestAdminCreateUser_NameDefaultsToEmail(t *testing.T) {
	srv, q := newTestServer(t)
	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	body := map[string]any{
		"email":    "noname@test.com",
		"password": "securepass1",
	}
	code, resp := postJSON(t, srv, adminToken, "/v1/users", body)
	require.Equal(t, http.StatusCreated, code)
	assert.Equal(t, "noname@test.com", resp["name"])
}

// ─── Archive helpers ─────────────────────────────────────────────────────────

// seedAdmin creates an admin user directly via the store. Does not log in.
func seedAdmin(t *testing.T, q *store.Queries, email, name string) store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("placeholder"), bcrypt.MinCost)
	require.NoError(t, err)
	u, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: true, PasswordHash: string(hash),
	})
	require.NoError(t, err)
	return u
}

// archiveUser sends POST /v1/users/{id}/archive as the given admin. Returns
// the response code and decoded body (object on error or success).
func archiveUser(t *testing.T, srv *api.Server, token, userID string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/users/"+userID+"/archive", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// countAPITokens returns the number of api_tokens rows for a user — used
// to verify the cascade in archive.
func countAPITokens(t *testing.T, pool *pgxpool.Pool, userID pgtype.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM api_tokens WHERE user_id = $1`, userID).Scan(&n)
	require.NoError(t, err)
	return n
}

// createAndLoginUser seeds a non-admin user with the given password, then
// logs in and returns the token. Mirrors loginAsAdmin but without IsAdmin.
// IMPORTANT: do not call seedUser separately for the same email — this
// helper already creates the row.
func createAndLoginUser(t *testing.T, srv *api.Server, q *store.Queries, email, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	_, err = q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: email, Email: email, IsAdmin: false, PasswordHash: string(hash),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["token"].(string)
}

// mustGetUser is a small helper for store-level tests.
func mustGetUser(t *testing.T, q *store.Queries, email string) store.User {
	t.Helper()
	u, err := q.GetUserByEmail(t.Context(), email)
	require.NoError(t, err)
	return u
}

// uuidStrTest is a tiny helper duplicating the package-private uuidStr so the
// _test package can stringify pgtype.UUID without exporting the helper.
func uuidStrTest(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ─── Archive tests ────────────────────────────────────────────────────────────

func TestArchiveUser_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	// Create alice with a known password and log her in (creates+logs in in
	// one shot — do NOT seedUser separately or you'll get a unique-violation
	// on email).
	aliceToken := createAndLoginUser(t, srv, q, "alice@test.com", "alicepass")
	target, err := q.GetUserByEmail(t.Context(), "alice@test.com")
	require.NoError(t, err)

	code, body := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, body["archived_at"])

	// Pre-existing alice token must now be rejected.
	req := httptest.NewRequest("GET", "/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Login attempt now returns generic 401.
	loginBody, _ := json.Marshal(map[string]string{"email": "alice@test.com", "password": "alicepass"})
	req = httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// api_tokens cascade.
	assert.Equal(t, 0, countAPITokens(t, pool, target.ID))
}

func TestArchiveUser_SelfArchiveForbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	admin, err := q.GetUserByEmail(t.Context(), "admin@test.com")
	require.NoError(t, err)

	code, body := archiveUser(t, srv, adminToken, uuidStrTest(admin.ID))
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "cannot archive yourself", body["error"])
}

// TestArchiveUser_LastAdminGuard exercises the last-admin guard by simulating
// a race: admin A authenticates, then A's row is archived externally (e.g.,
// by another admin in a parallel request) before A's archive call reaches the
// guard. BearerAuth does not filter on archived_at (see middleware.go), so
// A's still-valid token passes auth; the guard then catches the inconsistent
// state when A tries to archive admin B (the only remaining active admin).
// TestArchiveUser_ArchivedAdminTokenRejected supersedes the old
// TestArchiveUser_LastAdminGuard. That test simulated the login-vs-archive race
// by archiving admin A via direct SQL while keeping A's token, then relied on
// A's still-valid token reaching the last-admin guard. With the archived_at
// predicate now on GetTokenWithUser (Change A), A's token is rejected at the
// auth boundary (401 invalid token) before the handler runs, which is the
// correct, fixed behavior. The CountActiveAdmins guard in the handler remains as
// defense in depth; it is simply no longer reachable through an archived admin's
// own token.
func TestArchiveUser_ArchivedAdminTokenRejected(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminAToken := loginAsAdmin(t, srv, q, "admin-a@test.com", "passa")
	adminB := seedAdmin(t, q, "admin-b@test.com", "Admin B")

	// Archive A directly in the DB, leaving A's token row in place (the race).
	_, err := pool.Exec(t.Context(),
		`UPDATE users SET archived_at = NOW() WHERE email = 'admin-a@test.com'`)
	require.NoError(t, err)

	// A's still-present token is now rejected at the auth boundary.
	code, body := archiveUser(t, srv, adminAToken, uuidStrTest(adminB.ID))
	require.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "invalid token", body["error"])
}

func TestCountActiveAdmins(t *testing.T) {
	q := newTestQueries(t)

	// 0 admins.
	n, err := q.CountActiveAdmins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// Seed two admins, count = 2.
	_ = seedAdmin(t, q, "a@test.com", "A")
	_ = seedAdmin(t, q, "b@test.com", "B")
	n, err = q.CountActiveAdmins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	// Archive one, count = 1.
	_, err = q.ArchiveUser(t.Context(), mustGetUser(t, q, "b@test.com").ID)
	require.NoError(t, err)
	n, err = q.CountActiveAdmins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestArchiveUser_AlreadyArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	target := seedUser(t, q, "alice@test.com", "Alice")

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)

	code, body := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "user is already archived", body["error"])
}

func TestArchiveUser_NotFound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := archiveUser(t, srv, adminToken, "00000000-0000-0000-0000-000000000000")
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "user not found", body["error"])
}

func TestArchiveUser_InvalidUUID(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := archiveUser(t, srv, adminToken, "not-a-uuid")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid user id", body["error"])
}

func unarchiveUser(t *testing.T, srv *api.Server, token, userID string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/users/"+userID+"/unarchive", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestUnarchiveUser_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	aliceToken := createAndLoginUser(t, srv, q, "alice@test.com", "alicepass")
	target, err := q.GetUserByEmail(t.Context(), "alice@test.com")
	require.NoError(t, err)

	// Archive, then unarchive.
	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)

	code, body := unarchiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, body["archived_at"])

	// Old token still revoked.
	req := httptest.NewRequest("GET", "/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// New login succeeds.
	loginBody, _ := json.Marshal(map[string]string{"email": "alice@test.com", "password": "alicepass"})
	req = httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestUnarchiveUser_NotArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	target := seedUser(t, q, "alice@test.com", "Alice")

	code, body := unarchiveUser(t, srv, adminToken, uuidStrTest(target.ID))
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "user is not archived", body["error"])
}

func TestUnarchiveUser_NotFound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")

	code, body := unarchiveUser(t, srv, adminToken, "00000000-0000-0000-0000-000000000000")
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "user not found", body["error"])
}

func TestListUsers_FiltersArchivedByDefault(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	_ = seedUser(t, q, "bob@test.com", "Bob")

	// Archive alice.
	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	// Default list excludes alice.
	code, users, _ := getUsers(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	emails := emailSet(users)
	assert.NotContains(t, emails, "alice@test.com")
	assert.Contains(t, emails, "bob@test.com")
	assert.Contains(t, emails, "admin@test.com")
}

func TestListUsers_IncludeArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	_ = seedUser(t, q, "bob@test.com", "Bob")

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	code, users, _ := getUsers(t, srv, adminToken, "include_archived=true")
	require.Equal(t, http.StatusOK, code)
	emails := emailSet(users)
	assert.Contains(t, emails, "alice@test.com")
	assert.Contains(t, emails, "bob@test.com")

	// And the archived row carries archived_at.
	for _, u := range users {
		if u["email"] == "alice@test.com" {
			assert.NotNil(t, u["archived_at"])
		} else {
			assert.Nil(t, u["archived_at"])
		}
	}
}

func TestListUsers_EmailLookupHidesArchived(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	_, _ = archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))

	// Without include_archived, lookup returns [].
	code, users, _ := getUsers(t, srv, adminToken, "email=alice%40test.com")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, users, 0)

	// With include_archived, lookup returns the archived user.
	code, users, _ = getUsers(t, srv, adminToken, "email=alice%40test.com&include_archived=true")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, users, 1)
	assert.Equal(t, "alice@test.com", users[0]["email"])
	assert.NotNil(t, users[0]["archived_at"])
}

// emailSet extracts the email field from a list of decoded user rows.
func emailSet(users []map[string]any) map[string]bool {
	out := make(map[string]bool, len(users))
	for _, u := range users {
		out[u["email"].(string)] = true
	}
	return out
}

// mintRawToken inserts an api_tokens row directly for userID and returns the
// raw bearer string. Mirrors how auth.go issues tokens (random 32 bytes ->
// hex -> tokenhash.Hash), but bypasses login so the token survives a direct-SQL
// archive (reproducing the login-vs-archive race).
func mintRawToken(t *testing.T, q *store.Queries, userID pgtype.UUID) string {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: tokenhash.Hash(rawHex),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)
	return rawHex
}

// TestArchivedUser_TokenRejected reproduces the login-vs-archive race: a token
// minted for a user that is then archived via direct SQL (leaving the token in
// place) must be rejected at the auth boundary. Regression for the gap
// documented in TestArchiveUser_LastAdminGuard's comment.
func TestArchivedUser_TokenRejected(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	alice := seedUser(t, q, "alice@test.com", "Alice")
	aliceToken := mintRawToken(t, q, alice.ID)

	// Sanity: the token authenticates while alice is active.
	req := httptest.NewRequest("GET", "/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Archive alice via direct SQL, leaving the token row in place (the race).
	_, err := pool.Exec(t.Context(),
		`UPDATE users SET archived_at = NOW() WHERE email = 'alice@test.com'`)
	require.NoError(t, err)

	// The still-present token must now be rejected with the generic 401.
	req = httptest.NewRequest("GET", "/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	assert.Equal(t, "invalid token", body["error"])
}

// TestActiveUser_TokenStillValid guards against a regression where the new
// archived predicate would reject active users.
func TestActiveUser_TokenStillValid(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	alice := seedUser(t, q, "alice@test.com", "Alice")
	aliceToken := mintRawToken(t, q, alice.ID)

	req := httptest.NewRequest("GET", "/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// seedEnabledScheduledJob inserts an enabled scheduled job owned by ownerID and
// returns the created row. (Named to avoid colliding with the pool-based
// seedScheduledJob helper in scheduled_jobs_sort_integration_test.go.)
func seedEnabledScheduledJob(t *testing.T, q *store.Queries, ownerID pgtype.UUID, name string) store.ScheduledJob {
	t.Helper()
	sj, err := q.CreateScheduledJob(t.Context(), store.CreateScheduledJobParams{
		Name:          name,
		OwnerID:       ownerID,
		CronExpr:      "@daily",
		Timezone:      "UTC",
		JobSpec:       []byte(`{"name":"x","tasks":[{"name":"t","command":"echo","args":["hi"]}]}`),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	return sj
}

// TestArchiveUser_DisablesSchedules verifies archiving a user flips their
// enabled scheduled jobs to enabled = FALSE and bumps updated_at.
func TestArchiveUser_DisablesSchedules(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	sj := seedEnabledScheduledJob(t, q, alice.ID, "nightly")
	before := sj.UpdatedAt.Time

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	got, err := q.GetScheduledJob(t.Context(), sj.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled, "schedule should be disabled after archive")
	assert.True(t, got.UpdatedAt.Time.After(before), "updated_at should advance")
}

// TestArchiveUser_LeavesDisabledSchedulesUntouched guards the enabled = TRUE
// predicate: an already-disabled schedule's updated_at must NOT be bumped.
func TestArchiveUser_LeavesDisabledSchedulesUntouched(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	sj := seedEnabledScheduledJob(t, q, alice.ID, "paused")

	// Disable it directly first and capture updated_at.
	_, err := pool.Exec(t.Context(),
		`UPDATE scheduled_jobs SET enabled = FALSE, updated_at = NOW() WHERE id = $1`, sj.ID)
	require.NoError(t, err)
	pre, err := q.GetScheduledJob(t.Context(), sj.ID)
	require.NoError(t, err)

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	post, err := q.GetScheduledJob(t.Context(), sj.ID)
	require.NoError(t, err)
	assert.False(t, post.Enabled)
	assert.Equal(t, pre.UpdatedAt.Time, post.UpdatedAt.Time,
		"already-disabled schedule's updated_at must not be bumped")
}

// TestArchiveUser_DoesNotAffectOtherOwners verifies the WHERE owner_id = $1
// scoping: archiving alice does not disable bob's schedules.
func TestArchiveUser_DoesNotAffectOtherOwners(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	bob := seedUser(t, q, "bob@test.com", "Bob")
	_ = seedEnabledScheduledJob(t, q, alice.ID, "alice-job")
	bobJob := seedEnabledScheduledJob(t, q, bob.ID, "bob-job")

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	got, err := q.GetScheduledJob(t.Context(), bobJob.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled, "bob's schedule must stay enabled")
}

// TestUnarchiveUser_DoesNotReEnableSchedules locks in AUTONOMOUS DECISION 2:
// unarchiving does NOT resurrect schedules that archiving disabled.
func TestUnarchiveUser_DoesNotReEnableSchedules(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	adminToken := loginAsAdmin(t, srv, q, "admin@test.com", "adminpass")
	alice := seedUser(t, q, "alice@test.com", "Alice")
	sj := seedEnabledScheduledJob(t, q, alice.ID, "nightly")

	code, _ := archiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	code, _ = unarchiveUser(t, srv, adminToken, uuidStrTest(alice.ID))
	require.Equal(t, http.StatusOK, code)

	got, err := q.GetScheduledJob(t.Context(), sj.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled, "schedule must remain disabled after unarchive")
}
