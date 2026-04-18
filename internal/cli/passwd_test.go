// internal/cli/passwd_test.go
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

func TestRunPasswd_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PUT", r.Method)
		require.Equal(t, "/v1/users/me/password", r.URL.Path)
		require.Equal(t, "Bearer mytoken", r.Header.Get("Authorization"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "oldpassword", body["current_password"])
		require.Equal(t, "newpassword1", body["new_password"])
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	passwords := []string{"oldpassword", "newpassword1", "newpassword1"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "mytoken"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "Password changed")
}

func TestRunPasswd_NotLoggedIn(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}

func TestRunPasswd_PasswordMismatch(t *testing.T) {
	passwords := []string{"oldpassword", "newpassword1", "different1234"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: "http://localhost", Token: "mytoken"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "passwords do not match")
}

func TestRunPasswd_ServerReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "current password is incorrect"})
	}))
	defer srv.Close()

	passwords := []string{"wrongpassword", "newpassword1", "newpassword1"}
	callCount := 0
	orig := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		p := passwords[callCount]
		callCount++
		return p, nil
	}
	t.Cleanup(func() { readPasswordFn = orig })

	cfg := &Config{ServerURL: srv.URL, Token: "mytoken"}
	var out strings.Builder
	err := doPasswd(context.Background(), cfg, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "current password is incorrect")
}
