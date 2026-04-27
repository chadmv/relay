package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogout_Current_CallsDeleteAndClearsToken(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/auth/token", r.URL.Path)
		require.Equal(t, "Bearer mytoken", r.Header.Get("Authorization"))
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var saved *Config
	orig := saveConfigFn
	saveConfigFn = func(c *Config) error { saved = c; return nil }
	t.Cleanup(func() { saveConfigFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "mytoken"}
	var out strings.Builder
	err := doLogout(context.Background(), cfg, []string{}, &out)
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, saved)
	require.Empty(t, saved.Token, "token should be cleared from config")
	require.Equal(t, srv.URL, saved.ServerURL, "server URL preserved")
	require.Contains(t, out.String(), "Logged out")
}

func TestLogout_All_CallsDeleteAllAndClearsToken(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/auth/tokens", r.URL.Path)
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var saved *Config
	orig := saveConfigFn
	saveConfigFn = func(c *Config) error { saved = c; return nil }
	t.Cleanup(func() { saveConfigFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "mytoken"}
	var out strings.Builder
	err := doLogout(context.Background(), cfg, []string{"--all"}, &out)
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, saved)
	require.Empty(t, saved.Token)
	require.Contains(t, out.String(), "Logged out of all sessions")
}

func TestLogout_NotLoggedIn_NoOp(t *testing.T) {
	cfg := &Config{}
	var out strings.Builder
	err := doLogout(context.Background(), cfg, []string{}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "not logged in")
}
