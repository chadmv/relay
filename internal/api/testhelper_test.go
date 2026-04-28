//go:build integration

package api_test

import (
	"context"
	"fmt"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

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
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return store.New(pool)
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("relay_test"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}
