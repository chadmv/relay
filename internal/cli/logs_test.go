// internal/cli/logs_test.go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"relay/internal/relayclient"
)

// fakeJobServer serves:
//
//	GET /v1/jobs/<id>           → running job with one pending task
//	GET /v1/events?job_id=<id>  → SSE stream ending with finalJobStatus
//	GET /v1/tasks/<id>/logs     → log entries
func fakeJobServer(t *testing.T, jobID, taskID, finalJobStatus string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "running",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "pending"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// task finishes
			fmt.Fprintf(w, "event: task\ndata: {\"id\":%q,\"status\":\"done\"}\n\n", taskID)
			// job finishes
			fmt.Fprintf(w, "event: job\ndata: {\"status\":%q}\n\n", finalJobStatus)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			json.NewEncoder(w).Encode([]struct {
				Stream  string `json:"stream"`
				Content string `json:"content"`
			}{
				{Stream: "stdout", Content: "frame rendered"},
			})
		}
	}))
}

// fakeCompletedJobServer serves a job already in a terminal state with no SSE.
func fakeCompletedJobServer(t *testing.T, jobID, taskID, jobStatus string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: jobStatus,
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			json.NewEncoder(w).Encode([]struct {
				Stream  string `json:"stream"`
				Content string `json:"content"`
			}{
				{Stream: "stdout", Content: "frame rendered"},
			})
		}
	}))
}

// fakeRaceJobServer models the terminal-before-subscribe race: the job reads
// "running" until the SSE subscription is established, then "done" afterward.
// The events endpoint sends NO event and holds the connection open, modeling
// the missed terminal event that the broker (no replay) never delivers.
func fakeRaceJobServer(t *testing.T, jobID, taskID string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	subscribed := false
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			done := subscribed
			mu.Unlock()
			status, taskStatus := "running", "pending"
			if done {
				status, taskStatus = "done", "done"
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: status,
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: taskStatus}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			mu.Lock()
			subscribed = true
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Send no event; hold open until the request context is cancelled.
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			json.NewEncoder(w).Encode([]struct {
				Stream  string `json:"stream"`
				Content string `json:"content"`
			}{
				{Stream: "stdout", Content: "frame rendered"},
			})
		}
	}))
}

func TestWatchJobLogs_TerminalBeforeSubscribe_DoesNotHang(t *testing.T) {
	jobID, taskID := "job-race", "task-race"
	srv := fakeRaceJobServer(t, jobID, taskID)
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var out strings.Builder
	status, err := watchJobLogs(ctx, c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}

func TestWatchJobLogs_DoneExits0(t *testing.T) {
	jobID, taskID := "job-1", "task-1"
	srv := fakeJobServer(t, jobID, taskID, "done")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out strings.Builder
	status, err := watchJobLogs(context.Background(), c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}

func TestWatchJobLogs_FailedReturnsFailed(t *testing.T) {
	jobID, taskID := "job-2", "task-2"
	srv := fakeJobServer(t, jobID, taskID, "failed")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out strings.Builder
	status, err := watchJobLogs(context.Background(), c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "failed", status)
}

func TestRunLogs_DoneExitsCleanly(t *testing.T) {
	jobID, taskID := "job-3", "task-3"
	srv := fakeJobServer(t, jobID, taskID, "done")
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doLogs(context.Background(), cfg, []string{jobID}, &out)
	require.NoError(t, err)
}

func TestRunLogs_FailedReturnsSilentError(t *testing.T) {
	jobID, taskID := "job-4", "task-4"
	srv := fakeJobServer(t, jobID, taskID, "failed")
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out strings.Builder
	err := doLogs(context.Background(), cfg, []string{jobID}, &out)
	require.Error(t, err)
	var se silentError
	require.ErrorAs(t, err, &se)
}

func TestWatchJobLogs_AlreadyDone_PrintsLogsAndExits(t *testing.T) {
	jobID, taskID := "job-5", "task-5"
	srv := fakeCompletedJobServer(t, jobID, taskID, "done")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out strings.Builder
	status, err := watchJobLogs(context.Background(), c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}

func TestWatchJobLogs_AlreadyCancelled_ReturnsCancelled(t *testing.T) {
	jobID, taskID := "job-6", "task-6"
	srv := fakeCompletedJobServer(t, jobID, taskID, "cancelled")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out strings.Builder
	status, err := watchJobLogs(context.Background(), c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
}
