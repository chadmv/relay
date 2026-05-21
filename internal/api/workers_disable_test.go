//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doDisableReq issues an authenticated request and returns the recorder.
func doDisableReq(t *testing.T, srv *api.Server, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func mustParseUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(s))
	return u
}

func TestDisableWorker_AdminOnly(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Dis User", "dis-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)
	admin := createTestUser(t, q, "Dis Admin", "dis-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	row, err := q.UpsertWorkerByHostname(t.Context(), store.UpsertWorkerByHostnameParams{
		Name: "dw", Hostname: "dw", CpuCores: 4, RamGb: 16,
		GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	workerID := fmtUUID(row.ID)

	// Non-admin -> 403.
	rec := doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/disable", userToken)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Admin -> 200, status coalesced to "disabled".
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/disable", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "disabled", body["status"])
	assert.NotNil(t, body["disabled_at"])
	assert.Equal(t, float64(0), body["requeued_tasks"], "drain mode requeues nothing")

	// Idempotent: second disable also 200, still disabled.
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/disable", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)

	// Enable -> 200, status reverts, disabled_at gone.
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/enable", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	body = map[string]any{}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.NotEqual(t, "disabled", body["status"])
	disabledAtVal, hasDisabledAt := body["disabled_at"]
	assert.True(t, !hasDisabledAt || disabledAtVal == nil,
		"disabled_at must be absent or null once enabled")

	// Idempotent enable: second enable also 200.
	rec = doDisableReq(t, srv, "POST", "/v1/workers/"+workerID+"/enable", adminToken)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDisableWorker_UnknownWorkerIs404(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Dis Admin 404", "dis-admin-404@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	const missing = "/v1/workers/00000000-0000-0000-0000-000000000000/disable"
	rec := doDisableReq(t, srv, "POST", missing, adminToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = doDisableReq(t, srv, "POST",
		"/v1/workers/00000000-0000-0000-0000-000000000000/enable", adminToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDisableWorker_DrainModeLeavesRunningTaskAlone(t *testing.T) {
	env := newCancelTestServer(t)
	user := createTestUser(t, env.q, "Drain User", "drain-user@example.com", true)
	userToken := createTestToken(t, env.q, user.ID)
	jobID := seedRunningTask(t, env, user.ID)

	// Disable without ?requeue: the running task must stay running.
	rec := doDisableReq(t, env.srv, "POST",
		"/v1/workers/"+uuidString(env.workerID)+"/disable", userToken)
	require.Equal(t, http.StatusOK, rec.Code)

	tasks, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "running", tasks[0].Status, "drain mode must not touch the running task")

	// No CancelTask should have been sent to the agent.
	for _, m := range env.cs.snapshot() {
		assert.Nil(t, m.GetCancelTask(), "drain mode must not send CancelTask")
	}
}

func TestDisableWorker_RequeueModeRequeuesAndCancels(t *testing.T) {
	env := newCancelTestServer(t)
	user := createTestUser(t, env.q, "Requeue User", "requeue-user@example.com", true)
	userToken := createTestToken(t, env.q, user.ID)
	jobID := seedRunningTask(t, env, user.ID)

	rec := doDisableReq(t, env.srv, "POST",
		"/v1/workers/"+uuidString(env.workerID)+"/disable?requeue=true", userToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(1), body["requeued_tasks"])

	// Task is back to pending, unassigned.
	tasks, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "pending", tasks[0].Status)
	assert.False(t, tasks[0].WorkerID.Valid)

	// A CancelTask was sent to the agent for that task.
	var sawCancel bool
	for _, m := range env.cs.snapshot() {
		if c := m.GetCancelTask(); c != nil && c.TaskId == uuidString(tasks[0].ID) {
			sawCancel = true
		}
	}
	assert.True(t, sawCancel, "requeue mode must send CancelTask for the requeued task")
}
