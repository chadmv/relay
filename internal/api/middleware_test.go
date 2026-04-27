//go:build integration

package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/api"
	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBearerAuth_MissingToken(t *testing.T) {
	q := newTestQueries(t)
	handler := api.BearerAuth(q)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestBearerAuth_ValidToken(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "Alice", Email: "alice@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	raw := "test-token-12345"
	hash := tokenhash.Hash(raw)

	_, err = q.CreateToken(ctx, store.CreateTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{}, // no expiry
	})
	require.NoError(t, err)

	var gotUser api.AuthUser
	handler := api.BearerAuth(q)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := api.UserFromCtx(r.Context())
		require.True(t, ok)
		gotUser = u
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "alice@test.com", gotUser.Email)
	assert.False(t, gotUser.IsAdmin)
	assert.True(t, gotUser.TokenID.Valid, "TokenID should be populated")
}

func TestAdminOnly_NonAdmin(t *testing.T) {
	handler := api.AdminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// No user in context
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
