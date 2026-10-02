//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/stretchr/testify/require"
)

func getJobTasks(t *testing.T, srv *api.Server, token, jobID string) (int, []map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	var resp struct {
		Tasks []map[string]any `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return rec.Code, resp.Tasks
}

// The worker's name ("retry-w") differs from its hostname ("retry-host") and its
// id, so the assertion fails if the handler reads the wrong column. The pending
// task has no worker and must carry no worker_name key at all. Two tasks share
// one worker, so a lookup that resolves each distinct worker once must still name both.
func TestGetJob_TaskCarriesItsWorkersName(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "worker-name@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "assigned", "running")
	e.task(t, job, "assigned-too", "running")
	e.task(t, job, "unassigned", "pending")

	code, tasks := getJobTasks(t, e.srv, token, uuidString(job.ID))
	require.Equal(t, http.StatusOK, code)
	require.Len(t, tasks, 3)
	byName := map[string]map[string]any{}
	for _, tk := range tasks {
		byName[tk["name"].(string)] = tk
	}
	require.Equal(t, "retry-w", byName["assigned"]["worker_name"])
	require.Equal(t, "retry-w", byName["assigned-too"]["worker_name"])
	require.Equal(t, uuidString(e.w.ID), byName["assigned"]["worker_id"])
	_, has := byName["unassigned"]["worker_name"]
	require.False(t, has, "a task with no worker must omit worker_name")
}

// A failed name lookup answers 500 rather than a 200 with names silently missing.
func TestGetJob_WorkerNameReadFails_IsAnError(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "worker-name-fail@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "assigned", "running")

	var fired atomic.Int64
	crippled := api.New(e.pool, store.New(failOneQueryDB{pool: e.pool, name: "ListWorkerNamesByIDs", fired: &fired}),
		events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)

	code, _ := getJobTasks(t, crippled, token, uuidString(job.ID))
	require.Equal(t, http.StatusInternalServerError, code)
	require.NotZero(t, fired.Load(), "the injector must have matched ListWorkerNamesByIDs")
}
