# Race + Integration-Build Verification in CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `make test-race` and `make vet-integration` Makefile targets and a new `go-ci.yml` GitHub Actions workflow so data races and integration-tagged compile breaks are caught by the default verification flow.

**Architecture:** Build/CI tooling only. Two new Makefile targets drive a Docker-free race-detector unit run (`go test -race ./...`) and an integration-tagged compile check (`go vet -tags integration ./...`). A new Linux-only GitHub Actions workflow runs both targets on every push to main and every pull request, reusing the same `make` targets developers run locally. No Go production or test code changes.

**Tech Stack:** GNU Make (TAB-indented recipes), GitHub Actions (`actions/checkout@v4`, `actions/setup-go@v5`), Go 1.26.2 race detector (cgo + gcc).

---

## Slice independence

This is a **single sequential tooling slice**. There is no frontend code, no backend code, and no Go production or test code. There is no parallelism to declare for Phase 3: the Makefile targets must exist and be verified locally before the workflow that invokes them is meaningful, so the tasks run in order. The conductor should treat this as one linear slice.

## Grounding facts (confirmed by reading the repo)

- **Go version:** `go.mod` line 3 is `go 1.26.2`. The workflow uses `go-version-file: go.mod`, so CI inherits `1.26.2` with no second source of truth.
- **Makefile style:** `.PHONY` is line 1. Recipe lines are indented with a literal **TAB**, not spaces. The existing `test` target (lines 21-23) and `test-integration` target (lines 25-27) are the placement anchor; the two new targets go immediately after `test-integration` (after line 27).
- **Existing workflows:** only `.github/workflows/python.yml` and `.github/workflows/release.yml` exist. **No Go CI workflow exists.** `python.yml` pins `actions/checkout@v4` and `actions/setup-python@v5` with `cache:`; the new Go workflow mirrors that versioning style with `actions/setup-go@v5`.
- **Race test has no build tag:** `internal/agent/source/perforce/registry_race_test.go` line 1 is `package perforce` (no `//go:build` line above it). So a plain `go test -race ./...` (no `-tags integration`) compiles and runs `TestRegistry_ConcurrentSweepAndMutate` under the detector, and pulls in zero testcontainers/Docker code. **`go test -race ./...` does NOT require Docker** and will not hang waiting for testcontainers.

## Critical risks this plan addresses

1. **A CI change cannot be verified by running GitHub Actions locally.** Verification is done by running the exact `make` targets the workflow invokes, on the agent's Linux env (which has Docker + cgo + gcc), plus a structural review of the YAML against the existing `python.yml`. See Task 3 verify steps.
2. **Whole-module `-race` runs every package's unit tests under the detector for the first time.** It may surface a *pre-existing* race never before run under `-race`. This change adds zero Go code, so any race it surfaces is pre-existing. Task 2 carries an explicit **contingency branch**: descope `test-race` to a known-clean package set so CI is green on arrival, and file the surfaced race(s) as new backlog item(s). **The new CI must be GREEN when it lands - do not ship a red CI.**
3. **`go vet -tags integration ./...` may surface a current integration build break.** That is a real pre-existing bug. Task 1 carries the same contingency: capture it, fix only if trivial and in-scope-adjacent, otherwise file it; CI must be green on landing.

---

## Task 1: Add the `vet-integration` Makefile target

**Files:**
- Modify: `Makefile:1` (`.PHONY` line) and after `Makefile:27` (insert new target).

- [ ] **Step 1: Add `vet-integration` to `.PHONY` and insert the target**

Edit line 1 of `Makefile` to add `vet-integration`:

```makefile
.PHONY: build test test-integration vet-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev
```

Insert this block immediately after the `test-integration` target (after line 27, before the `generate` target). **The recipe line must be indented with a literal TAB, not spaces:**

```makefile
# Type-check (compile) the integration-tagged code without running it. Catches
# shared-signature breaks in //go:build integration files that the unit `test`
# target never compiles. Needs no Postgres/p4d containers.
vet-integration:
	go vet -tags integration ./...
```

- [ ] **Step 2: Run the target to verify it passes (Linux agent env)**

Run: `make vet-integration`
Expected: exit 0, no output other than `go vet` progress. This compiles every package plus all `//go:build integration` files (e.g. `cmd/relay-server/startup_reconcile_test.go`) without running them or needing Docker.

> **CONTINGENCY BRANCH (Task 1).** If `make vet-integration` FAILS, the failure is a *pre-existing* integration build break (this change adds no Go code):
> 1. Capture the full `go vet` error output verbatim.
> 2. If the break is trivial and in-scope-adjacent (e.g. a one-line stale callsite signature like the documented grace `onExpire` case), the conductor may authorize fixing it in a separate, clearly-scoped commit so the CI lands green. Do not silently bundle a behavioral change.
> 3. If it is non-trivial, do NOT fix it here. File a new backlog item under `docs/backlog/` describing the break (file, signature, capture), and the CI must not run `vet-integration` against a known-broken tree - block the merge until the break is resolved separately.
> The target itself ships only once `make vet-integration` is green. **Do not ship a red CI.**

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: add vet-integration target (compile integration-tagged code)"
```

---

## Task 2: Add the `test-race` Makefile target

**Files:**
- Modify: `Makefile:1` (`.PHONY` line, already edited in Task 1) and after the `vet-integration` target inserted in Task 1.

- [ ] **Step 1: Add `test-race` to `.PHONY` and insert the target**

Edit line 1 of `Makefile` so `.PHONY` now also lists `test-race` (it already lists `vet-integration` from Task 1):

```makefile
.PHONY: build test test-integration test-race vet-integration generate clean python-test python-test-integration python-lint web-install web-build web-dev
```

Insert this block immediately after the `vet-integration` target added in Task 1 (still before `generate`). **The recipe line must be a literal TAB:**

```makefile
# Run all tests under the race detector (whole module, unit tests only - no
# Docker). Catches concurrency regressions across the agent send goroutine, the
# worker/grace registries, the scheduler, and the perforce registry race guard.
# NOTE (Windows): -race needs cgo with a working gcc. The default Strawberry Perl
# gcc fails (exit status 0xc0000139); use MSYS2 mingw64 via
# CC=/c/msys64/mingw64/bin/gcc.exe (with its bin on PATH). Linux/CI is unaffected.
test-race:
	go test -race ./... -timeout 180s
```

- [ ] **Step 2: Run the target to verify it passes (Linux agent env)**

Run: `make test-race`
Expected: exit 0, all packages `ok` (or `[no test files]`), and `internal/agent/source/perforce` runs `TestRegistry_ConcurrentSweepAndMutate` under the detector with no `WARNING: DATA RACE` lines. This needs cgo + gcc (present on the Linux agent) and NO Docker - confirm the run does not hang waiting on a container (there is no `-tags integration`, so testcontainers code is not compiled in).

> **CONTINGENCY BRANCH (Task 2) - whole-module -race race surfaced.** If `make test-race` reports `WARNING: DATA RACE` or fails, and the race is NOT introduced by this change (it cannot be - this change adds no Go code), then it is a *pre-existing* race that has simply never run under `-race`:
> 1. Capture the full race report verbatim (both goroutine stacks, the package, the test name).
> 2. Descope the `test-race` recipe to a narrower, known-clean set so the new CI is green on arrival. Preferred narrowing, smallest blast radius first:
>    - the package the backlog item actually targets: `go test -race ./internal/agent/source/perforce/... -timeout 180s`, and if clean, widen toward the concurrency-critical set;
>    - or the concurrency-critical set: `go test -race ./internal/agent/... ./internal/worker/... ./internal/scheduler/... -timeout 180s`.
>    Choose the widest set that passes cleanly, excluding only the package(s) with the surfaced race.
> 3. File the surfaced pre-existing race(s) as new backlog item(s) under `docs/backlog/` (one per distinct race), each with the captured report and the package/test, referencing this plan as the discovery source.
> 4. Add a Makefile comment on the `test-race` recipe noting which package(s) are temporarily excluded and pointing at the filed backlog item(s), so the descope is visible and reversible.
> **The new CI must be GREEN when it lands.** Do NOT fix the pre-existing race in this CI-tooling iteration, and do NOT ship a red CI.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: add test-race target (whole-module -race unit run)"
```

---

## Task 3: Create the `go-ci.yml` GitHub Actions workflow

**Files:**
- Create: `.github/workflows/go-ci.yml`

- [ ] **Step 1: Write the workflow file**

Create `.github/workflows/go-ci.yml` with exactly this content (mirrors `python.yml`'s action versions; `vet-integration` runs before `test-race` so the fast compile-class failure surfaces before the slower instrumented run):

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

- [ ] **Step 2: Validate the YAML structurally (cannot run Actions locally)**

Because GitHub Actions cannot run locally, verify by these proxies:
- Run a YAML lint / parse check. If `yamllint` is available: `yamllint .github/workflows/go-ci.yml` (expect no errors). Otherwise parse it: `python -c "import yaml,sys; yaml.safe_load(open('.github/workflows/go-ci.yml')); print('yaml ok')"` (expect `yaml ok`).
- Structural review against `.github/workflows/python.yml`: same `on:` shape (`push.branches: [main]` + `pull_request`), same `actions/checkout@v4`, the Go analogue `actions/setup-go@v5` with a `with:` block, two `run:` steps that invoke the two `make` targets verified green in Tasks 1 and 2.
- Confirm the two `run:` steps name targets that now exist in the `Makefile` (`make vet-integration`, `make test-race`) - both were proven to pass locally in Tasks 1-2, which is the real evidence the CI job will be green. There is no `paths:` filter (Go code can live anywhere in the module), unlike `python.yml`.

Expected: YAML parses; structure matches the established pattern; both invoked targets exist and passed locally.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/go-ci.yml
git commit -m "ci: add go-ci workflow (vet-integration + test-race on push/PR)"
```

---

## Task 4: Close both backlog items (conductor-owned, final task)

**Files:**
- Move: `docs/backlog/idea-2026-06-19-race-test-target-perforce-package.md` -> `docs/backlog/closed/`
- Move: `docs/backlog/idea-2026-06-20-vet-integration-tagged-build.md` -> `docs/backlog/closed/`

This task is owned by the conductor and runs last, after Tasks 1-3 land green. Per the relay convention, closing backlog items is required scope, not optional cleanup. **Both** items close because this single iteration delivers both `test-race` (race target item) and `vet-integration` (integration-build item).

- [ ] **Step 1: Move both backlog files to closed**

```bash
git mv docs/backlog/idea-2026-06-19-race-test-target-perforce-package.md docs/backlog/closed/
git mv docs/backlog/idea-2026-06-20-vet-integration-tagged-build.md docs/backlog/closed/
```

(If `docs/backlog/closed/` does not yet exist, create it as part of the move per the existing repo convention for closed items.)

- [ ] **Step 2: Verify the moves**

Run: `git status`
Expected: two renames staged into `docs/backlog/closed/`; nothing left in `docs/backlog/` for these two ideas.

- [ ] **Step 3: Commit**

```bash
git add -A docs/backlog
git commit -m "backlog: close race-test-target and vet-integration-build ideas"
```

---

## Self-review against the spec

- **Acceptance 1 (test-race target, whole module, runs the perforce race test under -race):** Task 2.
- **Acceptance 2 (vet-integration target, `go vet -tags integration ./...`):** Task 1.
- **Acceptance 3 (go-ci.yml on push main + PR, ubuntu-latest, both make steps):** Task 3.
- **Acceptance 4 (Windows CC comment, no hard-coded Windows path):** Task 2 Step 1 comment block; no target sets `CC`.
- **Acceptance 5 (both backlog items moved to closed):** Task 4.
- **Decision 3 (document CC, do not hard-code):** honored - comment only, recipe is `go test -race ./... -timeout 180s` with no `CC`.
- **Decision 4 (fold integration-build item):** honored - single iteration, both items close in Task 4.
- **Spec timeout 120s -> 180s:** honored in Task 2 recipe.
- **No Go production or test code change:** honored - only `Makefile`, `.github/workflows/go-ci.yml`, and backlog doc moves.
- **Placeholder scan:** every code step shows the literal file content. No TBD/TODO.
- **Type/name consistency:** target names `vet-integration` and `test-race` are identical across `.PHONY`, recipes, and the workflow `run:` steps.
