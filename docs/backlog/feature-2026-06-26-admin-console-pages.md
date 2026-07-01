---
title: Admin console pages (Users / Invites / Agent enrollments / Reservations / Server tabs)
type: feature
status: open
created: 2026-06-26
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
