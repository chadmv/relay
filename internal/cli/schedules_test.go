package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchedulesList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		require.Equal(t, "Bearer tkn", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":"abc","name":"n","cron_expr":"@hourly","timezone":"UTC","enabled":true,"next_run_at":"2026-04-22T00:00:00Z"}]`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"list"}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "abc")
	require.Contains(t, buf.String(), "n")
}

func TestSchedulesCreate_Success(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"abc","name":"nightly","cron_expr":"@hourly"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	spec := `{"name":"r","tasks":[{"name":"t","command":["echo","hi"]}]}`
	require.NoError(t, os.WriteFile(specPath, []byte(spec), 0600))

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg,
		[]string{"create", "--name", "nightly", "--cron", "@hourly", "--spec", specPath},
		&buf)
	require.NoError(t, err)
	require.Equal(t, "nightly", receivedBody["name"])
	require.Equal(t, "@hourly", receivedBody["cron_expr"])
	require.Contains(t, buf.String(), "abc")
}

func TestSchedulesDelete_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"delete", "abc"}, &buf)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(buf.String()), "deleted")
}

func TestSchedulesRunNow_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc/run-now", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"jobxyz","name":"r","status":"pending"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"run-now", "abc"}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "jobxyz")
}

func TestSchedulesUnknownSubcommand(t *testing.T) {
	cfg := &Config{ServerURL: "http://x", Token: "t"}
	err := doSchedules(context.Background(), cfg, []string{"bogus"}, io.Discard)
	require.Error(t, err)
}
