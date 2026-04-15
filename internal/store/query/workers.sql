-- name: CreateWorker :one
INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetWorker :one
SELECT * FROM workers WHERE id = $1;

-- name: GetWorkerByHostname :one
SELECT * FROM workers WHERE hostname = $1;

-- name: ListWorkers :many
SELECT * FROM workers ORDER BY name;

-- name: UpdateWorker :one
UPDATE workers
SET name = $2, labels = $3, max_slots = $4
WHERE id = $1
RETURNING *;

-- name: UpdateWorkerStatus :one
UPDATE workers
SET status = $2, last_seen_at = $3
WHERE id = $1
RETURNING *;
