//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doDeleteWorker(t *testing.T, srv *api.Server, id, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/v1/workers/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestDeleteWorker_RequeuesLiveTasksBeforeReleasingTheRow is the SPINE (T-B1).
// VACUITY WARNING: `worker_id IS NULL` alone passes against a build with NO
// REQUEUE AT ALL, because the FK's ON DELETE SET NULL produces it. The epoch
// increment and status='pending' are the only assertions the FK cannot produce,
// and they are what kills M2 (requeue moved after the delete).
func TestDeleteWorker_RequeuesLiveTasksBeforeReleasingTheRow(t *testing.T) {
	env := newCancelTestServer(t)
	admin := createTestUser(t, env.q, "Del Admin", "del-admin@example.com", true)
	adminToken := createTestToken(t, env.q, admin.ID)
	jobID := seedRunningTask(t, env, admin.ID)

	// The worker must be OFFLINE: delete refuses a connected one (spec 8.1), and
	// newCancelTestServer leaves the row at its default status.
	_, err := env.q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{
		ID: env.workerID, Status: "offline",
	})
	require.NoError(t, err)

	before, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, "running", before[0].Status)
	beforeEpoch := before[0].AssignmentEpoch

	rec := doDeleteWorker(t, env.srv, uuidString(env.workerID), adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	after, err := env.q.ListTasksByJob(t.Context(), mustParseUUID(t, jobID))
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, "pending", after[0].Status, "the FK cannot produce this - only RequeueWorkerTasks can")
	assert.Equal(t, beforeEpoch+1, after[0].AssignmentEpoch,
		"the generation must END BEFORE the row is released; a delete-first build leaves this unchanged")
	assert.False(t, after[0].WorkerID.Valid)
	assert.False(t, after[0].AssignedAt.Valid)
	assert.False(t, after[0].StartedAt.Valid)

	_, err = env.q.GetWorker(t.Context(), env.workerID)
	require.Error(t, err, "the worker row must be gone")

	// T-B2: reported, not merely done. Without this a handler that discards
	// RequeueWorkerTasks's return still passes everything above.
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, float64(1), body["requeued_tasks"])
}

// TestDeleteWorker_FreesTheHostnameForTokenlessAutoEnroll is the ITEM'S HEADLINE
// ACCEPTANCE CRITERION (T-E1), asserted at the level R7 says it can be tested at:
// a subsequent InsertWorkerForAutoEnroll returns a row instead of pgx.ErrNoRows.
// THE MIDDLE REFUSAL IS NOT OPTIONAL - without it this passes against a build
// where auto-enroll never refused anything at all.
func TestDeleteWorker_FreesTheHostnameForTokenlessAutoEnroll(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Free Admin", "free-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	const host = "render-07"

	params := store.InsertWorkerForAutoEnrollParams{
		Name: host, Hostname: host, CpuCores: 4, RamGb: 16, GpuCount: 0, GpuModel: "", Os: "linux",
	}
	first, err := q.InsertWorkerForAutoEnroll(t.Context(), params)
	require.NoError(t, err)

	_, err = q.InsertWorkerForAutoEnroll(t.Context(), params)
	require.Error(t, err, "the hostname is claimed, so the second auto-enroll must be refused")

	_, err = q.UpdateWorkerStatus(t.Context(), store.UpdateWorkerStatusParams{ID: first, Status: "offline"})
	require.NoError(t, err)
	rec := doDeleteWorker(t, srv, uuidString(first), adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	third, err := q.InsertWorkerForAutoEnroll(t.Context(), params)
	require.NoError(t, err, "the hostname must be free again")
	assert.NotEqual(t, first, third, "a NEW row, not a revived one")
}
