//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lastErrorDownTarget is the schema version just below 000022, i.e. the state
// its down migration restores.
const lastErrorDownTarget = 21

// TestMigration000022AddsLastError asserts both columns exist after the full up
// migration, are NULLABLE, and carry the intended types.
//
// NULLABLE IS THE WHOLE POINT AND IT IS NOT COSMETIC. Migrations are embedded
// and run on startup (cmd/relay-server/main.go), so a migration that can fail is
// a deployment that cannot start. A nullable ADD COLUMN with no DEFAULT, no
// CHECK and no backfill has no existing row it can reject and no expression it
// can fail to evaluate; in Postgres it is a catalog-only change that takes a
// brief ACCESS EXCLUSIVE lock and returns without rewriting the table, whatever
// its size. This is the same reasoning by which the retry-bounds slice declined
// a CHECK constraint on tasks.retries.
//
// NULL also carries meaning: it is how a schedule says "no recorded failure".
// A NOT NULL DEFAULT '' would make an empty string and an absent failure the
// same value, which is precisely what the response's `omitempty` relies on being
// distinguishable.
func TestMigration000022AddsLastError(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	for _, c := range []struct{ column, dataType string }{
		{"last_error", "text"},
		{"last_error_at", "timestamp with time zone"},
	} {
		var dataType, isNullable string
		err := pool.QueryRow(ctx, `
			SELECT data_type, is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'scheduled_jobs'
			  AND column_name = $1`, c.column,
		).Scan(&dataType, &isNullable)
		require.NoError(t, err, "scheduled_jobs.%s must exist after migration 000022", c.column)
		assert.Equal(t, c.dataType, dataType, "%s type", c.column)
		assert.Equal(t, "YES", isNullable,
			"%s must be nullable: NULL is how a schedule says it has no recorded failure", c.column)
	}
}

// TestMigration000022DownUp confirms the down migration drops both columns and
// migrating back up round-trips cleanly (no duplicate-column collision).
func TestMigration000022DownUp(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()

	countColumns := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name='scheduled_jobs'
			  AND column_name IN ('last_error','last_error_at')`,
		).Scan(&n))
		return n
	}

	require.NoError(t, store.MigrateTo(dsn, lastErrorDownTarget),
		"down migration to 000021 must succeed")
	assert.Equal(t, 0, countColumns(), "down must drop both columns")

	require.NoError(t, store.Migrate(dsn), "re-applying up must succeed")
	assert.Equal(t, 2, countColumns(), "up must restore both columns")
}
