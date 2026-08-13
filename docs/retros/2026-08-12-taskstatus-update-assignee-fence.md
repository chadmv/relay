---
date: 2026-08-12
topic: taskstatus-update-assignee-fence
branch: claude/pr-merging-session-012b70
range: aae22fe..782039b
---

# Session Retro: 2026-08-12 - Task-Status Update Assignee Fence

**TL;DR:** Closed `bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero`, the status-path
sibling of the log-path hole that merged an hour earlier. `handleTaskStatus` gained a **Go** identity
gate placed after its `GetTask` and ahead of all nine side effects, and `UpdateTaskStatus` gained
`AND worker_id = sqlc.arg(worker_id)` while **losing `worker_id` from its SET list** - the argument
became a fence, not a value. The implementation was correct on the first pass and drew no confirmed
finding against the shipped code, so almost all of this iteration's value is in review process:
a **mutation proof that leaves nothing behind proves nothing durable** (the correctness lens re-ran
the engineer's own mutation against the committed tree and everything stayed green); **a SQL-only
fix would not have closed the bug**, because the retry branch returns before the fenced statement;
**two lenses reached opposite conclusions on the same fact** and the conductor had to adjudicate;
**three of four lenses independently flagged the same test-only query**; and the conductor's own
`/code-review` returned zero findings for the second iteration running and was partially refuted
both times.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md`, **plan**
  `docs/superpowers/plans/2026-08-12-taskstatus-update-assignee-fence.md` (11 sequential tasks, one
  backend engineer; zero files under `web/`, so no frontend slice).
- `internal/worker/handler.go` - `handleTaskStatus(ctx, workerID pgtype.UUID, upd)`, the identity
  gate at `:474` with the currency gate kept as a separate `if` below it, the `Connect` call site,
  and `UpdateTaskStatus` now bound to the connection's own identity rather than the `task.WorkerID`
  just read.
- `internal/store/query/tasks.sql` - `UpdateTaskStatus` gains the worker predicate, drops
  `worker_id` from the SET list, and carries a comment block covering the `=`-not-`IS NOT DISTINCT
  FROM` rule, why one fenced statement beats a second un-fenced one, that the fence binds a worker
  and not a connection, and that the SQL predicate is **not** sufficient alone.
- `UpdateTaskStatusEpoch` gained a `TEST-ONLY ... must not gain a production caller` warning
  (`tasks.sql:211-218`), the direct product of a finding three lenses raised independently.
- Tests: four handler-layer exposure tests carrying the behavioral RED evidence, a permanent
  `TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask`, a `Connect`-wiring
  test driving the real message loop, and `TestUpdateTaskStatus_AssigneeGuarded`'s four store cases.
- `CLAUDE.md` Epoch fence bullet: the "remaining unfenced writer" clause retired, plus the new
  general rule **when you add a fence, enumerate what runs before it**.
- `docs/backlog/bug-2026-06-26-retry-resurrects-cancelled-task.md` annotated (stays **open**);
  `docs/backlog/closed/bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero.md` closed via
  `/backlog close`.

## Key Decisions

- **The identity check goes in Go, not only in SQL.** See Problem 2. This is the decision the whole
  change hinges on and it was made at spec time, not discovered during implementation.
- **One fenced query for both callers, no sentinel, no second query.** A sentinel meaning "skip the
  check" is reachable by any caller that merely failed to resolve its identity; a second un-fenced
  query leaves a writer a future caller can pick by mistake, silently choosing the unsafe one. The
  dispatcher's predicate is tautological by construction, which is the cheap side of the trade, and
  it fails closed and loudly because `failClaimedTask` already logs any error including
  `pgx.ErrNoRows`.
- **`worker_id` left the SET list.** See Problem 3.
- **Both `.Valid` checks stay, and both are documented as defense in depth rather than as live
  protection.** Against a real UUID they are redundant with the `Bytes` comparison, and
  `!workerID.Valid` is unreachable from `Connect`, which closes the stream on a Scan failure.
  Removing either one alone leaves the hole closed; removing **both** opens it, because
  `pgtype.UUID` is a comparable struct and a bare `!=` is the Go form of `IS NOT DISTINCT FROM`.
  The comment says exactly that, at that precision, so nobody deletes one as dead weight.
- **Silent drop on rejection**, matching the epoch gate. A log line here would be attacker-keyed
  volume on the recv goroutine with no sink; detection is routed to the audit-log item. Note this
  also removed a flood vector the SQL-only design would have *introduced*, since
  `handleTaskStatus` logs unconditionally when `UpdateTaskStatus` errors and `pgx.ErrNoRows` is an
  error.

## Problems Encountered

1. **A mutation proof that leaves nothing behind proves nothing durable.** This is the headline.
   During implementation the engineer verified the gate's two `.Valid` checks were load-bearing by
   mutating the gate to a bare `task.WorkerID != workerID` **and** editing
   `TestHandleTaskStatus_RejectsRunningForANeverClaimedTask` to pass a zero-value worker id, watched
   it go red, then reverted both. That is a correct and valuable check, and the plan asked for it.
   But the discriminating input was reverted along with the mutation, so the committed tree
   contained no test that could ever observe the regression. The correctness lens re-ran the exact
   same mutation against the committed tree and got **every task-status test green** - all seven
   then in the tree, including the one the mutation proof had used. The property had been proven
   once and locked never.

   This is a sharper form of the project's standing "a green test can be vacuous" lesson, and it
   deserves its own sentence: **a mutation proof is evidence about the moment it ran; a regression
   guard is a discriminating input that survives into a permanent test.** The two are not
   interchangeable, and a plan that asks for the first has not asked for the second.

   The fix was `TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask`, and
   *which branch it uses is the subtle part*. It sends `FAILED` at epoch 0 to a never-claimed task
   carrying `Retries: 1`, so the message takes the **retry branch**. A `DONE` or `RUNNING` variant
   would have looked equivalent and been silently weaker: with the Go gate deleted outright, the SQL
   fence on `UpdateTaskStatus` would still have rejected it, so the test would have stayed green
   against the mutation it was written to catch. Only the retry branch escapes the SQL fence, so
   only the retry branch can express the Go gate's own contract. Two properties had to hold at once
   (both sides of the comparison zero-valued, *and* the message routed past the SQL fence) and no
   other test in the package had both.

2. **A SQL-only fix would not have closed this bug, and the spec is what found that.** The spec
   mapped `handleTaskStatus`'s nine side effects in program order and found three of them
   (`IncrementTaskRetryCount`, `updateJobStatusFromTasks`, `NotifyTaskSubmitted`) sitting in the
   retry branch, which `return`s at `handler.go:522` before `UpdateTaskStatus` is ever reached.
   `IncrementTaskRetryCount` has a bare `WHERE id = $1` - no epoch fence, no worker fence. So a
   forged `FAILED` at the current epoch on a task that opted into retries would have sailed past an
   SQL-only fence and burned a retry, NULLed `worker_id` and bumped `assignment_epoch`, **evicting
   the agent legitimately running the task**, repeatably until retries were exhausted, after which
   the DAG cascade fires anyway. The backlog item's own Proposal was the SQL-only version, so this
   is the second iteration running where an item's proposal was incomplete in a way that changed the
   shape of the work.

   Generalized and now in CLAUDE.md: **when you add a fence, enumerate what runs before it.** A
   fence in a statement protects only the state that statement writes, and only for callers that
   reach it. The audit is mechanical: list every side effect the handler can produce, in program
   order, and mark which ones are gated on the fence having actually matched.

3. **`worker_id` became a fence rather than a value, and that invalidated a test the previous
   iteration had just added.** Removing `worker_id` from the SET list turned PR #119's *documented*
   contract ("callers MUST pass the task's existing worker_id through, or they strand a live agent")
   into a *structural* one: the statement can no longer clear the column at all, so the failure mode
   the comment warned about became unrepresentable. Good change. The consequence worth recording is
   that it made part of the previous iteration's
   `TestUpdateTaskStatus_TerminalTransitionKeepsTheAssignee...` vacuous - it asserted the assignee
   survives a terminal transition, which was a real property of the *caller's* discipline before and
   is now a property of the statement being incapable of doing otherwise.

   State it plainly: **a change can invalidate a test by fixing the thing the test was guarding.**
   The project's "gate a refactor on byte-identical tests" discipline does not catch this, because
   the test needs no edit at all - it simply stops discriminating. The tell is not a failing
   assertion, it is an assertion whose subject moved from behavior to structure. When a fix converts
   a documented contract into a structural guarantee, go read whatever was pinning the documented
   version.

4. **Two lenses reached opposite conclusions on the same fact, and the conductor had to
   adjudicate.** The engineer annotated `bug-2026-06-26-retry-resurrects-cancelled-task` with "what
   remains is exactly the cancel-during-retry race". The **security** lens confirmed that annotation
   as accurate. The **invariants** lens refuted it, and was right: a terminal transition
   deliberately does not bump `assignment_epoch` and now structurally keeps `worker_id`, so the
   task's own assignee can send `DONE` at epoch N (letting dependents dispatch) and then `FAILED` at
   the same epoch N. Both gates pass - it really is the assignee, the epoch really is current -
   `terminal && task.RetryCount < task.Retries` holds, and `IncrementTaskRetryCount` moves an
   already-completed task back to `pending` while its dependents are already running. Single actor,
   no concurrency, reachable by accident from a crash-looping agent.

   The instructive detail: the security lens's *own* separate finding described this exact route.
   It found the route and it confirmed the annotation, without connecting the two. So this is not
   "one lens was better"; it is that **a lens confirming someone else's claim is doing a different
   cognitive task than a lens deriving the fact fresh**, and the second is where refutations come
   from. That is an argument for lens independence being a feature rather than redundancy: had the
   lenses been merged into one pass, or had one been given the other's conclusions as input, the
   annotation would have shipped wrong.

5. **Three of four lenses independently flagged `UpdateTaskStatusEpoch`.** It is a second writer to
   `tasks.status` fenced on epoch only, with zero production callers, sitting in the same file as a
   newly written comment block arguing that a second un-fenced writer must not exist. The spec had
   already checked it and deliberately scoped it out, and recorded that "so a reviewer does not flag
   it" - three reviewers flagged it anyway. That is the correct outcome from a lens, not noise: a
   query that is safe only because nobody calls it is one import away from being unsafe, and
   "verified out of scope in a spec" is not visible from the code. The resolution was a `TEST-ONLY`
   warning in the query comment saying it has no production caller and must not gain one. Worth
   generalizing: **when a spec scopes something out because it is currently unreachable, the reason
   belongs in the code, not only in the spec** - otherwise every future reviewer re-derives it, and
   one of them eventually derives the wrong answer and calls it.

6. **The conductor's `/code-review` returned zero findings for the second iteration running, and was
   partially refuted both times.** Recorded honestly because two data points in a row is a pattern
   worth watching. The narrow lenses, each with an explicit brief, produced the mutation-proof gap
   (Problem 1), the annotation refutation (Problem 4) and the `UpdateTaskStatusEpoch` warning
   (Problem 5). This is not evidence that the broad pass is worthless - it is cheap and it is the
   input the lenses confirm or refute - but it is now clear evidence that **a zero-finding broad
   review must not be treated as a gate that closes the question.** The Phase 4 fan-out is doing the
   work.

## Findings Triage

- **0 high, and no confirmed finding against the shipped code itself.** The two substantive findings
  were about the *durability of the evidence* (Problem 1) and the *accuracy of a doc annotation*
  (Problem 4), plus the `UpdateTaskStatusEpoch` hygiene item (Problem 5). Both code-adjacent
  findings were fixed in-branch: a permanent test and a query comment.
- Continuing the pattern from the previous two batches: **the review's yield has moved off the
  diff and onto the artifacts around it** - specs, backlog annotations, and the evidence trail.
  That is what happens when a change is small, spec-argued and test-pinned before review runs.
- **Open doc defect, found while writing this retro and not yet corrected.** The refutation in
  Problem 4 was applied to `bug-2026-06-26-retry-resurrects-cancelled-task`, which now documents
  both routes correctly, but the refuted claim survives verbatim in two other artifacts: section 3.4
  of `docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md` ("the query's remaining
  exposure is exactly the cancel race described in that item and nothing else") and the Resolution
  of `docs/backlog/closed/bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero.md` ("narrows
  its remaining exposure to the cancel-during-retry race alone"). Both are exactly the text a future
  reader picking up the `06-26` item will find first. Each needs a dated Correction block, per the
  convention the previous iteration set. The lesson is the process one: **a refutation has to be
  chased through every artifact that repeated the claim, not just the one where it was raised** -
  and the artifact where a claim is *written down as settled* is usually not the one where the
  reviewer found it.
  - **Closed (2026-08-12).** Both artifacts now carry dated Correction blocks, applied by the
    retry-resurrect status-guard iteration
    (`docs/superpowers/plans/2026-08-12-retry-resurrect-status-guard.md`, Task 10): section 9
    item 6 of the spec, and Task 11 of the plan. The closed backlog item had already been
    corrected in-branch before it shipped. The `06-26` item that all of them undersize is
    itself closed by that iteration.

## Deferred Findings

- `bug-2026-06-26-retry-resurrects-cancelled-task` (**bug/high**) stays **open** and now carries
  both routes: the original cancel-during-retry interleaving, and the single-actor post-terminal
  resurrection from Problem 4. Its structural fix is a status predicate
  (`AND status NOT IN ('done','failed','timed_out')`) on both `UpdateTaskStatus` and
  `IncrementTaskRetryCount`, and explicitly **not** an epoch bump on terminal transitions, which
  would break the trailing-log flush.
- `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` (**medium**) broadened to cover the status
  path rather than duplicated. `handleTaskStatus` has two pre-gate `log.Printf` calls
  (`handler.go:426` bad task id, `:432` `GetTask` failure) that run **before both gates**, are not
  rate-limited at all, and are fully attacker-driven: any enrolled agent gets one unbounded log line
  plus one wasted database round trip per gRPC message by sending well-formed but nonexistent task
  UUIDs, synchronously on the recv goroutine ahead of that worker's real ingest. Neither is a log
  *injection* vector (`%q` on the unparsed id; the `%s` line is reachable only after the string
  parsed as a UUID), which is worth preserving explicitly rather than rediscovering.
- **Not filed, deliberately:** the connection-versus-worker identity question. Two lenses observed
  that the fence binds a worker row rather than a connection, so two concurrent streams sharing one
  agent token both pass the gate. It is pre-existing, identical on the log path, crosses no
  privilege boundary (the token *is* the worker's identity, so a second stream is the same
  principal, and the specs already record that the fence cannot defend against an attacker holding
  the assignee's own token), and fencing on `connection_epoch` would orphan a task's stream across a
  grace-window reconnect - which is exactly why the log-path spec chose the worker. The only
  actionable residue was that a reader might mistake the predicate for connection-scoped, and that
  is now closed by the comment at `tasks.sql:36-39`. See Known Limitations.

## Known Limitations

- **The fence binds a worker, not a connection.** Two concurrent streams registered for the same
  worker row both satisfy it, as does a superseded stream still draining during a reconnect. This
  is deliberate and is what keeps reconnect-within-the-grace-window working; `connection_epoch`
  machinery exists (`RegisterWorkerConnection`, `MarkWorkerOfflineIfEpoch`,
  `RequeueWorkerTasksIfEpoch`) and is used for *teardown*, where connection scope is the right
  scope. Do not "fix" the task fence to match it without redesigning the grace window.
- **The gate makes the retry branch unforgeable, not atomic.** `GetTask` and
  `IncrementTaskRetryCount` are separate statements with no re-check between them, so a concurrent
  writer can still move the row after the gate passed. That residual race is the `06-26` item.
- **An attacker holding worker W's token can still drive W's own tasks.** Unchanged from the log
  path, and no server-side check on this path can do better.
- **Under `RELAY_ALLOW_AUTO_ENROLL` the fence is bounded by auto-enroll's trust model**
  (`bug-2026-08-12-auto-enroll-hostname-takeover`), not a substitute for it.
- **A rejected status update is invisible.** Forged and stale are deliberately indistinguishable and
  neither is logged. Detection belongs with `feature-2026-06-26-audit-log-admin-console-actions`.
- **None of this is covered by `make test`.** Every test touched is `//go:build integration`. The
  unit gate is a no-regression gate and never evidence for this change.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, and it paid
  the largest dividend again (Problem 2). The item's Proposal would not have closed the bug.
- **A backlog proposal is not a contract** - honored; the spec deviated from the Proposal on the
  central design point and said why.
- **When a fix changes a signature, stage it so RED precedes the signature change** - honored, and
  this path got a stronger version than the log path: because `UpdateTaskStatusParams.WorkerID`
  already existed, the store-layer RED was behavioral too, not a compile error, so there was no
  excuse for a compile-error RED anywhere in the change and there was none.
- **Plan-supplied tests are untrusted** - honored in the breach and then repaired: the plan's
  mutation check was correct as far as it went, and it took a review lens to notice it left nothing
  behind (Problem 1).
- **Epoch fence** - extended with an identity clause and the "enumerate what runs before it" rule.
- **Backlog housekeeping is required scope** - honored; `/backlog close` ran as a plan task and the
  `06-26` annotation as another.

New from this iteration:

- **A mutation proof that leaves nothing behind proves nothing durable.** If a mutation had to be
  paired with a test edit to be observable, the edited test is the regression guard and it must be
  committed, not reverted. **Candidate for durable memory**, closely related to but sharper than the
  existing "a green test can be vacuous" note, and the cost of getting it wrong is silent: the tree
  looks tested.
- **When you add a fence, enumerate what runs before it.** Now in CLAUDE.md. Generalizes past
  fences to any late-placed guard: a check is only as strong as the earliest side effect it
  precedes.
- **A change can invalidate a test by fixing the thing the test was guarding.** Not caught by
  byte-identical-assertion discipline, because the test needs no edit - it just stops
  discriminating. **Candidate for durable memory.**
- **Lens independence is a feature.** Do not feed one lens another's conclusions, and treat a lens
  that only *confirms* a prior claim as having done less work than one that derived the fact fresh.
  Belongs in the agent-team review guidance alongside the existing "a zero-finding broad review does
  not close the question" note, which earned its second data point here.
- **When a spec scopes something out because it is currently unreachable, put the reason in the
  code.** Three reviewers independently spent effort on `UpdateTaskStatusEpoch` because its safety
  argument lived only in a spec.
- **Chase a refutation through every artifact that repeated the claim.** See Findings Triage; two
  shipped docs still carry the "cancel race alone" wording after the item itself was corrected.
  (Corrected 2026-08-12 by the retry-resurrect status-guard iteration; the lesson stands, the
  open defect does not.)

## Files Most Touched

- `internal/worker/handler.go` - the threaded identity, the identity gate at `:436-476` with its
  comment block (including the explicit note that the gate makes the retry branch unforgeable but
  not atomic, and that it does not close the `06-26` item), the currency gate kept separate below
  it, and `UpdateTaskStatus` now bound to the connection's own identity.
- `internal/store/query/tasks.sql` - `UpdateTaskStatus` (`:12-51`) and the `TEST-ONLY` warning on
  `UpdateTaskStatusEpoch` (`:211-218`).
- `internal/store/tasks.sql.go` - regenerated by sqlc; content-only diff, LF churn reverted per the
  CLAUDE.md procedure. `models.go` unchanged, `internal/proto/` untouched.
- `internal/worker/handler_taskstatus_integration_test.go` - new file, six tests: four exposure
  tests with positive controls, the permanent NULL-rejection test from Problem 1, and the
  `Connect`-wiring test that was itself mutation-proved by binding a zero-value UUID at the call
  site.
- `internal/store/store_test.go` - `TestUpdateTaskStatus_AssigneeGuarded` (four cases, case 4
  mutation-proved against `IS NOT DISTINCT FROM`) plus the two fixture repairs the spec predicted.
- `internal/worker/export_test.go`, `internal/worker/handler_test.go` (three mechanical call sites,
  no assertion changed), `CLAUDE.md`, and the two backlog items.

## Verification

- **Full integration suite green: 573 of 573 subtests across every package**, including the p4d
  lane (`-tags integration -p 1`, Docker Desktop on the `desktop-linux` context). `internal/api`
  alone took 487s, which is the dominant cost of a full run and worth budgeting for.
- **Unit gate green**, recorded as a no-regression gate only.
- `go vet -tags integration ./...` clean, the real compile gate after a signature change.
- Behavioral RED captured verbatim before the gate landed, on all four exposure tests: the forged
  DONE written, the DAG cascaded to `failed`, the retry burned, the never-claimed task wedged out of
  `GetEligibleTasks`. Store-layer RED likewise behavioral, with two cases writing the attacker's
  worker id into the row.
- Mutation checks, all three restored by checkout afterward: the bare `!=` on the Go gate (red, and
  now permanently red via the new test); a zero-value UUID at `Connect`'s call site (red on the
  wiring test); `IS NOT DISTINCT FROM` on the SQL (exactly case 4 red, cases 1 to 3 green).
- The hard gate held: the two `internal/scheduler` `failClaimedTask` tests passed **with no edit**,
  which is the evidence for the one-query decision, and no existing assertion anywhere was changed.
- Generated-file hygiene confirmed; nothing hand-edited under `internal/store/*.sql.go`.
- `/backlog close` performed on the status item; `bug-2026-06-26-retry-resurrects-cancelled-task`
  verified still open with both routes documented.
</content>
