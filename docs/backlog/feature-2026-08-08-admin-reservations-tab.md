---
title: "Admin console: Reservations tab (list + create + delete)"
type: feature
status: open
created: 2026-08-08
priority: high
source: carved from feature-2026-06-26-admin-console-pages during the 2026-08-08 admin-console-shell-users-tab slice
---

# Admin console: Reservations tab (list + create + delete)

## Summary
The Reservations tab of the admin console: list worker reservations, create one, and delete one. All
three endpoints exist and are admin-gated; frontend-only.

## Context
Carved out of the admin-console omnibus item when the 2026-08-08 slice shipped the `/admin/:tab` shell
plus the Users tab. Design source of truth is `AdminReservations` in
`design_handoff_relay_holo/hifi3-holo-pages.jsx` (line 2205); `reference/screens/admin.js` is
structure-only.

## Proposal
- Register a `reservations` tab in the admin shell registry.
- List via `GET /v1/reservations` (admin, cursor-paginated). Response rows are
  `{ id, name, selector, worker_ids, user_id, project?, starts_at?, ends_at?, created_at }` per
  `reservationResponse` in `internal/api/reservations.go:13-23`. Render `worker_ids` as chips.
- Create via `POST /v1/reservations`; delete via `DELETE /v1/reservations/{id}` behind
  `ConfirmDialog` (destructive variant), invalidating the `['reservations']` prefix on success.
- Reuse the shipped list-page shape (cursor stack, `computePageRange` footer, loading / error / empty
  triad) and the `useWorkerActions`-style invalidate-on-success mutation hook.
- Carry the Holo footnote that `selector` is informational in v1 - only explicit `worker_ids` lists
  are enforced by the scheduler. Confirm that statement against `internal/scheduler/` before shipping
  the copy.

## Acceptance / Done When
- `/admin/reservations` lists reservations with cursor pagination, admin-gated.
- An admin can create a reservation and delete one (delete behind a confirm dialog); both refresh the
  list without a manual reload.
- Nullable `starts_at` / `ends_at` render as an explicit dash, not a fabricated date.
- Vitest + MSW coverage for the list query, both mutations and their invalidation, and confirm gating.

## Related
- Carved from [[feature-2026-06-26-admin-console-pages]]
- Shell + patterns: `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`
- Overlaps the worker-detail view of the same data: [[feature-2026-06-05-worker-detail-reservations-panel]]
- Sibling tabs: [[feature-2026-08-08-admin-enrollments-tab]],
  [[feature-2026-08-08-admin-server-overview-tab]], [[feature-2026-08-08-admin-invites-tab]]
- Source: `internal/api/server.go:134-136`, `internal/api/reservations.go`,
  `design_handoff_relay_holo/hifi3-holo-pages.jsx:2205`

## Notes
Two things to settle during design, not to assume:
- **`user_id` display.** Rows carry a bare owner UUID with no join to `users`, so a "created by
  email" column is unbacked. Either show the id, or omit the column.
- **Sort keys.** The Holo offers eight sort options (`created_at`, `name`, `starts_at`, `ends_at`,
  each direction). Verify the actual `ReservationsSortSpec` in the handler before wiring a sort
  control; only offer keys the server accepts.
Also confirm the `POST /v1/reservations` request body field-for-field against the handler (selector
shape, whether `worker_ids` or a selector is required) rather than inferring it from the response.
