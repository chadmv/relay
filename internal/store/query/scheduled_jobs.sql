-- name: CreateScheduledJob :one
INSERT INTO scheduled_jobs (
    name, owner_id, cron_expr, timezone, job_spec,
    overlap_policy, enabled, next_run_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetScheduledJob :one
SELECT * FROM scheduled_jobs WHERE id = $1;

-- name: ListScheduledJobsPage :many
SELECT * FROM scheduled_jobs
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobs :one
SELECT COUNT(*) FROM scheduled_jobs;

-- name: ListScheduledJobsByOwnerPage :many
SELECT * FROM scheduled_jobs
WHERE owner_id = sqlc.arg(owner_id)::uuid
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountScheduledJobsByOwner :one
SELECT COUNT(*) FROM scheduled_jobs WHERE owner_id = $1;

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

-- name: ListScheduledJobsPageByCreatedAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY created_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNameDesc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY name DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNameAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY name ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNextRunDesc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (next_run_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY next_run_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByNextRunAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (next_run_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY next_run_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByUpdatedDesc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (updated_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY updated_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsPageByUpdatedAsc :many
SELECT * FROM scheduled_jobs
WHERE NOT @cursor_set::bool OR (updated_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY updated_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByCreatedAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY created_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNameDesc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid))
ORDER BY name DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNameAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid))
ORDER BY name ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNextRunDesc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (next_run_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY next_run_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByNextRunAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (next_run_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY next_run_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByUpdatedDesc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (updated_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY updated_at DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListScheduledJobsByOwnerPageByUpdatedAsc :many
SELECT * FROM scheduled_jobs
WHERE owner_id = @owner_id::uuid
  AND (NOT @cursor_set::bool OR (updated_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY updated_at ASC, id ASC
LIMIT @page_limit + 1;
