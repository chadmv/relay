//go:build integration

package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The indexdef fragment is asserted, not just the index name: an index with the
// right name over the wrong columns supports nothing and would leave mine=true
// on a sequential plan while a name-only assertion stayed green.
const (
	jobsOwnerCreatedIndex   = "idx_jobs_submitted_created_id"
	jobsOwnerCreatedColumns = "(submitted_by, created_at DESC, id DESC)"
)

func TestMigration000023_JobsOwnerCreatedIndexExists(t *testing.T) {
	pool := newTestPool(t)

	var def string
	err := pool.QueryRow(t.Context(),
		`SELECT indexdef FROM pg_indexes
		 WHERE schemaname = 'public' AND tablename = 'jobs' AND indexname = $1`,
		jobsOwnerCreatedIndex).Scan(&def)
	require.NoError(t, err, "index %s on jobs is missing (check migration 000023)", jobsOwnerCreatedIndex)
	assert.Contains(t, def, jobsOwnerCreatedColumns,
		"index %s exists but over the wrong columns: %s", jobsOwnerCreatedIndex, def)
}

func TestMigration000023_DownDropsJobsOwnerCreatedIndex(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := t.Context()

	// Positive control: it exists before the down migration runs, so a green
	// result below cannot come from it never having been created.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`,
		jobsOwnerCreatedIndex).Scan(&n))
	require.Equal(t, 1, n, "fixture: %s must exist before the down migration", jobsOwnerCreatedIndex)

	require.NoError(t, storeMigrateTo(dsn, 22))

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`,
		jobsOwnerCreatedIndex).Scan(&n))
	assert.Equal(t, 0, n, "%s must be dropped by 000023_jobs_owner_created_index.down.sql", jobsOwnerCreatedIndex)

	// The table itself must survive: a down migration that dropped jobs would
	// also satisfy the assertion above.
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'jobs'`).Scan(&n))
	assert.Equal(t, 1, n, "jobs must still exist after the down migration")
}
