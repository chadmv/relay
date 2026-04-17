// internal/cli/login_test.go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/auth/token", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-abc",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	cfg := &Config{ServerURL: srv.URL}
	// Simulate: user hits Enter to accept pre-filled URL, then types email.
	input := strings.NewReader("\nuser@example.com\n")
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.NoError(t, err)

	require.NotNil(t, saved)
	require.Equal(t, srv.URL, saved.ServerURL)
	require.Equal(t, "tok-abc", saved.Token)
	require.Contains(t, out.String(), "Logged in")
}

func TestRunLogin_EmptyEmailReturnsError(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\n\n") // blank URL, blank email
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "email is required")
}

func TestRunLogin_InviteRequired_PromptsAndRetries(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		callCount++
		if callCount == 1 {
			// First call: no invite token — server rejects.
			require.Equal(t, "", body["invite_token"])
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "invite required"})
			return
		}
		// Second call: invite token present.
		require.Equal(t, "my-invite-token", body["invite_token"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-new",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	cfg := &Config{ServerURL: srv.URL}
	// hits Enter to accept pre-filled URL, types email, types invite token
	input := strings.NewReader("\nnewuser@example.com\nmy-invite-token\n")
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.NoError(t, err)
	require.Equal(t, 2, callCount)
	require.NotNil(t, saved)
	require.Equal(t, "tok-new", saved.Token)
	require.Contains(t, out.String(), "Logged in")
	require.Contains(t, out.String(), "Invite token:")
}

func TestRunLogin_InviteRequired_BlankToken_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "invite required"})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL}
	input := strings.NewReader("\nnewuser@example.com\n\n") // blank invite token
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invite token required")
}
