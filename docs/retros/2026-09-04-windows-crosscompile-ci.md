---
date: 2026-09-04
topic: windows-crosscompile-ci
branch: claude/roadmap-now-dependencies-159be0
range: 9625119..11ca876
---

# Session Retro: 2026-09-04 - Windows Cross-Compile CI

**TL;DR:** Relay's agent runs on Windows render nodes, but its automated tests only ever ran on
Linux machines. Anything written specifically for Windows was therefore never even handed to a
compiler - a plain typo in one of those files would have left every check reporting success. This
session added a step that compiles the Windows code on the existing Linux machine, so a broken
Windows file now fails loudly. It does not yet *run* that code; that half was deliberately left for
later. The work was proven by deliberately breaking a Windows file and confirming the old checks
stayed green while the new one went red.

## Handoff

Lane B of the six-item Now batch. `idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code`
half 1 shipped as PR #200 (`11ca876`); the item stays OPEN for half 2 (a `windows-latest` job
running `go test ./...`). One step in `go-ci.yml`'s `test` job, ordered AFTER `Race unit tests`,
runs three commands under `bash -e`: `GOOS=windows go build ./...`, `GOOS=windows go vet ./...`,
`GOOS=windows go vet -tags integration ./...`. They reach disjoint file sets - see "no line is
redundant" below. `TestWindowsCrossCompileStepCommandsArePresent`
(`internal/agent/ci_crosscompile_guard_test.go`) pins all three by string match and says in its own
comment that it pins presence, not effect. The item's Summary, Repro and Acceptance were rescoped to
execution only, because it stays open and is therefore the artifact the next person reads.
`ROADMAP.md:41` and `:621` still repeat the now-false "`grep -rn GOOS .github/ Makefile` returns
nothing" and are left for the batch-end roadmap refresh. Next session: half 2 needs two decisions -
whether `-race` is included (cgo on the Windows runner is the awkward part) and the constraint that
`-tags integration` stays Linux, since the Windows runner has no useful Docker.

## What Was Built

- **`.github/workflows/go-ci.yml`** - one `Windows cross-compile check` step in the `test` job,
  plus a `# PLATFORM COVERAGE.` header and a `# Platform: linux.` line on each of the three jobs,
  satisfying the item's third acceptance bullet.
- **`internal/agent/ci_crosscompile_guard_test.go`** (new, untagged) - reads the workflow and
  asserts all three commands are present.
- **`docs/backlog/idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code.md`** - rescoped to
  execution only; the compile half is recorded as shipped.

## Key Decisions

- **Half 2 stays out.** The item names it as the judgement call. Half 1 is seconds of runtime with
  no new job and no Docker; half 2 buys execution of about five files plus two `runtime.GOOS`
  branches against added CI minutes on every PR.
- **The step is ordered after the race suite.** `run: |` executes under `bash -e`, so a Windows
  type error aborts the job. Placed first, it would cost all race-detector signal on exactly the
  runs where something is already wrong.
- **A third command was added beyond the item's proposal.** Neither untagged line compiles a
  `//go:build windows && integration` file, and `make vet-integration` builds for Linux, so that
  combination was reachable by nothing. `internal/agent` already carries both constraint kinds
  separately, so it is one edit away. Measured `exit 0` at HEAD, so it is a pure guard addition.
- **The presence guard was accepted despite being a weak instrument.** A string match on YAML
  cannot prove GitHub schedules the step. It was taken because deletion of the step otherwise
  leaves every lane green, and because it also pins the three-command set against the redundancy
  trap below. Its comment states the limit rather than letting the name imply more.
- **`GOARCH` was deliberately not set.** The tree has zero architecture-constrained files, so
  windows/arm64 and windows/amd64 admit an identical file set.

## What Went Wrong and What Changes

Ledger on the prior retro (`2026-09-04-text-search-cost-bound`), none of whose entries were
promoted: "the item's prescription was unbuildable" **applied** - every lane in this batch was
briefed to re-verify its item's dated claims, and three of four lanes refuted their input.
"An expected survival was recorded rather than engineered away" **applied**, and one better: the
equivalent gap here (deleting the step leaves every lane green) was closed with a guard rather than
recorded. "The conductor shipped a documentation defect and review caught it" **recurred**, in the
same shape - see the first entry below. The remaining three entries (the priority order, the
boundary-value example, the measurement changing the item's subject) were **not exercised**; they
go through the promotion check below along with everything else.

- **A false claim shipped in a comment, and the author had already rewritten it once to satisfy
  the same rule.** The header said a `//go:build windows` file "reaches a compiler only through the
  `test` job's cross-compile step". The implementer had caught and reworded a *different* sentence
  for exactly this reason before committing, then left this one. It is not merely unpinned, it is
  wrong: this project's primary development machine is Windows, where an ordinary `go build`
  compiles all five constrained files natively. Its practical harm is specific - a reader told CI
  is the sole compiler will not think to build locally, and will spend a CI round trip on an error
  `go build` shows in two seconds.
  -> **What changes:** when a comment scopes a claim to CI, test the claim on the DEV machine
  before writing it. "Only in CI" is a claim about every other environment, and this project's dev
  environment is the one the claim most often gets wrong. Remedy stays deletion, never correction.
  (already covered by [[reference_uniqueness_claim_is_about_the_complement]] and
  [[reference_correcting_a_uniqueness_claim]])

- **Two commands that look redundant covered disjoint file sets, and nothing said so.** The
  comment read as if `go build` and `go vet` both covered all five Windows-constrained files. They
  do not: `go build` does not read `_test.go`, so only the vet line reaches
  `credentials_acl_windows_test.go` and `runner_cancel_windows_test.go`. Measured - a type error in
  that test file is `exit 0` under build and `exit 1` under vet. A future editor deleting one line
  as redundant would drop coverage from five files to three, with every check still green and the
  comment still reading as if all five were covered.
  -> **What changes:** when a step runs several similar commands, state the CONSTRAINT that makes
  each non-redundant (`go build` does not read `_test.go`), never a census of what each covers, and
  pin the set with a guard so a deletion goes red. A census in a comment is forbidden and also
  useless here - it is the constraint a future editor needs.
  (promoted to [[reference_commands_that_look_redundant_cover_disjoint_sets]])

- **The guard's own RED could not be observed on the machine it was written on.** Proving "CI does
  not compile this file" requires a host where the file is genuinely excluded. On this Windows host
  `GOOS=windows` is native, so `make vet-integration` already compiles `proctree_windows.go` and
  the mutation dies immediately - the discriminating step looks like it was already working. The
  whole four-step proof had to be run under Linux.
  -> **What changes:** to prove a guard that closes a PLATFORM-EXCLUSION gap, run the proof on the
  platform that lacks the coverage, not the one that has it. This is the mirror of the existing
  rule about running `//go:build !windows` tests in a Linux container, and the same container is
  the tool for both. (promoted to [[feedback_platform_gated_test_verification]])

- **A test's injected attack value and its asserted-against value were the same literal, so a real
  environment could satisfy both.** Found outside the diff.
  `TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN` injects `?user=root` and
  asserts the derived default user is not `"root"`. But `wantDefaultUser` falls through to
  `pgx.ParseConfig`, which derives the OS user - measured directly as `chadv` on this host. Under
  any run whose OS user is `root`, both assertions fail together. That is the CLAUDE.md-recommended
  local container route, and it is green on GitHub only because that runner is `runner`.
  -> **What changes:** when a test injects a value and then asserts against that same literal, pick
  a discriminator no real environment can produce. Sharing the literal couples the attack to the
  ambient state and makes the test red for reasons that have nothing to do with its subject.
  (promoted to [[reference_injected_value_must_not_share_a_literal_with_the_assertion]])

- **The reviewer, not the implementer, produced the evidence for the two claims that mattered.**
  Both the 3-files-vs-5-files difference and "`go vet` runs nothing" were *asserted* in the
  implementation report and *measured* in review - the latter by adding an `init()` with
  `os.Exit(3)` to a Windows test file and watching `GOOS=windows go vet` exit 0 silently on a host
  where the binary could have run.
  -> **What changes:** when an implementer's report contains a claim about tool behaviour ("vet
  analyses test files", "this runs nothing"), brief the reviewer to measure that specific claim
  rather than to review the diff generally. Naming the claim in the review brief is what turned
  both into evidence here. (promoted to [[feedback_verify_tree_not_subagent_claims]])

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`bug-2026-09-04-pgdsn-user-guard-is-red-for-any-run-as-root`](../backlog/bug-2026-09-04-pgdsn-user-guard-is-red-for-any-run-as-root.md) - pgdsn's DSN user-arm guard is RED for any run as root, which is the documented local container route
- See [`idea-2026-09-04-go-ci-race-step-comment-carries-change-history`](../backlog/idea-2026-09-04-go-ci-race-step-comment-carries-change-history.md) - go-ci.yml's Race unit tests comment carries the change history current policy forbids

## Files Most Touched

- `.github/workflows/go-ci.yml` (+30) - the cross-compile step, the platform-coverage header, and
  a per-job platform line on each of the three jobs.
- `internal/agent/ci_crosscompile_guard_test.go` (+60, new) - the presence guard and a `moduleRoot`
  helper that walks up to `go.mod`.
- `docs/backlog/idea-2026-09-01-go-ci-never-compiles-or-runs-windows-code.md` (+35/-20) - rescoped
  to execution only; the compile half recorded as shipped, the item kept open.
