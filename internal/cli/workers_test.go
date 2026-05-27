// internal/cli/workers_test.go
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

func TestWorkersList_SortFlag(t *testing.T) {
	var capturedRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{Items: []workerResp{}, Total: 0})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"list", "--sort", "-priority"}, &out)
	require.NoError(t, err)
	require.Contains(t, capturedRawQuery, "sort=-priority")
}

func TestWorkersListGet_Dispatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/workers":
			json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{
				Items: []workerResp{
					{ID: "w-1", Name: "render-node-1", Status: "online", CpuCores: 32, RamGb: 128, GpuCount: 2, GpuModel: "RTX 4090"},
				},
				Total: 1,
			})
		case "/v1/workers/w-1":
			json.NewEncoder(w).Encode(workerResp{
				ID: "w-1", Name: "render-node-1", Hostname: "node1.local",
				Status: "online", CpuCores: 32, RamGb: 128, GpuCount: 2, GpuModel: "RTX 4090",
			})
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}

	t.Run("list", func(t *testing.T) {
		var out strings.Builder
		err := doWorkers(context.Background(), cfg, []string{"list"}, &out)
		require.NoError(t, err)
		require.Contains(t, out.String(), "render-node-1")
		require.Contains(t, out.String(), "online")
	})

	t.Run("get", func(t *testing.T) {
		var out strings.Builder
		err := doWorkers(context.Background(), cfg, []string{"get", "w-1"}, &out)
		require.NoError(t, err)
		require.Contains(t, out.String(), "node1.local")
		require.Contains(t, out.String(), "RTX 4090")
	})

	t.Run("list --json", func(t *testing.T) {
		var out strings.Builder
		err := doWorkers(context.Background(), cfg, []string{"list", "--json"}, &out)
		require.NoError(t, err)
		var result []map[string]any
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out.String())), &result))
	})
}
