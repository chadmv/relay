---
title: Perforce workspace registry races - unlocked iteration and escaping interior pointers
type: bug
status: open
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

# Perforce workspace registry races - unlocked iteration and escaping interior pointers

## Summary
Three races on the shared `*Registry` (sweeper goroutine, eviction goroutine, Prepare in runner goroutines): (1) `SweepOnce` ranges over `reg.Workspaces` with no lock, concurrent with `Upsert`'s append and `Remove`'s in-place compaction under `reg.mu` - a race-detector-visible data race; (2) `shortIDInUse` ranges unlocked; (3) `Get`/`GetBySourceKey` return `&r.Workspaces[i]` and Prepare mutates that memory outside the lock, while `Remove` compacts the slice in place, so `cur` can point at a stale or wrong slot, and `reg.Upsert(*cur)` can clobber a concurrently-added pending CL.

## Proposal
Never let interior pointers escape:
- `Get`/`GetBySourceKey` return a `WorkspaceEntry` value copy plus a bool.
- Add `Mutate(shortID string, fn func(*WorkspaceEntry)) error` that runs under `r.mu`.
- Give the sweeper a locked `Snapshot() []WorkspaceEntry` accessor.
- Run the perforce package tests with `-race` covering concurrent sweep + prepare.

## Related
- `internal/agent/source/perforce/sweeper.go:62-67`
- `internal/agent/source/perforce/registry.go:78, 106, 113-119`
- `internal/agent/source/perforce/perforce.go:208-229, 396-403`

## Closed 2026-06-19
Fixed: `Get`/`GetBySourceKey` now return `WorkspaceEntry` value copies + bool;
added `Mutate`/`Snapshot`/`ShortIDInUse` locked methods. All consumers (Prepare,
EvictWorkspace, ListInventory, allocateShortID, sweeper) route through the locked
API. The dangerous `cur := reg.Get(...)` read-modify-`Upsert(*cur)` block in
Prepare became an in-place `Mutate`, which also can no longer clobber an
`OpenTaskChangelist` appended concurrently during the unlocked sync. No unlocked
`reg.Workspaces` iteration or escaping interior pointer remains outside
`registry.go`. Verified clean under `go test -race ./internal/agent/source/perforce/...`
via a new Docker-free concurrency test (`registry_race_test.go`) - confirmed
load-bearing (the pre-fix pattern trips `-race`).

Spec: `docs/superpowers/specs/2026-06-19-perforce-registry-races-design.md`.
Plan: `docs/superpowers/plans/2026-06-19-perforce-registry-races.md`.
