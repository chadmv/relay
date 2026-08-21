---
title: handleTaskStatus's two fence-rejection arms are silent and uncounted, so a discarded TERMINAL status report leaves no runtime signal
type: idea
status: open
created: 2026-08-21
priority: medium
source: Phase 4 of the 2026-08-21-silent-drop-observability slice 3; the sibling arms the closing item's scope deliberately excluded
---

# handleTaskStatus's two fence-rejection arms are silent and uncounted

## Summary

Slice 3 (`docs/retros/2026-08-21-silent-drop-observability-slice3.md`) closed the fence-rejection blind
spot for **one** of the three epoch-fenced writes on the gRPC recv path. The other two are unchanged:

```go
// internal/worker/handler.go:964-977 - IncrementTaskRetryCount
if _, err := h.q.IncrementTaskRetryCount(ctx, ...); err != nil {
    if !errors.Is(err, pgx.ErrNoRows) {
        log.Printf("worker: handleTaskStatus IncrementTaskRetryCount %s: %v", uuidStr(taskID), err)
    }
}

// internal/worker/handler.go:1013-1023 - UpdateTaskStatus
if err != nil {
    if !errors.Is(err, pgx.ErrNoRows) {
        log.Printf("worker: handleTaskStatus UpdateTaskStatus %s -> %s: %v", uuidStr(taskID), statusStr, err)
    }
    return
}
```

Both are the same shape `handleTaskLog`'s arm had before slice 3: `pgx.ErrNoRows` is swallowed with no
log line (correctly), no counter, no metric, and nothing returned to the agent. Both comments say
"drop silently, exactly like the two gates", which was defensible while there was no read surface.
**There is one now** - `GET /v1/server/counters`, admin-only, with three sections shipped - and this
arm is not on it.

**The motivating case is SHARPER than the log one, and this is the reason to file rather than shrug.**
A `handleTaskLog` fence rejection loses a chunk of task output. An `UpdateTaskStatus` fence rejection
means **a terminal status report was discarded**: the agent said "this task finished" and the
coordinator refused to record it. Its live, non-adversarial cause is the coordinator stale-task
watchdog having already bumped the epoch. README says of `RELAY_TASK_WATCHDOG_MARGIN`:

> set it too small and healthy work is killed with no way for the agent to object

The observable outcome is a task marked `timed_out` that **actually succeeded**, with no runtime signal
of any kind - the same class of misconfiguration-with-no-symptom that `task_log_fence.counts.
rejected_total` was shipped to make visible, one status machine higher.

## Two nuances that must not be rediscovered at spec time

Both were established while scoping slice 3 and are the reason this is not a copy-paste of that slice.

### (a) The status counter is NOISIER than the log one, and a single scalar needs its interpretation written down

`UpdateTaskStatus` carries `AND status IN ('pending','dispatched','running')` - a terminal row is not
writable at all, by design, so that a second terminal message from the task's own assignee at the same
epoch cannot flip or resurrect it. **A duplicate terminal message from a perfectly healthy agent is
therefore an EXPECTED rejection**, and it is indistinguishable from an epoch end: same statement, same
`pgx.ErrNoRows`, same absence of a row to carry a reason.

So unlike `rejected_total`, whose baseline on a healthy fleet is genuinely near zero, this number has a
**non-zero healthy floor whose height depends on agent retry behaviour**. A single scalar published with
no interpretation reads as constant alarm, which is worse than no number: an operator who learns to
ignore it has lost the signal permanently. Whatever ships must either state the expected baseline in the
payload's own documentation and README, or split the arms (the retry-branch rejection and the
status-write rejection are two different statements and could be two keys) - decided with an argument,
not by defaulting to one number because slice 3 did.

### (b) The number is a FLOOR, not a measurement

`handleTaskStatus` runs **two Go-side gates before either write** and both `return` silently:

- the identity gate (`handler.go:914`): `!workerID.Valid || !task.WorkerID.Valid || task.WorkerID.Bytes != workerID.Bytes`
- the currency gate (`handler.go:927`): `int64(task.AssignmentEpoch) != upd.Epoch`

A forged or stale message that would have been rejected by the SQL fence is frequently rejected by one
of these first, and never reaches the write. The counter therefore counts rejections **that survived the
Go-side pre-filter**, which is a strict subset. Say so where the number is declared and in README, the
way `refused_per_source` documents its own floor semantics. Note this differs from `handleTaskLog`,
which has no equivalent pre-filter - so the two numbers are not comparable and the payload must not
invite the comparison.

## Context

**Not a duplicate of [[bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget]], and
the relationship is worth stating precisely because they are one line apart.** That item is about the
**other arm of the same `if`**: the `!errors.Is(err, pgx.ErrNoRows)` branch, where a real database error
IS logged and the line does not consult the per-connection log budget. This item is about the
`errors.Is` branch, where the fence rejected and nothing is recorded at all. Different noun (a
suppressed log line versus a discarded status report), different remedy (route an existing line through
`lim.allow` versus add a counter and a payload section), and **no input executes both**. This is
exactly the relationship `idea-2026-08-14-tasklog-fence-rejection-is-unobservable` had with
`idea-2026-08-15-ingest-log-suppression-is-uncounted`, which were correctly kept separate and shipped as
two slices. They should be read together and may ship together; they must not be merged.

**The pattern to copy is settled and cheap**, which is most of the argument for medium rather than low
priority. Slice 3 established, in a lane CI runs: a value `atomic.Uint64` field on `*worker.Handler`
(zero value ready, no nil case), an exported scalar accessor, its own `api.CounterSources` field, a
section in `internal/api/server_counters.go`, and a `stubFenceDB` (`internal/worker/tasklog_fence_
counter_test.go`) that drives the **real** statement through a stub `store.DBTX` with **no container**.
`handleTaskStatus` reaches `IncrementTaskRetryCount`/`UpdateTaskStatus` through the same `store.Queries`
seam, so the same harness applies - **check that rather than tagging the test by reflex**, which is the
mistake slice 3's plan caught (R4).

**Read `cmd/relay-server/counters_wiring_test.go` before adding the section.** Slice 3 rewrote the
completeness relation twice: the string-list version was proved decorative (a fourth section satisfied
by appending one token, green module-wide), and the shipped answer is an **executed** count of served
top-level keys against `NumField(api.CounterSources)` plus a walk over `buildHTTPServer`'s own
assignments. Adding a section that reuses `agentHandler` costs nothing there; adding one that does not
needs a `wiredDep` row. Do not relax either check.

## Proposal

To be argued at spec time rather than adopted as written.

- **Two counters or one, decided with an argument.** The retry-branch rejection and the status-write
  rejection are different statements with different meanings (`IncrementTaskRetryCount` refusing means a
  retry was not burned, which is usually the correct outcome; `UpdateTaskStatus` refusing means a
  terminal report was discarded, which is the actionable one). Merging them buries the signal in the
  noisier arm. Splitting them costs one more JSON key.
- **A value `atomic.Uint64` field on `*worker.Handler` per counter**, incremented inside the existing
  `errors.Is` branch, before the existing control flow. No log line - the same argument holds
  (caller-driven volume on the recv goroutine, firing on the legitimate duplicate-terminal case) and
  both arms' comments already say detection "belongs with the audit-log work".
- **Its own `api.CounterSources` field and its own section**, never a widened `TaskLogFenceSource`.
  Three controls on one `*worker.Handler` is already the shipped shape and `buildHTTPServer`'s single
  nil filter absorbs a third assignment.
- **Per-reason splitting is DECLINED here for the same priced reason as `task_log_fence`**, not
  impossible: both statements are single-row `UPDATE ... WHERE` forms that return `pgx.ErrNoRows` on any
  predicate failure, so there is nothing to carry a reason. Recovering it needs a second round trip
  (forbidden on this path) or a result-contract rewrite. **Write "declined, and here is the price", never
  "impossible"** - that correction was slice 3's headline finding.
- **State the healthy baseline and the floor semantics** in the payload doc and README, per (a) and (b)
  above.

## Acceptance / Done When

- A fence-rejected `UpdateTaskStatus` increments a counter, proven by a handler-layer test that reads
  the counter across a rejection **and** a success, in the DEFAULT lane (no container) unless a
  measurement shows one is required.
- The same for the `IncrementTaskRetryCount` arm, whether or not it shares a key.
- The success leg establishes acceptance **positively**, not by a projection every other arm also
  produces - slice 3 shipped that defect and closed it by asserting the publish. For this handler the
  equivalent positive is the follow-on effect the accepted path has and the rejected path does not.
- The counter(s) are readable through `GET /v1/server/counters` with an unwired section ABSENT rather
  than zero-valued, and a wired-but-zero section still PRESENT.
- No new log line on either arm, no new DB round trip, no goroutine, no queue and no lock on the recv
  goroutine.
- One-counter-versus-two is a stated decision with its reason recorded where the counters are declared.
- **The expected healthy baseline is documented** - a duplicate terminal message from a healthy assignee
  is an expected rejection - so the number does not read as constant alarm.
- **The floor semantics are documented**: the two Go-side gates pre-filter, so this counts rejections
  that reached the write.
- The payload states what the number does NOT cover, and in particular does not invite comparison with
  `task_log_fence.counts.rejected_total`, which has no equivalent pre-filter.
- `TestHandleTaskStatus_*` and every `internal/store` fence guard still pass with no assertion weakened.

## Related

- Source: `internal/worker/handler.go` (`handleTaskStatus`: the identity gate `:914`, the currency gate
  `:927`, the `IncrementTaskRetryCount` arm `:964-977`, the `UpdateTaskStatus` arm `:1005-1024`)
- The pattern to copy, shipped 2026-08-21 by slice 3: `internal/worker/handler.go` (the
  `taskLogFenceRejects` field and the `pgx.ErrNoRows` arm's comment),
  `internal/worker/tasklog_fence_counter_test.go` (`stubFenceDB` - the default-lane harness),
  `internal/api/server_counters.go` (`TaskLogFenceSource` and `taskLogFenceSection`),
  `cmd/relay-server/counters_wiring_test.go`
  (`TestBuildHTTPServer_EverySourceFieldProducesAServedSection` and `countersAssignmentSources`)
- The item this completes: [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]
  (**closed 2026-08-21**) - it deliberately scoped itself to `handleTaskLog`'s arm alone
- The sibling on the **other arm of the same `if`**, to be read together and NOT merged:
  [[bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget]]
- The knob whose misconfiguration this detects: `RELAY_TASK_WATCHDOG_MARGIN` /
  `RELAY_TASK_MAX_ASSIGNMENT` (`cmd/relay-server/watchdog_config.go`), and the README sentence that
  names the failure mode with no way for the agent to object
- The slice that made the epoch bump reachable from the coordinator:
  `docs/retros/2026-08-20-coordinator-stale-task-watchdog.md`
- Why a terminal status is not writable at all (the source of the expected-rejection baseline):
  CLAUDE.md's Epoch fence invariant, `internal/store/query/tasks.sql` (`UpdateTaskStatus`'s status
  allow-list)
- Also wants a watchdog-side number: [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]
  - a sweep counted there and a discarded terminal report counted here are the **two ends of the same
  event**, seen from the coordinator and from the agent. Whoever ships the second should say how the two
  numbers relate, because an operator will try to reconcile them and they will not match (the watchdog
  sweeps tasks whose agents are gone and never report at all).

## Notes

Filed at **medium**, one step above the two log-budget siblings, on the strength of the outcome rather
than the mechanism: those items describe log lines an operator cannot see, this one describes **a
successful task recorded as a timeout**. Nothing here is a security exposure - reaching either arm needs
a registered agent that already passed the identity gate for a task it owns.

The generalizable rule, which is the closing item's own rule pointed one function to the left: **a
silent rejection path needs a counter the day a second reason to reject is added to it.** These two arms
each acquired their second reason on 2026-08-20, when the coordinator watchdog became a writer that can
end an assignment without the agent knowing. The moment to add the number has already passed, exactly as
it had for `handleTaskLog`.
