//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/store"
)

var reservationsSortKeys = []string{
	"-created_at", "created_at",
	"-name", "name",
	"-starts_at", "starts_at",
	"-ends_at", "ends_at",
}

// seedReservation inserts a reservation directly via the pool so we can control
// all fields including nullable starts_at and ends_at.
func seedReservation(t *testing.T, pool *pgxpool.Pool, name string, startsAt, endsAt *time.Time) string {
	t.Helper()
	var id string
	var err error
	if startsAt != nil && endsAt != nil {
		err = pool.QueryRow(t.Context(),
			`INSERT INTO reservations (name, selector, worker_ids, starts_at, ends_at)
			 VALUES ($1, '{}', '{}', $2, $3)
			 RETURNING id`,
			name, startsAt, endsAt,
		).Scan(&id)
	} else if startsAt != nil {
		err = pool.QueryRow(t.Context(),
			`INSERT INTO reservations (name, selector, worker_ids, starts_at)
			 VALUES ($1, '{}', '{}', $2)
			 RETURNING id`,
			name, startsAt,
		).Scan(&id)
	} else if endsAt != nil {
		err = pool.QueryRow(t.Context(),
			`INSERT INTO reservations (name, selector, worker_ids, ends_at)
			 VALUES ($1, '{}', '{}', $2)
			 RETURNING id`,
			name, endsAt,
		).Scan(&id)
	} else {
		err = pool.QueryRow(t.Context(),
			`INSERT INTO reservations (name, selector, worker_ids)
			 VALUES ($1, '{}', '{}')
			 RETURNING id`,
			name,
		).Scan(&id)
	}
	require.NoError(t, err, "seedReservation %s", name)
	return id
}

// seedReservationsForSort seeds 8 reservations with varied names and nullable
// starts_at/ends_at values. 5 have non-null timestamps; 3 have at least one NULL.
func seedReservationsForSort(t *testing.T, q *store.Queries, pool *pgxpool.Pool) {
	t.Helper()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	ts1 := base.Add(1 * time.Hour)
	ts2 := base.Add(2 * time.Hour)
	ts3 := base.Add(3 * time.Hour)
	ts4 := base.Add(4 * time.Hour)
	ts5 := base.Add(5 * time.Hour)
	ts6 := base.Add(6 * time.Hour)
	ts7 := base.Add(7 * time.Hour)
	ts8 := base.Add(8 * time.Hour)
	ts9 := base.Add(9 * time.Hour)
	ts10 := base.Add(10 * time.Hour)

	type r struct {
		name     string
		startsAt *time.Time
		endsAt   *time.Time
	}
	reservations := []r{
		// 5 with non-null starts_at and ends_at.
		{"alpha", &ts1, &ts6},
		{"bravo", &ts2, &ts7},
		{"charlie", &ts3, &ts8},
		{"delta", &ts4, &ts9},
		{"echo", &ts5, &ts10},
		// 3 with NULL starts_at and/or NULL ends_at.
		{"foxtrot", nil, &ts6},  // NULL starts_at, non-null ends_at
		{"golf", &ts3, nil},     // non-null starts_at, NULL ends_at
		{"hotel", nil, nil},     // both NULL
	}

	for _, res := range reservations {
		seedReservation(t, pool, res.name, res.startsAt, res.endsAt)
		time.Sleep(2 * time.Millisecond) // ensure distinct created_at
	}
}

func getReservationsPage(t *testing.T, srv interface {
	Handler() http.Handler
}, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/reservations"
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

// assertReservationsSorted confirms the field implied by sortKey is monotonic.
// For nullable timestamp keys (starts_at, ends_at), null values sort to one end
// depending on direction (DESC NULLS LAST / ASC NULLS FIRST).
func assertReservationsSorted(t *testing.T, items []map[string]any, sortKey string) {
	t.Helper()
	desc := len(sortKey) > 0 && sortKey[0] == '-'
	key := sortKey
	if desc {
		key = sortKey[1:]
	}

	switch key {
	case "starts_at", "ends_at":
		// NULL-aware sort check.
		seenNull := false
		var prevNonNull string
		for i, it := range items {
			v := it[key]
			if v == nil {
				seenNull = true
				if desc {
					// DESC NULLS LAST: nulls come after non-nulls.
					continue
				}
				// ASC NULLS FIRST: nulls come before non-nulls, ok.
				continue
			}
			str, _ := v.(string)
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

	default:
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
}

func TestListReservations_Sort_OrderingAcrossKeys(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Alice", "rsort-alice@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedReservationsForSort(t, q, pool)

	for _, sortKey := range reservationsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			code, p := getReservationsPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code, "%s: status", sortKey)
			require.Len(t, p.Items, 8, "%s: item count", sortKey)
			assertReservationsSorted(t, p.Items, sortKey)
		})
	}
}

func TestListReservations_Sort_PaginationAcrossPages(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Bob", "rpage-bob@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedReservationsForSort(t, q, pool)

	for _, sortKey := range reservationsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline.
			code, single := getReservationsPage(t, srv, token, "sort="+sortKey+"&limit=50")
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
				code, p := getReservationsPage(t, srv, token, qs)
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

func TestListReservations_Sort_CursorMismatchRejected(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Carol", "rmismatch-carol@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedReservationsForSort(t, q, pool)

	// Get a cursor under sort=-name.
	code, p := getReservationsPage(t, srv, token, "sort=-name&limit=3")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "expected has-more cursor")

	// Resend under sort=-created_at -- must 400.
	code, _ = getReservationsPage(t, srv, token, "sort=-created_at&limit=3&cursor="+p.NextCursor)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestListReservations_Sort_NullBoundaryPagination_Starts(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Dan", "rnull-starts-dan@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedReservationsForSort(t, q, pool)

	for _, sortKey := range []string{"-starts_at", "starts_at"} {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline (all 8).
			code, single := getReservationsPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, single.Items, 8, "baseline must return all 8 reservations")

			// Walk with page size 3 to force crossing the NULL boundary.
			var paged []map[string]any
			seen := map[string]bool{}
			cursor := ""
			for i := 0; i < 6; i++ { // safety bound
				qs := fmt.Sprintf("sort=%s&limit=3", sortKey)
				if cursor != "" {
					qs += "&cursor=" + cursor
				}
				code, p := getReservationsPage(t, srv, token, qs)
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

			require.Equal(t, 8, len(paged),
				"paged walk must return all 8 reservations for sort=%s (got %d)", sortKey, len(paged))

			// Verify ordering matches the single-page baseline.
			for i := range single.Items {
				assert.Equal(t, single.Items[i]["id"], paged[i]["id"],
					"row %d differs for sort=%s", i, sortKey)
			}

			// Verify NULL placement.
			// Seed has 2 reservations with NULL starts_at: "foxtrot" and "hotel".
			desc := len(sortKey) > 0 && sortKey[0] == '-'
			nullCount := 0
			for _, item := range single.Items {
				if item["starts_at"] == nil {
					nullCount++
				}
			}
			require.Equal(t, 2, nullCount, "expected 2 NULL starts_at values")
			if desc {
				// DESC NULLS LAST: last 2 should be NULL.
				for i := 6; i < 8; i++ {
					assert.Nil(t, single.Items[i]["starts_at"],
						"item %d should have null starts_at under DESC NULLS LAST", i)
				}
			} else {
				// ASC NULLS FIRST: first 2 should be NULL.
				for i := 0; i < 2; i++ {
					assert.Nil(t, single.Items[i]["starts_at"],
						"item %d should have null starts_at under ASC NULLS FIRST", i)
				}
			}
		})
	}
}

func TestListReservations_Sort_NullBoundaryPagination_Ends(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Eve", "rnull-ends-eve@test.com", true)
	token := createTestToken(t, q, user.ID)

	seedReservationsForSort(t, q, pool)

	for _, sortKey := range []string{"-ends_at", "ends_at"} {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline (all 8).
			code, single := getReservationsPage(t, srv, token, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, single.Items, 8, "baseline must return all 8 reservations")

			// Walk with page size 3 to force crossing the NULL boundary.
			var paged []map[string]any
			seen := map[string]bool{}
			cursor := ""
			for i := 0; i < 6; i++ { // safety bound
				qs := fmt.Sprintf("sort=%s&limit=3", sortKey)
				if cursor != "" {
					qs += "&cursor=" + cursor
				}
				code, p := getReservationsPage(t, srv, token, qs)
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

			require.Equal(t, 8, len(paged),
				"paged walk must return all 8 reservations for sort=%s (got %d)", sortKey, len(paged))

			// Verify ordering matches the single-page baseline.
			for i := range single.Items {
				assert.Equal(t, single.Items[i]["id"], paged[i]["id"],
					"row %d differs for sort=%s", i, sortKey)
			}

			// Verify NULL placement.
			// Seed has 2 reservations with NULL ends_at: "golf" and "hotel".
			desc := len(sortKey) > 0 && sortKey[0] == '-'
			nullCount := 0
			for _, item := range single.Items {
				if item["ends_at"] == nil {
					nullCount++
				}
			}
			require.Equal(t, 2, nullCount, "expected 2 NULL ends_at values")
			if desc {
				// DESC NULLS LAST: last 2 should be NULL.
				for i := 6; i < 8; i++ {
					assert.Nil(t, single.Items[i]["ends_at"],
						"item %d should have null ends_at under DESC NULLS LAST", i)
				}
			} else {
				// ASC NULLS FIRST: first 2 should be NULL.
				for i := 0; i < 2; i++ {
					assert.Nil(t, single.Items[i]["ends_at"],
						"item %d should have null ends_at under ASC NULLS FIRST", i)
				}
			}
		})
	}
}
