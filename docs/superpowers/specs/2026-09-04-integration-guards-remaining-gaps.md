---
date: 2026-09-04
topic: integration-guards-remaining-gaps
item: docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md
status: draft, pending human review
---

# The rest of the integration-tag gap: every remaining package into a lane that runs, one stub widened, and the item closed

## Status of this document

Written by the TPM agent from tree evidence in the worktree
`D:/dev/relay/.claude/worktrees/now-c-integration-guards` at `origin/main`.

**Nothing in this document was measured by running it.** The task brief said "You have Bash:
measure what you can". Bash is not available in this session - every invocation returns
`No such tool available: Bash. Bash is disabled for this session, in subagents as well as here.`
So every figure below is one of three kinds, and section 10 labels each one individually:
counted in the tree by me, re-derived by arithmetic from somebody else's measurement, or quoted
with an attribution. The one class of number this slice most needs - what the new lanes actually
cost on a runner - can only come from the implementer, and section 6 makes that a precondition
rather than a nicety.

The brainstorming flow's one-question-at-a-time dialogue is not available to a subagent, so each
open design question was resolved against the tree and the evidence that settled it is recorded
beside the answer.

## 1. The slice, in one paragraph

Point every remaining integration-tagged package at a CI lane that runs it. `internal/worker`,
`internal/scheduler`, `internal/mcp` and `internal/agent` join the existing `pg-integration` job by
taking their database from `internal/testsupport/pgdsn` (or, for `internal/agent`, by needing no
service at all); `internal/api` gets its own `api-integration` job because it is on its own
timescale. Widen `internal/worker`'s default-lane `statusStubDB` so it captures the arguments it
currently discards, which retires the one instance whose remedy is not a CI job at all. Write the
refusal for `internal/agent/source/perforce` into its own test's comment, which is what the
project's rule asks for when the answer is no. Then **close the backlog item**, because after this
there is nothing left to append to it.

## 2. Why this closes rather than continues

The item's own Acceptance / Done When has two clauses, and both are one step from satisfied:

> - Each of the three named guards either has a default-lane sibling covering its core property, a
>   CI lane that runs it, or a written decision in the test's own comment saying why not.
> - The decision generalizes: a rule (in CLAUDE.md or the test-writing docs) states that a
>   security-property guard must not be integration-only without a recorded reason.

Clause two shipped on 2026-09-04 (`CLAUDE.md`, "A guard behind a build tag must be able to run").
Of clause one's three guards, **two are already satisfied and neither the item nor the roadmap says
so** - see section 3. The third is `internal/api/server_counters_realsocket_integration_test.go`,
and the only thing that satisfies it is `internal/api` in a lane.

Everything else the item carries is accretion: eleven appended instances over three months, each
one a real finding, filed onto a ticket whose headline stopped describing it around instance six.
That accretion is itself the problem to solve. A slice that puts four more packages in a lane and
leaves three out guarantees a twelfth instance next month under the same number. So the scope rule
for this slice is: **take every package the mechanism can reach, write the refusal for the one it
cannot, and close the item.** Section 8 states what that leaves behind, and it is deliberately a
short list of things that are not instances of this item at all.

## 3. What is no longer true, refuted against the tree

The item is 300 lines and parts of it are stale. Read once for content and once asking only what is
still true, these are the claims that failed.

**R1. Named guard 1 already has its default-lane sibling.** The item's Summary says
`TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` is "the sole proof that a stale-epoch chunk
is dropped before `broker.Publish`". It is not. `internal/worker/tasklog_fence_counter_test.go` is
**untagged** (`package worker`, no build constraint) and
`TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot` carries both halves of the property:
`assert.Equal(t, "", logged())` for the no-log-line half, whose message names the integration twin
outright ("Its integration twin is TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll; this is
the half CI can see"), and `require.Empty(t, published())` against a real `h.broker.Subscribe`
for the no-publish half. It works because `AppendTaskLog` is one `QueryRow` plus one `Scan`, so a
seven-line `store.DBTX` stub reaches the fence arm with no Postgres. This satisfies the item's own
first branch ("a default-lane sibling covering its core property") and nothing in the item records
it.

**R2. Named guard 2 is already in a CI lane.** `cmd/relay-server` joined `pg-integration` in
PR #201; `make test-pg-integration` names `./cmd/relay-server/...`, and
`grpc_admission_e2e_integration_test.go` takes its database from
`pgdsn` via `newTestPoolAndQueries` -> `newPgdsnPoolAndQueries`. Satisfied by the second branch.

**R3. There are no non-test files behind the tag, and the file count is 137, not 132.** The prior
spec's section 4.6 says "132 files in the module carry the tag, test and non-test together".
Measured today with two searches: `^//go:build .*integration` over `*.go` returns 137 files;
the same pattern over `*_test.go` returns the same 137. Every tagged file in the module is a test
file. Whether 132 was wrong or merely older, 137/137 is what the tree says now, and the "test and
non-test together" framing describes a population that does not exist.

**R4. The Makefile's `internal/api` runtime figure is stale and the CLAUDE.md one is live.** See
section 4.1. This is load-bearing: it is the reason the prior spec gave for excluding
`internal/api`, and it dissolves.

**R5. The prior spec's shared-harness claim that "a package that needs only Postgres joins
`pg-integration` by taking its database from `internal/testsupport/pgdsn`" was refuted by its own
commit** and has already been corrected in CLAUDE.md to require the Makefile edit too. The tree
still contains two live proofs that the correction was necessary and that the trap is easy to fall
into: `internal/scheduler/notify_statement_timeout_integration_test.go:43` and
`internal/api/statement_timeout_integration_test.go:46` **already call
`pgdsn.NewIntegrationDSN(t)`**, and neither package appears in any Makefile target CI invokes, so
both tests run only when a human runs them. Wiring to `pgdsn` joins no lane. Both edits are
required, every time.

**R6. The open question in section 6 of the brief is already answered and shipped.** "Is
`python/tests/integration/` ever meant to run automatically?" was settled as **no, formally
manual**, and the decision is recorded in two durable places rather than in a spec:
`python/tests/integration/conftest.py`'s `pytest_collection_modifyitems` docstring ("This lane is
accepted as manual, not automated. An assertion whose only home is here is not CI evidence; give
it a Go-side home instead where the property allows one.") and CLAUDE.md's second rider. Both are
present in the tree today. This slice re-verifies and records; it does not re-litigate. Section
4.6 states the answer for the record.

**Still true, verified:** the item's `errorMessageLogStream` stub-widening finding (section 4.5),
its p4d exclusion (section 4.4), and its sixth instance about `internal/api`'s whole handler
surface. `go test ./internal/api/... -run TestListWorkers` reports no tests to run because
`internal/api/api_test.go` line 1 is `//go:build integration`, and the file holding `newTestServer`
is that same file.

## 4. Decisions

### 4.0 The census, and the axis it is counted over

The item's own list is a mix of named guards and whole-surface claims. This is the census produced
independently, because a count is a claim about the complement and must name its instrument.

**Instrument.** `Grep` (ripgrep) for `^//go:build .*integration`, restricted by glob to
`*_test.go`, over the whole worktree, `head_limit: 0`. **Re-run on the tree rebased onto
`origin/main` at `f746118`: 139 files**, not the 137 counted when this section was first written -
`f1849cd` (#203) landed between the two counts and added one file each to `internal/api` and
`internal/agent/source/perforce`. Grouped by directory, and cross-referenced by hand against the
package lists hardcoded in `Makefile`'s `test-cli-integration` and `test-pg-integration` targets,
which were the only two integration lanes `.github/workflows/go-ci.yml` invoked at the time this
census was taken (before this slice's own commit added `test-api-integration`).

**Axis: integration-tagged test FILES, by package, by whether a CI lane named the package before
this slice's own commit.**

| Package | Tagged files | External service the harness needs | In a CI lane today |
| --- | --- | --- | --- |
| `internal/api` | 58 | Postgres only | **no** |
| `internal/store` | 30 | Postgres only | yes (`pg-integration`) |
| `internal/worker` | 17 | Postgres only | **no** |
| `internal/cli` | 11 | Postgres only | yes (`cli-integration`) |
| `internal/schedrunner` | 7 | Postgres only | yes (`pg-integration`) |
| `internal/scheduler` | 4 | Postgres only | **no** |
| `cmd/relay-server` | 4 | Postgres only | yes (`pg-integration`) |
| `internal/agent/source/perforce` | 4 | p4d container built from a Dockerfile, plus the `p4` binary on PATH | **no** |
| `internal/agent` | 2 | **none** | **no** |
| `internal/mcp` | 1 | Postgres only | **no** |
| `internal/testsupport/pgdsn` | 1 | Postgres only | yes (`pg-integration`) |

53 files are in a lane CI runs. **86 are not**, and 82 of those 86 need nothing CI cannot give
them. That 82 is the size of what remains, and it is the number this slice is about. (The two extra
files against the original 84/81 both landed in `f1849cd`: one in `internal/api`, which needs
nothing CI cannot give, and one in `internal/agent/source/perforce`, which does - so the delta is
+2 not-in-a-lane and +1 needs-nothing.)

**The service column was established by reading, not by the tag.** The lead handed over from
PR #201's review re-runs green: `testcontainers` appears in exactly six `*_test.go` files
(`internal/worker/handler_test.go`, `internal/scheduler/dispatch_test.go`,
`internal/mcp/mcp_integration_test.go`, `internal/api/testhelper_test.go`,
`internal/agent/source/perforce/p4d_container_test.go`, and a comment in
`cmd/relay-server/agent_subprocess_e2e_integration_test.go` warning against reintroducing it).
Each of the four candidate harnesses was then read in full. `internal/api`'s `newTestPool` /
`newTestQueries`, `internal/scheduler`'s `newTestStoreWithPool`, `internal/mcp`'s
`startRelayForMCP` and `internal/worker`'s `newTestStore` are byte-for-byte the same
`tcpostgres.Run` + `store.Migrate` + `pgxpool.New` shape `pgdsn.NewIntegrationDSN` replaces. None
of them shells out, looks up a binary on PATH, dials an address outside the process, or touches
the Docker API for anything but that one Postgres. `internal/mcp` and
`internal/api/server_counters_realsocket_integration_test.go` do open real listeners and real
loopback sockets, and both are in-process: `httptest.NewServer` and `net.Listen("tcp",
"127.0.0.1:0")`.

**Axes nobody enumerated, stated so the count is not read as more than it is:**

- **Test functions behind the tag.** Counted for some packages (below) and not as a module-wide
  total. A file count is not a test count.
- **Subtests.** `t.Run` bodies hold most of the assertions and no count here sees them.
- **Which tagged tests are security-property guards** in the CLAUDE.md rule's sense - an
  authorization check, an epoch or identity fence, an input bound, a sanitiser. That is the
  population the rule actually cares about, it is not a syntactic property, nobody has enumerated
  it, and this slice does not either. The rule is a reading instruction and remains one.
- **Assertions that would newly run.** Unknowable without running the lanes.

### 4.1 `internal/api`: IN, on its own job. Settling the two costs that differ by 2x

This is the single biggest decision in the slice, and the prior spec excluded `internal/api` on
exactly one argument: "its cost is not known within a factor of two, and the tree disagrees with
itself about it". Both figures are still in the tree:

- `CLAUDE.md`, under the single-test-run block: "The whole `internal/api` integration package runs
  about 9.5 minutes; a 600s timeout is inside its variance band and reports FAIL with no `--- FAIL`
  line beneath it. Use `-timeout 1800s`."
- `Makefile`, in `test-integration`'s comment: "every integration test spins up its own real
  Postgres container, so `internal/api` alone runs ~320-340s."

**The CLAUDE.md figure is right. The Makefile figure is stale.** Three independent reasons, in
order of weight:

1. **One of them carries a reproduction and the other carries nothing.** CLAUDE.md's number is
   attached to an observed event: somebody ran the package with a 600s timeout, it was killed
   inside its variance band, and the log reported FAIL with no `--- FAIL` line beneath it. That is
   a description of something that happened. The Makefile's number is an aside inside a comment
   about why the timeout is generous, with no incident attached. This project's own rule is that a
   reproduction outranks an argument.
2. **Re-derivation from a measured sibling lands on 9.3 minutes.** `docs/retros/2026-09-04-pg-integration-ci-lane.md`
   measured `internal/store` at **244.7s in container mode**. Counted in the tree,
   `internal/store` performs about 73 database acquisitions, giving **3.35 s per acquisition** in
   container mode. `internal/api` performs about 167 (counted below). 167 x 3.35 = **559s, or 9.3
   minutes**. Arithmetic, from somebody else's measurement and my count.
3. **The Makefile figure implies a per-acquisition rate below the measured one for a lighter
   package.** 340s over 167 acquisitions is 2.0 s per acquisition, against 3.35 measured for
   `internal/store`, whose tests issue a handful of queries where an `internal/api` test stands up
   an `api.Server`, seeds users through `bcrypt` at `MinCost`, and drives several HTTP requests.
   `internal/api` should be heavier per acquisition, not 40% lighter. The most likely explanation
   is that the comment is accurate for the commit it was written on: 340s at 3.35 s/acquisition
   corresponds to about 101 acquisitions, and the package has grown since.

**The honest caveat, which the implementer must close.** The two measured container rates in the
retro disagree with each other: `internal/store` 244.7s / 73 acquisitions = 3.35 s/acq, but
`internal/schedrunner` 24.9s / 14 acquisitions = 1.78 s/acq. Applying the full band to
`internal/api` gives 297s to 559s, which spans both tree figures and settles nothing by itself.
The argument above is that the store rate is the right analogue because `internal/api`'s per-test
work is at least as heavy as `internal/store`'s and much heavier than `internal/schedrunner`'s -
that is a judgement, not a measurement, and reason 1 is what carries the conclusion. **The
implementer measures container mode once at HEAD and reports the number**, which turns this
paragraph into a fact and lets the stale Makefile comment be corrected with evidence.

**Cost in the mode CI actually uses.** Shared-service mode removes the per-acquisition container
start. From the retro's measured pair - `internal/store` 244.7s container against about 75s shared,
`internal/schedrunner` 24.9s against 9.5s - the shared-service rate is 0.68 to 1.03 s per
acquisition and the container overhead removed is 1.1 to 2.3 s per acquisition. For 167
acquisitions that is **114s to 172s**, and the subtract-the-overhead route from the 559s container
figure gives 559 - (167 x 2.32) = **172s**. Two routes, both landing near **3 minutes alone**.

**Why its own job and not four more packages on `test-pg-integration`.** Three reasons:

1. **`pg-integration`'s timeout comment reasons explicitly about its package set**, and
   `internal/api` alone is about 1.8x the acquisition count of the entire current lane. Adding it
   there does not fit inside that argument; it replaces it. Section 4.2 already has to update that
   comment's numbers for four smaller packages, and doing so is defensible. Adding the single
   largest package on top would make the coupling argument something nobody has measured.
2. **Two jobs run concurrently on separate runners, so the wall clock is the max and not the sum,
   and a red result names one lane.** That is the reasoning `cli-integration`'s and
   `pg-integration`'s own comments already give for being separate jobs, twice. `internal/api` has
   at least as good a claim to it as either.
3. **`internal/api` brings one genuinely heavy test to a shared server.**
   `statement_timeout_integration_test.go` inserts 100,000 rows with `generate_series`, runs
   `ANALYZE jobs`, and then drives unindexable `strpos` scans against a 50ms budget. On its own
   Postgres that is one test's problem. Sharing a server with eight other packages' tests, it is
   everybody's, and the question of how much it perturbs them is one nobody would have an answer
   for. Its own service removes the question instead of answering it.

**The rewire is two function bodies.** `internal/api/testhelper_test.go` holds `newTestPool` and
`newTestQueries`; both become `pgxpool.New` over `pgdsn.NewIntegrationDSN(t)` with their own
`store.Migrate` call dropped, because the harness has already run it. The 166 call sites are
untouched, `newTestServer` and `newTestServerWithBroker` are untouched, and
`installFailDeleteTrigger` needs no change: it creates a function and a trigger, both of which are
per-database objects, and the shared-service mode's unit of isolation is the database.

**Acquisition count, counted.** Grep for `newTestServer\(t\)|newTestServerWithBroker\(t\)|newTestPool\(t\)|newTestQueries\(t\)`
over `internal/api` returns 168 matches across 30 files; two of those are the definition bodies of
`newTestServer` and `newTestServerWithBroker` calling down into `newTestPool`, so the net is 166
call sites, plus one direct `pgdsn.NewIntegrationDSN(t)` in `statement_timeout_integration_test.go`:
**about 167**. The count is over CALL SITES, not executions - a call inside a loop or a subtest
closure runs more than once, so 167 is a lower bound on acquisitions and an exact count of sites.

### 4.2 `internal/worker`, `internal/scheduler`, `internal/mcp`: IN, on `pg-integration`

All three are Postgres-only (section 4.0) and each is a one-function rewire.

| Package | Tagged test funcs | Acquisitions (call sites) | Entry point to rewire |
| --- | --- | --- | --- |
| `internal/worker` | 89 | 70 | `newTestStore` in `handler_test.go` |
| `internal/scheduler` | 16 | 17 | `newTestStoreWithPool` in `dispatch_test.go` |
| `internal/mcp` | 8 | 8 | `startRelayForMCP` in `mcp_integration_test.go` |

Notes the implementer will hit:

- **`internal/scheduler` is half-done already.** `notify_statement_timeout_integration_test.go`
  takes `pgdsn.NewIntegrationDSN(t)` today; `dispatch_test.go`'s `newTestStoreWithPool` is the only
  `tcpostgres.Run` left, and `newTestPoolFromQueries` is a two-line wrapper over it that needs no
  change.
- **`internal/mcp`'s harness returns a `teardown func()` rather than using `t.Cleanup`**, and that
  teardown currently calls `pg.Terminate`. `pgdsn` owns the database's teardown through `t.Cleanup`,
  so the container termination must come out of `teardown` entirely rather than be reworded; what
  stays is `httpSrv.Close()` and `pool.Close()`. Use `pgdsn.BoundedCleanup` for the `pool.Close`.
- **`internal/worker`'s harness is `package worker_test`**, so the `pgdsn` import lands in the
  external test package. No cycle: `pgdsn` imports only `relay/internal/store`, and `store` imports
  no `relay/internal/...` package at all.
- **`internal/mcp`'s harness is `package mcp`, an in-package test.** Also no cycle, for the same
  reason. `pgdsn`'s doc comment names the one arrangement Go rejects (a `package store` test
  importing it) and none of these four is that.

**Timing risk, named up front.** `internal/worker` contains the timing-sensitive tests in this set:
`handler_watchdog_e2e_integration_test.go`, `handler_preparing_watchdog_integration_test.go` and
`handler_teardown_test.go` all reason about grace windows and watchdog margins. Under a lane where
eight packages hammer one Postgres, a deadline that was generous on an idle container may not be.
Section 6 requires repeated runs specifically because of this.

### 4.3 `internal/agent`: IN, and it needs no service at all

This is a new instance, found by the census this slice was required to produce, and it is the
cheapest thing left in the tree. `internal/agent` has two integration-tagged files and **neither
touches a database, a container, or anything outside the process**:

- `agent_test.go` (`package agent_test`, 3 tests) stands up an in-process `fakeCoord` gRPC server
  on a real loopback listener and drives a real `agent.Agent` against it. The task command it runs
  is `echo` (`cmd /c echo` on Windows). No `testcontainers` import, no `store` import.
- `runner_cancel_integration_test.go` (`package agent`, 1 test)
  `TestRunner_TreeKill_RealSubprocesses` spawns `sh -c "sleep 60 & echo $! > file; wait"` and
  asserts the grandchild is gone within 2s of cancel. Real subprocesses, nothing else.

That makes it a step-1 case under CLAUDE.md's triage ("Does it need the tag at all?"), and the
answer is arguably no. **This slice does not untag them**, and the reason is a measurement nobody
has: `make test` is documented as needing no Docker and runs with `-timeout 120s`, and the race
lane runs with `-timeout 180s`. Moving four tests that spawn real subprocesses and a real gRPC
listener into both of those, one of which is `-short`-gated, is a change to the default lane's
character that should be made with a number in hand, not on the way past.

**Instead, add `./internal/agent` to `test-pg-integration`'s package list.** It costs zero database
acquisitions. It is in a Postgres lane because that is the job which runs integration-tagged tests
on an ubuntu runner, not because it needs the service, and the target's comment must say exactly
that so the next reader does not "fix" an apparent inconsistency.

**The package path must be `./internal/agent` and never `./internal/agent/...`.** The `/...` form
pulls in `internal/agent/source/perforce`, whose `startP4dContainer` calls `t.Skip` when `p4` is
missing or Docker is unreachable. On a GitHub runner both are true, so the tests would **skip
silently and the job would go green having run nothing** - a false green, which is worse than the
gap it appears to close. This is the single most likely way to get this slice wrong.

### 4.4 `internal/agent/source/perforce`: OUT by construction, with a written decision

The item's closing clause is correct here and this slice does not change it. `p4d_container_test.go`
needs three things a `services:` block cannot supply:

1. The `p4` client binary on PATH, because the code under test shells out to it.
2. A Docker **build** of `testdata/p4d`, not a pull: the request is
   `testcontainers.FromDockerfile{Context: "testdata/p4d"}`.
3. The Docker API, for that build and for the container.

Cost to bring it into CI: a workflow job with the Docker daemon available (which GitHub's
ubuntu runners do have), a `p4` client install step, and a 2-minute startup wait
(`wait.ForLog("p4d ready").WithStartupTimeout(2 * time.Minute)`) on top of the image build, on
every push, for 2 tests. That is a different project with a different justification, and it is not
this item's.

**What this slice does instead, per CLAUDE.md's step 3.** Add one sentence to
`startP4dContainer`'s doc comment naming what would have to exist for these tests to run in CI: a
job with a Docker daemon, a `p4` client install step, and an image build budget. That converts an
absence into a recorded decision, which is the third branch of the item's own acceptance criterion
and is what lets the item close honestly rather than by omission.

**And say the trap out loud in that same comment**, because it is the reason the exclusion has to
be explicit rather than merely true: these tests SKIP rather than FAIL when their dependencies are
missing, so adding this package to any existing lane produces a green job that ran nothing.

### 4.5 The `errorMessageLogStream` stub: still true, and the remedy is still stub-widening

Confirmed against the tree; the item's 2026-09-03 entry holds without amendment.

`internal/worker/handler.go:202` declares `const errorMessageLogStream = "stderr"`, and it is read
at two sites in `handleTaskStatus`'s terminal arm: as `Stream` in the `store.AppendTaskLogParams`
literal (line 1565) and as the stream argument to `h.publishTaskLog` on the success leg (line
1573). The only assertion on its value anywhere in the tree is
`internal/worker/handler_taskstatus_errormessage_integration_test.go:46`
(`assert.Equal(t, "stderr", rows[0].Stream)`), which is `//go:build integration`.

**Why no untagged test can observe it today.** The two untagged tests that drive `handleTaskStatus`
(`taskstatus_errormessage_test.go`) use `statusStubDB`, whose method signature is:

    func (d *statusStubDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row

The variadic arguments are discarded at the signature. It dispatches on the statement text and
counts calls (`appendCalls`), so a test can prove the append happened and can never see what was
appended. Mutating the constant to `"stdout"` changes no observable in the default lane. That is
structural, exactly as the item says.

**Remedy: capture the arguments.** `sqlc`'s generated `AppendTaskLog` passes them positionally
(`internal/store/tasks.sql.go:158-165`) in the order `TaskID, AssignmentEpoch, WorkerID,
MinFinishedAt, Stream, Content`, so `Stream` is `args[4]`. The stub records the argument slice for
the `AppendTaskLog` branch and a new untagged assertion in `taskstatus_errormessage_test.go` pins
it to `"stderr"`.

Two riders that make the guard worth what it costs:

- **Assert positionally against a discriminator, not by scanning for a string.** `Stream` and
  `Content` are both strings, and `Content` begins `"[prepare_failed] "`. A test that asserts "one
  of the args is stderr" passes on a transposition. Pin `args[4]`, and pin `args[5]` to the content
  in the same assertion so a swap of the two reddens.
- **Add the publish-side half too.** The success leg publishes with the same constant, and
  `internal/worker` already has the subscriber helper pattern (`fenceSubscribe` in
  `tasklog_fence_counter_test.go`) and a payload test that reads `got["stream"]`
  (`tasklog_payload_test.go`). Asserting the published stream costs three lines and closes the case
  where somebody replaces the constant at one of the two read sites with a literal - which the
  args-capture assertion alone would not catch.

This is not a CI-lane change and does not depend on any other part of this slice.

### 4.6 `python/tests/integration/`: answered, and the answer is on the record

**No. It is formally manual, and this was settled and shipped before this slice.** Recorded for
completeness because the brief asks the question:

The lane's `conftest.py` requires `RELAY_INTEGRATION=1`, a `RELAY_URL`, a `RELAY_TOKEN`, and its
own docstring requires "at least one online agent able to run the submitted task". That is a
topology requirement - relay-server plus a live relay-agent executing real subprocesses plus
Postgres, wired together - not a service-dependency one, and no `services:` block reaches it.
Separately, `.github/workflows/python.yml` filters both triggers on `paths: python/**`, so the
properties that lane uniquely proves - all of them properties of Go code - could not fire on the
commits that break them even if it were fully automated.

The binding consequence is already written in both places it needs to be: the `conftest.py`
docstring and CLAUDE.md's rider. **An assertion whose only home is `python/tests/integration/` is
not evidence.** A future slice must find a home in a lane that runs or do without and say so.
Nothing in this slice changes `python.yml`, the `RELAY_INTEGRATION` gate, or
`make python-test-integration`.

## 5. The lanes

### 5.1 `test-pg-integration` grows by four packages

    go test -tags integration -count=1 \
      ./internal/store/... ./internal/schedrunner/... ./internal/testsupport/... \
      ./cmd/relay-server/... ./internal/worker/... ./internal/scheduler/... \
      ./internal/mcp/... ./internal/agent \
      -timeout 600s

Unchanged and deliberately so: `-count=1`, for the reason `test-cli-integration`'s comment gives
(the cache key says nothing about whether a live TCP connection to Postgres succeeded); no `-p 1`,
for the reason this target's comment already gives, and because the 2026-09-04 review falsified
the diagnosis that `-p 1` would have addressed; `-timeout 600s`, per test binary.

Comment edits that are **required scope, not tidying**:

- The `-timeout 600s` / `timeout-minutes: 12` paragraph in `.github/workflows/go-ci.yml` says the
  budget "is coupled to those packages running concurrently" and says **four**. After this it is
  eight. The number and the new worst case (section 5.3) go in.
- The target's own comment says "the Postgres-only integration lanes". `./internal/agent` is in the
  list and uses no database. Say why, in one sentence, per section 4.3.
- The job's `name:` string enumerates its packages and must be updated.

### 5.2 A new `test-api-integration` target and `api-integration` job

    test-api-integration:
        go test -tags integration -count=1 ./internal/api/... -timeout 1800s

`-timeout 1800s` and not 600s, because the same target must work in **container mode** for a
developer with Docker and no `RELAY_TEST_DATABASE_URL`, where section 4.1 puts the package near
570s with a variance band wide enough that 600s has already killed it once. This is CLAUDE.md's own
prescription for this package and the target inherits it rather than re-deriving it.

The job mirrors `cli-integration` exactly: same `postgres:16` service with the same user, password,
database, port and `pg_isready` health options; the same
`RELAY_TEST_DATABASE_URL: postgres://relay:relay@127.0.0.1:5432/postgres?sslmode=disable` including
the literal `127.0.0.1` for the reason that variable's existing comment records;
`actions/checkout@v5`; `actions/setup-go@v6` with `go-version-file: go.mod` and `cache: true`; one
step running `make test-api-integration`. `timeout-minutes: 10`.

**One inversion to state in the job's comment so nobody "fixes" it.** In the other two jobs the Go
timeout is below the job timeout, so a Go panic and a GitHub kill name themselves. Here it is the
other way round (Go 1800s, job 600s), because the target's Go timeout is set for container mode
while the job runs in shared-service mode at roughly a third of that. The property that matters is
that the two are not EQUAL, and they are not: in CI the job kill always wins and is therefore
unambiguous, and locally the Go timeout is the correct one for the mode a human is in. Write that
down; it looks like a mistake and is not.

**Advisory, like every job in this repo.** One sentence saying so. `main` carries no branch
protection and no rulesets. Do not describe this or any other job as a merge gate.

### 5.3 What the new worst case is

The brief requires this explicitly. All inputs are labelled in section 10.

**`pg-integration`.** Acquisitions go from about 95 (store ~73, schedrunner ~14, cmd/relay-server 7,
pgdsn 1) to about **190** (+70 worker, +17 scheduler, +8 mcp, +0 agent). Roughly 2.0x.

- **Concurrent, which is the mode it runs in.** Measured by the conductor today: the current
  four-package lane runs 72-95s alone and 93-105s with a second concurrent copy of itself against
  the same Postgres - that is, roughly 2x total load costs +11% to +29% wall clock, because the
  work is spread over packages that run in parallel and Postgres is not the bottleneck at this
  scale. Doubling the lane's own acquisitions is approximately that second copy. New band:
  **93-125s of test time**, plus compilation for four more packages. Call it **2 to 2.5 minutes**.
- **Serialized, which is the case the job's timeout comment is actually about.** At the measured
  shared-service rate of 1.03 s per acquisition (`internal/store`, 75s / 73 acquisitions), 190
  acquisitions is 196s of test time; adding per-package compilation for nine packages at the
  10-20s per package the CLI lane implies gives **240-280s, about 4 to 4.7 minutes**.
- **Against `timeout-minutes: 12` (720s): 2.5x headroom at the serialized worst case**, 5x at the
  concurrent one. The 12-minute budget survives this slice. Its comment's stated coupling does not
  and must be rewritten with these numbers.

**`api-integration`.** About 167 acquisitions on its own runner and its own Postgres.

- Derived shared-service cost: **114s to 172s**, two routes converging near 172s (section 4.1).
  Call it **3 minutes**.
- Worst case allowing for a CI runner slower than the machine the rates were measured on, and for
  the 100k-row `statement_timeout` test: **5 to 6 minutes**.
- **Against `timeout-minutes: 10` (600s): about 1.7x headroom at that worst case.** That is the
  tightest margin anywhere in this slice and it is the number the implementer must confirm before
  merging. If the measured job comes in above 6 minutes, raise `timeout-minutes` rather than
  shipping a job that will be killed intermittently, and record the real number in the comment.

**Total CI wall clock is unchanged.** Jobs run concurrently on separate runners, so the file's wall
clock is the max over jobs, and the `test` job's `timeout-minutes: 15` remains the ceiling.

## 6. The RED-risk protocol

A lane CI has never run may contain tests that do not pass on a clean runner. The previous slice
learned three specific things the hard way; all three are conditions of this one.

### 6.1 Required measurements, in order, with real output in the PR body

1. **Baseline, container mode, at HEAD, before any edit**, for each of the five packages being
   moved. `RELAY_TEST_DATABASE_URL` unset. Record the `ok relay/... <seconds>` line per package.
   The `internal/api` figure settles section 4.1 and is the number that lets the stale Makefile
   comment be corrected with evidence rather than argument.
2. **Shared-service mode, after the rewire**, same five packages, against a Postgres the
   implementer starts. **Do not use the `relay-postgres` container other lanes depend on for a
   destructive run**, and do not stop it; start your own and leave anything you start running.
3. **`make test-pg-integration` and `make test-api-integration`, each at least seven times.** Not
   once. The previous slice shipped a lane that failed about a third of the time and reported one
   green run as evidence; a reviewer running it seven times found 5 green and 2 red. Report the
   full tally, and if any run is red report "N green, M red, cause unestablished" - never "flake".
4. **A control that should die.** The previous slice also produced seven uniformly red runs from a
   harness pointed at a container that no longer existed, which reads exactly like a failed fix.
   Before trusting a battery, run something that must pass and confirm it does. Uniform results
   across a battery are this project's broken-instrument signal.
5. **`make test-cli-integration`, `make test`, `make vet-integration`, `go build ./...`** after the
   rewires, because five packages' test files changed and one untagged test file
   (`taskstatus_errormessage_test.go`) gained assertions.

### 6.2 The lane-membership claim needs the right instrument

Reading the Makefile tells you which packages the target names. It does not tell you which tests
execute. The instrument that settles a lane-membership claim is

    go test -tags integration -list '.*' <the lane's own package pattern>

run before and after, with the counts recorded. That is how the previous slice's regression (a
guard moved OUT of a lane) was caught, and it is the check that catches the
`./internal/agent` vs `./internal/agent/...` mistake in section 4.3: with the wrong pattern, the
list includes `TestPerforce_E2E_*` and the job will silently skip them.

### 6.3 Known interference sites, and one that is genuinely absent

Shared-service mode keeps per-database isolation and loses server-wide isolation. The search for
server-wide state, run over all `*_test.go` in the module for
`pg_stat_activity|pg_locks|pg_blocking_pids|pg_database|current_setting|max_connections|pg_terminate|pg_get_constraintdef|pg_indexes|information_schema|ALTER SYSTEM|CREATE ROLE|CREATE DATABASE`,
returns **20 matches across 10 files, every one of them in `internal/store` or
`internal/schedrunner`** - packages already in the lane and already proven under it. **Zero matches
in `internal/api`, `internal/worker`, `internal/scheduler` or `internal/mcp`.** The interference
class the previous slice had to reason about does not exist for the packages this slice adds.
Re-run the search rather than trusting this sentence; a claim about a complement is checkable only
by searching for the shape, and the hit count belongs in the PR body.

Three residual axes that the search cannot see:

- **LISTEN/NOTIFY.** `internal/scheduler/notify_test.go` drives `pg_notify` and asserts a listener
  fires. Postgres delivers notifications only to sessions in the same database, so per-database
  isolation covers it - but this is the one cross-session mechanism in the added set and it is
  worth confirming rather than assuming, especially since this same test has a flake retro of its
  own (`docs/retros/2026-05-04-flaky-notify-listener-test.md`).
- **Connection count.** `postgres:16` defaults to 100 connections. Eight packages running
  concurrently, each test opening a `pgxpool` at its default size, is more concurrent pools than
  the lane has had. A red reading `too many clients already` means a pool is outliving its test;
  the fix is that pool, not the server's configuration.
- **Timing.** `internal/worker`'s watchdog, grace-window and teardown tests reason about deadlines
  that were generous on a dedicated container. Section 6.1's seven runs exist mostly for these,
  and for `TestRunner_TreeKill_RealSubprocesses`, whose 2-second post-cancel window is the tightest
  assertion being added to a loaded lane.

### 6.4 What to do when a lane is red

Diagnose direction first, because "pre-existing" and "caused by this slice" have opposite
resolutions and only step 6.1.1 can tell them apart.

- **Red in container mode at HEAD.** Pre-existing, and it is the item's own thesis made visible.
  Out of scope to fix. File it naming the test and the failure, note it in the PR, and **exclude
  that package from the new lane** until it is fixed. Shipping a job known to be red teaches a
  reader to ignore it, which hides the next real red.
- **Green in container mode, red in shared-service mode, cause inside the test's own setup** (an
  assumption that it is alone on its server). Fix it here. Scope the query; do not serialize the
  lane. `-p 1` is not a remedy for a shared-server interference symptom - it changes the rate of
  statements, not the condition, and the 2026-09-04 review measured that directly.
- **Green in container mode, red in shared-service mode, cause anywhere else.** Exclude that
  package, file a specific item naming the test and the interference, ship the rest. **The item
  then does NOT close** - it stays open with that package named, which is a different and worse
  outcome than closing, and is worth saying so in the PR.

Shipping all five packages is not a success criterion. Shipping lanes that are green and say
something is.

## 7. Acceptance criteria

1. `internal/api/testhelper_test.go`'s `newTestPool` and `newTestQueries` take their database from
   `pgdsn.NewIntegrationDSN`; no `tcpostgres.Run` remains in `internal/api`.
2. The same for `internal/worker/handler_test.go`, `internal/scheduler/dispatch_test.go` and
   `internal/mcp/mcp_integration_test.go`; no `tcpostgres.Run` remains in any of the three. The
   only `testcontainers` import left in a `*_test.go` in the module is
   `internal/agent/source/perforce/p4d_container_test.go`, and the check for that is the same grep
   section 4.0 used, re-run, with the count in the PR body.
3. `make test-pg-integration` names `./internal/worker/... ./internal/scheduler/... ./internal/mcp/...`
   and **`./internal/agent`** (exact package, never `/...`), and `go test -tags integration -list`
   against the target's pattern lists no `TestPerforce_E2E_*`.
4. `make test-api-integration` exists, is in `.PHONY`, is documented in CLAUDE.md's Commands block,
   and `.github/workflows/go-ci.yml` has an `api-integration` job that invokes it.
5. Both jobs are green, with their run times in the PR body against section 5.3's derived numbers.
   A measurement outside the derived band is not a failure but must be reported with the number, so
   the comments carry a real figure.
6. `pg-integration`'s `timeout-minutes` comment names the new package count and the new worst case;
   the target's comment says why `./internal/agent` is in a Postgres lane; the job's `name:` string
   is updated.
7. The Makefile's `~320-340s` figure for `internal/api` is corrected with the measured number from
   step 6.1.1, or deleted in favour of a pointer to CLAUDE.md's figure.
8. `internal/worker`'s `statusStubDB` records the `AppendTaskLog` argument slice; an untagged
   assertion in `taskstatus_errormessage_test.go` pins `args[4]` to `"stderr"` and `args[5]` to the
   content, and a second pins the published stream. Mutating `errorMessageLogStream` to `"stdout"`
   reddens `go test ./internal/worker/...` with no tag, and the mutation-kill is demonstrated with
   real output.
9. `startP4dContainer`'s doc comment records the refusal: what would have to exist, and that these
   tests skip rather than fail so adding the package to a lane produces a false green.
10. CLAUDE.md's "A guard behind a build tag must be able to run" section lists three
    `services: postgres` jobs rather than two.
11. `make test`, `make vet-integration`, `make test-cli-integration` and `go build ./...` are
    green, with output in the PR body.
12. `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` is **closed via
    `/backlog close`**, which `git mv`s it into `docs/backlog/closed/`, stamps the frontmatter and
    appends a Resolution note. The Resolution note records section 3's refutations - in particular
    that two of the three originally named guards were already satisfied before this slice and the
    item never said so.

## 8. Scope boundary: what this slice does NOT do, and why the item closes

**Not in scope:**

- **Untagging `internal/agent`'s four tests.** Section 4.3: they need no service, so step 1 of the
  triage arguably applies, but moving real subprocesses and a real gRPC listener into `make test`
  and the race lane is a change to the default lane's character that needs a measurement first.
  They get a lane instead. If somebody later wants them untagged, the measurement to take is their
  contribution to `make test`'s 120s and the race lane's 180s.
- **A p4d CI lane.** Section 4.4. Out by construction, with the refusal written into the test.
- **Any change to `make test-integration`, `make test-race`, `make vet-integration`, or
  `python.yml`.**
- **Rewriting the default-lane fixtures.** The 51 vacuous fixture bodies in `internal/cli` are a
  separate open concern with its own item and nothing here touches them.
- **The migration-file alternative to the vocabulary lockstep guards** (the item's 2026-08-26
  entry). Those guards now run in CI, which is the stronger claim - what the database actually has,
  not what the repo says it should have. The alternative stays available and is not a task this
  slice creates.
- **`internal/api`'s counter-payload guards proven against fixtures rather than producers**
  (`idea-2026-08-24-counter-payload-guards-check-fixtures-not-producers`). That item's own text
  says it plainly: fixing the CI lane makes integration-tagged guards run and still leaves those
  predicates checking fixtures. Different gap, still open, untouched.

**The item closes.** After this slice every integration-tagged test in the module is in a lane CI
runs, except **four files** (`p4d_container_test.go`, `perforce_integration_test.go`,
`perforce_remap_integration_test.go` and `perforce_exclusion_integration_test.go`, five
`TestPerforce_E2E_*` funcs between them) in `internal/agent/source/perforce`, whose refusal is
written in their own test's comment - which is the item's own third branch, not an omission. Both
remaining clauses of
its Acceptance / Done When are satisfied and the generalizing rule is in CLAUDE.md with a third
job added to its list.

**Say plainly in the Resolution note that the title never described the item after instance six.**
It is called "Three of the admission/counters/log-fence security guards run only under the
integration tag CI never executes". Two of those three were satisfied before this slice began and
nothing recorded it; the item's real subject for its last two months was "most of this module's
integration tests are invisible to CI". An item that grows eleven appended instances under a title
about three named tests is an item that stopped being findable, and closing it is worth more than
the twelfth instance would have been.

## 9. Rejected alternatives

- **Add `internal/api` to `test-pg-integration` instead of giving it a job.** Cheaper in machine
  time by one runner and one Postgres. Rejected on section 4.1's three grounds, of which the
  decisive one is that it would put the single largest package on a clock whose budget argument was
  written for four small ones, requiring that argument to be re-derived rather than updated.
- **Exclude `internal/api` again, as the prior spec did.** Its stated reason was that the tree
  disagrees with itself about the cost by nearly 2x. Section 4.1 settles which figure is right, on
  a reproduction plus a re-derivation, so the reason no longer holds - and `internal/api` is the
  only thing standing between this item and closure.
- **Split this into two slices (api first, the three small packages later).** Rejected because the
  three small packages are one function body each and the mechanism is proven; deferring them
  creates a successor item that is this item renamed, and the accretion continues.
- **`-p 1` on the grown lane as insurance.** Rejected on measurement, not preference: the
  2026-09-04 review falsified the diagnosis `-p 1` was proposed for (200 create/drop cycles at
  concurrency 8 produced zero errors) and the real trigger was addressed structurally with
  `TEMPLATE template0`. `-p 1` lowers the rate of statements, not their count, and rate was not the
  variable. It would also convert the lane's concurrent 2-2.5 minutes into its serialized 4-4.7.
- **A `go/parser` guard asserting that no security-property test is integration-only.** The
  expensive fallback, and worse than usual here: "security-property" is not a syntactic property,
  so the guard would end up enforcing a naming convention it first had to invent. CLAUDE.md's rule
  is a reading instruction and that is the honest form for it.
- **Moving `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` or the other named guards.**
  Unnecessary. R1 and R2 in section 3: one has a default-lane sibling already and the other is
  already in a lane. The third is closed by putting its package in a lane, not by moving it.

## 10. Provenance of every number in this document

**Measured by the author, by running something: NOTHING.** Bash was unavailable for the whole
authoring session. Every runtime figure below is re-derived or quoted.

**Counted by me in the tree today, with the instrument named:**

- 137 integration-tagged files at first writing, and that all 137 were `_test.go` files - two
  `Grep` runs of `^//go:build .*integration`, one globbed to `*.go` and one to `*_test.go`,
  `head_limit: 0`. **Re-run on the rebased tree: 139, still all `_test.go`** - `f1849cd` added one
  each to `internal/api` and `internal/agent/source/perforce` between the two runs.
- The per-package file counts in section 4.0's table - the same result, grouped by directory,
  re-run and corrected the same way.
- 53 files in a CI lane and 86 not (was 84, before the re-run above) - the same result,
  cross-referenced by hand against the package lists in `Makefile`'s `test-cli-integration` and
  `test-pg-integration` as they stood before this slice's own commit.
- Acquisition counts, all over CALL SITES rather than executions: `internal/api` 168 raw matches
  minus 2 definition-internal, plus 1 direct `pgdsn` call, = ~167; `internal/worker` 70;
  `internal/scheduler` 18 raw minus 1 = 17; `internal/mcp` 8; `internal/store` 76 raw minus ~3
  definition-internal = ~73; `internal/schedrunner` 15 raw minus 1 = ~14; `cmd/relay-server` 8 raw
  minus 1 = 7; `internal/testsupport/pgdsn` 1; `internal/agent` 0. Search shapes are the harness
  entry-point names given per package in sections 4.1 and 4.2.
- Tagged test-function counts for `internal/worker` (89), `internal/scheduler` (16), `internal/mcp`
  (8), `internal/agent` (4) - `^func Test` per file, summed over that package's tagged files only.
- 23 migration files in `internal/store/migrations/`.
- 20 server-wide-catalog matches across 10 files, all in `internal/store` and `internal/schedrunner`
  - the search shape is quoted in full in section 6.3.
- 6 `*_test.go` files importing `testcontainers`, and which they are.
- `Stream` is `args[4]` in `AppendTaskLog` - read directly from
  `internal/store/tasks.sql.go:158-165`.

**Quoted from another document, with attribution:**

- `internal/store` 244.7s container / ~75s shared, `internal/schedrunner` 24.9s container / ~9.5s
  shared, `pgdsn` ~1.5s, about 1m10s wall - `docs/retros/2026-09-04-pg-integration-ci-lane.md`,
  measured by that slice.
- `pg-integration` at about 1m20s in CI - `ROADMAP.md`, Recently shipped.
- `cli-integration` at 54s in CI - the backlog item's 2026-08-27 entry and `ROADMAP.md`.
- The current four-package lane at 72-95s alone and 93-105s under a concurrent second copy -
  **supplied in the task brief as measured today by the conductor**. I did not run it and I do not
  know the machine, the mode, or whether the second copy shared the Postgres. Section 5.3 treats it
  as an elasticity measurement (2x load costs +11% to +29% wall clock) and that reading is the
  weakest link in section 5.3's arithmetic. **The implementer should re-run it rather than inherit
  it.**
- `internal/api` at about 9.5 minutes (CLAUDE.md) and ~320-340s (Makefile) - both quoted, and
  section 4.1 argues for the first over the second rather than reconciling them.
- The 5-green-2-red seven-run tally and the `SQLSTATE 55006` diagnosis - the same retro.

**Re-derived by me, arithmetic shown:**

- 3.35 s per acquisition in container mode = 244.7s / 73, from `internal/store`. 1.78 = 24.9 / 14,
  from `internal/schedrunner`. **These two disagree by 1.9x and section 4.1 says so rather than
  averaging them.**
- 1.03 s per acquisition in shared-service mode = 75 / 73. 0.68 = 9.5 / 14. Container overhead
  removed per acquisition: 2.32 and 1.10 respectively.
- `internal/api` container mode = 167 x 3.35 = **559s, 9.3 min**, which is the argument that
  CLAUDE.md's 9.5 minutes is the live figure. Using the schedrunner rate instead gives 297s, which
  is why section 4.1 does not rest on this alone.
- `internal/api` shared-service = 167 x [0.68, 1.03] = 114-172s, and 559 - (167 x 2.32) = 172s.
- New `pg-integration` acquisitions = 95 + 70 + 17 + 8 + 0 = **190**.
- New `pg-integration` serialized = 190 x 1.03 = 196s of test time, plus 10-20s per package
  compile for nine packages = **240-280s**.
- New `pg-integration` concurrent = the current 72-95s band scaled by the +11% to +29% elasticity =
  **93-125s**.
- 340s / 167 = 2.0 s per acquisition, the implied rate behind the Makefile figure, and 340 / 3.35 =
  101 acquisitions, the package size that figure would have been accurate for.

**The weakest assumptions, named so a reviewer can attack them rather than the conclusions:** that
per-acquisition cost transfers between packages whose per-test work differs (it does not, cleanly -
the two measured rates differ by 1.9x); that the conductor's 2x-load elasticity measurement
transfers from four packages to eight; and that a CI runner's rate resembles the developer machine
all the shared-service rates were measured on. Every one of these is retired by section 6.1's
measurements, and none of them changes a decision in section 4 - they change how much headroom
section 5.3 claims.

## 11. Where the item, the prior spec, or the tree contradicts itself

Recorded so it can be corrected in the Resolution note rather than re-litigated later.

1. **The item's Summary describes three named guards, two of which were already satisfied.** R1 and
   R2. Neither the item nor `ROADMAP.md` records it.
2. **CLAUDE.md's ~9.5 min against the Makefile's ~320-340s for `internal/api`.** Settled in section
   4.1 in favour of CLAUDE.md; the Makefile comment is corrected by acceptance criterion 7.
3. **The prior spec's "132 files carry the tag, test and non-test together".** 137, and all of them
   test files. R3.
4. **Two of the item's entries are both labelled "a tenth instance"**, dated 2026-08-29 and
   2026-09-03, and the 2026-08-29 one is titled "TENTH" while `ROADMAP.md`'s summary calls the
   Python lane the ninth. The ordinals drifted long ago; the instances are distinct and both real.
   Do not renumber them on the way out - just stop counting.
5. **The retro's two measured container rates disagree by 1.9x** (3.35 vs 1.78 s per acquisition),
   which is not a contradiction in the measurements but does mean neither is a general constant.
   Section 10 says so at every point one is used.
6. **`internal/scheduler` and `internal/api` each already call `pgdsn` from one test file while
   belonging to no lane.** Not a contradiction in the tree so much as a live demonstration that the
   two-edit requirement in CLAUDE.md is real, and worth citing there if that rule is ever
   questioned.
