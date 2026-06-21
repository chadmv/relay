//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"relay/internal/store"
)

// seedUserAndJob inserts a user and a valid job, returning their ids. Used to
// satisfy FK constraints when probing tasks/task_logs.
func seedUserAndJob(t *testing.T, pool *pgxpool.Pool) (userID, jobID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"u-"+t.Name(), t.Name()+"@example.com").Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO jobs (name, submitted_by) VALUES ('j', $1) RETURNING id`,
		userID).Scan(&jobID))
	return userID, jobID
}

// TestStatusVocabularyConstraints_Reject confirms migration 000019's six CHECK
// constraints reject an out-of-vocabulary value on each column.
func TestStatusVocabularyConstraints_Reject(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	userID, jobID := seedUserAndJob(t, pool)

	var taskID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO tasks (job_id, name) VALUES ($1, 'tt') RETURNING id`,
		jobID).Scan(&taskID))

	// workers.status
	_, err := pool.Exec(ctx,
		`INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, status)
		 VALUES ('w', 'w-host', 4, 16, 0, '', 'linux', 'bogus')`)
	require.Error(t, err, "workers_status_check must reject 'bogus'")

	// jobs.status
	_, err = pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, status) VALUES ('j2', $1, 'dispatched')`, userID)
	require.Error(t, err, "jobs_status_check must reject 'dispatched'")

	// jobs.priority
	_, err = pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, priority) VALUES ('j3', $1, 'hgih')`, userID)
	require.Error(t, err, "jobs_priority_check must reject 'hgih'")

	// tasks.status
	_, err = pool.Exec(ctx,
		`INSERT INTO tasks (job_id, name, status) VALUES ($1, 'tt2', 'queued')`, jobID)
	require.Error(t, err, "tasks_status_check must reject 'queued'")

	// task_logs.stream
	_, err = pool.Exec(ctx,
		`INSERT INTO task_logs (task_id, stream, content) VALUES ($1, 'syslog', 'x')`, taskID)
	require.Error(t, err, "task_logs_stream_check must reject 'syslog'")

	// scheduled_jobs.overlap_policy
	_, err = pool.Exec(ctx,
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, overlap_policy, next_run_at)
		 VALUES ('s', $1, '@daily', '{}'::jsonb, 'maybe', NOW())`, userID)
	require.Error(t, err, "scheduled_jobs_overlap_policy_check must reject 'maybe'")
}

// TestStatusVocabularyConstraints_AcceptValid confirms each constrained column
// still accepts its in-vocabulary values after 000019.
func TestStatusVocabularyConstraints_AcceptValid(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	userID, jobID := seedUserAndJob(t, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, status)
		 VALUES ('w', 'w-host', 4, 16, 0, '', 'linux', 'stale')`)
	require.NoError(t, err, "workers_status_check must accept 'stale'")

	_, err = pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, status, priority) VALUES ('j2', $1, 'cancelled', 'high')`, userID)
	require.NoError(t, err, "jobs_status_check/priority_check must accept 'cancelled'/'high'")

	_, err = pool.Exec(ctx,
		`INSERT INTO tasks (job_id, name, status) VALUES ($1, 'tt2', 'timed_out')`, jobID)
	require.NoError(t, err, "tasks_status_check must accept 'timed_out'")

	var taskID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO tasks (job_id, name) VALUES ($1, 'tt') RETURNING id`, jobID).Scan(&taskID))
	_, err = pool.Exec(ctx,
		`INSERT INTO task_logs (task_id, stream, content) VALUES ($1, 'stderr', 'x')`, taskID)
	require.NoError(t, err, "task_logs_stream_check must accept 'stderr'")

	_, err = pool.Exec(ctx,
		`INSERT INTO scheduled_jobs (name, owner_id, cron_expr, job_spec, overlap_policy, next_run_at)
		 VALUES ('s', $1, '@daily', '{}'::jsonb, 'allow', NOW())`, userID)
	require.NoError(t, err, "scheduled_jobs_overlap_policy_check must accept 'allow'")
}

// TestStatusVocabularyConstraints_RoundTrip confirms 000019 up normalizes a
// drifted priority and down removes the constraints. It drives golang-migrate
// down to 000018 then back up to confirm the round-trip is clean.
func TestStatusVocabularyConstraints_RoundTrip(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := context.Background()

	// After full migration, the constraints exist: a bogus job priority is rejected.
	userID, _ := seedUserAndJob(t, pool)
	_, err := pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, priority) VALUES ('jbad', $1, 'urgent')`, userID)
	require.Error(t, err, "jobs_priority_check should be present after up")

	// Migrate down past 000019 (to 000018 = version 18): constraints dropped.
	require.NoError(t, store.MigrateTo(dsn, 18))
	_, err = pool.Exec(ctx,
		`INSERT INTO jobs (name, submitted_by, priority) VALUES ('jbad2', $1, 'urgent')`, userID)
	require.NoError(t, err, "after down to 000018, jobs_priority_check should be gone")

	// Seed a drifted priority so the up normalization has something to fix.
	_, err = pool.Exec(ctx,
		`UPDATE jobs SET priority = 'sometypo' WHERE name = 'jbad2'`)
	require.NoError(t, err)

	// Migrate back up: the UPDATE in 000019 normalizes the drift to 'normal'
	// before adding the constraint, so the up succeeds.
	require.NoError(t, store.MigrateTo(dsn, 19),
		"000019 up must normalize drifted priority before constraining")

	var got string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT priority FROM jobs WHERE name = 'jbad2'`).Scan(&got))
	require.Equal(t, "normal", got, "drifted priority must be normalized to 'normal'")
}
