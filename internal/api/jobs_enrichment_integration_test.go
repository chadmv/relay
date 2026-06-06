//go:build integration

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// Submits a job, drives two of its three tasks to done with timing, and asserts
// the list row reports total/done counts, started_at/finished_at, and (because
// the job is schedule-spawned) the schedule name.
func TestListJobs_Enrichment(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	user := createTestUser(t, q, "Enrich", "enrich-user@test.com", false)
	token := createTestToken(t, q, user.ID)

	// A scheduled job owned by the user, and a job linked to it.
	var schedID pgtype.UUID
	err := pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, next_run_at)
		 VALUES ('nightly-etl', $1, '@daily', '{}'::jsonb, NOW()) RETURNING id`,
		user.ID).Scan(&schedID)
	require.NoError(t, err)

	var jobID pgtype.UUID
	err = pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, priority, submitted_by, scheduled_job_id)
		 VALUES ('etl-run', 'normal', $1, $2) RETURNING id`,
		user.ID, schedID).Scan(&jobID)
	require.NoError(t, err)

	// Three tasks: two done (with started/finished), one pending. The `commands`
	// column is JSONB NOT NULL DEFAULT '[]' so it is omitted here.
	started := time.Now().Add(-10 * time.Minute)
	finished := time.Now().Add(-2 * time.Minute)
	for i, st := range []string{"done", "done", "pending"} {
		var sAt, fAt any
		if st == "done" {
			sAt, fAt = started, finished
		}
		_, err = pool.Exec(t.Context(),
			`INSERT INTO tasks (job_id, name, status, started_at, finished_at)
			 VALUES ($1, $2, $3, $4, $5)`,
			jobID, fmt.Sprintf("t%d", i), st, sAt, fAt)
		require.NoError(t, err)
	}

	code, page := getJobsPage(t, srv, token, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, page.Items, 1)
	row := page.Items[0]

	require.EqualValues(t, 3, row["total_tasks"])
	require.EqualValues(t, 2, row["done_tasks"])
	require.Equal(t, "nightly-etl", row["scheduled_job_name"])
	require.NotEmpty(t, row["started_at"])
	require.NotEmpty(t, row["finished_at"])
}
