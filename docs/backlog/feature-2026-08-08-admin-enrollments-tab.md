---
title: "Admin console: Agent enrollments tab (create + list)"
type: feature
status: open
created: 2026-08-08
priority: high
source: carved from feature-2026-06-26-admin-console-pages during the 2026-08-08 admin-console-shell-users-tab slice
---

# Admin console: Agent enrollments tab (create + list)

## Summary
The Agent enrollments tab of the admin console: list active (unconsumed, unexpired) enrollment tokens
and create new ones, with the raw token shown clear-text exactly once on creation. Backend-ready;
frontend-only.

## Context
The 2026-08-08 admin-console slice built the `/admin/:tab` shell and the Users tab only, carving the
remaining four tabs into their own items. The shell registry is designed so a new tab joins with one
entry - see `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`. Design source of
truth is the `AdminEnrollments` component in `design_handoff_relay_holo/hifi3-holo-pages.jsx`
(line 2137) plus the shared `AdminTokenModal` (line 2340); the `reference/screens/admin.js` sketch is
structure-only.

## Proposal
- Register an `enrollments` tab in the admin shell registry.
- List via `GET /v1/agent-enrollments` (admin, cursor-paginated, `?sort=` `created_at`/`expires_at`
  with `-` prefix). Reuse the shipped list-page shape (cursor stack, `computePageRange` footer,
  `placeholderData: keepPreviousData`).
- Create via `POST /v1/agent-enrollments` with `{ hostname_hint?, ttl_seconds? }` (default 24h, max
  7d). The 201 response is `{ id, token, expires_at }` - surface `token` in a copy-once modal with the
  "cannot be retrieved again" warning, then invalidate the list.
- Derive an `active` / `expiring` status pill client-side from `expires_at` (the endpoint already
  returns active-only rows, so there is no server-side status field to read).
- Explain the enrollment-vs-invite distinction in the footnote, as the Holo does: the printed token
  goes into `RELAY_AGENT_ENROLLMENT_TOKEN` on the agent's first boot and is consumed in exchange for a
  long-lived agent token.

## Acceptance / Done When
- `/admin/enrollments` lists active enrollments with sort + cursor pagination, admin-gated.
- Creating an enrollment shows the raw token once, with a copy affordance and the one-time warning,
  and refreshes the list.
- Columns with no backing data are omitted, not faked (see Notes).
- Vitest + MSW coverage for the list query, the create mutation and its `['agent-enrollments']`
  invalidation, and the token-modal one-time display.

## Related
- Carved from [[feature-2026-06-26-admin-console-pages]]
- Shell + patterns: `docs/superpowers/specs/2026-08-08-admin-console-shell-users-tab.md`
- The revoke action is a separate open item: [[feature-2026-06-26-agent-enrollment-revocation]]
  (`DELETE /v1/agent-enrollments/{id}` does not exist yet)
- Sibling tabs: [[feature-2026-08-08-admin-reservations-tab]],
  [[feature-2026-08-08-admin-server-overview-tab]], [[feature-2026-08-08-admin-invites-tab]]
- Source: `internal/api/server.go:142-143`, `internal/api/agent_enrollments.go`,
  `design_handoff_relay_holo/hifi3-holo-pages.jsx:2137`

## Notes
Verified against `enrollmentRowToMap` (`internal/api/agent_enrollments.go:83-94`): list rows carry
`id`, `created_at`, `expires_at`, `created_by`, and optional `hostname_hint` - nothing else. Two Holo
columns are therefore unbacked and must be omitted rather than faked:
- **TOKEN PREFIX** - only the SHA-256 hash is stored; no prefix is retained or returned.
- **CREATED BY** as an email - `created_by` is a bare user UUID with no join to `users`.

There is also no per-row revoke action until the revocation item lands; the Holo's action cell copy
("consumed on first agent connect") is informational and can ship as-is.
