---
date: 2026-06-21
topic: evictworkspace-state-leak-toctou
branch: claude/blissful-brown-c7780a
pr: "2026-06-21 / evictworkspace-state-leak-toctou"
merge: "2026-06-21 / evictworkspace-state-leak-toctou"
---

# Session Retro: 2026-06-21 - EvictWorkspace state leak and Prepare TOCTOU

**TL;DR:** Closed `bug-2026-06-20-evictworkspace-state-leak-toctou`. `Provider.EvictWorkspace`
(the manual single-workspace eviction path) had two defects: its ad-hoc Sweeper lacked
`OnEvictedCB`, so per-task state survived a manual eviction; and its locked-check/evict was
not atomic vs a concurrent `Prepare`. Both are now fixed on the manual path.

## What Was Built

- `internal/agent/source/perforce/perforce.go`:
  - Wired `OnEvictedCB: p.InvalidateWorkspace` into the ad-hoc Sweeper (matching the
    background sweeper) so `syncedPaths` / the in-memory `*Workspace` is invalidated.
  - Added a `p.evicting map[string]bool` reservation set. `EvictWorkspace`, under `p.mu`,
    does an inline holder check and reserves the short ID, runs the slow `evict`
    (DeleteClient + RemoveAll) lock-free, and clears the reservation in a defer. `Prepare`
    refuses a reserved short ID at get-or-create AND re-checks `p.evicting` under `p.mu`
    after `ws.Acquire`, backing out (releasing the handle) if a reservation landed in the
    pre-check->Acquire gap.
  - Dropped the dead `ListLocked` field from the ad-hoc Sweeper literal and corrected the
    doc comment to describe the real guarantee.
- Deterministic, channel-synchronized fake-runner tests, including a recheck test that gates
  an eviction reservation while `Prepare` acquires, proving the losing Prepare backs out
  without syncing.

## Key Decisions

- **Atomic claim, not lock-across-evict:** holding `p.mu` across the slow p4 `client -d` /
  `RemoveAll` would stall every concurrent `Prepare`. The reservation set bridges the gap
  instead; the slow evict runs lock-free. Only a bool crosses `p.mu` (no-interior-pointers),
  and `p.mu`->`ws.mu` ordering is preserved.
- **Post-Acquire re-check** is the load-bearing half: the first implementation reserved
  under `p.mu` and had `Prepare` pre-check, but a Prepare already past its pre-check (but
  pre-`Acquire`) could still race in. The fix serializes both decisions on `p.mu` -
  EvictWorkspace's holder-check+reserve is one critical section, and Prepare's `Acquire`
  happens-before its `p.mu` re-check, so exactly one wins.
- **Split the background sweeper:** the same TOCTOU class exists on `SweepOnce`->`evict`
  (the dominant eviction trigger), which never sets `p.evicting`. That is a separate,
  pre-existing defect; scoping it here would have bloated the change, so it was filed as
  [[bug-2026-06-21-sweeper-prepare-toctou]].

## Backlog Triage

- Filed [[bug-2026-06-21-sweeper-prepare-toctou]] (bug, medium) for the background-sweeper
  TOCTOU surfaced by verification.

## Process Note

- relay-verify earned its keep on this concurrency fix: it caught that the
  reservation-only implementation left a residual Prepare window (and that the doc comment
  overstated atomicity), and separately surfaced the background-sweeper variant. The
  follow-up post-Acquire re-check was proven load-bearing with a channel-synchronized test
  that reaches `p4 sync` against a being-deleted workspace without the fix - again the
  discipline that a guard must fail when the fix is absent.
