---
title: source.sync has no entry-count bound, and each #head entry is one ResolveHead round trip
type: idea
status: open
created: 2026-09-04
priority: medium
source: Recommended by the sync-spec exclusion paths design (2026-09-04)
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
