---
title: RequeueTaskByID has neither an epoch fence nor a worker_id predicate, so a stale reconcile view can tear a fresh assignment off a live worker
type: bug
status: open
created: 2026-08-20
priority: medium
source: Phase 4 of the 2026-08-20-reconcile-canonical-task-ids slice; found independently by the invariants lens and the security lens
---

# RequeueTaskByID has neither an epoch fence nor a worker_id predicate, so a stale reconcile view can tear a fresh assignment off a live worker

## Summary

`RequeueTaskByID` (`internal/store/query/tasks.sql`) is the only requeue statement in the tree whose
`WHERE` clause names **nothing but the task id and a status allow-list**:

```sql
UPDATE tasks
SET status = 'pending', worker_id = NULL, started_at = NULL, finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = $1 AND status IN ('dispatched', 'running');
```

Its one production caller - `reconcileRunningTasks`'s requeue loop in `internal/worker/handler.go` -
**has both missing predicates in hand and passes neither**: `serverSet` holds
`int64(t.AssignmentEpoch)` for exactly this task, and `finishRegister` has already resolved the
connection's authenticated `workerID`. The statement therefore satisfies the epoch-fence invariant
only through its second branch ("conditionally end the assignment"), and satisfies the identity half
of that invariant **not at all**.

The contrast is inside the same file. The sibling requeue on the same handler,
`RequeueWorkerTasksIfEpoch`, is fenced on `connection_epoch` precisely so a stale grace timer cannot
requeue a reconnected worker's live tasks - a fix this project already shipped
([[bug-2026-06-19-finishregister-gap-connection-epoch-race]]). `RequeueTaskByID` is the same
operation reached from the same handler with no equivalent guard.

## Repro / Symptoms

Two overlapping registrations of the same worker W. Nothing serializes `Connect` per worker:
`finishRegister` calls `RegisterWorkerConnection` and then `reconcileRunningTasks` with no lock, and
the `connection_epoch` it obtains fences **teardown**, not reconcile.

1. Task T: `worker_id = W`, `status = 'running'`, `assignment_epoch = 5`.
2. Connections B and C both enter `finishRegister` for W. Both `GetActiveTasksForWorker` reads return
   `{T: 5}`. Neither report lists T (the agent crashed and restarted, so it is running nothing).
3. B's requeue loop calls `RequeueTaskByID(T)`. It matches: T becomes `pending`, epoch 6,
   `worker_id = NULL`. `triggerDispatch` fires.
4. The dispatcher claims T and dispatches it to W2: `status = 'dispatched'`, epoch 7,
   `worker_id = W2`.
5. C, still walking **its own** `serverSet` snapshot, calls `RequeueTaskByID(T)`. `'dispatched'` is in
   the allow-list, the id matches, and there is no other predicate - so **it matches**. T is torn off
   W2 mid-run, epoch bumped to 8, `worker_id` nulled.
6. W2's subprocess keeps running. Its status update at epoch 7 is fenced out silently by
   `UpdateTaskStatus`. T is dispatched a third time. **Duplicate execution, and not one log line
   anywhere** - the caller discards the return with `_ =`.

A second window reaches the same end without two concurrent `Connect`s: a grace timer for an older
connection of W that fires just before `finishRegister`'s `h.grace.Cancel(workerID)`. The requeue and
redispatch to W2 then land inside the window between reconcile's `GetActiveTasksForWorker` read and
its `RequeueTaskByID` write, and step 5 plays out identically.

Both are races. Neither needs an attacker, though a deliberate agent can widen the window by opening
two streams with the same token.

## Context

Found in Phase 4 of the `reconcile-canonical-task-ids` slice
(`docs/superpowers/plans/2026-08-20-reconcile-canonical-task-ids.md`), independently by the invariants
lens and the security lens. **Pre-existing, and that slice strictly narrows what reaches the
statement** - before it, a non-canonical id spelling sent live tasks down this path on every
reconnect; after it, only genuinely unreported tasks arrive. So this is not a regression, and the
slice made it rarer rather than more likely.

The statement's own doc comment reasons about exactly the right window and then defends the wrong
thing:

> ...it is the backstop for the window between that read and this write, and it is what keeps
> reconcile from being able to resurrect a terminal task.

The status allow-list does keep reconcile from resurrecting a **terminal** task. It does nothing
about a **fresh assignment** made inside that same window, which is the more damaging outcome, and
the comment does not mention it. That is this project's recurring "a principle stated in a comment is
not a check" shape - see `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`.

CLAUDE.md's Invariants state the rule this violates directly: "The epoch establishes currency, not
identity... any write that should be restricted to a task's assignee needs a second predicate on
`tasks.worker_id` matching the connection's authenticated worker... and that comparison must stay
NULL-rejecting - a plain `=`, never `IS NOT DISTINCT FROM`."

## Proposal

Make the requeue a fenced write:

```sql
UPDATE tasks
SET status = 'pending', worker_id = NULL, started_at = NULL, finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)          -- plain =, never IS NOT DISTINCT FROM
  AND status IN ('dispatched', 'running');
```

and pass, from `reconcileRunningTasks`, the epoch already sitting in `serverSet` plus
`finishRegister`'s authenticated worker id. Keep the status allow-list: it is a third, independent
guarantee (no terminal resurrection) and removing it would be a separate regression.

**Callers, checked before proposing the signature change.** There is exactly **one production
caller**: `reconcileRunningTasks`'s requeue loop, `_ = h.q.RequeueTaskByID(ctx, tID)`. Two test call
sites also break on the signature change and must be updated in the same commit:
`internal/store/store_test.go` (the requeue-clears-worker_id assertion, and
`TestRequeueTaskByID_BumpsEpochAndFencesStaleUpdates`). `internal/store/retry_job_tasks_integration_test.go`
mentions it only in a comment. Nothing in `internal/api`, `internal/scheduler` or `internal/cli`
calls it.

Two design questions to settle rather than assume:

- **What the caller does when zero rows match.** Today the return is discarded. Once the statement is
  fenced, "zero rows" becomes meaningful - it means somebody else ended this assignment first, which
  is the *correct* outcome, not an error. Do not add an unbudgeted log line here: reconcile runs
  inside `finishRegister`, before `Connect` allocates the connection's `ingestLogLimiter`
  ([[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]). A counter through
  the existing `Handler.Metrics` seam is the shape that fits, per
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]].
- **Whether `requeued > 0` should still gate `triggerDispatch`.** It currently counts *attempts*, not
  *matches*. With `:execrows` it can count matches, which is strictly better and removes a spurious
  dispatcher wake.

Carrying the epoch also lets the requeue loop hold `t.ID` directly instead of round-tripping the
canonical string back through `Scan` - see [[idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes]],
which is the natural companion change.

Needs `make generate` (sqlc) plus the CRLF revert dance, and a read-back of the emitted
`tasks.sql.go` body **and its doc comment** - the revert has silently discarded a regeneration in
this repo before.

## Acceptance / Done When

- A `RequeueTaskByID` call carrying a stale `assignment_epoch` moves **zero rows**, proven by a store
  test that is RED against today's statement.
- A call carrying the right epoch but a **different** `worker_id` moves zero rows, likewise RED.
- A call with a zero-value `worker_id` moves zero rows (fails closed on the missing value, the
  `=` versus `IS NOT DISTINCT FROM` property).
- The existing behaviour is preserved for the correct-epoch, correct-assignee case, with
  `TestRequeueTaskByID_BumpsEpochAndFencesStaleUpdates`'s assertions unchanged - only its parameter
  literal moves. An assertion needing adjustment IS the finding.
- A terminal task is still not resurrected (the allow-list arm still carries its own test).
- An integration test on the handler covers the repro above: two reconciles over one stale
  `serverSet` snapshot leave the second assignment intact.
- `internal/store/incrementtaskretrycount_guard_test.go`'s structural guard, and
  `TestTasksStatusVocabularyIsExactly`, are both checked against the new predicate set.

## Related

- Source: `internal/store/query/tasks.sql` (`RequeueTaskByID`, and `RequeueWorkerTasksIfEpoch` as the
  fenced precedent 200 lines away), `internal/worker/handler.go` (`reconcileRunningTasks`'s requeue
  loop, its only production caller; `finishRegister`, which resolves the authenticated worker id;
  `requeueWorkerTasks`, which passes the fence its sibling has)
- The slice that surfaced it and narrowed the path to it:
  [[bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones]] (closed),
  `docs/retros/2026-08-20-reconcile-canonical-task-ids.md`
- The same failure shape, fenced on `connection_epoch` rather than `assignment_epoch`, already fixed:
  [[bug-2026-06-19-finishregister-gap-connection-epoch-race]] (closed),
  [[bug-2026-06-10-stale-stream-teardown-clobbers-registration]] (closed)
- The pass that added the epoch **bump** to five requeue statements without adding the **fence**:
  [[bug-2026-06-10-requeue-paths-skip-epoch-bump]] (closed)
- **Must be read together with** [[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]]:
  one of the two shapes that item proposes is "requeue it (`RequeueTaskByID`)". If that shape is
  chosen, it adds a **second, periodic, non-agent-driven** caller of this unfenced statement and this
  item becomes a hard prerequisite rather than a race hardening.
- The companion cleanup on the caller side:
  [[idea-2026-08-20-key-reconcile-task-maps-on-raw-uuid-bytes]]
- A second path to running reconcile against another identity's tasks:
  [[bug-2026-08-12-auto-enroll-hostname-takeover]]

## Notes

**Filed at medium, deliberately, and here is the argument for and against high.** The consequence is
the worst one this subsystem has - duplicate execution of a live task with no signal anywhere - and
it is exactly the shape CLAUDE.md's epoch-fence bullet exists to prevent. Against high: it needs a
race (two overlapping registrations, or a grace expiry landing inside one reconcile's read-write
window), the shipped agent does not produce either on its own, and the 2026-08-20 slice removed the
one input that reached this path deterministically.

**The condition that should promote it to high is written down so nobody has to re-derive it:** if
[[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]] is specced with the requeue-shaped
fix, this stops being a race and becomes a scheduled writer with no fence, and it should be done
first or in the same slice.

Worth keeping even if the SQL looks obvious, because the transferable part is the question, not the
patch: **a statement can satisfy the epoch-fence invariant's "conditionally end the assignment"
branch and still be an unauthenticated write.** Bumping the epoch proves the caller ended *a*
generation. It says nothing about whether the caller was entitled to end *this* one.
