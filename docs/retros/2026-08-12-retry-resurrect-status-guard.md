---
date: 2026-08-12
topic: retry-resurrect-status-guard
branch: claude/pr-merging-session-012b70
range: origin/main..HEAD (green, not yet merged)
---

# Session Retro: 2026-08-12 - Retry and Status-Writer Terminality Guard

**TL;DR:** Closed `bug-2026-06-26-retry-resurrects-cancelled-task` and with it the third and final
iteration of the epoch-fence hardening family. `IncrementTaskRetryCount`, the last writer to
`tasks.status` with a bare `WHERE id = $1`, gained all three predicates - `assignment_epoch`,
`worker_id`, and a non-terminal status allow-list - and `UpdateTaskStatus` gained the status
predicate too. The headline is not the diff. **Across all three iterations the code was right every
time and the prose was wrong every time.** Iteration 1 shipped a spec whose section 4 claim was
disproved by its own implementation. Iteration 2 shipped an overstated claim about the two `.Valid`
checks and a mutation proof that left nothing behind. Iteration 3 shipped a justification that the
*same commit* falsified: the Go identity gate's stated reason for staying was that each forged
message otherwise costs a `log.Printf`, while that commit wrapped both write sites in
`!errors.Is(err, pgx.ErrNoRows)`. Three lenses found it independently. Every `/code-review` the
conductor ran across the three iterations returned zero findings and was partially refuted each
time; the narrow lenses found what the broad pass did not, consistently, three for three.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md`, **plan**
  `docs/superpowers/plans/2026-08-12-retry-resurrect-status-guard.md` (11 sequential tasks, one
  backend engineer; zero files under `web/`, so no frontend slice).
- `internal/store/query/tasks.sql` - `IncrementTaskRetryCount` gains `assignment_epoch`,
  `worker_id` and `status IN ('pending','dispatched','running')` plus a comment block naming the
  distinct question each predicate answers; `UpdateTaskStatus` gains the identical status predicate
  and becomes the **canonical statement of the terminality rule**, which the retry statement
  cross-references rather than duplicating.
- `internal/store/tasks_status_vocabulary_lockstep_test.go` - **new**.
  `TestTasksStatusVocabularyIsExactly` reads `pg_get_constraintdef('tasks_status_check')` and pins
  the six-value vocabulary, naming all three sites that partition it (`UpdateTaskStatus`,
  `IncrementTaskRetryCount`, `RecomputeJobStatus`) and which side a new status has to be decided
  onto. This is the guard that makes the allow-list decision safe rather than merely defensible.
- `internal/worker/handler.go` - the params struct at the retry call site binding the connection's
  own `workerID` and the epoch the currency gate already proved, `pgx.ErrNoRows` dropped silently at
  **both** write sites, and the gate's rationale comment rewritten from a false cost claim into a
  measured one.
- Tests: `TestHandleTaskStatus_AssigneeCannotResurrectItsOwnCompletedTaskViaRetry` (route B1),
  `TestHandleTaskStatus_ASecondTerminalFromTheAssigneeDoesNotOverwriteOrCascade` (route B2), a
  `Connect`-level test driving two terminals over the real message loop, seven store cases in
  `TestIncrementTaskRetryCount_StatusEpochAndAssigneeGuarded`, and two new cases in
  `TestUpdateTaskStatus_AssigneeGuarded`.
- `CLAUDE.md` Epoch fence bullet: the retry statement's vacuous-satisfaction counter-example
  retired, the terminality rule added, and the new general rule **write status predicates as
  allow-lists, never as the equivalent deny-list**.
- `/backlog close bug-2026-06-26-retry-resurrects-cancelled-task`, plus dated Correction blocks on
  the two artifacts the previous retro logged as an open doc defect.

## Key Decisions

- **The deny-list was reversed on review, and the reversal came with a guard test.** See Problem 2.
  This is the only shipped design point that changed after Phase 4.
- **All three predicates, not just the status one.** Each has a store case that is red for it and
  only for it: status (route B1, case 3), epoch (a requeue-then-reclaim to the *same* worker, case
  4), worker (a never-claimed task at epoch 0, cases 6 and 7). The item's Proposal offered
  "either/or"; only "and" works, and the predicate the item never mentions - `worker_id` - was
  required too. Third iteration running where an item's Proposal was incomplete in a way that
  changed the shape of the work.
- **A status predicate, never an epoch bump on terminal transitions.** The bump would break the
  trailing-log flush that
  `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` pins.
  That test passing byte-identical is the evidence for the whole design.
- **Silent drop on `pgx.ErrNoRows` at both `handleTaskStatus` write sites; `dispatch.go` left
  loud.** Consistent with both predecessors. The one argument specific to this path - that a
  rejected retry can be a *genuine* lost retry - does not survive: in the genuine case the cancel or
  the requeue won, which is the correct outcome, so there is nothing to diagnose.
- **The Go identity gate stays, and its role is honestly downgraded from correctness control to "a
  second question plus one saved round trip".** See Problem 1 for what that honesty cost to arrive
  at.
- **No rename of `IncrementTaskRetryCount`,** despite its precondition inverting. A query comment
  instead of call-site churn inside a security fix. Recorded so it is not re-proposed as an
  oversight.

## Problems Encountered

1. **A comment justified itself with a property the same commit removed.** This is the headline, and
   it is the sharpest instance of the arc's pattern. The gate's rationale said it buys "zero
   database round trips and zero log lines per forged message". The same change wrapped both write
   sites in `if !errors.Is(err, pgx.ErrNoRows)`, so deleting the gate costs **zero** log lines: the
   claim was falsified by a hunk a hundred lines below it in the same diff. All three review lenses
   found it independently, and it had already propagated - the spec's section 3.2, the spec's
   section 8.5, the `UpdateTaskStatus` query comment, the handler comment, and the CLAUDE.md
   invariant bullet all carried some form of it.

   Two further corrections fell out of chasing it, and both matter more than the original error:
   - **The round-trip saving is one, not two.** `GetTask` runs ahead of the gate either way, so the
     gate saves one statement instead of two, not two instead of none.
   - **The function never had a "zero attacker-keyed log lines" property to protect.** The
     bad-task-id and `GetTask`-failure branches at the top of `handleTaskStatus` both log
     unconditionally on `upd.TaskId`, *ahead* of the gate.
     `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` is live on exactly that path and this gate
     does not address it.

   Generalize it as: **a justification that cites a cost is a measurement, and it has to be re-taken
   when the code around it moves.** The tell here was available for free - the claim and its
   refutation were in the same commit - and nobody looking at either half alone would have seen it.
   A cost claim in a comment is the one kind of comment that a nearby, unrelated-looking change can
   silently invalidate, because nothing type-checks it and no test goes red.

2. **The deny-list decision was reversed on review, and the reversal is stronger than either
   original.** The spec deliberately chose `NOT IN ('done','failed','timed_out')` and argued for it:
   it is the exact set `RecomputeJobStatus` uses, so the deny-list keeps the cross-reference visible
   and the two definitions in lockstep. Two lenses independently argued for the allow-list
   `IN ('pending','dispatched','running')` on the grounds that the two forms are **equivalent today
   and fail in opposite directions tomorrow**: a task-level `cancelled` status is a plausible
   near-term addition (`CancelJobTasks` squashes cancellation onto `failed` today), and under the
   deny-list such a status would be silently writable and retryable, re-opening the exact
   resurrection this predicate closes.

   The conductor took the allow-list **and** required the lockstep to be enforced rather than
   asserted: `TestTasksStatusVocabularyIsExactly` reads the live check constraint via
   `pg_get_constraintdef` and goes RED when the vocabulary moves, with a comment naming all three
   partition sites and which direction each fails in. Worth recording that these two were the only
   deny-list-shaped status predicates in `tasks.sql`; every other one was already an allow-list, so
   the spec's choice would have made two exceptions to a rule the file otherwise kept.

   The lesson is not "allow-lists are better", it is the shape: **when two forms are equivalent
   today, choose by which way they fail when the vocabulary grows, and then pin the vocabulary.**
   The spec's reasoning was sound and its conclusion was wrong because it optimized for a reader's
   cross-reference over a future editor's mistake.

3. **The engineer found a vacuous green inside its own verification, and only because it re-ran the
   matrix.** After flipping to the allow-list, the mutation matrix was re-run. Row M5 (drop the
   status predicate from `UpdateTaskStatus`) reported **four** failures where the first run reported
   five. The missing one was the `finished_at` restamp assertion: both writes bound `time.Now()`,
   and on Windows two consecutive calls can land in the same clock tick, so the overwritten
   timestamp compared equal to the original and the assertion **passed even though the write
   landed**. A vacuous green in the exact assertion written to catch a write getting through. Fixed
   by binding the second write `time.Now().Add(time.Hour)`, with the reason written at the
   assertion.

   Two things worth separating here. First, the bug: **a "did not change" assertion on a clock value
   is only as strong as the clock's resolution**, and the fix is to make the rejected write's value
   unmistakably different rather than merely later. Second, and more useful: this was found because
   a mutation matrix was **re-run after a design change**, and the discrepancy between two runs of
   the same row is what exposed it. A single run would have shown four reds and looked fine. Treat a
   mutation matrix as a fixture that gets re-run whenever the mutated code changes, not as a
   one-time ceremony.

4. **A test that discriminates nothing, kept and honestly relabelled.**
   `TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask` was the previous
   iteration's headline fix - the permanent guard written precisely because a mutation proof that
   leaves nothing behind proves nothing durable. One iteration later it discriminates neither layer:
   it passes with the Go gate deleted outright (measured, not reasoned), it passes under matrix row
   M3 (worker predicate dropped) and it passes under M4 (`IS NOT DISTINCT FROM`). It goes red only
   for the *conjunction*, and no matrix row removes both at once.

   It was retained as a **scenario** and its comment rewritten to say exactly that, including a
   three-line table of what it was measured to survive. An earlier draft of that same comment
   claimed it "goes red instead if `IncrementTaskRetryCount`'s worker predicate is dropped or
   rewritten" - false, and the plan's own matrix already listed the handler tests in the GREEN
   column for M3 and M4. So even the correction to the previous iteration's correction had the same
   defect: **the matrix was right and the prose was wrong.** The SQL-layer property is pinned by
   store cases 6 and 7 instead, which is where M3 and M4 actually go red.

   This is the previous retro's Problem 3 recurring one iteration later on the very test written to
   fix its Problem 1: **a change can invalidate a test by fixing the thing the test was guarding.**
   The response that scales is the one taken here - relabel and record what it now proves, rather
   than delete it (losing a shape no other test has) or oversell it (leaving a guard everyone
   believes in).

5. **Closing an item broke four links, and the `git mv` is what broke them.** Moving
   `bug-2026-06-26-retry-resurrects-cancelled-task.md` into `docs/backlog/closed/` left
   `feature-2026-07-01-job-retry-action.md` citing it as a **live blocker**, plus three other stale
   references (`feature-2026-06-26-web-enabler-backend-endpoints.md`,
   `bug-2026-07-01-cancel-job-for-update-lock.md`, `idea-2026-07-01-dead-status-vocabulary.md`). All
   four were repaired in-branch, and the retry-action repair is the interesting one: the fix did not
   merely unblock that feature, it **inverted its constraint**. `IncrementTaskRetryCount` now fences
   on epoch, worker and non-terminality, which are the exact inverse of an operator re-run's
   preconditions, so the endpoint must not reuse the statement - a fact that reads as good news in a
   changelog and as a design constraint in the item.

   Generalize into the close procedure: **whenever `/backlog close` runs, grep the whole tree for
   the item's slug and fix every inbound reference in the same commit.** A closed item that is still
   cited as a blocker is worse than an open one, because the next reader trusts the citation.
   `ROADMAP.md` still carries five links to the pre-move path; that is expected, since `/roadmap`
   regenerates it, but it is the one place where the stale links are load-bearing (they sit in the
   Now tier) and it is worth confirming the next refresh clears them.

6. **The conductor's `/code-review` returned zero findings for the third iteration running, and was
   partially refuted all three times.** Three data points is no longer a pattern worth watching, it
   is a calibration. The broad pass is cheap and it is the input the lenses confirm or refute, but
   it must not be treated as a gate that closes the question, and its zero must not shorten the
   fan-out.

7. **The refuted claim survives at three sites in the shipped tree, found while writing this retro.**
   The Correction from Problem 1 was chased through the handler comment, the query comment, the
   spec's 3.2 and section 12, and CLAUDE.md - and missed:
   - `internal/store/tasks.sql.go:1051-1058` carries the **superseded** paragraph
     ("...one round trip and one log line earlier"), while its source
     `internal/store/query/tasks.sql:74-84` carries the corrected one. The generated file is out of
     sync with the query it was generated from. The statement text, params and behavior are correct
     and identical - this is the doc comment only - but it means `sqlc generate` was not re-run
     after the final comment edit, or the CRLF-churn revert took the file back with it.
   - `internal/worker/handler_taskstatus_integration_test.go:326` still says the gate's remaining
     value is "(zero round trips, zero attacker-keyed log lines)" - both halves of the exact claim
     that was refuted.
   - `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md:696` (section 8.5) says the
     same thing, in the same spec whose section 3.2 carries the Correction refuting it.

   This is the previous retro's own lesson - **chase a refutation through every artifact that
   repeated the claim** - failing on the very iteration that closed that retro's instance of it. The
   honest reading is that "grep for the claim" is not a step anybody is doing; it is being done from
   memory, and memory missed the generated file and the test comments. Two of the three sites are
   source and out of a doc agent's reach; see Findings Triage.

## Findings Triage

- **0 high, and no confirmed finding against the shipped statements.** The predicates, the params
  binding and the error-branch splits drew nothing. As in both predecessors, **the review's yield
  was in the prose and the evidence**, not the diff: a self-falsifying comment (Problem 1), a design
  form that fails open on a future edit (Problem 2), a vacuous assertion in the verification itself
  (Problem 3), and a test whose advertised power had moved (Problem 4).
- **The evidence regime was the strongest of the three iterations, and it was verified rather than
  trusted.** The correctness lens independently reproduced all six mutation-matrix rows in a
  scratchpad copy of the tree, including M4's strict "case 7 **only**" and case 5 going red only
  under M6. Contrast iteration 2, where the same lens re-ran the engineer's proof and found it left
  nothing behind. The difference is structural, not diligence: this plan required every mutation's
  discriminating input to be a **permanent committed test** with no test edit needed to observe it,
  which is exactly what makes a third party able to re-run the matrix at all.
- **Open doc defect, not corrected here** (Problem 7). Two of the three sites are source files
  (`internal/store/tasks.sql.go`, `internal/worker/handler_taskstatus_integration_test.go`) and one
  is this iteration's own spec. The TPM writes only to `docs/`, so all three are reported to the
  conductor rather than fixed in this pass. The generated-file one is the most actionable: it is a
  one-command fix (`sqlc generate`, then the CLAUDE.md CRLF procedure) and it is exactly the drift
  that generated-file hygiene checks exist to catch.

## Deferred Findings

Proposed and filed, not fixed:

1. `bug-2026-08-12-tasklog-terminal-task-append-unbounded` (**bug/medium**) - `AppendTaskLog` has no
   terminality and no time bound, so the trailing-log window on a completed task never closes. This
   PR **reaffirms** that a terminal transition preserves `assignment_epoch` and `worker_id`, in
   several comments and pinned by a test, because trailing chunks arriving just after a terminal
   status must still be stored. The consequence is the mirror image: after this PR no production
   statement can modify a terminal task's row at all, and an agent authenticated as that worker can
   append log rows to a task it finished, indefinitely, with no row cap and no retention anywhere in
   the schema. The bound must be time-based on `finished_at`, not a status predicate - a status
   predicate would break the very flush this iteration pinned.
2. `bug-2026-08-12-retries-unvalidated-and-budget-only-in-go` (**bug/medium**) - `jobspec.Retries` is
   bounded nowhere in `jobspec.Validate` and flows straight through `jobcreate` into `CreateTask`,
   so `retries: 2000000000` on a permanently-failing task is an authenticated-user dispatch/fail/
   requeue loop occupying a worker slot. The half that belongs to *this* iteration is the second
   one: the budget check `task.RetryCount < task.Retries` exists **only** at the single Go call
   site. `IncrementTaskRetryCount` has no `retry_count < retries` predicate, which makes the budget
   the one part of the retry path this PR did **not** move into SQL, and the integration lane
   confirmed plainly that no existing test would catch a task exceeding its retry budget.
3. `feature-2026-06-26-audit-log-admin-console-actions` **updated, not duplicated**: status-fence
   rejections are now silently dropped with zero observability at both `handleTaskStatus` write
   sites, and this PR's comments explicitly defer detection to the audit-log work. That deferral now
   lives on the receiving item instead of only in the deferring comments - the same lesson as
   Problem 5, applied forward: a pointer is only useful in the direction someone will actually read
   it.

Also noted and **not** filed: `RecomputeJobStatus`'s `cancelled`-blindness (it recomputes a
cancelled job whose tasks are all terminal to `failed`). It is unreachable through this path once
the predicates land, it is a job-status-vocabulary question with its own blast radius, and
`feature-2026-06-26-web-enabler-backend-endpoints` already records that the retry endpoint must
decide it. Filing a third item would fragment the decision.

## Known Limitations

- **No test in the tree discriminates the Go identity gate in `handleTaskStatus`.** Deleting it
  leaves every test green - measured during implementation, not assumed. What remains is
  non-functional (one saved round trip, and a different question) and a round trip is not observable
  state, so it is recorded rather than pinned. Without this note the next reviewer deletes the gate
  and sees a green suite.
- **The fence binds a worker, not a connection.** Two concurrent streams for the same worker row
  both satisfy every predicate. Deliberate, unchanged across all three iterations, and what keeps
  reconnect-within-the-grace-window working.
- **The retry budget is enforced only in Go** (Deferred Finding 2). Everything else on the retry
  path is now enforced in SQL.
- **A terminal task's log stream stays open forever** (Deferred Finding 1). This is the deliberate
  flip side of the trailing-log flush.
- **A rejected write is invisible.** Forged, stale and duplicate are deliberately
  indistinguishable, and none is logged. Detection belongs with the audit-log item.
- **None of this is covered by `make test`.** Every test touched is `//go:build integration`. The
  unit gate is a no-regression gate and never evidence for this change.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, third time,
  and it paid again: the item's "either/or" Proposal, its missing third predicate, its unnamed
  requeue variant of route A, and its unnecessary job-not-cancelled check were all caught at spec
  time.
- **A backlog proposal is not a contract** - honored; the spec deviated on the central design point
  and said why, and its acceptance criterion 2 (an interleaving test) was declined explicitly with
  the honest substitute named.
- **A mutation proof must leave a permanent test behind** - honored by construction; every matrix
  row's discriminating input is a committed test, which is what let a lens re-run the whole matrix
  independently.
- **Stage the work so RED is behavioral** - honored, with the asymmetry stated rather than papered
  over: the status predicate's RED is behavioral at both layers, the epoch and worker predicates
  cannot have one (their arguments do not exist before the change) and are evidenced by mutation.
- **Backlog housekeeping is required scope** - honored, and extended: see Problem 5.
- **Chase a refutation through every artifact that repeated the claim** - **honored in the direction
  it was inherited and failed in its own** (Problem 7). The two artifacts the previous retro named
  now carry dated Corrections; three new sites for this iteration's own refutation do not.

New from this iteration:

- **Write status predicates as allow-lists, never as the equivalent deny-list, and pin the
  vocabulary.** Now in CLAUDE.md, with `TestTasksStatusVocabularyIsExactly` as the enforcement.
  Generalizes past status: whenever two set-membership forms are interchangeable today, choose by
  which way each fails when the set grows.
- **A cost claim in a comment is a measurement with an expiry date.** Nothing type-checks it and no
  test reddens when it goes false, so it can be falsified by a hunk in the same commit. **Candidate
  for durable memory**, closely related to the existing "a wrong contract in docs is a defect" note
  but distinct: that one is about contracts consumers implement against, this one is about
  justifications that keep code alive.
- **Re-run a mutation matrix after any change to the mutated code, and compare run to run.** The
  discrepancy between two runs of row M5 is what exposed a vacuous assertion; a single run showed
  four reds and looked healthy.
- **A "value did not change" assertion on a clock is only as strong as the clock's resolution.**
  Bind the rejected write an unmistakable distance away, not just a second `time.Now()`.
- **Grep for the slug when closing an item.** `/backlog close` moves a file and silently breaks
  every inbound link; four items cited this one, one of them as a live blocker.
- **A zero-finding broad review does not close the question** - third data point, and the fan-out
  found the shipped design's one reversible decision. Belongs in the agent-team review guidance as
  settled, not provisional.

## The Arc: three PRs in one day closed the family

Three iterations, one day, one shape of bug each:

| Iteration | Path | What was missing | What closed it |
| --- | --- | --- | --- |
| 1, `tasklog-append-assignee-fence` | `task_logs` writes | identity | `AND t.worker_id = $` in `AppendTaskLog`'s fence CTE |
| 2, `taskstatus-update-assignee-fence` | `tasks.status` writes | identity | a Go identity gate ahead of the retry branch, plus `AND worker_id = $` on `UpdateTaskStatus`, with `worker_id` dropped from its SET list |
| 3, `retry-resurrect-status-guard` | the retry path | currency, identity **and** terminality | all three predicates on `IncrementTaskRetryCount`, plus the terminality predicate on `UpdateTaskStatus` |

What the family established, as one statement: **every production writer to `tasks.status` or
`task_logs` now names the worker it writes on behalf of, proves that worker's generation is current,
and cannot write a task that has already finished** - except the requeue and cancel family, which
ends the assignment instead, which is the invariant's other branch. Iteration 2's residual ("the
retry branch is unforgeable, not atomic") is now an actual guarantee, and without a transaction: the
epoch predicate makes the retry atomic with respect to the row the branch decision was read from,
and exactly-once per generation under concurrency.

**The residual, recorded here so the boundary lives in one place: a compromised-but-enrolled agent,
over its own assigned tasks.** An attacker holding worker W's agent token *is* W as far as the
server can tell, so it can still drive W's own tasks through their legal state machine - `RUNNING`,
then one terminal - and can still append log content to those tasks. No server-side check on this
path can do better; the three iterations removed everything *else* from its reach: every other task
in the database, every stale generation, and every already-finished task including its own. What
remains is bounded by three items that are open and named rather than implied:
`bug-2026-08-12-auto-enroll-hostname-takeover` (how an attacker acquires an identity under
`RELAY_ALLOW_AUTO_ENROLL`), `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` (what an
authenticated agent can still cost the recv goroutine), and the new
`bug-2026-08-12-tasklog-terminal-task-append-unbounded` (what it can still cost the database).

The process result belongs beside the technical one, because it is what three iterations of the same
shape actually taught: **the diff was right every time and the writing about it was wrong every
time, in a way only a reader with a narrow brief and an obligation to derive the fact fresh ever
caught.** Iteration 1 found a spec section disproved by its own implementation. Iteration 2 found a
mutation proof that had locked nothing and a doc annotation two lenses disagreed about. Iteration 3
found a comment falsified by its own commit. None of the three came from the broad review pass; all
three came from lenses; and in each case the artifact carrying the error was the one a future reader
would have reached for first.

## Files Most Touched

- `internal/store/query/tasks.sql` - the source of truth. `UpdateTaskStatus` now carries the
  canonical terminality-rule comment (allow-list rationale, the lockstep set, and why the fix is
  never an epoch bump); `IncrementTaskRetryCount` carries the three-predicate block plus the
  standing instruction that `POST /v1/jobs/{id}/retry` must not call it.
- `internal/store/tasks.sql.go` - regenerated by sqlc. **Its `UpdateTaskStatus` doc comment is stale
  relative to the query source** (Problem 7); the statement itself is correct.
- `internal/worker/handler.go` - the params struct at the retry call site, both `pgx.ErrNoRows`
  drops, and the rewritten gate rationale that now states its cost at true size.
- `internal/store/tasks_status_vocabulary_lockstep_test.go` - new; the guard that makes the
  allow-list decision enforceable.
- `internal/store/store_test.go` - `TestIncrementTaskRetryCount_StatusEpochAndAssigneeGuarded`
  (seven cases, each naming the predicate that rejects it) and two new
  `TestUpdateTaskStatus_AssigneeGuarded` cases, including the `finished_at` restamp fix from
  Problem 3.
- `internal/worker/handler_taskstatus_integration_test.go` - the two route-B tests with the
  `dispatchable` helper and their positive controls, the `Connect`-level test, and the relabelled
  zero-value-worker-id scenario from Problem 4.
- `CLAUDE.md`, the closed backlog item, four repaired inbound references, and the two dated
  Correction blocks the previous retro asked for.

## Verification

- **Full suite green: 757 PASS, 0 FAIL.** Reported by the integration lane; commit hashes are not
  recorded here because this pass had no shell, so the range is given as `origin/main..HEAD`.
- **The integration lane caught five packages being served from Go's test cache** and forced an
  uncached re-run. Worth carrying forward as a gate detail: a cached PASS is not evidence for a
  change only integration tests exercise, and `(cached)` in the output is easy to read past.
- Behavioral RED captured verbatim before each fix landed, on both stages: the cancelled task
  resurrected to `pending`, the `done` task resurrected with `retry_count 1`, the `done` task
  flipped to `failed` with its dependent cascaded.
- The full six-row mutation matrix ran, each row producing exactly its predicted red set, including
  M4's strict "case 7 only" and case 5 red only under M6 - and was **independently reproduced by a
  review lens** in a scratchpad copy of the tree.
- The hard gates held:
  `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` and
  both `internal/scheduler` `failClaimedTask` tests passed **with no edit**, which is the evidence
  for the status-predicate design and for the "tautological on the dispatcher path" claim
  respectively. `TestIncrementTaskRetryCount_BumpsEpochAndFencesStaleRetry` took only the predicted
  params-struct repair, with every assertion byte-identical.
- `go vet -tags integration ./...` clean, the real compile gate after the signature change.
- `/backlog close` performed; the item is in `docs/backlog/closed/` with `status: closed`,
  `closed: 2026-08-12`, `resolution: fixed` and a Resolution note recording that three routes were
  closed rather than the one filed.
</content>
