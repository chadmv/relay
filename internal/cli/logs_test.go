// internal/cli/logs_test.go
package cli

import (
	"context"
	"encoding/json"
	"errors"
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
// does. Five behaviours are load-bearing. The first four are each asserted by a
// test below; the fifth is a PRECONDITION on the caller and nothing checks it:
//
//   - ?since_seq is EXCLUSIVE: rows with Seq > since_seq, because the SQL is
//     `WHERE task_id = $1 AND id > $2`. Asserted by
//     TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate.
//   - ?limit defaults to 50, and a value outside 1..200 is a 400 - the handler
//     rejects, it does not clamp.
//   - next_seq is the last returned row's seq, or 0 when the page is short.
//     Asserted by TestWatchJobLogs_PagesUntilDrained.
//   - total is the full row count, independent of the page. Asserted by
//     TestWatchJobLogs_IncompleteDiagnostic_NamesHowMuchIsMissing.
//   - PRECONDITION: `rows` must already be sorted ascending by Seq. The handler
//     gets its ordering from `ORDER BY id` in GetTaskLogsPage; here the order is
//     inherited from the slice and never enforced, and `next_seq =
//     items[len-1].Seq` is only the largest seq on the page if the input is
//     sorted. An unsorted fixture would hand the client a cursor that silently
//     re-returns or skips rows, and it would read as a client paging bug rather
//     than as a broken fixture. genRows satisfies this; so does oneFrameRows.
//
// Every fake server in this file routes its logs case through here, so editing
// any of those five lines changes what every CLI log test means.
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

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.True(t, completeness.complete())
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

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.True(t, completeness.complete())
	require.Equal(t, "done", status)
	require.Equal(t, 1, strings.Count(out.String(), "[frame-001 stdout] frame rendered"),
		"task terminal in both snapshot and stream must print exactly once")
}

func TestWatchJobLogs_DoneExits0(t *testing.T) {
	jobID, taskID := "job-1", "task-1"
	srv := fakeJobServer(t, jobID, taskID, "done")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(context.Background(), c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.True(t, completeness.complete())
	require.Equal(t, "done", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}

func TestWatchJobLogs_FailedReturnsFailed(t *testing.T) {
	jobID, taskID := "job-2", "task-2"
	srv := fakeJobServer(t, jobID, taskID, "failed")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(context.Background(), c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.True(t, completeness.complete())
	require.Equal(t, "failed", status)
}

// doLogs's two io.Writer arguments are same-typed and adjacent, so transposing
// them compiles and passes anything that only checks a combined output. The real
// mutant: `relay logs job 2>/dev/null` silences the logs instead of the
// diagnostics. Assert POSITIVELY on one stream and EMPTY on the other; the
// failure direction is pinned in TestWatchJobLogs_LogsFetchFails_ReportsOnStderr.
func TestRunLogs_DoneExitsCleanly(t *testing.T) {
	jobID, taskID := "job-3", "task-3"
	srv := fakeJobServer(t, jobID, taskID, "done")
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out, errOut strings.Builder
	err := doLogs(context.Background(), cfg, []string{jobID}, &out, &errOut)
	require.NoError(t, err)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered",
		"log lines belong on stdout so they can be redirected to a file")
	require.Empty(t, errOut.String(),
		"a fully successful run writes nothing to stderr")
}

func TestRunLogs_FailedReturnsSilentError(t *testing.T) {
	jobID, taskID := "job-4", "task-4"
	srv := fakeJobServer(t, jobID, taskID, "failed")
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out, errOut strings.Builder
	err := doLogs(context.Background(), cfg, []string{jobID}, &out, &errOut)
	require.Error(t, err)
	var se silentError
	require.ErrorAs(t, err, &se)
}

func TestWatchJobLogs_AlreadyDone_PrintsLogsAndExits(t *testing.T) {
	jobID, taskID := "job-5", "task-5"
	srv := fakeCompletedJobServer(t, jobID, taskID, "done")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(context.Background(), c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.True(t, completeness.complete())
	require.Equal(t, "done", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}

func TestWatchJobLogs_AlreadyCancelled_ReturnsCancelled(t *testing.T) {
	jobID, taskID := "job-6", "task-6"
	srv := fakeCompletedJobServer(t, jobID, taskID, "cancelled")
	defer srv.Close()

	c := relayclient.NewClient(srv.URL, "tok")
	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(context.Background(), c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.True(t, completeness.complete())
	require.Equal(t, "cancelled", status)
}

// fakeLogsFailServer serves a terminal job whose logs route always 500s. The
// task's own status is a parameter because a task that is NOT terminal in a
// terminal job is the input where the two reasons to call a log incomplete
// coincide.
func fakeLogsFailServer(t *testing.T, jobID, taskID, jobStatus, taskStatus string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: jobStatus,
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: taskStatus}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A failed log fetch must be distinguishable from an empty log. Before this
// slice the job was "done", so doLogs returned nil and the shell saw exit 0
// with nothing on either stream - the exact production symptom.
func TestWatchJobLogs_LogsFetchFails_ReportsOnStderr(t *testing.T) {
	jobID, taskID := "job-500", "task-500"
	srv := fakeLogsFailServer(t, jobID, taskID, "done", "done")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err, "a log-fetch failure must not abort the watch")
	require.Equal(t, "done", status)
	require.Equal(t, 1, completeness.incompleteTasks)
	require.Empty(t, out.String())
	require.Contains(t, errOut.String(), "frame-001", "the diagnostic names the task")
	require.Contains(t, errOut.String(), taskID, "the diagnostic names the task id")
	require.Contains(t, errOut.String(), "incomplete")
	require.NotContains(t, errOut.String(), "rows)",
		"the very first request failed, so no total was ever reported; "+
			"\"(0 of 0 rows)\" would be noise dressed up as data")

	// And doLogs turns that count into a printed, non-silent error, so the
	// shell sees exit 1 WITH a message rather than the bare exit 1 of
	// silentError{}.
	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out2, errOut2 strings.Builder
	err = doLogs(ctx, cfg, []string{jobID}, &out2, &errOut2)
	require.Error(t, err)
	var se silentError
	require.False(t, errors.As(err, &se),
		"a described log failure must not be silent - Dispatch has to print it")
	require.Contains(t, err.Error(), "logs incomplete")

	// The converse direction of the stdout/stderr split: on the failure path the
	// diagnostic is on stderr and stdout is empty. Together with
	// TestRunLogs_DoneExitsCleanly this pins both writers positionally, so
	// transposing the two same-typed arguments cannot survive.
	require.Empty(t, out2.String(), "nothing was fetched, so stdout stays empty")
	require.Contains(t, errOut2.String(), "frame-001")
	require.Contains(t, errOut2.String(), "incomplete")
}

// fakeLogPagingServer serves a job that is already done with one finished task,
// and answers the logs route through writeTaskLogPage - so the paging contract
// under test is the handler's, not a literal. It records one entry per logs
// request so a test can assert how the client paged.
type fakeLogPagingServer struct {
	*httptest.Server

	mu        sync.Mutex
	requests  int
	sinceSeqs []string
	limits    []string
	failFrom  int // when > 0, the Nth and later logs requests return 500
}

// There is no /v1/events case: an unmatched request returns 200 with an empty
// body, which StreamEvents treats as a live subscription that immediately ends.
// onSubscribed then sees the terminal job and prints. fakeCompletedJobServer
// relies on the same thing.
func newFakeLogPagingServer(t *testing.T, jobID, taskID string, rows []logRow, failFrom int) *fakeLogPagingServer {
	t.Helper()
	f := &fakeLogPagingServer{failFrom: failFrom}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			f.mu.Lock()
			f.requests++
			n := f.requests
			f.sinceSeqs = append(f.sinceSeqs, r.URL.Query().Get("since_seq"))
			f.limits = append(f.limits, r.URL.Query().Get("limit"))
			f.mu.Unlock()
			if f.failFrom > 0 && n >= f.failFrom {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeTaskLogPage(w, r, rows)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeLogPagingServer) stats() (int, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, append([]string(nil), f.sinceSeqs...), append([]string(nil), f.limits...)
}

// genRows returns n rows with CONTIGUOUS seq ids 1..n and content "line <seq>",
// so an assertion naming a line names a specific row.
//
// The contiguity is load-bearing. task_logs.id is a global BIGSERIAL, so
// per-task seqs are usually gapped - and a gapped fixture makes the classic
// off-by-one invisible, because `id > lastSeq+1` and `id > lastSeq` return the
// same rows when no id equals lastSeq+1. Contiguous ids are a legitimate
// production state (one task logging alone) and are the discriminating input.
// Do not "make this more realistic" by introducing gaps.
func genRows(n int) []logRow {
	rows := make([]logRow, n)
	for i := range rows {
		rows[i] = logRow{
			Seq:       int64(i + 1),
			Stream:    "stdout",
			Content:   fmt.Sprintf("line %d", i+1),
			CreatedAt: time.Unix(0, 0).UTC(),
		}
	}
	return rows
}

func outLines(t *testing.T, out string) []string {
	t.Helper()
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

func TestWatchJobLogs_PagesUntilDrained(t *testing.T) {
	jobID, taskID := "job-page", "task-page"
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(450), 0)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.True(t, completeness.complete())
	require.Empty(t, errOut.String())

	require.Contains(t, out.String(), "[frame-001 stdout] line 450",
		"the last row of the last page must be printed - a single-page client stops at 200")
	lines := outLines(t, out.String())
	require.Len(t, lines, 450)
	require.Equal(t, "[frame-001 stdout] line 1", lines[0])
	require.Equal(t, "[frame-001 stdout] line 450", lines[449])

	requests, sinceSeqs, limits := srv.stats()
	require.Equal(t, 3, requests, "450 rows at limit=200 is two full pages plus one short page")
	require.Equal(t, []string{"0", "200", "400"}, sinceSeqs,
		"the cursor is the previous page's next_seq verbatim - since_seq is exclusive")
	require.Equal(t, []string{"200", "200", "200"}, limits)
}

func TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate(t *testing.T) {
	jobID, taskID := "job-exact", "task-exact"
	// 400 CONTIGUOUS rows: page 1 and page 2 are both full, so both carry a
	// non-zero next_seq, and a third request is needed to learn the log is
	// drained. See genRows for why contiguity is the discriminating input.
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(400), 0)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.True(t, completeness.complete())

	require.Contains(t, out.String(), "[frame-001 stdout] line 201\n",
		"since_seq is EXCLUSIVE: paging with lastSeq+1 skips this row entirely")
	require.Equal(t, 1, strings.Count(out.String(), "[frame-001 stdout] line 200\n"),
		"paging with lastSeq-1 would re-return this row")
	require.Len(t, outLines(t, out.String()), 400)

	requests, sinceSeqs, _ := srv.stats()
	require.Equal(t, 3, requests,
		"a full second page carries a non-zero next_seq, so a third (empty) request is required")
	require.Equal(t, []string{"0", "200", "400"}, sinceSeqs)
}

func TestWatchJobLogs_FailsOnSecondPage_PrintsFirstPage(t *testing.T) {
	jobID, taskID := "job-midfail", "task-midfail"
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(400), 2)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, completeness.incompleteTasks)

	// Printed as it went: page 1 survives the failure of page 2. An
	// implementation that accumulates pages and discards them on error fails
	// here and passes TestWatchJobLogs_LogsFetchFails.
	require.Contains(t, out.String(), "[frame-001 stdout] line 1\n")
	require.Contains(t, out.String(), "[frame-001 stdout] line 200\n")
	require.NotContains(t, out.String(), "line 201")
	require.Len(t, outLines(t, out.String()), 200)

	require.Contains(t, errOut.String(), "stopped after seq 200",
		"the diagnostic names where the output stops, so an operator can resume by hand")
	require.Contains(t, errOut.String(), "frame-001")
}

// fakeNeverDrainingServer is a deliberately MISBEHAVING server, so it does not
// route through writeTaskLogPage (which is a correct simulator). Every logs
// request returns a full page of 200 rows. When advance is true the cursor
// strictly increases and the log never drains; when false the cursor is
// constant, which is the shape that loops forever at zero cost to the server.
func fakeNeverDrainingServer(t *testing.T, jobID, taskID string, advance bool) *fakeLogPagingServer {
	t.Helper()
	f := &fakeLogPagingServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			f.mu.Lock()
			f.requests++
			f.sinceSeqs = append(f.sinceSeqs, r.URL.Query().Get("since_seq"))
			f.mu.Unlock()

			since, _ := strconv.ParseInt(r.URL.Query().Get("since_seq"), 10, 64)
			base := since
			if !advance {
				base = 0 // always the same 200 rows, always the same next_seq
			}
			items := make([]logRow, 200)
			for i := range items {
				seq := base + int64(i) + 1
				items[i] = logRow{Seq: seq, Stream: "stdout", Content: fmt.Sprintf("line %d", seq)}
			}
			_ = json.NewEncoder(w).Encode(struct {
				Items   []logRow `json:"items"`
				NextSeq int64    `json:"next_seq"`
				Total   int64    `json:"total"`
			}{Items: items, NextSeq: base + 200, Total: 1 << 30})
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// These two tests mutate the maxLogPages package var, so they must never be
// t.Parallel() (nothing in this package is).
func TestWatchJobLogs_ServerNeverDrains_StopsAtCap(t *testing.T) {
	old := maxLogPages
	maxLogPages = 3
	defer func() { maxLogPages = old }()

	jobID, taskID := "job-nodrain", "task-nodrain"
	srv := fakeNeverDrainingServer(t, jobID, taskID, true)

	c := relayclient.NewClient(srv.URL, "tok")
	// Bounded so that a missing cap is a failed assertion rather than a hung
	// suite. If this test ever fails with "context deadline exceeded" in errOut,
	// report that plainly: the cap did not fire.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, completeness.incompleteTasks)

	requests, _, _ := srv.stats()
	require.Equal(t, 3, requests, "the loop must stop at maxLogPages requests")
	require.Len(t, outLines(t, out.String()), 600, "everything fetched before the cap is still printed")
	require.Contains(t, errOut.String(), "frame-001")
	require.Contains(t, errOut.String(), "truncated after 3 pages")
	require.NotContains(t, errOut.String(), "context deadline exceeded")
}

func TestWatchJobLogs_CursorDoesNotAdvance_StopsImmediately(t *testing.T) {
	old := maxLogPages
	// Well above 2. The point of this test is that the non-advancing guard
	// fires long BEFORE the page cap; 50 proves that as well as 10000 does and
	// keeps the mutation check below to 50 requests instead of 10000.
	maxLogPages = 50
	defer func() { maxLogPages = old }()

	jobID, taskID := "job-stuck", "task-stuck"
	srv := fakeNeverDrainingServer(t, jobID, taskID, false)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, completeness.incompleteTasks)

	requests, sinceSeqs, _ := srv.stats()
	require.Equal(t, 2, requests,
		"a cursor that does not advance is caught on the second request, far below maxLogPages")
	require.Equal(t, []string{"0", "200"}, sinceSeqs)
	require.Contains(t, errOut.String(), "did not advance")
	require.Contains(t, errOut.String(), "frame-001")
}

// fakeCancelAfterSubscribeServer models `relay cancel` landing while `relay logs`
// is already watching. The FIRST /v1/jobs read is the onSubscribed snapshot and
// shows the job running with its task still running; every later read is the
// authoritative post-cancel state.
//
// The SSE stream carries ONLY the job frame, because that is all the cancel path
// publishes: handleCancelJob calls CancelJobTasks, which flips every non-terminal
// task to `failed` in one statement, and then publishes a single
// `{"status":"cancelled"}` job event. No task frame is published for those tasks
// anywhere - the three production `Type: "task"` publish sites are two in
// internal/scheduler/dispatch.go and one in internal/worker/handler.go, and none
// of them is on the cancel path.
func fakeCancelAfterSubscribeServer(t *testing.T, jobID, taskID string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	jobReads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			jobReads++
			first := jobReads == 1
			mu.Unlock()
			status, taskStatus := "cancelled", "failed"
			if first {
				status, taskStatus = "running", "running"
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: status,
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: taskStatus}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "event: job\ndata: {\"status\":\"cancelled\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The headline symptom this slice exists for, by its second door. A task that was
// non-terminal at subscribe time and terminal by the time the job went terminal
// gets no task frame on the cancel path, so the stream alone never prints it: the
// job frame arrives, the handler stops the stream, and the command exits having
// printed nothing for a task that ran, produced output, and whose rows are sitting
// in task_logs.
func TestWatchJobLogs_CancelledAfterSubscribe_PrintsTheTaskThatRan(t *testing.T) {
	jobID, taskID := "job-cancel-mid", "task-cancel-mid"
	srv := fakeCancelAfterSubscribeServer(t, jobID, taskID)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
	require.True(t, completeness.complete())
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered",
		"the task ran and its rows are retrievable; a terminal job frame must not end the "+
			"watch before the authoritative task list has been reconciled")
}

// fakeReconcileFailServer answers the onSubscribed snapshot normally and then
// 500s every later /v1/jobs read, so the final reconcile cannot run.
func fakeReconcileFailServer(t *testing.T, jobID, taskID string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	jobReads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			jobReads++
			first := jobReads == 1
			mu.Unlock()
			if !first {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "running",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "event: job\ndata: {\"status\":\"done\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The reconcile's own failure fails CLOSED. Falling through the way the
// onSubscribed snapshot deliberately does would be wrong here: there the stream
// was still ahead and could deliver what the snapshot missed, but by the time the
// reconcile runs there is no stream left, so a silent fall-through re-creates the
// never-attempted omission the reconcile exists to close. The command must say so
// and must not exit 0.
func TestWatchJobLogs_FinalSnapshotUnreadable_RefusesToClaimCompleteness(t *testing.T) {
	jobID, taskID := "job-noreconcile", "task-noreconcile"
	srv := fakeReconcileFailServer(t, jobID, taskID)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err, "an unreadable final snapshot must not abort the watch")
	require.Equal(t, "done", status)
	require.False(t, completeness.complete())
	require.True(t, completeness.unreconciled)
	require.Equal(t, 0, completeness.incompleteTasks,
		"no task log was attempted and failed - the hole is that none was attempted at all")
	require.Contains(t, errOut.String(), jobID)
	require.Contains(t, errOut.String(), "logs may be missing")

	// And a done job with an unreadable task list still exits non-zero, with a
	// message: the zero value of logCompleteness is the exit code's claim, and
	// nothing here established it.
	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out2, errOut2 strings.Builder
	err = doLogs(ctx, cfg, []string{jobID}, &out2, &errOut2)
	require.Error(t, err)
	var se silentError
	require.False(t, errors.As(err, &se))
	require.Contains(t, err.Error(), "final task list could not be read")
}

// The two outcome facts are COMPOSED, not ranked. A message about logs alone
// invites the reader to conclude the job itself was fine, which against a failed
// job is the more important half of what happened and the half that was missing
// from every stream: stdout carries only task log lines, and the failed-job case
// is otherwise silent by design.
func TestRunLogs_FailedJobWithIncompleteLogs_ReportsBoth(t *testing.T) {
	jobID, taskID := "job-both", "task-both"
	srv := fakeLogsFailServer(t, jobID, taskID, "failed", "done")

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	err := doLogs(ctx, cfg, []string{jobID}, &out, &errOut)
	require.Error(t, err)
	var se silentError
	require.False(t, errors.As(err, &se))
	require.Contains(t, err.Error(), "logs incomplete")
	require.Contains(t, err.Error(), "failed",
		"the job's own outcome must survive alongside the log diagnostic")
}

// The envelope's `total` is the whole point of decoding it: "stopped after seq
// 4200" tells an operator where the output stops, but not how much is missing.
// 4200 of 4201 rows and 4200 of 91340 rows are very different situations and the
// diagnostic has to distinguish them.
func TestWatchJobLogs_IncompleteDiagnostic_NamesHowMuchIsMissing(t *testing.T) {
	jobID, taskID := "job-total", "task-total"
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(400), 2)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	_, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, 1, completeness.incompleteTasks)
	require.Contains(t, errOut.String(), "stopped after seq 200")
	require.Contains(t, errOut.String(), "(200 of 400 rows)",
		"the envelope's total is what turns a stopping point into a measure of what is missing")
}

// fakeEmptyPageServer is a MISBEHAVING server: it answers the first request with
// a full page and a non-zero next_seq, and every later request with an EMPTY page
// that still carries a non-zero next_seq.
//
// The real handler cannot do this - handleGetTaskLogs sets next_seq = 0 whenever
// len(items) < limit, so an empty page always reports drained. That is exactly why
// this shape must be an ERROR rather than a silent nil: a client that treats it as
// "drained" converts a detectable server misbehaviour into a completeness claim it
// cannot support.
func fakeEmptyPageServer(t *testing.T, jobID, taskID string) *fakeLogPagingServer {
	t.Helper()
	f := &fakeLogPagingServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			f.mu.Lock()
			f.requests++
			n := f.requests
			f.sinceSeqs = append(f.sinceSeqs, r.URL.Query().Get("since_seq"))
			f.mu.Unlock()

			items := []logRow{}
			if n == 1 {
				items = genRows(200)
			}
			_ = json.NewEncoder(w).Encode(struct {
				Items   []logRow `json:"items"`
				NextSeq int64    `json:"next_seq"`
				Total   int64    `json:"total"`
			}{Items: items, NextSeq: 200, Total: 500})
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func TestWatchJobLogs_EmptyPageWithoutDrainedFlag_IsAnError(t *testing.T) {
	jobID, taskID := "job-emptypage", "task-emptypage"
	srv := fakeEmptyPageServer(t, jobID, taskID)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, completeness.incompleteTasks,
		"an empty page that does not report the log as drained must not be read as drained")
	require.Len(t, outLines(t, out.String()), 200, "the first page still prints")
	require.Contains(t, errOut.String(), "empty page")
	require.Contains(t, errOut.String(), "(200 of 500 rows)")
}

// The page cap is the CLIENT's bound, and the old message blamed the server for
// hitting it. A log of exactly maxLogPages * 200 rows drains correctly, but its
// last page is full and so carries a non-zero next_seq: the client stops, and the
// server did nothing wrong.
func TestWatchJobLogs_ExactlyCapManyPages_MessageDoesNotBlameTheServer(t *testing.T) {
	old := maxLogPages
	maxLogPages = 2
	defer func() { maxLogPages = old }()

	jobID, taskID := "job-capboundary", "task-capboundary"
	// 400 rows at limit=200 is exactly two full pages. Every row is fetched and
	// printed; only the "is there more?" request is never made.
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(400), 0)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	_, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, 1, completeness.incompleteTasks)
	require.Len(t, outLines(t, out.String()), 400, "every row was in fact printed")
	require.Contains(t, errOut.String(), "truncated after 2 pages")
	require.Contains(t, errOut.String(), "client",
		"the cap is the client's, and this input is a server that behaved perfectly")
	require.NotContains(t, errOut.String(), "the server never reported the log as drained")

	// And it must not contradict the count printed in the same sentence. The
	// caller prepends "(400 of 400 rows)" from the envelope's own total, so
	// "the log may be longer than 400 rows" in the clause after it re-raises the
	// exact ambiguity that pair exists to resolve.
	require.Contains(t, errOut.String(), "(400 of 400 rows)")
	require.NotContains(t, errOut.String(), "may be longer than 400 rows",
		"the server's own total says 400 and 400 printed; nothing here is unknown")
}

// failingWriter is a stdout that cannot be written to: a full disk, a closed
// pipe, or a `> file` redirect onto something that refuses the write. Every
// Write fails, the way they would after the first ENOSPC.
type failingWriter struct{ err error }

func (f failingWriter) Write(p []byte) (int, error) { return 0, f.err }

// An unchecked Fprintf reaches this slice's own symptom by the other door: every
// write fails silently, the loop pages the entire log anyway, and the command
// exits 0 having printed nothing. The write error has to be as loud as a fetch
// error, and it has to STOP the paging - there is no point pulling 91340 more
// rows into a writer that is rejecting all of them.
func TestWatchJobLogs_StdoutWriteFails_ReportsIncompleteAndStops(t *testing.T) {
	jobID, taskID := "job-writefail", "task-writefail"
	srv := newFakeLogPagingServer(t, jobID, taskID, genRows(400), 0)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var errOut strings.Builder
	out := failingWriter{err: errors.New("no space left on device")}
	status, completeness, err := watchJobLogs(ctx, c, jobID, out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Equal(t, 1, completeness.incompleteTasks,
		"a log that could not be written is exactly as incomplete as one that could not be fetched")
	require.Contains(t, errOut.String(), "frame-001")
	require.Contains(t, errOut.String(), "no space left on device")

	requests, _, _ := srv.stats()
	require.Equal(t, 1, requests,
		"the loop must stop on the failing write, not page the whole log into a writer that rejects it")
}

// The task id is interpolated straight into a request path. It is not exploitable
// today - it is a gen_random_uuid() primary key that came from the same server the
// request goes to - but a crafted id would otherwise reach a different endpoint on
// that host with the operator's bearer token attached. Escaping removes the class
// rather than arguing about the current provenance.
func TestPrintTaskLogs_TaskIDIsPathEscaped(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		writeTaskLogPage(w, r, nil)
	}))
	t.Cleanup(srv.Close)

	c := relayclient.NewClient(srv.URL, "tok")
	_, err := printTaskLogs(context.Background(), c, "../../v1/users", "frame-001", &strings.Builder{})
	require.NoError(t, err)
	require.Contains(t, gotURI, "%2F", "the id's separators must be escaped, not path segments")
	require.NotContains(t, gotURI, "/v1/users")
}

// sseServerClosesTheStream, passed as the sse argument, makes /v1/events return
// as soon as the subscription is live: the SERVER dropped the connection, which
// is the one way this watch ever ends without a job frame.
//
// It is a distinct value because the empty string used to mean it, and that made
// the fixture model a server relay does not have. handleEvents holds the
// connection open with no heartbeat and no timeout until the client goes away, so
// "no frames" and "the stream ended" are not the same input - and a test whose
// only reason for terminating is a stream the real handler would never close
// cannot see a hang. That is precisely what hid the already-terminal case below.
const sseServerClosesTheStream = "\x00server-closed"

// fakeJobSnapshotServer answers GET /v1/jobs/<id> from bodies, one per read and
// repeating the last entry once they run out; writes sse verbatim on /v1/events
// and then holds the connection open the way handleEvents does, unless sse is
// sseServerClosesTheStream; and serves oneFrameRows on the task's logs route.
//
// It exists because the two snapshot readers' inputs are BODIES, not transport
// outcomes. Every failure this fixture is used for is a 200 that decodes
// cleanly and still cannot be the job's task list, which is the one shape the
// existing fixtures cannot express: fakeReconcileFailServer 500s, and a 500 is
// the failure mode the server does NOT produce when ListTasksByJob errors.
func fakeJobSnapshotServer(t *testing.T, jobID, taskID string, bodies []jobResp, sse string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			i := reads
			reads++
			mu.Unlock()
			if i >= len(bodies) {
				i = len(bodies) - 1
			}
			json.NewEncoder(w).Encode(bodies[i])

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if sse == sseServerClosesTheStream {
				return
			}
			fmt.Fprint(w, sse)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A 200 that decodes is not the same fact as an answer. `tasks` is
// `json:"tasks,omitempty"`, so a body without it decodes into a silently-empty
// slice - and handleGetJob discarded ListTasksByJob's error, so a pool
// exhaustion or statement timeout produced exactly that body. The reconcile then
// iterated nothing, set nothing, and returned having "reconciled": exit 0, both
// streams empty, which is bit-for-bit the production symptom this slice exists
// to fix, arriving through the function written to close it.
func TestWatchJobLogs_FinalSnapshotHasNoTasks_RefusesToClaimCompleteness(t *testing.T) {
	jobID, taskID := "job-notasks", "task-notasks"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "running", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}}},
		{ID: jobID, Status: "done"},
	}, "event: job\ndata: {\"status\":\"done\"}\n\n")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.False(t, completeness.complete(),
		"a body that cannot be the job's task list is a failed reconcile, not an empty one")
	require.True(t, completeness.unreconciled)
	require.Contains(t, errOut.String(), jobID)
	require.Contains(t, errOut.String(), "logs may be missing")

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	var out2, errOut2 strings.Builder
	err = doLogs(ctx, cfg, []string{jobID}, &out2, &errOut2)
	require.Error(t, err)
	var se silentError
	require.False(t, errors.As(err, &se))
}

// The reconcile never checked that the body it reasons about is the job it asked
// for. A response about another job is not a weaker answer than none; it is a
// wrong one, and printing its tasks would attribute another job's output to this
// one.
func TestWatchJobLogs_FinalSnapshotIsADifferentJob_RefusesToClaimCompleteness(t *testing.T) {
	jobID, taskID := "job-mismatch", "task-mismatch"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "running", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}}},
		{ID: "some-other-job", Status: "done", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}}},
	}, "event: job\ndata: {\"status\":\"done\"}\n\n")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.True(t, completeness.unreconciled)
	require.Empty(t, out.String(),
		"another job's task list must not be printed as this job's output")
}

// The subscribe-time snapshot has to reject the same body the reconcile does, or
// skipping the reconcile after a terminal snapshot reopens the hole by the other
// door: an unusable body carrying a terminal job status would set finalStatus,
// print nothing, and exit 0 with no reconcile left to catch it.
//
// Fail closed instead: the body establishes nothing, so the watch falls through
// to the stream exactly as it does for a transport error, and the terminal
// status is never taken from it.
// The stream is one the SERVER closed, which is the only way this input ever ends:
// every read of the job is unusable, so the watch never learns anything about it.
// What must NOT happen is the quiet version - exit 0 with nothing on either
// stream - so the completeness claim is asserted here as well as the error.
func TestWatchJobLogs_SubscribeSnapshotHasNoTasks_EstablishesNothing(t *testing.T) {
	jobID, taskID := "job-sub-notasks", "task-sub-notasks"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "done"},
	}, sseServerClosesTheStream)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.Error(t, err, "a terminal status must not be taken from a body that carries no task list")
	require.Contains(t, err.Error(), "connection lost")
	require.Empty(t, status)
	require.Empty(t, out.String())
	require.False(t, completeness.complete(),
		"nothing was ever established about this job, so nothing may claim its logs are whole")
	require.Contains(t, errOut.String(), "logs may be missing")
}

// A task still non-terminal in the FINAL snapshot of a TERMINAL job is a
// contradiction, not an absence: the job says everything is over and the task
// says it is not. Skipping it silently printed nothing for it and left the
// zero-value logCompleteness claiming the whole log was on stdout - the
// silent-and-optimistic direction, which is the wrong one.
//
// It is reachable, and not through the status vocabulary: internal/api's
// handleGetJob reads the job row and the task list in two separate statements
// with no transaction around them, so a cancel landing between the two answers a
// terminal job beside a task list read before the cancel committed. `preparing`
// is the fixture's non-terminal status here only because it is the state a task
// holds longest; any non-terminal value discriminates the same branch.
func TestWatchJobLogs_NonTerminalTaskInFinalSnapshot_PrintsAndFlagsIt(t *testing.T) {
	jobID, taskID := "job-nonterminal", "task-nonterminal"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "running", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}}},
		{ID: jobID, Status: "cancelled", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "preparing"}}},
	}, "event: job\ndata: {\"status\":\"cancelled\"}\n\n")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered",
		"the rows exist and are what the operator came for; withholding them is the same silence by another name")
	require.False(t, completeness.complete(),
		"a log that was still being written when the job ended is not a complete log")
	require.Contains(t, errOut.String(), "preparing")
	require.Contains(t, errOut.String(), "frame-001")
	require.False(t, completeness.unreconciled,
		"the final task list WAS read; what is incomplete is one task's log")
}

// The same rule at the other reader. onSubscribed sees the identical
// contradiction whenever the job was already terminal at subscribe time, and
// once the reconcile is gated on a terminal snapshot it is the ONLY reader that
// sees it on relay logs' most common invocation.
func TestWatchJobLogs_NonTerminalTaskInSubscribeSnapshot_PrintsAndFlagsIt(t *testing.T) {
	jobID, taskID := "job-sub-nonterminal", "task-sub-nonterminal"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "cancelled", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "preparing"}}},
	}, "")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
	require.False(t, completeness.complete())
	require.Contains(t, errOut.String(), "preparing")
}

// The mirror image, and the reason the rule is read off the JOB status rather
// than applied unconditionally: while the job is still RUNNING a non-terminal
// task is legitimately absent. Nothing is owed for it, its output is not final,
// and the stream will deliver its terminal frame later. Printing it here would
// duplicate it, and flagging it would make every ordinary watch report an
// incomplete log.
func TestWatchJobLogs_NonTerminalTaskWhileJobRuns_IsNotPrintedOrFlagged(t *testing.T) {
	jobID, taskID := "job-still-running", "task-still-running"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "running", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}}},
		{ID: jobID, Status: "done", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}}},
	}, "event: job\ndata: {\"status\":\"done\"}\n\n")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.True(t, completeness.complete())
	require.Equal(t, 1, strings.Count(out.String(), "[frame-001 stdout] frame rendered"),
		"the task was printed exactly once, by the reconcile, once it had gone terminal")
	require.Empty(t, errOut.String())
}

// fakeTerminalAtSubscribeServer serves a job that is already done with one
// finished task, 500s every /v1/jobs read after the first, and reports how many
// times that route was hit. The events route ends immediately, so onSubscribed
// is the only thing that observes the job.
func fakeTerminalAtSubscribeServer(t *testing.T, jobID, taskID string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			reads++
			first := reads == 1
			mu.Unlock()
			if !first {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return reads
	}
}

// `relay logs <finished-job>` is the dominant invocation of this command, and on
// it the reconcile is a pure duplicate request: onSubscribed already consumed an
// authoritative terminal snapshot, and a terminal job's every task is terminal in
// it (RecomputeJobStatus yields `running` while any is not; CancelJobTasks has
// already flipped the rest). There is nothing left for a second read to add.
//
// Not free, either. Fail that duplicate read and the command turns a perfect run
// - every log line printed, from the authoritative snapshot - into exit 1 telling
// the operator their logs may be missing. "By reconcile time there is no stream
// left to compensate" is true of the stream-terminated path and false of this
// one, where the compensating read succeeded moments earlier.
func TestWatchJobLogs_TerminalAtSubscribe_DoesNotReReadTheJob(t *testing.T) {
	jobID, taskID := "job-noreread", "task-noreread"
	srv, jobReads := fakeTerminalAtSubscribeServer(t, jobID, taskID)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
	require.True(t, completeness.complete(),
		"every task in an authoritative terminal snapshot was printed from it")
	require.Empty(t, errOut.String())
	require.Equal(t, 1, jobReads(),
		"the snapshot that ended the watch is the reconcile; asking again can only add a way to fail")

	// And end to end, on its own server because the fixture above is spent: the
	// command exits 0 on a finished job whose re-read would have failed.
	srv2, _ := fakeTerminalAtSubscribeServer(t, jobID, taskID)
	cfg := &Config{ServerURL: srv2.URL, Token: "tok"}
	var out2, errOut2 strings.Builder
	require.NoError(t, doLogs(ctx, cfg, []string{jobID}, &out2, &errOut2),
		"reading a finished job's logs is this command's dominant use and it exits 0")
	require.Contains(t, out2.String(), "[frame-001 stdout] frame rendered")
	require.Empty(t, errOut2.String())
}

// The reconcile is still owed on the path where the STREAM ended the watch, and
// this is the pin that stops the gate above from being widened into a bypass.
// fakeCancelAfterSubscribeServer's first snapshot is non-terminal, so the
// terminal status comes from the job frame and the second read is the only thing
// that can produce the task.
func TestWatchJobLogs_StreamEndedTheWatch_StillReconciles(t *testing.T) {
	jobID, taskID := "job-stream-reconcile", "task-stream-reconcile"
	srv := fakeCancelAfterSubscribeServer(t, jobID, taskID)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
	require.True(t, completeness.complete())
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}

// The reconcile's name-refresh is load-bearing exactly here: onSubscribed's
// snapshot failed, so taskNames is empty; the cancel path publishes no task
// frame, so the stream never names the task either; and the reconcile is the
// only thing that can put a name on the diagnostic and on every printed line.
// Without it the operator gets `[ stdout] ...` for a job whose tasks all have
// names.
func TestWatchJobLogs_SubscribeSnapshotFailed_ReconcileNamesTheTask(t *testing.T) {
	jobID, taskID := "job-latename", "task-latename"
	var mu sync.Mutex
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			reads++
			first := reads == 1
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "cancelled",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "failed"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "event: job\ndata: {\"status\":\"cancelled\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
	require.True(t, completeness.complete())
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered",
		"the reconcile is the only reader that ever saw this task's name")
}

// taskIsTerminal's three arms, each with a task in that state as the test's
// subject. `timed_out` appeared exactly once in this package - inside the
// predicate - so dropping it changed no test: the arm was registered in the
// store's lockstep guard while being behaviourally free.
//
// The discriminator is not "did it print". Since a non-terminal task in a
// terminal job now prints too, dropping an arm still produces the log line; what
// it produces as well is the not-final diagnostic and a non-zero exit. So the
// assertion that separates a terminal status from a non-terminal one is a clean
// completeness claim and an empty stderr.
func TestWatchJobLogs_EveryTerminalTaskStatusPrintsCleanly(t *testing.T) {
	for _, tc := range []struct{ taskStatus, jobStatus string }{
		{"done", "done"},
		{"failed", "failed"},
		{"timed_out", "failed"},
	} {
		t.Run(tc.taskStatus, func(t *testing.T) {
			jobID, taskID := "job-"+tc.taskStatus, "task-"+tc.taskStatus
			srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
				{ID: jobID, Status: tc.jobStatus,
					Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: tc.taskStatus}}},
			}, "")

			c := relayclient.NewClient(srv.URL, "tok")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			var out, errOut strings.Builder
			status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
			require.NoError(t, err)
			require.Equal(t, tc.jobStatus, status)
			require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
			require.True(t, completeness.complete(),
				"a task in a terminal state has a final log, so the output is complete")
			require.Empty(t, errOut.String(),
				"a terminal task is not 'still running when the job ended'")
		})
	}
}

// The job id is args[0] - typed by whoever ran the command, or pasted from
// wherever they got it - and it reached three request lines raw while the ONE id
// that was escaped was the gen_random_uuid() primary key the server itself had
// just handed back. The exploit string below is the one the task-id test already
// used, applied to the reachable argument.
//
// Two contexts, two escapers. On the two paths a `/` reroutes the request to
// another endpoint on the same host with the operator's bearer token attached;
// in the events query string an `&` or `#` truncates the request or injects a
// parameter the handler will read.
func TestWatchJobLogs_JobIDIsEscapedInEveryRequest(t *testing.T) {
	var mu sync.Mutex
	var uris []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uris = append(uris, r.RequestURI)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	// The empty bodies make every read fail, so this exercises the request lines
	// and nothing else. The watch ends with "connection lost", which is expected.
	_, _, err := watchJobLogs(ctx, c, "../admin/secret&x=1", &out, &errOut)
	require.Error(t, err)

	mu.Lock()
	got := append([]string(nil), uris...)
	mu.Unlock()
	// Every request line, not a fixed number of them: how many times a failing
	// snapshot is re-read is watchJobLogs' business and it has changed twice, but
	// each of the two SHAPES must be escaped for its own context, so both have to
	// be present and every one of them has to be checked.
	var jobsURI, eventsURI string
	var jobsCount, eventsCount int
	for _, u := range got {
		if strings.HasPrefix(u, "/v1/events") {
			eventsURI, eventsCount = u, eventsCount+1
		} else {
			jobsURI, jobsCount = u, jobsCount+1
		}
	}
	require.NotZero(t, jobsCount, "the job snapshot request line is under test")
	require.NotZero(t, eventsCount, "the events subscription request line is under test")

	for _, u := range got {
		require.NotContains(t, u, "/v1/admin/secret",
			"a crafted id must not reroute the request to another endpoint on the host")
	}
	require.Contains(t, jobsURI, "%2F", "the id's separators must be escaped, not path segments")
	require.NotContains(t, eventsURI, "&x=1",
		"a crafted id must not inject a second query parameter")
}

// The reconcile makes an authoritative read of the whole job and used only its
// task list. A job that emitted a `failed` frame and was then retried is
// `running` again by the time that read lands, and reporting the stale frame is
// a worse answer than the one already in hand.
//
// Only a TERMINAL fresh status is adopted. watchOutcomeError's vocabulary is
// terminal-only - "job finished running" is nonsense - and a non-terminal fresh
// status means the job was restarted after this watch's job frame, which does
// not make that frame untrue about the moment it described.
func TestWatchJobLogs_ReconcileAdoptsTheFresherJobStatus(t *testing.T) {
	jobID, taskID := "job-restatus", "task-restatus"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "running", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}}},
		// The stream said `failed`; by the reconcile the last task had finished
		// and RecomputeJobStatus had settled the job on `done`.
		{ID: jobID, Status: "done", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}}},
	}, "event: job\ndata: {\"status\":\"failed\"}\n\n")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status,
		"the reconcile looked straight at the job's current status; reporting the frame's is stale by choice")
	require.True(t, completeness.complete())
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
}

// The other direction. A non-terminal fresh status is NOT adopted: the job was
// restarted after the frame this watch saw, and "job finished running" is not a
// sentence watchOutcomeError can produce. The frame's status stays.
func TestWatchJobLogs_ReconcileDoesNotAdoptANonTerminalStatus(t *testing.T) {
	jobID, taskID := "job-retried", "task-retried"
	srv := fakeJobSnapshotServer(t, jobID, taskID, []jobResp{
		{ID: jobID, Status: "running", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}}},
		{ID: jobID, Status: "running", Tasks: []taskResp{{ID: taskID, Name: "frame-001", Status: "running"}}},
	}, "event: job\ndata: {\"status\":\"failed\"}\n\n")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "failed", status)
	require.True(t, completeness.complete(),
		"the job is running again, so its non-terminal task is owed nothing")
	require.Empty(t, out.String())
	require.Empty(t, errOut.String())
}

// fakeUUIDSpellingServer models a PRE-2026-08-30 relay-server: the three things
// every version does with a job id, plus the one thing versions before that date
// did not do. It is deliberately NOT updated to the current server - `relay logs`
// may be pointed at any server, the older one still exists in the field, and this
// fixture is the only thing proving the CLI works against it.
//
//   - GET /v1/jobs/{id} accepts every spelling pgtype.UUID.Scan takes - hex is
//     case-insensitive and the dashless 32-char form parses - and always answers
//     with the canonical lowercase-dashed form uuidStr renders.
//   - GET /v1/events?job_id= is taken VERBATIM here, and the broker's filter is
//     an exact string compare (internal/events/broker.go), so a non-canonical
//     spelling matches nothing that is published. A server from 2026-08-30
//     onward canonicalises it first (canonicalJobIDFilter, internal/api/events.go)
//     and would deliver; that server is not what this fixture models.
//   - That stream is then held open with no heartbeat and no timeout, which is
//     what turns "matches nothing" into a hang rather than an error.
//
// The accepted spellings are written out literally rather than derived from the
// CLI's own canonicaliser. A fixture built out of the code under test cannot
// detect drift in it.
func fakeUUIDSpellingServer(t *testing.T, taskID string, terminalAtSubscribe bool) *httptest.Server {
	t.Helper()
	accepted := map[string]bool{
		canonicalSpelling: true,
		uppercaseSpelling: true,
		dashlessSpelling:  true,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/jobs/"):
			if !accepted[strings.TrimPrefix(r.URL.Path, "/v1/jobs/")] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			status, taskStatus := "running", "running"
			if terminalAtSubscribe {
				status, taskStatus = "done", "done"
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     canonicalSpelling,
				Status: status,
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: taskStatus}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if !terminalAtSubscribe && r.URL.Query().Get("job_id") == canonicalSpelling {
				fmt.Fprintf(w, "event: task\ndata: {\"id\":%q,\"status\":\"done\"}\n\n", taskID)
				fmt.Fprint(w, "event: job\ndata: {\"status\":\"done\"}\n\n")
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// No heartbeat, no timeout: exactly like handleEvents.
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const (
	canonicalSpelling = "7e660488-1234-4321-8888-abcdefabcdef"
	uppercaseSpelling = "7E660488-1234-4321-8888-ABCDEFABCDEF"
	dashlessSpelling  = "7e660488123443218888abcdefabcdef"
)

// A job id is argv, and an operator pastes whatever their source gave them. The
// server takes all three spellings below and answers 200 with the right job, so
// every one of them is a working `relay get`. Only the canonical one worked here,
// and it failed in the worst way available: the id check compared the server's
// canonical answer against the raw argv spelling and declared a correct response
// unusable, and the SSE filter the non-canonical spelling subscribed to could
// never match, so the command sat on an open stream forever with nothing on
// either output.
//
// Both halves are covered: a job already FINISHED at subscribe time (where the
// snapshot is the only reader) and one still RUNNING (where the stream is). The
// running half is a hole this command shipped with; the finished half arrived
// with the id check.
func TestWatchJobLogs_NonCanonicalJobID_IsResolvedNotRejected(t *testing.T) {
	for _, spelling := range []struct{ name, id string }{
		{"canonical", canonicalSpelling},
		{"uppercase", uppercaseSpelling},
		{"dashless", dashlessSpelling},
	} {
		for _, phase := range []struct {
			name     string
			terminal bool
		}{
			{"already finished", true},
			{"still running", false},
		} {
			t.Run(spelling.name+"/"+phase.name, func(t *testing.T) {
				srv := fakeUUIDSpellingServer(t, "task-spelling", phase.terminal)
				c := relayclient.NewClient(srv.URL, "tok")
				// Its own deadline: a regression here is a HANG, and a hang
				// without a bound wedges the suite instead of failing it.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				var out, errOut strings.Builder
				status, completeness, err := watchJobLogs(ctx, c, spelling.id, &out, &errOut)
				require.NoError(t, err)
				require.Equal(t, "done", status)
				require.True(t, completeness.complete())
				require.Equal(t, 1, strings.Count(out.String(), "[frame-001 stdout] frame rendered"))
				require.Empty(t, errOut.String())
			})
		}
	}
}

// fakeSnapshotFailsThenRecoversServer 500s the first failFirst reads of
// GET /v1/jobs/<id> and answers a TERMINAL job with a finished task afterwards.
// Its events stream carries no frames and is held open the way handleEvents holds
// it, unless the server closes it.
//
// It models what the server-side half of this slice created. handleGetJob used to
// answer 200 with `tasks` absent when ListTasksByJob failed; it now 500s, which is
// the honest answer and is also a transport error at every client. The broker has
// no replay, so for a job that was already terminal when the command started there
// is no frame coming, ever: every fact this command can learn it has to learn from
// a snapshot.
func fakeSnapshotFailsThenRecoversServer(t *testing.T, jobID, taskID string, failFirst int, closeStream bool) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			reads++
			n := reads
			mu.Unlock()
			if n <= failFirst {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(jobResp{
				ID:     jobID,
				Status: "done",
				Tasks:  []taskResp{{ID: taskID, Name: "frame-001", Status: "done"}},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if closeStream {
				return
			}
			// No heartbeat, no timeout: exactly like handleEvents.
			<-r.Context().Done()

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+taskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// `relay logs <finished-job>` is this command's dominant invocation, and on it the
// subscribe-time snapshot is the ONLY thing that can establish anything: the job
// went terminal before the subscription existed and the broker has no replay, so
// no frame is coming. Falling through to the stream on a failed read therefore
// waits on something that will never arrive, forever - no heartbeat, no server
// timeout, and no deadline on the command's context. Nothing is printed and
// nothing is said, which is bit-for-bit the symptom this slice exists to fix,
// reached through the guard written to make failures visible.
//
// A single transient failure is exactly the input the server-side half of this
// slice created: a ListTasksByJob error that used to be a silent 200 is now a 500.
// Ask again.
func TestWatchJobLogs_TransientSnapshotFailure_DoesNotStrandAFinishedJob(t *testing.T) {
	jobID, taskID := "job-transient", "task-transient"
	srv := fakeSnapshotFailsThenRecoversServer(t, jobID, taskID, 1, false)

	c := relayclient.NewClient(srv.URL, "tok")
	// Its own deadline: the regression is a hang, and an unbounded hang wedges
	// the suite instead of failing it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "done", status)
	require.True(t, completeness.complete())
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")
	require.Empty(t, errOut.String())
}

// The backstop behind the retry, for the failure the retry did not outlast. A
// subscribe-time snapshot that establishes NOTHING leaves the watch with no idea
// whether the job is running or long finished, so it stays on the stream - and
// when that stream ends, the reconcile is owed. It used to be skipped, because it
// was armed on a terminal status having been observed and this path observed none;
// the operator got "connection lost" and no output for a job that finished
// yesterday.
//
// What the reconcile establishes replaces that message. What it does not
// establish must stay loud: this is the same fall-through that must never become
// exit 0 with nothing printed, which
// TestWatchJobLogs_SubscribeSnapshotHasNoTasks_EstablishesNothing pins from the
// other side.
func TestWatchJobLogs_SubscribeSnapshotEstablishedNothing_ReconcileStillRuns(t *testing.T) {
	jobID, taskID := "job-backstop", "task-backstop"
	// Two failures outlast the retry, so the fall-through is what is under test.
	srv := fakeSnapshotFailsThenRecoversServer(t, jobID, taskID, 2, true)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err,
		"the reconcile read the job successfully, so 'connection lost' is no longer the answer")
	require.Equal(t, "done", status)
	require.True(t, completeness.complete())
	require.Contains(t, out.String(), "[frame-001 stdout] frame rendered")

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	srv2 := fakeSnapshotFailsThenRecoversServer(t, jobID, taskID, 2, true)
	cfg.ServerURL = srv2.URL
	var out2, errOut2 strings.Builder
	require.NoError(t, doLogs(ctx, cfg, []string{jobID}, &out2, &errOut2))
	require.Contains(t, out2.String(), "[frame-001 stdout] frame rendered")
}

// The one input where emitSnapshot's two reasons to call a task's log incomplete
// coincide: a terminal job, a task still non-terminal in its snapshot, and a log
// fetch that fails. Each reason had a test; their intersection had none, so
// emit's return value could be inverted here and the whole suite stayed green.
//
// Two things are owed and they are not the same thing.
//
//   - The COUNT is one. incompleteTasks counts TASKS whose log is not fully on
//     stdout, not reasons, and one task with two reasons is still one task. That
//     is what emit's boolean is for and it is now its only job.
//   - Both DIAGNOSTICS are owed. The operator who reads "logs for frame-001 are
//     incomplete: fetching page 1 failed" and nothing else concludes the task
//     finished and its log was unavailable. It had not finished. The rows they
//     eventually retrieve will be short for a second reason nobody mentioned.
func TestWatchJobLogs_UnfinishedTaskWithAFailingLogFetch_SaysBothAndCountsOne(t *testing.T) {
	jobID, taskID := "job-both", "task-both"
	srv := fakeLogsFailServer(t, jobID, taskID, "cancelled", "preparing")

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, err)
	require.Equal(t, "cancelled", status)
	require.Equal(t, 1, completeness.incompleteTasks,
		"one task is one task however many things are wrong with it")
	require.Contains(t, errOut.String(), "are incomplete",
		"the log fetch failed and the operator is told so")
	require.Contains(t, errOut.String(), "was still preparing when job",
		"the task had not finished either, and that is a different fact about the same rows")
}

// A stream that never opened has an error, and that error is the answer. The
// defer's `err != nil` guard is the whole of what keeps it: without it the
// transport failure falls through to the connection-lost line below, which
// overwrites the one actionable half of what happened. A 502 on the subscribe, a
// 401, a reset connection and `bufio.Scanner: token too long` all arrive on this
// path and all of them name a different remedy; "job may still be running" names
// none.
//
// The guard's own comment argues for it correctly, and an argument is not a test:
// mutating it to `if false` left every package in internal/cli green.
func TestWatchJobLogs_StreamTransportFailure_KeepsItsOwnError(t *testing.T) {
	jobID := "job-502"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/events" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		t.Errorf("unexpected request %s %s - the subscribe never succeeded", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, _, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.Error(t, err)
	require.Contains(t, err.Error(), "502",
		"the transport failure's own text is what tells the operator what to fix")
	require.NotContains(t, err.Error(), "connection lost",
		"a subscribe that never returned 200 is not a connection that was lost mid-watch")
	require.Empty(t, status)
	require.Empty(t, out.String())
}

// A well-formed uuid that names no job is the single most likely thing an
// operator mistypes into this command, and it used to hang forever on both
// streams. handleEvents deliberately does not validate `?job_id=` (its own
// comment, internal/api/events.go), so an unknown job gets an open, permanently
// empty stream with no heartbeat and no server-side timeout, and cmd/relay's
// context has no deadline. The snapshot said 404 twice, the code shrugged, and
// `relay logs <mistyped-uuid>` printed nothing on either stream until Ctrl-C.
//
// That is bit-for-bit the symptom this slice exists to remove. internal/mcp's
// wait loop already states the rule this file broke: an answer that is as true on
// the hundredth read as on the first ends the wait at once.
func TestWatchJobLogs_JobDoesNotExist_ReportsItInsteadOfWaitingOnTheStream(t *testing.T) {
	jobID := "9c8b5a4d-1e2f-4a3b-8c7d-6e5f4a3b2c1d"
	var mu sync.Mutex
	jobReads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/jobs/"+jobID:
			mu.Lock()
			jobReads++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})

		case r.Method == "GET" && r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// No heartbeat, no timeout: exactly like handleEvents.
			<-r.Context().Done()

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := relayclient.NewClient(srv.URL, "tok")
	// Its own deadline: the defect is a hang, and an unbounded one wedges the
	// suite instead of failing it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	_, _, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.NoError(t, ctx.Err(),
		"the command must answer on its own, not be rescued by the test's deadline")
	require.Error(t, err)
	require.Contains(t, err.Error(), "job not found",
		"the server's own answer is what the operator needs; it names the mistake")
	require.NotContains(t, err.Error(), "connection lost")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, jobReads,
		"an answer that is the same on every later read is asked for once")
}

// The reconcile's diagnostic is printed on two paths and used to describe only
// one of them. Here the subscribe-time snapshot established nothing, the stream
// then ended without a word, and the reconcile failed too - so nothing in this
// run ever established that the job had finished, while the message said "after
// it finished" directly above an error saying the job may still be running.
//
// Cosmetic, because a non-nil watch error means doLogs discards the completeness
// anyway. Both lines still reach the operator's terminal, and they contradicted
// each other.
func TestWatchJobLogs_NothingEstablishedTheJobEnded_DiagnosticDoesNotSayItDid(t *testing.T) {
	jobID, taskID := "job-nostatus", "task-nostatus"
	// Every job read fails, and the stream closes without a frame.
	srv := fakeSnapshotFailsThenRecoversServer(t, jobID, taskID, 99, true)

	c := relayclient.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out, errOut strings.Builder
	status, completeness, err := watchJobLogs(ctx, c, jobID, &out, &errOut)
	require.Error(t, err)
	require.Contains(t, err.Error(), "may still be running")
	require.Empty(t, status)
	require.True(t, completeness.unreconciled)
	require.Contains(t, errOut.String(), "logs may be missing")
	require.NotContains(t, errOut.String(), "finished",
		"nothing in this run established that the job had ended, and the error beside this line says the opposite")
}
