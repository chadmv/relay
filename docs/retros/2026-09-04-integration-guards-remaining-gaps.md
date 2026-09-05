---
date: 2026-09-04
topic: integration-guards-remaining-gaps
branch: claude/roadmap-now-dependencies-159be0
range: f1849cd..294a23a
---

# Session Retro: 2026-09-04 - Integration Guards, Remaining Gaps

**TL;DR:** A large share of this project's tests only run when someone types a special flag by hand,
so the automated checks never executed them - a test could quietly stop working and every check would
still report success. This session put every remaining group of those tests into an automated lane,
and closed a two-month-old tracking item. The irony worth recording: the change itself shipped five
comments telling readers that the automated checks do not run things they now run, and the count used
to justify closing the item was computed against a version of the code that was already out of date.

## Handoff

Lane C of the six-item Now batch. Closed `idea-2026-08-23-integration-only-guards-ci-never-runs` after
eleven appended instances; merged as PR #204. `internal/worker`, `internal/scheduler`, `internal/mcp`
and `internal/agent` joined `pg-integration`; `internal/api` got its own `api-integration` job
(~230s, `API_TEST_TIMEOUT ?= 1800s` with `480s` in the job env so the Go runtime panics inside the
job kill and names the hung test). `internal/worker`'s `statusStubDB` now captures `AppendTaskLog`'s
arguments. `internal/agent/source/perforce` stays out, with the refusal in `startP4dContainer`'s own
comment. **`Makefile` uses `./internal/agent`, not `./internal/agent/...`** - the recursive form picks
up perforce, whose tests `t.Skip` without a `p4` binary, which is a silent skip reporting green.
`TestMakefileIntegrationTargetsHaveACIStep` pins every `test-*-integration` target to a `make` step in
the workflow. Census on the final tree: 139 integration-tagged files, 53 in a lane, 4 in perforce.

## What Was Built

- `.github/workflows/go-ci.yml` - the `api-integration` job; `pg-integration` grown to eight packages.
- `Makefile` - `test-api-integration`, the expanded `test-pg-integration`, `API_TEST_TIMEOUT`.
- `internal/{api,mcp,scheduler,worker}` test harnesses rewired onto `internal/testsupport/pgdsn`.
- `internal/worker/taskstatus_errormessage_test.go` - the widened stub and the test it enables.
- `internal/agent/ci_makefile_lockstep_guard_test.go` - the target-to-job guard.

## Key Decisions

- **`internal/api` gets its own job rather than joining `pg-integration`.** At ~230s against the
  others' ~105s combined, two runners make the wall clock the max rather than the sum. The "1.8x"
  ratio originally given for this was measured against the pre-slice four-package job and was ~0.7x
  against the shipped one; the split is right by wall clock, so the number went and the argument
  stayed.
- **The Go timeout goes BELOW the job timeout**, matching the sibling jobs, so a hang produces a Go
  panic with a goroutine dump naming the test rather than a bare GitHub kill.
- **The perforce refusal is a comment, not a job**, which is the item's own third acceptance branch.
- **The item closes**, and the Resolution says plainly that its title stopped describing it at
  instance six.

## What Went Wrong and What Changes

- **The slice committed the defect it exists to fix.** Five durable comments now asserted that CI does
  not run things this diff makes it run - including a `THE TESTS IN THIS FILE DO NOT RUN IN CI` banner
  above a paragraph describing this PR's own work as future work, and an explicit uniqueness claim
  ("the ONE test covering bug-... that RUNS IN CI"). Two more were already false before the slice
  began, having been falsified by the previous slice and never revisited.
  -> **What changes:** when a slice changes a global property of the repo - what CI runs, what a lane
  covers, which platform compiles what - grep the whole tree for prose asserting the OLD property
  before opening the PR, and delete each hit rather than rewriting it. The comments that go stale are
  never in the files you edited; that is why editing the code does not surface them.
  (promoted to [[reference_changing_a_global_property_stales_prose_you_did_not_touch]])

- **A claim about the complement was computed against a base that moved underneath it.** The census
  underwriting "the item closes" was taken before a sibling lane in the same batch merged and added an
  integration-tagged file to `internal/agent/source/perforce`. Re-run on the rebased tree: 139 files
  rather than 137, `internal/api` 58 rather than 57, and perforce four files where the spec's table
  said three and its own closing sentence said two.
  -> **What changes:** in a multi-lane batch, rebase onto current `main` immediately before computing
  any census, and re-run the instrument after any rebase. A count is a claim about everything you did
  not look at, and in a concurrent batch that set changes while you work.
  (promoted to [[reference_rebase_before_you_count]])

- **A pattern's recursive form was a silent false green, and only a listing showed it.** `./internal/agent/...`
  picks up `internal/agent/source/perforce`, whose tests `t.Skip` without a `p4` binary. On the dev box
  both p4 and Docker exist so they run; on a bare runner they skip and the job is green.
  -> **What changes:** when adding a package pattern to a CI lane, run `go test -tags <tag> -list '.*'`
  on the exact pattern and read the names. A skip is indistinguishable from a pass in the job summary,
  so the only place the difference is visible is the test list.

- **A justification cited a fact about the CI runner that is false.** "The p4 client or Docker is
  unreachable - both true on a GitHub runner." Docker is available on `ubuntu-latest`; the same
  workflow file's `services: postgres` blocks are Docker service containers. The conclusion survived
  on the p4 arm alone, and the spec contradicted itself on this thirty lines apart.
  -> **What changes:** a claim about the CI environment is checkable from the workflow file you are
  editing. Before writing one, look for the counter-example in the same file.

## Recommended Backlog Items

Backlog intake, not a priority order. Everything this slice deliberately left alone already has its
own item: `internal/cli`'s 51 vacuous fixtures, the counter-payload guards proven against fixtures
rather than producers, the status-vocabulary migration-parsing alternative, and
`python/tests/integration/`'s manual-lane status.

## Files Most Touched

- `docs/superpowers/specs/2026-09-04-integration-guards-remaining-gaps.md` (+781) - the spec, with the
  census corrected in the fix round.
- `Makefile` (+69/-20) and `.github/workflows/go-ci.yml` (+68/-12) - the new job, the grown lane, the
  timeout variable.
- `internal/worker/taskstatus_errormessage_test.go` (+64) - the widened stub's test; the mutation that
  proves it leaves the untagged package blind at `origin/main`.
- `internal/{api,mcp,scheduler,worker}` harness files (+139/-52) - `pgdsn` wiring, five packages.
- `internal/agent/ci_makefile_lockstep_guard_test.go` (new) - the target-to-job presence guard.
- `CLAUDE.md` (+32/-12) - the lane list and what each covers.
