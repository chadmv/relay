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
)

// scheduledJobsSortKeys enumerates every (key, direction) in ScheduledJobsSortSpec.
var scheduledJobsSortKeys = []string{
	"-created_at", "created_at",
	"-name", "name",
	"-next_run_at", "next_run_at",
	"-updated_at", "updated_at",
}

// seedScheduledJob inserts a scheduled job directly via the pool so we can
// control name, owner_id, next_run_at, and updated_at precisely.
func seedScheduledJob(t *testing.T, pool *pgxpool.Pool, name, ownerID string, nextRunAt, updatedAt time.Time) string {
	t.Helper()
	jobSpec := `{"name":"` + name + `-job","tasks":[{"name":"t","command":["echo","x"]}]}`
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs
		   (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at, updated_at)
		 VALUES ($1, $2::uuid, '@hourly', 'UTC', $3::jsonb, 'skip', true, $4, $5)
		 RETURNING id`,
		name, ownerID, jobSpec, nextRunAt, updatedAt,
	).Scan(&id)
	require.NoError(t, err, "seedScheduledJob %s", name)
	return id
}

func getScheduledJobsPage(t *testing.T, srv interface {
	Handler() http.Handler
}, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/scheduled-jobs"
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

// seedScheduledJobsForSort seeds 8 scheduled jobs with varied names, next_run_at,
// and updated_at values so that each sort key yields a distinct ordering.
// Names are reverse-alphabetical in creation order (hotel first, alpha last)
// so name-sort != created_at-sort.
func seedScheduledJobsForSort(t *testing.T, pool *pgxpool.Pool, ownerID string) {
	t.Helper()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	type s struct {
		name      string
		nextRunAt time.Time
		updatedAt time.Time
	}

	// Names in reverse-alphabetical order of creation so name-asc != created_at-asc.
	// next_run_at and updated_at are also varied to be distinct from created_at order.
	schedules := []s{
		{"sched-hotel", base.Add(8 * time.Hour), base.Add(7 * time.Hour)},
		{"sched-golf", base.Add(7 * time.Hour), base.Add(8 * time.Hour)},
		{"sched-foxtrot", base.Add(6 * time.Hour), base.Add(1 * time.Hour)},
		{"sched-echo", base.Add(5 * time.Hour), base.Add(2 * time.Hour)},
		{"sched-delta", base.Add(4 * time.Hour), base.Add(3 * time.Hour)},
		{"sched-charlie", base.Add(3 * time.Hour), base.Add(4 * time.Hour)},
		{"sched-bravo", base.Add(2 * time.Hour), base.Add(5 * time.Hour)},
		{"sched-alpha", base.Add(1 * time.Hour), base.Add(6 * time.Hour)},
	}

	for _, sc := range schedules {
		seedScheduledJob(t, pool, sc.name, ownerID, sc.nextRunAt, sc.updatedAt)
		time.Sleep(2 * time.Millisecond) // ensure distinct created_at
	}
}

func TestListScheduledJobs_Sort_OrderingAcrossKeys_Admin(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjsort-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	// Seed schedules under multiple owners.
	owner1 := createTestUser(t, q, "Owner1", "sjsort-owner1@test.com", false)
	owner2 := createTestUser(t, q, "Owner2", "sjsort-owner2@test.com", false)

	seedScheduledJobsForSort(t, pool, uuidString(owner1.ID))
	// Add 0 more from owner2 — the 8 seeded above are sufficient; the admin
	// path sees all of them regardless of owner.
	_ = owner2

	for _, sortKey := range scheduledJobsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			code, p := getScheduledJobsPage(t, srv, adminToken, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code, "sort=%s", sortKey)
			require.Len(t, p.Items, 8, "sort=%s: expected 8 items", sortKey)
			assertSorted(t, p.Items, sortKey)
		})
	}
}

func TestListScheduledJobs_Sort_OrderingAcrossKeys_OwnerScoped(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)

	// Two regular users.
	alice := createTestUser(t, q, "Alice", "sjscope-alice@test.com", false)
	aliceToken := createTestToken(t, q, alice.ID)
	bob := createTestUser(t, q, "Bob", "sjscope-bob@test.com", false)
	bobToken := createTestToken(t, q, bob.ID)

	// Seed 8 schedules for Alice and 2 for Bob.
	seedScheduledJobsForSort(t, pool, uuidString(alice.ID))
	seedScheduledJob(t, pool, "bob-sched-1", uuidString(bob.ID),
		time.Now().Add(1*time.Hour), time.Now())
	seedScheduledJob(t, pool, "bob-sched-2", uuidString(bob.ID),
		time.Now().Add(2*time.Hour), time.Now())

	for _, sortKey := range scheduledJobsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			// Alice should see exactly her 8 schedules.
			code, p := getScheduledJobsPage(t, srv, aliceToken, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code, "alice sort=%s", sortKey)
			require.Len(t, p.Items, 8, "alice sort=%s: expected 8 items, got %d", sortKey, len(p.Items))
			assertSorted(t, p.Items, sortKey)

			// Bob should see exactly his 2 schedules.
			code2, p2 := getScheduledJobsPage(t, srv, bobToken, "sort="+sortKey+"&limit=50")
			require.Equal(t, http.StatusOK, code2, "bob sort=%s", sortKey)
			require.Len(t, p2.Items, 2, "bob sort=%s: expected 2 items, got %d", sortKey, len(p2.Items))
		})
	}
}

func TestListScheduledJobs_Sort_PaginationAcrossPages_Admin(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjpage-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjpage-owner@test.com", false)

	seedScheduledJobsForSort(t, pool, uuidString(owner.ID))

	for _, sortKey := range scheduledJobsSortKeys {
		t.Run(sortKey, func(t *testing.T) {
			// Single-page baseline.
			code, single := getScheduledJobsPage(t, srv, adminToken, "sort="+sortKey+"&limit=50")
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
				code, p := getScheduledJobsPage(t, srv, adminToken, qs)
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

func TestListScheduledJobs_Sort_CursorMismatchRejected(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjmismatch-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjmismatch-owner@test.com", false)

	seedScheduledJobsForSort(t, pool, uuidString(owner.ID))

	// Get a cursor under sort=-name.
	code, p := getScheduledJobsPage(t, srv, adminToken, "sort=-name&limit=3")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "expected has-more cursor")

	// Resend under sort=-next_run_at -- must 400.
	code, _ = getScheduledJobsPage(t, srv, adminToken, "sort=-next_run_at&limit=3&cursor="+p.NextCursor)
	require.Equal(t, http.StatusBadRequest, code)
}
