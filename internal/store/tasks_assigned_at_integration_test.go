//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assignedAtDownTarget is the schema version just below 000021, i.e. the state
// its down migration restores.
const assignedAtDownTarget = 20

// TestMigration000021AddsAssignedAt asserts the column exists after the full up
// migration, is nullable, and is TIMESTAMPTZ.
func TestMigration000021AddsAssignedAt(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var dataType, isNullable string
	err := pool.QueryRow(ctx, `
		SELECT data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'tasks' AND column_name = 'assigned_at'`,
	).Scan(&dataType, &isNullable)
	require.NoError(t, err, "tasks.assigned_at must exist after migration 000021")
	assert.Equal(t, "timestamp with time zone", dataType)
	assert.Equal(t, "YES", isNullable,
		"assigned_at must be nullable: NULL is how a task says it holds no assignment")
}

// TestMigration000021DownUp confirms the down migration drops the column and
// migrating back up round-trips cleanly (no duplicate-column collision).
func TestMigration000021DownUp(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()

	countColumn := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name='tasks' AND column_name='assigned_at'`,
		).Scan(&n))
		return n
	}

	require.NoError(t, store.MigrateTo(dsn, assignedAtDownTarget),
		"down migration to 000020 must succeed")
	assert.Equal(t, 0, countColumn(), "down must drop tasks.assigned_at")

	require.NoError(t, store.Migrate(dsn), "re-applying up must succeed")
	assert.Equal(t, 1, countColumn(), "up must restore tasks.assigned_at")
}
