---
title: POST /v1/reservations returns 500 for a nonexistent user_id, and validates almost nothing
type: bug
status: open
created: 2026-08-09
priority: medium
source: Spec phase of the admin reservations tab iteration (2026-08-09)
---

# POST /v1/reservations returns 500 for a nonexistent user_id, and validates almost nothing

## Summary
`handleCreateReservation` validates only that `name` is non-empty and that any supplied UUIDs parse.
Every error from `CreateReservation` then funnels to a single 500, so a **client** mistake - a
well-formed `user_id` that does not identify a user - surfaces as a server error via the users
foreign key rather than a 400. Nothing else is validated either: `worker_ids` need not identify real
workers, and a time window may end before it starts.

## Repro / Symptoms
`POST /v1/reservations` as an admin with `{"name":"x","user_id":"<valid UUID, no such user>"}` returns
**500**, not 400. A client cannot distinguish "you sent a bad id" from "the server is broken", so a
UI either has to present a server-error banner for what is really a field error, or pre-validate
against a separate endpoint. The shipped admin Reservations tab sidesteps it by never sending
`user_id` at all.

Related, from the same handler:
- `worker_ids` are not checked against the workers table (there is no FK), so a reservation can name
  workers that do not exist. Those ids simply never match anything in the dispatcher's exclusion set,
  so the reservation silently reserves nothing.
- `starts_at` / `ends_at` are not checked against each other, so an inverted window (`ends_at` before
  `starts_at`) is accepted and stored. It is never active, so it is inert but permanently confusing -
  the SPA has to derive an `ENDED` status for it specifically.

## Context
Found while specifying the admin Reservations tab (2026-08-09), which ships against this handler.
Worth noting the sibling endpoint has the mirror-image problem: creating an agent enrollment likewise
funnels errors to a 500. So the underlying issue may be a shared "wrap every store error as 500"
habit rather than one handler, and is worth checking across `internal/api/` before fixing just this
one.

## Proposal
Distinguish client errors from server errors in `handleCreateReservation`:

- Map a users-FK violation on `user_id` to **400** (or 404) with a message naming the field. pgx
  surfaces this as a `*pgconn.PgError` with SQLSTATE `23503`; check how the codebase already
  distinguishes `23505` for the duplicate-email case in `internal/api/users.go` and follow that
  precedent rather than inventing a second style.
- Reject an inverted window with a 400.
- Decide deliberately whether `worker_ids` should be validated against existing workers. There is an
  argument for leaving it permissive (a reservation could be created before an agent enrolls), in
  which case say so in a comment - but silently reserving nothing is a poor default either way.

Never leak the raw Postgres error text to the client; `writeError` messages should stay
caller-facing. Note that `pgconn.PgError.Error()` omits `Detail`, which is where Postgres puts
"Failing row contains (...)" - preserve that.

## Acceptance / Done When
- A well-formed but nonexistent `user_id` returns a 4xx naming the field, proven by a test that is
  RED against today's handler.
- An inverted time window is rejected with a 400.
- The `worker_ids` decision is either enforced or documented in the handler.
- No raw Postgres error text reaches the response body.

## Related
- Surfaced by `docs/superpowers/specs/2026-08-09-admin-reservations-tab.md`
- Source: `internal/api/reservations.go` (`handleCreateReservation`),
  `internal/store/query/reservations.sql`, `internal/scheduler/dispatch.go` (the only reader of
  reservations)
- Precedent for mapping a PgError to a 4xx: the duplicate-email path in `internal/api/users.go`
- Sibling with the same funnel-to-500 shape: `internal/api/agent_enrollments.go`

## Notes
The shipped Reservations tab does not expose `user_id` at all, partly for this reason and partly
because the field is inert (a reservation excludes its workers from dispatch entirely and does not
route the owner's work anywhere). If that inertness is ever addressed, this validation gap becomes
user-visible rather than merely latent.
