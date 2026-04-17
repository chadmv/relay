//go:build integration

package main

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

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

func TestBootstrapAdmin_NoUsers_CreatesAdmin(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
	assert.Equal(t, "admin@example.com", user.Email)
}

func TestBootstrapAdmin_ExistingUser_Promotes(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	_, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Bob", Email: "admin@example.com", IsAdmin: false,
	})
	require.NoError(t, err)

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
}

func TestBootstrapAdmin_AdminAlreadyExists_Skips(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	_, err := q.CreateUser(ctx, store.CreateUserParams{
		Name: "Existing Admin", Email: "other@example.com", IsAdmin: true,
	})
	require.NoError(t, err)

	require.NoError(t, bootstrapAdmin(ctx, q, "new@example.com"))

	// The requested email must NOT have been created.
	_, err = q.GetUserByEmail(ctx, "new@example.com")
	require.Error(t, err, "expected no user created when an admin already exists")
}
