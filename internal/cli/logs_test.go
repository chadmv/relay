// internal/cli/logs_test.go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"relay/internal/relayclient"
)

// logRow is the wire shape handleGetTaskLogs writes for one row - the api
// package's unexported logEntry (internal/api/tasks.go), reproduced here with
// its own json tags.
//
// It is deliberately NOT the CLI's own taskLogEntry. A fixture built out of the
// type under test cannot detect drift in that type: if taskLogEntry's tags were
// wrong, the fixture would marshal the same wrong keys and the whole suite would
// stay green against a CLI that cannot talk to the real server. That is exactly
// the failure this file is being changed to fix, so do not "de-duplicate" these
// two structs.
type logRow struct {
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// writeTaskLogPage serves rows the way handleGetTaskLogs (internal/api/tasks.go)
// does. Four behaviours are load-bearing and each is asserted by a test below:
//
//   - ?since_seq is EXCLUSIVE: rows with Seq > since_seq, because the SQL is
//     `WHERE task_id = $1 AND id > $2`. Asserted by
//     TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate.
//   - ?limit defaults to 50, and a value outside 1..200 is a 400 - the handler
//     rejects, it does not clamp.
//   - next_seq is the last returned row's seq, or 0 when the page is short.
//     Asserted by TestWatchJobLogs_PagesUntilDrained.
//   - total is the full row count, independent of the page.
//
// Every fake server in this file routes its logs case through here, so editing
// any of those four lines changes what every CLI log test means.
func writeTaskLogPage(w http.ResponseWriter, r *http.Request, rows []logRow) {
	writeErr := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeErr(http.StatusBadRequest, "limit must be 1..200")
			return
		}
		limit = n
	}

	var since int64
	if v := r.URL.Query().Get("since_seq"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(http.StatusBadRequest, "since_seq must be a non-negative integer")
			return
		}
		since = n
	}

	items := make([]logRow, 0, limit)
	for _, row := range rows {
		if row.Seq > since && len(items) < limit {
			items = append(items, row)
		}
	}
	var nextSeq int64
	if len(items) > 0 {
		nextSeq = items[len(items)-1].Seq
	}
	if len(items) < limit {
		nextSeq = 0 // drained
	}

	// Marshalled through a local anonymous struct rather than the CLI's
	// taskLogPage, for the same reason logRow is not taskLogEntry.
	_ = json.NewEncoder(w).Encode(struct {
		Items   []logRow `json:"items"`
		NextSeq int64    `json:"next_seq"`
		Total   int64    `json:"total"`
	}{Items: items, NextSeq: nextSeq, Total: int64(len(rows))})
}

// oneFrameRows is the single row the four fake servers below used to hand-write
// as a bare JSON array. The assertion "[frame-001 stdout] frame rendered" in the
// tests below is this row.
func oneFrameRows() []logRow {
	return []logRow{{
		Seq:       1,
		Stream:    "stdout",
		Content:   "frame rendered",
		CreatedAt: time.Unix(0, 0).UTC(),
	}}
}

// fakeJobServer serves:
//
//	GET /v1/jobs/<id>           → running job with one pending task
//	GET /v1/events?job_id=<id>  → SSE stream ending with finalJobStatus
//	GET /v1/tasks/<id>/logs     -> one page of the handler's log envelope
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
			writeTaskLogPage(w, r, oneFrameRows())
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
			writeTaskLogPage(w, r, oneFrameRows())
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
			writeTaskLogPage(w, r, oneFrameRows())
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

// fakeOverlapJobServer returns the job already terminal (task done) AND streams a
// duplicate terminal task event plus a job event - modeling the snapshot/stream overlap.
func fakeOverlapJobServer(t *testing.T, jobID, taskID string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "running", // not terminal, so the stream is still consumed
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Duplicate terminal task event for the same task already seen in the snapshot.
			fmt.Fprintf(w, "event: task\ndata: {\"id\":%q,\"status\":\"done\"}\n\n", taskID)
			fmt.Fprintf(w, "event: job\ndata: {\"status\":\"done\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
}

func TestWatchJobLogs_TaskInSnapshotAndStream_PrintedOnce(t *testing.T) {
	jobID, taskID := "job-dup", "task-dup"
	srv := fakeOverlapJobServer(t, jobID, taskID)
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var out strings.Builder
	status, err := watchJobLogs(ctx, c, jobID, &out)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, strings.Count(out.String(), "[frame-001 stdout] frame rendered"),
		"task terminal in both snapshot and stream must print exactly once")
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
