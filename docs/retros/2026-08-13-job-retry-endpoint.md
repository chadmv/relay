---
date: 2026-08-13
topic: job-retry-endpoint
branch: claude/pr-merging-session-6aede7
range: origin/main..HEAD (26 commits, green, not yet merged)
---

# Session Retro: 2026-08-13 - POST /v1/jobs/{id}/retry, and a claim refuted at every stage

**TL;DR:** Shipped `POST /v1/jobs/{id}/retry?task=failed|all`, the operator re-run of a terminal
job's failed or all tasks: three new store statements (`RetryJobTasks`, `SelectRetryableTaskIDs`,
`GetJobForUpdate`), one handler, one extracted authorization gate now shared with cancel, and a
lock-order change to `handleCancelJob`. Closes
`docs/backlog/feature-2026-08-13-job-retry-endpoint.md` and, as a side effect nobody scheduled,
satisfies `docs/backlog/bug-2026-07-01-cancel-job-for-update-lock.md` as well.

**The story of this iteration is a chain of refutations, five links long, in which every stage found
the previous stage's claims wrong and the last link caught the conductor.** The spec refuted three of
the item's own premises, including its framing question. The plan refuted two spec claims, one of
which was a RED proof that would have passed under its own mutation. The implementer found four
plan-supplied tests that did not discriminate and fixed the tests rather than the assertions. Four
review lenses reached two opposite conclusions and the integration lane broke the tie by reading the
SQL, discovering that a **stale doc comment had misled a reviewer mid-review**. Then the conductor
relayed a false claim from a lens into an engineer's task brief, it was written into a shipped
comment, and the re-verify pass caught it. One medium finding landed, and it was a regression this
PR's own refactor introduced into a **shipped** endpoint.

## The refutation chain

Read this section first. Everything else in the document is detail hanging off it.

Six actors produced claims about this work: the backlog item, the spec, the plan, the implementing
engineer, four review lenses, and the conductor. **Five of the six were caught being wrong by the
next one down the line.** The item is the only one that was never checked by a successor, because it
is the head of the chain, and it is also the one with the most wrong claims. That is not a
coincidence and it is the transferable point: **a claim's error rate is a function of how many
readers have had to act on it, not of who wrote it.**

### Link 1: the spec refuted three of the item's premises, including its framing question

The item is the long one, seven weeks of accumulated constraints, and its own text says "do not
treat any bullet below as advisory". Three of them did not survive contact with the tree.

1. **The item's framing question rests on a sibling that does not exist.** Its Proposal opens the
   auth decision with "the sibling force-cancel is admin-only while ordinary cancel is not, so
   'operator re-run' needs an explicit answer rather than an inherited one". There is no admin-gated
   force-cancel. `DELETE /v1/jobs/{id}` is registered `auth(...)` with no `admin(...)` in the chain
   (`internal/api/server.go:125`); `force` is a query parameter parsed inside that one handler
   (`internal/api/jobs.go:728`), and one owner-or-admin gate covers graceful and forced cancel
   identically. The SPA does not gate the Force button on admin either. There are not two
   differently-gated siblings to choose between; there is exactly one precedent for a destructive
   job-scoped write, and the spec adopted it. **The question the item spent its Route bullet posing
   had already been answered by the code it cited.**
2. **Constraint 5 as worded would have made the guard inert.** "It must not reopen a task whose
   dependents already ran" is correct as an intent and dead on arrival as a predicate: when T fails,
   `FailDependentTasks` marks its still-`pending` dependents `failed`, so on *any* healthy failed
   job every failed task has failed dependents. A literal implementation refuses every ordinary
   retry while passing the obvious negative test. The guard has to be relative to the selected set -
   `dep.status <> 'pending' AND dep.id NOT IN (selected)` - and the item does not say so.
3. **Constraint 6's regression does not reproduce.** The item says reopening a terminal job
   reactivates `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`. It does not: a retried job leaves
   the `done_24h`/`failed_24h` buckets the instant it becomes `running`, and re-enters with an
   `updated_at` equal to its new finish. There is no state in which a job is counted in a bucket
   with an `updated_at` that is not its most recent finish. The only real effect is a **transient
   undercount** while the job re-runs, which is defensible on the merits. The bug item's own comment
   in `JobStatusCounts` was separately false ("the only writer of `updated_at` is `UpdateJobStatus`"
   - `RecomputeJobStatus` writes it too, on every task transition), and that prose is corrected in
   this branch.

The item was also **silent on the thing that turned out to be required scope**: it never mentions
`handleCancelJob`. Retry and cancel are the only two multi-statement writers over both `jobs` and
`tasks`, and they would have taken their row locks in opposite orders. That is an ABBA deadlock pair
between two ordinary operator actions, plus a live route to run work on a cancelled job. See Key
Decisions.

Two premises were wrong, one constraint was inverted, and the omission was the largest single piece
of work in the slice. **Eighth consecutive iteration in which "verify the item's technical claims
during spec" changed the shape of the work**, and the strongest instance yet: the previous record
holder (the UserMenu inversion) had every factual claim right and every prescription wrong. This one
got the framing question wrong on a fact it cited by file and line.

### Link 2: the plan refuted two spec claims, one of them a vacuous RED

The plan is a doc-only artifact by an agent with no shell, written against a spec approved twenty
minutes earlier, and it still found two things.

1. **The two-concurrent-retries narrative is wrong given the spec's own lock decision.** Spec
   decision 9 describes what happens when two retries race: B blocks on A's row locks, re-checks its
   qual, and returns 409 case C. That describes the UPDATE **in isolation**. Decision 7 makes the
   handler take `GetJobForUpdate` first, so two retries on one job fully serialize: the second's
   selection runs after the first commits, the job is now `running`, and it hits the **job status
   gate** and returns "job is not finished". Case C is unreachable through HTTP. The plan reported
   this as a finding rather than designing around it, kept case C as a defensive branch, and proved
   it **deterministically** with a `BEFORE UPDATE ... RETURN NULL` trigger following the
   `installFailDeleteTrigger` precedent. That is stronger evidence than a race that cannot occur.
2. **Spec test 2 was vacuous as written.** The spec asked for a RED proof of the "status allow-list
   must live in the row-level `WHERE`, not only in a CTE" property, staged as: reopen a task, then
   call `RetryJobTasks` again. Under the stated mutation the *sequential* second call recomputes
   `selected` from a fresh snapshot in which the task is already `pending`, so `selected` is empty
   and the mutated statement also returns zero rows. **The test passes under the mutation it was
   written to catch.** The property is genuinely about EvalPlanQual and requires a real concurrent
   interleave, which the plan built.

Both of these are the standing lesson **plan-supplied tests are untrusted**, applied one level up:
here it is the *spec*-supplied test that was the guess, and the plan was the first reader with
enough of the design in view to see that its two halves contradicted each other.

### Link 3: the implementer refuted four plan-supplied tests and fixed the tests

Four plan test bodies did not discriminate what they claimed. In each case the engineer **fixed the
test rather than weakening the assertion**, which is the behaviour the standing rule asks for and
which is worth recording because the tempting move is always the other one.

- A fixture that never stamped `finished_at`, so the assertion that retry clears it could not fail.
- A seed where `retry_count` was already 0, so "retry resets `retry_count` to 0" was true before the
  statement ran.
- A "transitive" dependents case whose shape did not actually require the recursive term: the direct
  edge alone produced the same answer, so the test proved nothing about the descendant closure it
  was named for.
- An all-or-nothing case whose shape a per-row guard would have blocked anyway, so it could not
  distinguish the all-or-nothing guard from a per-row one.

The engineer also found that **one plan-predicted mutation reddens nothing**: mutating the placement
of `NotifyTaskSubmitted` relative to the rollback does not fail, because Postgres queues the
`pg_notify` payload until commit and the rollback discards it either way. The plan predicted a RED
there; the correct conclusion is that the property is enforced by Postgres rather than by the
handler's ordering, and the test that remains asserts the observable (no notify on any 409 path)
rather than the mechanism.

Note the pattern across links 2 and 3: **seven of the artifacts handed to the implementer were
wrong, and every one of them was wrong in the direction of a test that passes.** None of them was
wrong in a way that would have produced a red gate. That is what makes plan-supplied test bodies
dangerous rather than merely imprecise.

### Link 4: four lenses disagreed, and the disagreement was the finding

The dependents guard is the most expensive part of the statement: a `WITH RECURSIVE` descendant walk
and an uncorrelated `NOT EXISTS`. The question the lenses were implicitly answering is whether it
ever fires.

- **Invariants and security both concluded it is unreachable in any well-formed history.** A `done`
  or `running` descendant of a `failed` task requires a state the dependency rules forbid.
- **Correctness said it IS reachable**, via `RequeueTaskByID`'s reconcile path, and cited that
  statement's own doc comment, which said it requeues a task **"regardless of current status"**.
- **The integration lane refuted correctness by reading the SQL rather than the comment.**
  `RequeueTaskByID` carries `status IN ('dispatched','running')`, and its only caller sources
  candidates from `GetActiveTasksForWorker`, which filters identically. It cannot produce a `done`
  descendant. The guard is unreachable.

So the majority answer was right, the dissent was well-reasoned, and **the dissent was caused by a
false comment in the tree.** That comment is itself a live defect - it is now corrected in this
branch to say the `WHERE` clause matches only `dispatched` and `running`
(`internal/store/query/tasks.sql:265-268`) - and it is the ninth consecutive iteration in which
wrong prose about correct code was the dominant defect class.

**What is new is the cost measurement.** Previous instances of that lesson were about a future
reader being misled. This one has a receipt: a stale doc comment consumed one full review lens's
conclusion and one integration lane's time to adjudicate. Wrong prose is not only a latent hazard
for the next author; **it bills the current review, at the rate of one reviewer-pass per false
sentence.** That is the strongest argument yet for treating comment correctness as in-scope work
rather than as tidying.

The guard stays, unreachable and all. See Findings Triage for why that is the right call and why it
is not filed.

### Link 5: the conductor propagated a false claim, and the re-verify caught it

The security lane, arguing that a retry cannot evict a live agent, asserted that **nothing in the
repository ever writes `timed_out`**. The conductor passed that to the engineer as a fix instruction
without checking it, and the engineer wrote it into `RetryJobTasks`'s comment block.

It is false. `internal/agent/runner.go:233` sets `TASK_STATUS_TIMED_OUT` when a task's subprocess
exceeds its deadline, and `internal/worker/handler.go:527` maps that to the string `timed_out` and
writes it through `UpdateTaskStatus`. The fifth pass caught it, and the passage was rewritten.

**The safety conclusion survived, for a different reason than the one that had been written down.**
A `timed_out` row is safe to reopen not because nothing writes it, but because its one writer is the
**assignee itself**, filing its own terminal report after the subprocess is already dead and its
proctree cleaned up. The corrected comment
(`internal/store/query/tasks.sql:411-427`) says exactly that, and then names the change that would
break it: a **server-side watchdog** stamping `timed_out` on a task an agent is still running would
make this statement a duplicate-execution primitive. That sentence is worth more than the original
claim was, because it converts a fact about today into a trigger for tomorrow.

Record the shape, because it is subtler than "a claim was wrong": **a conclusion that survives for a
different reason than its stated one is not a confirmed conclusion.** The right answer plus the wrong
reason reads exactly like verification and is not. It took a fifth pass to catch here, and it was
caught by grepping for a writer of the literal string rather than by re-reading the argument.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-13-job-retry-endpoint.md` (nine numbered decisions, each
  with the alternatives it beat; written in AUTONOMOUS gate mode, so decisions 4, 6 and 8 are
  product judgments made without a human and each is flagged as such). **Plan**
  `docs/superpowers/plans/2026-08-13-job-retry-endpoint.md` (14 sequential tasks, one
  `relay-backend-engineer`, no parallelism available because `sqlc generate` rewrites every file in
  `internal/store/`).
- **Route.** `POST /v1/jobs/{id}/retry`, `auth(...)` only, at `internal/api/server.go:126`. The Jobs
  block comment at `:105-120` was corrected: its old sentence claimed only the cancel is
  owner-or-admin gated, which the new route made false.
- **`handleRetryJob`** (`internal/api/jobs.go:882-1080`). Parses `id` and `task` before any DB work;
  owner gate on an unlocked read; then one transaction that locks the job row, gates on
  `job.Status IN ('done','failed')`, selects, updates, recomputes, and calls `NotifyTaskSubmitted`
  **inside** the transaction so the wake is gated on the commit as well as on the row count.
  `broker.Publish` after commit, matching cancel.
- **`jobOwnerOr404`** (`internal/api/jobs.go:679-719`), the single shared owner-or-admin gate, with a
  40-line comment block that is the finding in Problems 1 written at the site.
- **Three new store statements** in `internal/store/query/`: `SelectRetryableTaskIDs`
  (`tasks.sql:380-390`), `RetryJobTasks` (`tasks.sql:392-500`) and `GetJobForUpdate` in `jobs.sql`.
  `RetryJobTasks` carries roughly 80 lines of comment covering why the status allow-list must stay
  in the row-level `WHERE` and not migrate into the CTE, why the one negation in the statement is
  the fail-closed direction, why a dependent in the selected set does not block, and the corrected
  `timed_out` passage from link 5.
- **`handleCancelJob` changed** (`internal/api/jobs.go:721-834`): `GetJob` for the gate, then
  `GetJobForUpdate` inside the transaction, so both handlers take the job row lock first and the
  status gate is decided on the locked row.
- **Comment corrections in existing code**, all of them defects rather than tidying:
  `IncrementTaskRetryCount`'s forward reference now names `RetryJobTasks`
  (`tasks.sql:129-136`); `RequeueTaskByID`'s "regardless of current status" is replaced with what its
  `WHERE` actually does (`:265-268`); `JobStatusCounts`'s single-writer claim is corrected;
  `TestTasksStatusVocabularyIsExactly`'s doc comment gains the two new partition sites.
- **Tests.** `internal/store/incrementtaskretrycount_guard_test.go` (structural, **untagged**, so it
  runs in the plain gate with no Docker: fails if `IncrementTaskRetryCount` appears in any non-test
  Go file outside `internal/worker/handler.go`); `internal/store/retry_job_tasks_integration_test.go`;
  `internal/api/jobs_retry_integration_test.go`; `internal/api/jobs_retry_log_test.go` (untagged, the
  bounded-log unit tests from Problems 2).
- **README** gained the route row, including that `failed` covers `timed_out` and that `task` has no
  default.
- **Zero files under `web/`.** Backend-only slice; the `git checkout -- web/dist/` rule was declared
  inapplicable in the plan's independence block rather than silently skipped.

## Key Decisions

- **A cancelled job is refused (409), and `RecomputeJobStatus` is not touched.** The item offered
  three options. Refusal was chosen because `CancelJobTasks` squashes cancellation onto `failed`, so
  on a cancelled job `?task=failed` would select every task that was in flight when the cancel
  landed - retry would silently mean "un-cancel everything", the most surprising available reading
  of a Retry button. The property this buys is checkable by reading eight lines of handler:
  **because the gate admits only `done` and `failed`, the only job-status transition this endpoint
  can cause is `done|failed -> running`**, so `RecomputeJobStatus`'s cancelled-blindness stays
  exactly as latent as it was. That is a stronger guarantee than fixing the CASE would have given,
  and it avoids changing the hottest status statement in the repo inside the same PR as a new fenced
  multi-row write.
- **`retry_count` resets to 0**, and the test proves the reset is functional rather than cosmetic:
  it asserts that a subsequent agent-reported failure at the new epoch is accepted by
  `IncrementTaskRetryCount` and burns a retry. Asserting the column alone would have passed against
  a reset no consumer honors. Cost stated plainly: lifetime attempt count is no longer recoverable
  from the row, and if it is ever wanted it belongs in an attempts table, not in a counter that also
  gates behaviour.
- **Zero matched is a 409, not a 200 with `tasks_retried: 0`.** Matches the sibling (cancel returns
  409 when the requested change is a no-op), nothing was written so a 200 would report success for
  an action that did not happen, and a specific message answers the operator's actual question. The
  item's requirement - that a client can tell a zero from a real re-run - is satisfied more strongly
  by a distinct status code than by a distinct number.
- **The status allow-list stays in the UPDATE's own `WHERE`, on the target table's columns.** Under
  READ COMMITTED, a blocked UPDATE re-evaluates its **row-level** qual against the updated tuple
  (EvalPlanQual); it does not re-execute CTEs. The tidier spelling `t.id IN (SELECT id FROM
  selected)` passes every single-threaded test and silently double-bumps `assignment_epoch` under
  concurrency. This is the single most likely way to ship the endpoint subtly broken, and the only
  thing standing in front of it is one test with a real concurrent interleave.
- **The dependents guard is all-or-nothing, and that is a safety property rather than tidiness.**
  Partial application would leave a selected task `pending` behind a dependency that stayed
  terminal; `GetEligibleTasks` requires every dependency to be `done`, so that task is never
  eligible again and the job sits at `running` forever. Silent partial exclusion converts a
  corrupted-DAG diagnosis into a wedged job. Because the `NOT EXISTS` is uncorrelated it is true for
  every row or for none, which also makes a **partial** result provably a concurrency outcome rather
  than a guard outcome - the classification the handler's three 409 messages rest on, with no extra
  query.
- **`GetJobForUpdate` is required scope, not hardening.** Without it the endpoint has a live route
  to run work on a cancelled job (cancel's `CancelJobTasks` runs against a pre-retry snapshot,
  matches nothing, then stamps the job `cancelled` over freshly `pending` tasks, and
  `GetEligibleTasks` does not consult job status) and an ABBA deadlock pair with cancel. The spec
  predicted a reviewer would suggest removing it as unrelated diff, and pre-armed acceptance
  criterion 11 and a serialization test against that.
- **The one negation in the statement is deliberate and is documented as such.** `dep.status <>
  'pending'` looks like the deny-list the house rule forbids. It is not: this predicate authorizes
  **blocking**, not writing, so the negation is the fail-closed direction. The mechanical
  "allow-list" spelling would fail open on the next status added. The rule is about which way the
  failure falls, not about the syntax, and the statement says so where a pattern-matching reviewer
  will read it.

## Problems Encountered

1. **The one medium finding was a regression the refactor introduced into a shipped endpoint.**
   This is the finding of the iteration and it deserves to be read as a class, not an incident.

   Extracting `jobOwnerOr404` was a spec instruction and obviously right: an authorization gate that
   exists twice is an authorization gate that will diverge. The extraction took the gate a job row
   already fetched inside the caller's transaction, which put the owner check **after**
   `SELECT ... FOR UPDATE` on both handlers. `handleCancelJob` had gated on an unlocked read before
   this PR.

   Consequence: any authenticated caller could `POST /v1/jobs/<someone-else's-job>/retry` or
   `DELETE` it, park in the Postgres lock queue behind the owner's in-flight cancel or retry, and
   hold a pgxpool connection for the duration of somebody else's transaction - before being told 404.
   Neither route is rate limited. **The 404 was still correct; the cost of getting it was not.**

   Fixed by gating on an unlocked `GetJob` and then taking `GetJobForUpdate` inside the transaction
   (`internal/api/jobs.go:730-758`), pinned by
   `TestJobWrites_NonOwner_404_WithoutQueueingForTheJobRowLock`. That is only safe because
   `jobs.submitted_by` is **immutable** - set once by `CreateJob`, never updated by any statement in
   the repo - which was verified repo-wide rather than assumed, and the comment records that if
   `submitted_by` ever becomes writable the ordering becomes a TOCTOU. Every gate reading a
   **mutable** column, which is both handlers' status checks, stays on the locked row.

   The class: **an extraction that preserves the gate's code can still move it relative to a lock,
   and the diff shows only the move.** `git diff` on `handleCancelJob` shows a six-line block
   replaced by a one-line call, which reads as a pure refactor. The behaviour change is in the
   ordering, which is not a line. Two of this project's last three "pure refactor" slices produced
   their only real finding this way. The countermeasure is cheap and worth making standard: **when a
   refactor moves code across a lock, a transaction boundary, or an await, say so in the plan and
   name what now happens before what.**

2. **A test pinned a literal instead of the wiring, for the third iteration in a row of that
   family.** The bounded-log helper `uuidStrHead` truncates the 409 diagnostic lines to `logIDHead`
   ids (`internal/api/jobs.go:846-849`). Its first test passed a local `8` rather than the constant,
   so raising `logIDHead` to 5000 left every assertion green while the two real call sites went
   unbounded. Corrected: every assertion in `internal/api/jobs_retry_log_test.go` is written against
   `logIDHead` itself, plus a guard that fails if the constant exceeds 32
   (`:50-51`), and the file's header comment records what the previous version pinned so nobody
   reverts it as noise.

   This is the same shape as "a cadence test must assert the wiring" and "a mutation proof must
   leave a test behind", now on a bound rather than an interval. The generalization is getting
   sharp enough to state once and reuse: **a test that restates a constant's value tests the
   constant; a test that reads the constant tests the code that consumes it.** Whenever a test
   contains a numeric literal that also exists in production code, the literal is the bug.

3. **The item's dependents constraint would have shipped an inert feature, and only a matched pair
   of tests could have caught it.** Covered in link 1, but the testing consequence is its own
   lesson. A guard implementing constraint 5 literally (`dep.status <> 'pending'`, no selected-set
   exclusion) **passes the obvious negative test** - plant a `done` dependent, assert the retry
   returns zero rows - and refuses every real retry in production. The negative control alone is
   worse than no guard, because the feature is inert and looks tested. The spec named tests 6 and 7
   as a matched pair that must be written together and declared neither meaningful alone. They were,
   and they are.

4. **Case C is unreachable through HTTP, and the honest response was to prove it deterministically
   rather than to delete it or to fake a race.** Two retries on one job serialize on the job row
   lock, so the count-mismatch branch cannot be reached by racing. The plan reported the spec's
   contrary narrative as a finding, kept the branch, and reached it with a `BEFORE UPDATE ... RETURN
   NULL` trigger on `tasks`. Three options were available - delete the branch, write a flaky racing
   test, or build a deterministic fixture - and the third is the only one that leaves both the
   defence and the evidence in the tree.

5. **A reported test failure was adjudicated as a flake by non-reproduction, and the evidence is
   weaker than a both-ways measurement.** `TestConnect_NoCredentialRejected` was reported failing.
   The integration lane re-ran it **in the true full-suite shape** and it passed at 2.12s. That is
   the basis for calling it a flake: **non-reproduction under the shape that reported it**, not a
   matched branch-versus-origin/main pair. The standing rule from the 2026-08-12 arc is to measure a
   suspected pre-existing failure both ways and get a number for each; that was not done here, and
   the retro should not imply it was. The test is in `internal/worker`, a package this slice does
   not touch, which is corroboration and not proof. If it recurs, the both-ways measurement is the
   first thing to do and it has not been done yet.

6. **The `web/dist` rule and the sqlc CRLF rule swapped places again, and the plan declared both up
   front.** Second consecutive backend slice; the discipline held. Both `tasks.sql.go` and
   `jobs.sql.go` carry real content changes here (new functions **and** edited doc comments), which
   is precisely the shape the CRLF revert has silently discarded before, and the plan made the
   read-back a mandatory step rather than a reminder.

## Findings Triage

- **1 medium (Problems 1, a regression into a shipped endpoint, introduced by this PR and fixed
  before merge). 0 high. 0 security findings against the shipped endpoint.** Everything else was a
  claim correction: three in the item, two in the spec, five in the plan's test bodies, one between
  review lenses, and one from the conductor.
- **Four lenses ran plus the conductor's `/code-review`, supplied to each lane as prior findings per
  the standing shape.** The lanes disagreed with each other exactly once and that disagreement was
  productive (link 4). This is the second iteration running in which the most useful review output
  was two lanes contradicting each other rather than either lane's own findings, which argues for
  keeping the lens count at four even when three would fit the diff.
- **The dependents guard is unreachable surface and stays.** Two lenses said so, one dissented, and
  the dissent was traced to a false comment. **Not filed as an item.** It is a fail-closed assertion
  against a corrupted DAG and against future statuses, it costs one uncorrelated `NOT EXISTS` over
  an index that already exists, and it has both a negative and a positive control. Deleting
  unreachable defensive code on the strength of a reachability analysis is exactly the move this
  iteration disproved twice: the spec's reachability claim about case C was wrong, and correctness's
  reachability claim about the guard was wrong. **An item proposing to delete it would be an item
  proposing to trust a third reachability analysis.** Recorded here so the question is answered
  rather than rediscovered.
- **Every RED proof in the slice was re-run rather than trusted**, and the exercise paid: it is how
  the four non-discriminating fixtures and the one non-reddening mutation were found. Sixth
  iteration of that practice, and the fourth in which it refuted something.
- **The `jobs_cancel_test.go` byte-identity gate held.** The plan made "no existing assertion may be
  adjusted" a hard gate on the `GetJobForUpdate` swap, on the stated theory that an assertion
  needing adjustment IS the finding. No existing cancel assertion changed. Note that this gate did
  **not** catch Problems 1: the lock-ordering regression is invisible to a single-threaded test, so
  byte-identity proved behaviour preservation only in the dimension the existing tests measure. That
  is worth saying out loud, because the gate has now been credited three iterations running and this
  is the first time its blind spot has been demonstrated.

## Deferred Findings

**Filed this pass (one item, proposed for human review rather than treated as accepted):**

1. `bug-2026-08-13-single-job-responses-report-zero-total-tasks.md` (**bug/low**) -
   `jobResponse.TotalTasks`/`DoneTasks` are non-`omitempty` `int32` populated only by
   `applyJobEnrichment`, which only the list-row converters call. All five single-job responses
   (create, detail, cancel, retry, scheduled run-now) therefore assert `total_tasks: 0,
   done_tasks: 0`. The retry 200 can carry `tasks_retried: 3` and `total_tasks: 0` in the same
   object. Meanwhile `web/src/jobs/api.ts:105-106` states the detail endpoint does **not** return
   those fields, which is false for two of the four fields it names. Filed at **low** because no
   consumer is harmed today - the detail page derives progress from `tasks[]`, and both job
   mutations invalidate rather than seed the cache - and with the two triggers that would raise it
   written into the item. The recommendation is `*int32` plus `omitempty` so absence is
   representable, not populating five call sites.

**A second backlog item is closed by this work and the conductor should close it:**

- `docs/backlog/bug-2026-07-01-cancel-job-for-update-lock.md` (bug/low, open since 2026-07-01) asked
  for exactly `GetJobForUpdate` in `handleCancelJob`, with the terminal-state check inside the same
  transaction as the cancel writes. Both are now true (`internal/api/jobs.go:753-774`). Its third
  acceptance bullet, "coverage for the concurrent-cancel path", is satisfied **obliquely** rather
  than literally: there is a lock test (`TestGetJobForUpdate_TakesARowLockThatBlocksASecondReader`)
  and a cancel-versus-**retry** serialization test, but no cancel-versus-cancel interleave. Judgment:
  close it, and say in the Resolution that the coverage is cancel-versus-retry, so a reader looking
  for the concurrent-cancel test knows where the evidence actually is. This item was not scheduled
  for this slice and is closed as a side effect of a lock-order decision the backlog item never
  connected to it.

**Amendments to existing items (factual corrections, not scope changes):**

- `feature-2026-07-01-job-retry-action` states "the `POST /v1/jobs/{id}/retry` route **does not
  exist yet**", which is now false, and lists the jobs-stats bug as a blocker. **Recommended
  amendment:** record that the route landed in the 2026-08-13-job-retry-endpoint slice with the
  `?task=failed|all` contract the item already assumed, and that the jobs-stats blocker is
  discharged by the finding below rather than by a fix. Not applied here, because the item's Blocked
  section is its scheduling contract and editing it changes when a human would pick it up. The item
  is otherwise ready: its three-key invalidation and confirm-dialog plan need no revision, and the
  409-on-zero-match decision gives it one more branch to render.
- `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`'s Context asserts the proxy "would become
  inaccurate if a `POST /v1/jobs/:id/retry` endpoint is added". The endpoint is added and it does
  not. **Recommended amendment** to its Context, per spec acceptance criterion 12 which explicitly
  requires this be *proposed* and not applied: the trigger condition did not fire, the real residual
  is a transient undercount while a retried job re-runs, and the item's own cited comment in
  `JobStatusCounts` was separately false and is corrected in this branch. The item stays open on its
  original merits, which are about `updated_at` being a finish proxy at all.

**Considered and not filed, with reasons:**

- **Inverting `SelectRetryableTaskIDs`** - run `RetryJobTasks` first and only on zero rows run the
  selection, to tell case A (nothing matched) from case B (the guard blocked). This is a real
  simplification: it removes one query from every success path and deletes the case-C branch plus
  its trigger fixture. **Rejected.** What it costs is the ability to detect a partial application at
  all: with only a post-hoc selection, `0 < len(reopened) < len(selected)` is unrepresentable and a
  partial would commit and report success. The argument for accepting that cost is that case C is
  unreachable because the job row lock serializes retries - which is a **reachability analysis**,
  and this iteration refuted two of those (link 2 and link 4). Trading a fail-closed detector for
  one indexed query, on a path that already takes a row lock and runs a recursive walk, is the wrong
  side of that trade. Recorded here so the next reader who spots the redundancy finds the answer
  instead of the idea.
- **The unreachable dependents-guard 409 and its diagnostic log.** See Findings Triage. Not
  actionable; deleting it is the proposal, and the proposal is wrong.
- **The post-commit `broker.Publish` window.** After `tx.Commit`, the dispatcher can finish a
  one-task job before the handler publishes, so an SSE subscriber may latch a stale `running` after
  the job is already `done`. Identical shape in `handleCancelJob` (`internal/api/jobs.go:827-831`).
  **Rejected as a two-endpoint item.** Three things argue against filing it as scoped: the SPA has
  no consumer of the `job` event type at all today (only `useTaskLogStream` consumes SSE), `useJob`
  polls `['job', id]` every 3s so any latched value self-corrects within one interval, and the same
  window exists in three other publishers (`internal/scheduler/dispatch.go:380`,
  `internal/worker/handler.go:628`). An item scoped to cancel and retry would fix two instances of a
  five-instance pattern and imply the other three are fine. If it is ever filed it should be
  tree-wide and framed as "SSE job events are a status snapshot published outside the transaction
  that produced them", with the question being whether the event should carry a status at all rather
  than a bare "this job changed, refetch" nudge.
- **The ragged comment reflow at `internal/api/server.go:114-116`** ("handleRetryJob return 404 /
  (not 403)"). Cosmetic, one line for whoever next touches the file, cheaper to fix than to triage.

## Known Limitations

- **Nothing in this slice has been exercised by a browser or a human.** The endpoint is proven
  entirely by integration tests. Its consumer, `feature-2026-07-01-job-retry-action`, is a separate
  slice that does not exist, so the response shape is argued against the sibling cancel action and
  the frontend item's stated contract, not observed in a rendered page.
- **`make test` on Windows exercises almost none of this.** Every behavioural test is
  `//go:build integration`. The two exceptions are deliberate and narrow: the structural
  `IncrementTaskRetryCount` guard and the bounded-log unit tests. A green `make test` on this
  machine is a no-regression signal and **is never evidence that the endpoint works**.
- **Case C is proven by a trigger, not by a race.** The deterministic fixture is stronger evidence
  than a flaky interleave would be, and it is still a fixture: it proves the handler's branch, not
  that any real-world sequence reaches it. Nothing does today, by construction.
- **The dependents guard has never fired outside a planted fixture.** Both its controls seed the
  state directly. If the healthy state machine can reach it, no one has found the path.
- **The cancel/retry serialization test proves one interleave, not the lock order.** It asserts the
  end state is never (cancelled job, pending tasks). The ABBA deadlock the lock order prevents is
  not directly tested and is argued from the two handlers' statement order.
- **`TestConnect_NoCredentialRejected` was called a flake on non-reproduction alone.** No
  branch-versus-origin/main measurement was taken. See Problems 5.
- **`retry_count` reset is irreversible information loss** and is accepted deliberately: a job's
  lifetime attempt count is no longer recoverable from the row after an operator re-run.
- **Retrying concatenates log runs.** `task_logs` has no attempt or epoch column and nothing deletes
  rows, so after a retry the task-log view shows the previous run's output followed by the new run's
  with no separator. Scoped out in the spec with a proposed follow-up
  (`feature-2026-08-13-attempt-scoped-task-logs`, not filed this pass - it is a migration plus a
  log-view change and belongs in front of a human as a feature decision, not as retro cleanup).
- **Decisions 4, 6 and 8 were made without a human.** Refuse cancelled jobs, 409 on zero matched,
  accept the stats residual. Each is reversible and each is stated with the alternative it beat, and
  they remain the three most likely places a human would overrule this spec.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, eighth
  iteration running, and this is the highest-yield instance so far: two false premises, one
  inverted constraint, and one omission that turned out to be the largest piece of work in the
  slice.
- **A backlog proposal is not a contract** - eight for eight. This item was the most authoritative
  one yet written ("do not treat any bullet below as advisory") and three of its six constraints
  needed correction. Authority in an item's tone is not evidence about its accuracy.
- **Plan-supplied tests are untrusted** - honored and extended one level up. Here the *spec*-supplied
  test was the guess (link 2), and four *plan*-supplied fixtures did not discriminate (link 3). In
  every case the failure direction was a test that passes.
- **Re-running the implementer's own proofs is cheap and should stay standard** - honored; it is how
  links 3 and 5 were found.
- **A mutation proof must leave a test behind** - honored: each non-discriminating fixture was
  repaired so the discriminating input survives into a permanent test, rather than reverted with the
  mutation.
- **When a claim is refuted, grep the tree for its wording** - honored and it paid: the `timed_out`
  correction was found by grepping for a writer of the literal string, not by re-reading the
  argument.
- **Wrong prose about correct code is the dominant defect class** - ninth consecutive iteration, and
  this time it cost a review lens's conclusion rather than a future reader's afternoon.
- **A spec that reverses a human decision should be adjudicated by a lane briefed not to
  rubber-stamp it** - not applicable; no human decision was reversed. The three autonomous product
  judgments are flagged instead.
- **When the Go diff is empty, spend the integration lane on a real browser** - not applicable; the
  Go diff is the whole slice, and zero files under `web/` changed.
- **Backlog housekeeping is required scope** - the `/backlog close` on the item is the conductor's
  step and was running in parallel with this pass. Note the second item (`cancel-job-for-update-lock`)
  that also closes; it was not on anyone's list.

New from this iteration:

- **A claim's error rate tracks how many readers have acted on it, not who wrote it.** The backlog
  item, the only artifact with no successor to check it, had the most wrong claims - including its
  framing question, wrong about a fact it cited by file and line. Treat the head of a document chain
  as the least verified link, not the most authoritative one. **Candidate for durable memory.**
- **A conclusion that survives for a different reason than its stated one is not a confirmed
  conclusion.** The security lane's "nothing writes `timed_out`" was false; the safety property it
  supported was true, for a different reason. Right answer plus wrong reason reads exactly like
  verification. When re-checking a conclusion, check the reason separately from the conclusion.
  **Candidate for durable memory.**
- **Verify a lens's claim before relaying it as a brief** - carried from the previous iteration and
  **violated this iteration**, in the same direction. The conductor relayed "nothing writes
  `timed_out`" into an engineer's task brief unchecked and it reached a shipped comment. One
  iteration between the lesson and its repeat. The check is a grep and costs seconds; the reason it
  keeps being skipped is that a lens's claim arrives already argued, which is the same property that
  makes it persuasive.
- **When a refactor moves code across a lock, a transaction boundary, or an await, say so in the
  plan and name what now happens before what.** `git diff` renders an ordering change as a pure
  extraction. This is the third "pure refactor" in this project's recent history whose only real
  finding was a moved boundary, and it is the frontend "end the generation before releasing the
  resource" family seen from the server side.
- **A byte-identity gate on existing tests proves preservation only in the dimensions those tests
  measure.** It held here and did not catch the lock-ordering regression, because no existing cancel
  test is concurrent. Keep the gate, and stop citing it as evidence of behaviour preservation in
  general.
- **A test containing a numeric literal that also exists in production code is testing the
  literal.** `logIDHead` is the third instance of this family in four iterations (an interval, a
  constant, now a bound). Read the value off the production symbol, and prove RED by changing the
  symbol rather than the test.
- **A guard whose obvious negative test passes while the feature is inert needs a positive control
  in the same file, written at the same time.** Constraint 5's literal reading would have refused
  every retry in production and passed the test anyone would write first. When a predicate's job is
  to block, the test that matters is the one proving it lets the ordinary case through.
- **Do not delete unreachable defensive code on the strength of a reachability analysis.** Two
  reachability analyses were refuted this iteration, one in the spec and one from a review lens.
  Unreachable-and-cheap is a fine steady state; the burden on a proposal to remove such code is a
  proof, and a proof is what keeps turning out to be wrong.

## Files Most Touched

- `internal/store/query/tasks.sql` - the center of the slice. `SelectRetryableTaskIDs` at
  `:380-390`, `RetryJobTasks` at `:392-500` with roughly 80 lines of comment, of which the
  EvalPlanQual passage and the corrected `timed_out` passage at `:411-427` are the two that will be
  reread. Also `IncrementTaskRetryCount`'s forward reference at `:129-136` and `RequeueTaskByID`'s
  corrected doc comment at `:265-268`, the false sentence that misled a review lens.
- `internal/api/jobs.go` - `jobOwnerOr404` at `:679-719` (its comment block is the Problems 1 finding
  written at the site, including the `submitted_by`-immutability argument and what breaks if that
  changes); `handleCancelJob` at `:721-834` with the gate-then-lock ordering at `:730-758`;
  `retryJobResponse` at `:836-844`; `logIDHead` at `:846-849`; `handleRetryJob` at `:882-1080`.
- `internal/api/server.go:105-126` - the route plus the corrected Jobs block comment. Its old claim
  that only cancel is owner-or-admin gated became false the moment the route landed, which is the
  cheapest possible instance of the wrong-prose class: a sentence made false by the commit that
  makes it false.
- `internal/store/query/jobs.sql` - `GetJobForUpdate` with the two properties that depend on it
  written at the statement, and the corrected `JobStatusCounts` comment.
- `internal/store/incrementtaskretrycount_guard_test.go` - untagged, runs with no Docker, fails if
  `IncrementTaskRetryCount` appears outside `internal/worker/handler.go`. The backstop against the
  cheapest failure mode the item names: an implementer who reads the title, finds the existing retry
  statement, and reuses it, producing an endpoint that silently does nothing.
- `internal/store/retry_job_tasks_integration_test.go` - the store-layer proofs, including the
  concurrent interleave for the EvalPlanQual property and the matched negative/positive dependents
  pair.
- `internal/api/jobs_retry_integration_test.go` - the HTTP proofs, the non-owner-does-not-queue
  regression test from Problems 1, `installSkipUpdateTrigger` at `:393-409`, and the key-set
  equality at `:363-366` that is also the reason the filed backlog item exists.
- `internal/api/jobs_retry_log_test.go` - the bounded-log tests, rewritten against `logIDHead`
  itself; its header comment records what the previous version pinned.
- `internal/store/query/jobs.sql` `RecomputeJobStatus` - **not touched, deliberately**, and decision
  4's refusal of cancelled jobs is what keeps it out of this slice's blast radius.
- `docs/superpowers/specs/2026-08-13-job-retry-endpoint.md` - notable for its "Where the backlog item
  is wrong, incomplete, or right" section, which is the format that produced link 1 and is worth
  copying.
- `docs/superpowers/plans/2026-08-13-job-retry-endpoint.md` - notable for its "Deviations from the
  spec, and one spec claim that does not hold" section, which is link 2 and which a plan without
  that section would have silently implemented.

## Verification

- **This pass had no shell.** Every claim below that could be checked by reading was checked against
  the worktree; nothing was executed, and no `git log` or `git diff` was run by this pass.
- **Verified by reading:** the route registration and its middleware chain
  (`internal/api/server.go:126`) and the corrected block comment (`:105-120`); the absence of
  `admin(...)` on the cancel route (`:125`) and the `force` query parameter inside the handler
  (`internal/api/jobs.go:728`), which is the refutation of the item's framing premise;
  `jobOwnerOr404` and its ordering comment (`:679-719`); the gate-then-lock ordering in
  `handleCancelJob` (`:730-758`) and its status check on the locked row (`:768-774`); the
  post-commit publish (`:827-831`) and the un-enriched `toJobResponse(job, "", nil, nil)` at `:833`
  and `:1076`; `jobResponse`'s non-`omitempty` counters (`:70-71`) and `applyJobEnrichment`
  (`:122-140`); `retryJobResponse` (`:836-844`) and `logIDHead` (`:846-849`); the retry handler's
  entry point (`:882-891`) and its `SelectRetryableTaskIDs`/`RetryJobTasks` call sites
  (`:974-1014`); both new statements and their comment blocks in `tasks.sql` including the corrected
  `timed_out` passage (`:411-427`) and the corrected `RequeueTaskByID` comment (`:265-268`); the
  **falsity of the relayed security claim**, at `internal/agent/runner.go:233` and
  `internal/worker/handler.go:527`; the four new test files; the key-set assertion at
  `internal/api/jobs_retry_integration_test.go:363-366` and `installSkipUpdateTrigger` at `:393-409`;
  `internal/api/jobs_retry_log_test.go:43-88`; the SPA's contradicting type and comment at
  `web/src/jobs/api.ts:14-15,105-106`, `JobsTable.tsx:35`, `JobDetailPage.tsx:85-86` and
  `useJobActions.ts:20-23`; and the full text of the backlog item,
  `bug-2026-07-01-cancel-job-for-update-lock` and `feature-2026-07-01-job-retry-action`.
- **Reported by the implementing and verifying lanes, not re-run here:** `make test` green;
  `make test-integration` green; `go vet -tags integration ./...` clean; every mutation RED; the
  `TestConnect_NoCredentialRejected` pass at 2.12s; and the byte-identity of
  `internal/api/jobs_cancel_test.go`.
- **Not verified:** all test results, all mutation outputs, the generated-file read-back after
  `sqlc generate`, the exact commit count and diff stat of the branch, and anything requiring
  execution. Each is attributed above to the lane that reported it. The exact-file-set check and the
  final gate run are the conductor's, per the standing rule that subagent claims are verified
  against the tree rather than trusted.
- **Both backlog items were still in `docs/backlog/` when this was written** and neither was edited
  to look closed. `/backlog close feature-2026-08-13-job-retry-endpoint` was running in parallel with
  this pass; `/backlog close bug-2026-07-01-cancel-job-for-update-lock` is proposed above and has
  not been run. The second one's Resolution should state that the coverage is cancel-versus-retry
  rather than cancel-versus-cancel, so its third acceptance bullet is not read as literally met.
