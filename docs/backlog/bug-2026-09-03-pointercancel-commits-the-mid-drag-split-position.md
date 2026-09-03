---
title: The job-detail split commits the mid-drag position on pointercancel instead of reverting
type: bug
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# The job-detail split commits the mid-drag position on pointercancel instead of reverting

## Summary
useSplitWidth shares one finish handler between pointerup and pointercancel, so a gesture the browser interrupts (a context menu, a lost capture) persists whatever position the pointer had reached rather than the pre-drag value. The comment calls this deliberate; the MF re-verify flagged it as a product decision nobody had made.

## Context
From the MF re-verify (PR #183).

## Proposal
Record the pointerdown value and restore it on pointercancel, keeping pointerup as the only commit; one test each.

## Related
- web/src/jobs/useSplitWidth.ts
