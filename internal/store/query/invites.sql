-- name: CreateInvite :one
INSERT INTO invites (token_hash, email, created_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = $1;

-- name: MarkInviteUsed :execrows
UPDATE invites
SET used_at = NOW(), used_by = $2
WHERE id = $1 AND used_at IS NULL;

-- name: ListInvitesPage :many
-- One page of the admin invite list, newest first. Every state is included -
-- active, expired and redeemed - because those are exactly what the Admin
-- Invites tab exists to show. There is no WHERE filter and no filter parameter;
-- if one is ever added, the sort+filter 400 rule at internal/api/jobs.go:417-422
-- becomes live for this endpoint.
--
-- The projection is EXPLICIT and deliberately omits i.token_hash. That omission
-- is the endpoint's entire security control: with the column absent from the
-- SELECT, the generated row type has no field for it, so returning it is a
-- compile error rather than a review miss. Never change this to SELECT *, and
-- never add token_hash "for debugging".
--
-- The JOIN to users is INNER, which is safe because users are archived, never
-- hard-deleted: no DELETE FROM users statement exists anywhere in
-- internal/store/query/. Precedent for the email projection is submitted_by_email
-- in jobs.sql:16,20.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.created_at, i.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListInvitesPageByCreatedAsc :many
-- The ascending arm of created_at. parseSort strips a leading '-' before
-- checking the allowlist (internal/api/pagination.go:178-181), so every key in
-- InvitesSortSpec.Keys is reachable in BOTH directions and each direction needs
-- its own statement and its own dispatch arm. A missing arm is a
-- client-triggerable panic, not a 400.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.created_at, i.id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.created_at ASC, i.id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountInvites :one
-- The `total` for the invites list. It carries the SAME join as the list
-- statements and the same (empty) filter predicate, so the pagination footer
-- can never state a number the client cannot page to. The join is redundant
-- against today's FK, and it is kept anyway so "total uses the list's own
-- predicate" is literally true rather than true by argument.
SELECT COUNT(*) FROM invites i JOIN users u ON u.id = i.created_by;

-- name: ListInvitesPageByExpiresDesc :many
-- The expires_at descending arm. invites.expires_at is NOT NULL
-- (000002_invites.up.sql:7), so unlike workers.last_seen_at this needs no
-- NULLS FIRST/LAST index pair and no cursor-null handling.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.expires_at, i.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.expires_at DESC, i.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListInvitesPageByExpiresAsc :many
-- The expires_at ascending arm. See ListInvitesPageByCreatedAsc for why both
-- directions of every allowlisted key need their own statement.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.expires_at, i.id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.expires_at ASC, i.id ASC
LIMIT sqlc.arg(page_limit)::int + 1;
