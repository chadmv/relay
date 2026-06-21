---
title: Background sweeper has a Prepare TOCTOU on the dominant eviction path
type: bug
status: open
created: 2026-06-21
priority: medium
source: surfaced by relay-verify while fixing evictworkspace-state-leak-toctou
---

# Background sweeper has a Prepare TOCTOU on the dominant eviction path

## Summary
The eviction-vs-`Prepare` TOCTOU was closed for the manual `Provider.EvictWorkspace` path
(via the `p.evicting` reservation + a post-`Acquire` re-check in `Prepare`), but the
BACKGROUND sweeper - the dominant eviction trigger - has the same race and never sets
`p.evicting`. `SweepOnce` snapshots holders once via `ListLocked` (`sweeper.go`), then
`evict` runs the slow `DeleteClient` + `os.RemoveAll`. Between the snapshot and the
`RemoveAll`, a concurrent `Prepare` can pass its checks and `ws.Acquire` (it is gated only
by `p.evicting`, which the sweeper never sets) and start a task whose subprocess syncs into
the directory the sweeper is deleting.

## Context
Found by the relay-verify pass while fixing
[[bug-2026-06-20-evictworkspace-state-leak-toctou]]. That fix scoped only to the manual
`EvictWorkspace` API per its backlog item; this is the same TOCTOU class on the separate,
pre-existing background-sweeper code path, which is the more common eviction trigger, so it
warrants its own fix.

## Proposal
Route the background sweeper's per-entry eviction through the same atomic-claim discipline
so both eviction paths share one guard. Options:
- Give `Sweeper.evict` a claim/release hook that sets `p.evicting[shortID]` under `p.mu`
  after a holder re-check and clears it in a defer, OR
- Have `SweepOnce` evict each candidate through `Provider.EvictWorkspace` (which already has
  the reservation + re-check), OR
- At minimum, re-check the holder/reservation set under `p.mu` immediately before
  `DeleteClient`/`RemoveAll` rather than relying on the `SweepOnce`-time `ListLocked`
  snapshot.
Mirror the manual path: a `Prepare` that loses the race must back out (release, no sync),
and the eviction must not delete a workspace a live `Prepare` has acquired.

## Related
- `internal/agent/source/perforce/sweeper.go` (`SweepOnce`, `evict`, `ListLocked` snapshot)
- `internal/agent/source/perforce/perforce.go` (`Provider.EvictWorkspace`, `Prepare`, `p.evicting`)
- `cmd/relay-agent/main.go` (background sweeper construction)
- [[bug-2026-06-20-evictworkspace-state-leak-toctou]] (the manual-path fix this was split from)
