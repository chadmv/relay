//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScheduledJobFailure_IsVisibleOnGetAndOnList_AndClearedByPatch is the
// headline regression test for
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md.
//
// ################ THIS TEST DOES NOT RUN IN CI. ################
//
// .github/workflows/go-ci.yml runs `go test -race ./...` with NO TAGS and
// `make test-cli-integration` (which names ./internal/cli/... only). This file
// is //go:build integration in internal/api, so it is in neither job. It is not
// a gate and must not be described as one; it runs when a human runs
// `go test -tags integration -p 1 ./internal/api/...`, which needs Docker.
//
// That is a DECISION, recorded here in the form
// docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md's own
// acceptance criteria allow ("a written decision in the test's own comment
// saying why not"). Closing it properly means extending go-ci.yml's
// `services: postgres` job to internal/api, which requires moving
// newIntegrationDSN out of internal/cli's test files and converting
// internal/api's harness to honour RELAY_TEST_DATABASE_URL - a refactor of that
// item's own scope, on this one's critical path. That item is separately in Now
// and already names internal/api as its sharpest instance.
//
// WHAT DOES RUN IN CI, and what each covers:
//   - internal/api/scheduled_jobs_response_test.go (untagged): the wire
//     contract. Field names, absent-not-zero, present-with-values, and the arity
//     relationship between store.ScheduledJob and scheduledJobResponse. No
//     database.
//   - internal/schedrunner/failure_test.go (untagged): the classification (which
//     failure classes are recordable) and the sanitize-and-truncate helper. No
//     database.
//   - internal/cli/schedules_failure_integration_test.go: runs under
//     `make test-cli-integration`, which IS a CI job. It plants last_error with
//     SQL and reads it back through a REAL internal/api server over HTTP, so it
//     covers column -> handler -> JSON -> client in CI. (Slice C. If that slice
//     is dropped, this bullet is false and must go with it.)
//
// WHAT NOTHING IN CI COVERS, and this test is the only witness: that the
// SCHEDRUNNER writes the record at all. That half needs a tick, and a tick needs
// Postgres.
//
// WHY internal/api RATHER THAN internal/schedrunner. The criterion requires
// asserting the error is visible VIA THE API, which crosses two packages.
// internal/api already imports internal/schedrunner (ValidateMinInterval,
// ParseSchedule) and schedrunner imports nothing from internal/api, so there is
// no cycle; an internal/api integration test has the httptest server, a real
// pool, and can construct schedrunner.NewRunner and call TickOnce.
// internal/schedrunner's harness cannot do the reverse - it has no server.
//
// ASSERTIONS GO THROUGH map[string]any, NOT scheduledJobResponse. An assertion
// routed through the response struct agrees with itself by construction on both
// the key names and the omitempty behaviour, and a deep-equal against a fixture
// cannot see an absent optional field at all.
func TestScheduledJobFailure_IsVisibleOnGetAndOnList_AndClearedByPatch(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Alice", "failvis-alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	// STORED DIRECTLY, NOT THROUGH POST, and that is the whole point of the
	// defect: POST validates before storing, so the API cannot create the row
	// this test is about. The row is what a schedule accepted by an EARLIER
	// RELEASE looks like after a later release tightened jobspec.Validate.
	// retries: 50 was accepted by every relay release before the retry-bounds
	// change.
	overBound := []byte(`{"name":"legacy","tasks":[{"name":"t","command":["echo","hi"],"retries":50}]}`)
	healthySpec := []byte(`{"name":"fine","tasks":[{"name":"t","command":["echo","hi"]}]}`)

	poisoned, err := q.CreateScheduledJob(t.Context(), store.CreateScheduledJobParams{
		Name: "failvis-poisoned", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: overBound, OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-10 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// CONTROL, in the same tick. Without it, a TickOnce that had stopped firing
	// anything would satisfy every assertion below, and the "neither key is
	// present" assertions would be true of a response that never carries them.
	healthy, err := q.CreateScheduledJob(t.Context(), store.CreateScheduledJobParams{
		Name: "failvis-healthy", OwnerID: user.ID, CronExpr: "@hourly", Timezone: "UTC",
		JobSpec: healthySpec, OverlapPolicy: "skip", Enabled: true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	require.NoError(t, schedrunner.NewRunner(pool, q).TickOnce(t.Context()))

	// ---- DIAGNOSIS: the detail endpoint ----
	body := getScheduleBody(t, srv, token, uuidString(poisoned.ID))
	errText, ok := body["last_error"].(string)
	require.True(t, ok, "GET /v1/scheduled-jobs/{id} must carry last_error as a string, got %#v", body["last_error"])
	assert.Contains(t, errText, "retries must be between 0 and 10",
		"the recorded text must be jobspec.Validate's own per-task message, which is the same sentence run-now answers with")
	at, ok := body["last_error_at"].(string)
	require.True(t, ok, "GET must carry last_error_at, got %#v", body["last_error_at"])
	_, perr := time.Parse(time.RFC3339, at)
	assert.NoError(t, perr, "last_error_at must be RFC3339")

	healthyBody := getScheduleBody(t, srv, token, uuidString(healthy.ID))
	assertNoFailureKeys(t, healthyBody, "a schedule that fired")

	// ---- DISCOVERY: the LIST endpoint. This is the half the item exists for ----
	// run-now already closes diagnosis. What was missing is a way to see WHICH
	// schedule to suspect without suspecting anything first, and that is the list.
	items := listScheduleBodies(t, srv, token)
	poisonedItem := items[uuidString(poisoned.ID)]
	require.NotNil(t, poisonedItem, "the poisoned schedule must appear in GET /v1/scheduled-jobs")
	listErrText, ok := poisonedItem["last_error"].(string)
	require.True(t, ok,
		"THE POINT OF THIS SLICE: the LIST must carry last_error too, or an operator still has to "+
			"suspect a schedule before they can learn anything about it")
	assert.Contains(t, listErrText, "retries must be between 0 and 10")

	healthyItem := items[uuidString(healthy.ID)]
	require.NotNil(t, healthyItem)
	assertNoFailureKeys(t, healthyItem, "a healthy row in the list")

	// ---- PRESERVE: a PATCH that changes none of the three inputs ----
	patchSchedule(t, srv, token, uuidString(poisoned.ID), `{"name":"failvis-renamed"}`)
	afterRename := getScheduleBody(t, srv, token, uuidString(poisoned.ID))
	assert.Equal(t, "failvis-renamed", afterRename["name"], "precondition: the rename must have applied")
	_, stillThere := afterRename["last_error"]
	assert.True(t, stillThere,
		"renaming a schedule must NOT erase the only signal that it is broken. On an @monthly schedule "+
			"nothing would rewrite the record for a month")

	// ---- CLEAR: a PATCH that supplies a valid job_spec ----
	patchSchedule(t, srv, token, uuidString(poisoned.ID),
		`{"job_spec":{"name":"repaired","tasks":[{"name":"t","command":["echo","hi"]}]}}`)
	afterRepair := getScheduleBody(t, srv, token, uuidString(poisoned.ID))
	assertNoFailureKeys(t, afterRepair,
		"a PATCH that replaced job_spec (validated before storing, so any record about the old one is stale)")

	// ---- CLEAR: a PATCH of cron_expr, which is a SEPARATE ARM of the same
	// condition and had no witness until this leg existed.
	//
	// THIS LEG EXISTS BECAUSE A MUTATION SURVIVED. Narrowing the handler's
	// `req.JobSpec != nil || req.CronExpr != nil || req.Timezone != nil` to
	// `req.JobSpec != nil` alone left every other leg of this test green: the
	// rename leg does not distinguish it (it clears nothing either way) and the
	// job_spec leg exercises only the first arm. cron_expr is one of the three
	// recorded failure classes in its own right - `parse cron: ...` - so a PATCH
	// that replaces it makes a record about the old one stale by exactly the same
	// argument, and nothing anywhere said so.
	//
	// The record is PLANTED with SQL rather than produced by a tick. Producing a
	// cron failure means storing an unparseable cron_expr, which POST and PATCH
	// both refuse, so a planted row is the only way to reach this state - the same
	// reason internal/schedrunner/skip_preserves_failure_integration_test.go
	// plants its own.
	_, err = pool.Exec(t.Context(),
		`UPDATE scheduled_jobs SET last_error = $1, last_error_at = NOW() WHERE id = $2`,
		"parse cron: expected 5 fields, found 3", poisoned.ID)
	require.NoError(t, err)
	beforeCron := getScheduleBody(t, srv, token, uuidString(poisoned.ID))
	require.Contains(t, beforeCron, "last_error", "precondition: the planted record must be visible")

	patchSchedule(t, srv, token, uuidString(poisoned.ID), `{"cron_expr":"@daily"}`)
	afterCron := getScheduleBody(t, srv, token, uuidString(poisoned.ID))
	assert.Equal(t, "@daily", afterCron["cron_expr"], "precondition: the cron change must have applied")
	assertNoFailureKeys(t, afterCron,
		"a PATCH that replaced cron_expr (validated before storing, so any record about the old one is stale)")
}

func getScheduleBody(t *testing.T, srv *api.Server, token, id string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	return m
}

func listScheduleBodies(t *testing.T, srv *api.Server, token string) map[string]map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var env pageEnvelope[map[string]any]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	out := make(map[string]map[string]any, len(env.Items))
	for _, it := range env.Items {
		id, _ := it["id"].(string)
		out[id] = it
	}
	return out
}

func patchSchedule(t *testing.T, srv *api.Server, token, id, body string) {
	t.Helper()
	req := httptest.NewRequest("PATCH", "/v1/scheduled-jobs/"+id, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

// assertNoFailureKeys checks ABSENCE, not emptiness. `""` and `null` are both
// failures here: absent is the only spelling of "healthy" the four clients read.
func assertNoFailureKeys(t *testing.T, m map[string]any, subject string) {
	t.Helper()
	for _, k := range []string{"last_error", "last_error_at"} {
		v, present := m[k]
		assert.False(t, present, "%s must omit %q entirely, got %#v", subject, k, v)
	}
}
