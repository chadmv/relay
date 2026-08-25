package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

func TestWorkersDelete_ByIDWithYes(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000011"
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/workers/"+workerID, r.URL.Path)
		called = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": workerID, "hostname": "render-07",
			"requeued_tasks": 2, "reservations_updated": 1, "enrollments_unlinked": 1,
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	require.NoError(t, doWorkers(context.Background(), cfg, []string{"delete", "--yes", workerID}, &out))
	require.True(t, called)
	got := out.String()
	require.Contains(t, got, "deleted")
	require.Contains(t, got, "2 task(s) requeued")
	require.Contains(t, got, "1 reservation(s) updated")
	require.Contains(t, got, "1 enrollment(s) unlinked")
}

// TestWorkersDelete_RequiresConfirmation (T-F1). VACUITY: assert NO REQUEST WAS
// MADE, not just the exit code - an implementation that deletes and then errors
// would pass an exit-code-only check.
func TestWorkersDelete_RequiresConfirmation(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000012"
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"delete", workerID}, &out)
	require.Error(t, err, "without --yes the command must exit non-zero")
	require.Contains(t, err.Error(), "--yes")
	require.Empty(t, requests, "no request may be issued without --yes")
	require.Contains(t, out.String(), workerID, "it must print what it WOULD delete")
}

// TestWorkersDelete_ResolvesARevokedHostname (T-F2). VACUITY: a fixture serving
// the worker from /v1/workers too passes WITHOUT the fallback, so the worker must
// be ABSENT from the primary list.
func TestWorkersDelete_ResolvesARevokedHostname(t *testing.T) {
	const workerID = "00000000-0000-0000-0000-000000000013"
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workers":
			// Empty on purpose: GET /v1/workers excludes revoked rows.
			_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{Items: []workerResp{}, Total: 0})
		case r.Method == "GET" && r.URL.Path == "/v1/workers/revoked":
			_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[workerResp]{
				Items: []workerResp{{ID: workerID, Hostname: "render-07", Status: "revoked"}}, Total: 1,
			})
		case r.Method == "DELETE" && r.URL.Path == "/v1/workers/"+workerID:
			deleted = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": workerID})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-tok"}
	var out strings.Builder
	require.NoError(t, doWorkers(context.Background(), cfg, []string{"delete", "--yes", "render-07"}, &out))
	require.True(t, deleted, "the DELETE must reach the revoked worker's id")
}
