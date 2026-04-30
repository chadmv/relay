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

func TestProfileUpdate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/users/me", r.URL.Path)
		require.Equal(t, "Bearer usertoken", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "New Name", body["name"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "22222222-2222-2222-2222-222222222222",
			"email":      "user@test.com",
			"name":       "New Name",
			"is_admin":   false,
			"created_at": "2026-04-02T12:00:00Z",
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "New Name"}, &out)
	require.NoError(t, err)

	got := out.String()
	require.Contains(t, got, "ID:")
	require.Contains(t, got, "22222222-2222-2222-2222-222222222222")
	require.Contains(t, got, "Email:")
	require.Contains(t, got, "user@test.com")
	require.Contains(t, got, "Name:")
	require.Contains(t, got, "New Name")
}

func TestProfileUpdate_EmptyName(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", ""}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestProfileUpdate_WhitespaceOnlyName(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "   "}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestProfileUpdate_MissingNameFlag(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--name is required")
}

func TestProfileUpdate_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestProfileUpdate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "name is required"})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"update", "--name", "x"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "name is required")
}

func TestProfile_UnknownSubcommand(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, []string{"frobnicate"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown profile subcommand")
}

func TestProfile_NoArgs(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "usertoken"}
	var out strings.Builder
	err := doProfile(context.Background(), cfg, nil, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage: relay profile update")
}
