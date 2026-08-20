---
title: Key reconcile's task maps on the raw [16]byte rather than a rendered string, deleting the spelling-mismatch bug class instead of re-encoding around it
type: idea
status: open
created: 2026-08-20
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

## Related

- Source: `internal/worker/handler.go` (`reconcileRunningTasks`, `uuidStr`)
- The bug this would have made unrepresentable:
  [[bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones]] (closed),
  `docs/retros/2026-08-20-reconcile-canonical-task-ids.md`
- The natural companion, which also wants the loop to hold `t.ID` rather than a string:
  [[bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence]]
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
it was missed.

Filed at **low**. The bug is closed, the tests hold it closed, and this is about making the closure
structural rather than behavioural.
