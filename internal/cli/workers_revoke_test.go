// internal/cli/workers_revoke_test.go
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

func TestWorkersRevoke_ByID(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000001"
	deleted := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/workers/"+workerID+"/token", r.URL.Path)
		deleted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"revoke", workerID}, &out)
	require.NoError(t, err)
	require.True(t, deleted)
	require.Contains(t, out.String(), "revoked.")
}

func TestWorkersRevoke_ByHostname(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000002"
	deleted := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workers":
			json.NewEncoder(w).Encode([]workerResp{
				{ID: workerID, Hostname: "render-node-42", Status: "online"},
			})
		case r.Method == "DELETE" && r.URL.Path == "/v1/workers/"+workerID+"/token":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"revoke", "render-node-42"}, &out)
	require.NoError(t, err)
	require.True(t, deleted)
	require.Contains(t, out.String(), "revoked.")
}

func TestWorkersRevoke_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/workers", r.URL.Path)
		json.NewEncoder(w).Encode([]workerResp{
			{ID: "00000000-0000-0000-0000-000000000003", Hostname: "other-node", Status: "online"},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"revoke", "missing-host"}, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no worker found with hostname")
	require.Contains(t, err.Error(), "missing-host")
}
