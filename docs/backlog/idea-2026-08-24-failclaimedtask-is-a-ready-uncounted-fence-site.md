---
title: Dispatcher.failClaimedTask is a ready but uncounted fence-rejection site, so task_status_fence is not a census
type: idea
status: open
created: 2026-08-24
priority: low
source: scope decision of the 2026-08-24 handletaskstatus-pair slice, recorded rather than glossed
---

# failClaimedTask is a ready but uncounted fence-rejection site

## Summary

`GET /v1/server/counters`'s `task_status_fence` section counts the rejections produced by
`handleTaskStatus`'s two epoch-fenced writes. It is **the agent-reported status path only**, and it is
not a census of fence rejections: `Dispatcher.failClaimedTask` and `Watchdog.SweepOnce` are fenced by
the same statement and counted nowhere.

`failClaimedTask` is the ready one. The 2026-08-24 finishRegister slice silenced its `pgx.ErrNoRows`
arm and deliberately added no counter, so as not to pre-empt the counter shape the handleTaskStatus
item then chose. That shape is now shipped, so the site is available.

## Context

The handleTaskStatus slice considered folding it in and declined on three grounds, the first decisive:

1. **Its target is `dispatched` by construction.** `ClaimTaskForWorker` requires `status = 'pending'`,
   and `tasks.sql` records that the status predicate is *tautological* there - so every rejection
   `failClaimedTask` can produce classifies as `raced`. The three-way partition
   (`raced`/`duplicate`/`conflicting`) degenerates to a single value, and the section's keys would then
   mean different things depending on which producer wrote them.
2. **It counts a different noun.** The dispatcher failing to record a terminal *it* decided is not an
   agent's report being discarded.
3. It would need a fifth `CounterSources` field for one number.

That reasoning still holds, which is why this is filed at low rather than queued. The cost of the
decision is stated in the code and the README rather than hidden - both say the section is not a census.

## Proposal

Do **not** add it to `task_status_fence`. If the coordinator's own fence rejections are worth counting,
they are their own section with their own keys, because they answer a different question: *did the
coordinator fail to record an outcome it decided?* rather than *was an agent's report discarded?*

Before building anything, decide whether that question is actionable. `raced` on the dispatcher path
means another writer ended the assignment between claim and terminal-write, which is the benign race
the code already documents. If every value is benign, a counter buys nothing and this closes as
`wontfix` with the reasoning recorded - which is a legitimate and probably the likeliest outcome.

`Watchdog.SweepOnce`'s rejections are a separate question again, and are partly visible already through
`watchdog.counts.swept_total` versus the tasks it attempted.

## Acceptance / Done When

- A decision is recorded either way, in the code rather than only in this item.
- If a section is added, its keys are not the `task_status_fence` keys, and the README says how the two
  differ.

## Related

- `internal/scheduler/dispatch.go` - `failClaimedTask` and the fence-rejection partition comment, which
  is the authoritative enumeration of these sites
- `internal/worker/taskstatus_fence_counters.go` - the "not a census" statement
- [[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]] - the slice that made this ready
