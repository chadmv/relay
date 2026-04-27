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
SET status = $2, last_seen_at = $3, disconnected_at = $4
WHERE id = $1
RETURNING *;

-- name: UpsertWorkerByHostname :one
-- Insert a new worker or update hardware specs on reconnect.
-- Admin-managed fields (name, labels, max_slots) are preserved on conflict.
INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (hostname) DO UPDATE
    SET cpu_cores = EXCLUDED.cpu_cores,
        ram_gb    = EXCLUDED.ram_gb,
        gpu_count = EXCLUDED.gpu_count,
        gpu_model = EXCLUDED.gpu_model,
        os        = EXCLUDED.os
RETURNING id, name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, max_slots, labels, status, last_seen_at, created_at;

-- name: SetWorkerAgentToken :exec
UPDATE workers SET agent_token_hash = $2 WHERE id = $1;

-- name: ClearWorkerAgentToken :execrows
UPDATE workers
SET agent_token_hash = NULL, status = 'revoked'
WHERE id = $1;

-- name: GetWorkerByAgentTokenHash :one
SELECT * FROM workers
WHERE agent_token_hash = $1 AND status != 'revoked';
