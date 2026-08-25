---
title: Three handleTaskStatus error lines run on the recv goroutine with the log budget already in scope and never consult it
type: bug
status: closed
created: 2026-08-21
priority: low
closed: 2026-08-24
resolution: fixed
source: Phase 4 of the 2026-08-21-silent-drop-observability slice 2; the README claim that was false, and the half of it the registration item does not cover
---

# Three handleTaskStatus error lines run on the recv goroutine with the log budget already in scope and never consult it

## Summary

`internal/worker/handler.go` has **twelve** `log.Printf` sites. Five go through the per-connection log
budget (`lim.allow`) and are counted by the `ingest_log_budget` section shipped 2026-08-21. The other
seven do not, and they split into two classes with different causes:

- **Registration-time (`:233`, `:522`, `:553`).** The budget does not exist yet when they run - it is
  allocated after `authenticateAndRegister` returns. Already tracked by
  [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]. **Not this item.**
- **Post-registration, inside `handleTaskStatus`, with `lim` already a parameter of the function
  (`:939`, `:984`, `:991`).** The budget exists, is in scope, is used twice in the same function
  (`:771`, `:802`) - and these three lines simply do not call it. **That is this item.**

There is also `:1197` (`handleTaskLog marshal`), listed for completeness and **not** claimed as an
exposure: its input is a struct whose only caller-supplied field is `string(chunk.Content)`, and
`encoding/json` replaces invalid UTF-8 rather than failing, so no input is known to reach it.

```go
// handler.go:976-987, with lim in scope from the function signature
if err != nil {
    if !errors.Is(err, pgx.ErrNoRows) {
        log.Printf("worker: handleTaskStatus UpdateTaskStatus %s -> %s: %v", uuidStr(taskID), statusStr, err)
    }
    return
}
```

The difference between this and the registration item matters for the fix: those three sites need the
limiter **moved and threaded**; these three need one call each on a limiter that is already there.

## Repro / Symptoms

The `GetTask` line at `:802` is budgeted, so a **total** database outage is bounded - `GetTask` fails
first and its line spends the budget. The exposure is any condition where the **read succeeds and the
write fails**:

- a serialization failure or deadlock (`40001` / `40P01`) on `UpdateTaskStatus` or on
  `FailDependentTasks` (a recursive CTE, the most expensive statement on this path) under contention;
- a `statement_timeout` that the short `GetTask` clears and the write does not;
- a connection reset between the two round trips.

Under any of those, an agent streaming status updates for tasks it legitimately owns produces **one
unbudgeted log line per message**, at whatever rate it chooses to send. Every number in
`ingest_log_budget` stays at **zero** while it happens, because nothing on that path consults the
budget - so the section an operator would check to answer "is something driving log volume?" answers
"no".

The `:939` retry line has the same shape one branch earlier.

## Context

Found by a Phase 4 lens of the ingest-counters slice, checking the README sentence that slice added:
*"every caller-driven log line on the gRPC receive path is rate-limited per connection"*. **It was
false**, and `ingest_log_limiter.go`'s own comment says so correctly two files away - the two documents
disagreed inside one PR. README was corrected in the same slice (it now says the budget covers those
five sites and no others, and names these three), so **the documentation half is closed and only the
code half remains**.

**Why this is not an amendment to [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]].**
That item's whole content is an **allocation-order** problem and a decision about whether an audit
line should be suppressible at all; its Done-When is about moving the limiter to the top of `Connect`
and threading it through `finishRegister`. These three sites need none of that. Amending it would
widen a narrow, well-argued item into "all unbudgeted log lines everywhere", which is how this project
has repeatedly ended up with items that are wrong about their own scope. The two should be read
together and may well ship together; they should not be merged.

**What makes this cost more than three lines, and it is worth knowing before scoping.** Since
2026-08-21 the `logKind` names are a **response contract**: each kind is a JSON key under
`ingest_log_budget.counts.deduped` and `.suppressed`. Adding a kind now means a const inside the
sentinel, an array cell, a field on `worker.IngestLogDropsByKind`, a line in `byKind`, a field and json
tag on `api.ingestLogKindCounts`, a line in `ingestLogKindCountsFrom`, two entries in
`counterPayloadLeaves`, and the kinds list in `TestServerCounters_ReportsTheIngestLogSnapshot`. Slice 2
proved that **a kind can be added correctly on the worker side and published nowhere with every package
green**; `TestIngestLogKindCountsPublishesEveryWorkerSideField` now reddens on that, and three guards
fire on a new kind. This is not an argument against doing it - it is the checklist.

**The choice this item exists to make deliberately: one new kind, or three?** All three lines are
"`handleTaskStatus`'s write failed", and a single `kindStatusWrite` would cost one JSON key rather than
three. Against that, the three statements fail for genuinely different reasons and the operator
question "which write is failing" is exactly the kind of thing these keys exist to answer. Settle it
with an argument, not by defaulting.

**One thing that must not change.** These three lines are already correctly gated on
`!errors.Is(err, pgx.ErrNoRows)` - the fence rejecting is not a failure. The budget goes **inside**
that gate, never around it, and the `&&` shape at `:802` is the precedent: a short-circuit before
`allow` means the drop is not counted, which is correct, because the decision not to log was made
upstream of the budget. Whatever ships here must preserve that reading, and the payload's own "what
these numbers are" comment must stay true.

## Proposal

To be argued at spec time rather than adopted as written.

- Route `:939`, `:984` and `:991` through `lim.allow` with a `logKey` carrying the canonical task id
  and the epoch, exactly as `kindTaskLogPersist` does. Never the wire string:
  `pgtype.UUID.Scan` does not constrain the bytes at indices 8, 13, 18 and 23, so the wire spelling
  gives a caller 2^32 distinct keys for one (task, epoch) pair.
- Decide one kind versus three, with the reason recorded in the const block's comment.
- Add the kind(s) through the full checklist above, in one commit, so no kind is ever counted on the
  recv path and published under no JSON key.
- Decide `:1197` explicitly - budgeted, or left alone with a comment saying no input is known to reach
  it - rather than leaving it as the twelfth site nobody mentioned.
- Say in `ingest_log_limiter.go`'s comment what the budget now covers, so the next reader does not have
  to enumerate call sites to find out.

## Acceptance / Done When

- A flood of status updates whose write fails produces at most the burst plus the refill rate of log
  lines, proven by a handler-layer test that is RED against today's code. It needs no database: the
  test can inject a failing `DBTX`, and `handleTaskStatus` is reachable with a bare `&Handler{}` for
  the sites above it - check that rather than tagging the test by reflex.
- Every drop on the new path is counted and published, proven by reading it back through
  `GET /v1/server/counters` and by the arity assertion between the worker-side and api-side types.
- The `!errors.Is(err, pgx.ErrNoRows)` gate is unchanged and still short-circuits before `allow`, so a
  fence rejection is still not counted as a budget drop.
- One-kind-versus-three is a stated decision with its reason in the const block.
- README's `ingest_log_budget` bullets are updated to match whatever the budget then covers - they
  currently name these three sites as being outside it.
- No new DB round trip, goroutine, queue or lock on the recv goroutine.
- `TestConnect_TwoConnectionsDoNotShareTheLogBudget` and the ingest-counter tests still pass with no
  assertion weakened.

## Related

- Source: `internal/worker/handler.go` (`handleTaskStatus`, lines around `:939`, `:984`, `:991`, with
  the budgeted `:771` and `:802` in the same function for contrast; `handleTaskLog`'s `:1197`),
  `internal/worker/ingest_log_limiter.go` (the budget and the `logKind` block, whose names are now a
  response contract), `internal/worker/ingest_log_counters.go` (the counters the new kinds would feed)
- The sibling on the other class, to be read together and NOT merged:
  [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]
- The slice that found this and corrected the README half:
  `docs/retros/2026-08-21-silent-drop-observability-slice2.md`,
  `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice2.md` (R5, the `&&` short-circuit)
- The item this closes a gap in: [[idea-2026-08-15-ingest-log-suppression-is-uncounted]]
  (**closed 2026-08-21**) - the counters it shipped read zero while these three sites carry volume
- Why the key must be the canonical id: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] (closed),
  and [[idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes]]
- The reason "one line per connection" is not a bound in general:
  [[idea-2026-08-21-per-stream-log-budget-renewal-is-unpriced]]

## Notes

Filed at **low**, matching its sibling, because there is no known unauthenticated path to it: reaching
`:984` requires a registered agent that passes the identity gate for a task it actually owns. What
raises it above a shrug is the second-order effect that this whole cluster exists to prevent - the
`ingest_log_budget` section reads **all zeros** while these lines carry the volume, so the endpoint an
operator consults to distinguish "a flood is in progress" from "the fleet is quiet" gives the wrong
answer with total confidence. A control that reports zero is worse than one that reports nothing.

## Resolution

Closed 2026-08-24 (handletaskstatus-pair), shipped in one pass with the fence-rejection counter item -
same 60-line region, same publish checklist.

All three sites named by the deep review are now inside the per-connection ingest log budget, via three
new `logKind`s added across `internal/worker` and `internal/api` **in a single commit**, because slice
2 of this cluster shipped a fully correct sixth kind that was counted on one side and published under
no JSON key with all three packages green.

The item was right that it is three sites and not two arms: `FailDependentTasks` is on neither
`pgx.ErrNoRows` arm and could not be gated on it anyway, since it is `:exec` and cannot return that
error. Measured at HEAD: thirteen `log.Printf` sites in `handleTaskStatus`'s file, eight now budgeted
against five before, and **no existing site lost its budget**.

### One defect found while doing it, and one caveat the fix introduced

**A short-circuit operand order carried a side effect.** Swapping
`!errors.Is(err, pgx.ErrNoRows) && lim.allow(...)` to the other order compiles, vets clean, changes no
log line and leaves the whole module green - while letting the cheapest forged message (a well-formed
uuid naming no task) spend a budget token and claim a dedupe slot on every call. A second instance of
the same shape was found on `FailDependentTasks`' arm, and a later AST walk over all fifteen `&&`/`||`
sites in both packages established there is no third. Pinned by
`TestHandleTaskStatus_TheSilentArmsSpendNoBudget`, poisoned input first.

**The three lines are now suppressible by the connection's own peer**, which they were not before -
the 16-token bucket is shared across all eight kinds, and one kind's dedupe key is wire-derived. Filed
as [[bug-2026-08-24-wire-keyed-dedupe-lets-a-peer-suppress-its-own-diagnostics]] with the reproduction.
Per-connection only and itself counted under `ingest_log_budget.counts.suppressed.*`, so it is
detectable - but an agent causing status-write failures can now hide them from its own connection's log.

Note for whoever reads `status_fail_dependents`: a non-zero value is a **data-integrity** signal, not a
logging one. `FailDependentTasks` failing leaves the failed task's dependents `pending` forever, and
`GetEligibleTasks` blocks on `dep.status != 'done'`, so the job is stuck.
