// internal/cli/jobs_test.go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"relay/internal/relayclient"
)

func TestListJobs_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/jobs", r.URL.Path)
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(relayclient.PageEnvelope[jobResp]{
			Items: []jobResp{
				{ID: "job-1", Name: "render-a", Status: "done", CreatedAt: time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)},
			},
			Total: 1,
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
	require.Contains(t, out.String(), "Total:")
}

func TestListJobs_StatusFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "running", r.URL.Query().Get("status"))
		json.NewEncoder(w).Encode(relayclient.PageEnvelope[jobResp]{Items: []jobResp{}, Total: 0})
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
		json.NewEncoder(w).Encode(relayclient.PageEnvelope[jobResp]{
			Items: []jobResp{{ID: "job-1", Name: "render-a", Status: "done"}},
			Total: 1,
		})
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

func TestListJobs_SortFlag(t *testing.T) {
	var capturedRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(relayclient.PageEnvelope[jobResp]{Items: []jobResp{}, Total: 0})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doListJobs(context.Background(), cfg, []string{"--sort", "-priority"}, &out)
	require.NoError(t, err)
	require.Contains(t, capturedRawQuery, "sort=-priority")
}

// Verify flag parsing helper compiles (used in jobs.go).
var _ = flag.NewFlagSet

func TestSubmitJob_DetachPrintsID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/jobs", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(jobResp{ID: "new-job-id", Status: "pending"})
	}))
	defer srv.Close()

	// Write a minimal job JSON to a temp file.
	f, err := os.CreateTemp("", "job*.json")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	json.NewEncoder(f).Encode(map[string]any{
		"name":  "test",
		"tasks": []map[string]any{{"name": "t1", "command": []string{"echo", "hi"}}},
	})
	f.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out, errOut strings.Builder
	err = doSubmit(context.Background(), cfg, []string{"--detach", f.Name()}, &out, &errOut)
	require.NoError(t, err)
	require.Contains(t, out.String(), "new-job-id")
}

// ─── relay submit, non-detach ────────────────────────────────────────────────
//
// Everything below covers doSubmit's WAITING path. Until this file carried it,
// that path had no test at all: the only doSubmit call in the package passed
// --detach and returned before watchJobLogs was ever reached, so replacing the
// whole completeness branch with a discard left the package green.

// writeJobFile writes a minimal one-task job spec and returns its path.
func writeJobFile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "job*.json")
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(f).Encode(map[string]any{
		"name":  "test",
		"tasks": []map[string]any{{"name": "frame-001", "command": []string{"echo", "hi"}}},
	}))
	require.NoError(t, f.Close())
	return f.Name()
}

// fakeSubmitServer accepts the POST and then answers as a finished job with one
// finished task. logsFail routes the task-log request to a 500.
//
// There is no /v1/events case on purpose: an unmatched request returns 200 with
// an empty body, which StreamEvents treats as a subscription that immediately
// ends, so onSubscribed sees the terminal job and prints. fakeCompletedJobServer
// in logs_test.go relies on the same thing.
func fakeSubmitServer(t *testing.T, jobID, taskID string, logsFail bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/jobs":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(jobResp{ID: jobID, Status: "pending"})

		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			if logsFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSubmitJob_LogFetchFails_ReportsIncomplete(t *testing.T) {
	jobID, taskID := "job-submit-500", "task-submit-500"
	srv := fakeSubmitServer(t, jobID, taskID, true)

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	err := doSubmit(ctx, cfg, []string{writeJobFile(t)}, &out, &errOut)
	require.Error(t, err)
	var se silentError
	require.False(t, errors.As(err, &se),
		"an incomplete log must not be silent - Dispatch has to print it")
	require.Contains(t, err.Error(), "logs incomplete")

	// And the per-task diagnostic went to errOut, not stdout. stdout carries the
	// job id and nothing else on this path.
	require.Contains(t, errOut.String(), "frame-001")
	require.Contains(t, errOut.String(), taskID)
	require.Contains(t, errOut.String(), "incomplete")
	require.Equal(t, jobID+"\n", out.String())
}

// The two io.Writer arguments are same-typed and adjacent, so transposing them
// is invisible to the compiler and to every assertion that only checks a
// combined output. The real mutant: `relay submit job.json > run.log` captures
// an empty file while the logs flood the terminal.
//
// Assert POSITIVELY on one stream and EMPTY on the other, in both directions, so
// a transposition has nowhere to hide.
func TestSubmitJob_LogsGoToStdout_DiagnosticsToStderr(t *testing.T) {
	jobID, taskID := "job-submit-split", "task-submit-split"
	srv := fakeSubmitServer(t, jobID, taskID, false)

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	err := doSubmit(ctx, cfg, []string{writeJobFile(t)}, &out, &errOut)
	require.NoError(t, err)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered",
		"log lines belong on stdout so they can be redirected to a file")
	require.Empty(t, errOut.String(),
		"a fully successful run writes nothing to stderr")
}
