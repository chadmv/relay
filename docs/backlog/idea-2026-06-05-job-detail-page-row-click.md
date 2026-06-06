---
title: Job detail page and row-click navigation
type: idea
status: open
created: 2026-06-05
source: jobs-list-frontend retro (2026-06-05)
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
