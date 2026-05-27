//go:build integration

package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobsSortKeys enumerates every (key, direction) in the server's jobsSortSpec.
// New keys added to the allowlist must be added here.
var jobsSortKeys = []string{
	"-created_at", "created_at",
	"-name", "name",
	"-priority", "priority",
	"-status", "status",
	"-updated_at", "updated_at",
}

// submitJobWithFields is like submitJob but lets the caller pick priority.
// (status is server-set to "pending" at create time; we vary it later via DB.)
func submitJobWithFields(t *testing.T, srv interface{ Handler() http.Handler },
	token, name, priority string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"priority":%q,"tasks":[{"name":"t","command":["echo","x"]}]}`,
		name, priority)
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "submit %s: %s", name, rec.Body.String())
}

func TestListJobs_Sort_OrderingAcrossKeys(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Alice", "sort-alice@test.com", false)
	token := createTestToken(t, q, user.ID)

	// Seed 10 jobs with unique names and priorities chosen so the default
	// created_at order differs from each non-default sort key's order.
	// Names are descending in creation order so name-sort != created_at-sort.
	// Priorities cycle through all 4 values, giving unambiguous alphabetical
	// ordering when combined with unique names as tiebreaker.
	priorities := []string{"critical", "high", "low", "normal", "critical", "high", "low", "normal", "critical", "high"}
	for i := 0; i < 10; i++ {
		// job-09 is created first, job-00 last — so name-desc != created_at-desc.
		submitJobWithFields(t, srv, token, fmt.Sprintf("job-%02d", 9-i), priorities[i])
		time.Sleep(2 * time.Millisecond)
	}

	for _, sortKey := range jobsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			code, p := getJobsPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code, "%s", sortKey)
			require.Len(t, p.Items, 10)
			assertSorted(t, p.Items, sortKey)
		})
	}
}

func TestListJobs_Sort_PaginationAcrossPages(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Bob", "page-bob@test.com", false)
	token := createTestToken(t, q, user.ID)

	priorities := []string{"critical", "high", "low", "normal", "critical", "high", "low", "normal", "critical", "high"}
	for i := 0; i < 10; i++ {
		submitJobWithFields(t, srv, token, fmt.Sprintf("p-%02d", 9-i), priorities[i])
		time.Sleep(2 * time.Millisecond)
	}

	for _, sortKey := range jobsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline.
			code, single := getJobsPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, single.Items, 10)

			// Walk in pages of 3.
			var paged []map[string]any
			cursor := ""
			for i := 0; i < 5; i++ { // safety bound
				qs := "sort=" + sortKey + "&limit=3"
				if cursor != "" {
					qs += "&cursor=" + cursor
				}
				code, p := getJobsPage(t, srv, token, qs)
				require.Equal(t, http.StatusOK, code)
				paged = append(paged, p.Items...)
				if p.NextCursor == "" {
					break
				}
				cursor = p.NextCursor
			}

			require.Equal(t, len(single.Items), len(paged), "paged length mismatch for sort=%s", sortKey)
			for i := range single.Items {
				assert.Equal(t, single.Items[i]["id"], paged[i]["id"], "row %d differs for sort=%s", i, sortKey)
			}
		})
	}
}

func TestListJobs_Sort_CursorMismatchRejected(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Carol", "mismatch-carol@test.com", false)
	token := createTestToken(t, q, user.ID)

	for i := 0; i < 5; i++ {
		submitJobWithFields(t, srv, token, fmt.Sprintf("m-%02d", i), "normal")
	}

	// Get a cursor under sort=-name.
	code, p := getJobsPage(t, srv, token, "sort=-name&limit=2")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "expected has-more cursor")

	// Resend under sort=-priority -- must 400.
	code, _ = getJobsPage(t, srv, token, "sort=-priority&limit=2&cursor="+p.NextCursor)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestListJobs_Sort_FilterAndSortConflict(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Dan", "conflict-dan@test.com", false)
	token := createTestToken(t, q, user.ID)
	submitJob(t, srv, token, "x")

	// Combining ?sort= with ?status= must 400.
	code, _ := getJobsPage(t, srv, token, "sort=name&status=pending")
	require.Equal(t, http.StatusBadRequest, code)
}

// assertSorted confirms the field implied by sortKey is monotonic in the
// expected direction across items.
func assertSorted(t *testing.T, items []map[string]any, sortKey string) {
	t.Helper()
	desc := false
	key := sortKey
	if len(sortKey) > 0 && sortKey[0] == '-' {
		desc = true
		key = sortKey[1:]
	}
	values := make([]string, len(items))
	for i, it := range items {
		v, _ := it[key].(string)
		values[i] = v
	}
	for i := 1; i < len(values); i++ {
		if desc {
			assert.GreaterOrEqual(t, values[i-1], values[i], "sort=%s not monotonic at i=%d (%v vs %v)", sortKey, i, values[i-1], values[i])
		} else {
			assert.LessOrEqual(t, values[i-1], values[i], "sort=%s not monotonic at i=%d (%v vs %v)", sortKey, i, values[i-1], values[i])
		}
	}
}
