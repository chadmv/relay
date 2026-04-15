-- name: CreateJob :one
INSERT INTO jobs (name, priority, submitted_by, labels)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1;

-- name: ListJobs :many
SELECT * FROM jobs ORDER BY created_at DESC;

-- name: ListJobsByStatus :many
SELECT * FROM jobs WHERE status = $1 ORDER BY created_at DESC;

-- name: UpdateJobStatus :one
UPDATE jobs
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = $1;
