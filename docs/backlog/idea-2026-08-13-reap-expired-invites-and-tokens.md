---
title: Nothing ever reaps expired invites or api_tokens, while agent_enrollments has an hourly janitor
type: idea
status: open
created: 2026-08-13
priority: low
source: Phase 6 triage of the 2026-08-13-web-enabler-list-endpoints slice
---

# Nothing ever reaps expired invites or api_tokens

## Summary

`relay-server` runs an hourly janitor goroutine that deletes expired agent enrollments
(`runEnrollmentJanitor`, `cmd/relay-server/main.go:245-253`, calling
`DeleteExpiredAgentEnrollments`). There is no equivalent for `invites` or `api_tokens`. Both tables
grow monotonically for the life of a deployment: one row per invite ever created, one row per login
ever performed.

Nothing is broken today. This is a hygiene item filed so the growth path is tracked rather than
discovered.

## Context

Surfaced while specifying the two list endpoints. It shaped two decisions there, which is why it is
worth a file:

- **`GET /v1/auth/tokens` filters to live rows** partly because nothing reaps the dead ones, so an
  unfiltered list on a long-lived account would be mostly archaeology
  (`internal/store/query/tokens.sql:70-73` states this at the statement).
- **`GET /v1/invites` deliberately does not filter**, so its `COUNT(*)` runs over the full unreaped
  table on every request. That is small-number territory at farm scale - invites are hand-created by
  admins - but it is an unbounded scan with no janitor behind it.

The `api_tokens` case is the larger one in row count: every `relay login` and every SPA sign-in
inserts a row, and only `DELETE /v1/auth/tokens`, `PUT /v1/users/me/password` (via
`DeleteOtherTokensForUser`) and user archival ever remove any. A scripted login loop makes it grow
fast. Migration 000020's `idx_api_tokens_user_created_id` now indexes the table, so the practical
cost of the dead rows is storage and index bloat rather than scan time.

## Proposal

Extend the **existing** janitor rather than adding a second timer. `runEnrollmentJanitor` already
holds a `*store.Queries` and an hourly ticker; rename it to something table-neutral and add two
statements beside the enrollment one:

- `DELETE FROM invites WHERE expires_at < NOW() - <grace>` in `internal/store/query/invites.sql`
- `DELETE FROM api_tokens WHERE expires_at IS NOT NULL AND expires_at < NOW() - <grace>` in
  `internal/store/query/tokens.sql`

Two details that are decisions, not defaults:

1. **The `expires_at IS NOT NULL` guard on `api_tokens` is mandatory.** The column is nullable and a
   NULL means **never expires** (`internal/store/migrations/000001_initial.up.sql:18`;
   `internal/api/middleware.go:32-35` only rejects on `Valid && Before(now)`). A reaper written as
   `expires_at < NOW()` would leave NULL rows alone by SQL's three-valued logic, which is the right
   outcome by accident - state it explicitly so nobody "fixes" it into
   `NOT (expires_at > NOW())` and deletes every permanent credential in the system. This is the same
   trap the sessions list spec called out from the read side.
2. **A grace period, not an exact boundary.** Deleting a row the instant it expires destroys the
   evidence for "why was I signed out", and interacts badly with the two-clocks item
   ([[bug-2026-08-13-token-expiry-two-clocks]]). Something on the order of days, and configurable if
   the ops-timeout convention applies.

Redeemed invites (`used_at IS NOT NULL`) are a separate question from expired ones: they are the
audit trail of who invited whom, and reaping them loses that. Default to keeping them, or reap on a
much longer horizon.

## Acceptance / Done When

- One janitor goroutine reaps all three tables on the same ticker; no second timer is added.
- The `api_tokens` statement is proven not to delete a NULL-`expires_at` row, with a test that goes
  RED against the naive predicate. This is the only assertion in the item that must not be skipped.
- The grace period is a named constant or an env var, not an inline literal, and its value is
  justified in a comment.
- Redeemed-but-unexpired invites are explicitly either kept or reaped, with the choice stated.
- A row count assertion proves the reaper actually removed something, so a no-op reaper cannot pass.

## Related

- Source: `cmd/relay-server/main.go:245-253` (the existing janitor),
  `internal/store/query/invites.sql`, `internal/store/query/tokens.sql:74-110`
- Proposed by `docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md` (finding 9 and the
  "Scoped out" table); triaged in `docs/retros/2026-08-13-web-enabler-list-endpoints.md`
- Interacts with the expiry-boundary question: [[bug-2026-08-13-token-expiry-two-clocks]]

## Notes

Filed at low. The honest case for **rejecting** this item is that no deployment has reported a
problem and the tables are indexed, so the only cost today is disk. The case for keeping it is that
the NULL-expiry trap in the proposed statement is subtle enough to be worth writing down before
somebody implements the reaper from memory in fifteen minutes.
