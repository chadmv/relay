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
// The two rows differ on EVERY filter axis and each filter case names which row
// it expects, so no case can be satisfied by an accident on a different axis:
// dropping the cron arm from the q disjunction, or folding enabled=false into
// enabled=true, each reddens exactly one case on the arm that carries the
// mutation. Admin and owner scope are separate subtests because they run
// separate statements.
func TestListScheduledJobs_FilterArms_FirstPage(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjfilter-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	keepOwner := createTestUser(t, q, "Keeper", "aardvark@sjfilter.test", false)
	keepToken := createTestToken(t, q, keepOwner.ID)
	otherOwner := createTestUser(t, q, "Other", "zebra@sjfilter.test", false)

	keepID := seedFilterSchedule(t, pool, "keeper-nightly", uuidString(keepOwner.ID), "17 3 * * *", true)
	otherID := seedFilterSchedule(t, pool, "other-weekly", uuidString(otherOwner.ID), "@daily", false)

	scopes := []struct {
		name  string
		token string
	}{
		{"admin", adminToken},
		{"owner", keepToken},
	}
	filters := []struct {
		name   string
		query  string
		wantID string
	}{
		{"enabled_true", "enabled=true", keepID},
		{"enabled_false", "enabled=false", otherID},
		{"q_name", "q=keeper", keepID},
		{"q_email", "q=aardvark", keepID},
		{"q_cron", "q=17+3", keepID},
		{"both", "enabled=true&q=keeper", keepID},
	}

	for _, sc := range scopes {
		for _, arm := range sortArms(api.ScheduledJobsSortSpec) {
			for _, f := range filters {
				t.Run(sc.name+"/"+arm+"/"+f.name, func(t *testing.T) {
					// The owner arm only ever holds the keeper row, so a case
					// expecting the other owner's row has nothing to assert there.
					if sc.name == "owner" && f.wantID != keepID {
						t.Skip("the other row belongs to a different owner and is out of this scope")
					}
					code, p := getScheduledJobsPage(t, srv, sc.token, arm+"&"+f.query)
					require.Equal(t, http.StatusOK, code)
					require.Len(t, p.Items, 1,
						"%s %s on the FIRST PAGE must return exactly the matching row; "+
							"two rows means the filter was dropped, which is the unparenthesised "+
							"cursor disjunction", arm, f.query)
					assert.Equal(t, f.wantID, p.Items[0]["id"])
					assert.Equal(t, int64(1), p.Total,
						"total must count the filtered set, not the table")
				})
			}
		}
	}
}
