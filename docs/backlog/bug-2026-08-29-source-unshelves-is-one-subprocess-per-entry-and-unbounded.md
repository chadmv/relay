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

## Amendment 2026-09-10

**A fourth count bound landed, so the Proposal's "if a fourth count bound is ever added" is now
"a fifth".** `maxSyncEntries = 512` shipped in [#207](https://github.com/chadmv/relay/pull/207),
closing [[idea-2026-09-04-source-sync-has-no-entry-count-bound]] - the other per-entry axis on this
same `source` spec. That slice's retroactivity argument is the precedent this item asked for: no
grandfathering, a stored spec over the bound stops firing and records why in
`scheduled_jobs.last_error`, and the bound is non-configurable for the reason `maxRetries` states.

**This item is NOT closed by that slice, and the sync item's own framing is why the confusion is
possible.** That item claimed the sync axis was "the only one of the three with no bound at all",
which was false precisely because this item exists. The claim is corrected in the closed item's
resolution note. Read this one as *sharper* now, not resolved: it is the remaining unbounded
per-entry axis on the source spec, and the sync slice did not touch `Unshelves`.

**One measurement from that slice transfers and is worth carrying.** The sync cap does not reduce the
per-REQUEST aggregate, because `validateSourceSpec` runs per TASK and `maxTasksPerJob` is 5000 - one
1 MiB body still carries about 72 tasks at the cap. Any bound proposed here inherits that property,
so price it as a per-spec concentration control and do not claim an aggregate reduction it cannot
deliver. The honest comparison on byte density stands: two bytes per unshelve entry against 25 for
the cheapest legal sync entry.

**Check the batching question before the bound, as the Proposal already says.** The sync slice is
evidence for that ordering rather than against it: its cap sits ABOVE the platform command-line limit
that the one un-batched `p4 sync` invocation reaches, which is now its own item
([[bug-2026-09-10-syncstream-argv-length-is-unbounded]]). A count bound that leaves the per-entry
subprocess shape alone buys less than it appears to.
