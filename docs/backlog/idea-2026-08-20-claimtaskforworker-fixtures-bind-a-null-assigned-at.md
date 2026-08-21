---
title: Most ClaimTaskForWorkerParams literals omit AssignedAt and silently bind SQL NULL, so a test written from one is exempt from the watchdog's absolute arm
type: idea
status: open
created: 2026-08-20
priority: low
source: conductor /code-review of the 2026-08-20-coordinator-stale-task-watchdog slice, downgraded to low by all three Phase 4 lenses
---

# Most ClaimTaskForWorkerParams literals omit AssignedAt and silently bind SQL NULL

## Summary

Migration `000021` added `tasks.assigned_at`, and `ClaimTaskForWorker` now writes it from a
caller-supplied Go value. `store.ClaimTaskForWorkerParams` gained an `AssignedAt` field.

A **keyed Go struct literal that omits the new field compiles**, binds the zero
`pgtype.Timestamptz`, and therefore writes SQL NULL. The tree is full of such literals - the great
majority of `ClaimTaskForWorkerParams` construction sites are test fixtures spread across
`internal/store`, `internal/worker`, `internal/api`, `internal/scheduler` and
`cmd/relay-server`, and only the production caller plus a handful of watchdog-era tests set the
field.

The one production site is `Dispatcher.sendTask` in `internal/scheduler/dispatch.go`, which does set
it and is pinned by its own test, so **there is no live consequence today**. Every existing test that
actually runs the watchdog or the scan sets `AssignedAt` explicitly.

The cost is entirely future, and it is the quiet kind. The next person writing a watchdog or
scan-related test will copy one of the existing claim fixtures, get a row with `assigned_at IS NULL`,
and therefore get a row that is **silently exempt from the absolute arm** - because
`ListOverdueAssignedTasks`'s absolute arm requires `assigned_at IS NOT NULL`, deliberately and
correctly, as its own comment says at length ("EVERY ARM FAILS CLOSED ON A MISSING VALUE... Do NOT
'fix' any of them into `IS NULL OR ...`"). The test then passes for the wrong reason, or fails in a
way that invites exactly the fail-open "fix" the comment forbids. Nothing is red, nothing warns, and
the failure is a `nil`-shaped hole in a bool.

## Repro / Symptoms

1. Write a new integration test that claims a task with a `ClaimTaskForWorkerParams{ID: ..., WorkerID: ...}`
   literal copied from any of the older fixtures.
2. Backdate nothing; call `ListOverdueAssignedTasks` with the absolute arm enabled and a cutoff far in
   the future.
3. Observed: the row is not returned. It looks like the absolute arm is broken, or like the row is
   not assigned. It is neither - `assigned_at` is NULL because the literal did not mention it.

The same shape appears the other way round: a test asserting a row is **not** swept passes
vacuously, because the row could never have been swept by that arm regardless of what the test was
trying to prove.

## Context

Raised by the conductor's `/code-review` on the watchdog slice and independently downgraded to low by
all three Phase 4 lenses, which is the right call: nothing is broken, and the population of literals
is large.

Recorded deliberately **without a count of the affected literals**. The number moves with every test
added, a stale number in an item is a maintenance liability that reddens nothing, and the property
("almost all of them omit it; the production site and the watchdog-era fixtures do not") is what
matters. This follows the standing lesson from the 2026-08-20 reconcile retro: *the correct fix for a
stale count is usually to delete the count*.

## Proposal

**The fix is the fixture, not the field. Do not churn the literals.**

Rewriting every `ClaimTaskForWorkerParams` literal to add `AssignedAt` would be a large mechanical
diff across many packages, would bury any real change made in the same window, and would leave the
next literal exactly as breakable as the current ones. It also does not address the actual failure -
somebody copying an old pattern - since the new literals would be equally copyable and equally
omittable.

Better options, in preference order:

- **Point new tests at the fixtures that already do this correctly.** `internal/store` has two:
  `overdueFixture` (`dispatched`, `running`, `terminal`) in
  `list_overdue_assigned_tasks_integration_test.go`, which drives tasks into states **through the
  production statements**, and `assignedFixture.claimedAt` in `tasks_assigned_at_integration_test.go`.
  Both take the timestamp as a parameter, so omission is not expressible. The cheapest version of
  this item is a sentence in the right place pointing at them.
- **Or add one shared claim helper** that takes the timestamp as a required argument, put it where all
  three packages can reach it, and **migrate opportunistically** - a literal gets converted when
  somebody is already editing that test, never as a sweep.
- **Consider whether the field should be non-optional at the type level.** sqlc emits a plain struct
  and there is no ergonomic way to require a field in a Go keyed literal, so this is probably not
  achievable without a wrapper the rest of the codebase would have to adopt. Worth one paragraph of
  thought and then almost certainly rejecting - noted here so the next reader does not spend an hour
  rediscovering that Go has no required struct fields.

**Explicitly out of scope:** relaxing the absolute arm to tolerate a NULL `assigned_at`. That is the
fail-open direction, the statement's comment forbids it by name, and this item exists so that a
confusing test result does not get "fixed" that way.

## Acceptance / Done When

- A developer writing a new claim-based test is led to a construction path where the timestamp cannot
  be silently omitted - either by a helper that takes it as an argument, or by documentation at the
  point where they will look.
- No mass edit of existing literals. If any are converted, they are converted because that test was
  being edited anyway.
- The `assigned_at IS NOT NULL` predicate in `ListOverdueAssignedTasks` is unchanged, and whatever
  lands says so, so a future reader does not read this item as licence to relax it.

## Related

- Source: `internal/store/tasks.sql.go` (`ClaimTaskForWorkerParams`),
  `internal/store/query/tasks.sql` (`ClaimTaskForWorker`, whose comment calls this "THE ONLY
  LOAD-BEARING WRITE OF assigned_at", and `ListOverdueAssignedTasks`'s fail-closed paragraph),
  `internal/scheduler/dispatch.go` (`Dispatcher.sendTask`, the one production caller)
- The fixtures that already get it right: `internal/store/list_overdue_assigned_tasks_integration_test.go`
  (`overdueFixture`), `internal/store/tasks_assigned_at_integration_test.go` (`assignedFixture`)
- The rule the column lives by: `TestAssignedAtIsClearedWhereverWorkerIDIs`
- The slice that added the field: `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md`
  (section 5.2), `docs/retros/2026-08-20-coordinator-stale-task-watchdog.md`
- Adjacent in kind - a test that passes for the wrong reason:
  `docs/retros/2026-08-14-scheduled-job-owner-email.md` (a frontend test that was green *because of*
  the bug)

## Notes

The general shape, which is worth more than this instance: **adding a field to a struct that is
constructed by keyed literal across a codebase is a silent, tree-wide default change.** The compiler
helps with positional literals and says nothing about keyed ones, so the new field's zero value
becomes the de facto value everywhere, instantly, with no diff. When that zero value has semantics -
here, "this row is invisible to one of the watchdog's two arms" - the right response is to make the
correct construction path the easy one, not to chase the literals.
