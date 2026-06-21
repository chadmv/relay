//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/store"
)

// hotPathDownTarget is the schema version just below 000018_hot_path_indexes,
// i.e. the state its down migration restores.
const hotPathDownTarget = 17

// indexesAdded are the 5 indexes migration 000018 must create.
var indexesAdded = []string{
	"idx_task_deps_depends_on",
	"idx_tasks_worker_active",
	"idx_task_logs_task_id_id",
	"idx_jobs_status_updated",
	"idx_workers_status_disabled",
}

// indexesDropped are the 4 indexes migration 000018 must remove.
var indexesDropped = []string{
	"idx_task_logs_task_id",
	"idx_api_tokens_token_hash",
	"ix_agent_enrollments_token_hash",
	"ix_workers_agent_token_hash",
}

// publicIndexSet returns the set of index names in the public schema.
func publicIndexSet(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT indexname FROM pg_indexes WHERE schemaname = 'public'`)
	require.NoError(t, err)
	defer rows.Close()

	got := make(map[string]bool)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		got[name] = true
	}
	require.NoError(t, rows.Err())
	return got
}

// TestHotPathIndexes confirms migration 000018 added its 5 indexes and removed
// the 4 redundant ones after the full up migration (the newTestPool path).
func TestHotPathIndexes(t *testing.T) {
	pool := newTestPool(t)
	got := publicIndexSet(t, pool)

	for _, name := range indexesAdded {
		assert.True(t, got[name], "expected index %q to exist after up (check migration 000018)", name)
	}
	for _, name := range indexesDropped {
		assert.False(t, got[name], "expected index %q to be dropped by up (check migration 000018)", name)
	}
}

// TestHotPathIndexesDownUp confirms the 000018 down migration restores the
// original 4 indexes and removes the new 5, and that migrating back up
// round-trips cleanly (no duplicate-name collision on the second up).
func TestHotPathIndexesDownUp(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)

	// Roll back just 000018.
	require.NoError(t, store.MigrateTo(dsn, hotPathDownTarget),
		"down migration to 000017 must succeed")

	down := publicIndexSet(t, pool)
	for _, name := range indexesDropped {
		assert.True(t, down[name], "down must restore original index %q", name)
	}
	for _, name := range indexesAdded {
		assert.False(t, down[name], "down must remove new index %q", name)
	}

	// Re-apply 000018; a clean re-up proves the down left a consistent state.
	require.NoError(t, store.Migrate(dsn), "re-applying up after down must succeed")

	up := publicIndexSet(t, pool)
	for _, name := range indexesAdded {
		assert.True(t, up[name], "second up must re-create index %q", name)
	}
	for _, name := range indexesDropped {
		assert.False(t, up[name], "second up must re-drop index %q", name)
	}
}
