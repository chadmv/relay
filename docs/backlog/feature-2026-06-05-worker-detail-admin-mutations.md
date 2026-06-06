---
title: Worker detail page admin mutation actions
type: feature
status: open
created: 2026-06-05
priority: medium
source: deferred from the worker-detail-page read-only slice (2026-06-05 brainstorm)
---

# Worker detail page admin mutation actions

## Summary
The first worker-detail slice ships read-only (identity, telemetry, labels, admin
workspaces view). The wireframe's admin write actions were deferred to keep that
slice read-only. This item covers adding them as a follow-up "mutations slice" -
the first write operations in the web frontend.

## Context
The `v3Detail` wireframe in `design_handoff_relay_holo/reference/screens/workers.js`
surfaces drain, rename, edit-labels, set-slots, revoke-token, and per-workspace
evict. All have backend support already; only the UI is missing.

## Proposal
Wire up, behind admin gating, the existing endpoints:
- `PATCH /v1/workers/{id}` - rename, edit labels, set `max_slots`.
- `POST /v1/workers/{id}/disable` (drain, with optional `?requeue=`) and `.../enable`.
- `DELETE /v1/workers/{id}/token` - revoke agent token.
- `POST /v1/workers/{id}/workspaces/{short_id}/evict` - the Evict action on the
  workspaces panel (held workspaces refuse).

Use TanStack Query mutations with optimistic/invalidate-on-success against the
detail queries. Confirmation prompts for destructive actions (revoke, drain,
evict).

## Acceptance / Done When
- Admins can rename, edit labels, set max_slots, drain/enable, revoke token, and
  evict workspaces from the detail page; non-admins see none of these controls.
- Each action reflects its result without a manual refresh.
- Tests cover the mutations and admin gating.

## Related
- `web/src/workers/` (detail page from the read-only slice)
- `internal/api/workers.go`, `internal/api/workspaces.go`
- `docs/superpowers/specs/2026-06-05-worker-detail-page-design.md`
