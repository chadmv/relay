//go:build integration

package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"relay/internal/store"
	"relay/internal/testsupport/pgdsn"
)

// TestMigrate's subject is store.Migrate itself, including its second,
// idempotent call, so it needs a database with no schema_migrations row -
// pgdsn.NewEmptyDSN, not NewIntegrationDSN, which would already have run it.
func TestMigrate(t *testing.T) {
	dsn := pgdsn.MigrateDSN(pgdsn.NewEmptyDSN(t))

	err := store.Migrate(dsn)
	require.NoError(t, err, "migration should succeed")

	// Running again should be a no-op (ErrNoChange is swallowed)
	err = store.Migrate(dsn)
	require.NoError(t, err, "idempotent re-run should succeed")
}
