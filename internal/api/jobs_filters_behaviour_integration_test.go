//go:build integration

package api_test

import (
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
