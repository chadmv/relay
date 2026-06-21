---
title: Windows proctree setup races with cmd.Start() on the agent Runner
type: bug
status: closed
created: 2026-06-20
closed: 2026-06-20
resolution: fixed
priority: medium
source: surfaced by the first `-race` run while closing race-test-target-perforce-package
---

# Windows proctree setup races with cmd.Start() on the agent Runner

## Summary
The first whole-module `go test -race ./...` run (added in
`race-test-target-perforce-package`) surfaced a pre-existing data race in
`relay/internal/agent`, in the Windows process-tree setup. `setupProcTree`
(`internal/agent/proctree_windows.go`) is called from `(*Runner).Run`
(`internal/agent/runner.go:174`) BEFORE `cmd.Start()` (`runner.go:188`), and it
spawns a watcher goroutine that reads `cmd` / `cmd.Process` concurrently with
`cmd.Start()` writing `cmd.Process`. The watcher goroutine races the `Start()`
call on the same `*exec.Cmd`.

The race is **Windows-only**: the racing code is in a `//go:build windows` file
(`proctree_windows.go`). On Linux, `proctree_unix.go` is compiled instead and is
race-clean (the integration tester confirmed `go test -race ./...` is fully green
on Linux, including `internal/agent`). So CI (Linux) is unaffected, but a Windows
build has a genuine data race in agent task execution.

## Detection
- **Package:** `relay/internal/agent`
- **Tests that detected it:** `TestRunner_MultiStepAllSucceed`,
  `TestRunner_MultiStepFailFastSkipsRest`,
  `TestRunner_SingleCommandReportsStepOneOfOne`, `TestRunner_PrepareEmitsPreparing`,
  `TestRunner_done`, `TestRunner_timeout`,
  `TestSetupProcTree_Windows_AssignsJobObject`,
  `TestAgentRunnerSurvivesConnectionContextCancellation`
- **Racing accesses on `*exec.Cmd` / `cmd.Process`:**
  - Write: `os/exec.(*Cmd).Start()` setting `cmd.Process`, from
    `(*Runner).Run` at `internal/agent/runner.go:188`.
  - Read: `setupProcTree.func3` at `internal/agent/proctree_windows.go:96` and
    `setupProcTree.func1` at `internal/agent/proctree_windows.go:59`.

## Proposal
Establish a happens-before edge so the proctree watcher never touches `cmd` /
`cmd.Process` until after `cmd.Start()` has returned. Likely options:
- Start the proctree watcher AFTER `cmd.Start()` (move the `setupProcTree` call,
  or have it only capture the PID/handle once `Start` has populated `cmd.Process`).
- Or pass the watcher the concrete `cmd.Process`/PID after `Start()` rather than a
  reference to the `*exec.Cmd` it reads concurrently.
Add a regression assertion: this is now caught by `go test -race` on Windows, so
once fixed, re-include `relay/internal/agent` in the `make test-race` target (the
target currently excludes it; see the Makefile comment) so the local Windows race
run covers the agent package again.

## Related
- `internal/agent/proctree_windows.go` (`setupProcTree`)
- `internal/agent/runner.go:174,188` (`(*Runner).Run`)
- `Makefile` (`test-race` target, currently excludes `relay/internal/agent`)
- [[idea-2026-06-19-race-test-target-perforce-package]] (the CI race target that surfaced this)

## Resolution
fixed - `setupProcTree` now returns `(assign, cleanup)` on both platforms; `(*Runner).Run`
calls `assign()` synchronously immediately after a successful `cmd.Start()`, so the
`cmd.Process` read happens-after the Start write on the same goroutine. The racing poll
goroutine (and its `runtime`/`time` imports and leak-guard deadline) is deleted from
`proctree_windows.go`; on Windows `assign` runs `ensureAssigned()` (Job Object assignment)
synchronously, on unix it is a no-op (the process group is set via `SysProcAttr` before
Start). `cmd.Cancel`, kill-on-job-close semantics, and `cleanup` are otherwise unchanged.
With the race gone, `relay/internal/agent` was re-included in the `make test-race` target
(Windows-gcc NOTE kept). Verified with load-bearing-red under `go test -race` on Windows
(MSYS2 mingw64 gcc): the detector reported the race before the fix and is clean after;
Linux `-race` stays green (unix path unchanged). Plan:
`docs/superpowers/plans/2026-06-20-agent-proctree-windows-race.md`.
