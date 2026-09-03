---
title: ScheduleRunsPanel is the fourth panel-title and table-label duplication
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# ScheduleRunsPanel is the fourth panel-title and table-label duplication

## Summary
Lane TB fixed three panels whose table name was typed twice, once as the panel title and once as the table's accessible name, and added a structural test comparing them. ScheduleRunsPanel has the same shape and was outside that lane's scope, and WorkerTasksPanel reproduced the defect the day before the fix.

## Context
From the TB spec.

## Proposal
Route ScheduleRunsPanel through the same Panel primitive and extend the structural test's count.

## Related
- web/src/schedules/ScheduleRunsPanel.tsx
