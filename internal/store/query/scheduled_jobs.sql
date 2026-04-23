-- name: CreateScheduledJob :one
INSERT INTO scheduled_jobs (
    name, owner_id, cron_expr, timezone, job_spec,
    overlap_policy, enabled, next_run_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetScheduledJob :one
SELECT * FROM scheduled_jobs WHERE id = $1;

-- name: ListScheduledJobs :many
SELECT * FROM scheduled_jobs ORDER BY created_at DESC;

-- name: ListScheduledJobsByOwner :many
SELECT * FROM scheduled_jobs WHERE owner_id = $1 ORDER BY created_at DESC;

-- name: UpdateScheduledJob :one
UPDATE scheduled_jobs
SET name           = $2,
    cron_expr      = $3,
    timezone       = $4,
    job_spec       = $5,
    overlap_policy = $6,
    enabled        = $7,
    next_run_at    = $8,
    updated_at     = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteScheduledJob :execrows
DELETE FROM scheduled_jobs WHERE id = $1;

-- name: ListEligibleScheduledJobs :many
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND next_run_at <= NOW()
 ORDER BY next_run_at ASC
 LIMIT $1
 FOR UPDATE SKIP LOCKED;

-- name: ListOverdueScheduledJobsForCatchup :many
SELECT * FROM scheduled_jobs
 WHERE enabled
   AND next_run_at < NOW();

-- name: AdvanceScheduledJob :exec
UPDATE scheduled_jobs
SET next_run_at = $2,
    last_run_at = NOW(),
    last_job_id = COALESCE($3, last_job_id),
    updated_at  = NOW()
WHERE id = $1;

-- name: CountActiveJobsForSchedule :one
SELECT COUNT(*) FROM jobs
 WHERE scheduled_job_id = $1
   AND status IN ('pending','queued','running','dispatched');
