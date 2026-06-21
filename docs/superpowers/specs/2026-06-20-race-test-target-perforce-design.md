# Race + integration-build verification in CI

Date: 2026-06-20
Status: Approved (autonomous brainstorming run)
Scope: Build/CI tooling only - Makefile + GitHub Actions workflow YAML. No Go production or test code changes.

## Problem

Two failure classes escape the default verification flow today:

1. **Data races.** `make test` runs `go test ./... -timeout 120s` with no `-race`.
   `internal/agent/source/perforce/registry_race_test.go` (a plain, non-tagged
   unit test guarding the closed `bug-2026-06-10-perforce-registry-races`) only
   trips a reintroduced race when run under `-race`. A regression in the perforce
   registry, the agent send goroutine, the worker registry, the scheduler, or the
   grace registry would ship green.

2. **Integration-tagged compile breaks.** The unit `make test` does not compile
   `//go:build integration` files. A shared-signature change can leave an
   integration-tagged callsite broken while the default flow stays green. This
   exact break happened closing `bug-2026-06-19-finishregister-gap-connection-epoch-race`:
   the grace `onExpire` signature change broke `cmd/relay-server/startup_reconcile_test.go`
   and was invisible until the Phase 4 integration build.

**Critical context discovered during exploration:** there is NO Go CI workflow at
all. `.github/workflows/` contains only `python.yml` (Python SDK) and
`release.yml` (PyPI publish). No GitHub Actions job runs `go test`, `go vet`, or
`go build` for the Go module. So "wire race into CI" cannot mean "add a `-race`
step to the existing Go job" - there is no Go job. The honest minimum to satisfy
the items' intent ("caught by the default verification flow") is to create a Go CI
workflow and seat both new checks in it.

## Current state (quoted)

Root `Makefile` test target:

```makefile
# Run all tests (no Docker required)
test:
	go test ./... -timeout 120s
```

Current CI test invocation for the Go module: **none.** The only workflows are
`.github/workflows/python.yml` and `.github/workflows/release.yml`; neither runs
any Go command. The Go unit suite and the integration suite run only on
developer machines via `make test` / `make test-integration`.

The race test `internal/agent/source/perforce/registry_race_test.go` has **no
build tag** - it is `package perforce` with a plain `TestRegistry_ConcurrentSweepAndMutate`.
It therefore compiles and runs under any non-integration `go test ./...` invocation,
so a whole-module `-race` unit run includes it without special handling.

## Decisions (autonomous, with rationale)

### 1. Scope of the race target: whole-module unit tests

`make test-race` runs `go test -race ./... -timeout 180s` (whole module, no
integration tag), NOT perforce-only.

- The race test is untagged, so `./...` includes it - the guard is in scope.
- Without `-tags integration`, `./...` pulls in zero testcontainers/Docker code,
  so the run needs no Postgres or p4d and stays fast.
- Whole-module breadth catches the concurrency-heavy, invariant-critical paths
  that perforce-only would miss: the agent's single bounded send goroutine
  (`sendCh`), the server's `workerSender`, `worker.Registry`, `GraceRegistry`,
  and the scheduler. These are exactly the "one bounded sender per stream" and
  "no interior pointers across locks" invariants, and they are where a race is
  most damaging.
- Timeout bumped 120s -> 180s because `-race` instrumentation slows execution
  roughly 2-10x; the headroom avoids flaky timeouts without masking a real hang.

Rejected: perforce-only (`go test -race ./internal/agent/source/perforce/...`).
It would protect one closed bug while leaving the rest of the concurrent surface
unguarded, for negligible time savings on a young, fast suite.

### 2. CI wiring: every push and PR

The race run gates every push and pull_request, not nightly/label-gated.

- The unit suite is small and Docker-free; `-race` on it is cheap enough to run
  inline on every change.
- Highest signal, simplest mental model: green PR means race-clean. A nightly
  cron defers the signal to after merge, which is strictly worse for a regression
  guard. Revisit only if wall-clock becomes a problem (see Non-goals).

### 3. Windows CC requirement: document in a comment, do not hard-code

`make test-race` does NOT set `CC`. CI is Linux and unaffected. A Makefile
comment above the target tells local Windows developers that `-race` needs cgo
with a working gcc and to set `CC=/c/msys64/mingw64/bin/gcc.exe` (MSYS2 mingw64),
because the default Strawberry Perl gcc fails with `exit status 0xc0000139`.
Hard-coding a Windows path would break the Linux CI run; a comment serves the
local case without coupling the target to one machine.

### 4. Fold the integration-build-check item: YES

`idea-2026-06-20-vet-integration-tagged-build.md` (cli-client sibling) is folded
into this iteration. Rationale:

- Same surface: both touch only the Makefile and the same (new) Go CI workflow.
- Same class: both close a default-flow verification gap.
- Both are Docker-free and fast, so both seat naturally as steps in one Go CI job.
- The sibling item's own Notes invite folding ("Could be folded into the same
  CI/Makefile change").
- Folding adds one `make vet-integration` target and one CI step - a small,
  low-risk increment, not a scope explosion.

**Both backlog items close.**

## Design

### Makefile changes

Add two targets and extend `.PHONY`. Place them adjacent to the existing `test`
and `test-integration` targets to match style.

`vet-integration` runs `go vet -tags integration ./...`. `go vet` type-checks
(compiles) every package and its integration-tagged tests without running them,
so it surfaces the shared-signature break class with no Docker dependency.

```makefile
# Run all tests under the race detector (whole module, unit tests only - no
# Docker). Catches concurrency regressions across the agent send goroutine, the
# worker/grace registries, the scheduler, and the perforce registry race guard.
# NOTE (Windows): -race needs cgo with a working gcc. The default Strawberry Perl
# gcc fails (exit status 0xc0000139); use MSYS2 mingw64 via
# CC=/c/msys64/mingw64/bin/gcc.exe (with its bin on PATH). Linux/CI is unaffected.
test-race:
	go test -race ./... -timeout 180s

# Type-check (compile) the integration-tagged code without running it. Catches
# shared-signature breaks in //go:build integration files that the unit `test`
# target never compiles. Needs no Postgres/p4d containers.
vet-integration:
	go vet -tags integration ./...
```

`.PHONY` line gains `test-race` and `vet-integration`.

### CI changes: new workflow `.github/workflows/go-ci.yml`

A new Go CI workflow, since none exists. One job on `ubuntu-latest`, triggered on
push and pull_request. It checks out, sets up Go (version from `go.mod`), and runs
both new verifications. It deliberately does NOT run `make test-integration`
(that needs Docker/Postgres/p4d and belongs to a separate, heavier job out of
scope here).

```yaml
name: go-ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    name: race + integration-build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: Integration-tagged build check
        run: make vet-integration
      - name: Race unit tests
        run: make test-race
```

Notes on the workflow:
- `go-version-file: go.mod` keeps CI pinned to the module's Go version (1.26.2)
  with no second source of truth to drift.
- `vet-integration` runs before `test-race` so a fast compile-class failure
  surfaces before the slower instrumented run.
- No `paths:` filter (unlike `python.yml`): Go changes can live anywhere in the
  module, so the job runs on every push/PR. The python workflow's path filter is
  unaffected and remains independent.
- Driving CI through the same `make` targets developers run locally keeps one
  definition of "what we verify" and avoids invocation drift between local and CI.

## Failure modes and load lens

- **Flaky-timeout under instrumentation:** mitigated by the 180s timeout. If a
  legitimately slow package emerges, raise the timeout rather than dropping
  `-race`.
- **CI wall-clock growth:** as the suite grows, `-race` cost grows with it. The
  fallback (out of scope now) is to move `test-race` to a nightly cron and keep
  plain `make test` on PRs. Documented here as the escape hatch; not implemented.
- **A real race is found on an unrelated PR:** expected and desirable - that is
  the guard working. The race is a correctness defect regardless of which PR
  exposes it.

## Non-goals

- Running the integration suite (`make test-integration`) in CI - separate,
  Docker-backed, heavier; a future item.
- A Windows or macOS Go CI matrix - Linux-only is sufficient for the race and
  build-check signal; the Python workflow already covers cross-OS for its SDK.
- Any change to Go production or test code, including the race test itself.
- Branch-protection / required-status-check configuration (a repo setting, not a
  file in this change).

## Acceptance criteria

1. `make test-race` exists, runs `go test -race ./... -timeout 180s`, and on
   Linux executes `TestRegistry_ConcurrentSweepAndMutate` under the race detector.
2. `make vet-integration` exists and runs `go vet -tags integration ./...`,
   failing if any integration-tagged file does not compile.
3. `.github/workflows/go-ci.yml` exists, triggers on push (main) and
   pull_request, and runs both `make vet-integration` and `make test-race` on
   ubuntu-latest.
4. The Makefile carries the Windows `CC` comment; no Windows path is hard-coded
   in any target.
5. Both backlog items move to `docs/backlog/closed/`:
   `idea-2026-06-19-race-test-target-perforce-package.md` and
   `idea-2026-06-20-vet-integration-tagged-build.md`.

## Closes

- `docs/backlog/idea-2026-06-19-race-test-target-perforce-package.md`
- `docs/backlog/idea-2026-06-20-vet-integration-tagged-build.md`
