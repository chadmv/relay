---
date: 2026-06-19
topic: perforce-registry-races
branch: claude/kind-varahamihira-4f434a
range: ab6fed6..0a0db48
---

# Session Retro: 2026-06-19 - Perforce Workspace Registry Races

**TL;DR:** Closed `bug-2026-06-10-perforce-registry-races` by making the perforce
`Registry` fully lock-safe - getters return value copies + bool, mutation goes
through a new `Mutate`/`Snapshot`/`ShortIDInUse` API, and every consumer (Prepare,
EvictWorkspace, ListInventory, allocateShortID, sweeper) was routed through it;
verified with a Docker-free `-race` regression test that provably tripped on the
pre-fix code.

## What Was Built

Three data races on the shared `*perforce.Registry` are gone, satisfying the
"No interior pointers across locks" invariant:

- **Registry API** (`registry.go`) - `Get`/`GetBySourceKey` now return
  `(WorkspaceEntry, bool)` value copies instead of `*WorkspaceEntry`. Added
  `Mutate(shortID, func(*WorkspaceEntry)) error` (edit the live slot under the
  lock), `Snapshot() []WorkspaceEntry` (locked slice copy for read-only
  iteration), and `ShortIDInUse(shortID, sourceKey) bool` (replacing the
  package-level free function that ranged the slice unlocked).
- **Consumers routed through the locked API** (`perforce.go`, `sweeper.go`) -
  the dangerous `cur := reg.Get(...)` read-modify-`Upsert(*cur)` block in
  `Prepare` became an in-place `Mutate`, which as a bonus can no longer clobber
  an `OpenTaskChangelist` appended concurrently during the unlocked sync.
  `ListInventory` dropped its direct `reg.mu` reach-in for `Snapshot()`; the
  sweeper builds candidates from `Snapshot()` and checks existence via the
  bool-returning `Get`.
- **Race regression test** (`registry_race_test.go`) - 8 writers x 200 iterations
  hammering `Upsert`/`Mutate`/`AddPendingCL`/`RemovePendingCL`/`Get`/`Snapshot`
  while a sweeper loops `SweepOnce` over the same `*Registry`. Docker-free (fake
  P4 client, eviction kept off the hot path). Confirmed load-bearing: the
  reviewer reproduced the pre-fix unlocked-scan pattern and it trips
  `WARNING: DATA RACE` immediately.

## Key Decisions

- **Full invariant sweep over the literal proposal.** The backlog item named
  three races; the user chose to also route `shortIDInUse` and `ListInventory`
  through the locked API so zero unlocked iteration and zero escaping interior
  pointers remain anywhere in the package - a slightly larger diff that fully
  closes the invariant rather than leaving a safe-but-inconsistent reach-in.
- **`Mutate` in place, not read-copy-`Upsert`.** Editing the live entry under
  the lock is strictly safer than overwriting the whole struct with a stale copy
  read before a slow unlocked `SyncStream` - it eliminates the CL-clobber window
  for free. Shallow copy of `OpenTaskChangelists` was kept (YAGNI): every
  `Get`/`Snapshot` consumer is read-only on that field; the only writers go
  through the locked `AddPendingCL`/`RemovePendingCL`.
- **Grouped the coupled refactor into two implementer dispatches, not six.** The
  plan's Tasks 1-4 are one compile unit (Task 1 breaks call sites that 2-4 fix),
  so a single backend-engineer dispatch produced a compiling, green tree; Task 5
  (the race test) was a second dispatch. Each got its own `relay-code-reviewer`
  pass. Adapting the per-task subagent cadence to the real build boundaries
  avoided non-compiling intermediate handoffs.

## Problems Encountered

- **The `-race` gate did not run on this box.** The gcc on PATH (Strawberry Perl
  8.3.0) is incompatible with Go's Windows race runtime - `go test -race` fails
  with `exit status 0xc0000139` on *every* package, including the untouched
  `internal/tokenhash`. The reviewer flagged it; I verified it was environmental,
  then found a working MSYS2 mingw64 gcc 13.2.0 and ran the gate with
  `export PATH="/c/msys64/mingw64/bin:$PATH"; export CC=/c/msys64/mingw64/bin/gcc.exe`.
  Under that toolchain the whole package passes `-race` clean.
- **My plan's race-test body had a teardown deadlock.** It put the sweeper
  goroutine in the same `WaitGroup` as the writers and called `wg.Wait()` before
  `close(stop)`, so the first run hung to the 600s panic timeout. The implementer
  diagnosed it from the stack and split into two WaitGroups
  (`writers.Wait()` -> `close(stop)` -> `sweeper.Wait()`). Goroutine-teardown
  code in a plan needs the same wait/close-ordering scrutiny as production code.

## Open Questions

- See [`idea-2026-06-19-race-test-target-perforce-package`](../backlog/idea-2026-06-19-race-test-target-perforce-package.md) - `make test` and CI do not run `-race`, so the new regression test only guards under an explicit `-race` invocation; add a dedicated race target.

## Improvement Goals

- **Verify the `-race` toolchain before treating `-race` as a verification gate
  (Windows)** (promoted to [[reference-race-detector-toolchain]]). New this
  session. The default Strawberry Perl gcc breaks the race detector; the working
  toolchain is MSYS2 mingw64 (`CC=/c/msys64/mingw64/bin/gcc.exe`, its bin on
  PATH). A future session that plans a `-race` gate should confirm the toolchain
  up front instead of discovering mid-implementation that it can't run.
- **Give goroutine/teardown code in a plan the same scrutiny as production code.**
  New this session. The plan's `WaitGroup`/`close(stop)` ordering deadlocked;
  tracing the wait-before-close at plan-write time would have caught it.
- **Match commit here-string syntax to the tool's shell** (carried, already
  promoted to [[feedback-commit-heredoc-shell]]). Applied again - bash
  heredocs/quoting throughout.
- **Treat a backlog proposal as a starting point, not a contract** (carried,
  already promoted to [[feedback-backlog-proposal-not-contract]]). Applied:
  verified the proposal against code, then deliberately widened it to the full
  invariant sweep with the user's sign-off.

## Files Most Touched

- `internal/agent/source/perforce/registry.go` - getters return copies; new
  `Mutate`/`Snapshot`/`ShortIDInUse` locked methods.
- `internal/agent/source/perforce/perforce.go` - Prepare/EvictWorkspace/
  ListInventory/allocateShortID routed through the locked API; free
  `shortIDInUse` deleted.
- `internal/agent/source/perforce/registry_race_test.go` - new concurrency
  regression test (the `-race` gate).
- `internal/agent/source/perforce/sweeper.go` - candidate build via `Snapshot()`;
  existence check via bool `Get`.
- `internal/agent/source/perforce/registry_test.go` - tests for the new getter
  signatures and `Mutate`/`Snapshot`/`ShortIDInUse`.
- `internal/agent/source/perforce/{sweeper,perforce,perforce_integration}_test.go`
  - call-site signature updates for value+bool getters.
- `docs/superpowers/specs/2026-06-19-perforce-registry-races-design.md` /
  `docs/superpowers/plans/2026-06-19-perforce-registry-races.md` - spec + plan.
