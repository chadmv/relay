---
title: WorkersTable's fr-sized name and labels cells lack min-w-0, violating Table.tsx's stated precondition
type: bug
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# WorkersTable's fr-sized name and labels cells lack min-w-0, violating Table.tsx's stated precondition

## Summary
Table.tsx states that an fr-sized cell must carry min-w-0 and truncate or a long value widens the track past the grid. WorkersTable's name and labels cells are fr-sized without either. WorkerTasksPanel's inline links inside truncate cells are partially clipped for the related reason that a truncating cell clips its focus ring.

## Context
From the TB review (pre-existing).

## Proposal
Add the two utilities, add the cells to the structural test that checks the precondition, and give the inline links room for their outline.

## Related
- [[idea-2026-08-14-table-minwidth-magnitude-is-unchecked]]
- web/src/workers/WorkersTable.tsx, web/src/workers/WorkerTasksPanel.tsx
