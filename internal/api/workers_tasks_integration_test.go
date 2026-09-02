//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedJob inserts a job directly so tests control its name.
func seedJob(t *testing.T, pool *pgxpool.Pool, owner store.User, name string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, priority, submitted_by, labels)
		 VALUES ($1, 'normal', $2, '{}'::jsonb) RETURNING id`,
		name, owner.ID,
	).Scan(&id))
	return id
}

// seedWorkerTask inserts one task row in a chosen state. Direct SQL, matching
// seedWorker and seedLogRow: this suite tests what the READ endpoint puts on the
// wire, and the write paths have their own store-level tests.
func seedWorkerTask(t *testing.T, pool *pgxpool.Pool, jobID, workerID, name, status string,
	assignedAt *time.Time, startedAt *time.Time) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO tasks (job_id, name, commands, env, requires, retries, status, worker_id, assigned_at, started_at)
		 VALUES ($1, $2, '[["echo","x"]]'::jsonb, '{}'::jsonb, '{}'::jsonb, 0, $3, $4, $5, $6)
		 RETURNING id`,
		jobID, name, status, workerID, assignedAt, startedAt,
	).Scan(&id))
	return id
}

func getWorkerTasks(t *testing.T, srv *api.Server, token, workerID, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/workers/" + workerID + "/tasks"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	}
	return rec.Code, resp
}

func names(p pageEnvelope[map[string]any]) []string {
	out := make([]string, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it["name"].(string))
	}
	return out
}

// Only the assignment partition. Seeds one task per status of the full six-value
// vocabulary and asserts exactly the two assigned ones come back.
func TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Part", "partition@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	for i, status := range []string{"pending", "dispatched", "running", "done", "failed", "timed_out"} {
		ts := at.Add(time.Duration(i) * time.Minute)
		seedWorkerTask(t, pool, jobID, workerID, "t-"+status, status, &ts, nil)
	}

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	assert.ElementsMatch(t, []string{"t-dispatched", "t-running"}, names(p))
}

// Rows are scoped to the path worker, in both directions.
func TestListWorkerTasks_DoesNotLeakAnotherWorkersTasks(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Scope", "scope@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	a := seedWorker(t, pool, "rig-a", "online", nil)
	b := seedWorker(t, pool, "rig-b", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, a, "on-a", "running", &at, &at)
	seedWorkerTask(t, pool, jobID, b, "on-b", "running", &at, &at)

	_, pa := getWorkerTasks(t, srv, token, a, "")
	assert.Equal(t, []string{"on-a"}, names(pa))
	_, pb := getWorkerTasks(t, srv, token, b, "")
	assert.Equal(t, []string{"on-b"}, names(pb))
}

// The posture pin. This goes RED if the route is ever wrapped in admin(...):
// both neighbours (GET /v1/workers/{id} and /metrics) are auth-only, and every
// task read route is auth-only under an explicit render-farm-semantics comment.
func TestListWorkerTasks_IsReadableByANonAdmin(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Plain", "plain@tasks-test.com", false)
	require.False(t, user.IsAdmin, "control: this user must not be an admin")
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)

	code, _ := getWorkerTasks(t, srv, token, workerID, "")
	assert.Equal(t, http.StatusOK, code)
}

func TestListWorkerTasks_RequiresAuthentication(t *testing.T) {
	srv, _, pool := newTestServerWithPool(t)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)

	code, _ := getWorkerTasks(t, srv, "", workerID, "")
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestListWorkerTasks_UnknownWorkerIs404AndMalformedIdIs400(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "NF", "notfound@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)

	code, _ := getWorkerTasks(t, srv, token, "3f7c1b6e-0000-4000-8000-000000000000", "")
	assert.Equal(t, http.StatusNotFound, code)

	code, _ = getWorkerTasks(t, srv, token, "not-a-uuid", "")
	assert.Equal(t, http.StatusBadRequest, code)
}

// limit is validated, not clamped, and the endpoint serves exactly one order.
func TestListWorkerTasks_RejectsBadLimitAndUnsupportedSort(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Lim", "limits@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)

	for _, query := range []string{"limit=0", "limit=201", "limit=abc", "sort=name", "sort=assigned_at", "cursor=zzz"} {
		code, _ := getWorkerTasks(t, srv, token, workerID, query)
		assert.Equal(t, http.StatusBadRequest, code, "query %q must be a 400", query)
	}

	code, _ := getWorkerTasks(t, srv, token, workerID, "limit=200&sort=-assigned_at")
	assert.Equal(t, http.StatusOK, code, "control: the supported limit and sort are accepted")
}
