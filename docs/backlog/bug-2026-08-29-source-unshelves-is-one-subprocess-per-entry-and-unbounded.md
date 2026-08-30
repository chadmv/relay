---
title: "`source.unshelves` is one `p4 unshelve` subprocess per entry, unbounded, at 4.5x the byte density of the axis just capped"
type: bug
status: open
created: 2026-08-29
priority: medium
source: Security lens of the Phase 4 review of the count-bounds slice (2026-08-29)
---

# `source.unshelves` is one `p4 unshelve` subprocess per entry, unbounded, at 4.5x the byte density of the axis just capped

## Summary

`internal/agent/source/perforce/perforce.go` loops over `pf.Unshelves` issuing one `Client.Unshelve`
subprocess per entry. `jobspec.Validate`'s `validateSourceSpec` checks only that each changelist is
positive; nothing bounds the count.

**It is denser than the axis the 2026-08-29 slice bounded.** An unshelve entry is `1,` - two bytes -
so a 1 MiB body expresses roughly **524,000 entries**, against roughly 116,000 for the cheapest
runnable command. The count bounds landed on `tasks` and `commands`; this runs in the
workspace-PREPARE phase, before the command loop, so none of the three reaches it.

## Honest caveats, which are why this is medium and not high

Both matter to the severity and neither is a reason to close it:

- **The loop returns on the first failure.** The attacker needs changelists that actually unshelve,
  which means real shelved changelists on the depot. This is not a free-cost axis the way a body full
  of `["true"]` is - it is closer to the "must be runnable" property the command-count analysis
  established for `sendStepMarker`.
- **It requires a Perforce-capable agent** with a valid ticket.

The availability half is independent of both: `len(req.Unshelves) > 0` forces `ModeExclusive` in
`workspace.go`, so the whole workspace is serialised for the duration.

## Proposal

Sketch only. If a fourth count bound is ever added to `jobspec.Validate`, **this is the axis with the
best byte-per-spawn ratio and should be first in line.** Price it with the same retroactivity argument
the count-bounds slice already wrote: `Validate` runs on stored `scheduled_jobs.job_spec` rows on five
paths, so a bound below an existing stored spec's count stops that schedule firing. See
[[reference_tightening_a_validator_is_retroactive]].

Before adopting a bound, check whether `p4 unshelve` accepts multiple changelists in one invocation -
a batching fix carries no retroactivity cost and is preferable for the same reason it is preferable in
[[bug-2026-08-29-createjobfromspec-inserts-one-dependency-edge-per-round-trip]].

## Acceptance / Done When

- A spec with a large `unshelves` list either prepares its workspace in a bounded number of
  subprocesses, or is refused at submission with an error naming the limit.
- If a bound lands: a spec at the boundary is accepted, one over is refused, and the PR states the
  retroactivity consequence for stored schedules.

## Related

- Source: `internal/agent/source/perforce/perforce.go` (the `Unshelves` loop),
  `internal/agent/source/perforce/workspace.go` (`ModeExclusive`), `internal/jobspec/jobspec.go`
  (`validateSourceSpec`)
- The slice that bounded `tasks` and `commands` and measured this as residual:
  [[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]]
- Sibling axis found in the same pass:
  [[bug-2026-08-29-perforce-workspace-admission-is-quadratic-under-the-mutex]]
