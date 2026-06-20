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

func makeSourceSpecJSON(t *testing.T) []byte {
	t.Helper()
	spec, err := json.Marshal(map[string]any{
		"name": "src-job",
		"tasks": []map[string]any{{
			"name":    "t",
			"command": []string{"true"},
			"source": map[string]any{
				"type":   "perforce",
				"stream": "//streams/X/main",
				"sync": []map[string]any{
					{"path": "//streams/X/main/...", "rev": "#head"},
				},
			},
		}},
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

func TestRunner_ReconcileOnStartup_DoesNotSetLastRunAt(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	userID := h.createUser(t, "alice-reconcile-lastrun@example.com")

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

	// Sanity: freshly created schedule has no last_run_at.
	before, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.False(t, before.LastRunAt.Valid, "precondition: last_run_at unset on create")

	require.NoError(t, schedrunner.ReconcileOnStartup(ctx, h.q))

	row, err := h.q.GetScheduledJob(ctx, sj.ID)
	require.NoError(t, err)
	require.True(t, row.NextRunAt.Time.After(time.Now()), "next_run_at should advance")
	require.False(t, row.LastRunAt.Valid, "reconcile must NOT set last_run_at for a run that never happened")
}

// insertScheduledJobBogusOwner plants a poison scheduled_jobs row whose owner_id
// references no user. scheduled_jobs.owner_id has an FK to users(id) that rejects
// a bogus owner on insert, so we momentarily drop that one constraint, insert the
// poison, then restore it. The jobs.submitted_by FK to users(id) is untouched, so
// when fireOne runs CreateJob for this row the FK insert fails inside the
// savepoint - the DB-layer failure that would abort the whole tx without one.
func (h *runnerHarness) insertScheduledJobBogusOwner(t *testing.T, name string, spec []byte, next time.Time) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	bogusOwner := pgtype.UUID{Bytes: [16]byte{0xde, 0xad, 0xbe, 0xef}, Valid: true}

	_, err := h.pool.Exec(ctx, `ALTER TABLE scheduled_jobs DROP CONSTRAINT scheduled_jobs_owner_id_fkey`)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Restore the constraint regardless of how the test exits (including t.Fatal
		// mid-insert). NOT VALID avoids a full table scan and matches the original.
		_, _ = h.pool.Exec(context.Background(),
			`ALTER TABLE scheduled_jobs ADD CONSTRAINT scheduled_jobs_owner_id_fkey
			   FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID`)
	})

	var id pgtype.UUID
	err = h.pool.QueryRow(ctx,
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, timezone, job_spec, overlap_policy, enabled, next_run_at)
		 VALUES ($1,$2,'@hourly','UTC',$3,'skip',true,$4) RETURNING id`,
		name, bogusOwner, spec, next).Scan(&id)
	require.NoError(t, err)

	return id
}

func TestRunner_PoisonedScheduleDoesNotAbortHealthyOne(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	healthyOwner := h.createUser(t, "healthy@example.com")

	overdue := pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Second), Valid: true}

	// Poison: a scheduled job whose owner_id references no user, so fireOne's
	// CreateJob fails on the jobs.submitted_by FK. The DB error would abort the
	// outer tx without a savepoint. The poison sorts FIRST (older next_run_at) to
	// prove it does not starve the healthy one.
	poisonID := h.insertScheduledJobBogusOwner(t, "poison", makeSpecJSON(t), time.Now().Add(-10*time.Second))

	healthy, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "healthy",
		OwnerID:       healthyOwner,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       makeSpecJSON(t),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     overdue,
	})
	require.NoError(t, err)

	runner := schedrunner.NewRunner(h.pool, h.q)
	require.NoError(t, runner.TickOnce(ctx), "tick must commit despite one poisoned schedule")

	// Healthy schedule committed its job + advance.
	healthyJobs, err := h.q.ListJobsByScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	require.Len(t, healthyJobs, 1, "healthy schedule must commit its job")

	healthyRow, err := h.q.GetScheduledJob(ctx, healthy.ID)
	require.NoError(t, err)
	require.True(t, healthyRow.NextRunAt.Time.After(time.Now()), "healthy next_run_at must advance")
	require.True(t, healthyRow.LastRunAt.Valid, "healthy last_run_at must be set")

	// Poison schedule created no job but still advanced (no hot-loop), and
	// did NOT falsely record last_run_at.
	poisonJobs, err := h.q.ListJobsByScheduledJob(ctx, poisonID)
	require.NoError(t, err)
	require.Len(t, poisonJobs, 0, "poison schedule must create no job")

	poisonRow, err := h.q.GetScheduledJob(ctx, poisonID)
	require.NoError(t, err)
	require.True(t, poisonRow.NextRunAt.Time.After(time.Now()), "poison next_run_at must advance so it does not hot-loop")
	require.False(t, poisonRow.LastRunAt.Valid, "poison last_run_at must stay unset (no run happened)")
}

func TestRunner_FiresScheduleWithSource_PersistsSource(t *testing.T) {
	h := newRunnerHarness(t)
	ctx := context.Background()
	userID := h.createUser(t, "alice-source@example.com")

	sj, err := h.q.CreateScheduledJob(ctx, store.CreateScheduledJobParams{
		Name:          "nightly-source",
		OwnerID:       userID,
		CronExpr:      "@hourly",
		Timezone:      "UTC",
		JobSpec:       makeSourceSpecJSON(t),
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

	tasks, err := h.q.ListTasksByJob(ctx, jobs[0].ID)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.NotNil(t, tasks[0].Source, "cron-fired task must persist its source spec")
	require.Contains(t, string(tasks[0].Source), `"//streams/X/main"`)
}
