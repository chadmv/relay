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
