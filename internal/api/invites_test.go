//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateInvite_NonAdmin_Forbidden(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "Alice", Email: "alice@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	token := createTestToken(t, q, user.ID)

	srv := api.New(pool, q, nil, nil, nil)

	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateInvite_Admin_ReturnsToken(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "Admin", Email: "admin@test.com", IsAdmin: true, PasswordHash: "x",
	})
	require.NoError(t, err)
	token := createTestToken(t, q, admin.ID)

	srv := api.New(pool, q, nil, nil, nil)

	body := `{"expires_in": "24h"}`
	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])
	assert.Len(t, resp["token"], 64, "token should be 64-char hex string")
	assert.NotEmpty(t, resp["id"])
	assert.NotEmpty(t, resp["expires_at"])
	assert.Nil(t, resp["email"]) // no email bound
}

func TestCreateInvite_InvalidExpiry_BadRequest(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "Admin", Email: "admin2@test.com", IsAdmin: true, PasswordHash: "x",
	})
	require.NoError(t, err)
	token := createTestToken(t, q, admin.ID)

	srv := api.New(pool, q, nil, nil, nil)

	body := `{"expires_in":"notaduration"}`
	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateInvite_Admin_EmailBound(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	ctx := t.Context()

	admin, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "Admin", Email: "admin@test.com", IsAdmin: true, PasswordHash: "x",
	})
	require.NoError(t, err)
	token := createTestToken(t, q, admin.ID)

	srv := api.New(pool, q, nil, nil, nil)

	body := `{"email": "newuser@test.com", "expires_in": "24h"}`
	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])
	assert.Equal(t, "newuser@test.com", resp["email"])
}
