//go:build integration

package api_test

import (
	"net/http"
	"testing"
	"time"

	"relay/internal/api"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedFilterSchedule inserts one schedule with an explicit enabled flag and cron
// expression, neither of which seedScheduledJob exposes.
func seedFilterSchedule(t *testing.T, pool *pgxpool.Pool, name, ownerID, cronExpr string, enabled bool) string {
	t.Helper()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return seedScheduledJobFull(t, pool, name, ownerID, cronExpr, enabled, base, base)
}

// TestListScheduledJobs_FilterArms_FirstPage is the anti-drift guard.
//
// EVERY ARM IS EXERCISED ON THE FIRST PAGE, WITH NO CURSOR. That is the input
// that discriminates the parenthesisation defect: with a cursor present an
// unparenthesised `NOT cursor_set OR keyset AND filter` behaves correctly, so a
// test that walks to page two passes against the bug.
//
// EACH SCOPE MUST HOLD MORE ROWS THAN ANY CASE SELECTS, or a count assertion is
// satisfied by the scope rather than by the filter. keepOwner therefore owns two
// schedules, one of which matches no needle and is paused; otherOwner's row
// carries the only other owner email, which is what makes the q-on-email axis
// separable from q-on-name. Every case names the id SET it expects and asserts
// Total against its size, because enabled=false selects more than one row
// fleet-wide.
func TestListScheduledJobs_FilterArms_FirstPage(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjfilter-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	keepOwner := createTestUser(t, q, "Keeper", "aardvark@sjfilter.test", false)
	keepToken := createTestToken(t, q, keepOwner.ID)
	otherOwner := createTestUser(t, q, "Other", "zebra@sjfilter.test", false)

	keepID := seedFilterSchedule(t, pool, "keeper-nightly", uuidString(keepOwner.ID), "17 3 * * *", true)
	// Same owner as keepID, so the owner scope holds two rows; matches no needle
	// on name or cron, and is paused.
	fillerID := seedFilterSchedule(t, pool, "quiet-filler", uuidString(keepOwner.ID), "@monthly", false)
	otherID := seedFilterSchedule(t, pool, "other-weekly", uuidString(otherOwner.ID), "@daily", false)

	scopes := []struct {
		name  string
		token string
	}{
		{"admin", adminToken},
		{"owner", keepToken},
	}
	filters := []struct {
		name      string
		query     string
		wantAdmin []string
		wantOwner []string
	}{
		{"enabled_true", "enabled=true", []string{keepID}, []string{keepID}},
		{"enabled_false", "enabled=false", []string{fillerID, otherID}, []string{fillerID}},
		{"q_name", "q=keeper", []string{keepID}, []string{keepID}},
		{"q_email", "q=aardvark", []string{keepID, fillerID}, []string{keepID, fillerID}},
		{"q_cron", "q=17+3", []string{keepID}, []string{keepID}},
		{"both", "enabled=true&q=keeper", []string{keepID}, []string{keepID}},
	}

	for _, sc := range scopes {
		for _, arm := range sortArms(api.ScheduledJobsSortSpec) {
			for _, f := range filters {
				t.Run(sc.name+"/"+arm+"/"+f.name, func(t *testing.T) {
					want := f.wantAdmin
					if sc.name == "owner" {
						want = f.wantOwner
					}
					code, p := getScheduledJobsPage(t, srv, sc.token, arm+"&"+f.query)
					require.Equal(t, http.StatusOK, code)

					got := make([]string, 0, len(p.Items))
					for _, it := range p.Items {
						id, _ := it["id"].(string)
						got = append(got, id)
					}
					assert.ElementsMatch(t, want, got,
						"%s %s on the FIRST PAGE must return exactly the matching rows; a row too "+
							"many means the filter was dropped, which is the unparenthesised cursor "+
							"disjunction", arm, f.query)
					assert.EqualValues(t, len(want), p.Total,
						"total must count the filtered set, not the table")
				})
			}
		}
	}
}
