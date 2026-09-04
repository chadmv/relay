---
date: 2026-09-04
topic: preparing-task-status
branch: claude/pr-merging-session-65b658
range: 01d31792ca00b9a95a4d55e80ac02744e3f226e0..a777357eb77107f59912d8178afa320072181a97
---

# Session Retro: 2026-09-04 - Preparing Task Status

**TL;DR:** Before a relay worker can run a task it has to sync a Perforce workspace, which on a big
depot takes hours. The database had no word for that phase, so the task sat marked "dispatched" the
whole time and an operator could not tell a healthy sync from a hung agent. This session made
"preparing" a real status the database stores, and taught every consumer about it - the SQL, the
API, the command-line client, the web UI and the Python library. It shipped green on every test
lane. The review caught one real bug it had introduced on the way: the job detail page counted a
preparing task as neither running nor waiting, so a job whose tasks were all syncing displayed as
completely idle.

## Handoff

Autopilot iteration 1 of 4. Closed [[feature-2026-09-03-preparing-task-status]], the head of the
hand-placed fork-upstreaming batch in ROADMAP.md's Now. Sixteen commits: spec `3d0f5a4`, plan
`ac2ef1b`, ten implementation commits `ad9f290..bd707f0`, three fix-round commits
`c98e627`/`7e8dd18`/`30ee312`, then the close and backlog housekeeping.

Migration `000023` widens `tasks_status_check` to seven values AND widens the partial index
`idx_tasks_worker_active` in the same file - the fourteenth site, which the backlog item did not
name and which would have made the index unusable for all eight assignment-partition statements
including the dispatcher's per-cycle `CountActiveTasksByAllWorkers`. Thirteen predicates in
`internal/store/query/tasks.sql` gained `preparing`, including `AppendTaskLog`'s FIRST disjunction
arm only. `started_at` stays stamped at `running` only, preserving R3 of
`docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md`: stamping it at `preparing`
would start the execution watchdog's clock during the sync and sweep a healthy task mid-sync.
`RequeueTask` deliberately did NOT widen - its only caller fires when the `DispatchTask` was never
delivered, and `workerSender.Send`'s two error returns are select branches disjoint from the
enqueue, so the agent cannot have reported a status only a delivered dispatch produces.

Spec decision D5 accepts a backward `running -> preparing` transition at the same epoch by the same
assignee; the security lens established it is a second spelling of the already-accepted repeated-
`RUNNING` case, not a new capability, and that `started_at` is guarded TWICE (the handler carries
the row's value forward for every non-running status, and `UpdateTaskStatus` COALESCEs), so neither
watchdog arm is evadable. `TestWatchdog_ARunningThenPreparingTaskIsStillSweptByTheExecutionArm`
composes it.

Verify: four lenses plus five integration lanes (store 243s, worker 187s, scheduler 38s, api 657s,
cli 57s), all green; `-race` in the `golang:1.26` Linux container across all 21 packages, zero data
races; vitest 189 files / 1504 tests; `pytest python/tests/unit` 166; ruff and mypy clean. Three fix
rounds, and the third found no defect in the second's own diff, which is what established the
sequence terminated.

Two items filed: [[bug-2026-09-04-rolling-deploy-cancels-an-in-flight-sync]] (reproduced against a
real Postgres) and [[idea-2026-09-04-index-predicate-guard-is-blind-to-conjuncts]] (measured, a
blindness rather than a live regression). Three existing items were updated because this slice
falsified their quoted predicates.

Next session starts at ROADMAP.md's Now #2, the Perforce virtual and import+ remap streams bug.

## What Was Built

- **Migration `000023`**, up and down, widening the CHECK constraint and `idx_tasks_worker_active`
  together. The down migration demotes `preparing` rows to `dispatched` BEFORE narrowing the
  constraint - order is the correctness argument, and the test seeds a row precisely so it is
  observable. `dispatched` and not `pending`, because the row still has a live agent, a `worker_id`
  and an `assignment_epoch`, and demoting to `pending` would end a live assignment without bumping
  the epoch.
- **Thirteen SQL predicates** widened in `internal/store/query/tasks.sql`, with eight deliberately
  left narrow and each re-derived rather than assumed.
- **`handleTaskStatus`** gained the `TASK_STATUS_PREPARING` case, after both existing gates.
- **`taskStatusIsWritable`** and the hand-written cancel-signal collection in `internal/api/jobs.go`.
  The latter is the one path that enumerates statuses by hand rather than deriving them from a
  `RETURNING`, which is why it is also the path that could have been silently missed.
- **Clients**: the SPA's `TaskStatus` union, `taskStatusColor`, the job detail page's active count
  and default task selection; the Python SDK's enum member.
- **Guards**: an index-predicate test, a down-migration test, seven per-statement partition tests, a
  negative test for `RequeueTask`, two handler tests, a watchdog composition test, and a fixture
  table test for the vocabulary parser's own two new rungs.

## Key Decisions

- **`started_at` stays at `running`.** The fork this item came from stamps it at `preparing`, which
  starts the execution watchdog's clock during the sync. Upstream does not, and the absolute arm
  (`assigned_at`, `RELAY_TASK_MAX_ASSIGNMENT`) still bounds the assignment.
- **`RequeueTask` stays `'dispatched'`-only.** Widening a statement that ENDS an assignment to match
  a state meaning "the agent is working" is the fail-open direction.
- **The frontend and Python slices were folded into one lane, not run concurrently.** File sets were
  disjoint and the FE change was three lines, but a reviewer has to read the whole partition table
  against ONE diff.
- **The backward `running -> preparing` transition is accepted rather than blocked.** Blocking it
  would need a second writer of `tasks.status` against the watchdog slice's explicit discipline, and
  the security lens established the gain to a misbehaving assignee is a second spelling, not a
  second capability.

## What Went Wrong and What Changes

Ledger: every entry in the prior retro was promoted, so none are carried. Promoted lessons that
fired: [[feedback_verify_tree_not_subagent_claims]] (every agent report re-checked against the tree
before the next phase); [[feedback_assert_encoding_after_a_programmatic_edit]] (fired on the
conductor - see the third entry); [[reference_verify_the_mutation_applied]] (fired on an engineer,
whose first mutation attempt silently did not apply and reported all-green);
[[reference_uniqueness_claim_is_about_the_complement]] (the spec found the fourteenth site by
searching the shape rather than opening the thirteen named ones);
[[reference_a_kill_must_name_its_guard]] (the re-verify found the doubled-guard explanation attached
to an assertion the mutation never reaches, because a precondition fires first);
[[reference_a_guard_must_not_derive_its_expectation_from_its_subject]] (the index test's hard-coded
triple is correct for exactly this reason, and only its NAME overclaimed);
[[feedback_same_finding_across_parallel_lanes]] was exercised for the first time - the job-detail
regression was found independently by three of four lenses, and one lane was assigned the fix before
any fix round began.

- **The slice's only real regression was the one copy of the partition written by hand in another
  language.** Thirteen SQL predicates moved in lockstep under two lockstep guards and every one was
  correct. The defect that reached review was thirty characters of TypeScript - a filter admitting
  only `running` and `dispatched`, feeding a "N active" count - with no guard at all, in the state
  that is now the DOMINANT display state for a source-bearing job. The SQL comments enumerate this
  exact fail-open ("an operator sees an idle worker that is busy") nine times; the enumeration
  simply stopped at the language boundary.
  -> **What changes:** when a slice widens a set that a lockstep guard co-owns, enumerate the set's
  consumers per LANGUAGE before writing code, and treat the guarded copies as the safe ones. The
  guard proves the copies it knows about; the regression lands in the hand-written copy it cannot
  see. (promoted to [[reference_the_unguarded_copy_is_in_another_language]])

- **The commit that closed a fail-open shipped a comment saying the fail-open was still open.**
  `7e8dd18` made an unparseable migration a hard failure, and left the migration's own comment
  reading "a definition it cannot see is a definition it silently replaces with an older
  migration's" - false as of that same commit. Caught by the scoped re-verify, not by the round that
  wrote it.
  -> **What changes:** when a fix changes what happens on a FAILURE path, grep for prose describing
  the old failure path in the same commit, and start with the file that DOCUMENTED the hazard - the
  file that names a defect is the likeliest place to keep describing it after the defect is gone.
  (promoted: extends [[reference_wrong_prose_is_the_dominant_defect]])

- **The conductor's own backlog-close edit silently did nothing, and the skill's verify step is the
  only reason it was caught.** The replacement anchor ended in a bare line feed; the working-copy
  line ends in a carriage return plus line feed, so the replace matched nothing and appended a
  Resolution section to a file still marked open. The required marker count printed 1 where 4 was
  demanded.
  -> **What changes:** on this CRLF repo, read with universal newlines and write back with an
  explicit CRLF newline setting, and ASSERT the anchor is present and unique before replacing rather
  than letting a silent no-op through. A verify step that can print a wrong number is worth more
  than one that can only print "done". (already in
  [[feedback_assert_encoding_after_a_programmatic_edit]] - stamping the CRLF-anchor trigger so it
  stops being rediscovered)

- **A guard gained two new jobs and neither had a test, because both are INERT on the real input.**
  The vocabulary parser gained a fail-open check and an ordering assertion. On today's migrations
  directory zero files trip the first and the ordering is trivially satisfied, so all the evidence
  they worked lived in a reviewer's scratchpad and would have vanished with the session. The fix
  needed the helper to take its directory as a parameter before any fixture could reach it.
  -> **What changes:** when a new guard rung is inert against the repository's own current state,
  parameterizing its INPUT is part of the same slice, not a follow-up - otherwise the rung ships
  unexecuted and its only proof is a transcript. (promoted: extends
  [[reference_added_a_property_forgot_its_guard]])

- **A prescribed helper signature would have made the fix untestable, and the engineer refuted it.**
  The re-verify prescribed a helper still taking the test handle as its first parameter. A helper
  that calls the test framework's fatal ends the test, so a test cannot assert that a given input is
  REFUSED - which was the entire point of extracting it. The engineer returned a value-and-error
  pair instead and said so.
  -> **What changes:** when extracting a helper so that its FAILURES can be tested, it must return
  an error rather than call the test framework's fatal. A prescription that keeps the test handle in
  the signature cannot express "this input must be refused", so check the signature against the
  assertions the new tests need to make before prescribing it. (promoted to
  [[reference_extract_returns_an_error_not_a_fatal]])

- **A measured claim in a migration comment was wrong in the direction that understates an outage.**
  The comment justified skipping a concurrent index build with "the index is partial over the
  currently-assigned rows only, so the build is bounded by live work rather than by history". A
  partial index still scans the whole heap: measured 118 ms to 1028 ms across a tenfold history
  increase with live rows held at 50. Only the index's SIZE is bounded by live work. The comment
  also named the wrong lock and omitted the larger of the two costs.
  -> **What changes:** a performance or lock claim written to justify a decision is the claim most
  worth measuring, because it is the one a reader will rely on instead of re-deriving. Measure it or
  delete it; do not reason it. (already in [[reference_wrong_prose_is_the_dominant_defect]] - the
  "cost or mechanism claim is a measurement with an expiry date" bullet)

## Recommended Backlog Items

Backlog intake, not a priority order - ROADMAP.md orders the work.

- See [`bug-2026-09-04-rolling-deploy-cancels-an-in-flight-sync`](../backlog/bug-2026-09-04-rolling-deploy-cancels-an-in-flight-sync.md) - a rolling multi-replica deploy tells an agent to cancel its in-flight workspace sync
- See [`idea-2026-09-04-index-predicate-guard-is-blind-to-conjuncts`](../backlog/idea-2026-09-04-index-predicate-guard-is-blind-to-conjuncts.md) - the index predicate guard only reads quoted literals

## Files Most Touched

- `internal/store/query/tasks.sql` - thirteen predicates plus the prose sweep that followed them.
- `internal/store/tasks.sql.go` - regenerated three times; verified after each CRLF revert.
- `internal/worker/taskstatus_fence_counters_test.go` - the vocabulary parser's two new rungs, its
  extraction behind a directory parameter, and the fixture table test that pins them.
- `internal/store/migrations/000023_task_preparing_status.up.sql` and its down half - the vocabulary
  and the partial index, together, with the down migration's statement order under test.
- `internal/store/tasks_status_vocabulary_lockstep_test.go` - the census widened, plus the
  `CancelJobTasks` entry it had been missing.
- `internal/worker/handler.go` - one `case`, and the `started_at` block deliberately unchanged.
- `internal/api/jobs.go` - the hand-written cancel-signal collection.
- `internal/store/preparing_partition_integration_test.go` - seven per-statement positive tests.
- `internal/worker/handler_preparing_watchdog_integration_test.go` - the execution arm's silence,
  with an absolute-arm positive control, plus the D5 composition.
- `web/src/jobs/JobDetailPage.tsx` - the active count and the default task selection.
