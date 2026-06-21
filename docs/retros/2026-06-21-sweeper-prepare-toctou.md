---
date: 2026-06-21
topic: sweeper-prepare-toctou
branch: claude/gifted-meninsky-5fc18a
pr: "2026-06-21 / sweeper-prepare-toctou"
merge: "2026-06-21 / sweeper-prepare-toctou"
---

# Session Retro: 2026-06-21 - Background sweeper Prepare TOCTOU

**TL;DR:** Closed `bug-2026-06-21-sweeper-prepare-toctou`. The eviction-vs-`Prepare` TOCTOU
was already fixed on the manual `EvictWorkspace` path (the prior session), but the background
sweeper - the dominant eviction trigger - never set `p.evicting`, so a concurrent `Prepare`
could `Acquire` and sync into a workspace `SweepOnce`->`evict` was deleting. The sweeper now
shares the same atomic-claim discipline. Autopilot iteration 1 of a `/autopilot 4` run.

## What Was Built

- `internal/agent/source/perforce/sweeper.go`:
  - Added an optional `Sweeper.Claim func(shortID) (release func(), ok bool)` hook. `evict`
    calls it before any destructive work; on `!ok` it returns the new `ErrEvictClaimLost`
    sentinel without deleting, on `ok` it `defer release()` and proceeds.
  - `SweepOnce` treats `errors.Is(err, ErrEvictClaimLost)` as a benign skip in both the age
    and pressure passes (no log, not counted as evicted); real evict errors keep the
    log-and-continue behavior.
- `internal/agent/source/perforce/perforce.go`:
  - Added `Provider.ReserveForEvict`, mirroring `EvictWorkspace`'s holder-check + `p.evicting`
    reservation under the `p.mu`->`ws.mu` lock order, returning a release closure.
- `cmd/relay-agent/main.go`: wired `Claim: pp.ReserveForEvict` on the background `Sweeper`
  only. The internal Sweeper that `EvictWorkspace` builds keeps `Claim` nil (it already holds
  the reservation; claiming again would self-refuse).
- `internal/agent/source/perforce/sweeper_claim_test.go`: deterministic, timing-free
  regression test driving a concurrent `SweepOnce` into the Prepare gap via the existing
  `prepareAcquireHook`/`gatingRunner` harness. Proven RED (compile failure) before the fix.

## Key Decisions

- **Reuse the existing discipline, don't re-invent.** The manual path already had the
  `p.evicting` reservation + `Prepare` post-`Acquire` re-check. The sweeper fix is purely
  about making the sweeper *participate* in that same reservation, so `Prepare`'s existing
  re-check observes it. No `Prepare` change was needed.
- **Light dedup over a refactor.** relay-verify flagged that `ReserveForEvict` and
  `EvictWorkspace` now hold near-identical reservation blocks. The reviewer's own primary
  suggestion (have `EvictWorkspace` call `ReserveForEvict`) would collapse the two distinct
  error messages ("currently in use" vs "already being evicted") that `ReserveForEvict`
  reduces to `ok==false`. Took the lighter recommended path instead: cross-reference comments
  marking the two blocks as twins to keep in sync.
- **Determinism of the test.** The `prepareAcquireHook` forces the sweep to reserve *before*
  Prepare's `Acquire` (the hook waits on `gate.entered`, which fires only after the sweep
  passed `ReserveForEvict`). So deterministically the sweep wins and evicts while Prepare
  backs out - mirroring the manual-path test where the eviction completes. The plan's initial
  assertion (`res.evicted` empty) was inverted and corrected before implementation.

## Backlog Triage

- No new items. relay-verify returned no high/medium findings; its two low maintainability
  notes were resolved inline with cross-reference comments.

## Process Note

- The planner's draft test asserted the wrong winner (`res.evicted` empty). Caught at the
  plan-review gate by re-deriving the forced interleaving from the hook ordering, before any
  code was written - cheaper than discovering it as a flaky/failing test during implementation.
- relay-verify again earned its keep on a concurrency change: even with no correctness defect,
  it surfaced the genuine drift risk between the two twin reservation blocks, which is exactly
  the maintenance hazard that lets a future edit update one guard and not the other.
