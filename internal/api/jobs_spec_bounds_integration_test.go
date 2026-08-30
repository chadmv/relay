//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postJobSpec submits body to POST /v1/jobs as token and returns the status code
// and the decoded response object.
//
// The response is decoded into map[string]any DELIBERATELY. Decoding into
// internal/api's own jobResponse would agree with the handler by construction on
// every key and every type, which is the vacuous-fixture shape CLAUDE.md's
// "Where a CLI test goes" rule warns about, in its server-side form.
func postJobSpec(t *testing.T, srv *api.Server, token, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var out map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out),
		"every response on this route is JSON, including the 400")
	return rec.Code, out
}

// TestCreateJob_RetriesAndTimeoutBoundsAreEnforcedOnTheWire is what makes the
// half-A bound more than a library property: the real handler, the real router,
// the real BearerAuth middleware and the real response body.
//
// THE REFUSALS COME FIRST and the acceptance last, so a poisoned input is never
// the last thing the test does - a distinctive input placed at the end cannot
// detect an early-exit defect.
func TestCreateJob_RetriesAndTimeoutBoundsAreEnforcedOnTheWire(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Bounds", "bounds@example.com", false)
	token := createTestToken(t, q, user.ID)

	// The offending task is FIRST and a healthy task follows it, so the error
	// cannot be produced by "the last task lost".
	code, body := postJobSpec(t, srv, token, `{
		"name": "over-budget",
		"tasks": [
			{"name": "bad-task", "command": ["echo", "x"], "retries": 11},
			{"name": "healthy-task", "command": ["echo", "y"]}
		]}`)
	require.Equal(t, http.StatusBadRequest, code,
		"an out-of-range retries must be refused at submission, not stored and discovered at dispatch")
	assert.Equal(t, "task bad-task: retries must be between 0 and 10", body["error"],
		"the per-task message must reach the wire verbatim: a caller with a fifty-task spec has to be "+
			"told WHICH task is wrong")

	code, body = postJobSpec(t, srv, token, `{
		"name": "over-deadline",
		"tasks": [{"name": "bad-task", "command": ["echo", "x"], "timeout_seconds": 604801}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t,
		"task bad-task: timeout_seconds must be between 0 and 604800 (0 or omitted means no deadline)",
		body["error"])

	// POSITIVE CONTROL, at BOTH exact boundaries, on the same server. Without it
	// a handler that had started refusing every spec would pass both assertions
	// above. It is also the leg that goes red if either constant moves down by
	// one.
	code, body = postJobSpec(t, srv, token, `{
		"name": "at-the-boundary",
		"tasks": [{"name": "t", "command": ["echo", "x"], "retries": 10, "timeout_seconds": 604800}]}`)
	require.Equal(t, http.StatusCreated, code,
		"a spec AT the boundary must still be created")

	tasks, ok := body["tasks"].([]any)
	require.True(t, ok, "the created job must carry its tasks")
	require.Len(t, tasks, 1)
	task, ok := tasks[0].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 10, task["retries"],
		"the boundary value must be STORED and echoed, not clamped: validation refuses, it never edits")
	assert.EqualValues(t, 604800, task["timeout_seconds"],
		"same for the deadline - a clamping validator would pass the 201 assertion above and still be wrong")
}

// repeatCommands renders n JSON `["true"]` entries, comma-separated, with no
// trailing comma. Nine bytes each: the cheapest entry an agent will actually
// EXECUTE, which is the entry the bounds' own arithmetic is written against.
func repeatCommands(n int) string {
	return strings.TrimSuffix(strings.Repeat(`["true"],`, n), ",")
}

// repeatTasks renders n minimal one-command tasks with unique names, comma
// separated, with no trailing comma. About 31 bytes each, so 5001 of them is
// roughly 155 KB - comfortably inside maxBodyBytes, which matters: a 413 would
// make this test pass for the wrong reason.
func repeatTasks(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"name":"t%d","command":["true"]}`, i)
	}
	return b.String()
}

// TestCreateJob_TaskAndCommandCountBoundsAreEnforcedOnTheWire is what makes the
// three count bounds more than a library property: the real handler, the real
// router, the real BearerAuth middleware, the real 1 MiB body reader and the real
// response body.
//
// EVERY REFUSAL COMES FIRST and the acceptance last, so a distinctive input is
// never the last thing the test does - an offender placed at the end cannot
// detect an early-exit defect.
//
// EVERY REFUSAL MUST BE A 400, NOT A 413. All three bodies are well under
// maxBodyBytes on purpose; if one of them ever grows past 1 MiB this test starts
// passing because readJSON refused it, which is a different property entirely.
func TestCreateJob_TaskAndCommandCountBoundsAreEnforcedOnTheWire(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Counts", "counts@example.com", false)
	token := createTestToken(t, q, user.ID)

	code, body := postJobSpec(t, srv, token,
		`{"name":"too-many-tasks","tasks":[`+repeatTasks(5001)+`]}`)
	require.Equal(t, http.StatusBadRequest, code,
		"an over-count spec must be refused at submission, before 5001 sequential INSERTs are issued "+
			"inside one transaction")
	assert.Equal(t, "at most 5000 tasks are allowed, got 5001", body["error"],
		"the message must reach the wire verbatim, limit AND count: a caller who generated the spec "+
			"has to know by how much to chunk it")

	// The offending task is FIRST and a healthy task follows it, so the message
	// cannot be produced by "the last task lost".
	code, body = postJobSpec(t, srv, token,
		`{"name":"too-many-commands","tasks":[`+
			`{"name":"bad-task","commands":[`+repeatCommands(501)+`]},`+
			`{"name":"healthy-task","command":["echo","y"]}]}`)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "task bad-task: at most 500 commands are allowed, got 501", body["error"],
		"the per-task message must reach the wire verbatim: a caller with a fifty-task spec has to be "+
			"told WHICH task is wrong")

	// 50 tasks x 500 is exactly 25,000; one more command crosses it. Every task is
	// inside maxCommandsPerTask and the count is inside maxTasksPerJob, so this is
	// the refusal NEITHER per-axis bound can produce - the one that would be
	// missing if this slice had shipped two constants instead of three.
	var over strings.Builder
	over.WriteString(`{"name":"too-many-in-total","tasks":[`)
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&over, `{"name":"c%d","commands":[%s]},`, i, repeatCommands(500))
	}
	over.WriteString(`{"name":"one-more","commands":[["true"]]}]}`)
	code, body = postJobSpec(t, srv, token, over.String())
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "at most 25000 commands in total across all tasks are allowed", body["error"],
		"the job-level message carries NO task prefix on purpose: the budget is a property of the "+
			"job, and naming whichever task the accumulator crossed on would accuse an ordinary task")

	// POSITIVE CONTROL, at the exact per-task boundary, on the same server. Without
	// it a handler that had started refusing every spec would pass all three
	// assertions above. It is also the leg that goes red if maxCommandsPerTask
	// moves down by one.
	code, body = postJobSpec(t, srv, token,
		`{"name":"at-the-boundary","tasks":[{"name":"t","commands":[`+repeatCommands(500)+`]}]}`)
	require.Equal(t, http.StatusCreated, code, "a spec AT the boundary must still be created")

	tasks, ok := body["tasks"].([]any)
	require.True(t, ok, "the created job must carry its tasks")
	require.Len(t, tasks, 1)
	task, ok := tasks[0].(map[string]any)
	require.True(t, ok)
	cmds, ok := task["commands"].([]any)
	require.True(t, ok, "the created task must echo its commands")
	assert.Len(t, cmds, 500,
		"the boundary value must be STORED and echoed, not truncated: validation refuses, it never "+
			"edits, and a truncating validator would pass the 201 assertion above and still be wrong")
}
