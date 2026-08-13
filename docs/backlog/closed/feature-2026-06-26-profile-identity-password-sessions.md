---
title: Profile pages (Identity / Password / Sessions tabs)
type: feature
status: closed
created: 2026-06-26
priority: medium
source: ROADMAP web-frontend deep review against design_handoff_relay_holo (2026-06-26)
closed: 2026-08-12
resolution: fixed
---

# Profile pages (Identity / Password / Sessions tabs)

## Summary
The `/profile/*` route is a `JobsPlaceholder` stub even though the UserMenu already links to
`/profile`, `/profile/password`, and `/profile/sessions` (all currently dead). The Holo design
(`HoloProfile`) specifies three tabs - Identity, Password, and Sessions - a small, mostly
form-based surface.

## Context
Surfaced by the 2026-06-26 `/roadmap web-frontend deep` review against `design_handoff_relay_holo/`.
Router stub at `web/src/app/router.tsx:25`; dead UserMenu links at `web/src/shell/UserMenu.tsx`.
Per-screen spec: `design_handoff_relay_holo/reference/screens/auth.js`.

## Proposal
- **Identity** - display name / email; rename via `PATCH /v1/users/me`. Backend exists.
- **Password** - current + new; `PUT /v1/users/me/password` (revokes other sessions). Backend exists.
- **Sessions** - list active bearer tokens with "sign out everywhere". The sign-out-everywhere
  action (`DELETE /v1/auth/tokens`) exists, but **listing sessions needs a new
  `GET /v1/auth/tokens` endpoint** that does not exist yet.

## Acceptance / Done When
- `/profile/:tab` renders Identity / Password / Sessions.
- Identity rename and password change are wired to the existing endpoints with validation/error states.
- Sessions tab lists active sessions (once `GET /v1/auth/tokens` lands) and supports sign-out-everywhere.
- The UserMenu links resolve to real tabs instead of the placeholder.

## Related
- Design: `design_handoff_relay_holo/reference/screens/auth.js`, `hifi3-holo-pages.jsx` (`HoloProfile`)
- Pairs with [[feature-2026-06-05-usermenu-panel-menu-roles]] (the menu that links here)
- Source: `web/src/app/router.tsx:25`, `web/src/shell/UserMenu.tsx`, `internal/api/{users,auth}.go`

## Notes
Backend gap: `GET /v1/auth/tokens` (list active sessions) is missing; the other two tabs are
frontend-only against existing endpoints.

## Resolution
Implemented in full per `docs/superpowers/specs/2026-08-12-profile-pages.md` and
`docs/superpowers/plans/2026-08-12-profile-pages.md`: `/profile/:tab` now renders
Identity, Password and Sessions behind the three previously-dead `UserMenu` links,
replacing the `JobsPlaceholder` stub. Identity renames via `PATCH /v1/users/me`
with a changed-fields-only save; Password changes via
`PUT /v1/users/me/password` with three client-side guards (confirm match, min-8,
72-byte bcrypt cap) and correctly stays signed in on both success and a 403 wrong-
password response. Sessions ships the `Sign out everywhere` action
(`DELETE /v1/auth/tokens`) with a session teardown that ends the local session
generation before any other query can observe the destroyed credential.

One deliberate deviation from this item's original Acceptance criteria: Sessions
does **not** list active sessions. `GET /v1/auth/tokens` still does not exist, and
the spec found that a real list also needs a `last_used_at` column and
agent/IP/location columns that `api_tokens` does not have - a migration plus an
endpoint, not "one small endpoint" as this item's Notes implied. The tab ships
action-only with an honest footnote naming the gap, following the same
omit-what-the-backend-cannot-supply precedent as the Agent Enrollments tab. The
underlying enabler item (`feature-2026-06-26-web-enabler-backend-endpoints.md`)
remains open and is the correct place to pick this back up; the spec proposes
amending it with the concrete column/route requirements.
