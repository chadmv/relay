---
title: Two comment-policy leftovers from the web batch: UsersTab's debounce comment and a superlative in SchedulesTable's test
type: bug
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# Two comment-policy leftovers from the web batch: UsersTab's debounce comment and a superlative in SchedulesTable's test

## Summary
UsersTab's debounceMs comment says the test runs on real timers while the test uses fake timers, and SchedulesTable.test.tsx describes one fixture as the worst case in the app, a superlative about the complement that nothing pins. Both are prose defects under CLAUDE.md's Comments policy, each noticed by a lane whose scope did not include the file.

## Context
From the MF plan and the SF fix round.

## Proposal
Correct the first to say what the test does; delete the second's claim and keep the reason the fixture discriminates.

## Related
- web/src/admin/users/UsersTab.tsx, web/src/schedules/SchedulesTable.test.tsx
