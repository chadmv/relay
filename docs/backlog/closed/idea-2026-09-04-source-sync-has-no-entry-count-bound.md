---
title: source.sync has no entry-count bound, and each #head entry is one ResolveHead round trip
type: idea
status: closed
created: 2026-09-04
priority: medium
source: Recommended by the sync-spec exclusion paths design (2026-09-04)
closed: 2026-09-10
resolution: fixed
---

# source.sync has no entry-count bound, and each #head entry is one ResolveHead round trip

## Summary
`jobspec.validateSourceSpec` bounds nothing about `len(s.Sync)`, and `Prepare` runs one
`ResolveHead` subprocess per `#head` entry inside the task's own prepare phase. This is the
third per-entry subprocess axis on the same spec, beside `unshelves` and the exclusions the
2026-09-04 design adds, and it is the only one of the three with no bound at all.

## Context
Found while writing `docs/superpowers/specs/2026-09-04-sync-spec-exclusion-paths.md`. The
count-bounds slice bounded tasks and commands per job; it did not reach into the source spec,
so a single task can still carry an unbounded sync list.

## Related
- [[bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded]] - the sibling axis
- [[bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers]] - the same shape one level up
- `internal/jobspec/jobspec.go` (`validateSourceSpec`),
  `internal/agent/source/perforce/perforce.go` (`Prepare`, `ResolveHead`)

## Resolution

Fixed in PR #207, squashed as `0922fd12`. `maxSyncEntries = 512` counts includes and exclusions
together, checked immediately after the existing empty-list refusal and before every per-entry rule.
Non-configurable, retroactive over stored `scheduled_jobs.job_spec` with no grandfathering.

**This item's headline claim is false and must not be carried forward.** It says the sync axis "is the
only one of the three with no bound at all". `source.unshelves` has no count bound either -
`validateSourceSpec` checks only that each value is positive - and
[[bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded]] is still open. Two of
three axes were unbounded, and the error ran in the direction that inflated this item's priority.
**That sibling item must be re-read against this slice rather than closed by it**, since this design
corrects its premise.

The item also mis-frames exclusions as a peer axis beside sync. They live inside `s.Sync`, so they are
a bounded sub-population of the field this item is about, and they cost two subprocesses per entry
rather than one.

**The stated cost omits the factor that decided the design.** `selectWorker` calls
`BaselineHashFromAPISpec` inside its worker loop, so all entries - not the `#head` subset - are
re-sorted and re-hashed per candidate worker holding a matching warm workspace, on the coordinator, on
every dispatch attempt. One correction runs in the item's favour: the `ResolveHead` loop runs before
`ws.Acquire`, so it holds no workspace and serialises no peer task, unlike unshelves.

**What the bound buys, measured rather than argued.** It is a per-spec CONCENTRATION control.
`validateSourceSpec` runs per TASK and `maxTasksPerJob` is 5000, so one 1 MiB body still carries about
72 tasks x 512 = 36,864 entries against a pre-change 37,444 - a 1.5% reduction, not the 82x the first
version of the code comment claimed. That paragraph was deleted rather than corrected: it reproduced
the `maxCommandsPerJob` trap (two caps whose product exceeds what the body already permits reduce
nothing) two constants below the comment that states it.

**It does not bound argv bytes, and it does not narrow them.** Measured by actually starting the
process (Windows 11, Go 1.26.2; 16-char client name, 40-char path tails): 442 entries gives a
32,777-character command line and starts OK; 450 gives 33,369 and fails with "filename or extension is
too long"; 512 gives 37,957 and fails. So the cap sits ABOVE the documented 32,767-character
`CreateProcess` limit, which ordinary depot paths reach around 450 entries - inside the cap. It is a
refusal rather than a truncation, with no injection surface. A count bound is the wrong instrument
because path length is unbounded; filed separately.

Review found a gap the implementation could not see: nothing pinned that the bound applies to every
task. Mutating the loop to check only the first task's source left all 23 packages green, because every
source-bearing fixture in the tree is single-task. A two-task case now pins it.
