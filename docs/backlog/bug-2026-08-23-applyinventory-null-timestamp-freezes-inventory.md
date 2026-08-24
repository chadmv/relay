---
title: applyInventory converts an unparseable last_used_at to SQL NULL against a NOT NULL column, silently freezing the worker's inventory
type: bug
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - backend invariants lens finding N5
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
