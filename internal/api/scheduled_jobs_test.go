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

// createScheduleHelper creates a scheduled job via HTTP and returns its ID.
func createScheduleHelper(t *testing.T, srv *api.Server, token, name string) string {
	t.Helper()
	body := `{
		"name": "` + name + `",
		"cron_expr": "@hourly",
		"timezone": "UTC",
		"overlap_policy": "skip",
		"job_spec": {
			"name": "` + name + `-job",
			"tasks": [{"name": "task1", "command": ["echo", "hello"]}]
		}
	}`
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "createScheduleHelper: unexpected status, body: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	id, ok := resp["id"].(string)
	require.True(t, ok, "createScheduleHelper: id missing in response")
	require.NotEmpty(t, id)
	return id
}

func TestCreateScheduledJob_HappyPath(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "sched-alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{
		"name": "daily-render",
		"cron_expr": "@daily",
		"timezone": "UTC",
		"overlap_policy": "skip",
		"job_spec": {
			"name": "render-job",
			"tasks": [{"name": "render", "command": ["blender", "--render"]}]
		}
	}`
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, "daily-render", resp["name"])
	assert.Equal(t, "@daily", resp["cron_expr"])
	assert.NotEmpty(t, resp["next_run_at"])
}

func TestCreateScheduledJob_InvalidCron(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "sched-invalid-cron@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{
		"name": "bad-cron",
		"cron_expr": "not valid",
		"job_spec": {
			"name": "j",
			"tasks": [{"name": "t", "command": ["echo"]}]
		}
	}`
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateScheduledJob_InvalidTimezone(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "sched-invalid-tz@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{
		"name": "bad-tz",
		"cron_expr": "@hourly",
		"timezone": "Not/Real",
		"job_spec": {
			"name": "j",
			"tasks": [{"name": "t", "command": ["echo"]}]
		}
	}`
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateScheduledJob_TooShortInterval(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "sched-too-short@example.com", false)
	token := createTestToken(t, q, user.ID)

	body := `{
		"name": "too-fast",
		"cron_expr": "@every 5s",
		"job_spec": {
			"name": "j",
			"tasks": [{"name": "t", "command": ["echo"]}]
		}
	}`
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListScheduledJobs_OwnerOnlySeesOwn(t *testing.T) {
	srvAlice, qAlice := newTestServer(t)
	alice := createTestUser(t, qAlice, "Alice", "list-alice@example.com", false)
	aliceToken := createTestToken(t, qAlice, alice.ID)
	bob := createTestUser(t, qAlice, "Bob", "list-bob@example.com", false)
	bobToken := createTestToken(t, qAlice, bob.ID)

	// Alice creates one schedule
	createScheduleHelper(t, srvAlice, aliceToken, "alice-schedule")
	// Bob creates one schedule
	createScheduleHelper(t, srvAlice, bobToken, "bob-schedule")

	// Alice lists: should only see hers
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	srvAlice.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var items []map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&items))
	assert.Len(t, items, 1)
	assert.Equal(t, "alice-schedule", items[0]["name"])
}

func TestListScheduledJobs_AdminSeesAll(t *testing.T) {
	srv, q := newTestServer(t)
	alice := createTestUser(t, q, "Alice", "list-admin-alice@example.com", false)
	aliceToken := createTestToken(t, q, alice.ID)
	admin := createTestUser(t, q, "Admin", "list-admin-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	// Alice creates a schedule
	createScheduleHelper(t, srv, aliceToken, "alice-job")

	// Admin lists: should see all (1 created above)
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var items []map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&items))
	assert.Len(t, items, 1)
}

func TestGetScheduledJob_NotOwner_Returns404(t *testing.T) {
	srv, q := newTestServer(t)
	alice := createTestUser(t, q, "Alice", "get-alice@example.com", false)
	aliceToken := createTestToken(t, q, alice.ID)
	bob := createTestUser(t, q, "Bob", "get-bob@example.com", false)
	bobToken := createTestToken(t, q, bob.ID)

	// Alice creates a schedule
	aliceSchedID := createScheduleHelper(t, srv, aliceToken, "alice-private")

	// Bob tries to get Alice's schedule: should get 404
	req := httptest.NewRequest("GET", "/v1/scheduled-jobs/"+aliceSchedID, nil)
	req.Header.Set("Authorization", "Bearer "+bobToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPatchScheduledJob_UpdatesCronExprAndRecomputesNextRun(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "patch-alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	schedID := createScheduleHelper(t, srv, token, "patch-me")

	// Patch just the cron_expr
	patchBody := `{"cron_expr": "@hourly"}`
	req := httptest.NewRequest("PATCH", "/v1/scheduled-jobs/"+schedID, strings.NewReader(patchBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "@hourly", resp["cron_expr"])
	assert.NotEmpty(t, resp["next_run_at"])
}

func TestDeleteScheduledJob(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "delete-alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	schedID := createScheduleHelper(t, srv, token, "delete-me")

	// DELETE → 204
	req := httptest.NewRequest("DELETE", "/v1/scheduled-jobs/"+schedID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Subsequent GET → 404
	req2 := httptest.NewRequest("GET", "/v1/scheduled-jobs/"+schedID, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusNotFound, rec2.Code)
}

func TestRunScheduledJobNow_CreatesJob(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "runnow-alice@example.com", false)
	token := createTestToken(t, q, user.ID)

	schedID := createScheduleHelper(t, srv, token, "run-now-test")

	// POST run-now → 201 with job response
	req := httptest.NewRequest("POST", "/v1/scheduled-jobs/"+schedID+"/run-now", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["id"])
	// The job name should match the job_spec name from our helper: "run-now-test-job"
	assert.Equal(t, "run-now-test-job", resp["name"])
}
