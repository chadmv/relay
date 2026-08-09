---
title: Admin console pages (Users / Invites / Agent enrollments / Reservations / Server tabs)
type: feature
status: closed
created: 2026-06-26
closed: 2026-08-09
resolution: fixed
priority: high
source: ROADMAP web-frontend deep review against design_handoff_relay_holo (2026-06-26)
---

# Admin console pages (Users / Invites / Agent enrollments / Reservations / Server tabs)

## Summary
The `/admin` route is a `JobsPlaceholder` stub with no real UI, yet it is one of the two
largest remaining front-end surfaces. The Holo design (`HoloAdmin`) specifies five tabs -
Users, Invites, Agent enrollments, Reservations, and a Server/overview tab - and almost all of
the backend already exists, so this is mostly a frontend build.

## Context
Surfaced by the 2026-06-26 `/roadmap web-frontend deep` review cross-referencing
`design_handoff_relay_holo/` (the picked "Holo" design direction) against the code. Router stub
at `web/src/app/router.tsx:24`. Per-screen spec: `design_handoff_relay_holo/reference/screens/admin.js`.

## Proposal
Build the five tabs as admin-only pages, reusing the worker-mutation/optimistic-update pattern.
Slice per tab - each is independently shippable:
- **Users** - list (incl. `?include_archived=true`), create, rename/role (`PATCH /v1/users/:email`),
  archive/unarchive, admin password-reset. Backend exists.
- **Agent enrollments** - create + list. Backend exists (`POST`/`GET /v1/agent-enrollments`).
- **Reservations** - list + create. Backend exists (`GET`/`POST /v1/reservations`, admin).
- **Invites** - create with token-on-create modal (token shown clear-text once). `POST /v1/invites`
  exists, but **the invites list needs a new `GET /v1/invites` endpoint** (states: active /
  expiring / expired / redeemed).
- **Server / overview** - aggregate existing `/v1/jobs/stats` + `/v1/workers/stats`.

## Acceptance / Done When
- `/admin` renders the five tabs (tab via `?tab=` or `/admin/:tab`), admin-gated.
- Users / Enrollments / Reservations tabs are fully wired to existing endpoints.
- Invites tab creates invites (token-on-create modal) and lists them once `GET /v1/invites` lands.
- Non-admins do not see the admin route or nav entry.

## Related
- Design: `design_handoff_relay_holo/reference/screens/admin.js`, `hifi3-holo-pages.jsx` (`HoloAdmin`)
- Establishes the same mutation pattern as [[feature-2026-06-05-worker-detail-admin-mutations]]
- Source: `web/src/app/router.tsx:24` (stub), `internal/api/{users,invites,agent_enrollments,reservations}.go`

## Notes
Backend gap to track as part of the Invites tab: `GET /v1/invites` does not exist. Everything
else (users CRUD, invite create, enrollments, reservations) is present and admin-gated.

## Resolution
Closed as **decomposed after its first slice shipped**, following the precedent of
[[feature-2026-06-26-job-actions-submit-cancel-retry]]. This item's own instruction was
"slice per tab - each is independently shippable", and one tab (the Invites *list*) is
backend-blocked, so shipping all five under one item was neither reviewable nor achievable.

Shipped here (2026-08-09, admin-console-shell-users-tab): the `/admin` route shell -
admin-gated `/admin/:tab` with a registry-driven pill tab bar, `/admin` and unknown tabs
redirecting to `/admin/users`, and the Admin nav entry filtered on `is_admin` - plus a fully
wired **Users tab** on the shared Holo primitives: list with sort, cursor pagination,
`?include_archived=true` and a debounced exact-match `?email=` filter; create (the only place
`is_admin` is settable); rename via the UUID-keyed name-only PATCH; archive/unarchive behind
`ConfirmDialog`; and admin password reset, whose dialog warns that all of the target's sessions
are revoked - including your own if you target yourself. Archive and Unarchive are not rendered
on the acting admin's own row. Frontend-only; web suite green at 444 tests, build clean. Review
returned 0 high / 4 medium / 8 low, with all four mediums and four lows fixed (including a
vacuous no-poll test and a password reset that failed silently behind its own dialog scrim).

Three of this item's endpoint claims were wrong and the code won: `PATCH /v1/users/{id}` is
UUID-keyed and **name-only** (not `:email`), admin password reset is
`POST /v1/users/password-reset` keyed by email in the body (not a per-user path), and **no
endpoint mutates `is_admin` after creation** - so the UI exposes no role-change control. That
last one is a real gap, filed as [[feature-2026-08-09-user-role-change-endpoint]].

The four remaining tabs are carved into their own items, absent from the tab registry rather
than stubbed as dead tabs: [[feature-2026-08-08-admin-enrollments-tab]],
[[feature-2026-08-08-admin-reservations-tab]], [[feature-2026-08-08-admin-server-overview-tab]],
and [[feature-2026-08-08-admin-invites-tab]] (whose list half stays blocked on
[[feature-2026-06-26-web-enabler-backend-endpoints]]).

Unbacked hi-fi elements were omitted rather than faked: no SESSIONS count, no LAST LOGIN, no
`service` role, and no VERSION/BUILD/DB/UPTIME strip (deferred to the server-overview tab item).
