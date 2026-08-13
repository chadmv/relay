//go:build integration

package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/store"
)

// listEndpointIndexes are the three indexes migration 000020 adds to support
// GET /v1/invites and GET /v1/auth/tokens. The expected indexdef fragment is
// asserted, not just the name, because an index with the right name over the
// wrong columns supports nothing and would leave the keyset scans on a
// sequential plan while this test stayed green.
var listEndpointIndexes = []struct {
	table   string
	index   string
	columns string
}{
	{"invites", "idx_invites_created_id", "(created_at DESC, id DESC)"},
	{"invites", "idx_invites_expires_id", "(expires_at DESC, id DESC)"},
	{"api_tokens", "idx_api_tokens_user_created_id", "(user_id, created_at DESC, id DESC)"},
}

func TestMigration000020_ListEndpointIndexesExist(t *testing.T) {
	pool := newTestPool(t)
	ctx := t.Context()

	for _, tc := range listEndpointIndexes {
		var def string
		err := pool.QueryRow(ctx,
			`SELECT indexdef FROM pg_indexes
			 WHERE schemaname = 'public' AND tablename = $1 AND indexname = $2`,
			tc.table, tc.index).Scan(&def)
		require.NoError(t, err, "index %s on %s is missing (check migration 000020)", tc.index, tc.table)
		assert.Contains(t, def, tc.columns,
			"index %s exists but over the wrong columns: %s", tc.index, def)
	}
}

// The down migration must actually drop what the up migration created.
// store.MigrateTo drives golang-migrate down past the up-only startup path;
// migrate_down_test.go:24-51 established the fixture.
func TestMigration000020_DownDropsListEndpointIndexes(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := t.Context()

	// Positive control: they exist before the down migration runs, so a green
	// result below cannot come from them never having been created.
	for _, tc := range listEndpointIndexes {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`,
			tc.index).Scan(&n))
		require.Equal(t, 1, n, "fixture: %s must exist before the down migration", tc.index)
	}

	require.NoError(t, storeMigrateTo(dsn, 19))

	for _, tc := range listEndpointIndexes {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`,
			tc.index).Scan(&n))
		assert.Equal(t, 0, n, "%s must be dropped by 000020_list_endpoint_indexes.down.sql", tc.index)
	}

	// The tables themselves must survive: a down migration that dropped the
	// table would also satisfy every assertion above.
	for _, table := range []string{"invites", "api_tokens"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = $1`, table).Scan(&n))
		assert.Equal(t, 1, n, "%s must still exist after the down migration", table)
	}
}

// storeMigrateTo is a one-line alias so the import of relay/internal/store does
// not shadow the package-level helpers this file already uses.
func storeMigrateTo(dsn string, version uint) error { return store.MigrateTo(dsn, version) }
