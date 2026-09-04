//go:build integration

package pgdsn

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// TestIntegration_HarnessDSNIsMigratedAndEmpty is the harness's own test. It
// exists so that a later test's RED is attributable: without it, a caller's
// failure could mean the harness never produced a usable database.
func TestIntegration_HarnessDSNIsMigratedAndEmpty(t *testing.T) {
	dsn := NewIntegrationDSN(t)

	conn, err := pgx.Connect(t.Context(), dsn)
	require.NoError(t, err)
	closeCtx, cancel := context.WithTimeout(context.Background(), TeardownTimeout)
	defer cancel()
	defer conn.Close(closeCtx)

	// Migrations ran: schema_migrations exists and carries a version.
	var version int64
	require.NoError(t, conn.QueryRow(t.Context(),
		`SELECT version FROM schema_migrations`).Scan(&version))
	require.Positive(t, version)

	// The database is this call's alone. Every list assertion a consumer lane
	// makes (Total: 1, one row rendered) is only meaningful against a
	// known-empty database, so this is the property the whole harness rests on.
	for _, table := range []string{"workers", "jobs", "tasks", "users", "task_logs"} {
		var n int64
		require.NoError(t, conn.QueryRow(t.Context(),
			`SELECT count(*) FROM `+table).Scan(&n), "counting %s", table)
		require.Zero(t, n, "table %s must be empty in a fresh test database", table)
	}
}
