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
	assert.EqualValues(t, 2, p.Total,
		"total slices the same partition as items; a count that disagrees shows an operator a "+
			"fraction the rows underneath it contradict")
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

	// GetWorker runs BEFORE parsePage, so the resource's existence is answered
	// first. Swapping the two makes this a 400 and tells a caller the worker
	// might exist.
	code, _ = getWorkerTasks(t, srv, token, "3f7c1b6e-0000-4000-8000-000000000000", "limit=0")
	assert.Equal(t, http.StatusNotFound, code,
		"an unknown worker with a bad limit is a 404, not a 400")
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

// job_name comes from a second statement over the page's job ids rather than a
// JOIN, so two tasks of the same job must both carry it.
func TestListWorkerTasks_CarriesTheJobName(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "JN", "jobname@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	shared := seedJob(t, pool, user, "nightly-render")
	other := seedJob(t, pool, user, "smoke")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, shared, workerID, "shot-1", "running", &at, &at)
	seedWorkerTask(t, pool, shared, workerID, "shot-2", "dispatched", &at, nil)
	seedWorkerTask(t, pool, other, workerID, "smoke-1", "running", &at, &at)

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 3)

	byName := map[string]string{}
	for _, it := range p.Items {
		byName[it["name"].(string)] = it["job_name"].(string)
	}
	assert.Equal(t, "nightly-render", byName["shot-1"])
	assert.Equal(t, "nightly-render", byName["shot-2"])
	assert.Equal(t, "smoke", byName["smoke-1"])
}

// Order is assigned_at DESC with id DESC as the tiebreak. uuid comparison in
// Postgres is bytewise, and the canonical text form is those bytes in hex, so
// sorting the two ids as strings agrees with the SQL.
func TestListWorkerTasks_OrdersByAssignedAtDescThenIDDesc(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Ord", "order@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	newest := base.Add(2 * time.Hour)
	middle := base.Add(time.Hour)

	seedWorkerTask(t, pool, jobID, workerID, "newest", "running", &newest, &newest)
	seedWorkerTask(t, pool, jobID, workerID, "middle", "running", &middle, &middle)
	tieA := seedWorkerTask(t, pool, jobID, workerID, "tie-a", "dispatched", &base, nil)
	tieB := seedWorkerTask(t, pool, jobID, workerID, "tie-b", "dispatched", &base, nil)

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 4)
	assert.Equal(t, []string{"newest", "middle"}, names(p)[:2])

	firstTie, secondTie := "tie-a", "tie-b"
	if tieB > tieA {
		firstTie, secondTie = "tie-b", "tie-a"
	}
	assert.Equal(t, []string{firstTie, secondTie}, names(p)[2:],
		"equal assigned_at must break on id DESC")
}

// Crosses a REAL page boundary: limit=1 over three rows, following next_cursor,
// asserting the union is the full set with no duplicate and no skip.
func TestListWorkerTasks_PagesAcrossARealBoundary(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Pg", "paging@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	for i, n := range []string{"a", "b", "c"} {
		ts := base.Add(time.Duration(i) * time.Hour)
		seedWorkerTask(t, pool, jobID, workerID, n, "running", &ts, &ts)
	}

	var seen []string
	cursor := ""
	for i := 0; i < 4; i++ {
		query := "limit=1"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		code, p := getWorkerTasks(t, srv, token, workerID, query)
		require.Equal(t, http.StatusOK, code)
		seen = append(seen, names(p)...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	assert.Equal(t, []string{"c", "b", "a"}, seen, "no duplicate and no skip across the boundary")
}

// total is the ACTIVE count, on every page: not the worker's total task count
// and not a table count.
func TestListWorkerTasks_TotalIsTheActiveCount(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Tot", "total@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, workerID, "live-1", "running", &base, &base)
	seedWorkerTask(t, pool, jobID, workerID, "live-2", "dispatched", &base, nil)
	for i, status := range []string{"done", "failed", "timed_out"} {
		ts := base.Add(time.Duration(i) * time.Minute)
		seedWorkerTask(t, pool, jobID, workerID, "old-"+status, status, &ts, &ts)
	}

	code, p := getWorkerTasks(t, srv, token, workerID, "limit=1")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	assert.EqualValues(t, 2, p.Total, "total is the active count, not the row count of this page")

	code, p = getWorkerTasks(t, srv, token, workerID, fmt.Sprintf("limit=1&cursor=%s", p.NextCursor))
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 2, p.Total, "and it is the same on every page")
}

// workerTaskWireKeys is every key this endpoint may put on a task item, written
// out as a literal rather than derived from the Go type: a set derived from the
// type under test agrees with it by construction. workerTaskResponse embeds
// taskResponse so that new task fields arrive for free, which is exactly why the
// wire needs a closed set - a field added to taskResponse ships here with a
// zero-line diff in workers.go.
var workerTaskWireKeys = []string{
	"id", "name", "status", "commands", "env", "requires", "timeout_seconds",
	"retries", "retry_count", "depends_on", "worker_id",
	"job_id", "job_name", "assigned_at", "started_at",
}

// The subset of workerTaskWireKeys carrying no omitempty, so they are on the
// wire for every row. depends_on, worker_id, assigned_at and started_at are the
// omitempty keys and are deliberately absent from this list.
var workerTaskAlwaysPresentKeys = []string{
	"id", "name", "status", "commands", "env", "requires", "timeout_seconds",
	"retries", "retry_count", "job_id", "job_name",
}

// assignment_epoch is a fence token and appears nowhere. Decoded into
// map[string]any so this sees the WIRE, not the struct definition, and asserted
// as a CLOSED set so a new field on the embedded taskResponse turns it RED.
func TestListWorkerTasks_DoesNotExposeAssignmentEpoch(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Ep", "epoch@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, workerID, "shot-1", "running", &at, &at)

	req := httptest.NewRequest("GET", "/v1/workers/"+workerID+"/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.NotContains(t, envelope, "assignment_epoch")
	items, ok := envelope["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1, "control: the assertion below must have a row to inspect")
	for _, raw := range items {
		item := raw.(map[string]any)
		assert.NotContains(t, item, "assignment_epoch",
			"assignment_epoch is a fence token; publishing (task id, epoch) pairs for a named worker "+
				"hands out both values a forged status update would otherwise have to guess")
		for k := range item {
			assert.Contains(t, workerTaskWireKeys, k,
				"unexpected key %q on the wire - if this is a new taskResponse field, decide whether it "+
					"belongs on a per-worker view before widening workerTaskWireKeys", k)
		}
		for _, k := range workerTaskAlwaysPresentKeys {
			assert.Contains(t, item, k, "key %q carries no omitempty and must be present", k)
		}
	}
}

// A dispatched row mid-workspace-sync has no started_at. It must still be
// returned, and the key must be ABSENT rather than a zero timestamp.
func TestListWorkerTasks_ADispatchedTaskWithNoStartTimeIsReturned(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Sync", "sync@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, workerID, "sync-depot", "dispatched", &at, nil)

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	assert.Equal(t, "sync-depot", p.Items[0]["name"])
	assert.NotContains(t, p.Items[0], "started_at", "an absent start time must be absent, not a zero time")
	assert.Contains(t, p.Items[0], "assigned_at", "control: the sibling timestamp IS present")
}

// A NULL assigned_at is reachable only by direct SQL here, because
// ClaimTaskForWorker stamps the column in the same statement that assigns the
// task. The column is nevertheless nullable, so the query carries a NULLS LAST
// branch and a cursor arm for it, and this is what exercises them: the walk uses
// TWO null rows so a cursor is actually ISSUED from a null-valued row, which is
// the only way into the cursor_is_null predicate and the (*time.Time)(nil) row
// key. One null row would order correctly and never reach that arm.
func TestListWorkerTasks_PagesThroughTheNullAssignedAtTail(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Null", "nulltail@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-a", "online", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	newer := base.Add(time.Hour)
	seedWorkerTask(t, pool, jobID, workerID, "ts-newer", "running", &newer, &newer)
	seedWorkerTask(t, pool, jobID, workerID, "ts-older", "running", &base, &base)
	seedWorkerTask(t, pool, jobID, workerID, "null-a", "dispatched", nil, nil)
	seedWorkerTask(t, pool, jobID, workerID, "null-b", "dispatched", nil, nil)

	var seen []string
	cursor := ""
	lastCursor := "unset"
	for i := 0; i < 6; i++ {
		query := "limit=1"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		code, p := getWorkerTasks(t, srv, token, workerID, query)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, p.Items, 1, "limit=1 must return exactly one row while rows remain")
		assert.EqualValues(t, 4, p.Total, "total is the whole active set on every page")
		seen = append(seen, names(p)...)
		lastCursor = p.NextCursor
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	assert.Equal(t, "", lastCursor, "the walk must terminate with an empty next_cursor")
	require.Len(t, seen, 4, "gapless and duplicate-free: every row exactly once")
	assert.Equal(t, []string{"ts-newer", "ts-older"}, seen[:2],
		"timestamped rows come first, newest assignment first")
	assert.ElementsMatch(t, []string{"null-a", "null-b"}, seen[2:],
		"the NULL assigned_at rows are the tail, after every timestamped row")
}

// The handler's comment claims a revoked worker is not a 404 here, matching
// GET /v1/workers/{id}. GetWorker has no status filter, so nothing but this
// pins it - and a revoked worker is exactly the one an operator is trying to
// look at when they want to know what it was still holding.
func TestListWorkerTasks_RevokedWorkerStillReturnsItsTasks(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Rev", "revoked@tasks-test.com", false)
	token := createTestToken(t, q, user.ID)
	workerID := seedWorker(t, pool, "rig-gone", "revoked", nil)
	jobID := seedJob(t, pool, user, "nightly-render")

	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	seedWorkerTask(t, pool, jobID, workerID, "still-held", "running", &at, &at)

	code, p := getWorkerTasks(t, srv, token, workerID, "")
	require.Equal(t, http.StatusOK, code, "a revoked worker is a real row, not a 404")
	assert.Equal(t, []string{"still-held"}, names(p))
	assert.EqualValues(t, 1, p.Total)
}
