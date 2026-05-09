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
	_ = q // unused but available

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
