package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"relay/internal/relayclient"
)

// captureStdStreams runs fn with os.Stdout and os.Stderr replaced by pipes,
// returning what was written to each. It exercises the real command wiring
// (AdminCommand/ProfileCommand) rather than calling doAdmin/doProfile with an
// injected writer, so it proves where data actually lands.
func captureStdStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout, os.Stderr = outW, errW
	t.Cleanup(func() { os.Stdout, os.Stderr = origOut, origErr })

	outCh := make(chan string, 1)
	errCh := make(chan string, 1)
	go func() { b, _ := io.ReadAll(outR); outCh <- string(b) }()
	go func() { b, _ := io.ReadAll(errR); errCh <- string(b) }()

	fn()

	_ = outW.Close()
	_ = errW.Close()
	stdout = <-outCh
	stderr = <-errCh
	return stdout, stderr
}

func TestAdminCommand_UsersList_DataOnStdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items: []map[string]any{{
				"id":         "22222222-2222-2222-2222-222222222222",
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			}},
			Total: 1,
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	cmd := AdminCommand()

	var runErr error
	stdout, _ := captureStdStreams(t, func() {
		runErr = cmd.Run(context.Background(), []string{"users", "list"}, cfg)
	})
	require.NoError(t, runErr)
	require.Contains(t, stdout, "alice@test.com", "user table must go to stdout")
	require.Contains(t, stdout, "Total:", "list summary must go to stdout")
}

func TestAdminCommand_UsersGet_DataOnStdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items: []map[string]any{{
				"id":         "22222222-2222-2222-2222-222222222222",
				"email":      "alice@test.com",
				"name":       "Alice",
				"is_admin":   false,
				"created_at": "2026-04-02T12:00:00Z",
			}},
			Total: 1,
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admintoken"}
	cmd := AdminCommand()

	var runErr error
	stdout, _ := captureStdStreams(t, func() {
		runErr = cmd.Run(context.Background(), []string{"users", "get", "alice@test.com"}, cfg)
	})
	require.NoError(t, runErr)
	require.Contains(t, stdout, "alice@test.com", "user detail must go to stdout")
	require.Contains(t, stdout, "Email:", "user detail must go to stdout")
}

func TestProfileCommand_Update_DataOnStdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	cmd := ProfileCommand()

	var runErr error
	stdout, _ := captureStdStreams(t, func() {
		runErr = cmd.Run(context.Background(), []string{"update", "--name", "New Name"}, cfg)
	})
	require.NoError(t, runErr)
	require.Contains(t, stdout, "New Name", "profile result must go to stdout")
	require.Contains(t, stdout, "Email:", "profile result must go to stdout")
}
