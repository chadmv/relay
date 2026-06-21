---
title: Provider.EvictWorkspace leaks per-task state and has a lock TOCTOU
type: bug
status: closed
created: 2026-06-20
closed: 2026-06-21
resolution: fixed
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
- [[bug-2026-06-21-sweeper-prepare-toctou]] - the same TOCTOU class on the background
  sweeper path, split out during this fix

## Resolution
fixed - both defects on the manual `EvictWorkspace` path are closed. (1) The ad-hoc Sweeper
now wires `OnEvictedCB: p.InvalidateWorkspace`, so per-task state (`syncedPaths`) is
invalidated on a manual eviction. (2) The Prepare TOCTOU is closed via a `p.evicting`
reservation set: `EvictWorkspace`, under `p.mu`, does an inline holder check and reserves
the short ID, then runs the slow `evict` (DeleteClient + RemoveAll) lock-free and clears the
reservation in a defer; `Prepare` refuses a reserved short ID at get-or-create AND re-checks
`p.evicting` under `p.mu` after `ws.Acquire`, backing out (releasing the handle) if a
reservation landed in the pre-check->Acquire gap. Because Evict's holder-check+reserve is
one `p.mu` critical section and Prepare's Acquire happens-before its re-check, exactly one of
{evict, prepare} wins and the loser does no harm. Only a bool crosses `p.mu`
(no-interior-pointers); `p.mu`->`ws.mu` ordering preserved; `evict` stays lock-free so
`OnEvictedCB` re-acquiring `p.mu` cannot deadlock. A first reservation-only implementation
left a residual window (a Prepare past its pre-check but pre-Acquire); relay-verify caught
it and the post-Acquire re-check closed it, proven load-bearing-red with a deterministic
channel-synchronized test. Verified under `-race` (MSYS2) and the p4d integration suite. The
same TOCTOU class on the BACKGROUND sweeper (the dominant eviction trigger, which never sets
`p.evicting`) was out of scope here and is tracked in
[[bug-2026-06-21-sweeper-prepare-toctou]]. Plan:
`docs/superpowers/plans/2026-06-21-evictworkspace-state-leak-toctou.md`.
