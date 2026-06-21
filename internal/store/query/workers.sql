-- name: CreateWorker :one
INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetWorker :one
SELECT * FROM workers WHERE id = $1;

-- name: GetWorkerByHostname :one
SELECT * FROM workers WHERE hostname = $1;

-- name: GetWorkerByHostnameForUpdate :one
SELECT * FROM workers WHERE hostname = $1 FOR UPDATE;

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

-- name: RegisterWorkerConnection :one
-- Marks the worker online and atomically allocates a fresh connection_epoch for
-- this connection. The returned connection_epoch is the value this connection
-- owns; all later teardown writes for this connection fence on it. Clears
-- disconnected_at because a reconnected worker has no live disconnect timestamp.
-- supports_workspaces is the authoritative per-connect capability write: a NULL
-- param (old agent that omits proto field 12) leaves the existing value.
UPDATE workers
SET status = 'online',
    last_seen_at = $2,
    disconnected_at = NULL,
    connection_epoch = connection_epoch + 1,
    supports_workspaces = COALESCE(sqlc.narg(supports_workspaces)::bool, supports_workspaces)
WHERE id = $1
RETURNING *;

-- name: MarkWorkerOfflineIfEpoch :execrows
-- Flip the worker offline only if connection_epoch still matches the epoch the
-- caller's connection owned. A stale teardown whose epoch has been superseded by
-- a fresh registration affects zero rows and leaves the live worker online.
UPDATE workers
SET status = 'offline',
    last_seen_at = $2,
    disconnected_at = $3
WHERE id = $1 AND connection_epoch = $4;

-- name: UpsertWorkerByHostname :one
-- Insert a new worker or update hardware specs on reconnect.
-- Admin-managed fields (name, labels, max_slots) are preserved on conflict.
INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, supports_workspaces)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg(supports_workspaces)::bool, TRUE))
ON CONFLICT (hostname) DO UPDATE
    SET cpu_cores = EXCLUDED.cpu_cores,
        ram_gb    = EXCLUDED.ram_gb,
        gpu_count = EXCLUDED.gpu_count,
        gpu_model = EXCLUDED.gpu_model,
        os        = EXCLUDED.os,
        supports_workspaces = COALESCE(sqlc.narg(supports_workspaces)::bool, workers.supports_workspaces)
RETURNING id, name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, max_slots, labels, status, last_seen_at, created_at, disabled_at, supports_workspaces;

-- name: SetWorkerAgentToken :exec
-- Sets the long-lived agent token on (re)enrollment. Clears revoked_at and, for
-- a previously revoked worker, resets status to 'offline' so revoked_at and the
-- revoked status are cleared together (regaining a valid token means the worker
-- is no longer revoked). This is the one place a revoked worker is revived
-- (revocation nulls the token, so the reconnect-by-token path can no longer find
-- it). The CASE leaves every non-revoked caller's status unchanged. 'offline' is
-- the natural not-yet-connected state; RegisterWorkerConnection flips it to
-- 'online' a moment later when the agent's connection registers.
UPDATE workers SET agent_token_hash = $2, revoked_at = NULL,
    status = CASE WHEN status = 'revoked' THEN 'offline' ELSE status END
WHERE id = $1;

-- name: ClearWorkerAgentToken :execrows
UPDATE workers
SET agent_token_hash = NULL, status = 'revoked', revoked_at = NOW()
WHERE id = $1;

-- name: GetWorkerByAgentTokenHash :one
SELECT * FROM workers
WHERE agent_token_hash = $1 AND status != 'revoked';

-- name: ListWorkersPage :many
SELECT * FROM workers
WHERE status != 'revoked'
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountWorkers :one
-- Total workers for the list endpoint. Excludes revoked workers so the count
-- matches the rows returned by the paginated list queries.
SELECT COUNT(*) FROM workers WHERE status != 'revoked';

-- name: ListWorkersByLiveness :many
-- Workers eligible for staleness sweeping: those currently connected.
SELECT * FROM workers WHERE status IN ('online', 'stale');

-- name: SetWorkerStatus :exec
-- Updates only the status column, leaving last_seen_at and disconnected_at
-- untouched. Used by the liveness sweeper for online<->stale transitions.
UPDATE workers SET status = $2 WHERE id = $1;

-- name: DisableWorker :execrows
-- Marks a worker disabled. Idempotent: the disabled_at IS NULL guard means a
-- second call affects zero rows and does not re-stamp the timestamp.
UPDATE workers SET disabled_at = NOW() WHERE id = $1 AND disabled_at IS NULL;

-- name: EnableWorker :execrows
-- Clears the disabled flag. Idempotent: affects zero rows if already enabled.
UPDATE workers SET disabled_at = NULL WHERE id = $1 AND disabled_at IS NOT NULL;

-- name: ListWorkersPageByCreatedAsc :many
SELECT * FROM workers
WHERE status != 'revoked'
  AND (NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
ORDER BY created_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListWorkersPageByNameDesc :many
SELECT * FROM workers
WHERE status != 'revoked'
  AND (NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid))
ORDER BY name DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListWorkersPageByNameAsc :many
SELECT * FROM workers
WHERE status != 'revoked'
  AND (NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid))
ORDER BY name ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListWorkersPageByStatusDesc :many
SELECT * FROM workers
WHERE status != 'revoked'
  AND (NOT @cursor_set::bool OR (status, id) < (@cursor_v::text, @cursor_id::uuid))
ORDER BY status DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListWorkersPageByStatusAsc :many
SELECT * FROM workers
WHERE status != 'revoked'
  AND (NOT @cursor_set::bool OR (status, id) > (@cursor_v::text, @cursor_id::uuid))
ORDER BY status ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListWorkersPageByLastSeenDesc :many
-- DESC NULLS LAST. Cursor predicate splits on whether the cursor's
-- last_seen value is null (@cursor_is_null::bool):
--   - cursor null: we're in the NULL tail; only rows that are also NULL
--     with id < cursor_id qualify.
--   - cursor non-null: we're in the non-null head; qualify any non-null
--     row with (last_seen_at, id) < (cursor_ts, cursor_id), OR any null
--     row (nulls come after in DESC NULLS LAST).
SELECT * FROM workers
WHERE status != 'revoked'
  AND (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            last_seen_at IS NULL AND id < @cursor_id::uuid
       ELSE
            (last_seen_at IS NOT NULL AND
             (last_seen_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
         OR last_seen_at IS NULL
       END
   ))
ORDER BY last_seen_at DESC NULLS LAST, id DESC
LIMIT @page_limit + 1;

-- name: ListWorkersPageByLastSeenAsc :many
-- ASC NULLS FIRST. Mirror image:
--   - cursor null: we're in the NULL head; qualify any null row with
--     id > cursor_id, OR any non-null row.
--   - cursor non-null: we're in the non-null tail; qualify non-null rows
--     with (last_seen_at, id) > (cursor_ts, cursor_id).
SELECT * FROM workers
WHERE status != 'revoked'
  AND (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            (last_seen_at IS NULL AND id > @cursor_id::uuid)
         OR last_seen_at IS NOT NULL
       ELSE
            last_seen_at IS NOT NULL AND
            (last_seen_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
       END
   ))
ORDER BY last_seen_at ASC NULLS FIRST, id ASC
LIMIT @page_limit + 1;

-- name: WorkerStatusCounts :one
-- Fleet-wide worker counts for the dashboard summary strip. "disabled" is an
-- overlay (disabled_at IS NOT NULL) that wins over the internal status, mirroring
-- toWorkerResponse. Revoked workers are excluded from every bucket and from the
-- total computed by the caller, matching the GET /v1/workers list endpoint -
-- including a worker that is both disabled and revoked (it counts in no bucket).
SELECT
    COUNT(*) FILTER (WHERE disabled_at IS NOT NULL AND status != 'revoked') AS disabled,
    COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'online')       AS online,
    COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'stale')        AS stale,
    COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'offline')      AS offline
FROM workers;

-- name: CountRevokedWorkers :one
SELECT COUNT(*) FROM workers WHERE status = 'revoked';

-- name: ListRevokedWorkersPage :many
-- Revoked workers for the admin audit endpoint, newest revocation first.
-- revoked_at is nullable (rows revoked before the column existed), so the
-- cursor predicate mirrors ListWorkersPageByLastSeenDesc's NULLS LAST handling.
SELECT * FROM workers
WHERE status = 'revoked'
  AND (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            revoked_at IS NULL AND id < @cursor_id::uuid
       ELSE
            (revoked_at IS NOT NULL AND
             (revoked_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
         OR revoked_at IS NULL
       END
   ))
ORDER BY revoked_at DESC NULLS LAST, id DESC
LIMIT @page_limit + 1;
