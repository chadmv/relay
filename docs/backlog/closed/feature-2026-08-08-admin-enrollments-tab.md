---
title: "Admin console: Agent enrollments tab (create + list)"
type: feature
status: closed
created: 2026-08-08
closed: 2026-08-09
resolution: fixed
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

## Resolution
Fixed 2026-08-09 (admin-enrollments-tab). The second admin-console tab, plugging into the registry
the shell shipped with earlier the same day: create an agent-enrollment token, and list the
unconsumed unexpired ones. Frontend-only; web suite 530 -> 617 tests.

**The new piece was the token reveal.** `POST` returns a token shown clear-text exactly once and
unrecoverable afterward, so `TokenRevealDialog` is a **shared** component - the Invites tab needs the
identical surface and it carries the security invariants - while the create form stays tab-local,
since the fields, TTLs and endpoint differ and the hi-fi's shared `isInvite` flag is the rot pattern.

Keeping the token out of every store took more than clearing component state. TanStack retains a
mutation's `data` and `variables` for the mutation's lifetime, and `reset()` only detaches the
observer - it does **not** delete the underlying `Mutation` from the cache, whose default `gcTime` is
5 minutes. So the create mutation sets `gcTime: 0`. Verified against library source during review:
`reset()` -> `removeObserver` -> `scheduleGc()`, and `optionalRemove` removes only when
`observers.length === 0` and the status is settled, so `gcTime: 0` is both necessary and sufficient
and cannot evict while pending.

**Ships with no revoke control**, following the Users tab's precedent: `DELETE
/v1/agent-enrollments/{id}` does not exist and rendering a guaranteed-failing button is a dead
control. That endpoint stays [[feature-2026-06-26-agent-enrollment-revocation]], since adding it
makes this a backend slice carrying an integration requirement - revoking must not disturb an
already-enrolled worker. Blast radius meanwhile is bounded by single-use consumption, the TTL, and
`DELETE /v1/workers/{id}/token`.

Also omitted, none of them supportable by the list response: a TOKEN PREFIX column, CREATED BY, and
a CONSUMED status - every query filters `consumed_at IS NULL AND expires_at > NOW()`, so consumed and
expired rows simply vanish.

Three claims in this item were wrong and were corrected at spec time: it named the create form as the
reveal surface, so the reveal was actually undesigned and deferred to a "success toast" existing
nowhere in the handoff or the app; it omitted the mandatory request body and the 60s minimum TTL; and
it claimed two derivable enrollment states where three are observable. Worth recording that the same
agent authored both the item and the correction - verifying a proposal against the code matters even
when the proposal is your own.

Review returned 0 high / 2 medium / 5 low. The two mediums are worth remembering: the reveal dialog
**stole focus back every 60 seconds** (its focus effect depended on an inline `onDone` whose identity
changed each render, and the tab re-renders on a `useNow(60_000)` tick), so a keyboard admin who
tabbed to Done and paused to read the warning got nothing on Enter - with Escape as the plausible
next keystroke, permanently destroying the credential. And the shared leak checker had the exact
blind spot it was written to close, falling back to `JSON.stringify` for plain objects so
`console.error({err: new Error(token)})` passed.

`hostname_hint` is absent rather than null when unset, so its type is honestly optional and the table
renders a plain hyphen. Timestamps are RFC3339-nanosecond and are parsed, never string-compared.
Verified live against a real backend: the token is absent from the DOM, both web storages, every
request URL, and the console after the dialog closes.
