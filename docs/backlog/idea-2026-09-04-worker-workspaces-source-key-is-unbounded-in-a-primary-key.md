---
title: worker_workspaces.source_key is agent-supplied, unbounded, and inside a primary key
type: idea
status: open
created: 2026-09-04
priority: medium
source: Recommended by the sync-spec exclusion paths design (2026-09-04)
---

# worker_workspaces.source_key is agent-supplied, unbounded, and inside a primary key

## Summary
`applyInventoryUpdate` and the registration-time bulk ingest pass the agent's `source_key`
straight to `UpsertWorkerWorkspace`, and the column sits inside the table's primary key. Nothing
bounds its length.

## Context
Found while specifying sync-spec exclusion paths
(`docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`, section 6.1). That design
deliberately chose a short fixed-width key format partly to stay clear of this, which means the
underlying absence of a bound stays untested rather than being fixed.

This is the same shape as the unvalidated hostname reaching a unique btree: a value that arrives
over the wire and lands in an index, where an over-long value fails the index rather than
conflicting.

## Related
- [[bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index]] - the same shape, already filed
- [[feature-2026-09-04-implement-sync-spec-exclusion-paths]] - the design that routes around this
- `internal/worker/` (`applyInventoryUpdate`), `internal/store/query/` (`UpsertWorkerWorkspace`)
