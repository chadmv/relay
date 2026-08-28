---
date: 2026-08-28
topic: retry-bounds-and-budget-predicate
branch: claude/retries-unvalidated-budget-go-34419f
range: 9f57720..1bb0c71
---

# Session Retro: 2026-08-28 - Retry Bounds and the Budget Predicate

**TL;DR:** A user could ask relay to retry a failing task two billion times, and nothing anywhere
checked the number - so one small, well-formed request could tie up a worker indefinitely. This
session capped that number at 10, capped the companion "kill the task after N seconds" field at seven
days, and made the database statement that spends a retry refuse to spend one the task does not have.
The bug report also prescribed a fix for its second half, and that fix would have broken every failing
task in the system; the session caught it before writing any code and did the opposite of what was
asked. Closing the bug then turned out to break something else nobody had predicted, because relay
re-checks saved schedules against the current rules every time they run - so tightening a rule
silently disables schedules that were fine yesterday. That hazard is now documented and tested rather
than discovered in production, and one genuinely new bug the change introduced (a button reporting a
permanent failure as a temporary one) was found and fixed. The work is on a branch, fully verified,
not merged.

## Handoff

Branch `claude/retries-unvalidated-budget-go-34419f`, **34 commits, NOT pushed and NOT merged** - the
user deferred the Phase 5 decision to run this retro. Closes
[[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]] (now in `docs/backlog/closed/`).

`jobspec.Validate` gains `maxRetries = 10` and `maxTimeoutSeconds = 604800`, rejecting negatives on
both; nil and `0` both remain "no deadline". `IncrementTaskRetryCount` gains `AND retry_count <
retries` as a fourth predicate **and the Go gate `terminal && task.RetryCount < task.Retries` in
`handleTaskStatus` is kept byte-for-byte**. The item proposed the predicate as a REPLACEMENT for that
gate; the retry branch ends in an unconditional `return`, so a budget-exhausted task entering it gets
`ErrNoRows` and returns before `UpdateTaskStatus` - no `failed`, no `finished_at`, no
`FailDependentTasks`, no `RecomputeJobStatus`, no SSE - and `retries` defaults to 0, so that is every
failing task in the system. It would also have mis-counted, since `classifyStatusFenceRejection`
labels a still-`running` row `raced`.

`handleRunScheduledJobNow` now returns **400** for both a stored spec that fails validation and one
that fails to unmarshal, where both were 500. That was a live defect this change created:
`jobspec.Validate` is retroactive over `scheduled_jobs` rows (`schedrunner.fireOne` ->
`jobcreate.CreateJobFromSpec` -> `Validate`), run-now is the only interactive path that can explain
why a schedule stopped firing, and `relayclient.ErrorIsTransient` reads 5xx as transient. Neither the
spec nor the plan contained the string `run-now`.

The retroactivity itself ships as a documented hazard, per the user's gate decision to ship without
[[bug-2026-08-23-unfireable-schedule-is-invisible]] (which `ROADMAP.md` pairs with it):
`TestTickOnce_AStoredSpecOverTheBoundStopsFiringInvisibly_DocumentedHazard` plus a field-set tripwire
over `store.ScheduledJob` in the plain lane. **The PR body still owes the operator upgrade note** - a
stored spec over the bound stops firing silently on deploy - and the hardened detection query, which
exists only in the Phase 3 agent's report, not in any committed doc.

Gates at HEAD, the decisive ones run by the conductor: 22 packages green untagged, `go vet` and `go
vet -tags integration` clean, `-race` green in the `golang:1.26` container (zero races), integration
green across `internal/store` 102/102 (~200s), `worker`+`schedrunner`+`jobspec` 225/225,
`internal/cli` 186/186, `internal/api` 329/329 (~465s, no teardown stall), zero container leaks.
**`internal/store`'s lane holds half B's only discriminating test and `go-ci.yml` never runs it** -
appended as a seventh instance to [[idea-2026-08-23-integration-only-guards-ci-never-runs]].

**Next session starts at the Phase 5 merge decision.** `make` is not installed on this machine; every
`make` target must be run as its literal command.

## What Was Built

- **`internal/jobspec/jobspec.go`** (+70) - the two constants with their arguments as doc comments,
  and two checks appended to the existing per-task loop so command-form and duplicate-name errors keep
  the precedence they had.
- **`internal/store/query/tasks.sql`** plus regenerated `tasks.sql.go` - the fourth predicate, and a
  comment block recording that it changes no production rowcount today, why that is checkable rather
  than hopeful, and why the Go gate must stay.
- **`internal/api/scheduled_jobs.go`** (+31) - two status-code corrections on run-now, each carrying
  the `ErrorIsTransient` argument at the branch.
- **Seven new test files** (+938) - the jobspec boundary table (both directions on both constants,
  offender first and second), the store budget test (the only test that can isolate the predicate),
  the handler pair (ends-terminal-and-touches-no-counter, and retries-exactly-its-budget), the API and
  CLI wire tests, the run-now bounds test, and the schedrunner hazard test.
- **`internal/store/createtask_guard_test.go`** (+258) - the AST guard standing in for the declined
  CHECK constraint, with its statement list derived from the SQL rather than hardcoded.
- **`internal/schedrunner/scheduled_job_surface_test.go`** (+142) - the field-set tripwire, in the
  plain lane.
- **Docs** - spec (530 lines) and plan (2043 lines), committed at their phase boundaries.

## Key Decisions

- **Both halves, shipped alone, without the ROADMAP-paired sibling.** The user's call at the spec
  gate. The mitigation is a test that asserts the hazard plus an upgrade note, and the caps were
  chosen generously partly so the population of stored specs this can break is close to empty.
- **`maxRetries = 10`, argued DOWN rather than up.** There is no backoff anywhere on the retry path,
  so N retries against a contended resource are N immediate failures within seconds. A large N buys no
  waiting, only a faster burn. The comment says so, and says the instrument for contention is a
  reservation rather than a retry count.
- **`maxTimeoutSeconds = 604800` deliberately ABOVE `RELAY_TASK_MAX_ASSIGNMENT`'s 24h default**, so
  the independence of the two knobs is visible in the numbers. A cap below it would read as agreement
  and be maintained as if it were one.
- **Predicate AND Go gate, not either or** - see Handoff.
- **The DB CHECK constraint was declined**, because migrations run on startup: `ADD CONSTRAINT`
  refuses to boot the binary on exactly the population that has the bug, and `NOT VALID` converts that
  loud failure into a silently stuck task. Filed with the pre-existing-row decision as its subject.
- **The residual is documented, not hidden.** `Tasks` and `Commands` are still unbounded multipliers
  and `POST /v1/jobs` is unrated-limited, so the worst 1 MiB request still buys roughly 1.4M
  `task_logs` rows. README and the close note say the value is bounded, not that unbounded work is
  impossible.

## What Went Wrong and What Changes

**Ledger.** Every entry in `2026-08-27-cli-real-server-integration-lane` was already promoted, so none
is carried. Promoted lessons used this session, as evidence the homes are working:
[[feedback_autopilot_squash_merge_resync]]'s squash-orphan extension **fired within minutes of
starting this retro** - `git cat-file -e` reported the prior retro's end SHA present while `git
merge-base --is-ancestor` correctly said it was unreachable, and taking the blessed SHA would have
produced a wrong range; [[feedback_mutation_testing_needs_isolated_tree]]'s conductor-side extension
shaped the whole verify phase, since the mutating lens was forbidden and the fix round was held until
the test lane finished; [[reference_guard_inherits_mutation_shape]] **recurred twice**;
[[reference_match_the_instrument_to_the_claim]] recurred once, against the conductor;
[[feedback_verify_tree_not_subagent_claims]] was used after every agent;
[[feedback_backlog_proposal_not_contract]] and [[reference_accurate_item_wrong_remedy]] together made
the session's largest catch; [[reference_wrong_prose_is_the_dominant_defect]] and
[[reference_uniqueness_claim_is_about_the_complement]] both recurred;
[[reference_mutation_battery_needs_green_baseline]] and [[reference_mutation_proof_must_leave_a_test]]
were applied throughout. [[feedback_a_green_rerun_bounds_not_retires]] was not exercised - nothing
went red at any point.

- **The bug report's prescribed fix for half B would have been a severe regression, and the tell was
  inside the sentence prescribing it.** The item said to add the SQL predicate because "the existing
  `pgx.ErrNoRows` silent-drop branch already handles the rejection correctly with no restructuring".
  Reading that one clause against `handleTaskStatus` found an unconditional `return` ending the retry
  branch: every budget-exhausted failure would have stopped short of `UpdateTaskStatus`, and since
  `retries` defaults to 0 that is every failing task in the system. Caught by the spec agent at design
  time, before any code existed.
  -> No new process change - [[reference_accurate_item_wrong_remedy]] covers it exactly and it worked.
  Worth recording as its strongest instance yet: the diagnosis was correct in every particular and the
  remedy was catastrophic, which is precisely the combination that makes a well-written item dangerous.

- **Tightening a validator was retroactive over stored data, and only one of the two readers was
  found.** `jobspec.Validate` is not only an ingest check: `schedrunner.fireOne` re-validates stored
  `scheduled_jobs` specs on every fire. The spec and the plan both found that path and reasoned about
  it carefully. Neither contained the string `run-now`, and `handleRunScheduledJobNow` re-validates
  the same stored data on demand - mapping the new refusal to a 500 that `ErrorIsTransient` reads as
  retryable, on the only interactive surface that could have explained the failure. Two verify lenses
  found it independently.
  -> **What changes:** when adding or tightening a validation rule, enumerate every caller that
  re-validates STORED data, not just the ingest paths that write it. Writers are easy to find by
  following the type; readers are not, and a read path that re-validates turns a new rule into a
  retroactive one. Grep for the validator by name and read every call site, including those reaching
  it indirectly.
  (promoted to [[reference_tightening_a_validator_is_retroactive]])

- **"Changes no production rowcount" was true, and the change still broke two tests.** The new
  predicate's comment argues at length that it can never be the sole reason a row fails to match -
  correctly; the claim was independently re-verified twice. But `retryFixture.pending` created tasks
  with `Retries: 0`, so the predicate broke
  `TestRetryJobTasks_ReopenedRowFields_EpochIncrementsByExactlyOne` outright and would have hollowed
  out its sibling, which exists to isolate the epoch and worker predicates and would instead have
  passed on the budget alone - green with both of the predicates its own comment calls non-decorative.
  Both live in the lane CI never runs. The planner caught them by reading, before any code was written.
  -> **What changes:** a "no behaviour change in production" argument says nothing about test
  fixtures, which are free to hold values production cannot produce. When a change tightens a
  precondition, search the fixtures for values that would now be refused - separately from, and after,
  the production-reachability argument.
  (promoted to [[reference_no_production_change_still_breaks_fixtures]])

- **A programmatic text edit rewrote a whole file's line endings, and `git diff` hid it.** A fix-round
  agent applied `.replace('\n','\r\n')` to a string whose anchor line already ended `\r\n`, producing
  `\r\r\n`; git's lone-CR heuristic reclassified `README.md` as binary, autocrlf stopped normalizing,
  and a two-line change committed as 1845 insertions. It caught this from the diffstat and amended.
  Separately, the same round found that `git diff` normalizes LF churn away while `git status` still
  lists the files as modified - so "steps 2 and 3 produced identical file lists" reads as "nothing to
  revert" when there is something to revert.
  -> **What changes:** CLAUDE.md's CRLF guidance is scoped to `make generate`. This is not a `make
  generate` problem, it is a "this repo is CRLF and the tooling normalizes inconsistently" problem.
  After ANY programmatic edit to a tracked text file, check the diffstat against the size of the
  intended change and run `git ls-files --eol` before committing; and never conclude "nothing to
  revert" from `git diff` alone, because it and `git status` disagree by design here.
  (promoted to CLAUDE.md, new subsection "Line endings: this is a CRLF repo and the tooling
  normalizes inconsistently")

- **A dispatched agent stopped and reported one sentence instead of a result.** The integration lens
  returned "I'll wait for that notification before continuing with the next lanes" - waiting on a
  notification that was never coming, since it was itself the agent that runs the lanes. Recovered by
  confirming the tree was unmutated and resuming it with explicit foreground instructions.
  -> No process change - one-off. The recovery was correct and cost one round trip; checking the tree
  before resuming was the part that mattered.

- **The conductor's own verification instrument was inverted by shell semantics and reported six false
  positives.** Checking that the close note's wiki-links resolved, I wrote `ls docs/backlog/$s.md
  docs/backlog/closed/$s.md >/dev/null 2>&1` - which returns non-zero when EITHER path is missing, and
  every item exists in exactly one of the two directories. It reported all six links dangling,
  including one committed minutes earlier, which is the only reason I looked twice.
  -> No new process change - [[reference_match_the_instrument_to_the_claim]], recurring. The specific
  addition worth remembering: an existence check across two alternative locations needs `[ -f a ] || [
  -f b ]`, never a single command handed both paths.

## Recommended Backlog Items

All filed during the session; order carries no meaning.

- See [`bug-2026-08-27-no-backoff-between-a-failed-task-and-its-redispatch`](../backlog/bug-2026-08-27-no-backoff-between-a-failed-task-and-its-redispatch.md) - nothing waits between a failed task and its next dispatch, which is why the retries cap was argued down rather than up
- See [`idea-2026-08-27-db-check-constraint-on-retries-and-timeout-seconds`](../backlog/idea-2026-08-27-db-check-constraint-on-retries-and-timeout-seconds.md) - the column-level guarantee this slice declined, with the pre-existing-row decision as its subject
- See [`bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers`](../backlog/bug-2026-08-28-task-and-command-counts-are-unbounded-multipliers.md) - the measured residual: roughly 1.4M log rows from one 1 MiB request on an unrated-limited route
- See [`idea-2026-08-28-create-job-echoes-a-raw-internal-error-in-its-500-body`](../backlog/idea-2026-08-28-create-job-echoes-a-raw-internal-error-in-its-500-body.md) - filed with its own motivating repro refuted, because the shape survives the refutation
- See [`idea-2026-08-28-mcp-tool-schema-does-not-advertise-the-job-spec-bounds`](../backlog/idea-2026-08-28-mcp-tool-schema-does-not-advertise-the-job-spec-bounds.md) - an LLM client learns the new bounds only by being refused
- See [`idea-2026-08-23-integration-only-guards-ci-never-runs`](../backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md) - appended: the first case where a shipped security fix's only proof is CI-invisible

## Files Most Touched

| File | Why |
|---|---|
| `docs/superpowers/plans/...` (+2043) | The plan; refuted the spec four times, one of which would have shipped a broken test lane |
| `docs/superpowers/specs/...` (+530) | The spec; refuted the backlog item's own prescribed fix for half B |
| `internal/store/createtask_guard_test.go` (+258) | The stand-in for the declined CHECK; took three passes to stop being a spelling match |
| `internal/worker/handler_retry_budget_integration_test.go` (+187) | Pins the Go gate, including that a budget exhaustion touches no fence counter |
| `internal/jobspec/jobspec_bounds_test.go` (+148) | Both constants, both directions, offender first and second |
| `internal/schedrunner/scheduled_job_surface_test.go` (+142) | The field-set tripwire, moved to the plain lane so the sibling item's column reddens `go test ./...` |
| `internal/schedrunner/stored_spec_bounds_test.go` (+137) | The documented hazard; the only test in the tree that deliberately asserts a defect |
| `internal/api/scheduled_jobs_run_now_bounds_integration_test.go` (+137) | The regression test for the one live defect this change created |
| `internal/store/increment_task_retry_count_budget_integration_test.go` (+98) | The only test that can isolate the budget predicate, in the lane CI never runs |
| `internal/jobspec/jobspec.go` (+70) | The whole of half A, plus the two constants' arguments |
