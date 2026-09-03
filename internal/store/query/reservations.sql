-- name: CreateReservation :one
INSERT INTO reservations (name, selector, worker_ids, user_id, project, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetReservation :one
SELECT * FROM reservations WHERE id = $1;

-- name: ListReservationsPage :many
-- The optional predicate below is sqlc.narg: a NULL argument means "no filter".
-- A Params field left at its zero value therefore disables the filter for this
-- statement while the other list arms keep filtering, silently and with no
-- error, which is what parseReservationFilters plus a single spread in
-- handleListReservations exists to prevent.
--
-- Containment (@>) rather than = ANY, even though no index is added here: the
-- two are equivalent for a single element, and only @> can be served by a GIN
-- index, so a later index needs no rewrite if one is ever added.
SELECT * FROM reservations
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountReservations :one
-- total is the count of every row matching every active predicate, independent
-- of the cursor.
SELECT COUNT(*) FROM reservations
WHERE sqlc.narg(worker_id)::uuid IS NULL
   OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid];

-- name: ListActiveReservations :many
SELECT * FROM reservations
WHERE (ends_at IS NULL OR ends_at > NOW())
  AND (starts_at IS NULL OR starts_at <= NOW())
ORDER BY created_at;

-- name: DeleteReservation :exec
DELETE FROM reservations WHERE id = $1;

-- name: ListReservationsPageByCreatedAsc :many
-- THE OUTER PARENTHESES AROUND THE CURSOR DISJUNCTION ARE LOAD-BEARING. Without
-- them,
-- `NOT cursor_set OR keyset AND filter` binds as
-- `NOT cursor_set OR (keyset AND filter)`, so on the FIRST page - where
-- cursor_set is false - the whole WHERE is satisfied before the filter is
-- reached and every row comes back unfiltered. A cursor-bearing request behaves
-- correctly against that bug, so only a no-cursor request discriminates, which
-- is why TestListReservations_WorkerFilterArms_FirstPage sends no cursor.
SELECT * FROM reservations
WHERE (NOT @cursor_set::bool OR (created_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY created_at ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByNameDesc :many
SELECT * FROM reservations
WHERE (NOT @cursor_set::bool OR (name, id) < (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY name DESC, id DESC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByNameAsc :many
SELECT * FROM reservations
WHERE (NOT @cursor_set::bool OR (name, id) > (@cursor_v::text, @cursor_id::uuid))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY name ASC, id ASC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByStartsDesc :many
-- DESC NULLS LAST. Cursor null -> in NULL tail (id < cursor_id, AND null).
-- Cursor non-null -> in non-null head; qualify non-nulls below cursor or any null.
--
-- THE OUTERMOST PARENTHESES ARE LOAD-BEARING AND ARE NOT THE ONES AROUND THE
-- CASE. Without them the appended AND binds to the CASE arm alone, so a
-- first-page request (cursor_set false) satisfies the WHERE before the filter is
-- reached and returns every row.
SELECT * FROM reservations
WHERE (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            starts_at IS NULL AND id < @cursor_id::uuid
       ELSE
            (starts_at IS NOT NULL AND
             (starts_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
         OR starts_at IS NULL
       END
   ))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY starts_at DESC NULLS LAST, id DESC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByStartsAsc :many
-- ASC NULLS FIRST. Mirror.
SELECT * FROM reservations
WHERE (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            (starts_at IS NULL AND id > @cursor_id::uuid)
         OR starts_at IS NOT NULL
       ELSE
            starts_at IS NOT NULL AND
            (starts_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
       END
   ))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY starts_at ASC NULLS FIRST, id ASC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByEndsDesc :many
-- DESC NULLS LAST. Cursor null -> in NULL tail (id < cursor_id, AND null).
-- Cursor non-null -> in non-null head; qualify non-nulls below cursor or any null.
SELECT * FROM reservations
WHERE (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            ends_at IS NULL AND id < @cursor_id::uuid
       ELSE
            (ends_at IS NOT NULL AND
             (ends_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
         OR ends_at IS NULL
       END
   ))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY ends_at DESC NULLS LAST, id DESC
LIMIT @page_limit + 1;

-- name: ListReservationsPageByEndsAsc :many
-- ASC NULLS FIRST. Mirror.
SELECT * FROM reservations
WHERE (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            (ends_at IS NULL AND id > @cursor_id::uuid)
         OR ends_at IS NOT NULL
       ELSE
            ends_at IS NOT NULL AND
            (ends_at, id) > (@cursor_ts::timestamptz, @cursor_id::uuid)
       END
   ))
  AND (sqlc.narg(worker_id)::uuid IS NULL
       OR worker_ids @> ARRAY[sqlc.narg(worker_id)::uuid])
ORDER BY ends_at ASC NULLS FIRST, id ASC
LIMIT @page_limit + 1;

-- name: RemoveWorkerFromReservations :execrows
-- Scrubs a deleted worker's id out of every reservation naming it.
-- reservations.worker_ids is a bare UUID[] with NO foreign key
-- (000001_initial.up.sql:89) - the one place a worker id can outlive its row.
--
-- THIS IS NOT A DISPATCH CORRECTNESS FIX and must not be sold as one. The
-- dispatcher's reservedIDs map (internal/scheduler/dispatch.go:185-191) is an
-- EXCLUSION set iterated over live workers rows, so a dangling id matches nothing
-- and withholds nothing. What this fixes is the contract - delete means "this id
-- ceases to exist" - and GET /v1/reservations showing a phantom.
--
-- THE WHERE CLAUSE IS NOT REDUNDANT WITH array_remove. Without it every
-- reservation is rewritten and the :execrows count becomes the table size instead
-- of "how many reservations named this worker", which is the number the delete
-- response reports and a test asserts.
--
-- A reservation whose array empties is LEFT ALONE: it becomes inert rather than
-- wrong, and deleting it would be a second destructive act the admin did not
-- request. README documents that limitation.
UPDATE reservations
SET worker_ids = array_remove(worker_ids, sqlc.arg(worker_id)::uuid)
WHERE sqlc.arg(worker_id)::uuid = ANY(worker_ids);
