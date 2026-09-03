---
title: Selecting a task row is silent to assistive technology, and a non-name cell click does not move focus
type: bug
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# Selecting a task row is silent to assistive technology, and a non-name cell click does not move focus

## Summary
The tasks table (PR #179) marks the selected row with aria-current on a per-row button. A change of aria-current on an element that does not hold focus is not announced, and there is no live region, so a screen-reader user who selects a task hears nothing. Clicking a non-name cell selects the row but leaves focus where it was, which the old row-as-button did not.

## Context
From the TB review; the fix round kept it out of scope.

## Proposal
Announce the selection through the page's polite live region and move focus to the row's button on any cell click.

## Related
- web/src/jobs/TasksTable.tsx
