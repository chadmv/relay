---
title: Three of the admission/counters/log-fence security guards run only under the integration tag CI never executes
type: idea
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - integration-tester lens finding
---

# Three of the admission/counters/log-fence security guards run only under the integration tag CI never executes

## Summary
`.github/workflows/go-ci.yml` runs `make vet-integration` (`go vet -tags integration ./...` -
compiles, never runs) and `go test -race ./...` with no tag, so integration-tagged tests are
excluded from CI's build entirely. Three guards from the #139-#142 batch now depend on that gap
silently: `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll`
(`internal/worker/handler_ingest_budget_integration_test.go:458`) is the sole proof that a
stale-epoch chunk is dropped before `broker.Publish`; `cmd/relay-server/grpc_admission_e2e_integration_test.go`
is the only place main's real wiring runs end to end; and
`internal/api/server_counters_realsocket_integration_test.go` is the only test reading real
sockets back through the real admin route (the non-integration counters test feeds a
`fakeAdmissionSource`).

## Context
The gap itself is a known, accepted tradeoff - the closed
`idea-2026-06-20-vet-integration-tagged-build` item scoped itself away from running the suite in
CI. What changed is that security-property guards now live exclusively behind it, and this
project has already once found the epoch fence's drop-before-publish consequence guarded only in a
lane CI never runs (fixed in slice 3 by building a default-lane harness). This item is the
systematic version of that one-off fix.

## Proposal
Per guard, either add a non-Docker default-lane regression test for the property (the slice-3
precedent shows the harness often exists already), or stand up a Docker-capable CI lane for the
integration suite (cost: p4d image pulls, ~10min). A written per-guard decision is acceptable
where neither is worth it - what is not acceptable is the current silent state.

## Acceptance / Done When
- Each of the three named guards either has a default-lane sibling covering its core property, a
  CI lane that runs it, or a written decision in the test's own comment saying why not.
- The decision generalizes: a rule (in CLAUDE.md or the test-writing docs) states that a
  security-property guard must not be integration-only without a recorded reason.

## Related
- `.github/workflows/go-ci.yml`, `Makefile` (`vet-integration`)
- `docs/backlog/closed/idea-2026-06-20-vet-integration-tagged-build.md` - the accepted-tradeoff record this item revisits
- [[idea-2026-06-03-web-e2e-harness]] - the frontend half of the same verification-story gap

## 2026-08-24: a fourth instance, the sharpest yet, and this item's own remedy menu is incomplete for it

The finishregister-strand slice supplied a measured instance rather than another example.

`.github/workflows/go-ci.yml` runs `go test -race ./...` with no tags. In `internal/worker`, **every
test that drives a SUCCESSFUL worker registration is `//go:build integration`** - `handler_test.go`,
`handler_teardown_test.go`, and the new strand integration test. So the default lane structurally
cannot observe the registration success path at all.

What that cost, concretely: the slice needed to pin one line, `handedOff = true`, which decides which
of two deferred releases owns the worker generation. Deleting it left **all 21 packages green**, and
that mutant makes every successful registration mark its own worker offline, wipe its metrics entry,
and requeue a healthy agent's running tasks. Fleet-wide, on a green CI.

**This item's remedy menu does not cover the case.** The "add a default-lane sibling test" option is
unavailable here: `applyInventory` opens a transaction on the concrete `*pgxpool.Pool` unconditionally,
so a pool-less fixture panics before the success path is reached. The mechanical cause is filed
separately as [[idea-2026-08-24-handler-pool-has-no-seam]] - **the two are not duplicates**. Fixing the
CI lane makes the existing integration guards run; fixing the seam makes a default-lane behavioural
test possible. Either alone leaves the other gap open, and the seam item is the cheaper of the two.

The fallback the slice actually took - a `go/parser` guard in the default lane - is worth recording as
a data point on cost: it was **evaded twice** before it held, each time by a construct that is nil in
one context and real in the other (`h.Metrics != nil`, then `h.pool != nil` nested inside the guarded
branch). A behavioural test would have caught both on the first run. Treat a parser guard as the
expensive fallback it is, not as an equivalent substitute.

## 2026-08-24: the frontend twin was WORSE than the Go one, and it is now half-closed

This item was filed about Go guards behind `//go:build integration`. The frontend had the same disease
in a more advanced form and nobody had named it: **there was no web CI at all**. No workflow referenced
npm, node or `web/`, so `npm test` (1116 tests), `tsc -b` and `npm run build` had never run in CI on any
commit. The Go guards at least ran locally by convention; the frontend suite was advisory everywhere.

`.github/workflows/web-ci.yml` (2026-08-24) closes that half: unit tests, type-check, production build
and the browser suite now run on every PR.

**What this does NOT close, and the distinction matters for scoping:** the Go integration lane still
runs no tags in CI, which is this item's original subject and is untouched. Also note the new workflow
inherits a version-currency problem of its own -
[[idea-2026-08-24-web-ci-node-20-actions-and-unverified-node-version]].

One measurement worth carrying, because it prices the item: during the 2026-08-24 finishRegister slice a
line whose deletion left all 21 Go packages green had to be guarded by a `go/parser` test instead of a
behavioural one, and **that guard was evaded five times** before it held. The cost of a lane CI never
runs is not the missing signal alone; it is the elaborate and fragile substitute built in its place.

## Progress

- 2026-08-25: one named instance removed. `internal/worker`'s successful-registration
  path had every witness behind `//go:build integration`; narrowing `Handler.pool` to a
  one-method `txBeginner` interface put five behavioural tests in the default lane
  (`internal/worker/handler_register_success_test.go`) and let
  `handler_handoff_guard_test.go` shed five clauses. The item stays open - the remaining
  instances are untouched.

- 2026-08-26: a fifth instance, and it is **structural rather than a choice**. Both status-vocabulary
  lockstep guards - `TestTasksStatusVocabularyIsExactly` and the new `TestJobsStatusVocabularyIsExactly`
  (`internal/store/`) - are `//go:build integration`, so neither runs in `make test`. They cannot simply
  be moved: each reads `pg_get_constraintdef` from a live catalog, which needs a real Postgres. The
  jobs guard was added by `bug-2026-08-25-relay-logs-prints-nothing-envelope-drift` precisely because
  nothing pinned the jobs vocabulary at all, and writing it surfaced `terminalStatuses`
  (`internal/mcp/wait.go`) as an unregistered slicing site.

  This instance supplies a remedy the item's menu lacks: **parse the migration `.sql` for the CHECK
  constraint instead of querying the catalog.** The migration is the source of truth the catalog is
  built from, it is a file in the repo, and reading it needs no container - so the same exact-set
  assertion could run in the default lane. That trades "what the database actually has" for "what the
  repo says it should have", which is the weaker claim but the one that catches the drift this guard
  exists to catch.

## 2026-08-27: the first CI lane exists, and a sixth instance - `internal/api`'s default lane is structurally blind

Two updates from the CLI real-server integration lane slice
([[idea-2026-08-23-cli-tests-never-hit-real-server]], now closed).

**A mechanism now exists, and it is not the one this item priced.** The item's remedy menu offered
"stand up a Docker-capable CI lane for the integration suite (cost: p4d image pulls, ~10min)".
`.github/workflows/go-ci.yml` now has a `cli-integration` job that runs `internal/cli`'s
integration-tagged tests **without Docker-in-CI at all** - a `services: postgres` block plus a
harness that takes an externally-supplied DSN (`RELAY_TEST_DATABASE_URL`) and creates one database
per test, falling back to a testcontainer when the variable is unset. Measured: **54s in CI**,
against the 15-20 minutes a whole-suite testcontainers lane was priced at.

That is the generalisable half. Any package whose integration tests need only Postgres could join
the same job by pointing at the same harness; `newIntegrationDSN` was deliberately written to import
nothing from `internal/cli` or `internal/api` so the extraction is a file move rather than a
redesign. The packages that need **p4d** (`internal/agent/source/perforce`) or a real gRPC agent
still are not covered by this, and that distinction is what the original pricing conflated.

**A sixth instance, and the sharpest form yet: `internal/api`'s default lane cannot observe its own
handlers.** During the slice's mutation battery, every mutation of an `internal/api` handler left
`API-DEFAULT` green. That is not resilience - `internal/api/api_test.go`, which holds
`TestListWorkers` and effectively every test that drives a live server, is itself
`//go:build integration`. `go test ./internal/api/... -run TestListWorkers -v` reports
`testing: warning: no tests to run`. The handful of genuinely untagged files (`cors_test.go`,
`pagination_test.go`, `ratelimit_test.go`) are unit tests that never reach a handler.

So for `internal/api` the gap is not "some security guards are integration-only" - it is that the
**entire handler surface** is, and a reader looking at a green `go test ./...` has no signal about
it whatsoever. This is worse than the `internal/worker` instance recorded above (fixed 2026-08-25 by
the `txBeginner` seam), because there is no single seam to narrow: the tests need a real database,
not a mockable dependency. The `services: postgres` mechanism above is the plausible route.

Add to Related: `.github/workflows/go-ci.yml` (`cli-integration`),
`internal/cli/pgharness_integration_test.go` (`newIntegrationDSN`).

## Appended 2026-08-28 - a seventh instance, and the first where a SHIPPED SECURITY FIX has zero CI coverage

The retry-bounds slice (`bug-2026-08-12-retries-unvalidated-and-budget-only-in-go`) added
`AND retry_count < retries` to `IncrementTaskRetryCount`. The only test in the tree that can isolate
that predicate is `TestIncrementTaskRetryCount_BudgetPredicate_AnExhaustedTaskMovesZeroRows`, in
`internal/store`, under `//go:build integration`. `.github/workflows/go-ci.yml` runs `go test -race
./...` and `make test-cli-integration`, and nothing else - so **the predicate has no CI coverage at
all.** Its correctness rests entirely on a human remembering to run
`go test -tags integration -p 1 ./internal/store/...` locally, which needs Docker.

That is a step beyond the six instances above. Those are guards whose coverage is invisible; this is a
security fix whose proof is invisible. The distinction matters for prioritisation: a guard that
silently stops guarding degrades slowly, while a predicate that silently stops holding re-opens the
defect it closed.

**The same slice supplied direct evidence that reading is not a substitute for running it.** The
predicate BROKE two existing tests in that lane - `retryFixture.pending` created tasks with
`Retries: 0`, so `TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne` would have failed
outright, and its sibling rejection test would have gone silently vacuous, passing on the budget
predicate alone while claiming to isolate epoch and worker identity. The planner caught both by
reading, before any code was written. Nothing in CI would have caught either, and a `go test ./...`
run stays green through both.

`internal/store` is a strong candidate for the `services: postgres` mechanism: like `internal/cli` it
needs only Postgres, no Docker API, no p4d and no live agent. Its full integration lane runs in
roughly 200 seconds locally.

Add to Related: `internal/store/increment_task_retry_count_budget_integration_test.go`,
`internal/store/retry_job_tasks_integration_test.go`.

## Appended 2026-08-28 - an eighth instance, and the first where the UNCOVERED half is the WRITE path

From the Phase 4 review of [[bug-2026-08-23-unfireable-schedule-is-invisible]]. The slice that added
`scheduled_jobs.last_error` split cleanly into a write path (the schedrunner records a permanent fire
failure) and a read path (four clients render it). **CI runs the read half and not the write half**,
and the split is worth recording because it is not the usual shape.

The read half is genuinely covered. `internal/cli/schedules_failure_integration_test.go` runs in the
`cli-integration` CI job that the 2026-08-27 entry above describes, and `startRelayServer` gives it a
real `internal/api` server against a real Postgres - so column -> handler -> JSON -> client is proven
on every push. The slice's planner found this and corrected the spec, which had assumed the whole
thing was CI-invisible.

The write half has nothing. `internal/schedrunner`'s lane is integration-tagged and no CI job runs
it, so the headline regression test - a stored spec that stops validating, driven through a real
`TickOnce`, asserted visible via the API - is local-only. Its own comment says so.

**The concrete cost was measured during the same review, not predicted.** A reviewer mutated
`TickOnce`'s transient arm to call `advanceAfterFailure` instead of `advanceNextRun`, so a database
blip stamps garbage over an operator's real failure record - the exact behaviour README promises
cannot happen. It survived `go test ./...`, the schedrunner integration lane AND the api integration
lane, because no test existed for that branch at all. The gap was closed in the same PR, but note
what the sequence shows: the branch had no coverage, and no CI signal could have said so, because the
lane that would have carried the missing test is one CI never runs.

`internal/schedrunner` needs only Postgres - no Docker API, no p4d, no live agent - so it is a direct
candidate for the same `services: postgres` mechanism as `internal/cli` and `internal/store`. Its
full integration lane runs in roughly 20 seconds locally, the cheapest of the three.

Add to Related: `internal/schedrunner/stored_spec_bounds_test.go`,
`internal/schedrunner/startup_validation_integration_test.go`,
`internal/api/scheduled_jobs_failure_visibility_integration_test.go`.
