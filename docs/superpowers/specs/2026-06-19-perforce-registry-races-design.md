---
date: 2026-06-19
topic: perforce-registry-races
status: approved
backlog: bug-2026-06-10-perforce-registry-races
---

# Perforce Workspace Registry Races - Design

## Problem

The shared `*perforce.Registry` is accessed concurrently by three goroutine
classes: the sweeper loop (`SweepOnce`), the eviction path, and `Prepare` running
in per-task runner goroutines. Three race-detector-visible defects exist today:

1. **Unlocked iteration in `SweepOnce`** (`sweeper.go:62-67`) ranges over
   `reg.Workspaces` with no lock held, concurrent with `Upsert`'s append and
   `Remove`'s in-place slice compaction under `reg.mu`. A genuine data race.
2. **Unlocked iteration in `shortIDInUse`** (`perforce.go:396-403`) ranges the
   slice with no lock during short-id allocation.
3. **Escaping interior pointers.** `Get`/`GetBySourceKey` return
   `&r.Workspaces[i]`. `Prepare` (`perforce.go:208-229`) then mutates that memory
   (`cur.BaselineHash`, `cur.LastUsedAt`) outside the lock and calls
   `reg.Upsert(*cur)`. Concurrently `Remove` compacts the slice in place, so `cur`
   can point at a stale or wrong slot, and writing back the whole stale entry can
   clobber an `OpenTaskChangelist` a concurrent `AddPendingCL` just appended.

This sidesteps the project invariant **"No interior pointers across locks"**
(shared registries return value copies from getters; mutation happens through
methods that hold the lock).

## Goal

Eliminate every unlocked access to `Registry.Workspaces` and every interior
pointer that escapes `Registry.mu`. Verified under `-race`. No behavior change
beyond internal API signatures.

## Design

### Registry API (`internal/agent/source/perforce/registry.go`)

1. **`Get`/`GetBySourceKey` return a value copy + bool** instead of a pointer:
   ```go
   func (r *Registry) Get(shortID string) (WorkspaceEntry, bool)
   func (r *Registry) GetBySourceKey(sourceKey string) (WorkspaceEntry, bool)
   ```
   Callers can no longer mutate registry memory through the returned value.

2. **`Mutate(shortID string, fn func(*WorkspaceEntry)) error`** - runs `fn`
   against the live slot under `r.mu`; returns an error if no entry matches.
   Replaces the read-modify-`Upsert` dance in `Prepare`. Because it edits the
   existing entry in place rather than overwriting it with a stale copy, it
   cannot clobber an `OpenTaskChangelist` appended concurrently by `AddPendingCL`.

3. **`Snapshot() []WorkspaceEntry`** - returns a copy of the slice under `r.mu`,
   for read-only iteration. Registries hold a handful of workspaces, so the
   per-call copy cost is negligible.

4. **`ShortIDInUse(shortID, sourceKey string) bool`** - a locked method on
   `Registry`, replacing the package-level `shortIDInUse` free function that
   ranged the slice unlocked.

### Call-site updates (`internal/agent/source/perforce/`)

- **`perforce.go:208-229`** (the dangerous read-modify-write):
  - `cur, ok := reg.Get(shortID)` for the `needsSync` decision (snapshot read).
  - After sync, apply the update through:
    ```go
    _ = reg.Mutate(shortID, func(e *WorkspaceEntry) {
        e.BaselineHash = baseline
        e.LastUsedAt = time.Now()
    })
    ```
- **`perforce.go:149`**: `existing, ok := reg.GetBySourceKey(pf.Stream)`; branch on
  `ok` instead of `existing != nil`.
- **`perforce.go:277`** (`EvictWorkspace`): `e, ok := reg.Get(shortID)`; on `!ok`
  return the not-found error; pass the value `e` to `sw.evict`.
- **`perforce.go:389/396`**: `allocateShortID` calls `reg.ShortIDInUse(...)`; delete
  the free `shortIDInUse` function.
- **`perforce.go:77-88`** (`ListInventory`): iterate `reg.Snapshot()`, removing the
  direct `reg.mu.Lock()` reach-in.
- **`sweeper.go:62-67`**: build the candidate list from `reg.Snapshot()`.
- **`sweeper.go:91`**: `if _, ok := reg.Get(w.ShortID); !ok { continue }`.

### Not in scope

- The `Workspace` arbitrator (`ws.Acquire`/holders) - already lock-correct.
- The on-disk `Save` path - unchanged.
- The dispatch-side provider-capability filter - separate backlog item
  (`bug-2026-06-19-dispatch-provider-capability-filter`).

## Testing

- Update existing tests for the new `Get`/`GetBySourceKey` signatures
  (`registry_test.go`, `sweeper_test.go`, `perforce_test.go`,
  `perforce_integration_test.go`).
- Add a Docker-free **concurrency test** in the perforce package: N goroutines
  hammer `Upsert`/`Remove`/`AddPendingCL`/`Mutate` while a sweeper goroutine runs
  `SweepOnce` against a stubbed `Client.DeleteClient` and an in-memory root.
  Assert no panic and (under `-race`) no data race.
- **`make test` does NOT pass `-race`** (it runs `go test ./... -timeout 120s`).
  The race verification must therefore be run explicitly:
  `go test -race ./internal/agent/source/perforce/...`. This command is the
  primary gate for this change and must be part of the verification step.

## Success criteria

1. `go test -race ./internal/agent/source/perforce/...` passes with zero race
   reports.
2. No `reg.Workspaces` iteration or `reg.mu` access remains outside
   `registry.go`.
3. No getter returns a pointer into the backing slice.
4. Existing perforce unit and integration tests pass unchanged in behavior.
