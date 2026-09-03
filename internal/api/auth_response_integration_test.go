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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postAuthJSON(t *testing.T, srv interface {
	Handler() http.Handler
}, path string, body any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var m map[string]any
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	}
	return rec.Code, m
}

// assertAuthBody pins the CLOSED KEY SET of an auth response against
// HAND-WRITTEN names, decoded from real marshalled JSON. A fixture built from
// the handler's own response type would agree with itself by construction, and
// an absent omitempty field would compare equal whatever its name.
func assertAuthBody(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	assert.Len(t, m, 3, "the body must carry exactly token, expires_at and user; got %#v", m)
	for _, k := range []string{"token", "expires_at", "user"} {
		_, present := m[k]
		require.True(t, present, "key %q is missing", k)
	}

	// expires_at is unchanged on the wire: still one RFC3339 string, as it was
	// when the three handlers wrote a map[string]any.
	at, ok := m["expires_at"].(string)
	require.True(t, ok, "expires_at must be a JSON string, got %T", m["expires_at"])
	_, err := time.Parse(time.RFC3339, at)
	require.NoError(t, err, "expires_at must be RFC3339, got %q", at)

	user, ok := m["user"].(map[string]any)
	require.True(t, ok, "user must be a JSON object, got %T", m["user"])

	want := []string{"id", "email", "name", "is_admin", "created_at", "archived_at"}
	for _, k := range want {
		_, present := user[k]
		require.True(t, present, "user key %q is missing", k)
	}
	assert.Len(t, user, len(want), "user must carry exactly the /v1/users/me keys; got %#v", user)
	assert.Nil(t, user["archived_at"], "archived_at is always null on these endpoints")

	// No password material anywhere in the body, at either level.
	for _, level := range []map[string]any{m, user} {
		for k := range level {
			assert.NotContains(t, strings.ToLower(k), "password",
				"no key resembling a password may appear in an auth response")
			assert.NotContains(t, strings.ToLower(k), "hash",
				"no key resembling a hash may appear in an auth response")
		}
	}
	return user
}

// The user object must be the GET /v1/users/me body, asserted against that
// endpoint's own live response so the two cannot drift.
func TestAuthLogin_UserMatchesUsersMe(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	const email = "authuser@test.com"
	_ = createTestUser(t, q, "Auth User", email, false)

	// createTestUser stores a hash of this plaintext.
	code, m := postAuthJSON(t, srv, "/v1/auth/login", map[string]string{
		"email":    email,
		"password": "testpassword1",
	})
	require.Equal(t, http.StatusCreated, code)
	user := assertAuthBody(t, m)
	assert.Equal(t, email, user["email"])
	assert.Equal(t, false, user["is_admin"])

	token, _ := m["token"].(string)
	require.NotEmpty(t, token)

	req := httptest.NewRequest("GET", "/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var me map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))

	assert.Equal(t, me, user,
		"the login body's user must be exactly the GET /v1/users/me body")
}

// Both register arms carry the same body: the invite-redemption path and the
// self-serve path.
func TestAuthRegister_BothArmsCarryTheUser(t *testing.T) {
	t.Run("invite redemption", func(t *testing.T) {
		pool := newTestPool(t)
		q := store.New(pool)
		srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
		admin := createTestUser(t, q, "Admin", "reginv-admin@test.com", true)
		inviteToken := createTestInvite(t, q, admin.ID, nil, 72*time.Hour)

		code, m := postAuthJSON(t, srv, "/v1/auth/register", map[string]string{
			"email":        "invitee@reginv.test",
			"name":         "Invitee",
			"password":     "invitepassword1",
			"invite_token": inviteToken,
		})
		require.Equal(t, http.StatusCreated, code)
		user := assertAuthBody(t, m)
		assert.Equal(t, "invitee@reginv.test", user["email"])
		assert.Equal(t, false, user["is_admin"])
	})

	t.Run("self serve", func(t *testing.T) {
		pool := newTestPool(t)
		q := store.New(pool)
		srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
		srv.AllowSelfRegister = true

		code, m := postAuthJSON(t, srv, "/v1/auth/register", map[string]string{
			"email":    "selfserve@authbody.test",
			"name":     "Self Serve",
			"password": "selfpassword1",
		})
		require.Equal(t, http.StatusCreated, code)
		user := assertAuthBody(t, m)
		assert.Equal(t, "selfserve@authbody.test", user["email"])
		assert.Equal(t, false, user["is_admin"])
	})
}

// A refused login carries no user object, so the addition sits after every
// existing refusal and the email-enumeration behaviour is unchanged: an unknown
// address and a wrong password produce the same body.
func TestAuthLogin_RefusalsCarryNoUser(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	_ = createTestUser(t, q, "Known", "known@refusal.test", false)

	unknownCode, unknown := postAuthJSON(t, srv, "/v1/auth/login", map[string]string{
		"email":    "nobody@refusal.test",
		"password": "testpassword1",
	})
	wrongCode, wrong := postAuthJSON(t, srv, "/v1/auth/login", map[string]string{
		"email":    "known@refusal.test",
		"password": "not-the-password",
	})

	require.Equal(t, http.StatusUnauthorized, unknownCode)
	require.Equal(t, http.StatusUnauthorized, wrongCode)
	assert.NotContains(t, unknown, "user")
	assert.NotContains(t, wrong, "user")
	assert.Equal(t, unknown, wrong,
		"an unknown address and a wrong password must be indistinguishable")
}
