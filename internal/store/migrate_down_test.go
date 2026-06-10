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

	"relay/internal/store"
)

// taskCommandsDownTarget is the schema version just below 000008_task_commands,
// i.e. the state its down migration restores (tasks.command TEXT[]).
const taskCommandsDownTarget = 7

// newMigratedPoolWithDSN mirrors newTestPool but also returns the pgx5:// DSN
// so tests can drive golang-migrate past the startup up-only path.
func newMigratedPoolWithDSN(t *testing.T) (*pgxpool.Pool, string) {
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

	return pool, migrateDSN
}

func seedJob(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var userID, jobID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"u-"+t.Name(), t.Name()+"@example.com").Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO jobs (name, submitted_by) VALUES ('j', $1) RETURNING id`,
		userID).Scan(&jobID))
	return jobID
}

func seedTask(t *testing.T, pool *pgxpool.Pool, jobID, name, commandsJSON string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO tasks (job_id, name, commands) VALUES ($1, $2, $3::jsonb)`,
		jobID, name, commandsJSON)
	require.NoError(t, err)
}

func TestMigrateDownTaskCommands_MultiCommandFailsLoudly(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	jobID := seedJob(t, pool)
	seedTask(t, pool, jobID, "multi", `[["echo","a"],["echo","b"]]`)

	err := store.MigrateTo(dsn, taskCommandsDownTarget)
	require.Error(t, err, "down migration must refuse to silently drop multi-command data")
	require.Contains(t, err.Error(), "multi-command")
}

func TestMigrateDownTaskCommands_SingleAndEmptyRollBack(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	jobID := seedJob(t, pool)
	seedTask(t, pool, jobID, "single", `[["echo","hello"]]`)
	seedTask(t, pool, jobID, "empty", `[]`)

	require.NoError(t, store.MigrateTo(dsn, taskCommandsDownTarget),
		"single-command and empty rows are representable as TEXT[] and must roll back cleanly")

	var cmd []string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT command FROM tasks WHERE name = 'single'`).Scan(&cmd))
	require.Equal(t, []string{"echo", "hello"}, cmd)
}
