---
title: No lane can render a populated worker detail page, which now carries a second table under 15px of headroom
type: idea
status: open
created: 2026-09-02
priority: low
source: 2026-09-01 worker-detail-tasks-panel spec (lane E), Decision 11
---

# No lane can render a populated worker detail page

## Summary
/workers/:id was flagged as having under 15px of layout headroom. The current-tasks panel added a
table to it on an arithmetic argument only (fixed tracks 250px under a 560px min-width, below
WorkspacesPanel's measured-safe 600px in the same column). jsdom performs no layout and the browser
harness runs no agent, so no lane has ever rendered the page with a task row in it.

## Proposal
Either depend on [[idea-2026-08-24-e2e-harness-slice-2-agent-in-harness]], or seed a dispatched task
row directly in the harness fixtures as the cheaper alternative, then add the populated /workers/:id
to the surface list and measure at 320, 375 and 1280.

## Related
- web/src/workers/WorkerTasksPanel.tsx, web/e2e/surfaces.ts
