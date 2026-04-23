//go:build integration

package schedrunner_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"relay/internal/schedrunner"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

type runnerHarness struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

func newRunnerHarness(t *testing.T) *runnerHarness {
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

	return &runnerHarness{pool: pool, q: store.New(pool)}
}

func (h *runnerHarness) createUser(t *testing.T, email string) pgtype.UUID {
	t.Helper()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := h.q.CreateUserWithPassword(context.Background(), store.CreateUserWithPasswordParams{
		Name: email, Email: email, IsAdmin: false, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user.ID
}

func makeSpecJSON(t *testing.T) []byte {
	t.Helper()
	spec, err := json.Marshal(map[string]any{
		"name":  "r",
		"tasks": []map[string]any{{"name": "t", "command": []string{"echo", "hi"}}},
	})
	require.NoError(t, err)
	return spec
}

func TestRunner_FiresEligibleSchedule(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	userID := h.createUser(t, "alice-fire@example.com")

	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly",
		OwnerID:       userID,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       makeSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	runner := schedrunner.NewRunner(h.pool, h.q)
	require.NoError(t, runner.TickOnce(ctx))

	jobs, err := h.q.ListJobsByScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()))
	require.True(t, row.LastRunAt.Valid)
	require.True(t, row.LastJobID.Valid)
}

func TestRunner_OverlapSkip(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	userID := h.createUser(t, "alice-overlap@example.com")

	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly",
		OwnerID:       userID,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       makeSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true},
	})
	require.NoError(t, err)

	// Pre-create a pending job attached to the schedule.
	_, err = h.q.CreateJob(ctx, store.CreateJobParams{
		Name:           "r",
		Priority:       "normal",
		SubmittedBy:    userID,
		Labels:         []byte(`{}`),
		ScheduledJobID: sj.ID,
	})
	require.NoError(t, err)

	runner := schedrunner.NewRunner(h.pool, h.q)
	require.NoError(t, runner.TickOnce(ctx))

	// Still only 1 job; next_run_at advanced; last_job_id not set (skip).
	jobs, err := h.q.ListJobsByScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()))
	require.False(t, row.LastJobID.Valid)
}

func TestRunner_ReconcileOnStartup_AdvancesPastMissedTriggers(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	userID := h.createUser(t, "alice-reconcile@example.com")

	oldNext := time.Now().Add(-25 * time.Hour)
	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly",
		OwnerID:       userID,
		CronExpr:      "0 2 * * *",
		Timezone:      "UTC",
		JobSpec:       makeSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     pgtype.Timestamptz{Time: oldNext, Valid: true},
	})
	require.NoError(t, err)

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, h.q))

	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()), "next_run_at should be in the future")
}
