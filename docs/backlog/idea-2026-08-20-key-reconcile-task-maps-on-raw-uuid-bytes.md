---
title: Key reconcile's task maps on the raw [16]byte rather than a rendered string, deleting the spelling-mismatch bug class instead of re-encoding around it
type: idea
status: open
created: 2026-08-20
updated: 2026-08-20
priority: low
source: Phase 4 security lens of the 2026-08-20-reconcile-canonical-task-ids slice
---

# Key reconcile's task maps on the raw [16]byte rather than a rendered string, deleting the spelling-mismatch bug class instead of re-encoding around it

## Summary

`reconcileRunningTasks` (`internal/worker/handler.go`) keys `serverSet` and `agentSet` on a
**rendered UUID string**. The 2026-08-20 fix made both renderings agree by canonicalizing the wire id
through `pgtype.UUID.Scan` -> `uuidStr` before either map operation. That is correct and it is
guarded by tests, but it closes the bug by making two renderings match rather than by removing the
rendering.

`pgtype.UUID.Bytes` is a `[16]byte`, which is comparable and therefore a legal map key. Keying on it
removes the question "did I render this the same way on both sides?" from the function entirely, and
the property becomes structural rather than test-enforced.

## Proposal

- `serverSet` becomes `map[[16]byte]int64`, keyed on `t.ID.Bytes` from `GetActiveTasksForWorker`.
- `agentSet` becomes `map[[16]byte]bool`, keyed on `tID.Bytes` after the existing `Scan`.
- The reported loop drops the `canonical := uuidStr(tID)` line. `cancelIDs` keeps echoing
  `rt.TaskId` verbatim - that decision is a wire contract, is argued at the site, and is pinned by
  `TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings`. **Do not fold this change into a
  re-litigation of the echo.**
- The requeue loop constructs `pgtype.UUID{Bytes: k, Valid: true}` directly and drops its redundant
  re-`Scan` of a string this same function just rendered.

**One trap, and it is the reason this is not a pure simplification.** `uuidStr` returns `""` for an
invalid UUID, and the requeue loop's `Scan("")` then fails, so today the function **fails closed by
accident** on a `pgtype.UUID` with `Valid: false`. Raw `.Bytes` keying would silently promote a
zero-value UUID to `Valid: true` and requeue whatever task id is all zeroes. Not reachable today -
`tasks.id` is a NOT NULL primary key and `GetActiveTasksForWorker` selects it directly - but the
replacement must carry an explicit `if !t.ID.Valid { continue }` so it fails closed **on purpose**.

### Evidence added 2026-08-20 - `serverSet` now has a second consumer and a narrowing conversion

No scope change: the loop still holds a string and still re-`Scan`s it, so the proposal above is
intact and un-narrowed. What changed is the strength of the case.

The 2026-08-20 requeue-fence slice made `serverSet`'s **value** load-bearing. It was already
`map[string]int64`; the requeue loop used to range over keys only and throw the value away, and now
reads it:

```go
for taskIDStr, srvEpoch := range serverSet {
    ...
    n, _ := h.q.RequeueTaskByID(ctx, store.RequeueTaskByIDParams{
        ID:              tID,
        AssignmentEpoch: int32(srvEpoch),
        WorkerID:        workerID,
    })
```

So there are now **two** consumers of the map - the reported loop's epoch comparison against proto's
`int64 RunningTask.Epoch`, and this fence argument, which needs the `int32` the column actually is.
That is the point at which a two-field map value should become a struct carrying `pgtype.UUID` and
`int32` rather than a string and an `int64`, and doing so subsumes this item's change rather than
competing with it. Taken together the refactor now removes **three** things instead of two:

- the `int32(srvEpoch)` narrowing conversion at the fence call, which exists only because the map
  widened an `int32` column to `int64` for the *other* consumer (this is new since the item was
  filed, and it is commented in place as lossless, which it is - but a struct makes the comment
  unnecessary rather than merely true);
- the `tID.Scan(taskIDStr)` that re-parses a string this same function rendered from a `pgtype.UUID`
  ninety lines earlier;
- that `Scan`'s `continue` branch, which is the accidental fail-closed described above and is
  unreachable in practice.

**The two-consumer fact also sharpens the trap.** With one consumer, a wrong key was a missed match.
With two, the value carried alongside the key is now an argument to a *fenced write*, so a struct that
lets the id and the epoch be populated independently would be a way to pass a mismatched pair. Keep
them in one struct built at one place from one `GetActiveTasksForWorker` row - the same rule
`dispatchOne` states at its own call site, where both fence values are read off a single `RETURNING`
row and the comment forbids substituting an equal-looking value from elsewhere.

Source: `docs/retros/2026-08-20-requeue-task-by-id-fence.md`.

## Acceptance / Done When

- `reconcileRunningTasks` contains no UUID string as a map key or map lookup.
- `TestRegisterWorker_ReconcilesRunningTasks` and
  `TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings` both pass with their **files
  byte-identical** - this is a behaviour-preserving refactor and an assertion needing adjustment IS
  the finding.
- That gate is necessary but **not sufficient**, per
  `docs/retros/2026-08-14-cursor-pager-hook.md`: a zero-diff test gate held there and was still
  decorative for six of seven re-wirings until each was mutated. Mutate the new `t.ID.Valid` guard
  and confirm something reddens, or add the test that makes it load-bearing.
- The `cancelIDs` echo behaviour is unchanged for every input, parseable or not.
- **Added 2026-08-20:** the id and the epoch handed to `RequeueTaskByID` are built from one
  `GetActiveTasksForWorker` row and cannot be populated independently, and the `int32(srvEpoch)`
  narrowing conversion is gone rather than moved. Mutating the epoch argument to a zero value must
  still redden `TestRegisterWorker_ReconcilesRunningTasks` afterwards - that mutation is the only
  coverage the production wiring has.

## Related

- Source: `internal/worker/handler.go` (`reconcileRunningTasks`, `uuidStr`)
- The bug this would have made unrepresentable:
  [[bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones]] (closed),
  `docs/retros/2026-08-20-reconcile-canonical-task-ids.md`
- The slice that gave `serverSet`'s value a second consumer and added the narrowing conversion:
  [[bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence]],
  `docs/retros/2026-08-20-requeue-task-by-id-fence.md`
- The real bound on the cost figures below:
  [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]
- The zero-diff-refactor-gate lesson: `docs/retros/2026-08-14-cursor-pager-hook.md`

## Notes

**Perf is context, not the motivation.** Measured while the security lens was reading the loop:
`uuidStr` is a `fmt.Sprintf` with five boxed slice arguments, roughly 8-10 allocations and about a
microsecond per reported element, against a bare map insert afterwards. `reported` is uncapped and
bounded only by grpc-go's default 4 MiB receive limit - about 110k undashed ids - so a maximum-size
`RegisterRequest` moves registration CPU from roughly 0.01s to 0.1s of a core. That is a rounding
error next to the fact that connection admission itself is unbounded, which is the item that actually
bounds this.

If the structural change is judged not worth the churn, the one-line alternative is `tID.String()`
in place of `uuidStr(tID)`: byte-identical output, one allocation instead of ten. It does not delete
the bug class, so it is the lesser option, and it is recorded only so the next reader does not think
it was missed. **Note as of 2026-08-20 that this alternative no longer addresses the second half of
the case** - it does nothing about the map value, the narrowing conversion, or the id/epoch pairing.

Filed at **low**. The bug is closed, the tests hold it closed, and this is about making the closure
structural rather than behavioural.
