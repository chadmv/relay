---
title: Go CI compiles Windows code but runs none of it, so no Windows-only test ever executes
type: idea
status: open
created: 2026-09-01
priority: medium
source: 2026-09-01 per-task-identity-env-vars slice - a case-folding rule whose Windows arm no automated lane could execute
---

# Go CI compiles Windows code but runs none of it, so no Windows-only test ever executes

## Summary
**Half 1 of the Proposal shipped on 2026-09-04**, so the compile half of this item is done and its
remaining scope is EXECUTION only. `.github/workflows/go-ci.yml`'s `test` job now runs
`GOOS=windows go build ./...`, `GOOS=windows go vet ./...` and
`GOOS=windows go vet -tags integration ./...`, and a `//go:build windows` file therefore reaches a
compiler in CI.

What is left: every job in `.github/workflows/go-ci.yml` is `runs-on: ubuntu-latest`, so no
automated lane executes a Windows binary. The `//go:build windows` test files never run, and every
`runtime.GOOS` branch takes its non-Windows arm. Cross-compiling type-checks those files and says
nothing about their behaviour, so a logic error in one still leaves every check green. Relay agents
run on Windows render nodes, so this is the platform half of the product going unrun, not an edge
case.

## Repro / Symptoms
Measured 2026-09-01. Three production files and two test files carry a `windows` constraint:

- `cmd/relay-agent/free_disk_windows.go`
- `internal/agent/credentials_acl_windows.go` (the 0600-equivalent ACL on the persisted agent token)
- `internal/agent/proctree_windows.go` (job-object process-tree kill)
- `internal/agent/credentials_acl_windows_test.go`
- `internal/agent/runner_cancel_windows_test.go`

`grep -rn GOOS .github/ Makefile` returned nothing on 2026-09-01, so nothing cross-compiled them
either. That measurement no longer holds: as of 2026-09-04 the same grep hits the three
`GOOS=windows` commands half 1 added, and those five files are compiled and vetted. They are still
never executed. `release.yml` is the Python SDK's PyPI publish and builds no Go at all.

Four non-test sites branch on `runtime.GOOS`, two of them on `"windows"`:
`cmd/relay-agent/main.go`, `internal/cli/config.go` (the `%APPDATA%\relay\config.json` path),
`internal/agent/capabilities.go` (reports it, does not branch), and `internal/agent/runner.go`
(the reserved-name case fold). CI executes the non-Windows arm of each.

**The gap runs both ways, which is why neither side notices.** CI is Linux, so it skips the
`windows` files; the primary development machine is Windows, where `go test` silently skips the
`!windows` files. Each side is blind to exactly what the other covers, and both report green.

## Context
Surfaced by the per-task identity env vars slice
([[feature-2026-08-31-per-task-identity-env-vars]], closed). `isReservedIdentityName` folds case
only on Windows, matching `os/exec`'s `dedupEnvCase`. Measured both ways in that slice: dropping the
Windows clause is RED on Windows and green on Linux; dropping the platform check is green on Windows
and RED on Linux. So half the rule was guarded by a developer happening to run the suite on Windows.

That slice worked around it by extracting `isReservedIdentityNameFor(goos, k)` and table-testing
both `"windows"` and `"linux"` explicitly, so both arms die on every platform. **That is the right
local fix and it does not generalise**: a seam can parameterise a `runtime.GOOS` comparison, but it
cannot parameterise a `//go:build windows` file. `proctree_windows.go` calls the Windows job-object
API; there is nothing to inject.

Not hypothetical for this project: `bug-2026-06-20-agent-proctree-windows-race` (closed) was a
Windows-only race in exactly this code, and `go-ci.yml`'s own comment still carries its history.

Distinct from [[idea-2026-08-23-integration-only-guards-ci-never-runs]], and the distinction decides
the remedy. That item is about a build TAG the CI command does not pass, closable by changing the
command. This is a PLATFORM the runner does not have. No tag, flag or seam closes it - only a
cross-compile step or a second runner does.

## Proposal
Two independent halves; the first is cheap enough that it should not wait for a decision on the
second.

1. **SHIPPED 2026-09-04. A cross-compile check in `go-ci.yml`.** `GOOS=windows go build ./...`,
   `GOOS=windows go vet ./...` and `GOOS=windows go vet -tags integration ./...`, on the existing
   ubuntu runner, no new job. Seconds of runtime, no Docker, no flakes. It catches the type errors
   and unused imports that are the realistic failure, and proves nothing about behaviour. Each
   command exited 0 against the tree it landed on (2026-09-01 for the first two, 2026-09-04 for the
   tagged one), so it was a pure guard addition with no backlog of Windows build breakage to clear
   first. `go build` does not read `_test.go` files, so the vet lines are what reach the
   windows-constrained test files, and the tagged line is what reaches a
   `//go:build windows && integration` file. `TestWindowsCrossCompileStepCommandsArePresent` in
   `internal/agent` pins that all three commands are present, because deleting the step is otherwise
   green in every lane.
2. **A `windows-latest` job running `go test ./...`.** This is what actually executes the Windows
   arms and the two Windows-only test files. Costs real minutes, and note the constraint before
   scoping it: the integration lane needs Docker, which the Windows runner does not usefully
   provide, so this job is the default lane only - `-tags integration` stays Linux. Decide whether
   `-race` is included; cgo on the Windows runner is the part most likely to be awkward.

Do 1 regardless. 2 is the judgement call, and the honest framing is that it buys execution of about
five files plus two `runtime.GOOS` branches, against added CI minutes on every PR.

## Acceptance / Done When
- MET 2026-09-04 by half 1: a deliberate compile error introduced into
  `internal/agent/proctree_windows.go` fails CI. Measured both ways - green against the linux checks,
  which build constraints keep from seeing the file, and RED against the cross-compile step.
- If the second half is taken: `credentials_acl_windows_test.go` and `runner_cancel_windows_test.go`
  appear as executed tests in a CI log, and a mutation to the Windows arm of
  `isReservedIdentityNameFor` is killed by CI rather than only by a local Windows run.
- MET 2026-09-04 by half 1: each job in `go-ci.yml` carries a `# Platform:` line, so the next
  person adding a platform-conditional branch can see what will and will not run.

## Related
- [.github/workflows/go-ci.yml](.github/workflows/go-ci.yml) - every job `ubuntu-latest`
- [internal/agent/proctree_windows.go](internal/agent/proctree_windows.go),
  [internal/agent/credentials_acl_windows.go](internal/agent/credentials_acl_windows.go),
  [cmd/relay-agent/free_disk_windows.go](cmd/relay-agent/free_disk_windows.go)
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the tag-shaped sibling; different remedy
- [[feature-2026-08-31-per-task-identity-env-vars]] - the slice that surfaced it, and the `goos` seam
  that works around it locally
- [[feature-2026-09-01-strip-inherited-relay-identity-vars]] - reuses that seam, so it inherits the
  same untested Windows arm
