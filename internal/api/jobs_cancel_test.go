//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSender implements worker.Sender and records all messages sent to it.
type captureSender struct {
	mu   sync.Mutex
	sent []*relayv1.CoordinatorMessage
}

func (c *captureSender) Send(msg *relayv1.CoordinatorMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return nil
}

func (c *captureSender) snapshot() []*relayv1.CoordinatorMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*relayv1.CoordinatorMessage, len(c.sent))
	copy(out, c.sent)
	return out
}

// cancelTestEnv holds everything needed for cancel-job tests.
type cancelTestEnv struct {
	srv      *api.Server
	q        *store.Queries
	pool     *pgxpool.Pool
	cs       *captureSender
	workerID pgtype.UUID // the real DB worker UUID
}

// newCancelTestServer creates a real Postgres container, creates a worker row
// in the DB, registers a captureSender for that worker in the Registry, and
// returns the full test environment.
func newCancelTestServer(t *testing.T) *cancelTestEnv {
	t.Helper()
	ctx := t.Context()

	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	registry := worker.NewRegistry()

	// Insert a real worker so tasks can reference it via FK.
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name:     "test-worker",
		Hostname: "test-host",
		CpuCores: 4,
		RamGb:    8,
		GpuCount: 0,
		GpuModel: "",
		Os:       "linux",
	})
	require.NoError(t, err)

	cs := &captureSender{}
	workerIDStr := uuidString(w.ID)
	registry.Register(workerIDStr, cs)

	srv := api.New(pool, q, broker, registry, nil, 0, 0, 0, 0)
	return &cancelTestEnv{
		srv:      srv,
		q:        q,
		pool:     pool,
		cs:       cs,
		workerID: w.ID,
	}
}

// seedRunningTask creates a job and a task dispatched to env.workerID via
// ClaimTaskForWorker, which is how tasks reach a worker in production. The
// claim bumps assignment_epoch to 1, matching the state of any task that has
// ever been dispatched - the case the cancel handler must support.
// Returns the job ID string for use in URL paths.
func seedRunningTask(t *testing.T, env *cancelTestEnv, userID pgtype.UUID) string {
	t.Helper()
	ctx := t.Context()

	job, err := env.q.CreateJob(ctx, store.CreateJobParams{
		Name:        "cancel-test-job",
		Priority:    "normal",
		SubmittedBy: userID,
		Labels:      []byte("{}"),
	})
	require.NoError(t, err)

	task, err := env.q.CreateTask(ctx, store.CreateTaskParams{
		JobID:    job.ID,
		Name:     "cancel-test-task",
		Commands: []byte(`[["sleep","30"]]`),
		Env:      []byte("{}"),
		Requires: []byte("{}"),
		Retries:  0,
	})
	require.NoError(t, err)

	// Dispatch the task the way the scheduler does. This bumps assignment_epoch
	// to 1, so the cancel handler can no longer assume epoch 0.
	claimed, err := env.q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID:       task.ID,
		WorkerID: env.workerID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)

	return uuidString(job.ID)
}

func TestCancelJob_Default_SendsForceFalse(t *testing.T) {
	env := newCancelTestServer(t)

	user := createTestUser(t, env.q, "Alice", "cancel-default@example.com", false)
	token := createTestToken(t, env.q, user.ID)
	jobID := seedRunningTask(t, env, user.ID)

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	msgs := env.cs.snapshot()
	require.Len(t, msgs, 1)
	ct := msgs[0].GetCancelTask()
	require.NotNil(t, ct)
	assert.False(t, ct.Force)
}

func TestCancelJob_Force_SendsForceTrue(t *testing.T) {
	env := newCancelTestServer(t)

	user := createTestUser(t, env.q, "Alice", "cancel-force@example.com", false)
	token := createTestToken(t, env.q, user.ID)
	jobID := seedRunningTask(t, env, user.ID)

	req := httptest.NewRequest(http.MethodDelete, "/v1/jobs/"+jobID+"?force=true", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	msgs := env.cs.snapshot()
	require.Len(t, msgs, 1)
	ct := msgs[0].GetCancelTask()
	require.NotNil(t, ct)
	assert.True(t, ct.Force)
}

func TestCancelJob_Force_QueryParamParsing(t *testing.T) {
	cases := []struct {
		query     string
		wantForce bool
	}{
		{"force=true", true},
		{"force=1", true},
		{"force=TRUE", true},
		{"force=t", true},
		{"force=0", false},
		{"force=false", false},
		{"force=garbage", false},
		{"", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("query=%q", tc.query), func(t *testing.T) {
			env := newCancelTestServer(t)

			email := fmt.Sprintf("cancel-parse-%s@example.com", tc.query)
			user := createTestUser(t, env.q, "Bob", email, false)
			token := createTestToken(t, env.q, user.ID)
			jobID := seedRunningTask(t, env, user.ID)

			url := "/v1/jobs/" + jobID
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest(http.MethodDelete, url, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			env.srv.Handler().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)

			msgs := env.cs.snapshot()
			require.Len(t, msgs, 1)
			ct := msgs[0].GetCancelTask()
			require.NotNil(t, ct)
			assert.Equal(t, tc.wantForce, ct.Force, "query=%q", tc.query)

			// Confirm job is cancelled in the response.
			var resp map[string]any
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			assert.Equal(t, "cancelled", resp["status"])
		})
	}
}
