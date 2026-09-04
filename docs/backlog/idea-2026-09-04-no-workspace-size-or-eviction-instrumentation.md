---
title: Nothing records a workspace's size or counts evictions, so sweeper churn is unobservable
type: idea
status: open
created: 2026-09-04
priority: medium
source: Recommended by the sync-spec exclusion paths design (2026-09-04)
---

# Nothing records a workspace's size or counts evictions, so sweeper churn is unobservable

## Summary
`InventoryEntry` carries `SourceKey`, `ShortID`, `BaselineHash` and `LastUsedAt`, and no bytes.
`SweepOnce` returns the ids it evicted to `Sweeper.Run`, which discards them. So neither half of
the disk story is visible: not how much a workspace costs, and not how often the pressure pass
throws one away.

## Context
This became load-bearing while specifying sync-spec exclusions
(`docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`, section 3.4). That design
splits one workspace per exclusion set, which trades disk for eviction churn, and the spec had
to state the cost as UNQUANTIFIED because this repository cannot measure either term. Without
this instrumentation an operator has no way to tell whether exclusions are net positive on a
given agent, and the trade stays unquantified permanently.

## Acceptance / Done When
- A workspace's size is recorded somewhere an operator can read.
- Evictions are counted rather than discarded.

## Related
- [[feature-2026-09-04-implement-sync-spec-exclusion-paths]] - the design whose trade this measures
- `internal/agent/source/perforce/perforce.go` (`InventoryEntry`, `SweepOnce`, `Sweeper.Run`)
