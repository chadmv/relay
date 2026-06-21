---
title: Background sweeper has a Prepare TOCTOU on the dominant eviction path
type: bug
status: closed
created: 2026-06-21
closed: 2026-06-21
resolution: fixed
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

## Resolution
Fixed 2026-06-21 (sweeper-prepare-toctou). The background sweeper now routes each
per-entry eviction through the same `p.evicting` atomic-claim discipline as the manual
`EvictWorkspace` path: a new optional `Sweeper.Claim` hook (wired to the new
`Provider.ReserveForEvict` only on the background sweeper in `cmd/relay-agent/main.go`)
reserves `p.evicting[shortID]` under `p.mu` after an inline holder re-check, holds it for
the duration of the slow `DeleteClient`+`RemoveAll`, and clears it in a defer. `Prepare`'s
existing post-`Acquire` re-check of `p.evicting` now observes a sweeper claim and backs out,
so a live `Prepare` never syncs into a workspace the sweeper is deleting; exactly one of
{sweep, prepare} proceeds. A lost claim is a benign `ErrEvictClaimLost` sentinel that
`SweepOnce` skips without logging or counting; the internal Sweeper that `EvictWorkspace`
builds keeps `Claim` nil (it already holds the reservation). The `EvictWorkspace`-built
Sweeper is unchanged. Covered by a deterministic, timing-free regression test
(`sweeper_claim_test.go`) that drives a concurrent `SweepOnce` into the Prepare gap via the
existing `prepareAcquireHook`/`gatingRunner` harness and asserts the sweep wins and Prepare
backs out with "being evicted" and zero holders; proven RED (compile failure) before the
fix. relay-verify returned no high/medium findings; two low maintainability notes were
addressed with cross-reference comments on the twin reservation blocks.
