package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminUsersList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/users", r.URL.Path)
		require.Equal(t, "", r.URL.RawQuery)
		require.Equal(t, "Bearer admintoken", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":         "11111111-1111-1111-1111-111111111111",
				"email":      "admin@test.com",
				"name":       "Admin",
				"is_admin":   true,
				"created_at": "2026-04-01T12:00:00Z",
			},
			{
				"id":         "22222222-2222-2222-2222-222222222222",
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "list"}, &out)
	require.NoError(t, err)

	got := out.String()
	require.Contains(t, got, "admin@test.com")
	require.Contains(t, got, "alice@test.com")
	require.Contains(t, got, "Admin")
	require.Contains(t, got, "Alice")
	require.Contains(t, got, "yes") // is_admin=true
	require.Contains(t, got, "no")  // is_admin=false
}

func TestAdminUsersList_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "list"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestAdminUsersGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/users", r.URL.Path)
		require.Equal(t, "email=alice%40test.com", r.URL.RawQuery)
		require.Equal(t, "Bearer admintoken", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":         "22222222-2222-2222-2222-222222222222",
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get", "alice@test.com"}, &out)
	require.NoError(t, err)

	got := out.String()
	require.Contains(t, got, "ID:")
	require.Contains(t, got, "22222222-2222-2222-2222-222222222222")
	require.Contains(t, got, "Email:")
	require.Contains(t, got, "alice@test.com")
	require.Contains(t, got, "Name:")
	require.Contains(t, got, "Alice")
	require.Contains(t, got, "Admin:")
	require.Contains(t, got, "no")
	require.Contains(t, got, "Created:")
}

func TestAdminUsersGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get", "nobody@test.com"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "user not found: nobody@test.com")
}

func TestAdminUsersGet_MissingEmail(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage:")
}

func TestAdminUsersGet_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "get", "alice@test.com"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}
