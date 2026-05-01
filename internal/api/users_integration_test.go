//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
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
	time.Sleep(10 * time.Millisecond)
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
