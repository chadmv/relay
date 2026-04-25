-- name: UpsertWorkerWorkspace :exec
INSERT INTO worker_workspaces (worker_id, source_type, source_key, short_id, baseline_hash, last_used_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (worker_id, source_type, source_key) DO UPDATE
SET short_id = EXCLUDED.short_id,
    baseline_hash = EXCLUDED.baseline_hash,
    last_used_at = EXCLUDED.last_used_at;

-- name: DeleteWorkerWorkspace :exec
DELETE FROM worker_workspaces
WHERE worker_id = $1 AND source_type = $2 AND source_key = $3;

-- name: ListWorkerWorkspaces :many
SELECT * FROM worker_workspaces
WHERE worker_id = $1
ORDER BY source_key;

-- name: GetWorkerWorkspace :one
SELECT * FROM worker_workspaces
WHERE worker_id = $1 AND source_type = $2 AND source_key = $3;

-- name: ListWarmWorkspacesForKeys :many
-- Used by dispatcher's warm-preference scoring. $1 is source_type, $2 is an
-- array of source_keys observed in the current eligible-task batch.
SELECT * FROM worker_workspaces
WHERE source_type = $1 AND source_key = ANY($2::text[]);

-- name: ReplaceWorkerInventory :exec
-- On agent reconnect: delete all existing rows for this worker and reinsert.
-- Caller wraps in a transaction with subsequent UpsertWorkerWorkspace calls.
DELETE FROM worker_workspaces WHERE worker_id = $1;
