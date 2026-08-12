---
date: 2026-08-12
topic: tasklog-append-assignee-fence
branch: claude/pr-merging-session-012b70
range: fb1ba84..3c8a27d
---

# Session Retro: 2026-08-12 - Task-Log Append Assignee Fence

**TL;DR:** Closed `bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero` by adding a third
predicate to `AppendTaskLog`'s fence CTE - `AND t.worker_id = sqlc.arg(worker_id)` - bound to the
connection's authenticated worker UUID, which `Connect` already holds from registration and which
never comes off the wire. Backend-only, one SQL predicate and one threaded parameter. What is worth
carrying forward is not the diff, it is five things the iteration taught: the **backlog item's own
framing hid the highest-impact case**; a **staged implementation was mandatory** to make RED
behavioral rather than a compile error; the engineer **stopped at a plan gate instead of pushing
through**, and the resulting redesign was strictly stronger than the spec's; **"unreachable" and
"unaddressable" are different claims** and a comment conflated them; and **three narrow review
lenses beat one broad pass**, including the conductor's own `/code-review`, which found nothing.
The work also surfaced a strictly worse sibling bug on the status path and deliberately did not fix
it.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md`, **plan**
  `docs/superpowers/plans/2026-08-12-tasklog-append-assignee-fence.md` (7 sequential tasks, one
  backend engineer; zero files under `web/`, so no frontend slice was allocated).
- `internal/store/query/tasks.sql` - the `AppendTaskLog` fence gains the assignee predicate, plus a
  comment block that states why the comparison must stay a plain `=`. `UpdateTaskStatus` also gained
  a comment: it writes `worker_id` without bumping the epoch, so clearing it there would strand a
  live agent's future chunks forever.
- `internal/worker/handler.go` - `handleTaskLog(ctx, workerID pgtype.UUID, chunk)`, the call site in
  `Connect`, and the `workerUUID.Scan` error promoted from `_ =` to a hard connection failure.
- Tests: two handler-layer exposure tests carrying the behavioral RED evidence, four store-layer
  cases pinning the fence, and a mutation run proving the NULL case is not vacuous.
- `CLAUDE.md` epoch-fence invariant amended in one sentence.
- `docs/backlog/closed/bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero.md` - closed via
  `/backlog close`, file moved, frontmatter stamped, Resolution appended.

## Key Decisions

- **Fence in SQL, not in Go.** A Go pre-check would need a `GetTask` round trip and would be a TOCTOU
  window against a concurrent requeue. One statement, one round trip, atomically.
- **`=`, never `IS NOT DISTINCT FROM`.** `tasks.worker_id` is nullable, so the comparison operator is
  the security control: `=` makes a never-claimed task reject every append and makes a caller that
  lost its identity (a zero-value `pgtype.UUID` binds SQL NULL) fail closed. See Problem 5.
- **Silent drop on rejection, with no forged-versus-stale signal.** Distinguishing costs either a
  second round trip or the "zero rows means no side effect" structural guarantee that CLAUDE.md's
  epoch-fence invariant leans on. A nullable inserted id is an `if` an implementer can forget, and
  forgetting it publishes forged content to live tailers. Detection routed to the audit-log item.
- **No proto change.** Adding a `worker_id` field to `TaskLogChunk` would be worse than useless - an
  attacker fills it in. The identity must come from registration.
- **Fail the connection when the worker UUID does not parse**, rather than preserving the silent
  discard. Before this change an unparseable id broke only inventory updates; after it, it would
  silently drop 100% of that worker's log output, which is a miserable thing to debug.

## Problems Encountered

1. **The backlog item's framing was wrong in a way that changed the shape of the work.** Its title
   and Summary said "any *never-claimed* task via Epoch 0". But the fence compares an integer - it
   has no notion of "never claimed". Epoch 0 is merely the cheapest guess. A task on first dispatch
   is at epoch 1 and epochs advance by one per requeue, so the guessable space for *any* task is a
   handful of small integers, probed one stream message at a time with no rate limit on the recv
   loop. The reachable set was every task in the database, **including one running live on another
   worker right now** - the case where a forged line does the most damage, because it interleaves
   with genuine output in an operator's live tail, and the exact case the item's framing hid. The
   spec states the window as a four-row state table instead of a sentence, and the closed item's
   Resolution records the correction. The habit: when an item names a *subset* of the reachable
   state, check what the predicate actually compares before accepting the subset.
2. **A staged implementation was required to make RED behavioral rather than a compile error.**
   Adding a parameter makes any new test fail to compile against the old code, and a compile failure
   proves nothing about behavior. Three stages, and the plan carried an explicit "do not collapse
   these" warning: **(1)** thread the identity with no SQL change - full suite green, behavior
   provably unchanged; **(2)** add the two handler tests - they compile and are RED *behaviorally*,
   because today's SQL ignores the argument they pass, so the forged chunk is genuinely stored and
   genuinely fanned out; **(3)** change the fence - the same tests, unmodified, go green. Stage 2's
   output is the acceptance evidence and it cannot be reproduced after stage 3 without reverting the
   fix. Worth generalizing beyond this change: **whenever a fix changes a signature, the RED step
   must precede the signature change, or the evidence is unobtainable.** A corollary the plan also
   resolved: the store-layer cases could *only* ever be a compile-error RED, because
   `AppendTaskLogParams.WorkerID` does not exist until the fix lands, so they were split out into
   their own task and given a mutation proof (Problem 5) instead.
3. **The engineer stopped at a gate instead of pushing through, and that was correct.** The plan's
   rule was "existing tests may gain an argument, never an assertion change - any test whose
   assertions need adjusting is a finding, not a chore". Two pre-existing tests tripped it:
   `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished` and
   `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` both used `CancelJobTasks` to end a
   generation, and cancel NULLs `worker_id` while bumping the epoch, so their positive controls were
   appending into an unassigned task. **The finding disproved a claim in the spec's own section 4**,
   which had asserted the two predicates could never disagree. They do disagree, on exactly that
   state. The resolution was to fix the **fixtures**, not the fence: requeue-then-redispatch
   (`RequeueTask` then `ClaimTaskForWorker`, since claim requires `pending` and cancel produces
   `failed`), which is the reachable equivalent. The redesign turned out **stronger than the
   original**, because the stale chunk is now sent as the task's genuine assignee, so its rejection
   is attributable to the epoch predicate alone - the test would have stayed green even if the epoch
   predicate were deleted, had the fixture been left alone. The spec carries a dated correction
   rather than being quietly amended.
4. **"Unreachable" and "unaddressable" are different claims, and a comment got it wrong.**
   `(epoch = current, worker_id = NULL)` **is** a perfectly reachable database state - `RequeueTask`
   and `CancelJobTasks` both produce it. What is true is narrower: no agent ever *holds* a matching
   epoch for it, because `ClaimTaskForWorker` is the only statement that sets a non-NULL `worker_id`
   and it bumps the epoch in the same atomic `UPDATE`, while every statement that clears `worker_id`
   also bumps. A reviewer caught the wrong word and named the concrete risk, which is the reason it
   mattered: an "unreachable" comment invites a future reader to trim the predicate as dead weight.
   The fixed comment now says the state is reachable but unaddressable, and says why. Precision in a
   comment is load-bearing when the comment's job is to stop someone deleting code.
5. **The NULL-comparison trap, and a regression test that had to be case 4.** Because
   `tasks.worker_id` is nullable, an implementer "fixing a NULL bug" with
   `IS NOT DISTINCT FROM` would re-open the exact hole this change closes, since a caller that failed
   to resolve its identity binds NULL. The spec proposed guarding this with a zero-value worker id on
   a *claimed* task - and **the planner caught that this would not catch the rewrite**: on a task
   claimed by W1, both `w1 = NULL` and `w1 IS NOT DISTINCT FROM NULL` are false, so that case is
   green under either operator. The rewrite is only observable when both sides are NULL, which needs
   a never-claimed task *and* a zero-value argument. That became case 4, and a mutation run confirmed
   exactly one case goes red under `IS NOT DISTINCT FROM` while cases 1 to 3 stay green. A regression
   test written after the fix has never been observed failing; mutating the source is how it earns
   its place.
6. **Three narrow review lenses beat one broad pass, and beat the conductor's own review.** The
   conductor's `/code-review` at high effort returned **zero findings**. The correctness lens
   partially refuted that and found two mediums; the security and invariants lenses independently
   found the same factual error in the spec's threat model (section 5 said an auto-enroll attacker
   picks an *unused* hostname; naming an in-use one is not blocked and is a full identity takeover -
   now item B below). Worth naming what a lens did that a generic pass did not: the correctness lens
   **spun up a real Postgres and executed the generated SQL** to confirm empirically that Postgres
   rejects a NUL byte at *bind time*, before the fence CTE is evaluated. That is what turned a test's
   scope note from an unverifiable assertion into a checked fact, and it is what makes item C's
   flood vector real rather than theoretical. Two lessons: **a zero-finding review is a data point,
   not a verdict**, and **a lens with a narrow brief will pay for a container to settle a question a
   broad pass would have hand-waved.**
7. **The iteration surfaced a strictly worse sibling bug and deliberately did not fix it.**
   `handleTaskStatus` has the identical missing identity check on the status path, with worse
   consequences (item A). The scope call was made at spec time and held: folding it in would have
   tripled the blast radius of a fix that should be trivially reviewable, and its fix is genuinely
   not a copy-paste, because `UpdateTaskStatus` has a second, server-internal caller whose identity
   story is different. It also earned its own finding during review - the third and worst consequence
   (a permanent wedge) was not in the spec's write-up and only came out of the security lens - which
   is itself an argument for the split: a deferred item got a full review pass it would not have
   received as a footnote in someone else's diff.

## Findings Triage

- **0 high, 2 medium, several low.** Both mediums were in the shipped spec's prose rather than the
  code: the auto-enroll "unused hostname" error (section 5) and the "reusing the `taskLogErrs`
  limiter would work" claim (section 7). Both were corrected inline with dated Correction blocks
  rather than by silent edit, so a reader can see what the spec originally believed. Note the
  pattern from the previous batch repeating: **a spec is a reviewable artifact, and its factual
  errors are findings.** Two of three lenses independently landed on the same one.
- The code itself drew no confirmed finding. That is consistent with the diff's size and with the
  fact that its two genuinely subtle decisions - the `=` operator and the staging - had already been
  argued out in the spec and pinned by tests before review ran.
- Four items proposed for the backlog, all accepted by the conductor and filed (see below). None was
  auto-filed.

## Deferred Findings

Filed rather than fixed, so they are not rediscovered:

1. `bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero` (**high**) - the same missing
   identity check on the status path. `DONE` on a never-claimed task marks work green that never
   ran and unblocks its dependents; `FAILED` cascades the transitive DAG in one message; and
   `RUNNING` writes `status='running'` with `worker_id` NULL, which is invisible to
   `GetEligibleTasks` and to every requeue path (all keyed `WHERE worker_id = $1`, which NULL never
   matches), permanently wedging the task and its downstream DAG.
2. `bug-2026-08-12-auto-enroll-hostname-takeover` (**medium**) - `UpsertWorkerByHostname` returns the
   *existing* row on conflict, so auto-enroll naming an in-use hostname seizes that worker identity
   and overwrites its token hash.
3. `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` (**medium**) - the persist-failure limiter
   keys on wire-supplied task id and epoch and resets on overflow, so fresh random UUIDs plus a NUL
   byte yield one `log.Printf` per message indefinitely on the recv goroutine.
4. `bug-2026-08-12-tasklog-epoch-int32-truncation` (**low**) - `int32(chunk.Epoch)` narrows a
   wire-supplied `int64`. Latent, not live: the assignee predicate now also has to match, and
   wrapping only reaches a task the sender already owns.

Also noted and **not** filed: the closed item's Resolution says the NULL-assignee state is
"unreachable for a real agent". That is qualified enough to be true, but it is one word away from
the imprecision Problem 4 was about; if that file is ever touched again, prefer "unaddressable".

## Known Limitations

- **The fence does not defend against an attacker who controls the assignee identity.** The spec's
  own stated acquisition path is "a job escaped and read the agent token on worker W" - and that
  attacker *is* W, so it can still forge into the tasks W is assigned. What the fence removes for it
  is every other task in the database. No server-side check on this path can do better.
- **Under `RELAY_ALLOW_AUTO_ENROLL` the fence is bounded by auto-enroll's trust model**, not a
  substitute for it: an attacker who can freely choose an identity is not constrained by a check on
  which tasks that identity may write to. That is item 2 above.
- **A forged chunk and a zombie chunk remain indistinguishable in the server log, and both are
  invisible.** Same posture as before, with the hole closed. Detection belongs with
  `feature-2026-06-26-audit-log-admin-console-actions`, where there is a durable sink.
- **Task ids are broadly discoverable.** `GET /v1/tasks/{id}` and `GET /v1/tasks/{id}/logs` are
  `auth(...)`-only with no per-owner gate, which is why half the attack was free. Out of scope here
  deliberately: it is a read-path policy decision across the whole API surface.
- **None of this is covered by `make test`.** Every test touched carries `//go:build integration`.
  The unit gate stays green even if all of it is broken, so it is a no-regression gate and never
  evidence for this change.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code during spec** - honored, and it paid
  the largest dividend of the iteration (Problem 1). The item's Proposal sketch was right about the
  column and wrong about the scope.
- **A backlog proposal is not a contract** - honored. The item's open question ("is there a
  legitimate path that appends logs for a task with no assignee?") was audited to a definite no
  rather than hedged with a blanket epoch-0 allowance.
- **A green test can be vacuous / gate a behavior-preserving change on byte-identical assertions** -
  honored twice: the gate fired (Problem 3) and the post-hoc regression test was mutation-proved
  (Problem 5).
- **Plan-supplied tests are untrusted** - honored. The planner caught that the spec's proposed
  NULL case would have been green under both operators.
- **Epoch fence / identity-checked teardown** - both in play and both reinforced. The teardown defer
  was moved above the new early return so a rejected connection cannot leave its sender registered.
- **Backlog housekeeping is required scope** - honored; `/backlog close` ran as a plan task.

New from this iteration:

- **When a fix changes a signature, stage it so RED precedes the signature change.** A compile-error
  RED is the vacuous-test failure wearing a different costume: it proves the code did not have the
  parameter, not that the behavior was wrong. **Candidate for durable memory** - it generalizes to
  every signature-changing bugfix, and the cost of getting it wrong is that the evidence is
  unrecoverable afterward.
- **"Unreachable" and "unaddressable" are different claims.** State which invariant makes a state
  unreachable *by whom*, because an over-broad claim in a comment is an invitation to delete the
  code the comment guards. **Candidate for durable memory**, closely related to the existing "a wrong
  contract in docs is a defect" note.
- **A zero-finding broad review does not close the question.** Narrow lenses with explicit briefs
  refuted it here, and one of them paid for a real database to settle a factual question rather than
  asserting it. Belongs as a sentence in the agent-team review guidance rather than as a new memory.
- **When a subset framing appears in an item's title, check the predicate.** "Never-claimed tasks
  via Epoch 0" was an accurate example presented as the boundary of the exposure. Same shape as the
  standing backlog-proposal note; worth one line appended there.

## Files Most Touched

- `internal/store/query/tasks.sql` - the source of truth. Carries the two-predicate fence, the
  "do not fix the NULL bug here" comment, and the new `UpdateTaskStatus` warning about clearing
  `worker_id` without bumping the epoch.
- `internal/store/tasks.sql.go` - regenerated by sqlc; content-only diff, LF churn reverted per the
  CLAUDE.md procedure. `models.go` and `internal/proto/` unchanged.
- `internal/worker/handler.go` - the threaded identity, the call site, the hard-failed `Scan`, and
  the rewritten `ErrNoRows` comment explaining that the fence now establishes two independent things.
- `internal/worker/handler_tasklog_integration_test.go` - `seedClaimedTask` now returns the assignee;
  the two new exposure tests with their positive controls; and the two rebuilt fixtures from
  Problem 3, whose comments carry the reachable-versus-addressable distinction from Problem 4 and the
  bind-time scope note from Problem 6.
- `internal/worker/handler_tasklog_e2e_integration_test.go` - mechanical forwarding only.
- `internal/store/store_test.go` - four assignee cases in `TestAppendTaskLog_EpochGuarded`, including
  the NULL-versus-NULL case 4 that the mutation run proved non-vacuous.
- `internal/worker/export_test.go`, `CLAUDE.md`, and the closed backlog item.

## Verification

- **Full integration suite green across all 20 packages**, including the p4d lane
  (`make test-integration`, `-p 1`, Docker Desktop on the `desktop-linux` context).
- **Unit gate green** (`make test`), recorded as a no-regression gate only - it exercises none of the
  new behavior.
- `go vet -tags integration ./...` clean, which is the real compile gate after a signature change.
- Behavioral RED captured verbatim before the fence change, showing the forged chunk both stored and
  published; the same tests green after, with both positive controls passing so a `handleTaskLog`
  that had stopped ingesting entirely could not have passed.
- Mutation check on the SQL: under `IS NOT DISTINCT FROM`, exactly case 4 goes red and cases 1 to 3
  stay green; tree restored by checkout and re-run green.
- Generated-file hygiene confirmed: real content change only in `tasks.sql`, `tasks.sql.go` and
  `store_test.go`; no line-ending-only churn; nothing hand-edited under `internal/store/*.sql.go`.
- Invariants spot-checked: one DB round trip and one statement on the recv path, no goroutine or
  queue added; no `Sender` / `Registry` / `sendCh` code touched; `.proto` unchanged; the teardown
  defer armed before the new early return.
- `/backlog close` performed; the item is in `docs/backlog/closed/` with `status: closed`,
  `closed: 2026-08-12`, `resolution: fixed` and a `## Resolution` note.
