//go:build integration

package api_test

import (
	"encoding/json"
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
