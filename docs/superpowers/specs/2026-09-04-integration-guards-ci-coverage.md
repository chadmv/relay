---
date: 2026-09-04
topic: integration-guards-ci-coverage
item: docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md
status: draft, pending human review
---

# One CI lane for the two cheapest Postgres-only integration packages, plus the rule that generalizes it

## Status of this document

Written by the TPM agent from tree evidence. The brainstorming flow's one-question-at-a-time
dialogue was not available in a subagent session, so the open design questions were resolved
against the tree and each answer is recorded below with the evidence that settled it and with
whether the supporting number was measured, re-derived, or repeated from an existing document.
**Nothing here was measured by running it.** No shell was available in the authoring session, so
every runtime figure is either re-derived arithmetic or a quotation with an attribution. Section 10
says which is which, per number. The implementer measures for real; section 6 makes that a
precondition rather than a nicety.

## 1. The slice, in one paragraph

Extract `newIntegrationDSN` out of `internal/cli/pgharness_integration_test.go` into an importable,
untagged helper package; rewire `internal/store` and `internal/schedrunner` to take their database
from it; add one `services: postgres` job to `.github/workflows/go-ci.yml` that runs those two
packages' integration-tagged tests; and add a short rule to CLAUDE.md saying a security-property
guard must not be integration-only without a recorded reason. Nothing else.

## 2. Why this, and why it is worth a spec

The backlog item has grown to ten appended instances and its original framing (three named guards)
is the least interesting part of it now. Two things changed under it:

- **The mechanism problem is solved.** `.github/workflows/go-ci.yml`'s `cli-integration` job runs
  integration-tagged tests with no Docker API, no image pull and no Ryuk reaper: a `services:
  postgres` block plus `RELAY_TEST_DATABASE_URL`, which selects `newIntegrationDSN`'s
  shared-service mode (one freshly created database per test). The remaining work is not "invent a
  lane", it is "point more packages at the one that exists".
- **The value of doing so is signal, not enforcement.** Nothing in this repo blocks a merge:
  `main` carries no branch protection and no rulesets (verified 2026-08-27, and the
  `cli-integration` job's own comment records the decision to keep it that way). A new job here
  cannot stop anybody merging anything. What it does is make a red visible on the commit that
  caused it, to a reader who did not think to run `go test -tags integration -p 1
  ./internal/store/...` by hand with Docker running. Today the only thing standing between
  `IncrementTaskRetryCount`'s `AND retry_count < retries` budget predicate and a silent regression
  is a human remembering to do that. The argument for this slice is entirely about attribution
  speed, and it should not be written up as anything stronger.

The reason it needs a spec rather than a ticket is section 6: a lane CI has never run may contain
tests that do not pass on a clean runner, and moving from one-container-per-test to
one-database-per-test-on-a-shared-server changes an isolation property that at least two tests in
the candidate packages reason about explicitly. That risk is the single largest thing in the slice
and it needs a written protocol, not a hope.

## 3. Scope boundary

**In scope**

1. A new importable helper package holding the DSN harness, extracted from
   `internal/cli/pgharness_integration_test.go`.
2. `internal/store` and `internal/schedrunner` rewired onto it.
3. One new job in `.github/workflows/go-ci.yml` and one new `make` target that the job invokes.
4. The generalizing rule in CLAUDE.md, plus a Commands entry for the new target.
5. A progress note appended to the backlog item. **The item stays open.**

**Out of scope, by construction**

- `internal/agent/source/perforce` needs a p4d container (`p4d_container_test.go`) and the `p4`
  CLI. It cannot join a `services: postgres` job at all. It is out by construction, not by
  prioritization, and no future slice should treat it as a candidate for this mechanism.
- `internal/api`. Postgres-only, but excluded on cost and risk; see section 4.1.
- `internal/worker`, `internal/scheduler`, `internal/mcp`, `cmd/relay-server`. Postgres-only, and
  genuine candidates for a later slice; see section 4.1 and section 8.
- Making `python/tests/integration/` run automatically. Settled as a NO in section 4.4.
- Any change to `make test-integration`, `make test-race`, or `make vet-integration`.
- Any rewrite of the existing default-lane fixtures, which is a separate open item.

## 4. Decisions

### 4.1 Which packages join, and what each costs

Every integration-tagged package except `internal/agent/source/perforce` needs only Postgres as an
external service. That is broader than the item claims, and the correction matters: the item and
the roadmap both write the exclusion as "the packages that need p4d or a real gRPC agent". There is
no test in the tree that needs a real external gRPC agent.
`cmd/relay-server/grpc_admission_e2e_integration_test.go` builds a real `net.Listener`, a real gRPC
server from `grpcServerOptions`, `netlimit.Wrap` and `worker.Handler`, and drives it with an
in-process gRPC client. Its only external dependency is Postgres. So the real selection axis is
cost and risk, not capability.

Costs, expressed in database acquisitions because that is what the shared-service mode charges per
unit. Each acquisition is one `CREATE DATABASE` plus one full run of the 23 migrations in
`internal/store/migrations/`, which is the dominant fixed cost and is identical across packages.
Counts are from the tree (verified); seconds are derived from them (see section 10).

| Package | Acquisitions | Entry points to rewire | Derived shared-service cost |
| --- | --- | --- | --- |
| `internal/schedrunner` | 12 (`newRunnerHarness`) | 1 | ~17 to ~26 s |
| `internal/store` | ~72 (63 via `newTestPool`/`newTestQueries`, 8 via `newMigratedPoolWithDSN`, 1 in `TestMigrate`) | 3 | ~100 to ~160 s |
| `internal/api` | ~160 (`newTestPool`/`newTestQueries`) | 2 | ~220 to ~350 s |
| `internal/cli` (already in CI) | 25 (24 `startRelayServer`, 1 harness self-test) | done | 54 s measured in CI |

**Chosen: `internal/schedrunner` and `internal/store`.** Together they are about 84 acquisitions,
derived at roughly 120 to 190 seconds of test time, comfortably inside a job timeout with the
headroom section 5.2 asks for. They also carry the three named instances the item cares most about:
the status-vocabulary lockstep guards (`TestTasksStatusVocabularyIsExactly`,
`TestJobsStatusVocabularyIsExactly`), the shipped security fix whose only proof is
`TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows`, and the write half of
the schedule-failure split (`internal/schedrunner`'s `TickOnce` regression tests). One of those is a
security fix whose proof is invisible, which the item correctly ranks above a guard whose coverage
is invisible.

**`internal/api` is excluded, and the reason is not only its size.** Three arguments:

1. **Its cost is not known within a factor of two, and the tree disagrees with itself about it.**
   `CLAUDE.md` says "the whole `internal/api` integration package runs about 9.5 minutes";
   the `test-integration` target's own comment in `Makefile` says "`internal/api` alone runs
   ~320-340s". Both are in the tree and they differ by nearly 2x. Adding a lane whose runtime is
   unknown to that degree risks a `timeout-minutes` kill, which is exactly the ambiguity the
   `cli-integration` job's timeout comment exists to prevent.
2. **Its rewire surface is the largest and its blast radius the widest.** 29 files call a harness
   entry point, and `installFailDeleteTrigger` mutates the schema mid-test.
3. **Its gap is a different decision.** For `internal/api` the item's finding is not "some guards
   are integration-only", it is that the *entire handler surface* is: `internal/api/api_test.go`
   itself carries the tag. Whether the whole handler suite belongs on every push is a question with
   its own answer, and it deserves a slice where someone measures first rather than inheriting a
   number from a comment.

### 4.2 The harness extraction

**The item's claim, tested.** The item and the roadmap both say `newIntegrationDSN` "was
deliberately written to import nothing from `internal/cli` or `internal/api` so the extraction is a
file move rather than a redesign". The first half is **true and I verified it**: the file imports
only stdlib, `relay/internal/store`, `pgx`, `testify` and `testcontainers`, and the function body
references no `internal/cli` identifier. The second half is **too strong**, in four specific ways
the implementer will hit in the first hour:

- The harness lives in a `_test.go` file. **A test file cannot be imported by another package.**
  The move is therefore to a non-test file in a new package, with the symbol exported. That is a
  rename plus an export, not a move.
- The file is `package cli`, and two of its unexported declarations are used elsewhere in that
  package: `boundedCleanup` in `internal/cli/relayharness_integration_test.go` is typed against
  `dsnAssertT` and bounded by `teardownTimeout`, both declared in the harness file. Moving the file
  wholesale breaks `internal/cli` unless those travel too.
- The file mixes two kinds of test. `TestIntegration_HarnessDSNIsMigratedAndEmpty` needs a live
  database; the five DSN guards need nothing at all (section 4.6). They cannot share one build tag
  after the move.
- `internal/store` contains the only tests that migrate a database from scratch. `TestMigrate` and
  `newMigratedPoolWithDSN` need an **unmigrated** database and the `pgx5://` DSN form.
  `newIntegrationDSN` always migrates, so it cannot serve them as written. This is not in the item.

**Design.** A new package `internal/testsupport/pgdsn` (does not exist today; there is no
`internal/testsupport` directory in the tree). Its exported surface, deliberately minimal:

- `NewEmptyDSN(t) string` - creates the database (or the container) and returns a `postgres://` DSN
  with no migrations run.
- `NewIntegrationDSN(t) string` - `NewEmptyDSN` plus `store.Migrate`. This is today's
  `newIntegrationDSN` behaviour and today's callers keep it unchanged.
- `MigrateDSN(dsn) string` - today's `migrateDSN`, the `postgres://` to `pgx5://` rewrite, needed
  by the two `internal/store` files that drive `golang-migrate` themselves.
- `TeardownTimeout` and `AssertT` (today's `teardownTimeout` and `dsnAssertT`), plus
  `BoundedCleanup` (today's `boundedCleanup`, moved out of `relayharness_integration_test.go`).
  These three exist in the surface only because `internal/cli` already depends on them; do not
  invent further consumers in this slice.

Everything else in the file - `dbNamePattern`, `assertNoConnectionTargetOverride`,
`assertDSNTargetsDatabase`, `wantDefaultUser`, `pgConnectionTargetQueryKeys`, `newSharedServiceDSN`,
`newContainerDSN` - stays unexported inside the new package, with its comments intact. Those
comments carry the pgx `parseURLSettings` reasoning and the 2026-08-27 review findings; they are the
argument for the guards and must not be summarized away in the move.

**Build tag: none, and the choice is forced.** If `pgdsn`'s source file carried `//go:build
integration`, then an untagged `_test.go` beside it would reference symbols absent from the default
build and `go test ./...` would fail to compile the package. Since section 4.6 wants the five pure
DSN guards running untagged, the package source must be untagged. The consequences, stated so they
are not discovered later: `go build ./...` and `go vet ./...` will compile testcontainers-go and
`testing` into that package on every default run (both are already `go.mod` requirements, so no
dependency change), and `go test ./...` will run the five pure guards. Nothing in `cmd/` imports
it, so no binary changes. The package's one database-touching test keeps `//go:build integration`
in its own file.

**Import cycles: checked, not assumed.** `pgdsn` imports `relay/internal/store`. A cycle would need
a consumer that `store` imports, or an in-package test of `store` itself. Neither exists:

- No non-test file under `internal/store` imports any `relay/internal/...` package, so
  `store` cannot reach back to `pgdsn`.
- Every `internal/store` test file is `package store_test` except `export_test.go`, which is
  `package store` and needs no DSN (it declares `MigrateTo` only). So no in-package test of `store`
  imports `pgdsn`, which is the one arrangement Go rejects outright.

**This constraint is permanent and must be written into `pgdsn`'s doc comment**: a future
`package store` test file that wants a database is an import cycle, and the fix is to put it in
`store_test`, not to break the harness apart.

`internal/schedrunner` (`package schedrunner_test`) and `internal/cli` (`package cli`, which
`store` does not import) are both unaffected.

### 4.3 Rewiring, and what each package's harness becomes

- `internal/store/testhelper_test.go`: `newTestPool` becomes `pgxpool.New` over
  `pgdsn.NewIntegrationDSN(t)`; its own `store.Migrate` call is dropped, because the harness has
  already run it. `newTestQueries` is unchanged (it wraps `newTestPool`).
- `internal/store/migrate_test.go` (`TestMigrate`) and `internal/store/migrate_down_test.go`
  (`newMigratedPoolWithDSN`): these take `pgdsn.NewEmptyDSN(t)` and `pgdsn.MigrateDSN`.
  `TestMigrate`'s subject is `store.Migrate` itself, including its second, idempotent call, so it
  must keep receiving a database with no `schema_migrations` row. If it silently received a
  migrated one, it would still pass, and it would be asserting nothing. Call that out in the
  commit.
- `internal/schedrunner/runner_test.go`: `newRunnerHarness` takes
  `pgdsn.NewIntegrationDSN(t)` and drops its own `tcpostgres.Run` and `store.Migrate`.

Use `pgdsn.BoundedCleanup` for the `pool.Close` cleanups at the sites this slice already touches.
Both `newTestPool` and `newRunnerHarness` currently register a bare `t.Cleanup(pool.Close)`, which
is the unbounded-teardown shape recorded in
`docs/backlog/bug-2026-08-26-integration-lane-times-out-on-docker-teardown`. Do not go looking for
other cleanups to convert; that is a different slice.

### 4.4 `python/tests/integration/`: no, and it is formally manual

**The answer is no.** That lane is not meant to run automatically, and this slice records the
decision rather than leaving it implicit. Three reasons, in order of weight:

1. **It is not a service-dependency problem, it is a topology problem.** Its conftest requires
   `RELAY_INTEGRATION=1`, a `RELAY_URL`, a `RELAY_TOKEN`, and its docstring requires "at least one
   online agent able to run the submitted task". `test_submit_and_wait` waits for a job to reach
   `DONE` and reads its task's logs back; `test_cancel_running_job` submits a `sleep 30` and
   cancels it. Running those needs relay-server plus a live relay-agent executing real subprocesses
   plus Postgres, wired together in CI. That is a whole end-to-end lane, not a `services:` block,
   and it is a different project from this item.
2. **Placed where it is, it could not fire on the commits that break it anyway.**
   `.github/workflows/python.yml` filters on `paths: python/**` and
   `.github/workflows/python.yml`. The properties that lane uniquely proves are properties of Go
   code: `buildPage`'s answer for a drained empty page, and the absence of `omitempty` on
   `internal/api`'s `page[T]`. A commit that breaks either touches no file under `python/`, so even
   a fully automated `python.yml` would not run on it. Adding CI there would buy a green tick and
   no coverage.
3. **The precedent already ran and worked.** The strict-envelope slice did not file the gap; it
   moved the guard to the side that owns the tag.
   `TestPageEnvelope_AllThreeKeysArePresentOnAZeroValuePage` lives in
   `internal/api/pagination_test.go`, runs in `go-ci.yml`, which has no `paths:` filter, and kills
   all three single-tag mutations.

**The consequence, which is the part that actually binds future slices:** an assertion whose only
home is `python/tests/integration/` is **not evidence**. A slice that wants to prove something must
find a home in a lane that runs, or do without and say so. `python/tests/integration/` keeps its
job as a manual pre-release smoke lane against a real deployment, which is a real job, and the
`RELAY_INTEGRATION` gate plus `make python-test-integration` stay exactly as they are. If somebody
later wants a live-server Python lane, the right shape is a workflow with no `paths:` filter that
stands up the server, not a change to `python.yml`.

Record this in two places, because a spec is a record of a moment: one sentence in the
`pytest_collection_modifyitems` docstring in `python/tests/integration/conftest.py` saying the lane
is accepted as manual and why, and the second rider of the CLAUDE.md rule in section 4.5.

### 4.5 The generalizing rule, as exact prose

Add to `CLAUDE.md`, as a subsection under the testing material that precedes "## Architecture":

> ### A guard behind a build tag must be able to run
>
> `.github/workflows/go-ci.yml` runs `go test -race ./...` with no tags, plus two `services:
> postgres` jobs (`cli-integration`, `pg-integration`). Every other `//go:build integration` test
> runs only when a human runs it. Before putting a guard behind the tag, ask in this order:
>
> 1. **Does it need the tag at all?** A guard over a pure function does not. Move it to an untagged
>    file in the same package and delete the export shim it needed. That closes the gap by deleting
>    code rather than adding a job, and it is the cheapest outcome available.
> 2. **Can it run in a lane CI runs?** A package that needs only Postgres joins `pg-integration` by
>    taking its database from `internal/testsupport/pgdsn`.
> 3. **Otherwise write the reason in the test's own comment**, naming what would have to exist for
>    it to run. A security-property guard - one pinning an authorization check, an epoch or identity
>    fence, an input bound, or a sanitiser - must not be integration-only without that sentence.
>
> Two riders. A `go/parser` or `go/ast` guard is the expensive fallback, not an equivalent
> substitute: the `finishRegister` handoff guard was evaded five times before it held, each time by
> a construct that is nil in one context and real in the other. And a guard must live in a lane that
> runs on the commits that can break its property - a guard for a cross-language property placed
> under `.github/workflows/python.yml`'s `paths: python/**` filter cannot fire on the Go commit that
> renames the symbol, so it belongs on the Go side. `python/tests/integration/` is accepted as a
> manual lane, so an assertion whose only home is there is not evidence.

Three properties this prose is trying to have, stated so a reviewer can check them. It is ordered
cheapest-first, so the common case never reaches step 3. Every step is checkable by reading one
file. And it does not promise that a guard will run, only that a reader can find out why it does
not.

### 4.6 The cheapest sub-shape: ask whether the guard needs the tag at all

The 2026-09-03 entry's finding is that the cheapest instance to retire is one that never needed the
tag, and it is retired by deleting an export shim rather than adding a CI job.

**There is at least one such instance in the tree today, and it is in the file this slice is already
moving.** Five tests in `internal/cli/pgharness_integration_test.go` touch no database:
`TestAssertDSNTargetsDatabase_CatchesQueryOverridingPath`,
`TestAssertNoConnectionTargetOverride_RejectsConnectionTargetKeys`,
`TestAssertDSNTargetsDatabase_AcceptsLegitimateDSNs`,
`TestWantDefaultUser_DerivesOSUserWhenDSNCarriesNone` and
`TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN`. The first one's own
comment says so: "It needs no Docker and no reachable Postgres: `pgx.ParseConfig` only parses the
connection string, it never dials". They are security-property guards in the strict sense of the
rule above - they are the whole control that stops a supplied `RELAY_TEST_DATABASE_URL` from
redirecting this harness's `CREATE DATABASE`, `DROP DATABASE ... WITH (FORCE)`, migrations and admin
token seed to another server, user, or database. They run in CI today via `cli-integration`, so this
is not a coverage hole; it is a lane-appropriateness one. Because section 4.2 leaves `pgdsn`
untagged, these five land in an untagged `_test.go` in the new package and start running in
`make test` and in the race lane at zero extra cost.

**How the implementer should look for the rest, and what to record.** Do not assert a count without
running the filter, and do not claim these five are the only ones - that is a claim about the
complement.

1. Enumerate integration-tagged test files: search for `^//go:build integration` restricted to
   `*_test.go`. At the time of writing 132 files in the module carry the tag, test and non-test
   together; the test-file subset is the population.
2. Filter mechanically to files that mention none of `pgxpool`, `pgx.`, `store.Queries`,
   `tcpostgres`, `newTestPool`, `newTestQueries`, `newRunnerHarness`, `DSN`, `harness`. Those are
   the candidates.
3. Read each candidate. The tell for a false positive is a test that reaches production code
   through an `export_test.go` shim that is itself tagged - in that case the shim is the reason for
   the tag, and deleting the shim is the fix, exactly as the sanitiser guard did on 2026-09-03.
   Note that of the four `export_test.go` files in the module, three are tagged (`internal/api`,
   `internal/store`, `internal/worker`) and `internal/agent`'s is not; none of the three tagged
   ones is a pure-function shim today.
4. Record the hit count and the file list in the PR body. If the count is more than the five named
   here, **do not** widen this slice to fix them: file one item naming the files, since a
   file-by-file move is independent work that does not need this lane.

## 5. The lane

### 5.1 The make target

New target `test-pg-integration` (does not exist today):

    go test -tags integration -count=1 ./internal/store/... ./internal/schedrunner/... -timeout 600s

Each element, with its reason:

- `-count=1` for the reason the `test-cli-integration` comment already gives: Go's cache key covers
  the test binary, its arguments and the env vars a test read, and nothing about whether a live TCP
  connection to Postgres succeeded. Without it, a PR that edits only the `services.postgres` block
  can report `ok (cached)` having contacted no database.
- **No `-p 1`.** Every test gets its own freshly created database, so cross-package parallelism has
  no shared mutable state to corrupt, and the two server-wide catalog queries in these packages are
  already scoped (section 6.3 names them). `make test-integration`'s `-p 1` exists for parallel
  *container* conflicts, which this mode does not create. If the first local run shows interference,
  fall back to `-p 1` and record the observed symptom as the reason.
- `-timeout 600s` against a derived worst case of about 190 seconds, and deliberately not equal to
  the job's own limit; see below.

Add one line for it to `CLAUDE.md`'s Commands block, in the style of the neighbouring entries, and
add it to the `.PHONY` list at the top of `Makefile`.

### 5.2 The CI job

A second job in `.github/workflows/go-ci.yml`, named `pg-integration`, mirroring `cli-integration`:
the same `services: postgres` block (`postgres:16`, user/password/db `relay`, port 5432, the same
`pg_isready` health options), the same
`RELAY_TEST_DATABASE_URL: postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable` including
the literal `127.0.0.1` for the reason that variable's comment already records, `actions/checkout@v5`,
`actions/setup-go@v6` with `go-version-file: go.mod` and `cache: true`, and one step running
`make test-pg-integration`.

Two choices to state explicitly:

- **A separate job, not a step inside `cli-integration`.** A step would reuse one checkout and one
  Postgres service, which is cheaper in machine time, but it would put both lanes on one clock and
  serialize about 190 seconds onto a 54-second lane. Two jobs run concurrently on separate runners,
  so the wall clock is the max rather than the sum, and a red result names one lane.
- **`timeout-minutes: 12`, against the target's `-timeout 600s`.** Different numbers, deliberately,
  for the reason `cli-integration`'s comment gives: when the two are equal, a Go panic and a GitHub
  job kill are indistinguishable in the log.

**This job is advisory, like every other job in this repo.** The new job's comment should say so in
one sentence and should not describe itself, or any other check, as a merge gate.

## 6. The RED-risk protocol, which is the reason this slice needs a spec

A lane CI has never run may contain tests that do not pass on a clean runner. The implementer must
establish that these two do, locally, before the job exists, and must report real output.

### 6.1 Required measurements, in order

1. **Baseline, container mode, at HEAD, before any edit.** Run each candidate package's integration
   lane with `RELAY_TEST_DATABASE_URL` unset. Record the `ok relay/internal/... <seconds>` line and
   the elapsed time per package.
2. **Shared-service mode, after the rewire.** Same packages, `RELAY_TEST_DATABASE_URL` pointed at a
   Postgres the implementer started. Record the same lines.
3. **`make test-cli-integration`, after the extraction**, in both modes. The extraction edits
   `internal/cli`; this is its regression check, not a formality.
4. **`make test`, `make vet-integration`, and `go build ./...`** after the new untagged package
   exists. The first two because the untagged package changes what the default lane compiles; the
   third because a non-test package importing `testing` is a shape this repo has not had before.

Report real PASS output in the PR body, per package, with counts and timings. "The lane looks fine"
is not a result. A green re-run bounds a risk, it does not retire it: if a run is red once and
green after, report "N green, one red, cause unestablished", not "flake".

### 6.2 What to do when a lane is red

Diagnose direction first, because "pre-existing" and "caused by this slice" have opposite
resolutions and the only way to tell them apart is to have run step 1.

- **Red in container mode at HEAD.** Pre-existing, and it is the item's own thesis made visible.
  Out of scope to fix. File it naming the test and the failure, note it in the PR, and **exclude
  that package from the new job** until it is fixed, because shipping a job known to be red teaches
  a reader to ignore it, which also hides the next real red.
- **Green in container mode, red in shared-service mode, and the cause is a premise inside the
  test's own setup** (an assumption that it is alone on its server). Fix it in this slice. This is
  the expected shape and it is small: scope the query, do not serialize the lane.
- **Green in container mode, red in shared-service mode, and the cause is anywhere else.** Exclude
  that package from the job, file a specific item naming the test and the interference, and ship
  the other one. One package in CI is strictly better than none, and a half-slice that says which
  half is honest.

Shipping both packages is not a success criterion. Shipping a job that is green and says something
is.

### 6.3 The known interference sites, named so they are checked first

Moving from one container per test to one database per test on a shared server keeps per-database
isolation and loses server-wide isolation. Two tests in the candidate packages query server-wide
catalogs, and both already scope themselves. Both are expected to pass; verify, do not assume.

- `internal/schedrunner/startup_validation_fence_integration_test.go`'s
  `waitForBlockedScheduledJobsUpdate` polls `pg_stat_activity` for a backend blocked on a lock. It
  is predicated on `pg_blocking_pids(pid)` containing this test's own holder PID, and its comment
  states outright that an earlier version relied on `newRunnerHarness` giving every test its own
  container, and that `RELAY_TEST_DATABASE_URL` is what would break that reliance. This slice is the
  event that comment anticipated.
- `internal/store/retry_job_tasks_integration_test.go` polls `pg_stat_activity` filtered by
  `datname = current_database()`, which under the per-database model is exactly the right scope.

Re-run the search rather than trusting this list, since a uniqueness claim is a claim about the
complement. The shape to search for across both packages is
`pg_stat_activity|pg_locks|pg_blocking_pids|pg_database|current_setting|max_connections|pg_terminate`,
and the hit count belongs in the PR body. At the time of writing that search returns two files
across all of `internal/`, both named above.

One more axis, not a catalog query: connection count. `postgres:16` defaults to 100 connections;
each test opens one pool at pgxpool's default size and closes it in `t.Cleanup`, and tests within a
package run sequentially. A red reading `too many clients already` means a pool is outliving its
test, and the fix is that pool, not the server's configuration.

## 7. Acceptance criteria

1. `internal/testsupport/pgdsn` exists, is untagged, exports `NewEmptyDSN`, `NewIntegrationDSN`,
   `MigrateDSN`, `TeardownTimeout`, `AssertT` and `BoundedCleanup`, and carries the import-cycle
   constraint in its doc comment.
2. The five pure DSN guards named in section 4.6 live in an untagged test file and run under
   `make test`.
3. `internal/store` and `internal/schedrunner` take their databases from `pgdsn`, and no
   `tcpostgres.Run` call remains in either package.
4. `make test-pg-integration` exists, is in `.PHONY`, and is documented in `CLAUDE.md`'s Commands
   block.
5. `.github/workflows/go-ci.yml` has a `pg-integration` job that invokes it, and that job is green,
   with its run time recorded in the PR body against the derived estimate in section 4.1. A
   measurement that lands outside 100 to 190 seconds is not a failure, but it must be reported with
   the number, so the next slice's estimate for `internal/api` is calibrated against something real.
6. `make test-cli-integration`, `make test`, `make vet-integration` and `go build ./...` are green,
   with output in the PR body.
7. The CLAUDE.md rule from section 4.5 is present, verbatim or with reviewed edits.
8. `python/tests/integration/conftest.py`'s docstring records the manual-lane decision.
9. The backlog item has a progress note and **remains open**, listing section 8's residue.

## 8. What this slice does NOT close

The item stays open. Left untouched, by instance:

- **The three originally named guards.** `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll`
  (`internal/worker`), `cmd/relay-server/grpc_admission_e2e_integration_test.go`, and
  `internal/api/server_counters_realsocket_integration_test.go` all still run only when a human runs
  them. All three are Postgres-only and all three are reachable by the same mechanism in a later
  slice. Triage each against the section 4.5 ordering first, not straight to the CI job.
- **Instance 6, `internal/api`'s entire handler surface.** Excluded on cost and unknown runtime; see
  section 4.1. This is the largest remaining piece and should be its own slice, starting with a
  measurement.
- **Instance 10's sub-shape, a stub that discards the field under test.**
  `errorMessageLogStream` dies only under the tag because the untagged tests' `AppendTaskLog` stub
  does not capture the `Stream` argument. The remedy there is widening the stub, not a CI job, and
  it is independent of everything here.
- **`internal/agent/source/perforce`.** Out by construction: p4d.
- **`internal/worker`, `internal/scheduler`, `internal/mcp`, `cmd/relay-server`.** Postgres-only,
  unmeasured, deferred. `internal/worker` in particular carries 17 integration-tagged files and
  deserves its own triage pass.
- **The migration-file alternative to the vocabulary lockstep guards.** The 2026-08-26 entry
  proposes parsing the migration `.sql` for the CHECK constraint so the same exact-set assertion
  could run untagged. This slice makes those guards run in CI, which is the stronger claim (what the
  database actually has), so the alternative is no longer needed for coverage. It stays available if
  someone wants a default-lane sibling too; it is not a task this slice creates.
- **The manual Python lane itself.** Formally accepted as manual (section 4.4). That is a decision
  recorded, not an instance closed, and the item should say so.

## 9. Rejected alternatives

- **Run the whole tagged suite in CI with Docker-in-CI.** The item's original pricing, at about ten
  minutes with p4d image pulls. Superseded by the `services: postgres` mechanism for every package
  except perforce, and it drags a p4d dependency into a lane that does not need one.
- **Add the packages as a step inside `cli-integration`.** Cheaper in machine time, worse in signal:
  one clock over two lanes, and about 190 seconds serialized onto a 54-second lane.
- **Point the new job at `./...` with the integration tag.** That is `make test-integration`, which
  would pull p4d and need a Docker API. The package list must be explicit and must grow one slice at
  a time, each with its own measurement.
- **Give `pgdsn` the integration build tag.** Forecloses section 4.6's free win and would make the
  package empty in the default build, a shape nothing in this repo has today and therefore one whose
  `go build ./...` behaviour would have to be established rather than assumed.
- **Write a `go/parser` guard asserting that every security-property test is untagged.** The
  expensive fallback, and worse than usual here: "security-property" is not a syntactic property, so
  the guard would need a naming convention it would then be enforcing instead. The rule in section
  4.5 is a reading instruction, and that is the honest form for it.

## 10. Provenance of every number in this document

**Measured by the author: nothing.** No shell was available in the authoring session.

**Repeated from the tree, with attribution:**

- 54 s for `cli-integration` in CI - the backlog item's 2026-08-27 entry and ROADMAP.md.
- `internal/store`'s full integration lane at "roughly 200 seconds locally" - the item's 2026-08-28
  entry. Container mode.
- `internal/schedrunner` at "roughly 20 seconds locally" (item, 2026-08-28) and "~26s" (ROADMAP.md,
  Now tier). **These two disagree**; both are quoted rather than reconciled.
- `internal/api` at "about 9.5 minutes" (CLAUDE.md) and "~320-340s" (Makefile, `test-integration`
  comment). **These two disagree by nearly 2x**; see section 4.1, where the disagreement is part of
  the argument for exclusion.

**Verified by counting the tree:** every acquisition count and entry-point count in section 4.1's
table; the 23 migration files; the package clause of every `internal/store` test file; the import
list of `pgharness_integration_test.go`; the two `pg_stat_activity` sites; the 17 integration-tagged
files under `internal/worker`; the four `export_test.go` files and which three carry the tag.

**Re-derived by the author, arithmetic shown:** the CLI lane performs 25 database acquisitions in a
54-second job step that also compiles the package at the integration tag. Attributing 10 to 20
seconds of that to compilation leaves 1.4 to 1.8 seconds per acquisition, and 2.2 s is the ceiling
if compilation were free. Applying that band to the counted acquisitions gives section 4.1's ranges.
**The transfer is rough and its weakest assumption is that per-test work is comparable**: a CLI test
stands up an HTTP server and runs a bcrypt hash, where a schedrunner test does neither and some
`internal/store` index tests run heavier SQL. The band's floor and ceiling should be read as a
sanity check on the order of magnitude, not as a prediction. Note that the derivation lands
`internal/schedrunner` at about 17 to 26 seconds, which agrees with ROADMAP.md's 26 s and not with
the item's 20 s.

## 11. Where the item contradicts the tree or itself

Recorded so the item can be corrected rather than re-litigated:

1. **"the extraction is a file move rather than a redesign"** - the premise (no `internal/cli` or
   `internal/api` imports) is true and verified; the conclusion is too strong for the four reasons
   in section 4.2, of which the decisive one is that a `_test.go` file cannot be imported at all.
2. **"The packages that need p4d ... or a real gRPC agent still are not covered"** - no test needs
   a real external gRPC agent. `cmd/relay-server/grpc_admission_e2e_integration_test.go` runs a real
   listener and a real client in-process and needs only Postgres. p4d is the only true exclusion.
3. **`internal/schedrunner` at ~20 s (item) against ~26 s (ROADMAP.md)** - unreconciled; the
   author's independent derivation favours the roadmap.
4. **`internal/api` at ~9.5 min (CLAUDE.md) against ~320-340 s (Makefile)** - unreconciled, and
   load-bearing for the exclusion decision.
5. **Two entries dated 2026-09-03 and 2026-08-29 are both labelled "a tenth instance"**, and the
   2026-08-29 one is titled "TENTH" while the roadmap's summary calls the Python lane the ninth. The
   ordinals have drifted; the instances themselves are distinct and both real.
6. **The item's Summary still leads with the three originally named guards**, none of which this
   slice touches and none of which is any longer the strongest instance. The progress note should
   say the title now understates the item.
