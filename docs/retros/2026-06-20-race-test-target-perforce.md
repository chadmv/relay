---
date: 2026-06-20
topic: race-test-target-perforce
branch: claude/dazzling-easley-b7e79e
range: Makefile + .github/workflows/go-ci.yml (build/CI tooling only)
pr: 2026-06-20 / race-test-target-perforce
merge: 2026-06-20 / race-test-target-perforce
---

# Session Retro: 2026-06-20 - Race Test Target + First Go CI

**TL;DR:** Build/CI tooling only, no Go source touched. Closed
`idea-2026-06-19-race-test-target-perforce-package` and folded in the adjacent
`idea-2026-06-20-vet-integration-tagged-build` (same Makefile/CI surface, both
Docker-free verification-gap closers). Added Makefile `test-race` and
`vet-integration` targets and created the project's FIRST Go CI workflow
(`.github/workflows/go-ci.yml`). The headline lesson: the backlog item assumed CI
already ran Go tests without `-race`, but there was NO Go CI at all - the right
scope was creating the first Go CI, not tweaking an existing one. Turning on
`-race` for the first time also surfaced a pre-existing Windows-only data race in
`internal/agent`, which was filed (not fixed) to bound the iteration.

## What Was Built

Two surfaces, no Go code:

- **Makefile** (added to `.PHONY`):
  - `vet-integration` - `go vet -tags integration ./...`, type-checks the
    `//go:build integration` files the unit `test` target never compiles, catching
    shared-signature breaks without needing Postgres/p4d containers.
  - `test-race` - `go test -race -timeout 180s` over the module, DESCOPED to
    exclude `relay/internal/agent` so the same command stays green for local
    Windows devs (see the proctree race below). Windows `-race` needs a working
    cgo gcc; the `CC=/c/msys64/mingw64/bin/gcc.exe` (MSYS2 mingw64) requirement is
    documented inline because the default Strawberry Perl gcc fails with exit
    `0xc0000139`.
- **`.github/workflows/go-ci.yml`** - the project's FIRST Go CI workflow
  (previously only `python.yml` / `release.yml` existed). On push-to-main and on
  PR, ubuntu-latest, it runs `make vet-integration` then the FULL
  `go test -race ./... -timeout 180s` (the un-descoped command, not the Makefile
  target). Hardened with `permissions: contents: read`, a
  `cancel-in-progress` concurrency group keyed on `github.ref`, and
  `timeout-minutes: 15`.

The CI/Makefile split is deliberate: CI on Linux runs the complete race set
(`proctree_windows.go` is never compiled there, so the agent package is
race-clean and gets full coverage including the send goroutine and Runner), while
the shared `make test-race` stays descoped so it is green on the Windows dev box.

## Key Decisions

- **"Wire `-race` into CI" meant CREATING the first Go CI.** The backlog item was
  written assuming an existing Go CI job ran tests without `-race`. There was none
  (only Python and release workflows). Rather than treat that as out of scope, the
  correct minimal footprint was a new `go-ci.yml`. A larger change than the item
  implied, but the only way to actually deliver "run race in CI".
- **Fold `vet-integration` into the same iteration.** Both items live on the same
  CI/Makefile surface and both close the same class of gap (Docker-free
  verification that the unit-test path skips). One iteration, one PR, no churn of
  the same files twice.
- **CI runs the full race set; the Makefile target stays descoped.** The first
  whole-module `-race` run surfaced a pre-existing Windows-only race in
  `internal/agent`. Resolved the cross-platform tension by having CI invoke
  `go test -race ./...` directly (Linux-clean, max coverage) while `make test-race`
  excludes `internal/agent` so local Windows runs stay green until the race is
  fixed. The exclusion, its rationale, and the re-include trigger are all
  documented in the Makefile comment.
- **File the discovered race, do not fix it inline.** The proctree race is a
  genuine Windows-only concurrency bug, but fixing it is Go source work outside
  this build-tooling iteration's scope. Filed as
  `bug-2026-06-20-agent-proctree-windows-race` (medium) and bounded the iteration
  there.

## Problems Encountered

- **Assumed baseline was wrong.** The item said "wire the race target into CI" as
  if amending an existing Go CI job. There was no Go CI. Caught by checking
  `.github/workflows/` before scoping rather than after.
- **First `-race` run surfaced a latent race.** `setupProcTree`
  (`internal/agent/proctree_windows.go`) is called from `(*Runner).Run` before
  `cmd.Start()` and spawns a watcher goroutine that reads `cmd` / `cmd.Process`
  concurrently with `Start()` writing `cmd.Process`. The integration tester
  confirmed on Linux that `go test -race ./...` is fully green including
  `internal/agent` (the racy file is `//go:build windows`; `proctree_unix.go` is
  the clean Linux build), so CI is unaffected and only local Windows is exposed.

## Improvement Goals

- **Verify the assumed baseline before scoping a "tweak the CI" item.** This item's
  premise (a Go CI job exists, just lacks `-race`) was false. Before scoping any
  "adjust X" task, confirm X exists and in what form - a one-glance check of
  `.github/workflows/` would have set the correct scope from the start. A specific
  instance of the standing rule that a backlog proposal is not a contract.
- **Treat turning on `-race` for the first time on a package as a discovery tool.**
  The first `-race` run over a previously-uncovered package should be expected to
  surface latent races. Bound the iteration by FILING what it finds, not by trying
  to fix everything inline. That keeps a tooling change from sprawling into source
  work mid-iteration.
- **For cross-platform Makefile targets, let CI carry the platform that is clean.**
  When a shared `make` target must stay green on the dev OS but full coverage is
  only achievable on another OS, have CI invoke the full command directly and keep
  the Makefile target descoped, with the divergence and its re-convergence trigger
  documented at both sites.

## Backlog Triage

- **`bug-2026-06-20-agent-proctree-windows-race` (medium) is already filed by the
  conductor.** It records the exact racing accesses (`runner.go:188` writing
  `cmd.Process` vs `proctree_windows.go:59,96` reading it), the Windows-only scope,
  the detecting tests, a fix proposal (establish a happens-before edge so the
  watcher only touches `cmd.Process` after `Start()` returns), and the regression
  follow-up (re-include `relay/internal/agent` in `make test-race` once fixed). Not
  re-filed; acknowledged here.
- **Both source items are closed by the conductor:**
  `idea-2026-06-19-race-test-target-perforce-package` (primary) and
  `idea-2026-06-20-vet-integration-tagged-build` (folded). Not re-closed.
- **Declined: a tracking note for the `make test-race` agent exclusion.** Already
  fully covered. The exclusion, its rationale, and the "re-include
  `relay/internal/agent` once fixed" trigger are documented in the Makefile comment,
  in the `go-ci.yml` step comment, AND in the filed race item's Proposal. A separate
  tracking item would be pure duplication.
- **Declined: making `make test` itself run `-race`.** Out of scope and a behavioral
  regression for the dev loop - `-race` requires a working cgo gcc (the documented
  MSYS2 mingw64 dance on Windows) that the plain `make test` path deliberately does
  not, so folding `-race` into `test` would break the no-cgo unit-test loop. `-race`
  belongs in the dedicated `test-race` target (local) and CI (full), which is where
  it now lives. Not filed.

**Net: 0 new items filed this cycle (the proctree race was filed by the conductor,
not this retro). 2 source items closed** (`race-test-target-perforce-package`,
`vet-integration-tagged-build`).

## Files Most Touched

- `Makefile` - new `test-race` and `vet-integration` targets, both added to
  `.PHONY`; inline docs for the agent exclusion, the re-include trigger, and the
  Windows `CC=/c/msys64/mingw64/bin/gcc.exe` requirement.
- `.github/workflows/go-ci.yml` - new; the project's first Go CI. Runs
  `make vet-integration` then the full `go test -race ./...` on ubuntu-latest,
  on push-to-main + PR. Hardened with `contents: read`, a cancel-in-progress
  concurrency group, and `timeout-minutes: 15`.
- `docs/backlog/bug-2026-06-20-agent-proctree-windows-race.md` - the discovered
  Windows-only race, filed (not in this retro's scope; acknowledged).
