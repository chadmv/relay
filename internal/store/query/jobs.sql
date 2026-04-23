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

-- name: ListJobs :many
SELECT * FROM jobs ORDER BY created_at DESC;

-- name: ListJobsWithEmail :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
ORDER BY j.created_at DESC;

-- name: ListJobsByStatus :many
SELECT * FROM jobs WHERE status = $1 ORDER BY created_at DESC;

-- name: ListJobsByStatusWithEmail :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.status = $1
ORDER BY j.created_at DESC;

-- name: UpdateJobStatus :one
UPDATE jobs
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = $1;

-- name: ListJobsByScheduledJob :many
SELECT j.*, u.email AS submitted_by_email
FROM jobs j
JOIN users u ON u.id = j.submitted_by
WHERE j.scheduled_job_id = $1
ORDER BY j.created_at DESC;
