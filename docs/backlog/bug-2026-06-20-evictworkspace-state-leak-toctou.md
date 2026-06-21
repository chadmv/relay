---
title: Provider.EvictWorkspace leaks per-task state and has a lock TOCTOU
type: bug
status: open
created: 2026-06-20
priority: medium
source: split from bug-2026-06-10-sweeper-wedges-dirty-delete during planning
---

# Provider.EvictWorkspace leaks per-task state and has a lock TOCTOU

## Summary
`Provider.EvictWorkspace` builds an ad-hoc `Sweeper` without an `OnEvictedCB`, so
`InvalidateWorkspace` never runs on a manual single-workspace eviction and in-memory
per-task state (e.g. `syncedPaths`) survives the eviction. Separately, `EvictWorkspace`
reads `lockedShortIDs()` and then calls `evict` with no lock held across the gap, leaving a
TOCTOU window against a concurrent `Prepare` that could acquire/use the workspace between
the check and the evict.

## Context
Split out of `bug-2026-06-10-sweeper-wedges-dirty-delete` (the sweeper-wedge fix) during
planning, to keep that fix surgical. Confirmed against current code: the *background*
sweeper in `cmd/relay-agent/main.go` already sets `OnEvictedCB: pp.InvalidateWorkspace`, so
the leak is confined to the ad-hoc sweeper inside `Provider.EvictWorkspace`. The TOCTOU
half needs a lock/acquire design decision (hold the lock across the check+evict, or
acquire-then-evict), which is why it warrants its own scoping rather than folding into the
wedge fix.

## Proposal
- Wire `OnEvictedCB: p.InvalidateWorkspace` into the ad-hoc Sweeper that `EvictWorkspace`
  constructs, so per-task state is invalidated on a manual eviction the same way it is for
  the background sweeper.
- Close the lock gap in `EvictWorkspace` so the locked-check and the evict are atomic with
  respect to a concurrent `Prepare`.

## Related
- `internal/agent/source/perforce/perforce.go` (`Provider.EvictWorkspace`, the ad-hoc
  Sweeper construction, `lockedShortIDs()`, `InvalidateWorkspace`, `syncedPaths`)
- `cmd/relay-agent/main.go` (background sweeper, already wires `OnEvictedCB`)
- [[bug-2026-06-10-sweeper-wedges-dirty-delete]] - the wedge fix this was split from
