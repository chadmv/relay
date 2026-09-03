//go:build integration

package api_test

import (
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/api"
)

// jobsFilterFixture seeds the two rows every arm is probed with.
//
// The two jobs differ on all four filter axes and on nothing that would let a
// single accident satisfy two assertions:
//
//   - name:  "Alpha-100%-Target" vs "bravo_other". Exactly one carries a
//     literal %, exactly one a literal _, and neither email carries either, so
//     q=% and q=_ each select exactly one row under strpos - and BOTH select
//     both rows under an unescaped ILIKE, which is what makes the pair a
//     discriminator for the strpos-versus-ILIKE property rather than a
//     restatement of it.
//   - case: the alpha name is mixed-case while the q=alpha probe is lower.
//     With an all-lowercase fixture, dropping lower() from the COLUMN side of
//     strpos changes no answer; with this one it drops the row.
//   - owner: two users, so mine=true has something to exclude.
//   - email: "alpha" appears only in a name, "alice@" only in an email, so the
//     name arm and the email arm of q are independent.
//   - created_at: two fixed instants, used verbatim as the since and until
//     bounds, so the half-open [since, until) boundary is pinned at both ends
//     by rows sitting exactly ON the bounds.
//
// Both jobs hang off one scheduled job owned by alice, and both are status
// 'pending', so the status branch and the scheduled_job_id branch probe the
// same two rows as the sort arms.
type jobsFilterFixture struct {
	token    string
	schedID  string
	targetTS string // RFC3339 of the alpha row's created_at
	otherTS  string // RFC3339 of the bravo row's created_at
}

const (
	jobsFilterAlphaName = "Alpha-100%-Target"
	jobsFilterBravoName = "bravo_other"
)

func seedJobsFilterFixture(t *testing.T) (*api.Server, jobsFilterFixture) {
	t.Helper()
	s, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@jobfilters.test", false)
	bob := createTestUser(t, q, "Bob", "bob@jobfilters.test", false)

	alphaAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	bravoAt := time.Date(2026, 3, 1, 1, 0, 0, 0, time.UTC)

	var schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, next_run_at)
		 VALUES ('jobfilters-sched', $1, '@daily', '{}'::jsonb, NOW()) RETURNING id`,
		alice.ID).Scan(&schedID))

	alphaID := insertJobAt(t, pool, alice.ID, jobsFilterAlphaName, alphaAt)
	bravoID := insertJobAt(t, pool, bob.ID, jobsFilterBravoName, bravoAt)
	for _, id := range []pgtype.UUID{alphaID, bravoID} {
		_, err := pool.Exec(t.Context(),
			`UPDATE jobs SET scheduled_job_id = $1 WHERE id = $2`, schedID, id)
		require.NoError(t, err)
	}

	return s, jobsFilterFixture{
		token:    createTestToken(t, q, alice.ID),
		schedID:  uuidString(schedID),
		targetTS: alphaAt.Format(time.RFC3339Nano),
		otherTS:  bravoAt.Format(time.RFC3339Nano),
	}
}

// TestListJobs_FiltersApplyOnEveryArm crosses every filter predicate with
// every list arm the endpoint can dispatch to. The arms are derived from
// JobsSortSpec.Keys plus the two filtered branches, which have their own
// statements and cannot carry ?sort=.
func TestListJobs_FiltersApplyOnEveryArm(t *testing.T) {
	srv, fx := seedJobsFilterFixture(t)

	filters := []struct {
		name  string
		param url.Values
		want  string
	}{
		{"q matches the job name", url.Values{"q": {"alpha"}}, jobsFilterAlphaName},
		{"q is case-insensitive", url.Values{"q": {"ALPHA"}}, jobsFilterAlphaName},
		{"q matches the owner email", url.Values{"q": {"alice@"}}, jobsFilterAlphaName},
		{"q treats percent as a literal", url.Values{"q": {"%"}}, jobsFilterAlphaName},
		{"q treats underscore as a literal", url.Values{"q": {"_"}}, jobsFilterBravoName},
		{"mine restricts to the caller", url.Values{"mine": {"true"}}, jobsFilterAlphaName},
		{"window includes its since bound", url.Values{"since": {fx.targetTS}, "until": {fx.otherTS}}, jobsFilterAlphaName},
		{"window excludes its until bound", url.Values{"until": {fx.otherTS}}, jobsFilterAlphaName},
		{"since alone opens the window at the end", url.Values{"since": {fx.otherTS}}, jobsFilterBravoName},
	}

	arms := append(sortArms(api.JobsSortSpec), "status=pending", "scheduled_job_id="+fx.schedID)

	for _, arm := range arms {
		for _, f := range filters {
			t.Run(arm+"/"+f.name, func(t *testing.T) {
				qs := url.Values{}
				for k, vs := range f.param {
					qs[k] = vs
				}
				qs.Set("limit", "50")
				full := arm + "&" + qs.Encode()

				code, page := getJobsPage(t, srv, fx.token, full)
				require.Equal(t, 200, code, "query=%s", full)
				assert.Equal(t, []string{f.want}, names(page), "query=%s", full)
				assert.EqualValues(t, 1, page.Total,
					"total must count only rows matching every active predicate; query=%s", full)
			})
		}
	}
}

// The fixture is only a discriminator if the unfiltered request on each arm
// returns BOTH rows. Without this, every assertion above could be satisfied by
// an arm that returns one row for unrelated reasons.
func TestListJobs_FixtureIsUnfilteredOnEveryArm(t *testing.T) {
	srv, fx := seedJobsFilterFixture(t)

	arms := append(sortArms(api.JobsSortSpec), "status=pending", "scheduled_job_id="+fx.schedID)
	for _, arm := range arms {
		t.Run(arm, func(t *testing.T) {
			code, page := getJobsPage(t, srv, fx.token, arm+"&limit=50")
			require.Equal(t, 200, code)
			got := names(page)
			sort.Strings(got)
			assert.Equal(t, []string{jobsFilterAlphaName, jobsFilterBravoName}, got)
			assert.EqualValues(t, 2, page.Total)
		})
	}
}
