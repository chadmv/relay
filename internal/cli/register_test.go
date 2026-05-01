// internal/cli/register_test.go
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

func TestRunRegister_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/auth/register", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		require.Equal(t, "Alice", body["name"])
		require.Equal(t, "myinvite123", body["invite_token"])
		require.Equal(t, "mypassword1", body["password"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-reg",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	// readPasswordFn will be called twice: password + confirm.
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		callCount++
		return "mypassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL}
	// URL (accept), email, name, invite token
	input := strings.NewReader("\nuser@example.com\nAlice\nmyinvite123\n")
	var out strings.Builder
	err := doRegister(context.Background(), cfg, input, &out)
	require.NoError(t, err)

	require.Equal(t, 2, callCount, "expected two password prompts (password + confirm)")
	require.NotNil(t, saved)
	require.Equal(t, "tok-reg", saved.Token)
	require.Contains(t, out.String(), "Registered and logged in")
}

func TestRunRegister_PasswordMismatch(t *testing.T) {
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return "password1234", nil
		}
		return "different1234", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost"}
	input := strings.NewReader("\nuser@example.com\n\nmyinvite\n")
	var out strings.Builder
	err := doRegister(context.Background(), cfg, input, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}

func TestRunRegister_NoInviteToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/auth/register", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "user@example.com", body["email"])
		require.Equal(t, "mypassword1", body["password"])
		// invite_token is either absent or empty — both are acceptable.
		require.Empty(t, body["invite_token"])
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-self",
			"expires_at": time.Now().Add(30 * 24 * time.Hour),
		})
	}))
	defer srv.Close()

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error { saved = cfg; return nil }
	t.Cleanup(func() { saveConfigFn = origSave })

	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return "mypassword1", nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL}
	// URL (accept), email, name (blank), invite token (blank)
	input := strings.NewReader("\nuser@example.com\n\n\n")
	var out strings.Builder
	err := doRegister(context.Background(), cfg, input, &out)
	require.NoError(t, err)

	require.NotNil(t, saved)
	require.Equal(t, "tok-self", saved.Token)
}
