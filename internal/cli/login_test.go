// internal/cli/login_test.go
package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// withMockPassword sets readPasswordFn to return a fixed password for the test.
// Use a custom override when multiple calls need different return values.
func withMockPassword(t *testing.T, password string) {
	t.Helper()
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return password, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })
}

func TestRunLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/auth/login", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		require.Equal(t, "mypassword1", body["password"])
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

	withMockPassword(t, "mypassword1")

	cfg := &Config{ServerURL: srv.URL}
	input := strings.NewReader("\nuser@example.com\n") // accept URL, type email
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.NoError(t, err)

	require.NotNil(t, saved)
	require.Equal(t, srv.URL, saved.ServerURL)
	require.Equal(t, "tok-abc", saved.Token)
	require.Contains(t, out.String(), "Logged in")
}

func TestRunLogin_EmptyEmailReturnsError(t *testing.T) {
	withMockPassword(t, "mypassword1")
	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\n\n") // blank URL, blank email
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "email is required")
}

func TestRunLogin_EmptyPasswordReturnsError(t *testing.T) {
	withMockPassword(t, "")
	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\nuser@example.com\n")
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "password is required")
}

func TestRunLogin_ServerReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid email or password"})
	}))
	defer srv.Close()

	withMockPassword(t, "wrongpassword")
	cfg := &Config{ServerURL: srv.URL}
	input := strings.NewReader("\nuser@example.com\n")
	var out strings.Builder
	err := doLogin(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid email or password")
}
