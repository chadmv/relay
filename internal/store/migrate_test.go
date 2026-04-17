//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"relay/internal/store"
)

func TestMigrate(t *testing.T) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx,
		"postgres:16",
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

	// Replace postgres:// scheme with pgx5:// for golang-migrate pgx/v5 driver
	dsn = "pgx5" + dsn[len("postgres"):]

	err = store.Migrate(dsn)
	require.NoError(t, err, "migration should succeed")

	// Running again should be a no-op (ErrNoChange is swallowed)
	err = store.Migrate(dsn)
	require.NoError(t, err, "idempotent re-run should succeed")
}
