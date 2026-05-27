//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workersSortKeys enumerates every (key, direction) in the server's workersSortSpec.
var workersSortKeys = []string{
	"-created_at", "created_at",
	"-name", "name",
	"-status", "status",
	"-last_seen_at", "last_seen_at",
}

// seedWorker inserts a worker directly via the pool so we can control
// name, status, and last_seen_at (which CreateWorker doesn't expose).
func seedWorker(t *testing.T, pool *pgxpool.Pool, name, status string, lastSeen *time.Time) string {
	t.Helper()
	var id string
	var err error
	if lastSeen != nil {
		err = pool.QueryRow(t.Context(),
			`INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, status, last_seen_at)
			 VALUES ($1, $2, 4, 16, 0, '', 'linux', $3, $4)
			 RETURNING id`,
			name, name+"-host", status, lastSeen,
		).Scan(&id)
	} else {
		err = pool.QueryRow(t.Context(),
			`INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, status)
			 VALUES ($1, $2, 4, 16, 0, '', 'linux', $3)
			 RETURNING id`,
			name, name+"-host", status,
		).Scan(&id)
	}
	require.NoError(t, err, "seedWorker %s", name)
	return id
}

func getWorkersPage(t *testing.T, srv interface {
	Handler() http.Handler
}, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/workers"
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

// seedWorkersForSort seeds 8 workers with varied names, statuses, and
// last_seen_at values (2 with NULL last_seen_at). Returns the pool used.
func seedWorkersForSort(t *testing.T, q *store.Queries, pool *pgxpool.Pool) {
	t.Helper()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// 6 workers with non-null last_seen_at, varied names and statuses.
	type w struct {
		name     string
		status   string
		lastSeen *time.Time
	}
	ts1 := base.Add(1 * time.Hour)
	ts2 := base.Add(2 * time.Hour)
	ts3 := base.Add(3 * time.Hour)
	ts4 := base.Add(4 * time.Hour)
	ts5 := base.Add(5 * time.Hour)
	ts6 := base.Add(6 * time.Hour)

	workers := []w{
		{"alpha", "online", &ts1},
		{"bravo", "offline", &ts2},
		{"charlie", "stale", &ts3},
		{"delta", "online", &ts4},
		{"echo", "offline", &ts5},
		{"foxtrot", "stale", &ts6},
		// 2 with NULL last_seen_at (never seen).
		{"golf", "offline", nil},
		{"hotel", "online", nil},
	}

	for _, w := range workers {
		seedWorker(t, pool, w.name, w.status, w.lastSeen)
		time.Sleep(2 * time.Millisecond) // ensure distinct created_at
	}
}

func TestListWorkers_Sort_OrderingAcrossKeys(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Alice", "wsort-alice@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedWorkersForSort(t, q, pool)

	for _, sortKey := range workersSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			code, p := getWorkersPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code, "%s: %s", sortKey, p)
			require.Len(t, p.Items, 8)
			assertWorkersSorted(t, p.Items, sortKey)
		})
	}
}

func TestListWorkers_Sort_PaginationAcrossPages(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Bob", "wpage-bob@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedWorkersForSort(t, q, pool)

	for _, sortKey := range workersSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline.
			code, single := getWorkersPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, single.Items, 8)

			// Walk in pages of 3.
			var paged []map[string]any
			cursor := ""
			for i := 0; i < 5; i++ { // safety bound
				qs := fmt.Sprintf("sort=%s&limit=3", sortKey)
				if cursor != "" {
					qs += "&cursor=" + cursor
				}
				code, p := getWorkersPage(t, srv, token, qs)
				require.Equal(t, http.StatusOK, code, "sort=%s page %d", sortKey, i)
				paged = append(paged, p.Items...)
				if p.NextCursor == "" {
					break
				}
				cursor = p.NextCursor
			}

			require.Equal(t, len(single.Items), len(paged), "paged length mismatch for sort=%s", sortKey)
			for i := range single.Items {
				assert.Equal(t, single.Items[i]["id"], paged[i]["id"],
					"row %d differs for sort=%s", i, sortKey)
			}
		})
	}
}

func TestListWorkers_Sort_CursorMismatchRejected(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Carol", "wmismatch-carol@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedWorkersForSort(t, q, pool)

	// Get a cursor under sort=-name.
	code, p := getWorkersPage(t, srv, token, "sort=-name&limit=3")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "expected has-more cursor")

	// Resend under sort=-status -- must 400.
	code, _ = getWorkersPage(t, srv, token, "sort=-status&limit=3&cursor="+p.NextCursor)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestListWorkers_Sort_NullBoundaryPagination(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Dan", "wnull-dan@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedWorkersForSort(t, q, pool)

	for _, sortKey := range []string{"-last_seen_at", "last_seen_at"} {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline (all 8).
			code, single := getWorkersPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, single.Items, 8, "baseline must return all 8 workers")

			// Walk with page size 3 to force crossing the NULL boundary.
			var paged []map[string]any
			seen := map[string]bool{}
			cursor := ""
			for i := 0; i < 6; i++ { // safety bound
				qs := fmt.Sprintf("sort=%s&limit=3", sortKey)
				if cursor != "" {
					qs += "&cursor=" + cursor
				}
				code, p := getWorkersPage(t, srv, token, qs)
				require.Equal(t, http.StatusOK, code, "sort=%s page %d", sortKey, i)
				for _, item := range p.Items {
					id := item["id"].(string)
					assert.False(t, seen[id], "duplicate id across pages: %s (sort=%s)", id, sortKey)
					seen[id] = true
				}
				paged = append(paged, p.Items...)
				if p.NextCursor == "" {
					break
				}
				cursor = p.NextCursor
			}

			require.Equal(t, 8, len(paged), "paged walk must return all 8 workers for sort=%s (got %d)", sortKey, len(paged))

			// Also verify ordering matches the single-page baseline.
			for i := range single.Items {
				assert.Equal(t, single.Items[i]["id"], paged[i]["id"],
					"row %d differs for sort=%s", i, sortKey)
			}

			// Verify NULL placement: under DESC NULLS LAST, nulls are at the end;
			// under ASC NULLS FIRST, nulls are at the start.
			desc := len(sortKey) > 0 && sortKey[0] == '-'
			if desc {
				// -last_seen_at: last 2 should have null last_seen_at
				for i := 6; i < 8; i++ {
					v := single.Items[i]["last_seen_at"]
					assert.Nil(t, v, "item %d should have null last_seen_at under DESC NULLS LAST", i)
				}
			} else {
				// last_seen_at: first 2 should have null last_seen_at
				for i := 0; i < 2; i++ {
					v := single.Items[i]["last_seen_at"]
					assert.Nil(t, v, "item %d should have null last_seen_at under ASC NULLS FIRST", i)
				}
			}
		})
	}
}

// assertWorkersSorted confirms the field implied by sortKey is monotonic.
// For last_seen_at (nullable), null values sort to one end depending on direction.
func assertWorkersSorted(t *testing.T, items []map[string]any, sortKey string) {
	t.Helper()
	desc := len(sortKey) > 0 && sortKey[0] == '-'
	key := sortKey
	if desc {
		key = sortKey[1:]
	}

	if key == "last_seen_at" {
		// NULL-aware sort check: DESC NULLS LAST means nulls come after non-nulls;
		// ASC NULLS FIRST means nulls come before non-nulls.
		// Extract string values; null JSON values come through as nil (not a string).
		// We represent nulls as "" for DESC (they should be at end) and check
		// separately that non-null values among themselves are ordered.
		seenNull := false
		var prevNonNull string
		for i, it := range items {
			v := it[key]
			if v == nil {
				seenNull = true
				if desc {
					// After seeing a null, any remaining nulls are OK.
					// Non-nulls should NOT follow nulls under DESC NULLS LAST.
					continue
				}
				// Under ASC NULLS FIRST, nulls come first so seenNull starting = fine.
				continue
			}
			str, _ := v.(string)
			if seenNull && !desc {
				// Under ASC NULLS FIRST, once we've seen nulls and then a non-null,
				// that's fine -- but we should not see a null again.
				_ = i
			}
			if seenNull && desc {
				t.Errorf("sort=%s: non-null value %q at index %d appears after NULL (DESC NULLS LAST violation)", sortKey, str, i)
			}
			if prevNonNull != "" {
				if desc {
					assert.GreaterOrEqual(t, prevNonNull, str, "sort=%s not monotonic at i=%d", sortKey, i)
				} else {
					assert.LessOrEqual(t, prevNonNull, str, "sort=%s not monotonic at i=%d", sortKey, i)
				}
			}
			prevNonNull = str
		}
		return
	}

	// Non-nullable keys: simple string comparison.
	values := make([]string, len(items))
	for i, it := range items {
		v, _ := it[key].(string)
		values[i] = v
	}
	for i := 1; i < len(values); i++ {
		if desc {
			assert.GreaterOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortKey, i, values[i-1], values[i])
		} else {
			assert.LessOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortKey, i, values[i-1], values[i])
		}
	}
}
