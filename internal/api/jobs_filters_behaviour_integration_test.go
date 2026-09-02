//go:build integration

package api_test

import (
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertJobAt inserts one job with an EXACT created_at, which the POST /v1/jobs
// path cannot give (it stamps NOW()). Half-open window assertions need the
// bound to equal a row's timestamp to the microsecond, so the fixture writes
// the column directly. updated_at is set to the same instant so the
// updated_at sort arms are equally deterministic.
func insertJobAt(t *testing.T, pool *pgxpool.Pool, ownerID pgtype.UUID, name string, at time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, priority, submitted_by, created_at, updated_at)
		 VALUES ($1, 'normal', $2, $3, $3) RETURNING id`,
		name, ownerID, at).Scan(&id))
	return id
}

func jobNames(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		n, _ := it["name"].(string)
		out = append(out, n)
	}
	return out
}

// mine=true is resolved from the bearer token, so two users issuing the
// IDENTICAL request get disjoint lists. The request strings are byte-identical
// on purpose: nothing in the URL names a user, so nothing in the URL can be
// changed to ask for someone else's jobs.
func TestListJobs_MineIsResolvedFromTheToken(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@minefilter.test", false)
	bob := createTestUser(t, q, "Bob", "bob@minefilter.test", false)
	aliceTok := createTestToken(t, q, alice.ID)
	bobTok := createTestToken(t, q, bob.ID)

	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	insertJobAt(t, pool, alice.ID, "alice-job", base)
	insertJobAt(t, pool, bob.ID, "bob-job", base.Add(time.Hour))

	const query = "mine=true&limit=50"

	code, alicePage := getJobsPage(t, srv, aliceTok, query)
	require.Equal(t, 200, code)
	assert.Equal(t, []string{"alice-job"}, jobNames(alicePage.Items))
	assert.EqualValues(t, 1, alicePage.Total)

	code, bobPage := getJobsPage(t, srv, bobTok, query)
	require.Equal(t, 200, code)
	assert.Equal(t, []string{"bob-job"}, jobNames(bobPage.Items))
	assert.EqualValues(t, 1, bobPage.Total)

	// Control: without the filter, the identical token sees both, so the
	// disjointness above is the predicate's doing and not a seeding accident.
	code, all := getJobsPage(t, srv, aliceTok, "limit=50")
	require.Equal(t, 200, code)
	assert.Len(t, all.Items, 2)
	assert.EqualValues(t, 2, all.Total)
}

// The window is [since, until): a row created exactly at since is IN, a row
// created exactly at until is OUT. Half-open so consecutive Timeline buckets
// tile without a job appearing in two of them. The three rows sit exactly on
// the two bounds and strictly between them, so an inclusive-or-exclusive slip
// at either end changes the answer.
func TestListJobs_WindowIsHalfOpen(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Wendy", "wendy@window.test", false)
	token := createTestToken(t, q, user.ID)

	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	mid := since.Add(30 * time.Minute)
	until := since.Add(time.Hour)

	insertJobAt(t, pool, user.ID, "at-since", since)
	insertJobAt(t, pool, user.ID, "inside", mid)
	insertJobAt(t, pool, user.ID, "at-until", until)

	qs := url.Values{
		"since": {since.Format(time.RFC3339Nano)},
		"until": {until.Format(time.RFC3339Nano)},
		"limit": {"50"},
	}
	code, page := getJobsPage(t, srv, token, qs.Encode())
	require.Equal(t, 200, code)

	got := jobNames(page.Items)
	sort.Strings(got)
	assert.Equal(t, []string{"at-since", "inside"}, got,
		"since is inclusive and until is exclusive")
	assert.EqualValues(t, 2, page.Total)
}

// until == since is a legal empty window, not a 400.
func TestListJobs_EqualBoundsReturnAnEmptyPage(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Eve", "eve@window.test", false)
	token := createTestToken(t, q, user.ID)

	at := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	insertJobAt(t, pool, user.ID, "only-job", at)

	qs := url.Values{
		"since": {at.Format(time.RFC3339Nano)},
		"until": {at.Format(time.RFC3339Nano)},
		"limit": {"50"},
	}
	code, page := getJobsPage(t, srv, token, qs.Encode())
	require.Equal(t, 200, code)
	assert.Empty(t, page.Items)
	assert.EqualValues(t, 0, page.Total)
}

// total is the count of rows matching every active predicate, independent of
// the cursor. limit=1 is what separates the two: a total that echoed the page
// size, or that counted the unfiltered table, both fail here and in opposite
// directions.
func TestListJobs_TotalReflectsEveryPredicate(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@total.test", false)
	bob := createTestUser(t, q, "Bob", "bob@total.test", false)
	token := createTestToken(t, q, alice.ID)

	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		insertJobAt(t, pool, alice.ID, "alice-"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 2; i++ {
		insertJobAt(t, pool, bob.ID, "bob-"+string(rune('a'+i)), base.Add(time.Duration(10+i)*time.Minute))
	}

	code, page := getJobsPage(t, srv, token, "mine=true&limit=1")
	require.Equal(t, 200, code)
	require.Len(t, page.Items, 1)
	assert.EqualValues(t, 3, page.Total, "total counts alice's jobs, not the page and not the table")

	code, unfiltered := getJobsPage(t, srv, token, "limit=1")
	require.Equal(t, 200, code)
	assert.EqualValues(t, 5, unfiltered.Total, "control: without the filter the table has five rows")
}

// A cursor carries no record of the filters that were active when it was
// issued, and the server does not reject a mismatched one. The defined, safe
// failure mode is that the page may start at a surprising POSITION but can
// never contain a row that fails the CURRENT predicates, because every
// predicate is applied in SQL alongside the keyset comparison.
func TestListJobs_FilterCorrectnessIsCursorIndependent(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@cursor.test", false)
	bob := createTestUser(t, q, "Bob", "bob@cursor.test", false)
	token := createTestToken(t, q, alice.ID)

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	// Interleaved owners so the unfiltered first page ends on a bob row.
	insertJobAt(t, pool, bob.ID, "bob-1", base.Add(4*time.Minute))
	insertJobAt(t, pool, alice.ID, "alice-1", base.Add(3*time.Minute))
	insertJobAt(t, pool, bob.ID, "bob-2", base.Add(2*time.Minute))
	insertJobAt(t, pool, alice.ID, "alice-2", base.Add(1*time.Minute))

	code, first := getJobsPage(t, srv, token, "limit=1")
	require.Equal(t, 200, code)
	require.NotEmpty(t, first.NextCursor, "fixture: there must be a next page")

	code, second := getJobsPage(t, srv, token, "mine=true&limit=50&cursor="+url.QueryEscape(first.NextCursor))
	require.Equal(t, 200, code)
	for _, item := range second.Items {
		assert.Equal(t, "alice@cursor.test", item["submitted_by_email"],
			"a stale cursor may move the starting position but must never return a row that fails the current predicate")
	}
	require.NotEmpty(t, second.Items, "the assertion above is vacuous on an empty page")
}
