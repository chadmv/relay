//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunScheduledJobNow_AStoredOverBoundSpecIsRefusedAsPermanent covers the
// interactive path that tells an operator WHY a schedule stopped producing jobs.
//
// The bounds are retroactive: jobspec.Validate runs on the STORED spec every time
// a schedule fires, so a row written by an older release can now fail validation.
// schedrunner's own path records the same message on the row (last_error /
// last_error_at, pinned by
// TestTickOnce_AStoredSpecOverTheBoundRecordsItsFailureAndStaysEnabled), so the
// failure is discoverable without suspecting a schedule first. run-now is the way
// to ask the same question ON DEMAND and get the UNTRUNCATED answer back, rather
// than waiting for the next scheduled fire, and it is reachable from
// `relay schedules run-now`, the SPA and MCP.
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
		"the per-task message must reach the operator verbatim - run-now is the interactive path "+
			"that can explain why this schedule stopped firing, and a generic 'create job failed' "+
			"throws that explanation away")

	// THE COUNT AXIS, on the same server. A stored spec whose per-task command
	// count an older release accepted is refused by the same 400 and the same
	// verbatim message, and this is the leg that proves the count bounds inherit
	// run-now's permanence rather than merely POST /v1/jobs'.
	//
	// 501 entries at nine bytes is about 4.6 KB of stored JSONB - deliberately the
	// cheapest of the three count axes to express, since the property under test is
	// the status code and the message, not the size of the row.
	var cmds strings.Builder
	for i := 0; i < 501; i++ {
		if i > 0 {
			cmds.WriteString(",")
		}
		cmds.WriteString(`["true"]`)
	}
	overCount := `{"name":"legacy","tasks":[` +
		`{"name":"healthy-task","command":["echo","y"]},` +
		`{"name":"bad-task","commands":[` + cmds.String() + `]}]}`
	countSched, err := q.CreateScheduledJob(context.Background(), store.CreateScheduledJobParams{
		Name: "legacy-commands", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: []byte(overCount), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	code, body = postRunNow(t, srv, token, uuidString(countSched.ID))
	require.Equal(t, http.StatusBadRequest, code,
		"a stored spec over a COUNT bound is as permanent as one over a value bound: "+
			"relayclient.ErrorIsTransient reads 5xx as transient and a polling caller would never stop")
	assert.Equal(t, "task bad-task: at most 500 commands are allowed, got 501", body["error"],
		"the per-task message must reach the operator verbatim - run-now is how they turn a schedule "+
			"that has gone silent into a specific reason")

	// POSITIVE CONTROL on the same server: a schedule whose spec still validates
	// must still fire. Without it, a run-now that had started refusing every
	// schedule would pass both assertions above.
	healthy := createScheduleHelper(t, srv, token, "run-now-still-valid")
	code, body = postRunNow(t, srv, token, healthy)
	require.Equal(t, http.StatusCreated, code, "a valid stored spec must still produce a job")
	assert.Equal(t, "run-now-still-valid-job", body["name"])
}

// TestRunScheduledJobNow_AStoredSpecThatCannotDecodeIsRefusedAsPermanent covers
// the branch three lines ABOVE the validation refusal, which used to answer 500.
//
// It is the same permanence for the same reason. json.Unmarshal failing on a
// stored spec is a fact about the stored row, not about the server's moment: an
// identical request made later gets an identical answer, which is exactly
// ErrorIsTransient's own stated partition, and the operator's remedy is
// identical too (PATCH a new job_spec, or delete and recreate). Answering 500
// told a polling caller to retry a permanently broken schedule forever - the
// harm the validation branch was just changed to avoid, four lines below a
// comment that states the principle.
//
// The distinction that was considered and rejected: a validation failure is
// reachable with NOTHING corrupt (a later release tightened a bound), whereas a
// decode failure is not reachable through any current write path, since POST and
// PATCH both unmarshal before they validate. That makes it rarer, not
// transient, and a polling caller cannot tell the two apart.
//
// The fixture is valid JSONB - the column type guarantees that much - and still
// fails to decode into JobSpec, because `name` is a number where the struct
// wants a string. That is the shape a backwards-incompatible struct change
// takes across an upgrade.
func TestRunScheduledJobNow_AStoredSpecThatCannotDecodeIsRefusedAsPermanent(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Legacy", "run-now-undecodable@example.com", false)
	token := createTestToken(t, q, user.ID)

	const storedSpec = `{"name":123,"tasks":[{"name":"t","command":["echo","x"]}]}`
	sched, err := q.CreateScheduledJob(context.Background(), store.CreateScheduledJobParams{
		Name: "legacy-undecodable", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: []byte(storedSpec), OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	code, body := postRunNow(t, srv, token, uuidString(sched.ID))
	require.Equal(t, http.StatusBadRequest, code,
		"a stored spec that cannot decode is as permanent as one that cannot validate: "+
			"relayclient.ErrorIsTransient reads 5xx as transient and a polling caller would never stop")
	assert.Equal(t, "stored job_spec is invalid", body["error"])
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
