---
title: "A schedule fires under its owner's identity with no credential check at fire time, so revoking a token does not stop it"
type: bug
status: open
created: 2026-08-29
priority: medium
source: Security lens of the Phase 4 review of the count-bounds slice (2026-08-29)
---

# A schedule fires under its owner's identity with no credential check at fire time, so revoking a token does not stop it

## Summary

`ListEligibleScheduledJobs` filters on `enabled` and `next_run_at` only. There is no predicate on the
owner's tokens, and `schedrunner.fireOne` creates the job under `row.OwnerID` with no authentication at
fire time. So **a schedule keeps producing jobs after every credential its creator holds has been
revoked.**

This is the property that makes the schedule route interesting despite being the SLOWER of the two
submission paths.

## Why it is filed even though `/v1/jobs` is the bigger multiplier

Measured against the 2026-08-29 count bounds, raw rate favours `POST /v1/jobs` by roughly 300x:
`minScheduleInterval` is 30s and enforced on both create and PATCH, and `schedrunner.BatchLimit = 100`
caps rows scanned per 10s tick, so one schedule at the new bounds sustains ~833 commands/s where an
unthrottled `POST /v1/jobs` at 10 req/s is ~250,000 commands/s. **The rate-limit item is correctly
prioritised over this one** - see [[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]].

But rate is not the axis this item is about. The load here **survives the remedy an operator would
reach for first.** Deleting the USER does stop it (`owner_id ... ON DELETE CASCADE`), and deleting the
schedule stops it. Revoking a token does not, and there is no user-disable flag in the schema. So an
operator's only proportionate remedy - one short of deleting a person's account - is finding and
deleting each row by hand.

Note the shape: a remedy ladder whose only working rungs are "delete the schedule one at a time" and
"delete the user" is the ladder problem CLAUDE.md's Invariants describe, where the documented escape is
disproportionate to the signal.

## Proposal

Sketch. Two independent halves:

1. **Say it in README first, this is cheap and owed regardless.** The Scheduled jobs section presents
   the surface without this property. One sentence: a schedule fires under its owner's identity with no
   check on the owner's credentials at fire time; revoking a token does not stop a schedule, deleting
   the schedule or the user does.
2. **Give the operator a proportionate rung.** Candidates: a user-disable flag the eligible-schedules
   query respects, a predicate on the owner having at least one live token, or a per-user schedule
   quota. Each is a different product decision - a disable flag is an admin action, a token predicate
   silently disables schedules for anyone who lets a token lapse, and a quota bounds a different thing
   entirely. Argue them; do not adopt one because it is nearest.

Half 2 shares the quota question with [[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]] and with
[[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]]; deciding them apart
guarantees three answers.

## Acceptance / Done When

- README states the fire-time identity property.
- If a control lands: revoking whatever the control keys on demonstrably stops the schedule firing, and
  a schedule whose owner is in good standing is demonstrably unaffected.
- The choice between disable-flag, token-predicate and quota is argued in the PR, not just implemented.

## Related

- Source: `internal/store/query/scheduled_jobs.sql` (`ListEligibleScheduledJobs`),
  `internal/schedrunner/runner.go` (`fireOne`), `internal/api/server.go` (the route's `auth` wrapper),
  `internal/store/migrations/000006_scheduled_jobs.up.sql` (the `ON DELETE CASCADE`)
- LOW, noted rather than filed separately: `BatchLimit = 100` with `ORDER BY next_run_at ASC` means a
  large population of overdue attacker schedules DELAYS legitimate ones. It is not starvation - an
  unfired schedule keeps its old `next_run_at` and ages toward the front - so it is queueing delay
  proportional to the attacker's schedule count.
