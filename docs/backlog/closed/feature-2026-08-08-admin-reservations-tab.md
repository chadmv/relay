---
title: "Admin console: Reservations tab (list + create + delete)"
type: feature
status: closed
created: 2026-08-08
closed: 2026-08-09
resolution: fixed
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

## Resolution
Fixed 2026-08-09 (admin-reservations-tab). The admin console's third tab and the last fully
backend-ready one: list, create and delete worker reservations. Unlike the sibling tabs,
`DELETE /v1/reservations/{id}` does exist, so delete ships, behind a destructive `ConfirmDialog`
whose buttons carry row identity. Frontend-only; suite 617 -> 710 tests.

**The important discovery was what a reservation actually does.** It is read in exactly one place:
`ListActiveReservations`' `worker_ids` become a `reservedIDs` set and those workers are skipped for
**every** task. So it is a **pure exclusion from the dispatch pool** - `user_id`, `project` and
`selector` are stored and never consulted, and the effect lands on the next 30s dispatch tick. The
hi-fi's "reserve for X" framing implies reserved workers run that user's work, which is untrue, so
the UI does not repeat it. That honesty requirement was made a task with assertions rather than a
note, which is what caught the review finding below.

The list is unfiltered, so status is derived client-side by reproducing the SQL predicate;
`ACTIVE | SCHEDULED | ENDED` is all that is derivable. Nullability follows the real response rather
than assumption: `selector` arrives as JSON `null` while `project`, `starts_at` and `ends_at` keys are
**absent**. `listWorkers` gained an optional limit for the worker picker with the default preserved,
and the picker shows a visible ceiling note whenever `total` exceeds what was fetched, so a truncated
list is never presented as complete. Nothing here is secret, so no `TokenRevealDialog`, no
`gcTime: 0` and no `secretLeaks` - the gate greps the diff to prevent cargo-culting the enrollments
tab's machinery.

A bug in the plan's own reference `deriveStatus` was found and fixed: it compared each bound to `now`
but never to each other, so a window ending before it starts read `SCHEDULED` forever.

Review returned 0 high / 2 medium / 7 low, and the two mediums are both worth remembering:

- **The delete dialog asserted a dispatch effect that does not exist for non-ACTIVE rows** -
  "returns its N worker(s) to the general dispatch pool" is false for a SCHEDULED or ENDED
  reservation whose workers were never withheld, and reads absurdly as "returns its 0 worker(s)" for
  an empty reservation created via the CLI. Exactly the overstatement class the honest-copy
  requirement exists to catch.
- **The worker picker could diverge from its loaded page in two opposite directions.** After a
  window-focus refetch dropped a revoked worker, pressing Reserve submitted the vanished id (there is
  no FK on `worker_ids`, so it persisted), while toggling any other checkbox first silently dropped it
  instead.

Filed rather than fixed here, since it is backend and out of a frontend-only diff:
[[bug-2026-08-09-create-reservation-500-on-client-error]] - `POST /v1/reservations` returns 500 for a
well-formed but nonexistent `user_id` because every `CreateReservation` error funnels to a 500, and it
validates almost nothing else (no worker existence check, no inverted-window rejection). The shipped
tab sidesteps it by never sending `user_id`, which is also inert.

With this tab, only the server-overview tab (a quick win over existing stats) and the Invites tab
(list half still blocked on `GET /v1/invites`) remain of the original five-tab admin console.
