---
title: Nothing reaps auto-enrolled rows that never came back, and the schema cannot tell which they are
type: idea
status: open
created: 2026-08-25
priority: medium
source: 2026-08-25 auto-enroll-guards slice - the ceiling's complement, split out deliberately
---

# Nothing reaps auto-enrolled rows that never came back, and the schema cannot tell which they are

## Summary

`RELAY_AUTO_ENROLL_WORKER_CEILING` bounds how many non-revoked workers can exist. Nothing removes the
ones already there. A deployment that has already been hit gets no relief from the ceiling, and the
documented remedy - revoke the junk - frees ceiling budget without freeing the row or the hostname, so
under an active attacker it is a treadmill that trades a bounded count for an unbounded table.

The ceiling and a reaper are **complements, not alternatives**; the slice picked the ceiling because
the item's acceptance criterion demanded a stated bound, which a reaper cannot give (its steady state
is rate x TTL and the rate is unbounded).

## Context

**Lead with the schema gap, because it is the actual work.** Nothing records which path created a
`workers` row. There is no `enrolled_via` column and no timestamp distinguishing "auto-enrolled and
never seen again" from "token-enrolled and currently offline for maintenance". A reaper keyed on
`last_seen_at` alone would delete legitimate machines.

**And note the trap:** "a row that never had a successful post-enrollment reconnect" describes an
attacker's junk row *and* a first-boot agent whose token persist failed (see the first-boot lockout
item). Reaping the second is the recovery those operators want; reaping is also what an attacker
would want if it let them recycle a claimed hostname. Decide deliberately.

## Proposal

1. Record provenance. A column on `workers` set at creation - the two insert statements are
   `InsertWorkerForAutoEnroll` and `UpsertWorkerByHostname`, so the write sites are already distinct.
2. Reap on a TTL: auto-enrolled, never reconnected, older than N. Configurable, generous default, and
   an operator story for what it removes.
3. Decide whether it deletes or revokes. Deleting needs the FK work in the worker-delete item;
   revoking is available today and frees ceiling budget without freeing the hostname, which is a
   weaker but unblocked arm.

## Acceptance / Done When

- Provenance is recorded at both insert sites, with a migration.
- Rows matching the stated shape are reaped on a schedule, proven against real Postgres.
- README states what is reaped, when, and what an operator sees.
- Whichever arm ships, the interaction with the first-boot lockout window is stated rather than
  discovered.

## Related

- `internal/store/query/workers.sql`, `internal/scheduler/` (the existing periodic-sweep shape)
- [[bug-2026-08-25-no-worker-delete-at-any-layer]] - blocks the delete arm, not the revoke arm
- [[idea-2026-08-25-first-boot-lockout-window]]
- [[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]] (closed) - the ceiling this completes
