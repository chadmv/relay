---
date: 2026-08-28
topic: unfireable-schedule-visibility
branch: claude/unfireable-schedule-invisible-7ba5fb
range: 77a847a..cb32c2f
---

# Session Retro: 2026-08-28 - Un-fireable Schedule Visibility

**TL;DR:** Relay lets you save a recurring job, and it re-checks that saved job against the current
rules every time it runs. So when a rule gets stricter, a schedule that was fine yesterday quietly
stops producing any work - and every screen still showed it as healthy, because nothing anywhere
recorded why it had stopped. This session gave schedules a place to record that failure and put it on
all five things that display a schedule. The interesting part was the review: the fix's own
"clear the error" rule turned out to re-create the same invisibility, because adjusting a schedule's
timing wiped a failure message that was still true. A separate AI-facing surface nobody had counted
was quietly handing attacker-writable text to an assistant that could delete schedules. Both were
found and fixed before the pull request; three smaller issues were written down for later.

## Handoff

Branch `claude/unfireable-schedule-invisible-7ba5fb`, **43 commits, PUSHED, PR #159 open, NOT
merged.** Closes [[bug-2026-08-23-unfireable-schedule-is-invisible]] (now in `docs/backlog/closed/`).

Migration `000022` adds `last_error TEXT NULL` / `last_error_at TIMESTAMPTZ NULL` (catalog-only, no
default, no CHECK, no backfill). `TickOnce` classifies `fireErr` **after** the savepoint rollback and
writes on the **outer** tx; `fireOne` hoists `jobspec.Validate` above the overlap check and wraps the
three permanent classes in `permanent()`. Wrapped pgx errors are logged only. `AdvanceScheduledJob`
clears; the skip path was split into `AdvanceScheduledJobSkipped`, which deliberately does not, since
it returns before `Validate` runs. `ValidateStoredSpecsOnStartup` is record-only, never clears, never
moves `next_run_at`, and is fenced on `job_spec`/`cron_expr`/`timezone` plus
`last_error IS DISTINCT FROM`, as `:execrows`. `handlePatchScheduledJob` clears only when
`schedrunner.ValidateStoredSchedule(jobSpecJSON, cronExpr, tz) == nil` - deliberately WITHOUT
`ValidateMinInterval`, which would make a legacy row under an older minimum permanently unclearable.

Emitted types are `LastError *string` (sqlc `emit_pointers_for_null_types`) and `LastErrorAt
pgtype.Timestamptz`. Five renderers, not four: SPA, CLI, Python SDK, MCP, served by `internal/api`.
The rune set for control and bidi stripping now has **three copies** by design -
`internal/schedrunner/failure.go`, `internal/cli/schedules.go`, `internal/relayclient/sanitize.go`
(applied in `Do`, the un-escaping site, which covers MCP for free) - each naming the others.

Gates at HEAD `cb32c2f`, all run by the conductor: 22 packages untagged, both vets, integration
`store 233s` / `schedrunner 26s` / `api 517s` (re-confirmed 26s/508s at final HEAD), the CLI lane,
vitest 1135, `tsc -b`, Playwright 54, `-race` green in `golang:1.26` with zero races, zero container
leaks. **CI runs none of the schedrunner or api integration tests** - eighth instance appended to
[[idea-2026-08-23-integration-only-guards-ci-never-runs]]. One `internal/api` red
(`TestCancelJob_NonOwner_404_NoSideEffects`) at 1-red/5-green, **cause unestablished**, excluded from
this diff by mechanism: no `t.Parallel()` in the package, per-test containers, the new file sorts
after it, and migration 000022 measures 1.4ms/1.1ms on a populated table.

**Next session starts at the PR #159 merge decision.** `make` is not installed; run every target as
its literal command. `python/.venv` does not exist in the worktree and the main repo's venv baked
`D:\dev\relay\python\src` into `sys.path` via a `.pth`, so a naive pytest run tests the WRONG TREE -
override `PYTHONPATH` and assert `relay.models.__file__` resolves under the worktree.
`@playwright/test` is installed here with `--no-save`, and `tsc -b` needs it.

## What Was Built

- **`internal/schedrunner/failure.go`** (+117) - `permanentFireError`, `recordableFailure`, and
  `sanitizeFailureText` (1 KB bound, rune-boundary truncation, never returns `""`).
- **`internal/schedrunner/runner.go`** (+111) - the outer-tx write site with the savepoint argument
  stated at the branch, the `Validate` hoist, and the four-way `advance*` split.
- **`internal/schedrunner/startup_validation.go`** (+170) - the record-only boot sweep and
  `ValidateStoredSchedule`, now shared with the PATCH handler.
- **`internal/store/`** - migration 000022 plus five statements, the sweep's fence, and the
  `UpdateScheduledJob` `CASE`.
- **`internal/api/scheduled_jobs.go`** (+87) - two response fields and the corrected clear predicate.
- **`internal/cli/schedules.go`** (+184) - `STATE` column, the failure block in `show`, `--spec FILE`,
  and `terminalSafeLine` over every server-supplied cell.
- **`internal/relayclient/sanitize.go`** - `sanitizeServerText`, applied in `Do`.
- **`internal/mcp/untrusted.go`** - one shared provenance label used by both the list/get surface and
  run-now's ToolError; `updateScheduleArgs` gains `job_spec`.
- **`web/src/schedules/`** - a `FAILING` chip inside the NAME cell rather than a tenth column, the
  `Last failure` panel, and a `schedules-failing` Playwright surface measured at 320/375/1280.
- **`python/src/relay/models.py`**, **README**, and **`cmd/relay-server/`**'s AST wiring guard.

## Key Decisions

- **Auto-disable rejected** (spec gate G1). It destroys operator intent in response to a change the
  SERVER made - the exact failure mode the item exists for - and once `enabled` is server-writable you
  need a second column recording who flipped it. Consequence: all six assertions in
  `_DocumentedHazard` stay TRUE, `Enabled` included, and none was flipped.
- **Only three permanent classes recorded.** A pgx error is not a fact about the schedule, and its
  text can carry constraint, column and connection detail into a field five clients render.
- **The write goes on the OUTER transaction.** Inside `fireOne` the savepoint rollback discards it
  *silently* - no error, no log line. Stated at the branch, in the SQL comment, and in the task text.
- **Content fence, not `updated_at`, for the sweep.** `ReconcileOnStartup` bumps `updated_at`
  immediately before the sweep in every process, so an `updated_at` fence would be suppressed by a
  sibling replica's reconcile during a rolling deploy - least reliable exactly when a retroactive
  validation change lands. The engineer refuted the conductor's framing of this as an even trade.
- **`ValidateMinInterval` excluded from the clear predicate.** Admission policy is not a fireability
  fact, and including it would make a legacy row permanently unclearable through PATCH.
- **MCP labelled rather than stripped**, and given the repair rung. A per-site fix, justified by a
  survey that found exactly ONE cross-tenant stored-prose site rather than "several", plus an explicit
  recommendation AGAINST a blanket label at `MapError`: 20 of its 21 sites carry fixed relay strings,
  so the label would have to weaken to something inaccurate everywhere in order to be automatic once.

## What Went Wrong and What Changes

**Ledger.** Every prior entry was promoted except two marked one-off, and **both of those recurred**,
so each returns below as a real bullet. Promoted lessons used this session, as evidence the homes are
working: [[feedback_autopilot_squash_merge_resync]]'s squash-orphan extension fired again minutes into
this retro (`git cat-file -e` said the prior end SHA existed while `--is-ancestor` said it was
unreachable); [[reference_tightening_a_validator_is_retroactive]] is the *subject* of this whole
slice; [[reference_accurate_item_wrong_remedy]] and [[feedback_backlog_proposal_not_contract]]
together produced the spec's four refutations; [[feedback_verify_tree_not_subagent_claims]] was used
after every agent and found nothing wrong, which is itself worth recording; CLAUDE.md's CRLF
subsection was applied at every commit; [[reference_uniqueness_claim_is_about_the_complement]]
recurred, since a test header's "only witness" claim went stale three commits after it was written,
inside this same PR; and [[reference_mutation_proof_must_leave_a_test]] and
[[reference_verify_the_mutation_applied]] were applied throughout - every mutation was grep-confirmed
as applied before its result was believed.

- **The fix's own clearing rule re-created the defect the fix exists to close.** `clearFailure` fired
  on key PRESENCE while its justifying comment required the inputs it did NOT touch to have been
  validated, so a PATCH of `cron_expr` alone erased a `job_spec` failure that was still true. An
  operator who saw FAILING, adjusted the timing, and got back a healthy-looking schedule is precisely
  the scenario the item was filed about. Three lenses confirmed it; one called it prose-only and was
  overturned by a live probe. Five prose sites repeated the same false "stale by construction" claim.
  -> **What changes:** when a slice adds a signal, review the code that CLEARS or RESETS it as its own
  surface, against the original defect's shape. The remedy path is where the defect comes back, and it
  is not usually in the diff's headline. (extends [[reference_backstop_recreates_the_defect]] from "a
  new read" to "any new clear, reset, or dismiss path".) (promoted to [[reference_backstop_recreates_the_defect]])

- **A fifth consumer of the new field existed and was invisible to every diff-based reviewer.** The
  disclosure argument was written four times for four renderers. `internal/mcp` decodes into
  `map[string]any`, so `last_error` reached an LLM holding `relay_delete_schedule` with no label, no
  code change, and nothing in the diff to notice - and `updateScheduleArgs` had no `job_spec`, so the
  model could read the failure but only disable or delete, never repair.
  -> **What changes:** when a slice adds a field to a response struct, enumerate consumers by
  searching for decoders of that RESPONSE - including `map[string]any` and generic-envelope
  passthroughs - not by reading the diff. A passthrough consumer acquires the field with a zero-line
  diff and is structurally invisible to diff review.
  (promoted to [[reference_passthrough_consumer_invisible_to_diff]])

- **A test's determinism rested on a harness property it did not assert, and failed OPEN.**
  `waitForBlockedScheduledJobsUpdate` inferred "the sweep's LIST already ran" from any backend blocked
  on `UPDATE scheduled_jobs`. That is sound only because each test gets its own container - which the
  file never asserted and does not own, and which `RELAY_TEST_DATABASE_URL` already breaks for the CLI
  lane. Under a shared database the wait returns early, the sweep reads an already-healthy row, and
  the test goes green having exercised none of the fence. Raised by its own author.
  -> **What changes:** when a concurrency test waits on observed global state, predicate the wait on
  something owned by that test - here `pg_blocking_pids` naming its own lock holder. A mutation kill
  is evidence about today's harness, not about the wait's robustness.
  (promoted to [[reference_concurrency_wait_must_be_owned]])

- **Testing that a classifier classifies is not testing that the caller routes on it.**
  `failure_test.go` proved `recordableFailure` rejects a pgx-shaped error; nothing proved `TickOnce`
  branched on the answer. A mutation making a database blip stamp garbage over an operator's real
  record survived `go test ./...`, the schedrunner lane AND the api lane. Of four near-identical
  `advance*` helpers it was the only one with no distinguishing test.
  -> **What changes:** when a slice adds a classifier plus a branch that consumes it, the classifier's
  unit test does not cover the branch. Write the test whose subject is the ROUTING, and mutate the
  call site rather than the predicate to prove it. (extends
  [[reference_added_a_property_forgot_its_guard]].) (promoted there)

- **A count in prose was wrong three times in one PR, twice by the conductor.** "four clients render"
  became an undercount the moment MCP was labelled. Separately, while correcting stale counts in
  `web/e2e/README.md` I used `grep -c 'population:'`, which also matched the interface's field
  declaration - so I "corrected" three numbers that had all been RIGHT and asserted a staleness that
  did not exist. Caught by a review lens, then re-corrected with the enumeration itself.
  -> **What changes:** prefer an enumeration to a count in any durable prose; it explains itself and
  goes stale loudly. When a count is unavoidable, derive it from the structure by parsing the entries,
  never from a substring frequency. (extends [[feedback_sweep_count_needs_its_axis]], and recurs
  [[reference_match_the_instrument_to_the_claim]], now four instances against the conductor.)
  (promoted to [[feedback_sweep_count_needs_its_axis]])

- **(CARRIED) A dispatched agent stopped mid-lane and reported a sentence instead of a result.** The
  prior retro logged this as a one-off. It recurred twice here, both times with a long integration run
  as the agent's final step, and once with the agent wrongly reporting its own completed run as never
  having finished.
  -> **What changes:** never let a subagent's last step be the decisive gate. The conductor runs the
  gate the merge decision depends on and treats an agent's green as a lead. This is the conclusion
  [[feedback_verify_tree_not_subagent_claims]] reaches for the tree, applied to lanes.
  (promoted there)

- **Two review lenses disagreed, and the disagreement was settled by evidence type rather than
  seniority.** The integration lens judged the clearFailure finding prose-only, reasoning that "the
  supplied value is always validated before storing"; the correctness lens had a failing probe. The
  error was subtle: the *supplied* value is validated, but the record may be about the input that was
  not supplied.
  -> **What changes:** when parallel reviewers disagree, rank a reproduction over an argument, and say
  which you took and why. Do not average the lenses, and do not default to the one that ran tests.
  (promoted to [[feedback_reproduction_outranks_argument]])

## Recommended Backlog Items

All filed during the session; the order carries no meaning (the Handoff names the entry point).

- See [`bug-2026-08-28-run-now-neither-clears-nor-records-the-failure`](../backlog/bug-2026-08-28-run-now-neither-clears-nor-records-the-failure.md) - the first rung of the documented remedy ladder is a no-op on the signal it points at, in both directions
- See [`bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener`](../backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md) - unbounded `SELECT *` on the boot path, with no schedule quota and no rate limit on create
- See [`bug-2026-08-28-schedrunner-logs-operator-controlled-schedule-names-raw`](../backlog/bug-2026-08-28-schedrunner-logs-operator-controlled-schedule-names-raw.md) - 13 log sites, one added by this slice and internally incoherent
- See [`idea-2026-08-23-integration-only-guards-ci-never-runs`](../backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md) - appended: the first instance where CI covers the READ half of a slice and not the WRITE half

## Files Most Touched

| File | Why |
|---|---|
| `docs/superpowers/plans/...` (+4020, two files) | The plan; refuted six spec claims, including `pgtype.Text` and an unsatisfiable Playwright design |
| `docs/superpowers/specs/...` (+760) | The spec; refuted four backlog-item claims, two of which changed the design |
| `internal/cli/schedules_test.go` (+549) | Every new fixture hand-written; zero vacuous bodies added to a 51-offender file |
| `internal/store/scheduled_jobs.sql.go` (+415) | Regenerated repeatedly; the CRLF revert verified by grepping for the new symbols each time |
| `internal/mcp/schedules_untrusted_test.go` (+363) | The fifth renderer's label, with 9/9 mutations killed and a surviving negative control |
| `internal/api/scheduled_jobs_failure_visibility_integration_test.go` (+360) | The headline regression test, in a lane CI does not run |
| `internal/schedrunner/startup_validation_fence_integration_test.go` (+254) | The multi-replica race, demonstrated on real Postgres; its wait hardened after the author flagged it |
| `cmd/relay-server/schedrunner_startup_wiring_test.go` (+206) | Pins the sweep's call site AND its ordering; the `go func(){}` mutation is why index order alone was not enough |
| `internal/schedrunner/failure_test.go` (+185) | The classification unit tests - which, alone, did not cover the routing |
| `internal/cli/schedules.go` (+184) | `STATE`, `--spec`, and the sanitizer the forge test forced onto every server-supplied cell |
