---
title: task_status_fence's conflicting and duplicate counters are forgeable by the task's own assignee, and the documented remedy helps the forger
type: idea
status: open
created: 2026-08-24
priority: medium
source: Phase 4 security and invariants lenses of the 2026-08-24 handletaskstatus-pair slice; reproduced against real Postgres
---

# conflicting_total is forgeable by the task's own assignee

## Summary

`task_status_fence.counts.conflicting_total` is the key README calls "the actionable number". It is
forgeable, without bound, by the one party the identity gate lets through: **the task's own assignee**.

A terminal transition bumps neither `assignment_epoch` nor `worker_id`, so an agent can report `done`
at epoch N and then `failed` at epoch N and both Go gates pass legitimately. Each subsequent report is
refused by the terminality predicate and lands on `conflicting`. `AgentMessage_TaskStatus` dispatch is
unbudgeted, so the cost is one message on an already-open stream.

Measured: 10,000 forged messages produce `{Raced:0 Duplicate:0 Conflicting:10000}` with every
`ingest_log_budget` counter flat, no log line, and the row still `done` at its original epoch.

## Why the remedy makes it worse

README tells an operator that a climbing `conflicting_total` is the signature of
`RELAY_TASK_WATCHDOG_MARGIN` set too small - "a successful task recorded as a timeout". A wedged or
hostile agent is **precisely what the watchdog sweeps**, so after being stamped `timed_out` it can
replay `done` and manufacture that exact signature. The prescribed response is to raise the margin,
which widens the unbounded-assignment window the watchdog exists to close.

**The attacker's incentive and the documented remedy point the same way.** That is the part that makes
this worth an item rather than a footnote.

## Context

The slice that shipped the counter documented the forgeability at README:1307 with a cross-check
instruction (confirm the configured margin, cross-check `watchdog.counts.swept_by_worker` before
raising anything). So the advertisement is now honest. **What is not bounded is the counter itself.**

This is the second instance of a shape already filed for `task_log_fence`
([[idea-2026-08-21-rejected-total-is-forgeable-and-its-remedy-helps-the-forger]]), and the two should
cross-reference rather than merge: the mechanisms are disjoint (that one is cross-task, this one is
self-task, so the identity gate stops one and not the other) and so are the remedy spaces - the
per-reason split this item might otherwise ask for already exists here.

The identity gate itself is **sound and now pinned**: two lenses proved by construction that no
unrelated peer can move these counters, and `TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters`
holds it. Attributable is simply not the same as honest.

## Proposal

Options, and the first is not obviously wrong:

- **Accept and leave documented.** The counter is already caveated where it is read, the cost is three
  `uint64`s, and an operator who follows the cross-check instruction is not misled. Closing this as
  `wontfix` with that reasoning recorded is a legitimate outcome.
- **Bound it per task.** Count distinct tasks rather than reports, or cap the contribution of any one
  task. Both need state keyed on something, which runs straight into the cluster's cardinality rule -
  a task-keyed map is peer-driveable, so it would need the same hard cap and argued allow-list entry
  the watchdog's per-worker map has.
- **Budget the status message stream itself.** The real asymmetry is that log lines are budgeted and
  messages are not. That is a larger change with its own risks, and it would bound several things at
  once.

Whichever is chosen, do not "fix" it by removing the caveat.

## Acceptance / Done When

- A decision is recorded in the code, not only here.
- If bounded: the repro above no longer moves the counter without limit, and the cardinality rule is
  satisfied with a written argument.
- If accepted: README and the type's doc comment keep the caveat, and this item closes as `wontfix`
  citing the reasoning.

## Related

- `internal/worker/taskstatus_fence_counters.go` - the counters and the "WHAT THESE NUMBERS DO NOT COVER" list
- `internal/store/query/tasks.sql` - the paragraph that already states the reachable input in its own words
- [[idea-2026-08-21-rejected-total-is-forgeable-and-its-remedy-helps-the-forger]] - the sibling instance
- [[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]] - the slice that shipped the counter
