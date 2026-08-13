---
title: "Admin console: Invites tab"
type: feature
status: closed
created: 2026-08-08
closed: 2026-08-13
resolution: fixed
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
- **List: UNBLOCKED as of 2026-08-13.** `GET /v1/invites` shipped in
  `2026-08-13-web-enabler-list-endpoints` (closed item:
  [[feature-2026-06-26-web-enabler-backend-endpoints]]). It is admin-only, paginated with the house
  cursor shape, sortable on `created_at` and `expires_at` in both directions, and returns
  `id, created_at, expires_at, created_by, created_by_email` plus optional `email` and `used_at`.
  It deliberately returns **no `status` field**: the server ships facts and the four states
  (active / expiring / expired / redeemed) are derived client-side from `expires_at` and `used_at`,
  the same rule `enrollmentStatus.ts` already follows. The response cannot carry a token hash - the
  query does not select the column, so the generated row type has no field for it. **This item is
  now fully unblocked**; the whole tab is buildable.

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
- List enabler SHIPPED 2026-08-13: [[feature-2026-06-26-web-enabler-backend-endpoints]] (`GET /v1/invites`); this item has no remaining backend dependency
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

## Resolution
Shipped 2026-08-13 (`2026-08-13-admin-invites-tab`). **The title was stale in two ways and is
corrected above**: the list half stopped being blocked hours earlier when `GET /v1/invites` shipped,
and the whole tab went in one slice rather than create-now-list-later.

Create form with hour-denominated TTL presets, a token-revealed-once dialog, and a paginated
sortable list with a four-state pill. `Chip` gained an `err` tone for EXPIRED. This is the **fifth
and final admin tab to be built**; it sits second in the bar, between Users and Agent enrolls
(`web/src/admin/tabs.ts:21-25`), so "last" means last built, not last displayed.

**Two contract facts nobody had recorded, both found at spec time and both verified independently.**
`time.ParseDuration` has no day unit, so a `7d` or `30d` TTL preset 400s every time - the presets go
on the wire as `168h` and `720h`, pinned by a test that rejects `/[dwy]$/`. And `readJSON` runs
unconditionally on `POST /v1/invites`, so a body is mandatory even though every field is optional.

**Ships no revoke, delete or resend control**, because none of those endpoints exist and a
guaranteed-failing button is a dead control - the same rule the enrollments tab set. The four states
(active / expiring / expired / redeemed) derive client-side from `expires_at` and `used_at`, because
the server deliberately returns no `status` field.

**The review finding is the one worth remembering.** Cancelling an in-flight create destroyed an
unrecoverable credential: only the submit button was pending-disabled, so Cancel and the
`+ Create invite` toggle both called `create.reset()` while the mutation was live. `reset()` detaches
the observer but does not cancel `Mutation.execute`, so the POST still landed, the server minted and
persisted the invite, success dispatched to zero observers, `gcTime: 0` evicted the mutation, and the
only plaintext copy of the token was destroyed before it was ever rendered - leaving a permanently
unusable ACTIVE invite that nothing can revoke. Two lanes proved it independently with probes. It was
**inherited verbatim from the shipped enrollments tab**, so the fix landed in both.

Verified in a real browser: the token is absent from the DOM, `localStorage`, `sessionStorage` and the
URL after dismissal and after navigating away and back; a 4-point hit test proved the dialog paints
above the scrim; the TTL presets were observed on the wire as `168h`/`720h`.

**Debt shipped deliberately:** this is the 7th consumer of the cursor-pager pattern against a rule
that says extract before the third. A lens confirmed the copy is character-for-character faithful,
which is the only thing that makes shipping it defensible. Filed as
[[idea-2026-08-13-cursor-pager-hook]].

Suite 973 -> 1049.
