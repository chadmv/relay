-- name: CreateReservation :one
INSERT INTO reservations (name, selector, worker_ids, user_id, project, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetReservation :one
SELECT * FROM reservations WHERE id = $1;

-- name: ListReservations :many
SELECT * FROM reservations ORDER BY created_at DESC;

-- name: ListActiveReservations :many
SELECT * FROM reservations
WHERE (ends_at IS NULL OR ends_at > NOW())
  AND (starts_at IS NULL OR starts_at <= NOW())
ORDER BY created_at;

-- name: DeleteReservation :exec
DELETE FROM reservations WHERE id = $1;
