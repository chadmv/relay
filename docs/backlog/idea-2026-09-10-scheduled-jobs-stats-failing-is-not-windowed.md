---
title: GET /v1/scheduled-jobs/stats' failing key is documented "Not windowed" and now has a second cause of under-reporting
type: idea
status: open
created: 2026-09-10
priority: low
source: Found by the startup-validation deadline spec (PR #210) while enumerating who reads the sweep's count
---

# GET /v1/scheduled-jobs/stats' failing key is documented "Not windowed" and now has a second cause of under-reporting

## Summary

`failing` counts schedules in scope carrying a `last_error`. README documents it as "**Not
windowed**". It is the numeric reader an operator is most likely to trust, and it can under-report for
two separate reasons now.

## Context

Found by asking who reads the startup sweep's verdicts, while deciding what a truncated sweep may
claim. There are four readers and this is the only one that renders a number in a UI.

**It is a live `count(*)`, not the sweep's number**, which is why this is an idea and not a bug: it
reports the state of the table, accurately, at the moment it is asked.

The two causes of under-reporting are both about what is IN the table:

- Pre-existing: a schedule that has never been evaluated since a retroactive rule changed carries no
  `last_error` until something evaluates it.
- New: a truncated startup sweep never checks part of the enabled set, so those schedules carry no
  record on that boot. The sweep only ever ADDS records, so **a `last_error` that is SET stays
  trustworthy; only its ABSENCE loses meaning.**

The deadline slice states this where the boot line is read. It does not reach this endpoint's
documentation, which is the gap.

## Proposal

Sketch only. The honest statement is narrow and worth getting exactly right: `failing` is a floor, not
a total, and an absence means "no recorded failure" rather than "healthy". Decide whether that belongs
in README beside the key, in the payload itself, or both - and note that adding a field to say so is a
contract change, while a README sentence is not.

## Acceptance / Done When

- Wherever `failing` is documented, it says an absence of a recorded failure is not evidence of health.
- If the payload gains anything, it does not imply the count is a total.

## Related

- `README.md` (the scheduled-jobs stats table), `internal/api/scheduled_jobs.go` (the stats handler)
- [[feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep]] - closed; added the second cause
