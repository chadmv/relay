//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunScheduledJobNow_AStoredOverBoundSpecIsRefusedAsPermanent covers the one
// interactive path that can tell an operator WHY a schedule stopped producing
// jobs.
//
// The bounds are retroactive: jobspec.Validate runs on the STORED spec every
// time a schedule fires, so a row written by an older release can now fail
// validation. schedrunner's own path logs one server-side line and advances
// next_run_at, which is the invisibility
// TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard
// pins. run-now is the operator's way to ask the same question and get the
// answer back, and it is reachable from `relay schedules run-now`, the SPA and
// MCP.
//
// TWO PROPERTIES, AND THE STATUS CODE CARRIES BOTH. A validation failure on a
// stored spec is a fact about the request, not about the server's moment: a
// 500 would (a) discard the per-task message that is the entire point of
// validating, and (b) be classified TRANSIENT by relayclient.ErrorIsTransient,
// so a polling caller would retry a permanently broken schedule forever. 400 is
// the same answer POST /v1/jobs gives for the same spec. Not 422: that is also
// absent from ErrorIsTransient's permanent list and would reintroduce (b).
//
// The offending task is SECOND in the stored spec, so this cannot pass by way of
// a validator that only ever looks at Tasks[0].
func TestRunScheduledJobNow_AStoredOverBoundSpecIsRefusedAsPermanent(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Legacy", "run-now-bounds@example.com", false)
	token := createTestToken(t, q, user.ID)

	// retries: 50 was accepted by every relay release before the bounds shipped,
	// so POST /v1/scheduled-jobs can no longer build this fixture. The row goes
	// in directly, which is exactly how it got there on a real upgrade.
	const storedSpec = `{"name":"legacy","tasks":[` +
		`{"name":"healthy-task","command":["echo","y"]},` +
		`{"name":"bad-task","command":["echo","x"],"retries":50}]}`
	sched, err := q.CreateScheduledJob(context.Background(), store.CreateScheduledJobParams{
		Name: "legacy-retries", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: []byte(storedSpec), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	code, body := postRunNow(t, srv, token, uuidString(sched.ID))
	require.Equal(t, http.StatusBadRequest, code,
		"a stored spec that cannot validate is a permanent refusal, not a retryable server fault: "+
			"relayclient.ErrorIsTransient reads 5xx as transient and a polling caller would never stop")
	assert.Equal(t, "task bad-task: retries must be between 0 and 10", body["error"],
		"the per-task message must reach the operator verbatim - run-now is the ONLY interactive path "+
			"that can explain why this schedule stopped firing, and a generic 'create job failed' "+
			"throws that explanation away")

	// POSITIVE CONTROL on the same server: a schedule whose spec still validates
	// must still fire. Without it, a run-now that had started refusing every
	// schedule would pass both assertions above.
	healthy := createScheduleHelper(t, srv, token, "run-now-still-valid")
	code, body = postRunNow(t, srv, token, healthy)
	require.Equal(t, http.StatusCreated, code, "a valid stored spec must still produce a job")
	assert.Equal(t, "run-now-still-valid-job", body["name"])
}

// postRunNow fires run-now for id as token and returns the status code plus the
// decoded body. The body is decoded into map[string]any deliberately: decoding
// into internal/api's own response type would agree with the handler by
// construction on every key.
func postRunNow(t *testing.T, srv *api.Server, token, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs/"+id+"/run-now", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var out map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out),
		"every response on this route is JSON, including the refusal")
	return rec.Code, out
}
