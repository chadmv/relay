//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
	"relay/internal/store"
)

// newTestQueries spins up a fresh Postgres container, runs migrations,
// and returns a *store.Queries ready for use. The container is terminated when t ends.
func newTestQueries(t *testing.T) *store.Queries {
	t.Helper()
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

	migrateDSN := "pgx5" + dsn[len("postgres"):]
	require.NoError(t, store.Migrate(migrateDSN))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return store.New(pool)
}

func newTestUser(t *testing.T, q *store.Queries, isAdmin bool) store.User {
	t.Helper()
	ctx := context.Background()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	name := "user-" + t.Name()
	email := name + "@example.com"
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: isAdmin, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user
}

func newTestWorker(t *testing.T, q *store.Queries) store.Worker {
	t.Helper()
	ctx := context.Background()
	hostname := "test-worker-" + t.Name()
	row, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: hostname, Hostname: hostname, CpuCores: 4, RamGb: 16,
		GpuCount: 0, GpuModel: "", Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.GetWorker(ctx, row.ID)
	require.NoError(t, err)
	return w
}

func ptrStr(s string) *string { return &s }
