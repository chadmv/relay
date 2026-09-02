//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// submitTrivialJob creates a single-task job and returns its ID.
func submitTrivialJob(t *testing.T, srv *api.Server, token string) string {
	t.Helper()
	body := `{"name":"test-job","tasks":[{"name":"t","command":["echo","x"]}]}`
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "submitTrivialJob: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["id"].(string)
}

// firstTaskID returns the ID of the first task in the given job.
func firstTaskID(t *testing.T, srv *api.Server, token, jobID string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/jobs/"+jobID+"/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var tasks []map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&tasks))
	require.NotEmpty(t, tasks)
	return tasks[0]["id"].(string)
}

// seedLogRow inserts a single log row directly via the pool.
func seedLogRow(t *testing.T, pool *pgxpool.Pool, taskID, stream, content string) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO task_logs (task_id, stream, content) VALUES ($1, $2, $3)`,
		taskID, stream, content)
	require.NoError(t, err)
}

func newTestServerWithPool(t *testing.T) (*api.Server, *store.Queries, *pgxpool.Pool) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	registry := worker.NewRegistry()
	srv := api.New(pool, q, broker, registry, nil, 0, 0, 0, 0)
	return srv, q, pool
}

func TestTaskLogs_Pagination(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)

	user := createTestUser(t, q, "Alice", "alice@logs-test.com", false)
	token := createTestToken(t, q, user.ID)

	// Seed: create a job with one task and 5 log rows.
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	// Page 1: limit=2, no since_seq.
	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/tasks/%s/logs?limit=2", taskID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var page1 struct {
		Items []struct {
			Seq     int64  `json:"seq"`
			Stream  string `json:"stream"`
			Content string `json:"content"`
		} `json:"items"`
		NextSeq int64 `json:"next_seq"`
		Total   int64 `json:"total"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&page1))
	require.Len(t, page1.Items, 2)
	require.Equal(t, "line 0", page1.Items[0].Content)
	require.Equal(t, "line 1", page1.Items[1].Content)
	require.Equal(t, page1.Items[1].Seq, page1.NextSeq)
	require.Equal(t, int64(5), page1.Total)

	// Page 2: since_seq=NextSeq.
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/v1/tasks/%s/logs?limit=2&since_seq=%d", taskID, page1.NextSeq), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var page2 struct {
		Items []struct {
			Seq     int64  `json:"seq"`
			Content string `json:"content"`
		} `json:"items"`
		NextSeq int64 `json:"next_seq"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&page2))
	require.Len(t, page2.Items, 2)
	require.Equal(t, "line 2", page2.Items[0].Content)
	require.Equal(t, "line 3", page2.Items[1].Content)

	// Final page: drained.
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/v1/tasks/%s/logs?limit=2&since_seq=%d", taskID, page2.NextSeq), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var page3 struct {
		Items   []any `json:"items"`
		NextSeq int64 `json:"next_seq"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&page3))
	require.Len(t, page3.Items, 1) // 5 rows total - 4 returned = 1
	require.Equal(t, int64(0), page3.NextSeq)
}

func TestTaskLogs_LimitClamping(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)

	user := createTestUser(t, q, "Bob", "bob@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)

	// limit=0 → 400; limit=201 → 400; limit=-1 → 400; limit=abc → 400.
	for _, query := range []string{"limit=0", "limit=201", "limit=-1", "limit=abc"} {
		req := httptest.NewRequest("GET",
			fmt.Sprintf("/v1/tasks/%s/logs?%s", taskID, query), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code, "q=%s: body=%s", query, rr.Body.String())
		require.True(t, strings.Contains(rr.Body.String(), "limit"),
			"q=%s: expected 'limit' in error body, got: %s", query, rr.Body.String())
	}
}

// logsPageResp is a hand-written decode of the logs envelope. Its json tags are
// deliberately independent of anything in package api: a fixture or decoder
// derived from the production type agrees with it by construction and can never
// detect drift.
type logsPageResp struct {
	Items []struct {
		Seq       int64  `json:"seq"`
		Stream    string `json:"stream"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	} `json:"items"`
	NextSeq int64 `json:"next_seq"`
	PrevSeq int64 `json:"prev_seq"`
	Total   int64 `json:"total"`
}

func getLogs(t *testing.T, srv *api.Server, token, taskID, query string) (*httptest.ResponseRecorder, logsPageResp) {
	t.Helper()
	req := httptest.NewRequest("GET", fmt.Sprintf("/v1/tasks/%s/logs?%s", taskID, query), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var page logsPageResp
	if rr.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &page), "body=%s", rr.Body.String())
	}
	return rr, page
}

func logContents(p logsPageResp) []string {
	out := make([]string, len(p.Items))
	for i, it := range p.Items {
		out[i] = it.Content
	}
	return out
}

func TestTaskLogs_DescendingTailReturnsTheNewestPageInAscendingOrder(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Carol", "carol@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	rr, page := getLogs(t, srv, token, taskID, "order=desc&limit=2")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	// The exact ordered slice, not a length: order=desc selects the NEWEST rows
	// and the items inside the page are ASCENDING. A page returned descending
	// would still be length 2 and would still contain the right rows.
	require.Equal(t, []string{"line 3", "line 4"}, logContents(page))
	require.Equal(t, int64(5), page.Total)
}

func TestTaskLogs_CursorsAreDirectionExclusive(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Dan", "dan@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	// Descending, full page: prev_seq is the page's LOWEST seq, next_seq is 0.
	_, desc := getLogs(t, srv, token, taskID, "order=desc&limit=2")
	require.Len(t, desc.Items, 2)
	require.Equal(t, desc.Items[0].Seq, desc.PrevSeq)
	require.Equal(t, int64(0), desc.NextSeq)

	// Descending, short page: the beginning of the log has been reached.
	_, drained := getLogs(t, srv, token, taskID, "order=desc&limit=200")
	require.Len(t, drained.Items, 5)
	require.Equal(t, int64(0), drained.PrevSeq)
	require.Equal(t, int64(0), drained.NextSeq)

	// Ascending is unchanged and zeroes the descending cursor.
	_, asc := getLogs(t, srv, token, taskID, "limit=2")
	require.Equal(t, []string{"line 0", "line 1"}, logContents(asc))
	require.Equal(t, asc.Items[1].Seq, asc.NextSeq)
	require.Equal(t, int64(0), asc.PrevSeq)
}

func TestTaskLogs_NonContiguousSeqTailIsExactlyTheNewestRowsOfThatTask(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Nina", "nina@logs-test.com", false)
	token := createTestToken(t, q, user.ID)

	jobA := submitTrivialJob(t, srv, token)
	taskA := firstTaskID(t, srv, token, jobA)
	jobB := submitTrivialJob(t, srv, token)
	taskB := firstTaskID(t, srv, token, jobB)

	// task_logs.id is a table-wide BIGSERIAL consumed by every task logging
	// concurrently. Interleaving a second task's rows makes A's ids
	// non-contiguous, which is the whole reason "give me the last N" cannot be
	// derived client-side from total or from arithmetic on seq.
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskA, "stdout", fmt.Sprintf("a-%d", i))
		seedLogRow(t, pool, taskB, "stdout", fmt.Sprintf("b-%d", i))
	}

	_, page := getLogs(t, srv, token, taskA, "order=desc&limit=2")
	require.Equal(t, []string{"a-3", "a-4"}, logContents(page))
	require.Equal(t, int64(5), page.Total)
	require.Equal(t, page.Items[0].Seq, page.PrevSeq)
	// Non-vacuity: the two returned ids are genuinely NOT adjacent, so no
	// offset arithmetic on seq or on total could have produced this page.
	require.Greater(t, page.Items[1].Seq-page.Items[0].Seq, int64(1))
}

func TestTaskLogs_DescendingWalkEqualsTheForwardWalkWithNoGapAndNoDuplicate(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Ed", "ed@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	// Forward walk, limit 2, until next_seq is 0.
	var forward []string
	since := int64(0)
	for i := 0; i < 10; i++ {
		_, p := getLogs(t, srv, token, taskID, fmt.Sprintf("limit=2&since_seq=%d", since))
		forward = append(forward, logContents(p)...)
		if p.NextSeq == 0 {
			break
		}
		since = p.NextSeq
	}

	// Backwards walk, limit 2, until prev_seq is 0. Each page is prepended, so
	// the assembled slice is in the same order the forward walk produced.
	var backward []string
	before := int64(0)
	for i := 0; i < 10; i++ {
		query := "order=desc&limit=2"
		if before > 0 {
			query = fmt.Sprintf("order=desc&limit=2&before_seq=%d", before)
		}
		_, p := getLogs(t, srv, token, taskID, query)
		backward = append(logContents(p), backward...)
		if p.PrevSeq == 0 {
			break
		}
		before = p.PrevSeq
	}

	require.Equal(t, []string{"line 0", "line 1", "line 2", "line 3", "line 4"}, forward)
	require.Equal(t, forward, backward)
}

func TestTaskLogs_EnvelopeCarriesAllFourKeysOnAnEmptyLog(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Fay", "fay@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)

	for _, query := range []string{"limit=10", "order=desc&limit=10"} {
		rr, _ := getLogs(t, srv, token, taskID, query)
		require.Equal(t, http.StatusOK, rr.Code, "q=%s", query)

		// The RAW key set, not a decoded struct: a decoded page cannot tell a
		// present-and-zero key from a missing one.
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &keys), "q=%s", query)
		got := make([]string, 0, len(keys))
		for k := range keys {
			got = append(got, k)
		}
		require.ElementsMatch(t, []string{"items", "next_seq", "prev_seq", "total"}, got, "q=%s", query)
		require.Equal(t, "[]", string(keys["items"]), "q=%s: an empty page is [], never null", query)
		require.Equal(t, "0", string(keys["next_seq"]), "q=%s", query)
		require.Equal(t, "0", string(keys["prev_seq"]), "q=%s", query)
		require.Equal(t, "0", string(keys["total"]), "q=%s", query)
	}
}

func TestTaskLogs_DescValidationOverTheWire(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Gus", "gus@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)

	cases := []struct{ query, wantMsg string }{
		{"order=desc&since_seq=10", "since_seq is not valid with order=desc; use before_seq"},
		{"before_seq=10", "before_seq requires order=desc"},
		{"order=desc&before_seq=0", "before_seq must be a positive integer"},
		{"order=desc&before_seq=-1", "before_seq must be a positive integer"},
		{"order=DESC", "order must be asc or desc"},
		{"order=descending", "order must be asc or desc"},
	}
	for _, c := range cases {
		rr, _ := getLogs(t, srv, token, taskID, c.query)
		require.Equal(t, http.StatusBadRequest, rr.Code, "q=%s: body=%s", c.query, rr.Body.String())
		require.Contains(t, rr.Body.String(), c.wantMsg, "q=%s", c.query)
	}

	// Paired positive control on the same call path: the accepted spellings do
	// not 400, so the rejections above are not the endpoint rejecting
	// everything.
	for _, ok := range []string{"order=asc&since_seq=1", "order=desc&before_seq=1", "order=desc"} {
		rr, _ := getLogs(t, srv, token, taskID, ok)
		require.Equal(t, http.StatusOK, rr.Code, "q=%s: body=%s", ok, rr.Body.String())
	}
}

func TestTaskLogs_UnknownTaskIs404AheadOfParameterValidation(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Hal", "hal@logs-test.com", false)
	token := createTestToken(t, q, user.ID)

	// A well-formed UUID that names no task, plus a malformed order. The
	// existence check runs first, so this is a 404 and not a 400 - preserved
	// deliberately, because it is what the endpoint has always done.
	unknown := "11111111-2222-4333-8444-555555555555"
	rr, _ := getLogs(t, srv, token, unknown, "order=DESC&before_seq=0")
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), "task not found")
}

func TestTaskLogs_TailUsesTheSameAuthorizationAsTheForwardRead(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Ivy", "ivy@logs-test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)
	jobID := submitTrivialJob(t, srv, ownerToken)
	taskID := firstTaskID(t, srv, ownerToken, jobID)
	seedLogRow(t, pool, taskID, "stdout", "secret-ish output")

	// No token: 401 in BOTH directions. The tail is not a new capability, and
	// it must not be a cheaper one either.
	for _, query := range []string{"limit=10", "order=desc&limit=10"} {
		req := httptest.NewRequest("GET", fmt.Sprintf("/v1/tasks/%s/logs?%s", taskID, query), nil)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code, "q=%s", query)
	}

	// An ordinary non-admin, non-owner user succeeds in both directions: this
	// endpoint has no ownership check today and this slice deliberately does
	// not add or remove one.
	other := createTestUser(t, q, "Jo", "jo@logs-test.com", false)
	otherToken := createTestToken(t, q, other.ID)
	for _, query := range []string{"limit=10", "order=desc&limit=10"} {
		rr, page := getLogs(t, srv, otherToken, taskID, query)
		require.Equal(t, http.StatusOK, rr.Code, "q=%s: body=%s", query, rr.Body.String())
		require.Equal(t, []string{"secret-ish output"}, logContents(page), "q=%s", query)
	}
}

func TestTaskLogs_BeforePageIsAscendingAndExact(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Kim", "kim@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 7; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	// The walk test pins the assembled sequence, which a before-page returned
	// DESCENDING would still reproduce once each page is prepended. This pins
	// the order inside a single before-page directly.
	_, tail := getLogs(t, srv, token, taskID, "order=desc&limit=2")
	require.Equal(t, []string{"line 5", "line 6"}, logContents(tail))

	_, mid := getLogs(t, srv, token, taskID,
		fmt.Sprintf("order=desc&limit=2&before_seq=%d", tail.PrevSeq))
	require.Equal(t, []string{"line 3", "line 4"}, logContents(mid))
	require.Less(t, mid.Items[0].Seq, mid.Items[1].Seq, "items inside a before-page are ascending")
	require.Equal(t, mid.Items[0].Seq, mid.PrevSeq)
	require.Equal(t, int64(0), mid.NextSeq)
}

func TestTaskLogs_BeforeSeqAtAGapValueReturnsOnlyRowsStrictlyBelowIt(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Lee", "lee@logs-test.com", false)
	token := createTestToken(t, q, user.ID)

	jobA := submitTrivialJob(t, srv, token)
	taskA := firstTaskID(t, srv, token, jobA)
	jobB := submitTrivialJob(t, srv, token)
	taskB := firstTaskID(t, srv, token, jobB)

	for i := 0; i < 5; i++ {
		seedLogRow(t, pool, taskA, "stdout", fmt.Sprintf("a-%d", i))
		seedLogRow(t, pool, taskB, "stdout", fmt.Sprintf("b-%d", i))
	}

	// A cursor is compared against a table-wide sequence, so a client may hold a
	// seq that belongs to no row of the task it is paging - here another task's
	// id, which is exactly the shape a gap has. The predicate is a strict
	// inequality, not a lookup, so such a value is valid and selects the rows
	// below it rather than erroring or snapping to a neighbour.
	_, bAll := getLogs(t, srv, token, taskB, "limit=200")
	gap := bAll.Items[2].Seq

	_, aBelow := getLogs(t, srv, token, taskA, fmt.Sprintf("order=desc&limit=200&before_seq=%d", gap))
	require.Equal(t, []string{"a-0", "a-1", "a-2"}, logContents(aBelow))

	// Non-vacuity: the cursor really is absent from task A, so this cannot pass
	// by the value happening to be one of A's own ids.
	_, aAll := getLogs(t, srv, token, taskA, "limit=200")
	for _, it := range aAll.Items {
		require.NotEqual(t, gap, it.Seq, "before_seq must be a value task A does not own")
	}
}

func TestTaskLogs_ExactMultipleWalkEndsWithAnEmptyPageNotAShortOne(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Mo", "mo@logs-test.com", false)
	token := createTestToken(t, q, user.ID)
	jobID := submitTrivialJob(t, srv, token)
	taskID := firstTaskID(t, srv, token, jobID)
	for i := 0; i < 4; i++ {
		seedLogRow(t, pool, taskID, "stdout", fmt.Sprintf("line %d", i))
	}

	// 4 rows at limit=2: every page is FULL, so no page is ever short and the
	// walk can only terminate on the empty page. A client that stopped on a
	// short page would never stop here, which is why the contract is "stop when
	// the cursor is 0", not "stop when the page is short".
	_, p1 := getLogs(t, srv, token, taskID, "order=desc&limit=2")
	require.Equal(t, []string{"line 2", "line 3"}, logContents(p1))
	require.NotEqual(t, int64(0), p1.PrevSeq)

	_, p2 := getLogs(t, srv, token, taskID, fmt.Sprintf("order=desc&limit=2&before_seq=%d", p1.PrevSeq))
	require.Equal(t, []string{"line 0", "line 1"}, logContents(p2))
	require.NotEqual(t, int64(0), p2.PrevSeq, "a full final page still mints a cursor")

	rr3, p3 := getLogs(t, srv, token, taskID, fmt.Sprintf("order=desc&limit=2&before_seq=%d", p2.PrevSeq))
	require.Equal(t, http.StatusOK, rr3.Code, rr3.Body.String())
	require.Empty(t, p3.Items)
	require.Equal(t, int64(0), p3.PrevSeq)

	// Raw, because a decoded empty page cannot tell [] from null and the SPA
	// iterates this key on every page of the walk, including the last.
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr3.Body.Bytes(), &keys))
	require.Equal(t, "[]", string(keys["items"]), "the terminating page is [], never null")
	require.Equal(t, "0", string(keys["prev_seq"]))
}
