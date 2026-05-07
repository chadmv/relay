//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getJobsPage(t *testing.T, srv interface {
	Handler() http.Handler
}, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/jobs"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	}
	return rec.Code, resp
}

func submitJob(t *testing.T, srv interface {
	Handler() http.Handler
}, token, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"priority":"normal","tasks":[{"name":"t","command":["echo","x"]}]}`, name)
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "submit %s: %s", name, rec.Body.String())
}

func TestListJobs_PaginationDefaultLimit(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "page-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 75; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	code, page1 := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, page1.Items, 50, "default limit is 50")
	assert.NotEmpty(t, page1.NextCursor, "first page should signal more")
	assert.EqualValues(t, 75, page1.Total)

	code, page2 := getJobsPage(t, srv, token, "cursor="+page1.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, page2.Items, 25, "second page has the remainder")
	assert.Empty(t, page2.NextCursor, "second page is the last")
	assert.EqualValues(t, 75, page2.Total)

	seen := map[string]bool{}
	for _, j := range page1.Items {
		seen[j["id"].(string)] = true
	}
	for _, j := range page2.Items {
		id := j["id"].(string)
		assert.False(t, seen[id], "duplicate id across pages: %s", id)
		seen[id] = true
	}
	assert.Len(t, seen, 75)
}

func TestListJobs_StableUnderInsertMidPage(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "stable-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 75; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	code, page1 := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page1.Items, 50)
	require.NotEmpty(t, page1.NextCursor)

	submitJob(t, srv, token, "interloper")

	code, page2 := getJobsPage(t, srv, token, "cursor="+page1.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, page2.Items, 25, "page 2 must be stable: cursor bounds it to rows older than page 1")
	for _, j := range page2.Items {
		assert.NotEqual(t, "interloper", j["name"], "newly inserted row must not appear on page 2")
	}
}

func TestListJobs_LimitParam(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "limit-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 5; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	code, p := getJobsPage(t, srv, token, "limit=3")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 3)
	assert.NotEmpty(t, p.NextCursor)
	assert.EqualValues(t, 5, p.Total)
}

func TestListJobs_LimitOutOfRange(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "oor-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	cases := []string{"limit=0", "limit=201", "limit=-3", "limit=abc"}
	for _, qs := range cases {
		t.Run(qs, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/jobs?"+qs, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
		})
	}
}

func TestListJobs_BadCursor(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "bad-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	req := httptest.NewRequest("GET", "/v1/jobs?cursor=garbage!!!", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid cursor")
}

func TestListJobs_EmptyResult(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "empty-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	code, p := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, p.Items)
	assert.Empty(t, p.NextCursor, "empty result must yield empty cursor")
	assert.EqualValues(t, 0, p.Total)
}

func TestListJobs_StatusFilterPaginated(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "status-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 3; i++ {
		submitJob(t, srv, token, fmt.Sprintf("job-%02d", i))
		time.Sleep(time.Millisecond)
	}

	code, p := getJobsPage(t, srv, token, "status=running")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, p.Items)
	assert.EqualValues(t, 0, p.Total)

	code, p = getJobsPage(t, srv, token, "status=pending")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 3)
	assert.EqualValues(t, 3, p.Total)
}
