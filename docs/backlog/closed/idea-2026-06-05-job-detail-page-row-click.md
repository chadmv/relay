---
title: Job detail page and row-click navigation
type: idea
status: closed
created: 2026-06-05
source: jobs-list-frontend retro (2026-06-05)
closed: 2026-07-01
resolution: fixed
---

# Job detail page and row-click navigation

## Summary
Build the job detail page (`/jobs/:id`) and wire job table rows to navigate to it on click. Rows are currently non-interactive (the chevron affordance was dropped) because the detail page is a separate slice. The backend `GET /v1/jobs/{id}` already returns the full job with task detail and DAG dependencies.

## Context
Deferred from the first jobs-list slice. The hi-fi `HoloJobDetail` (resizable tasks+DAG / logs split) and `HoloTaskLog` (SSE log stream) are the design references.

## Related
- `internal/api/jobs.go` (`handleGetJob`)
- `web/src/jobs/JobsTable.tsx` (row click), `web/src/app/router.tsx`
- `design_handoff_relay_holo/reference/screens/job-detail.js`
- `docs/retros/2026-06-05-web-jobs-list.md`

## Resolution
Shipped the /jobs/:id job detail page and jobs-list row-click navigation (feature
commit cb410f2, autopilot iteration 2, 2026-07-01 job-detail-page). Frontend-only,
over existing GET /v1/jobs/{id} and GET /v1/tasks/{id}/logs: header with a reserved
actions slot, 55/45 split, task-DAG dependency strip (pure dagLayout helper),
selectable tasks table, and Spec + static Log tabs; JobsTable name cell is now an
accessible Link. Full web suite green (248 tests), production build clean. Live SSE
log tailing, job write-actions, an accessible drag-resizer
([[idea-2026-07-01-job-detail-resizable-split]]), and the per-task source block are
deferred to their own items. Design: docs/superpowers/specs/2026-07-01-job-detail-page-design.md;
plan: docs/plans/2026-07-01-job-detail-page-plan.md.
