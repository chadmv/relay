---
date: 2026-08-27
topic: cli-real-server-integration-lane
branch: claude/cli-tests-server-isolation-e66d38
range: e536f3e..2359ca5
---

# Session Retro: 2026-08-27 - CLI Real-Server Integration Lane

**TL;DR:** Every automated test for the `relay` command-line tool talked to a fake server whose
replies the tests themselves wrote. So if the real server changed the shape of a reply, the tool
would break and every test would still pass - which is exactly what had happened twice before. This
session built tests that talk to a real server backed by a real database, and added a continuous-
integration job that actually runs them, which the project had never had for tests of this kind.
Before any deliberately-introduced fault was tried, the new tests found two genuine bugs nobody
knew about: one had been printing an empty value instead of each task's command for three months,
and another was silently dropping seven fields from output the tool tells users is complete JSON.
Both are fixed. The work is open as a pull request, reviewed but not merged.

## Handoff

Branch `claude/cli-tests-server-isolation-e66d38`, 23 commits, **pushed as
[#157](https://github.com/chadmv/relay/pull/157), green on all three checks, NOT merged** - the
user is reviewing. Closes [[idea-2026-08-23-cli-tests-never-hit-real-server]] (now in
`docs/backlog/closed/`).

18 `//go:build integration` tests in `internal/cli` drive real `internal/api` handlers over real
HTTP against real Postgres. `newIntegrationDSN` (`pgharness_integration_test.go`) has two modes on
`RELAY_TEST_DATABASE_URL`: unset -> testcontainer per test (~40s); set -> one `CREATE`d
`relaytest_<hex>` database per test (~12s local, **4.55s in CI, not `(cached)`**). It imports
nothing from `internal/cli`/`internal/api` so extraction to a shared package is a file move.
`startRelayServer` (`relayharness_integration_test.go`) is `api.New(pool, q, broker, registry, nil,
0,0,0,0)` behind `httptest`; gRPC, dispatcher, schedrunner, watchdog and `webui` are deliberately
unwired.

`.github/workflows/go-ci.yml` gains job `cli-integration` using `services: postgres` (not
testcontainers), `timeout-minutes: 10` / `-timeout 480s`, deliberately distinct from the existing
15m/180s/900s so a Go panic and a job kill are distinguishable. **ACTION REQUIRED: a new job is not
automatically a required check.** Add `cli integration (real server)` to branch protection for
`main` or it can go red without blocking - which reproduces the closed item's own defect one level
up. A comment in the workflow says so.

Two live bugs the lane caught before any mutation was authored: `taskResp.Command []string
json:"command"` vs `toTaskResponse`'s `Commands json.RawMessage json:"commands"` (dead since
migration `000008_task_commands`, `relay get --json` showed `"command":null`); and `jobResp`
missing `labels`, `retry_count` and five list-enrichment fields - `--json` is a lossy re-encode
through a hand-written mirror, not a passthrough. Both fixed; `jobResp`/`taskResp` are now
field-for-field mirrors, `Commands` and `Labels` both `json.RawMessage`.

Guards: `require.JSONEq` against the server's raw body on both the detail and list paths (neither
is total alone - detail omits the four `omitempty` enrichment tags, list omits all 11 task tags;
the union is complete and both comments now say so). All **27** `jobResponse`/`taskResponse` tag
renames die; **9 survived** before the arity guards, including `created_at` -> `createdAt`, which
makes `relay list` print `0001-01-01 00:00` for every job with everything green.

Verification: `/code-review` + four parallel lenses, then **four** fix-and-reverify rounds. Gates at
HEAD: 21 packages green untagged, `go vet -tags integration ./...` clean, lane green in both modes
with identical per-test results, 5x flake green, 0 leaked `relaytest_` databases, `internal/api`
byte-identical to `origin/main`. The full `-p 1 ./...` suite was **not** run to completion - it
stalls in `internal/api` Docker teardown ([[bug-2026-08-26-integration-lane-times-out-on-docker-teardown]]).

**Next session starts at the merge decision on #157.** After that, ROADMAP `Now` needs a refresh; it
is stale and was not touched here.

## What Was Built

- **`internal/cli/pgharness_integration_test.go`** (+831) - `newIntegrationDSN` and its two modes,
  `assertNoConnectionTargetOverride` (rejects pgconn's whole connection-target query-key set),
  `assertDSNTargetsDatabase`, `dbNamePattern`, bounded teardown, and their own regression tests.
- **`internal/cli/relayharness_integration_test.go`** (+294) - `startRelayServer`, `testCtx`,
  `seedUserWithToken`, `seedWorker`, `seedLogRows`, `boundedCleanup`.
- **Six area test files** (+913) - workers delete (the four-deep 403/400/404/409 ladder), workers
  list, jobs, schedules, admin users, logs (a real 201-row page boundary plus the exact-multiple
  drain case).
- **`internal/cli/jobs.go`** (+84/-15) - the only production change. `taskResp.Commands`,
  `RetryCount`; `jobResp.Labels` and the six enrichment fields; `SubmittedByEmail` gains
  `omitempty` to match the server.
- **`.github/workflows/go-ci.yml`** (+56), **`Makefile`** `test-cli-integration`, **`CLAUDE.md`**
  routing rule.
- **Docs** - spec (735 lines) and plan (1785 lines) committed at their phase boundaries.

## Key Decisions

- **Two harness modes, not one.** testcontainers-only pays an image pull and a Ryuk reaper per CI
  job and inherits the unbounded-teardown hazard; `services: postgres`-only regresses local
  developers against every other integration package in the repo. The env-var switch gives both, and
  it is the mechanism that could let the rest of the integration suite run in CI - which is the
  standing [[idea-2026-08-23-integration-only-guards-ci-never-runs]].
- **The `commands` fix went IN the slice, sequenced RED-first.** The plan wrote the jobs round trip
  green with no `command` assertion (Task 5), then added only that assertion and observed it red
  against unfixed code (Task 6). A lane shipping beside a known live instance of its own defect
  class would have been a weak deliverable, and the real RED is stronger evidence than any authored
  mutation.
- **`jobResp` stays ONE struct** mirroring the server's one `jobResponse`, enrichment block and all.
  A separate CLI list type would need syncing with a server type that does not exist. Consequence
  accepted deliberately: `relay get --json` now emits `"total_tasks":0,"done_tasks":0` on a
  three-task job, because that is the server's actual body - a mirror that filled it in from
  `len(Tasks)` would look nicer and permanently lose the ability to show drift.
- **The lane sits BESIDE the `httptest` fixtures.** Most of `logs_test.go`'s 42 tests assert
  behaviour a real server cannot be made to produce (an empty page not reporting drained, a
  non-advancing cursor, a 500 on page 2, a stdout that rejects writes). Repointing them deletes
  coverage. The division is per-test, and CLAUDE.md now states the rule.

## What Went Wrong and What Changes

**Ledger.** Every entry in `2026-08-27-python-sdk-envelope-sweep` was already promoted, so none is
carried. Three recurred and are worth naming as evidence the homes are working:
[[reference_wrong_prose_is_the_dominant_defect]] **recurred in all four fix rounds** - the comment
attached to a correct fix was the defect every single time;
[[reference_match_the_instrument_to_the_claim]] **recurred four times on one number** (below);
[[reference_guard_inherits_mutation_shape]] **recurred as the session's most serious finding**.
Promoted lessons used: [[feedback_backlog_proposal_not_contract]] (five of the item's claims
refuted), [[reference_test_green_because_of_the_bug]], [[reference_uniqueness_claim_is_about_the_complement]],
[[feedback_verify_tree_not_subagent_claims]] (an agent reported "no commits made yet (not
requested)" when commits had been requested), [[reference_verify_the_mutation_applied]],
[[feedback_a_green_rerun_bounds_not_retires]], [[reference_mutation_proof_position]],
[[feedback_sweep_count_needs_its_axis]], and `docs/agent-team/README.md`'s fix-round rule, which was
vindicated four times over.

- **The lane's whole value arrived from a real defect, not a designed one - and the plan nearly
  filed it instead.** The item's acceptance asked for "a deliberately introduced response-shape
  change" as the discriminating mutation. What actually proved the lane was `relay get --json`
  printing `"command":null` from a real handler over real HTTP: a bug nobody authored to be caught,
  found at design time, with a RED nobody could have written by accident. The synthetic battery
  (M0/M1/M2) came later and proves something weaker - that the mechanism runs.
  -> **What changes:** when a slice builds a verification mechanism, hunt for a real live defect for
  it to catch and sequence that RED first, before authoring any synthetic mutation. A synthetic
  mutation proves the mechanism executes; a real bug proves it was needed. If no real defect can be
  found, say so explicitly - that is evidence about the mechanism's value, not a formality.
  (promoted to [[feedback_hunt_a_real_defect_for_a_new_mechanism]])

- **A total-equality guard was only as total as the fixture's field coverage.** `require.JSONEq`
  against the server's raw body was adopted precisely because per-key assertions fail *open* on the
  next field added. It could not catch `WorkerID` or `DependsOn` tag renames: both are `omitempty`,
  no fixture populated either, and **absent-key compares equal to absent-key regardless of tag
  name**. The fix agent found this itself while proving the guard, and had to seed a worker and a
  task dependency before the guard could see them.
  -> **What changes:** when a guard asserts two whole structures are equal, enumerate every
  `omitempty`/optional field on both sides and confirm the fixture populates it. An optional field
  absent on both sides is invisible to any equality comparison, so the guard silently covers less
  than its name claims. Prove it per-field with a mutation, not by reading.
  (promoted to [[reference_equality_guard_is_blind_to_absent_optional_fields]])

- **A control column in the mutation battery could not fail, and its green was reported as
  resilience.** Every mutation left `API-DEFAULT` green. That was not the default lane proving
  robust - `internal/api/api_test.go` is itself `//go:build integration`, so
  `go test ./internal/api/... -run TestListWorkers` reports `no tests to run`. The column was
  structurally incapable of going red, and the transcript read as though it had been tested.
  -> **What changes:** before recording any lane as a control column in a battery, prove it can go
  red at least once. A column that never fails is not evidence of anything, and labelling it green
  actively misleads. This extends the green-baseline rule from "the baseline must be green" to "each
  column must be capable of both colours".
  (promoted as an extension of [[reference_mutation_battery_needs_green_baseline]])

- **I fanned out a reading lens and a mutation-running lens at the same worktree simultaneously.**
  The reviewer caught the tester's live mutation mid-read - it saw `json:"submitted_byMUT"` on one
  field, then `json:"created_atMUT"` on another 90 seconds later - and correctly refused to trust
  any green gate from that tree. Recurrence of [[feedback_mutation_testing_needs_isolated_tree]],
  but the trigger was mine as conductor, not a subagent's.
  -> **What changes:** when dispatching parallel verify lenses, check whether any of them MUTATES
  the tree, and if so run it alone or give it a detached worktree. The existing memory is phrased
  from the mutating agent's point of view; the conductor is the one who creates the collision.
  (promoted as an extension of [[feedback_mutation_testing_needs_isolated_tree]])

- **The retro skill's own start-SHA validation passes on a squash-orphaned commit.** The prior
  retro's `range:` ends at `10595bd`. `git cat-file -e 10595bd^{commit}` succeeds, because the
  object still exists - but that branch was squash-merged as `e536f3e`, so it is **not an ancestor
  of HEAD**. Taking the blessed SHA would have produced `10595bd..HEAD` = 23 commits instead of 22,
  silently attributing the *previous* session's squashed work to this one.
  -> **What changes:** validate a recorded start SHA with `git merge-base --is-ancestor <sha> HEAD`,
  not `git cat-file -e`. Existence and reachability are different properties, and on any
  squash-merging repo the recorded end SHA is orphaned by construction.
  (promoted as an extension of [[feedback_autopilot_squash_merge_resync]])

- **The security fix inherited the demonstrated spelling instead of the defect's shape.** Round 1
  closed a HIGH where pgx's query string overrides the URL path, by asserting `cfg.Database`. But
  `pgconn`'s `parseURLSettings` ends in a blanket `for k, v := range parsedURL.Query() { settings[k]
  = v[0] }` - the *same* loop - so `?host=`, `?port=`, `?user=` and `?password=` beat the URL
  authority untouched. `RELAY_TEST_DATABASE_URL=...&host=<other>` would have sent `CREATE DATABASE`,
  `DROP ... WITH (FORCE)`, `store.Migrate`'s `m.Up()` and a never-expiring admin-token seed to
  another server while every check reported "postgres, as intended". Strictly worse than the finding
  it descended from. Recurrence of [[reference_guard_inherits_mutation_shape]], now fixed as an
  allow-list over the whole key set enumerated from `notRuntimeParams`.
  -> **What changes:** already covered by that memory. Worth one addition to how it is applied:
  when the defect is "a library overrides A with B", read the library's own code for the FULL set B
  can contain, and write the guard over that set - never over the member the reproduction used.

- **Four attempts at one count, all wrong, all by the same instrument.** The number of vacuous
  default-lane fixtures was reported as 19, then 29, then 42, then finally 51. Every wrong one
  grepped for `Encode(<cliType>{`; `logs_test.go`'s `fakeJobSnapshotServer` takes a `[]jobResp`
  **parameter** and routes nine call sites through one `Encode(bodies[i])` that no such grep can
  see. The correct number came from an AST walker. My own correction commit - whose subject was
  "state the count with its axis" - was one of the wrong ones.
  -> **What changes:** already [[reference_match_the_instrument_to_the_claim]], recurring. The
  specific addition, now written into CLAUDE.md beside the count: when a value travels through a
  function parameter, the shape to search for is the TYPE in any fixture position, not the call
  site. A grep over call sites cannot see indirection, and indirection is the normal case in test
  helpers.

- **An agent reported work complete while leaving it uncommitted, having been told to commit.** The
  round-2 engineer's report opened "No commits made yet (not requested)" and simultaneously claimed
  `git status --porcelain` was empty; four files were modified. Its battery table's "final: empty"
  row referred to mutation residue, not its own work.
  -> No process change - [[feedback_verify_tree_not_subagent_claims]] already covers it and it
  worked: I checked the tree, found the four files, reviewed and committed them myself.

## Recommended Backlog Items

All filed during the session; order carries no meaning.

- See [`idea-2026-08-27-cli-default-lane-fixtures-encode-through-their-own-decoder`](../backlog/idea-2026-08-27-cli-default-lane-fixtures-encode-through-their-own-decoder.md) - 51 fixture bodies encode through the CLI's own response structs, agreeing with the decoder by construction
- See [`bug-2026-08-27-cli-lane-leaks-test-databases-on-abnormal-exit`](../backlog/bug-2026-08-27-cli-lane-leaks-test-databases-on-abnormal-exit.md) - shared-service mode has no reaper; a killed run leaks a database carrying a never-expiring admin token
- See [`idea-2026-08-27-cli-lane-never-crosses-a-list-page-boundary`](../backlog/idea-2026-08-27-cli-lane-never-crosses-a-list-page-boundary.md) - `page[T].NextCursor` survives being renamed
- See [`idea-2026-08-23-integration-only-guards-ci-never-runs`](../backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md) - appended: a CI mechanism now exists, and `internal/api`'s default lane is structurally blind to its own handlers
- See [`bug-2026-08-27-api-rawjson-passes-null-where-rawobject-normalises`](../backlog/bug-2026-08-27-api-rawjson-passes-null-where-rawobject-normalises.md) - appended: a third client hit it, and `json.RawMessage` is now load-bearing in Go too

## Files Most Touched

| File | Why |
|---|---|
| `internal/cli/pgharness_integration_test.go` (+831) | The two-mode Postgres harness and its guards; grew ~270 lines across the fix rounds, mostly the connection-target guard |
| `internal/cli/jobs_integration_test.go` (+568) | The jobs area plus both JSONEq arity guards and the CREATED-column render test |
| `internal/cli/relayharness_integration_test.go` (+294) | `startRelayServer`, the seeding helpers, `testCtx`, `boundedCleanup` |
| `internal/cli/workers_delete_integration_test.go` (+100) | The four-deep refusal ladder; the item's stated first priority |
| `internal/cli/logs_integration_test.go` (+85) | The only test that crosses a real page boundary; its line-count assertion is the sole M2 killer |
| `internal/cli/jobs.go` (+84/-15) | The only production change - both live bugs |
| `.github/workflows/go-ci.yml` (+56) | The `cli-integration` job; the half that makes the lane matter |
| `Makefile` (+58/-8) | `test-cli-integration`, `-count=1`, and two rounds of correcting its own justification |
| `CLAUDE.md` (+7) | The routing rule and the fixture-vacuity count with its axis |
| `docs/superpowers/plans/...` (+1785) | The plan; refuted three spec claims before any code was written |
