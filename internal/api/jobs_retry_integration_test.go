//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryEnv struct {
	srv  *api.Server
	q    *store.Queries
	pool *pgxpool.Pool
	w    store.Worker
}

func newRetryEnv(t *testing.T) *retryEnv {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	w, err := q.CreateWorker(t.Context(), store.CreateWorkerParams{
		Name: "retry-w", Hostname: "retry-host", CpuCores: 4, RamGb: 8, Os: "linux",
	})
	require.NoError(t, err)
	return &retryEnv{srv: srv, q: q, pool: pool, w: w}
}

func (e *retryEnv) job(t *testing.T, owner pgtype.UUID) store.Job {
	t.Helper()
	job, err := e.q.CreateJob(t.Context(), store.CreateJobParams{
		Name: "retry-job", Priority: "normal", SubmittedBy: owner, Labels: []byte("{}"),
	})
	require.NoError(t, err)
	return job
}

// task drives a new task of job to status through the production path, so the
// row carries a real assignee and epoch. status may be pending, dispatched,
// running, done, failed or timed_out. started_at and finished_at are stamped the
// way handleTaskStatus stamps them.
func (e *retryEnv) task(t *testing.T, job store.Job, name, status string) store.Task {
	t.Helper()
	ctx := t.Context()
	task, err := e.q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: name, Commands: []byte(`[["echo","x"]]`),
		Env: []byte("{}"), Requires: []byte("{}"), Retries: 0,
	})
	require.NoError(t, err)
	if status == "pending" {
		return task
	}
	claimed, err := e.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: e.w.ID,
	})
	require.NoError(t, err)
	if status == "dispatched" {
		return claimed
	}
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	params := store.UpdateTaskStatusParams{
		ID: claimed.ID, Status: status, WorkerID: claimed.WorkerID,
		AssignmentEpoch: claimed.AssignmentEpoch, StartedAt: now,
	}
	if status != "running" {
		params.FinishedAt = now
	}
	updated, err := e.q.UpdateTaskStatus(ctx, params)
	require.NoError(t, err)
	return updated
}

// recompute settles the job's status from its tasks, the way every production
// task transition does. Without it the job would still be `pending` and the
// handler's job-status gate would 409 for the wrong reason.
func (e *retryEnv) recompute(t *testing.T, job store.Job) string {
	t.Helper()
	st, err := e.q.RecomputeJobStatus(t.Context(), job.ID)
	require.NoError(t, err)
	return st
}

func (e *retryEnv) do(t *testing.T, token, jobID, query string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/v1/jobs/" + jobID + "/retry"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *retryEnv) getTask(t *testing.T, id pgtype.UUID) store.Task {
	t.Helper()
	got, err := e.q.GetTask(t.Context(), id)
	require.NoError(t, err)
	return got
}

func (e *retryEnv) getJob(t *testing.T, id pgtype.UUID) store.Job {
	t.Helper()
	got, err := e.q.GetJob(t.Context(), id)
	require.NoError(t, err)
	return got
}

func errBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	s, _ := body["error"].(string)
	return s
}

func TestRetryJob_Unauthenticated_401(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-401@example.com", false)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, "", uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "failed", e.getJob(t, job.ID).Status)
}

func TestRetryJob_NonOwner_404_NoSideEffects(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-victim@example.com", false)
	attacker := createTestUser(t, e.q, "Attacker", "retry-attacker@example.com", false)
	token := createTestToken(t, e.q, attacker.ID)
	job := e.job(t, owner.ID)
	task := e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "job not found", errBody(t, rec))

	// A 404 that still performed the write is the failure this test exists for.
	got := e.getTask(t, task.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, task.AssignmentEpoch, got.AssignmentEpoch)
	assert.Equal(t, "failed", e.getJob(t, job.ID).Status)
}

func TestRetryJob_TaskParam_Rejects(t *testing.T) {
	cases := []struct{ name, query string }{
		{"absent", ""},
		{"empty", "task="},
		{"wrong_case", "task=Failed"},
		{"unrecognized", "task=everything"},
		{"repeated", "task=failed&task=all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRetryEnv(t)
			owner := createTestUser(t, e.q, "Owner",
				fmt.Sprintf("retry-parse-%s@example.com", tc.name), false)
			token := createTestToken(t, e.q, owner.ID)
			job := e.job(t, owner.ID)
			task := e.task(t, job, "t", "failed")
			e.recompute(t, job)

			rec := e.do(t, token, uuidString(job.ID), tc.query)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, errBody(t, rec), `"task" is required`)

			got := e.getTask(t, task.ID)
			assert.Equal(t, "failed", got.Status, "a rejected request must write nothing")
			assert.Equal(t, task.AssignmentEpoch, got.AssignmentEpoch)
		})
	}
}

// The 400 must be indistinguishable for an existing and a non-existent job:
// parsing happens before any database work, so a malformed request leaks nothing
// and costs nothing.
func TestRetryJob_TaskParam_400_BeforeAnyDatabaseWork(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-nodb@example.com", false)
	token := createTestToken(t, e.q, owner.ID)

	rec := e.do(t, token, "0d1b2f3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f", "task=nonsense")
	require.Equal(t, http.StatusBadRequest, rec.Code,
		"an unparseable mode must 400, not 404, even for a job that does not exist")

	rec = e.do(t, token, "not-a-uuid", "task=failed")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid job id", errBody(t, rec))
}
