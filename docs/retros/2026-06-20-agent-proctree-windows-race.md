---
date: 2026-06-20
topic: agent-proctree-windows-race
branch: claude/blissful-brown-c7780a
pr: "2026-06-20 / agent-proctree-windows-race"
merge: "2026-06-20 / agent-proctree-windows-race"
---

# Session Retro: 2026-06-20 - Windows proctree setup data race

**TL;DR:** Closed `bug-2026-06-20-agent-proctree-windows-race`. `setupProcTree` spawned a
goroutine that polled `cmd.Process` concurrently with `cmd.Start()` writing it - a
Windows-only data race in agent task execution (the racing code is in `//go:build windows`
`proctree_windows.go`; Linux compiles the clean `proctree_unix.go`, so Linux CI `-race`
never saw it). Fixed by replacing the poll goroutine with a synchronous post-Start
`assign()` call.

## What Was Built

- `internal/agent/proctree_windows.go` + `proctree_unix.go`: `setupProcTree` now returns
  `(assign func(), cleanup func())` on both platforms. The racing poll goroutine (and its
  `runtime`/`time` imports and leak-guard deadline) is deleted. Windows `assign` runs
  `ensureAssigned()` (Job Object assignment) synchronously; unix `assign` is a no-op
  (process group set via `SysProcAttr` before Start).
- `internal/agent/runner.go`: binds `assignProcTree, cleanupProcTree := setupProcTree(cmd)`
  and calls `assignProcTree()` right after a successful `cmd.Start()`, before `cmd.Wait()` -
  so the `cmd.Process` read happens-after the Start write on the same goroutine.
- Both `TestSetupProcTree_*` tests updated for the two-return signature.
- `Makefile`: re-included `relay/internal/agent` in the `make test-race` target (the
  comment had said to do so once this race was fixed), keeping the Windows-gcc NOTE.

## Key Decisions

- **Synchronous post-Start assign over polling:** the structural happens-before (`assign()`
  is unreachable before `cmd.Start()` returns on the same goroutine) eliminates the race
  without a watcher goroutine, polling, or a deadline leak-guard - simpler and correct by
  construction.
- **Cross-platform signature:** because `setupProcTree` is defined in both build-tagged
  files and called platform-agnostically from `runner.go`, the `(assign, cleanup)` change
  had to land in both at once (the package can't compile mid-signature-change), so it
  shipped as one coherent commit.

## Verification Note

- This race is verifiable only under `go test -race` on **Windows** with the MSYS2 mingw64
  gcc toolchain (`CC=/c/msys64/mingw64/bin/gcc.exe`); the default Strawberry Perl gcc fails,
  and Linux never compiles the racing file. The fix was proven load-bearing-red: the race
  detector reported the DATA RACE before the fix and was clean after. This is the mirror of
  the usual "verify platform-gated tests on a runnable platform" lesson - here the
  platform-gated code is Windows-only, so Linux CI structurally cannot catch it, and the
  re-included `make test-race` target is the local Windows guard going forward.

## Backlog Triage

- None. Code review came back clean (one low note about a Makefile comment referencing the
  `closed/` backlog path, which resolves as part of this same iteration's close). No new
  items filed.
