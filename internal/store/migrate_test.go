//go:build integration

package store_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"
)

// TestMigrate's subject is store.Migrate itself, including its second,
// idempotent call, so it needs a database with no schema_migrations row -
// pgdsn.NewEmptyDSN, not NewIntegrationDSN, which would already have run it.
func TestMigrate(t *testing.T) {
	base := pgdsn.NewEmptyDSN(t)
	dsn := pgdsn.MigrateDSN(base)

	err := store.Migrate(dsn)
	require.NoError(t, err, "migration should succeed")

	// store.Migrate swallows migrate.ErrNoChange, so a no-op run (a database
	// that was already migrated) also returns nil - a bare require.NoError
	// above cannot tell that apart from a real migration having run. Assert
	// schema_migrations directly to pin that this first call is the one that
	// actually applied something.
	conn, connErr := pgx.Connect(t.Context(), base)
	require.NoError(t, connErr)
	defer conn.Close(t.Context())
	var count int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&count))
	require.Positive(t, count, "store.Migrate must have applied at least one migration")

	// Running again should be a no-op (ErrNoChange is swallowed)
	err = store.Migrate(dsn)
	require.NoError(t, err, "idempotent re-run should succeed")
}
