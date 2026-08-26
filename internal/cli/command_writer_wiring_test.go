package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The stream split is pinned one call frame down, on doLogs/doSubmit, by tests
// that pass two strings.Builders. Those tests cannot see the argument pair the
// Command closures actually build: transposing `os.Stdout, os.Stderr` to
// `os.Stderr, os.Stdout` inside LogsCommand or SubmitCommand leaves every
// package green, because no test in the tree constructed either Command.
//
// This is the same-typed-adjacent-args shape. A wiring guard that only proves
// both writers arrive is fail-open on WHICH IS WHICH, so these run the real
// closures through captureStdStreams (admin_output_test.go) and assert
// positionally against the process's actual streams.
//
// The discriminator has to survive the type collision, so the fake server puts
// BOTH streams to work in one run: one task's log succeeds and lands on stdout,
// a second task's log 500s and its diagnostic lands on stderr. Each assertion is
// made in both directions - present here, absent there - so a transposition has
// nowhere to hide. The real mutant is `relay logs <id> > run.log` capturing an
// empty file while the log floods the terminal.
func fakeSplitStreamServer(t *testing.T, jobID, okTaskID, badTaskID string) *httptest.Server {
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
				Tasks: []taskResp{
					{ID: okTaskID, Name: "frame-001", Status: "done"},
					{ID: badTaskID, Name: "frame-002", Status: "done"},
				},
			})

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+okTaskID+"/logs":
			writeTaskLogPage(w, r, oneFrameRows())

		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+badTaskID+"/logs":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLogsCommand_WiresLogsToStdoutAndDiagnosticsToStderr(t *testing.T) {
	jobID, okTaskID, badTaskID := "job-logs-wiring", "task-logs-ok", "task-logs-bad"
	srv := fakeSplitStreamServer(t, jobID, okTaskID, badTaskID)

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := LogsCommand()
	var runErr error
	stdout, stderr := captureStdStreams(t, func() {
		runErr = cmd.Run(ctx, []string{jobID}, cfg)
	})
	require.Error(t, runErr, "one task's log failed, so the command must not report success")

	require.Contains(t, stdout, "[frame-001 stdout] frame rendered",
		"log lines belong on the process's stdout so they can be redirected to a file")
	require.NotContains(t, stdout, "incomplete",
		"the incomplete-log diagnostic must not pollute the redirected log")

	require.Contains(t, stderr, "frame-002")
	require.Contains(t, stderr, "incomplete",
		"the incomplete-log diagnostic belongs on the process's stderr")
	require.NotContains(t, stderr, "frame rendered",
		"log lines must not go to stderr")
}

func TestSubmitCommand_WiresLogsToStdoutAndDiagnosticsToStderr(t *testing.T) {
	jobID, okTaskID, badTaskID := "job-submit-wiring", "task-submit-ok", "task-submit-bad"
	srv := fakeSplitStreamServer(t, jobID, okTaskID, badTaskID)

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := SubmitCommand()
	var runErr error
	stdout, stderr := captureStdStreams(t, func() {
		runErr = cmd.Run(ctx, []string{writeJobFile(t)}, cfg)
	})
	require.Error(t, runErr, "one task's log failed, so the command must not report success")

	require.Contains(t, stdout, jobID, "the new job id belongs on stdout")
	require.Contains(t, stdout, "[frame-001 stdout] frame rendered",
		"log lines belong on the process's stdout so they can be redirected to a file")
	require.NotContains(t, stdout, "incomplete",
		"the incomplete-log diagnostic must not pollute the redirected log")

	require.Contains(t, stderr, "frame-002")
	require.Contains(t, stderr, "incomplete",
		"the incomplete-log diagnostic belongs on the process's stderr")
	require.NotContains(t, stderr, "frame rendered",
		"log lines must not go to stderr")
	require.NotContains(t, stderr, jobID+"\n",
		"the new job id must not go to stderr")
}
