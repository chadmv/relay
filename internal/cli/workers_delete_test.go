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
