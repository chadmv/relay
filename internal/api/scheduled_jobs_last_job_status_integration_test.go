//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The status served is the LIVE one, not a copy taken when last_job_id was
// stamped: flip the job's status and the next read follows.
func TestScheduledJob_LastJobStatusIsLive(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Owner", "sjlive-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	schedID := seedFilterSchedule(t, pool, "with-a-job", uuidString(owner.ID), "@daily", true)

	var jobID string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, submitted_by, status, scheduled_job_id)
		 VALUES ('spawned', $1::uuid, 'running', $2::uuid) RETURNING id`,
		uuidString(owner.ID), schedID).Scan(&jobID))
	_, err := pool.Exec(t.Context(),
		`UPDATE scheduled_jobs SET last_job_id = $2::uuid WHERE id = $1::uuid`, schedID, jobID)
	require.NoError(t, err)

	read := func() map[string]any {
		req := httptest.NewRequest("GET", "/v1/scheduled-jobs/"+schedID, nil)
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var m map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&m))
		return m
	}

	m := read()
	assert.Equal(t, jobID, m["last_job_id"])
	assert.Equal(t, "running", m["last_job_status"])

	_, err = pool.Exec(t.Context(), `UPDATE jobs SET status = 'failed' WHERE id = $1::uuid`, jobID)
	require.NoError(t, err)

	m = read()
	assert.Equal(t, "failed", m["last_job_status"],
		"the status must be read at request time, not copied when last_job_id was stamped")
}

// An enrichment lookup failure is a 500, NOT a silently absent key, which is
// the divergence from fillOwnerEmails that makes the pairing invariant
// enforceable: degrading would forge "this schedule has never produced a job"
// out of a database fault.
//
// The fault is injected by renaming the column only GetJobStatusesByIDs reads.
// That input discriminates: the list statement selects sj.* through the users
// join and never touches jobs, so it still succeeds, and a handler that ignored
// the enrichment error would answer 200 with last_job_id present and
// last_job_status absent. Each test owns its own container, so the rename is
// confined to this test.
func TestListScheduledJobs_EnrichmentFailureIsA500(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	owner := createTestUser(t, q, "Owner", "sjfail-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	schedID := seedFilterSchedule(t, pool, "has-job", uuidString(owner.ID), "@daily", true)
	var jobID string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, submitted_by, status, scheduled_job_id)
		 VALUES ('spawned', $1::uuid, 'done', $2::uuid) RETURNING id`,
		uuidString(owner.ID), schedID).Scan(&jobID))
	_, err := pool.Exec(t.Context(),
		`UPDATE scheduled_jobs SET last_job_id = $2::uuid WHERE id = $1::uuid`, schedID, jobID)
	require.NoError(t, err)

	// Control: the request answers 200 and both keys before the fault.
	code, p := getScheduledJobsPage(t, srv, ownerToken, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	require.Contains(t, p.Items[0], "last_job_status")

	_, err = pool.Exec(t.Context(), `ALTER TABLE jobs RENAME COLUMN status TO status_broken`)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/v1/scheduled-jobs", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"a failed enrichment lookup must fail the request, not drop the key")
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "error")
	assert.NotContains(t, body, "items",
		"the response must not be a page at all, so no row can carry last_job_id without its status")
}

// The list carries the field on the same terms as the get, in both scopes, and
// a schedule with no last job carries NEITHER key.
func TestListScheduledJobs_LastJobStatusPairingOnTheWire(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "sjpair-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	owner := createTestUser(t, q, "Owner", "sjpair-owner@test.com", false)
	ownerToken := createTestToken(t, q, owner.ID)

	withJob := seedFilterSchedule(t, pool, "has-job", uuidString(owner.ID), "@daily", true)
	seedFilterSchedule(t, pool, "no-job", uuidString(owner.ID), "@daily", true)

	var jobID string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, submitted_by, status, scheduled_job_id)
		 VALUES ('spawned', $1::uuid, 'done', $2::uuid) RETURNING id`,
		uuidString(owner.ID), withJob).Scan(&jobID))
	_, err := pool.Exec(t.Context(),
		`UPDATE scheduled_jobs SET last_job_id = $2::uuid WHERE id = $1::uuid`, withJob, jobID)
	require.NoError(t, err)

	for _, tc := range []struct{ name, token string }{
		{"admin", adminToken},
		{"owner", ownerToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, p := getScheduledJobsPage(t, srv, tc.token, "limit=50")
			require.Equal(t, http.StatusOK, code)
			require.Len(t, p.Items, 2)
			pairedCount := 0
			for _, it := range p.Items {
				_, hasID := it["last_job_id"]
				_, hasStatus := it["last_job_status"]
				assert.Equal(t, hasID, hasStatus,
					"last_job_status must be present exactly when last_job_id is, got id=%v status=%v",
					hasID, hasStatus)
				if hasID {
					pairedCount++
					assert.Equal(t, "done", it["last_job_status"])
				}
			}
			assert.Equal(t, 1, pairedCount,
				"exactly one seeded schedule has a last job; a zero here would make the "+
					"pairing assertion above vacuous")
		})
	}
}
