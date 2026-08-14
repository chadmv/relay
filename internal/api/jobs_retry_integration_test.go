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

func TestRetryJob_Owner_200(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-owner@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	task := e.task(t, job, "t", "failed")
	require.Equal(t, "failed", e.recompute(t, job))

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	got := e.getTask(t, task.ID)
	assert.Equal(t, "pending", got.Status)
	assert.Equal(t, task.AssignmentEpoch+1, got.AssignmentEpoch)
	assert.False(t, got.WorkerID.Valid)
}

func TestRetryJob_Admin_200_OnAnotherUsersJob(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-admin-owner@example.com", false)
	admin := createTestUser(t, e.q, "Admin", "retry-admin@example.com", true)
	token := createTestToken(t, e.q, admin.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "running", e.getJob(t, job.ID).Status)
}

// Item criterion 1: the two modes select demonstrably different sets. Two
// identically seeded jobs, so the difference cannot come from ordering.
func TestRetryJob_FailedVersusAll_SelectDifferentSets(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-modes@example.com", false)
	token := createTestToken(t, e.q, owner.ID)

	seed := func(name string) (store.Job, store.Task, store.Task) {
		job := e.job(t, owner.ID)
		done := e.task(t, job, name+"-done", "done")
		failed := e.task(t, job, name+"-failed", "failed")
		require.Equal(t, "failed", e.recompute(t, job))
		return job, done, failed
	}
	jobA, doneA, _ := seed("a")
	jobB, doneB, _ := seed("b")

	recA := e.do(t, token, uuidString(jobA.ID), "task=failed")
	require.Equal(t, http.StatusOK, recA.Code)
	var bodyA map[string]any
	require.NoError(t, json.NewDecoder(recA.Body).Decode(&bodyA))
	assert.Equal(t, float64(1), bodyA["tasks_retried"])
	assert.Equal(t, "done", e.getTask(t, doneA.ID).Status, "task=failed must leave a done task terminal")

	recB := e.do(t, token, uuidString(jobB.ID), "task=all")
	require.Equal(t, http.StatusOK, recB.Code)
	var bodyB map[string]any
	require.NoError(t, json.NewDecoder(recB.Body).Decode(&bodyB))
	assert.Equal(t, float64(2), bodyB["tasks_retried"])
	assert.Equal(t, "pending", e.getTask(t, doneB.ID).Status, "task=all must reopen the done task too")
}

// Key-set equality, so an accidentally added or renamed field fails. The absent
// keys are jobResponse's omitempty fields, which toJobResponse(job, "", nil, nil)
// leaves unset - exactly as handleCancelJob does.
func TestRetryJob_ResponseShape_JobPlusTasksRetried(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-shape@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	e.recompute(t, job)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))

	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{
		"id", "name", "priority", "status", "submitted_by", "labels",
		"created_at", "updated_at", "total_tasks", "done_tasks", "tasks_retried",
	}, keys, "the 200 body is jobResponse plus exactly one key")

	assert.Equal(t, "running", body["status"])
	assert.GreaterOrEqual(t, body["tasks_retried"], float64(1),
		"tasks_retried is never 0 on a 200: a zero match is a 409")
	assert.Equal(t, uuidString(job.ID), body["id"])
}

func TestRetryJob_JobStatusRecomputedToRunningInsideTheTransaction(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-recompute@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "failed")
	require.Equal(t, "failed", e.recompute(t, job))
	before := e.getJob(t, job.ID)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	after := e.getJob(t, job.ID)
	assert.Equal(t, "running", after.Status,
		"a job whose task is pending again cannot still be failed")
	assert.True(t, after.UpdatedAt.Time.After(before.UpdatedAt.Time),
		"RecomputeJobStatus stamps updated_at, and it ran in the same transaction as the reopen")
}

// installSkipUpdateTrigger makes UPDATEs on the named task a silent no-op, so
// RetryJobTasks matches it but produces no RETURNING row. This is the ONLY
// deterministic way to reach the handler's count-mismatch branch: two concurrent
// retries cannot, because both take the job row lock first and fully serialize
// (see the plan's Deviations section). Modeled on installFailDeleteTrigger.
func installSkipUpdateTrigger(t *testing.T, pool *pgxpool.Pool, taskName string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION skip_update_task() RETURNS trigger AS $$
		BEGIN
		  IF NEW.name = %s THEN RETURN NULL; END IF;
		  RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER skip_update_tasks BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION skip_update_task();
	`, quoteLiteral(taskName)))
	require.NoError(t, err)
}

func quoteLiteral(s string) string { return "'" + s + "'" }

func TestRetryJob_CancelledJob_409_NothingChanged(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-cancelled@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "running")

	// Cancel through the real endpoint, so CancelJobTasks squashes the running
	// task onto `failed` exactly as production does. That squash is why retry on
	// a cancelled job would mean "un-cancel everything".
	cancelReq := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+uuidString(job.ID), nil)
	cancelReq.Header.Set("Authorization", "Bearer "+token)
	cancelRec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(cancelRec, cancelReq)
	require.Equal(t, http.StatusOK, cancelRec.Code)
	require.Equal(t, "cancelled", e.getJob(t, job.ID).Status)

	tasksBefore, err := e.q.ListTasksByJob(t.Context(), job.ID)
	require.NoError(t, err)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "job was cancelled")

	assert.Equal(t, "cancelled", e.getJob(t, job.ID).Status,
		"RecomputeJobStatus is cancelled-blind; this endpoint must never reach it from cancelled")
	for _, was := range tasksBefore {
		got := e.getTask(t, was.ID)
		assert.Equal(t, was.Status, got.Status)
		assert.Equal(t, was.AssignmentEpoch, got.AssignmentEpoch)
	}
}

func TestRetryJob_NonTerminalJob_409(t *testing.T) {
	for _, tc := range []struct{ name, taskStatus, jobStatus string }{
		{"pending", "pending", "pending"},
		{"running", "running", "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRetryEnv(t)
			owner := createTestUser(t, e.q, "Owner",
				fmt.Sprintf("retry-nonterm-%s@example.com", tc.name), false)
			token := createTestToken(t, e.q, owner.ID)
			job := e.job(t, owner.ID)
			task := e.task(t, job, "t", tc.taskStatus)
			if tc.taskStatus != "pending" {
				require.Equal(t, tc.jobStatus, e.recompute(t, job))
			}

			rec := e.do(t, token, uuidString(job.ID), "task=all")
			require.Equal(t, http.StatusConflict, rec.Code)
			assert.Contains(t, errBody(t, rec), "job is not finished")

			got := e.getTask(t, task.ID)
			assert.Equal(t, tc.taskStatus, got.Status)
			assert.Equal(t, task.AssignmentEpoch, got.AssignmentEpoch)
		})
	}
}

// Case A. The item leaves the zero-match outcome open; the spec chose 409, so a
// client never gets a success toast and three refetches for a job that did not
// change.
func TestRetryJob_ZeroMatched_409_CaseA(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-zero@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	e.task(t, job, "t", "done")
	require.Equal(t, "done", e.recompute(t, job))
	before := e.getJob(t, job.ID)

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "no failed or timed_out tasks")

	after := e.getJob(t, job.ID)
	assert.Equal(t, "done", after.Status)
	assert.Equal(t, before.UpdatedAt.Time, after.UpdatedAt.Time,
		"a refused retry must not stamp updated_at, which the 24h stats buckets window on")
}

// Case B. The selection is non-empty but the guard blocks everything.
func TestRetryJob_DependentsBlocked_409_CaseB_NothingApplied(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-blocked@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	tsk := e.task(t, job, "t", "failed")
	dep := e.task(t, job, "d", "done")
	require.NoError(t, e.q.CreateTaskDependency(t.Context(), store.CreateTaskDependencyParams{
		TaskID: dep.ID, DependsOnTaskID: tsk.ID,
	}))
	require.Equal(t, "failed", e.recompute(t, job))

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "dependents that have already run")

	assert.Equal(t, "failed", e.getTask(t, tsk.ID).Status)
	assert.Equal(t, tsk.AssignmentEpoch, e.getTask(t, tsk.ID).AssignmentEpoch)
	assert.Equal(t, "failed", e.getJob(t, job.ID).Status)
}

// Case C, forced deterministically. See installSkipUpdateTrigger: racing two
// retries cannot produce this, because the job row lock serializes them.
func TestRetryJob_PartialMatch_409_CaseC_RollbackIsTotal(t *testing.T) {
	e := newRetryEnv(t)
	owner := createTestUser(t, e.q, "Owner", "retry-partial@example.com", false)
	token := createTestToken(t, e.q, owner.ID)
	job := e.job(t, owner.ID)
	keep := e.task(t, job, "t-keep", "failed")
	skip := e.task(t, job, "t-skip", "failed")
	require.Equal(t, "failed", e.recompute(t, job))
	before := e.getJob(t, job.ID)

	installSkipUpdateTrigger(t, e.pool, "t-skip")

	rec := e.do(t, token, uuidString(job.ID), "task=failed")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, errBody(t, rec), "the job changed while the retry was in flight")

	// Rollback is TOTAL: the row that did update must be back where it started.
	got := e.getTask(t, keep.ID)
	assert.Equal(t, "failed", got.Status, "a partial retry must never be committed")
	assert.Equal(t, keep.AssignmentEpoch, got.AssignmentEpoch,
		"the rolled-back row must not keep its epoch bump")
	assert.Equal(t, "failed", e.getTask(t, skip.ID).Status)
	after := e.getJob(t, job.ID)
	assert.Equal(t, "failed", after.Status)
	assert.Equal(t, before.UpdatedAt.Time, after.UpdatedAt.Time)
}
