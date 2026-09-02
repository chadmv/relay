---
title: Go CI runs only on ubuntu-latest and never cross-compiles, so no Windows-only file is ever built
type: idea
status: open
created: 2026-09-01
priority: medium
source: 2026-09-01 per-task-identity-env-vars slice - a case-folding rule whose Windows arm no automated lane could execute
---

# Go CI runs only on ubuntu-latest and never cross-compiles, so no Windows-only file is ever built

## Summary
Both jobs in `.github/workflows/go-ci.yml` are `runs-on: ubuntu-latest`, and no workflow or Makefile
target sets `GOOS`. Build constraints therefore exclude every `//go:build windows` file from every
automated lane: they are not compiled, not vetted, and not tested. **A syntax error or a type error
in one of them leaves all three checks green.** Relay agents run on Windows render nodes, so this is
the platform half of the product going unbuilt, not an edge case.

## Repro / Symptoms
Measured 2026-09-01. Three production files and two test files carry a `windows` constraint:

- `cmd/relay-agent/free_disk_windows.go`
- `internal/agent/credentials_acl_windows.go` (the 0600-equivalent ACL on the persisted agent token)
- `internal/agent/proctree_windows.go` (job-object process-tree kill)
- `internal/agent/credentials_acl_windows_test.go`
- `internal/agent/runner_cancel_windows_test.go`

`grep -rn GOOS .github/ Makefile` returns nothing, so nothing cross-compiles them either.
`release.yml` is the Python SDK's PyPI publish and builds no Go at all.

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

1. **A cross-compile check in `go-ci.yml`.** `GOOS=windows go build ./...` plus
   `GOOS=windows go vet ./...`, on the existing ubuntu runner, no new job. Seconds of runtime, no
   Docker, no flakes. This closes the "cannot compile" hole completely and catches the type errors
   and unused imports that are the realistic failure. It proves nothing about behaviour.
   **Both commands exit 0 against HEAD (measured 2026-09-01), so adopting this is a pure guard
   addition - there is no backlog of Windows build breakage to clear first.**
2. **A `windows-latest` job running `go test ./...`.** This is what actually executes the Windows
   arms and the two Windows-only test files. Costs real minutes, and note the constraint before
   scoping it: the integration lane needs Docker, which the Windows runner does not usefully
   provide, so this job is the default lane only - `-tags integration` stays Linux. Decide whether
   `-race` is included; cgo on the Windows runner is the part most likely to be awkward.

Do 1 regardless. 2 is the judgement call, and the honest framing is that it buys execution of about
five files plus two `runtime.GOOS` branches, against added CI minutes on every PR.

## Acceptance / Done When
- A deliberate compile error introduced into `internal/agent/proctree_windows.go` fails CI. Today it
  does not - that is the discriminating test, and it is RED against HEAD.
- If the second half is taken: `credentials_acl_windows_test.go` and `runner_cancel_windows_test.go`
  appear as executed tests in a CI log, and a mutation to the Windows arm of
  `isReservedIdentityNameFor` is killed by CI rather than only by a local Windows run.
- `go-ci.yml` states which platform each job covers, so the next person adding a
  platform-conditional branch can see what will and will not run.

## Related
- [.github/workflows/go-ci.yml](.github/workflows/go-ci.yml) - both jobs, both `ubuntu-latest`
- [internal/agent/proctree_windows.go](internal/agent/proctree_windows.go),
  [internal/agent/credentials_acl_windows.go](internal/agent/credentials_acl_windows.go),
  [cmd/relay-agent/free_disk_windows.go](cmd/relay-agent/free_disk_windows.go)
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the tag-shaped sibling; different remedy
- [[feature-2026-08-31-per-task-identity-env-vars]] - the slice that surfaced it, and the `goos` seam
  that works around it locally
- [[feature-2026-09-01-strip-inherited-relay-identity-vars]] - reuses that seam, so it inherits the
  same untested Windows arm
