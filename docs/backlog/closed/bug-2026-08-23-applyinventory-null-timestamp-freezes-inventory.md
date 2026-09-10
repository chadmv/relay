---
title: applyInventory converts an unparseable last_used_at to SQL NULL against a NOT NULL column, silently freezing the worker's inventory
type: bug
status: closed
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - backend invariants lens finding N5
closed: 2026-09-10
resolution: fixed
---

# applyInventory converts an unparseable last_used_at to SQL NULL against a NOT NULL column, silently freezing the worker's inventory

## Summary
`applyInventory` (`internal/worker/handler.go:1387-1411`) does
`ts, _ := time.Parse(time.RFC3339, u.LastUsedAt)` at `:1397` - discarding the parse error, with a
comment reading "blank -> zero time" as though that were benign - then binds
`pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()}`, i.e. SQL NULL, into
`worker_workspaces.last_used_at`, which is `TIMESTAMPTZ NOT NULL`
(`internal/store/migrations/000007_workspaces.up.sql:11`). The constraint violation aborts the
whole `BeginTxFunc`, rolling back the `ReplaceWorkerInventory` delete with it - so one malformed
timestamp freezes the worker's entire inventory at its previous state, and the dispatcher keeps
scoring warm-workspace affinity from stale rows (`internal/scheduler/dispatch.go:110-120`). The
only signal is one unbudgeted log line at `:590`. `applyInventoryUpdate` (`:1414-1429`) has the
identical conversion on the streaming path.

## Context
The comment states a behaviour the schema forbids three lines later - the recorded "a principle in
a comment is not a check" shape. The value is caller-supplied (a third-party agent, or a clock
oddity on a real one), and the trust posture of this path was already established by the ingest
work: agent-supplied strings are validated nowhere.

## Proposal
Reject or clamp, never NULL: an unparseable or zero `last_used_at` either fails that single
workspace entry with a counted/logged reason, or clamps to `NOW()` - either keeps the transaction
committable. Fix both `applyInventory` and `applyInventoryUpdate`; correct the comment.

## Acceptance / Done When
- A malformed `last_used_at` in one inventory entry no longer aborts the whole replace; the rest
  of the inventory lands (RED at HEAD: whole transaction rolls back).
- Both the replace and streaming-update paths are covered by the same test shape.
- The "blank -> zero time" comment is corrected to describe the actual behavior.

## Related
- `internal/worker/handler.go:1387-1429`, `internal/store/migrations/000007_workspaces.up.sql:11`, `internal/scheduler/dispatch.go:110-120`
- [[idea-2026-04-25-last-used-at-accuracy-sweeper]] (Deferred) - accuracy of the same field on the agent side
- [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]] - `:590` is one of its unbudgeted sites

## Amendment 2026-08-25

- Its regression test is now cheap and default-lane. `Handler.pool` is a `txBeginner`
  interface, so a `fakeTx.Exec` returning a NOT NULL violation reproduces this without
  Postgres - `TestFinishRegister_SucceedsWhenTheInventoryTransactionFails` in
  `internal/worker/handler_register_success_test.go` already injects exactly that error
  for a different purpose.
- Its line citations have drifted: `applyInventory` is at `internal/worker/handler.go:1770-1794`,
  not `:1387-1411`.

## Resolution

Fixed in PR #209, squashed as `ef86c877`, as a consequence of the slice that closed
[[idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key]]. The two are the same
mechanism on different columns: one malformed inventory row must not poison the whole batch.

All three Done-When criteria are satisfied. `inventoryUpsertParams`
(`internal/worker/inventory_params.go`) refuses an unstorable `last_used_at` BEFORE any statement runs,
`applyInventory` drops that row and continues, and the batch still commits - so the rest of the
inventory lands. Both the replace path and the streaming-update path route through the same constructor,
so they share one test shape. The "blank -> zero time" comment is gone.

**The parse alone is not the check, and that is why there are two arms.** An unparseable value yields
the zero `time.Time`, which binds as SQL NULL against a `TIMESTAMPTZ NOT NULL` column - but a zero
`time.Time` also FORMATS as the year-one RFC3339 instant, which parses cleanly. So a value that got past
the parse can still be NULL by the time it is bound, and both `time.Parse` failing and `ts.IsZero()` are
refused separately.

`time.Parse`'s own error message echoes its input, so it is deliberately not wrapped: the returned error
carries a column name and a compile-time bound and nothing agent-supplied. That matters because
`handleInventoryUpdate`'s existing rule is never to log the update itself.

This item's Amendment asserted in the present tense that it was not fixed. That sentence was deleted
rather than rewritten, since a correction in place is what regenerates this class of defect on this
project. The amendment's other content - that the regression is reproducible in the default lane
through the `txBeginner` seam - held, and is what made the drop-versus-rollback distinction testable
without Postgres.

Concretely reachable before the fix: a legacy or hand-edited `registry.json` with a missing
`last_used_at` unmarshals to the zero `time.Time`, the agent formats it as `0001-01-01T00:00:00Z`, and
the whole inventory replace aborted. Now that one row is dropped and counted.
