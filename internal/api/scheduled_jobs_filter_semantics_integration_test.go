//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enabled=false is a REAL filter meaning "only paused", not a synonym for
// absent. The fixture holds one of each, so a handler that folded false into
// absent returns two rows and fails.
func TestListScheduledJobs_EnabledFalseIsNotAbsent(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjtri-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjtri-owner@test.com", false)

	seedFilterSchedule(t, pool, "live-one", uuidString(owner.ID), "@daily", true)
	pausedID := seedFilterSchedule(t, pool, "paused-one", uuidString(owner.ID), "@daily", false)

	code, p := getScheduledJobsPage(t, srv, adminToken, "enabled=false")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	assert.Equal(t, pausedID, p.Items[0]["id"])
	assert.Equal(t, int64(1), p.Total)

	code, p = getScheduledJobsPage(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 2, "an absent enabled must not filter")
}

// Three axes, THREE SEPARATE ROWS, each matched by exactly one axis. A dropped
// OR arm is invisible if one row satisfies two axes at once.
func TestListScheduledJobs_QMatchesEachAxisIndependently(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjq-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	byName := createTestUser(t, q, "One", "one@sjq.test", false)
	byEmail := createTestUser(t, q, "Two", "zebra@sjq.test", false)
	byCron := createTestUser(t, q, "Three", "three@sjq.test", false)

	nameID := seedFilterSchedule(t, pool, "aardvark-run", uuidString(byName.ID), "@daily", true)
	emailID := seedFilterSchedule(t, pool, "plain-run", uuidString(byEmail.ID), "@daily", true)
	cronID := seedFilterSchedule(t, pool, "other-run", uuidString(byCron.ID), "17 3 * * *", true)

	cases := []struct {
		needle string
		wantID string
	}{
		{"aardvark", nameID},
		{"zebra", emailID},
		{"17 3", cronID},
	}
	for _, tc := range cases {
		t.Run(tc.needle, func(t *testing.T) {
			code, p := getScheduledJobsPage(t, srv, adminToken, "q="+url.QueryEscape(tc.needle))
			require.Equal(t, http.StatusOK, code)
			require.Len(t, p.Items, 1, "q=%q must match on exactly one axis", tc.needle)
			assert.Equal(t, tc.wantID, p.Items[0]["id"])
			assert.Equal(t, int64(1), p.Total)
		})
	}
}

func TestListScheduledJobs_QIsCaseInsensitive(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjcase-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjcase-owner@test.com", false)
	id := seedFilterSchedule(t, pool, "MixedCaseName", uuidString(owner.ID), "@daily", true)

	for _, needle := range []string{"mixedcase", "MIXEDCASE", "MixedCase"} {
		code, p := getScheduledJobsPage(t, srv, adminToken, "q="+needle)
		require.Equal(t, http.StatusOK, code, "needle=%s", needle)
		require.Len(t, p.Items, 1, "needle=%s", needle)
		assert.Equal(t, id, p.Items[0]["id"])
	}
}

// Percent and underscore are LITERAL characters, not wildcards. strpos has no
// metacharacters; this is the test that goes red if the predicate is ever
// rewritten to an unescaped ILIKE, where a needle of one percent sign matches
// every schedule.
func TestListScheduledJobs_QTreatsPercentAndUnderscoreAsLiterals(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjlit-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjlit-owner@test.com", false)

	litID := seedFilterSchedule(t, pool, "100%_done", uuidString(owner.ID), "@daily", true)
	seedFilterSchedule(t, pool, "plain-name", uuidString(owner.ID), "@daily", true)

	for _, needle := range []string{"%_", "%", "_"} {
		t.Run(needle, func(t *testing.T) {
			code, p := getScheduledJobsPage(t, srv, adminToken, "q="+url.QueryEscape(needle))
			require.Equal(t, http.StatusOK, code)
			require.Len(t, p.Items, 1,
				"needle %q must be matched literally; a wildcard reading returns both rows", needle)
			assert.Equal(t, litID, p.Items[0]["id"])
		})
	}
}

// A cursor carries no record of the filters that were active when it was issued,
// and the server does not reject a mismatched one. Filter correctness is
// nevertheless cursor-independent: every predicate is applied in SQL alongside
// the keyset comparison, so a stale cursor can start a page at a surprising
// position but can NEVER return a row that fails the current predicates.
func TestListScheduledJobs_StaleCursorNeverReturnsAFailingRow(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjcur-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjcur-owner@test.com", false)

	for i := 0; i < 6; i++ {
		seedFilterSchedule(t, pool, fmt.Sprintf("live-%d", i), uuidString(owner.ID), "@daily", true)
		seedFilterSchedule(t, pool, fmt.Sprintf("paused-%d", i), uuidString(owner.ID), "@daily", false)
	}

	code, p := getScheduledJobsPage(t, srv, adminToken, "enabled=true&limit=2")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor)

	code, p = getScheduledJobsPage(t, srv, adminToken,
		"enabled=false&limit=2&cursor="+url.QueryEscape(p.NextCursor))
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.Items,
		"the fixture must leave rows after the cursor, or the loop below asserts nothing")
	for _, it := range p.Items {
		assert.Equal(t, false, it["enabled"],
			"a cursor issued under enabled=true must never carry an enabled row into an "+
				"enabled=false page")
	}
}

// Owner scoping holds UNDER FILTERS: two users issuing the identical filtered
// request see disjoint sets, and an admin sees both.
func TestListScheduledJobs_OwnerScopingHoldsUnderFilters(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjscope-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	a := createTestUser(t, q, "A", "a@sjscope.test", false)
	b := createTestUser(t, q, "B", "b@sjscope.test", false)
	aToken := createTestToken(t, q, a.ID)
	bToken := createTestToken(t, q, b.ID)

	aID := seedFilterSchedule(t, pool, "shared-needle-a", uuidString(a.ID), "@daily", true)
	bID := seedFilterSchedule(t, pool, "shared-needle-b", uuidString(b.ID), "@daily", true)

	const query = "q=shared-needle&enabled=true"

	code, p := getScheduledJobsPage(t, srv, aToken, query)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	assert.Equal(t, aID, p.Items[0]["id"])
	assert.Equal(t, int64(1), p.Total, "total must be owner-scoped too")

	code, p = getScheduledJobsPage(t, srv, bToken, query)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	assert.Equal(t, bID, p.Items[0]["id"])

	code, p = getScheduledJobsPage(t, srv, adminToken, query)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 2)
	assert.Equal(t, int64(2), p.Total)
}

// The parser's 400s are reached THROUGH THE HANDLER. The default-lane tests pin
// the bodies; this pins that the parser is on the request path at all, and that
// the arity refusal covers both filter parameters.
func TestListScheduledJobs_FilterErrorsAreReachedThroughTheHandler(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjerr-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"enabled not a bool", "enabled=yes", "invalid enabled; expected true or false"},
		{"q repeated", "q=a&q=b", "query parameter \"q\" must appear at most once"},
		{"enabled repeated", "enabled=true&enabled=false", "query parameter \"enabled\" must appear at most once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/scheduled-jobs?"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			var body struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
			assert.Equal(t, tc.want, body.Error)
		})
	}
}

// walkScheduledJobs pages the whole filtered set and returns the ids seen, the
// page count and the Total reported on each page, failing on a duplicate id.
func walkScheduledJobs(t *testing.T, srv interface {
	Handler() http.Handler
}, token string, baseQS url.Values) (map[string]bool, int, []int64) {
	t.Helper()
	seen := map[string]bool{}
	var totals []int64
	cursor := ""
	pages := 0
	for {
		qs := url.Values{}
		for k, v := range baseQS {
			qs[k] = v
		}
		if cursor != "" {
			qs.Set("cursor", cursor)
		}
		code, page := getScheduledJobsPage(t, srv, token, qs.Encode())
		require.Equal(t, http.StatusOK, code)
		pages++
		totals = append(totals, page.Total)
		for _, it := range page.Items {
			id, _ := it["id"].(string)
			require.False(t, seen[id], "duplicate schedule id across pages: %s", id)
			seen[id] = true
		}
		if page.NextCursor == "" {
			return seen, pages, totals
		}
		cursor = page.NextCursor
		require.Less(t, pages, 20, "runaway pagination: cursor never terminated")
	}
}

// Both filters stay applied across a real page boundary, and total agrees with
// the walked set on every page.
//
// Every third row fails one predicate or the other, so an excluded row sits
// beside each boundary: that is the arrangement a filter dropped on page two
// would show up in, which a single-page assertion cannot see.
func TestListScheduledJobs_QAndEnabledSurviveACursorWalk(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjwalk-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjwalk-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	wantIDs := map[string]bool{}
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("walk-needle-%03d", i)
		switch i % 3 {
		case 0: // matches q, enabled: excluded by enabled=false
			seedFilterSchedule(t, pool, name, uuidString(owner.ID), "@daily", true)
		case 1: // matches q, paused: kept
			wantIDs[seedFilterSchedule(t, pool, name, uuidString(owner.ID), "@daily", false)] = true
		case 2: // paused but does not match q: excluded by q
			seedFilterSchedule(t, pool, fmt.Sprintf("walk-other-%03d", i), uuidString(owner.ID), "@daily", false)
		}
	}
	matching := len(wantIDs)

	baseQS := url.Values{"q": {"walk-needle"}, "enabled": {"false"}, "limit": {"5"}}
	for _, tc := range []struct{ name, token string }{
		{"admin", adminToken},
		{"owner", ownerToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen, pages, totals := walkScheduledJobs(t, srv, tc.token, baseQS)
			require.Greater(t, pages, 1, "the fixture must force more than one page")
			assert.Len(t, seen, matching, "the walk must return every matching row and no other")
			for id := range wantIDs {
				assert.True(t, seen[id], "matching schedule %s missing from the walk", id)
			}
			for _, tot := range totals {
				assert.EqualValues(t, matching, tot, "total must equal the walked count on every page")
			}
		})
	}
}
