-- name: CreateJob :one
INSERT INTO jobs (name, priority, submitted_by, labels, scheduled_job_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1;

-- name: GetJobWithEmail :one
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.id = $1;

-- name: ListJobsWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobs :one
SELECT COUNT(*) FROM jobs;

-- name: ListJobsByStatusWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.status = sqlc.arg(status)::text
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByStatus :one
SELECT COUNT(*) FROM jobs WHERE status = $1;

-- name: ListJobsByScheduledJobWithEmailPage :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = sqlc.arg(scheduled_job_id)::uuid
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (j.created_at, j.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY j.created_at DESC, j.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountJobsByScheduledJob :one
SELECT COUNT(*) FROM jobs WHERE scheduled_job_id = $1;

-- name: UpdateJobStatus :one
UPDATE jobs
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = $1;

-- name: ListJobsByScheduledJob :many
-- Internal use only (schedrunner tests). Not paginated.
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = $1
ORDER BY j.created_at DESC;
