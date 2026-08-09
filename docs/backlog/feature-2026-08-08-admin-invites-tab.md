---
title: "Admin console: Invites tab (create now, list blocked on GET /v1/invites)"
type: feature
status: open
created: 2026-08-08
priority: high
source: carved from feature-2026-06-26-admin-console-pages during the 2026-08-08 admin-console-shell-users-tab slice
---

# Admin console: Invites tab (create now, list blocked on GET /v1/invites)

## Summary
The Invites tab of the admin console. The create half - a form plus a token-on-create modal showing
the raw invite token clear-text exactly once - is buildable today. The list half is backend-blocked:
`GET /v1/invites` does not exist.

## Context
Carved out of the admin-console omnibus item when the 2026-08-08 slice shipped the `/admin/:tab` shell
plus the Users tab. Design source of truth is `AdminInvites` in
`design_handoff_relay_holo/hifi3-holo-pages.jsx` (line 2083) and the shared `AdminTokenModal`
(line 2340); `reference/screens/admin.js` is structure-only.

## Partially blocked
- **Create: unblocked.** `POST /v1/invites` exists (admin-only, `internal/api/server.go:139`,
  handler `internal/api/invites.go:16`). Body `{ email?, expires_in? }` where `expires_in` is a Go
  duration string, default `72h`, **max `720h`** (30d) - it rejects longer with
  `expires_in exceeds maximum of 720h`. A non-empty `email` is validated with `mail.ParseAddress` and
  pins the invite to that address. Response is 201 `{ id, token, expires_at, email? }`; `token` is the
  raw hex and is never retrievable again.
- **List: BLOCKED** on `GET /v1/invites`, tracked in
  [[feature-2026-06-26-web-enabler-backend-endpoints]]. `internal/store/query/invites.sql` has only
  Create / GetByTokenHash / MarkUsed, so a `ListInvites` query plus active / expiring / expired /
  redeemed state derivation is needed first.

## Proposal
Ship in two steps rather than waiting for the backend:
1. **Now:** register an `invites` tab that contains only the create flow - a form (optional email,
   `expires_in` preset pills 24h / 72h / 7d / 30d) and the copy-once token modal with the "returned
   once, cannot be retrieved again" warning. Where the table would go, render an honest empty state
   explaining that listing existing invites requires a server endpoint that does not exist yet - do
   not render a fake or perpetually-empty table.
2. **After `GET /v1/invites` lands:** add the table (token prefix / binds-to / expires / created-by /
   status) with cursor pagination and the shipped list-page shape, and drop the caveat.

## Acceptance / Done When
- `/admin/invites` lets an admin create an invite (with optional email binding and an `expires_in`
  choice) and shows the raw token exactly once with a copy affordance, admin-gated.
- Client-side validation matches the server: `expires_in` must be positive and <= 720h.
- The list area states plainly that listing is not yet available, rather than showing an empty table.
- Vitest + MSW coverage for the create mutation, the one-time token display, and the max-duration
  rejection path.
- Follow-up (same item, second step): the invites table lands once the endpoint exists.

## Related
- Carved from [[feature-2026-06-26-admin-console-pages]]
- List blocked on [[feature-2026-06-26-web-enabler-backend-endpoints]] (`GET /v1/invites`)
- Shell + patterns: `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`
- Sibling tabs: [[feature-2026-08-08-admin-enrollments-tab]],
  [[feature-2026-08-08-admin-reservations-tab]], [[feature-2026-08-08-admin-server-overview-tab]]
- Source: `internal/api/server.go:139`, `internal/api/invites.go`,
  `internal/store/query/invites.sql`, `design_handoff_relay_holo/hifi3-holo-pages.jsx:2083`

## Notes
Two Holo columns will still be unbacked even after `GET /v1/invites` lands unless the endpoint is
designed to supply them, so specify them there rather than in the UI:
- **TOKEN PREFIX** - only the SHA-256 hash is stored today; there is no retained prefix.
- **CREATED BY** as an email - `invites.created_by` is a user UUID with no join to `users`.
The Holo footnote is accurate and can ship as written: invites are one-time, there is no revoke
endpoint in v1, and expiry or redemption are the only terminal states.
