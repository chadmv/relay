---
title: "Perforce workspace admission is quadratic in sync-path count, under the mutex, with no early exit and a set that never shrinks"
type: bug
status: open
created: 2026-08-29
priority: medium
source: Security lens of the Phase 4 review of the count-bounds slice (2026-08-29)
---

# Perforce workspace admission is quadratic in sync-path count, under the mutex, with no early exit and a set that never shrinks

## Summary

`internal/agent/source/perforce/workspace.go`'s admission check compares every held sync path against
every requested sync path with `PathPrefixOverlap`. Three separate problems compound:

1. **No early exit.** The inner loop sets `disjoint = false` and keeps going. There is no `break`, so
   the full O(n x m) cost is paid even in the common overlapping case, which is the case that decides
   immediately.
2. **It runs under `w.mu`**, so the cost is a stall on all workspace admission on that agent, not
   background work.
3. **The held set never shrinks.** `release` writes every `SyncPath` into `w.syncedPaths`, and
   `modeForEmptyWorkspace` iterates that map against each incoming request. **So admission cost on an
   agent grows with every sync path it has ever seen, for the life of the process** - this is the part
   that makes it more than a per-request cost.

## Measured

Roughly **30,273 sync entries** fit in a 1 MiB body. Two such holders is ~9.16e8 `PathPrefixOverlap`
calls; extrapolated from a measured 8000x8000 square at 422 ms, that is **~6.0 s under the workspace
mutex** per admission attempt.

The 2026-08-29 count bounds do not reach this - they bound `tasks` and `commands`, and `source.sync`
is a third axis.

## Proposal

Sketch, in the order they should be considered:

1. **Add the `break`.** One line, kills the common case for free, no behaviour change - the loop
   already has its answer the moment `disjoint` goes false.
2. **Bound the growth of `syncedPaths`,** or key the overlap check on something that does not require a
   full cross-product. The cumulative growth is the durable defect; the `break` does not touch it.
3. **Bound `len(source.sync)` in `jobspec.Validate`** - the durable per-request fix, but it is a new
   validation rule and therefore retroactive over stored `scheduled_jobs.job_spec` rows on all five
   re-validating paths. See [[reference_tightening_a_validator_is_retroactive]]. Do not adopt without
   pricing that; note that `validateSourceSpec` already REQUIRES at least one sync entry, so the lower
   end of the range exists and only the upper end would be new.

Directions 1 and 2 carry no retroactivity cost and should be exhausted before 3 is considered.

## Acceptance / Done When

- The overlap check exits as soon as it has its answer, proven by a test that would be slow without it
  rather than by reading the code.
- A decision is recorded about `syncedPaths` growth - bounded, or explicitly left alone with the
  reasoning.
- If a count bound lands: boundary accepted, one over refused, retroactivity consequence stated.

## Related

- Source: `internal/agent/source/perforce/workspace.go` (the admission loop, `release`,
  `modeForEmptyWorkspace`), `internal/jobspec/jobspec.go` (`validateSourceSpec`)
- Sibling axis found in the same pass:
  [[bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded]]
- Residual of [[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]]
