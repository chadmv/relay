//go:build integration

package api_test

import (
	"net/http"
	"sort"
	"testing"
	"time"

	"relay/internal/api"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scheduleFilterArms is DERIVED FROM api.ScheduledJobsSortSpec.Keys, not written
// out. A sort key added without its filter arms turns this test RED instead of
// shipping one silently unfiltered ordering.
func scheduleFilterArms(t *testing.T) []string {
	t.Helper()
	keys := make([]string, 0, len(api.ScheduledJobsSortSpec.Keys))
	for k := range api.ScheduledJobsSortSpec.Keys {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic subtest order
	arms := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		arms = append(arms, k, "-"+k)
	}
	require.Len(t, arms, 2*len(api.ScheduledJobsSortSpec.Keys))
	return arms
}

// seedFilterSchedule inserts one schedule with an explicit enabled flag and cron
// expression, neither of which seedScheduledJob exposes.
func seedFilterSchedule(t *testing.T, pool *pgxpool.Pool, name, ownerID, cronExpr string, enabled bool) string {
	t.Helper()
	jobSpec := `{"name":"` + name + `-job","tasks":[{"name":"t","command":["echo","x"]}]}`
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs
		   (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at, updated_at)
		 VALUES ($1, $2::uuid, $3, 'UTC', $4::jsonb, 'skip', $5, $6, $7)
		 RETURNING id`,
		name, ownerID, cronExpr, jobSpec, enabled, base, base,
	).Scan(&id)
	require.NoError(t, err, "seedFilterSchedule %s", name)
	return id
}

// TestListScheduledJobs_FilterArms_FirstPage is the anti-drift guard.
//
// EVERY ARM IS EXERCISED ON THE FIRST PAGE, WITH NO CURSOR. That is the input
// that discriminates the parenthesisation defect: with a cursor present an
// unparenthesised `NOT cursor_set OR keyset AND filter` behaves correctly, so a
// test that walks to page two passes against the bug.
//
// Two schedules, distinguishable on BOTH filter axes at once. Admin and owner
// scope are separate subtests because they run separate statements.
func TestListScheduledJobs_FilterArms_FirstPage(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjfilter-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjfilter-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	keepID := seedFilterSchedule(t, pool, "keeper-nightly", uuidString(owner.ID), "@daily", true)
	seedFilterSchedule(t, pool, "other-weekly", uuidString(owner.ID), "0 4 * * 1", false)

	scopes := []struct {
		name  string
		token string
	}{
		{"admin", adminToken},
		{"owner", ownerToken},
	}
	filters := []struct {
		name  string
		query string
	}{
		{"enabled", "enabled=true"},
		{"q_name", "q=keeper"},
		{"both", "enabled=true&q=keeper"},
	}

	for _, sc := range scopes {
		for _, arm := range scheduleFilterArms(t) {
			for _, f := range filters {
				t.Run(sc.name+"/"+arm+"/"+f.name, func(t *testing.T) {
					code, p := getScheduledJobsPage(t, srv, sc.token, "sort="+arm+"&"+f.query)
					require.Equal(t, http.StatusOK, code)
					require.Len(t, p.Items, 1,
						"sort=%s %s on the FIRST PAGE must return exactly the matching row; "+
							"two rows means the filter was dropped, which is the unparenthesised "+
							"cursor disjunction", arm, f.query)
					assert.Equal(t, keepID, p.Items[0]["id"])
					assert.Equal(t, int64(1), p.Total,
						"total must count the filtered set, not the table")
				})
			}
		}
	}
}
