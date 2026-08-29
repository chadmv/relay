---
title: "`CreateJobFromSpec` issues one round trip per dependency edge, and no count bound reaches it"
type: bug
status: open
created: 2026-08-29
priority: medium
source: relay-tpm's refutation 5 of bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers, at its spec gate (2026-08-29)
---

# `CreateJobFromSpec` issues one round trip per dependency edge, and no count bound reaches it

## Summary

`jobcreate.CreateJobFromSpec` calls `CreateTaskDependency` **once per `depends_on` edge**, sequentially,
inside the caller's transaction. Edge count is bounded only by `min(body-derived count, V*(V-1))`, so
with short task names a 1 MiB body expresses roughly 250,000 edges across roughly 500 tasks: a quarter
of a million sequential INSERTs holding one pool connection for the length of one HTTP request.

The same function inserts **tasks** one round trip at a time as well, which is the smaller half of the
same problem and is at least bounded by the task cap.

## Why the count bounds do not fix this

`maxTasksPerJob = 5000` (landed 2026-08-29) does not bound the edge count in any useful way, because
**edges are quadratic in the task count**. At 5000 tasks the `V*(V-1)` ceiling is roughly 25 million,
far above anything 1 MiB can express, so `maxBodyBytes` remains the only binding constraint on edges
exactly as it was before that slice. The cap moved the task axis and left this one untouched.

## Why this was deliberately kept out of the count-bounds slice

Two reasons, both recorded so nobody re-derives them:

1. **It is a different defect class from the one that slice was about.** The item that slice closed was
   headlined "unbounded per-request multipliers" - costs amplified by the retry budget. This is not
   amplified: dependencies are inserted once, at job creation, and never re-inserted on a retry. It is
   linear in body size, which puts it in the same bucket as `Labels`/`Env`/`Requires`. The difference
   from that bucket, and the reason it is a bug rather than a note, is that **each unit here is a
   network round trip rather than a byte copy.**
2. **The fix lives in a different package and is a different kind of fix.** The count bounds are
   validation, in `internal/jobspec`. This is an insert strategy, in `internal/jobcreate`. Folding it in
   would have made that slice about two things.

## Proposal

Sketch only. Two candidate directions, not mutually exclusive:

1. **Batch the inserts.** A single multi-row INSERT (or `pgx.CopyFrom`) for the whole dependency set,
   and the same for the task set. This removes the round-trip cost without introducing a new refusal,
   so it changes no spec's acceptability - which is the reason to prefer it, given how much the
   retroactivity hazard cost the two preceding slices.
2. **Bound total edges in `jobspec.Validate`.** Cheaper to write, but it is a new validation rule and
   therefore **retroactive over stored `scheduled_jobs.spec` rows on all five re-validating paths** -
   see [[reference_tightening_a_validator_is_retroactive]] and the Retroactivity section of
   `docs/superpowers/specs/2026-08-29-task-and-command-count-bounds.md`. Do not adopt this without
   pricing that.

Direction 1 is the better default. If both land, note they are independent.

## Acceptance / Done When

- A spec with a large dependency graph creates its job in a bounded number of round trips, proven by
  counting statements rather than by wall-clock timing.
- If a bound lands instead of or alongside the batching: a spec at the boundary is accepted, one over is
  refused, and the PR states the retroactivity consequence for stored schedules.
- The task-insert loop is addressed or an explicit decision is recorded to leave it alone.

## Related

- Source: `internal/jobcreate/jobcreate.go` (`CreateJobFromSpec`, the `CreateTaskDependency` loop and the
  task-insert loop)
- Found while scoping: [[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]]
- `store.GetEligibleTasks` has **no `LIMIT`** and the dispatcher re-reads the full pending backlog on
  every trigger and every 30s. Different statement, same "one big job degrades the coordinator" theme;
  worth considering together if either is picked up.
