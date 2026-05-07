-- name: CreateReservation :one
INSERT INTO reservations (name, selector, worker_ids, user_id, project, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetReservation :one
SELECT * FROM reservations WHERE id = $1;

-- name: ListReservationsPage :many
SELECT * FROM reservations
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountReservations :one
SELECT COUNT(*) FROM reservations;

-- name: ListActiveReservations :many
SELECT * FROM reservations
WHERE (ends_at IS NULL OR ends_at > NOW())
  AND (starts_at IS NULL OR starts_at <= NOW())
ORDER BY created_at;

-- name: DeleteReservation :exec
DELETE FROM reservations WHERE id = $1;
