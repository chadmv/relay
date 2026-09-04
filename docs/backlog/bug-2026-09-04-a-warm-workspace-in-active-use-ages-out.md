---
title: A warm workspace reused at its baseline never refreshes last_used_at, so it ages out while in active use
type: bug
status: open
created: 2026-09-04
priority: low
source: Phase 4 re-verify lens on the perforce client-path slice
---

# A warm workspace reused at its baseline never refreshes last_used_at, so it ages out while in active use

## Summary

`WorkspaceEntry.LastUsedAt` is written by the cold-path `Upsert` and by the post-sync `Mutate`.
A warm workspace whose baseline already matches the request skips the sync entirely, so neither
writer runs - and the entry keeps ageing. Under `RELAY_WORKSPACE_MAX_AGE` the sweeper can
therefore evict a workspace that tasks are using continuously, between two of those tasks. The
next task pays a full re-sync of what may be a 1 TB+ tree.

## Context

Pre-existing rather than introduced by the client-path slice, but that slice's warm-path
`Registry.Mutate` block is where the fix would go, and its two sweeper-claim tests now
structurally DEPEND on the current behaviour: they seed a stale entry and rely on a warm Prepare
not refreshing it, which is how the age pass still selects it. Fixing this means re-seeding both.

## Proposal

Refresh `LastUsedAt` on every successful `Prepare`, not only when a sync ran - the field's meaning
is "when was this workspace last used", and being handed to a task is a use. Then re-seed
`TestSweeperClaim_*` so their gating does not depend on the omission.

## Acceptance / Done When

- A warm workspace handed to a task refreshes its `LastUsedAt`.
- The two sweeper-claim tests still drive their gate, by a seeding path a warm Prepare cannot undo.

## Related

- `internal/agent/source/perforce/perforce.go` (`Prepare`), `sweeper.go`, `sweeper_claim_test.go`
