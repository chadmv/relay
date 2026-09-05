//go:build integration

package api_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedScheduledJobFull inserts one schedule with every field the schedules
// tests vary. The thinner seedScheduledJob and seedFilterSchedule wrappers each
// fix the parameters their callers do not care about; a second copy of this
// INSERT would drift from the columns the schema requires.
func seedScheduledJobFull(t *testing.T, pool *pgxpool.Pool, name, ownerID, cronExpr string, enabled bool, nextRunAt, updatedAt time.Time) string {
	t.Helper()
	jobSpec := `{"name":"` + name + `-job","tasks":[{"name":"t","command":["echo","x"]}]}`
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO scheduled_jobs
		   (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at, updated_at)
		 VALUES ($1, $2::uuid, $3, 'UTC', $4::jsonb, 'skip', $5, $6, $7)
		 RETURNING id`,
		name, ownerID, cronExpr, jobSpec, enabled, nextRunAt, updatedAt,
	).Scan(&id)
	require.NoError(t, err, "seedScheduledJobFull %s", name)
	return id
}

// seedLastJob inserts a job attributed to the schedule and points the
// schedule's last_job_id at it, which is the two-statement setup every
// last_job_status test needs.
func seedLastJob(t *testing.T, pool *pgxpool.Pool, ownerID, schedID, status string) string {
	t.Helper()
	var jobID string
	require.NoError(t, pool.QueryRow(t.Context(),
		`INSERT INTO jobs (name, submitted_by, status, scheduled_job_id)
		 VALUES ('spawned', $1::uuid, $2, $3::uuid) RETURNING id`,
		ownerID, status, schedID).Scan(&jobID))
	_, err := pool.Exec(t.Context(),
		`UPDATE scheduled_jobs SET last_job_id = $2::uuid WHERE id = $1::uuid`, schedID, jobID)
	require.NoError(t, err)
	return jobID
}

// sortArms returns every arm of a SortSpec as a ?sort= query fragment, in both
// directions, DERIVED from the server's own allowlist rather than hand-listed:
// a key added to the spec without a dispatch arm reaches that switch's panic
// default, and a key given a dispatch arm but not the filter fields returns the
// wrong row set for that arm alone. Sorted so subtest names are stable.
func sortArms(spec api.SortSpec) []string {
	keys := make([]string, 0, len(spec.Keys))
	for k := range spec.Keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	arms := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		arms = append(arms, "sort="+k, "sort=-"+k)
	}
	return arms
}

// pageEnvelope mirrors the API's response envelope so tests can decode list
// endpoints without depending on the api package's internal page[T] type.
type pageEnvelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}

// installFailDeleteTrigger attaches a BEFORE DELETE trigger to the named table
// that raises a SQL error on every DELETE. Used to simulate a DB error on the
// session-revocation step of password handlers without breaking SELECTs that
// run earlier in the same request (e.g. BearerAuth).
func installFailDeleteTrigger(t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()
	stmt := fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION fail_delete_%[1]s() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'forced delete failure for test'; END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER block_delete_%[1]s BEFORE DELETE ON %[1]s
		FOR EACH STATEMENT EXECUTE FUNCTION fail_delete_%[1]s();
	`, table)
	_, err := pool.Exec(t.Context(), stmt)
	require.NoError(t, err)
}

func newTestQueries(t *testing.T) *store.Queries {
	t.Helper()
	return store.New(newTestPool(t))
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := pgdsn.NewIntegrationDSN(t)

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { pgdsn.BoundedCleanup(t, "pool.Close", pool.Close) })

	return pool
}
