package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWhoami_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/users/me", r.URL.Path)
		require.Equal(t, "Bearer t", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "abc", "email": "a@b.com", "name": "A B", "is_admin": true,
		})
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	out, terr := s.callWhoami(context.Background())
	require.Nil(t, terr)
	require.Equal(t, "abc", out["user_id"])
	require.Equal(t, "a@b.com", out["email"])
	require.Equal(t, true, out["is_admin"])
	require.Equal(t, srv.URL, out["server_url"])
}

func TestWhoami_AuthExpired(t *testing.T) {
	// The first /v1/users/me (NewServer's startup identity probe) must succeed so
	// construction does not fail; the second (the explicit callWhoami under test)
	// returns 401 so we can assert the auth_expired mapping.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "abc", "email": "a@b.com", "name": "A B", "is_admin": false,
			})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	_, terr := s.callWhoami(context.Background())
	require.NotNil(t, terr)
	require.Equal(t, "auth_expired", terr.Code)
}
