//go:build integration

package api_test

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobIDs is what makes the walk assertions identity-based: names repeat across
// fixtures, ids do not, so a duplicate here is a real duplicate row.
func jobIDs(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		id, _ := it["id"].(string)
		out = append(out, id)
	}
	return out
}

func TestListJobs_QFilterCursorWalkHasNoDuplicatesOrGaps(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@walkq.test", false)
	bob := createTestUser(t, q, "Bob", "bob@walkq.test", false)
	token := createTestToken(t, q, alice.ID)

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	const matching = 45
	const noise = 15
	wantIDs := make(map[string]bool, matching)
	for i := 0; i < matching; i++ {
		id := insertJobAt(t, pool, alice.ID, "walkq-target-"+padSeq(i), base.Add(time.Duration(i)*time.Minute))
		wantIDs[uuidString(id)] = true
	}
	for i := 0; i < noise; i++ {
		insertJobAt(t, pool, bob.ID, "no-match-"+padSeq(i), base.Add(time.Duration(matching+i)*time.Minute))
	}

	seen := map[string]bool{}
	var totals []int64
	cursor := ""
	pages := 0
	for {
		qs := url.Values{"q": {"walkq-target"}, "limit": {"20"}}
		if cursor != "" {
			qs.Set("cursor", cursor)
		}
		code, page := getJobsPage(t, srv, token, qs.Encode())
		require.Equal(t, 200, code)
		pages++
		totals = append(totals, page.Total)
		for _, id := range jobIDs(page.Items) {
			require.False(t, seen[id], "duplicate id across pages: %s", id)
			seen[id] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		require.Less(t, pages, 20, "runaway pagination: cursor never terminated")
	}

	require.Greater(t, pages, 1, "fixture must force more than one page for this to be a boundary-crossing test")
	assert.Len(t, seen, matching, "walked set must equal every matching row, no gap")
	for id := range wantIDs {
		assert.True(t, seen[id], "matching row %s missing from the walk", id)
	}
	for _, total := range totals {
		assert.EqualValues(t, matching, total, "total must be identical and match the walked count on every page")
	}
}

func TestListJobs_MineFilterCursorWalkHasNoDuplicatesOrGaps(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@walkmine.test", false)
	bob := createTestUser(t, q, "Bob", "bob@walkmine.test", false)
	token := createTestToken(t, q, alice.ID)

	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	const mine = 45
	const others = 15
	wantIDs := make(map[string]bool, mine)
	for i := 0; i < mine; i++ {
		id := insertJobAt(t, pool, alice.ID, "mine-"+padSeq(i), base.Add(time.Duration(i)*time.Minute))
		wantIDs[uuidString(id)] = true
	}
	for i := 0; i < others; i++ {
		insertJobAt(t, pool, bob.ID, "theirs-"+padSeq(i), base.Add(time.Duration(mine+i)*time.Minute))
	}

	seen := map[string]bool{}
	var totals []int64
	cursor := ""
	pages := 0
	for {
		qs := url.Values{"mine": {"true"}, "limit": {"20"}}
		if cursor != "" {
			qs.Set("cursor", cursor)
		}
		code, page := getJobsPage(t, srv, token, qs.Encode())
		require.Equal(t, 200, code)
		pages++
		totals = append(totals, page.Total)
		for _, id := range jobIDs(page.Items) {
			require.False(t, seen[id], "duplicate id across pages: %s", id)
			seen[id] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		require.Less(t, pages, 20, "runaway pagination: cursor never terminated")
	}

	require.Greater(t, pages, 1, "fixture must force more than one page for this to be a boundary-crossing test")
	assert.Len(t, seen, mine, "walked set must equal every one of the caller's rows, no gap")
	for id := range wantIDs {
		assert.True(t, seen[id], "owned row %s missing from the walk", id)
	}
	for _, total := range totals {
		assert.EqualValues(t, mine, total, "total must be identical and match the walked count on every page")
	}
}

func TestListJobs_WindowCursorWalkHasNoDuplicatesOrGaps(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Wendy", "wendy@walkwindow.test", false)
	token := createTestToken(t, q, user.ID)

	since := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	until := since.Add(60 * time.Minute)
	const inWindow = 45
	const outside = 15
	wantIDs := make(map[string]bool, inWindow)
	for i := 0; i < inWindow; i++ {
		at := since.Add(time.Duration(i) * time.Minute * 60 / inWindow)
		id := insertJobAt(t, pool, user.ID, "inwin-"+padSeq(i), at)
		wantIDs[uuidString(id)] = true
	}
	for i := 0; i < outside; i++ {
		insertJobAt(t, pool, user.ID, "outwin-"+padSeq(i), until.Add(time.Duration(i+1)*time.Minute))
	}

	seen := map[string]bool{}
	var totals []int64
	cursor := ""
	pages := 0
	for {
		qs := url.Values{
			"since": {since.Format(time.RFC3339Nano)},
			"until": {until.Format(time.RFC3339Nano)},
			"limit": {"20"},
		}
		if cursor != "" {
			qs.Set("cursor", cursor)
		}
		code, page := getJobsPage(t, srv, token, qs.Encode())
		require.Equal(t, 200, code)
		pages++
		totals = append(totals, page.Total)
		for _, id := range jobIDs(page.Items) {
			require.False(t, seen[id], "duplicate id across pages: %s", id)
			seen[id] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		require.Less(t, pages, 20, "runaway pagination: cursor never terminated")
	}

	require.Greater(t, pages, 1, "fixture must force more than one page for this to be a boundary-crossing test")
	assert.Len(t, seen, inWindow, "walked set must equal every in-window row, no gap")
	for id := range wantIDs {
		assert.True(t, seen[id], "in-window row %s missing from the walk", id)
	}
	for _, total := range totals {
		assert.EqualValues(t, inWindow, total, "total must be identical and match the walked count on every page")
	}
}

// Both predicates apply together on the scheduled_job_id branch. The two
// decoys each satisfy exactly one of them - one is in the schedule but fails
// q, one matches q under a different schedule - so a branch that dropped
// either predicate returns two rows rather than one.
func TestListJobs_ScheduledJobIDAndQCombine(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@combosched.test", false)
	token := createTestToken(t, q, alice.ID)

	var schedA, schedB string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, next_run_at)
		 VALUES ('combo-sched-a', $1, '@daily', '{}'::jsonb, NOW()) RETURNING id::text`,
		alice.ID).Scan(&schedA))
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, next_run_at)
		 VALUES ('combo-sched-b', $1, '@daily', '{}'::jsonb, NOW()) RETURNING id::text`,
		alice.ID).Scan(&schedB))

	base := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)
	bothMatchID := insertJobAt(t, pool, alice.ID, "combo-target", base)
	schedOnlyID := insertJobAt(t, pool, alice.ID, "no-needle-here", base.Add(time.Minute))
	qOnlyID := insertJobAt(t, pool, alice.ID, "combo-target-other-sched", base.Add(2*time.Minute))
	for _, pair := range []struct {
		id, sched string
	}{
		{uuidString(bothMatchID), schedA},
		{uuidString(schedOnlyID), schedA},
		{uuidString(qOnlyID), schedB},
	} {
		_, err := pool.Exec(t.Context(), `UPDATE jobs SET scheduled_job_id = $1 WHERE id = $2`, pair.sched, pair.id)
		require.NoError(t, err)
	}

	qs := url.Values{"scheduled_job_id": {schedA}, "q": {"combo-target"}, "limit": {"50"}}
	code, page := getJobsPage(t, srv, token, qs.Encode())
	require.Equal(t, 200, code)
	assert.Equal(t, []string{"combo-target"}, names(page),
		"only the row matching BOTH scheduled_job_id and q may appear")
	assert.EqualValues(t, 1, page.Total)
}

// Both predicates apply together on the status branch. The two decoys each
// satisfy exactly one - a done job before since, a pending job after it - so
// a branch that dropped either returns two rows rather than one.
func TestListJobs_StatusAndSinceCombine(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	alice := createTestUser(t, q, "Alice", "alice@combostatus.test", false)
	token := createTestToken(t, q, alice.ID)

	since := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	bothMatchID := insertJobAt(t, pool, alice.ID, "combo-status-target", since.Add(time.Hour))
	oldDoneID := insertJobAt(t, pool, alice.ID, "old-done", since.Add(-time.Hour))
	newPendingID := insertJobAt(t, pool, alice.ID, "new-pending", since.Add(2*time.Hour))
	for _, id := range []string{uuidString(bothMatchID), uuidString(oldDoneID)} {
		_, err := pool.Exec(t.Context(), `UPDATE jobs SET status = 'done' WHERE id = $1`, id)
		require.NoError(t, err)
	}
	_ = newPendingID // left at the default 'pending' status

	qs := url.Values{"status": {"done"}, "since": {since.Format(time.RFC3339Nano)}, "limit": {"50"}}
	code, page := getJobsPage(t, srv, token, qs.Encode())
	require.Equal(t, 200, code)
	assert.Equal(t, []string{"combo-status-target"}, names(page),
		"only the row matching BOTH status=done and since must appear")
	assert.EqualValues(t, 1, page.Total)
}

func padSeq(i int) string {
	return fmt.Sprintf("%02d", i)
}
