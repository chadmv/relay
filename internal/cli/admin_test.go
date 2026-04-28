package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminPasswd_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/users/password-reset", r.URL.Path)
		require.Equal(t, "Bearer admintoken", r.Header.Get("Authorization"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "alice@test.com", body["email"])
		require.Equal(t, "newpassword1", body["new_password"])
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	passwords := []string{"newpassword1", "newpassword1"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"passwd", "alice@test.com"}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "Password reset for alice@test.com")
}

func TestAdminPasswd_PasswordMismatch(t *testing.T) {
	passwords := []string{"newpassword1", "different1234"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"passwd", "alice@test.com"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}

func TestAdminPasswd_MissingEmail(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"passwd"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage:")
}

func TestAdminPasswd_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"passwd", "alice@test.com"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestAdmin_UnknownSubcommand(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"flarp"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown admin subcommand")
}

func TestAdmin_UsersUnknownSubcommand(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost", Token: "admintoken"}
	var out strings.Builder
	err := doAdmin(context.Background(), cfg, []string{"users", "wat"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown admin users subcommand")
}
