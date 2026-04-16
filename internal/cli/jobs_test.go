// internal/cli/jobs_test.go
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestListJobs_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/jobs", r.URL.Path)
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode([]jobResp{
			{ID: "job-1", Name: "render-a", Status: "done", CreatedAt: time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doListJobs(context.Background(), cfg, []string{}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "job-1")
	require.Contains(t, out.String(), "render-a")
	require.Contains(t, out.String(), "done")
}

func TestListJobs_StatusFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "running", r.URL.Query().Get("status"))
		json.NewEncoder(w).Encode([]jobResp{})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doListJobs(context.Background(), cfg, []string{"--status", "running"}, &out)
	require.NoError(t, err)
}

func TestGetJob_ShowsTasks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/jobs/job-1", r.URL.Path)
		json.NewEncoder(w).Encode(jobResp{
			ID:     "job-1",
			Name:   "render-a",
			Status: "running",
			Tasks: []taskResp{
				{ID: "task-1", Name: "frame-001", Status: "running"},
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doGetJob(context.Background(), cfg, []string{"job-1"}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "render-a")
	require.Contains(t, out.String(), "frame-001")
}

func TestCancelJob_PrintsID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/jobs/job-1", r.URL.Path)
		json.NewEncoder(w).Encode(jobResp{ID: "job-1", Status: "cancelled"})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doCancelJob(context.Background(), cfg, []string{"job-1"}, &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), "cancelled")
}

func TestListJobs_JSONFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]jobResp{{ID: "job-1", Name: "render-a", Status: "done"}})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doListJobs(context.Background(), cfg, []string{"--json"}, &out)
	require.NoError(t, err)
	// output should be valid JSON array
	var result []map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out.String())), &result))
}

// Verify flag parsing helper compiles (used in jobs.go).
var _ = flag.NewFlagSet
