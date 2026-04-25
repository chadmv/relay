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

func TestDoWorkersWorkspaces_Lists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/workers/00000000-0000-0000-0000-000000000001/workspaces", r.URL.Path)
		require.Equal(t, "Bearer testtok", r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"source_type":   "perforce",
				"source_key":    "//streams/main",
				"short_id":      "abc123",
				"baseline_hash": "deadbeef",
				"last_used_at":  "2026-04-24T00:00:00Z",
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "testtok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"workspaces", "00000000-0000-0000-0000-000000000001"}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "//streams/main")
	require.Contains(t, out.String(), "abc123")
	require.Contains(t, out.String(), "deadbeef")
}

func TestDoWorkersWorkspaces_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"source_type":   "perforce",
				"source_key":    "//streams/main",
				"short_id":      "abc123",
				"baseline_hash": "deadbeef",
				"last_used_at":  "2026-04-24T00:00:00Z",
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "testtok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"workspaces", "--json", "00000000-0000-0000-0000-000000000001"}, &out)
	require.NoError(t, err)

	var result []map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out.String())), &result))
	require.Len(t, result, 1)
	require.Equal(t, "abc123", result[0]["short_id"])
	require.Equal(t, "//streams/main", result[0]["source_key"])
	require.Equal(t, "deadbeef", result[0]["baseline_hash"])
}

func TestDoWorkersEvictWorkspace_Posts(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/workers/00000000-0000-0000-0000-000000000001/workspaces/abc123/evict", r.URL.Path)
		require.Equal(t, "Bearer testtok", r.Header.Get("Authorization"))
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "testtok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"evict-workspace", "00000000-0000-0000-0000-000000000001", "abc123"}, &out)
	require.NoError(t, err)
	require.True(t, called)
	require.Contains(t, out.String(), "evict")
	require.Contains(t, out.String(), "abc123")
}

func TestDoWorkersWorkspaces_MissingArg(t *testing.T) {
	cfg := &Config{ServerURL: "http://unused", Token: "tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"workspaces"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage")
}

func TestDoWorkersEvictWorkspace_MissingArgs(t *testing.T) {
	cfg := &Config{ServerURL: "http://unused", Token: "tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"evict-workspace", "some-id"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "usage")
}
