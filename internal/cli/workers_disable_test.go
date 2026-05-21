// internal/cli/workers_disable_test.go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"relay/internal/relayclient"
)

func TestWorkersDisable_Drain(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000011"
	called := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/workers/"+workerID+"/disable", r.URL.Path)
		require.Equal(t, "", r.URL.Query().Get("requeue"), "drain mode sends no requeue param")
		called = true
		json.NewEncoder(w).Encode(map[string]any{"status": "disabled", "requeued_tasks": 0})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"disable", workerID}, &out)
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "disabled.\n", out.String())
}

func TestWorkersDisable_Requeue(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000012"
	called := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/workers/"+workerID+"/disable", r.URL.Path)
		require.Equal(t, "true", r.URL.Query().Get("requeue"))
		called = true
		json.NewEncoder(w).Encode(map[string]any{"status": "disabled", "requeued_tasks": 3})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"disable", "--requeue", workerID}, &out)
	require.NoError(t, err)
	require.True(t, called)
	require.Contains(t, out.String(), "3 task(s) requeued")
}

func TestWorkersEnable_ByHostname(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000013"
	enabled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workers":
			json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{
				Items: []workerResp{{ID: workerID, Hostname: "render-node-9", Status: "disabled"}},
				Total: 1,
			})
		case r.Method == "POST" && r.URL.Path == "/v1/workers/"+workerID+"/enable":
			enabled = true
			json.NewEncoder(w).Encode(map[string]any{"status": "online"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"enable", "render-node-9"}, &out)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Contains(t, out.String(), "enabled.")
}
