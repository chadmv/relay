//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSortIndexesExist confirms migration 000013 created every composite
// index the configurable ?sort= feature depends on. Failing this test
// after adding a new sort key means the migration was not updated.
func TestSortIndexesExist(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx,
		`SELECT indexname FROM pg_indexes WHERE schemaname = 'public' ORDER BY indexname`)
	require.NoError(t, err)
	defer rows.Close()

	got := make(map[string]bool)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		got[name] = true
	}
	require.NoError(t, rows.Err())

	expected := []string{
		"idx_jobs_name_id", "idx_jobs_priority_id", "idx_jobs_status_id", "idx_jobs_updated_id",
		"idx_workers_name_id", "idx_workers_status_id", "idx_workers_last_seen_desc", "idx_workers_last_seen_asc",
		"idx_users_name_id", "idx_users_email_id",
		"idx_sched_jobs_name_id", "idx_sched_jobs_next_run_id", "idx_sched_jobs_updated_id",
		"idx_reservations_name_id", "idx_reservations_starts_desc", "idx_reservations_starts_asc",
		"idx_reservations_ends_desc", "idx_reservations_ends_asc",
		"idx_agent_enr_expires_id",
	}

	for _, name := range expected {
		assert.True(t, got[name], "expected pg_indexes to contain %q (check migration 000013)", name)
	}
}
