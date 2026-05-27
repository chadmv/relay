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

-- name: ListReservationsPageByCreatedAsc :many
SELECT * FROM reservations
WHERE NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
ORDER BY created_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByNameDesc :many
SELECT * FROM reservations
WHERE NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid)
ORDER BY name DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByNameAsc :many
SELECT * FROM reservations
WHERE NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid)
ORDER BY name ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByStartsDesc :many
-- DESC NULLS LAST. Cursor null -> in NULL tail (id < cursor_id, AND null).
-- Cursor non-null -> in non-null head; qualify non-nulls below cursor or any null.
SELECT * FROM reservations
WHERE NOT @cursor_set::bool
   OR (
       CASE WHEN @cursor_is_null::bool THEN
            starts_at IS NULL AND id < @cursor_id::uuid
       ELSE
            (starts_at IS NOT NULL AND
             (starts_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
         OR starts_at IS NULL
       END
   )
ORDER BY starts_at DESC NULLS LAST, id DESC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByStartsAsc :many
-- ASC NULLS FIRST. Mirror.
SELECT * FROM reservations
WHERE NOT @cursor_set::bool
   OR (
       CASE WHEN @cursor_is_null::bool THEN
            (starts_at IS NULL AND id > @cursor_id::uuid)
         OR starts_at IS NOT NULL
       ELSE
            starts_at IS NOT NULL AND
            (starts_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
       END
   )
ORDER BY starts_at ASC NULLS FIRST, id ASC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByEndsDesc :many
-- DESC NULLS LAST. Cursor null -> in NULL tail (id < cursor_id, AND null).
-- Cursor non-null -> in non-null head; qualify non-nulls below cursor or any null.
SELECT * FROM reservations
WHERE NOT @cursor_set::bool
   OR (
       CASE WHEN @cursor_is_null::bool THEN
            ends_at IS NULL AND id < @cursor_id::uuid
       ELSE
            (ends_at IS NOT NULL AND
             (ends_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
         OR ends_at IS NULL
       END
   )
ORDER BY ends_at DESC NULLS LAST, id DESC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByEndsAsc :many
-- ASC NULLS FIRST. Mirror.
SELECT * FROM reservations
WHERE NOT @cursor_set::bool
   OR (
       CASE WHEN @cursor_is_null::bool THEN
            (ends_at IS NULL AND id > @cursor_id::uuid)
         OR ends_at IS NOT NULL
       ELSE
            ends_at IS NOT NULL AND
            (ends_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
       END
   )
ORDER BY ends_at ASC NULLS FIRST, id ASC
LIMIT @page_limit + 1;
