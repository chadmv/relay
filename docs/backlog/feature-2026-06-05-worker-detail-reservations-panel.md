---
title: Worker detail reservations panel
type: feature
status: open
created: 2026-06-05
priority: low
source: deferred from the worker-detail-page read-only slice (2026-06-05 brainstorm)
---

# Worker detail reservations panel

## Summary
The wireframe's per-worker reservations panel (which reservations currently target
this worker) was dropped from the read-only worker-detail slice. The reservations
endpoint is global and not queryable by worker, so this needs a server-side lookup
or a documented client-side filter before the panel can be built.

## Context
`GET /v1/reservations` (admin) lists all reservations; reservations reference
`worker_ids` but there is no per-worker filter. The `v3Detail` wireframe shows the
reservations that apply to the worker being viewed.

## Proposal
- Either add a per-worker reservation filter/endpoint server-side, or fetch the
  global list and filter client-side by membership in `worker_ids` (acceptable only
  if the list stays small).
- Render an admin-gated reservations panel on the detail page.

## Acceptance / Done When
- The detail page shows reservations targeting the worker (admin-only).
- Tests cover the lookup/filter and the panel.

## Related
- `internal/api/` reservations handlers
- `docs/superpowers/specs/2026-06-05-worker-detail-page-design.md`
