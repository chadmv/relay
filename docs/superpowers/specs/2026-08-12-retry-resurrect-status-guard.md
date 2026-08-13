# Guard the retry and status writers on task terminality, and make the retry atomic

- Date: 2026-08-12
- Backlog item: `docs/backlog/bug-2026-06-26-retry-resurrects-cancelled-task.md`
- Status: design approved (autonomous mode - see section 9)
- Owner phase: Phase 1 (SPEC) of the agent-team pipeline
- Third iteration of one hardening family. Predecessors:
  - `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` (PR #119) + its retro
  - `docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md` (PR #120) +
    `docs/retros/2026-08-12-taskstatus-update-assignee-fence.md`

## 1. What is already settled and must not be re-litigated

Cited, not repeated. Every one of these was argued to conclusion in the two predecessor
specs and their retros, and this spec depends on all of them:

- **The epoch establishes currency, not identity.** #119 sections 1 and 9.4; #120 section 1;
  now the CLAUDE.md Epoch fence bullet.
- **`=`, never `IS NOT DISTINCT FROM`,** because `tasks.worker_id` is nullable, and the Go
  analogue: `pgtype.UUID` is a comparable struct, so a bare `!=` is the Go form of
  `IS NOT DISTINCT FROM` and fails open when both sides are zero-valued. #119 section 6.2,
  #120 section 4.1, retro Key Decisions.
- **Stage the work so RED is behavioral, not a compile error.** #119 section 8.1, #120
  section 8.1.
- **A `Connect`-level test is required; a shim-only test leaves the wiring unpinned.** #120
  section 8.4.
- **A mutation proof must leave a permanent test behind.** The previous iteration's headline
  lesson (retro Problem 1): a mutation that had to be paired with a test edit to be
  observable proves nothing durable once both are reverted. Every mutation this spec asks
  for names the permanent test that carries its discriminating input.
- **Silent drop on a fence rejection,** with no forged-versus-stale signal and no log line on
  the recv goroutine. #119 section 7, #120 sections 3.3 and 4.1.
- **The fence binds a worker, not a connection,** and that is deliberate. #120 Known
  Limitations.

## 2. Verified facts (read in the tree on `claude/pr-merging-session-012b70`)

### 2.1 The two statements today

`internal/store/query/tasks.sql:53-58` - the whole of `IncrementTaskRetryCount`, comment and
all:

```sql
-- name: IncrementTaskRetryCount :one
UPDATE tasks
SET retry_count = retry_count + 1, status = 'pending', worker_id = NULL, started_at = NULL, finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = $1
RETURNING *;
```

No comment, no epoch predicate, no worker predicate, no status predicate. It is the last
writer to `tasks.status` in the repo with a bare `WHERE id = $1`. The item's core claim is
confirmed verbatim.

`UpdateTaskStatus` (`tasks.sql:12-51`) carries the three predicates PR #120 landed - `id`,
`assignment_epoch`, `worker_id` - and **no status predicate**. It does not bump the epoch and
no longer writes `worker_id`.

### 2.2 Callers

Repo-wide grep:

| Statement | Production callers | Test callers |
| --- | --- | --- |
| `IncrementTaskRetryCount` | exactly one: `handler.go:516` | `store_test.go:742` |
| `UpdateTaskStatus` | two: `handler.go:540`, `dispatch.go:355` | `store_test.go:129, 233, 242, 428, 1077, 1096, 1122, 1137` |

`UpdateTaskStatusEpoch` (`tasks.sql:211-222`) remains test-only and now carries the
`TEST-ONLY ... must not gain a production caller` warning PR #120 added. Untouched here.

### 2.3 Route A - the cancel-during-retry race - is live, exactly as filed

`handleTaskStatus` reads the row once (`GetTask`, `handler.go:430`) and calls
`IncrementTaskRetryCount` at `:516`. Between those two statements it re-checks nothing.
`CancelJobTasks` (`tasks.sql:235-246`) is deliberately un-fenced, matches
`status IN ('pending','queued','running','dispatched')`, sets `status='failed'`,
`worker_id=NULL`, and bumps `assignment_epoch`. Landing in that window it cannot be seen by
the bare `WHERE id = $1`, so the retry overwrites it: the task goes back to `pending` at
epoch N+2.

The item's step 4 also holds. `RecomputeJobStatus` (`jobs.sql:89-107`) has no notion of
`cancelled`; it unconditionally writes `running`/`done`/`failed` over whatever the job's
status was. A single non-terminal task therefore drags the job out of `cancelled` into
`running`, and the retry branch calls it (`handler.go:519`) and then wakes the dispatcher
(`:520`). Confirmed.

A variant the item does not name, and the epoch predicate is the only thing that closes it:
a **requeue** landing in the same window. `RequeueTask`, `RequeueTaskByID`,
`RequeueWorkerTasks` and `RequeueWorkerTasksIfEpoch` all set `status='pending'` and bump the
epoch. `pending` is not terminal, so a status predicate alone would let the stale retry
through - and if a dispatcher has already re-claimed the task to a second worker (status
`dispatched`, still not terminal), the stale retry knocks it back to `pending`, bumps the
epoch again and **evicts a live agent**. This is why section 3.2 does not stop at the status
predicate.

### 2.4 Route B - the single-actor, race-free resurrection - is live, in two shapes

A terminal transition through `UpdateTaskStatus` neither bumps `assignment_epoch` nor writes
`worker_id` (the argument is a fence, `tasks.sql:44-51`). So after a terminal status the row
still satisfies both of `handleTaskStatus`'s gates for its own assignee at the same epoch.
The `terminal` computation (`handler.go:512`) and the retry condition (`:515`) read only
`statusStr` and the T0 row's `RetryCount`/`Retries` - never the T0 row's `status`.

- **B1, `retries > 0`:** assignee sends `DONE` at epoch N (task `done`, dependents become
  eligible via `GetEligibleTasks`' `dep.status != 'done'` and dispatch), then sends `FAILED`
  at epoch N. Identity gate passes, currency gate passes, `terminal && RetryCount < Retries`
  holds, `IncrementTaskRetryCount` moves the **completed** task back to `pending` while its
  dependents are already running.
- **B2, `retries = 0`:** the same `DONE` then `FAILED`, but the retry branch is skipped and
  `UpdateTaskStatus` writes `failed` over `done` - the epoch and worker predicates both still
  match. `FailDependentTasks` then fires (`handler.go:554`) and marks every transitively
  dependent task that is still `pending` as `failed`. A completed task's downstream is
  destroyed by a duplicate message.

B2 is the shape the item does not state and it matters, because it proves the status
predicate is needed on `UpdateTaskStatus` too and not only on `IncrementTaskRetryCount`.

Reachability without an attacker: `Runner.Run` sends exactly one terminal status per
invocation (`internal/agent/runner.go:101-241`; every exit path is a single send and
`finalStatus` is one variable), and gRPC does not redeliver. So a correct agent never
produces this. A crash-looping, double-dispatching or modified agent does, and
`Agent.handleDispatch` (`agent.go:301-318`) does not deduplicate: a second `DispatchTask` for
the same task id spawns a second `Runner` and overwrites the map entry, so two terminal
statuses at the same epoch are one coordinator bug away. No second principal and no
concurrency required, exactly as the item says.

### 2.5 The trailing-log-flush constraint still holds - so the fix is a predicate, not a bump

Re-verified. `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`
(`store_test.go:401-449`) marks a claimed task `done` and then requires that
`AppendTaskLog` at the same epoch and worker **still succeeds**. Bumping `assignment_epoch`
on a terminal transition would make that append fail and would silently discard the tail of
every task's output. The item's instruction is correct and this spec adopts it: **status
predicate, never an epoch bump on terminal.** That test must stay byte-identical.

### 2.6 Audit: does anything legitimately write a terminal status onto an already-terminal row?

No. Exhaustively, because a missed producer would silently drop a real status transition.

- **`Dispatcher.failClaimedTask`** (`dispatch.go:353-386`) only ever runs on a row returned
  by `ClaimTaskForWorker`, which requires `status='pending'` and sets `dispatched`
  (`tasks.sql:141-146`). Its target is `dispatched` by construction. If something made the
  row terminal in between it also bumped the epoch (`CancelJobTasks`) and the existing epoch
  predicate already rejects. The status predicate is therefore tautological on this path -
  the same trade PR #120 accepted for the worker predicate, and acceptable for the same
  reason: it fails closed, it already logs (`dispatch.go:363-366`), and it documents the
  precondition at the statement.
- **`handleTaskStatus`** maps the wire enum at `handler.go:492-510`. A legitimate agent's
  per-epoch sequence is `[PREPARING (unmapped, returns)] -> RUNNING -> exactly one of
  DONE/FAILED/TIMED_OUT`, or a single `PREPARE_FAILED`. Never two terminals.
- **`timed_out` after `failed`.** There is no server-side timeout writer at all: grep for
  `timed_out` outside tests finds only the handler's enum mapping, the CLI's terminal-status
  checks, `RecomputeJobStatus`, the MCP wait helper and the migration's check constraint. The
  agent chooses one `finalStatus` and sends it once.
- **Re-delivered terminal messages.** gRPC streams do not redeliver, the agent buffers
  nothing across reconnects, and `buildRegisterRequest` (`agent.go:332`) reports only
  *running* tasks. A terminal status lost to a broken stream is lost, not retried.

Conclusion: after this change, "already finished" means "not writable", with zero legitimate
paths affected.

### 2.7 What runs before and after the retry write (CLAUDE.md's enumerate rule)

`handler.go:515-523`, in program order:

| # | Effect | Line | Gated on the write having succeeded? |
| --- | --- | --- | --- |
| 1 | `IncrementTaskRetryCount` | 516 | it is the write |
| 2 | `log.Printf` on any error | 517 | runs on **every** error, including `pgx.ErrNoRows` |
| 3 | `updateJobStatusFromTasks` | 519 | **yes** - `else` branch |
| 4 | `NotifyTaskSubmitted` | 520 | **yes** - same `else` branch |
| 5 | `return` | 522 | unconditional; the branch never falls through |

Good news the item does not record: effects 3 and 4 are **already correctly gated**. When the
new predicates reject, `err != nil`, the `else` is skipped, the job is not recomputed and the
dispatcher is not woken. Nothing needs restructuring. Only effect 2 needs a decision, and it
gets one in 3.3.

For completeness, everything after `UpdateTaskStatus` (`FailDependentTasks` `:554`,
`updateJobStatusFromTasks` `:559`, both `broker.Publish` calls `:561`/`:568`,
`NotifyTaskCompleted` `:577`) is gated by the `err != nil { return }` at `:548-551`, as PR
#120 verified. Unchanged.

## 3. Decisions

### 3.1 Question 2 - the status predicate, on both statements, in deny-list form

**Decision: `AND status NOT IN ('done','failed','timed_out')` on both `UpdateTaskStatus` and
`IncrementTaskRetryCount`.** Worked out per statement, because they have different callers
and different jobs:

- **`IncrementTaskRetryCount`.** Its job is "this generation failed; put the task back in the
  queue". A finished task has no generation to fail. The predicate is the direct expression
  of its precondition and it closes route B1 and (together with the epoch predicate) route A.
- **`UpdateTaskStatus`.** Its job is "advance this task's status machine". Per the 2.6 audit
  there is no legitimate terminal-onto-terminal transition, so the predicate rejects only
  duplicates and forgeries. It closes route B2, which nothing else can: B2 never touches
  `IncrementTaskRetryCount`.

**Deny-list, not the allow-list `status IN ('dispatched','running')`,** for one concrete
reason: `RecomputeJobStatus` (`jobs.sql:98`) already defines the project's terminal set as
exactly `('done','failed','timed_out')`, and the two must stay in lockstep. A status that
`RecomputeJobStatus` counts as non-terminal but this predicate rejects (or the reverse) is
precisely the split-brain that produced this bug. Both new predicates get a comment pointing
at `jobs.sql:98` as the canonical set. The vocabulary itself is bounded by
`tasks_status_check` (`migrations/000019_status_vocabulary_checks.up.sql:23`), so the two
forms are equivalent today; the deny-list is chosen for the cross-reference, not for
behavior.

Observation recorded so a reviewer does not chase it: `CancelJobTasks` matches a `'queued'`
status that the check constraint does not permit. Dead but harmless. Out of scope.

### 3.2 Question 3 - `IncrementTaskRetryCount` gains all three predicates

**Decision: `AND assignment_epoch = sqlc.arg(assignment_epoch)`,
`AND worker_id = sqlc.arg(worker_id)`, and `AND status NOT IN ('done','failed','timed_out')`.**
It stays `:one` and starts taking a params struct. Each predicate buys a property the others
do not:

| Predicate | Property it buys | The case that is red without it, and only without it |
| --- | --- | --- |
| `status NOT IN (terminal)` | "the task is not already finished" | route B1: `DONE` then `FAILED` at the same epoch by the same assignee. Epoch matches, worker matches. |
| `assignment_epoch = $` | "the generation I decided on is still current" - i.e. **atomicity** for the T0 read | requeue-then-reclaim **to the same worker**: task at epoch 3 assigned to W1, stale caller holds epoch 1 and W1. Status `dispatched` (not terminal), worker matches. |
| `worker_id = $` | "the row is still mine", and structurally: the statement cannot be called without naming an identity | a never-claimed task at epoch 0 with a zero-value or unrelated worker id. Status `pending` (not terminal), epoch 0 matches. |

The epoch predicate is the one that converts PR #120's stated residual - "the gate makes the
retry branch unforgeable, not **atomic**" (retro Known Limitations) - into an actual
guarantee, and it does so without a transaction: the caller already holds `upd.Epoch`, which
the currency gate at `handler.go:487` has already proved equal to the T0 `assignment_epoch`.
It also makes the retry **exactly-once per generation** under concurrency. Two streams for
the same worker (the deliberate limitation in #120) both reading at epoch N will serialize on
the row lock, and under READ COMMITTED the second `UPDATE` re-evaluates its `WHERE` against
the already-updated row, sees epoch N+1 and a NULL `worker_id`, and affects zero rows.

The worker predicate is *not* redundant with the epoch predicate, despite the audit that
`worker_id` never changes without the epoch changing. That audit says the two predicates
agree wherever `worker_id` is non-NULL; it says nothing about `(epoch 0, worker_id NULL)`,
which is the exact state both prior bugs targeted and where the epoch predicate matches on a
free guess. Adding it also keeps the rule stated with no exceptions to remember: **every
production writer to `tasks.status` now names the worker it is writing on behalf of** -
`ClaimTaskForWorker` by setting it, `UpdateTaskStatus`, `AppendTaskLog` and now
`IncrementTaskRetryCount` by matching it; the requeue/cancel family ends the assignment
instead, which is the invariant's other branch.

**The Go gate must NOT be simplified or removed.** Argued, not asserted, and the honest form
of the argument is uncomfortable:

1. After this change the Go gate is **no longer a correctness control**. With it deleted, a
   forged terminal from a non-assignee at the current epoch reaches
   `IncrementTaskRetryCount`, which now rejects on `worker_id`, and then `return`s; a forged
   message on a retries-exhausted task reaches `UpdateTaskStatus`, which rejects on
   `worker_id`. The observable state is identical. Say this plainly rather than pretending
   otherwise.
2. What it still buys is **one fewer database round trip per forged message, and no log
   lines at all.**

   > **Correction (2026-08-12, applied during Phase 4).** As drafted this point claimed "zero
   > database round trips and zero log lines per forged message", and all three review lenses
   > independently found it false. Three errors, recorded rather than quietly edited because
   > the same overstatement pattern was the previous iteration's finding too:
   > (a) **the log-line claim is refuted by this spec's own section 3.3** - the decision to
   > drop `pgx.ErrNoRows` silently at *both* write sites means a forged message rejected by
   > either fence logs nothing whether or not the gate exists, so the gate saves no log lines;
   > 3.2 and 3.3 contradicted each other and 3.3 is the one that shipped.
   > (b) **the round-trip claim is overstated** - `GetTask` runs before the gate either way,
   > so the true saving is one statement instead of two, not two instead of none.
   > (c) **the "zero attacker-keyed log lines" property never held for this function** - the
   > bad-task-id and `GetTask` error branches at the top of `handleTaskStatus` both log
   > unconditionally on `upd.TaskId`, ahead of the gate. `bug-2026-08-12-tasklog-err-limiter-attacker-keyed`
   > remains live on this path and this gate does not address it.
   >
   > The gate still stays, on point 3 (it asks a different question) and point 4 (defense in
   > depth) plus the one saved round trip. It just does not stand on the cost claim.
3. It answers a different question. The gate answers "may this sender drive this task's status
   machine at all"; the predicates answer "is the row still in the state the branch decision
   was made from". Merging them loses the first question, and the first question is the one
   `handleTaskStatus`'s branch structure actually asks.
4. Defense in depth against a future edit to either half.

**But the gate's own comment becomes false and rewriting it is required scope, not polish.**
`handler.go:441-447` currently reads, in part, "An SQL-only fence would leave a forged FAILED
on a task with retries free to burn a retry, NULL the worker_id and bump the epoch". After
this change that sentence is wrong, and leaving a wrong rationale in the code is a defect in
its own right - it invites the next reader to delete the gate for the reason the comment
gives, or to keep it for a reason that no longer holds. The replacement must say: the gate is
now a cost control and a second question, the SQL predicates are the correctness control, and
neither is to be deleted. Same for the `handler.go:449-455` paragraph asserting the retry
branch is "unforgeable, not atomic" and that the `06-26` item stays open.

Considered and rejected: **renaming the statement** to something like `RetryTaskForAssignee`
to advertise its new precondition. It would help, but it churns the call site and the tests
inside a security fix, and the same clarity is available from a query comment. Rejected;
recorded so it is not re-proposed as an oversight.

### 3.3 Question 4 - what happens on rejection

**Decision: `pgx.ErrNoRows` from `IncrementTaskRetryCount` is a silent drop. Every other error
keeps today's `log.Printf`. The `return` at `handler.go:522` stays - no fall-through.**

```go
if _, err := h.q.IncrementTaskRetryCount(ctx, params); err != nil {
    // ErrNoRows is the fence rejecting, not a failure: the task finished, was
    // cancelled, or the generation ended between the GetTask above and here.
    // Drop silently, exactly like the two gates. Any other error is real.
    if !errors.Is(err, pgx.ErrNoRows) {
        log.Printf(...)
    }
} else {
    updateJobStatusFromTasks(...)
    _ = h.q.NotifyTaskSubmitted(ctx)
}
return
```

Justified against the prior iterations' silent-drop decisions rather than by analogy alone.
All three of #119 section 7's reasons hold here: the rejection **is** the control and is
complete on its own; a log line is caller-controlled volume on the recv goroutine (bounded
now to the assignee spamming its own tasks, but still unbounded in volume, and a
crash-looping agent produces exactly this); and there is no sink - `internal/metrics` is a
utilization ring buffer, not a counter registry. The one argument for logging - that a
rejection here can mean a *genuine* lost retry, unlike the log path - does not survive
contact: in the genuine case the cancel or the requeue won, which is the correct outcome, so
there is nothing to diagnose. Detection routes to
`feature-2026-06-26-audit-log-admin-console-actions` with the rest of the family.

Same treatment for `UpdateTaskStatus`'s error branch at `handler.go:548-551`: `ErrNoRows`
becomes an expected outcome there too (route B2 duplicates), attacker- or bug-driven, on the
same goroutine. **`dispatch.go:363-366` is deliberately left loud**, because that path is
server-internal, its volume is bounded by dispatch cycles, and PR #120 cited its loudness as
a virtue.

Not falling through from the retry branch: if the row is terminal, `UpdateTaskStatus` would
reject on the same status predicate; if it was cancelled or requeued, on the epoch predicate.
Falling through would buy nothing but a round trip.

### 3.4 Considered and rejected

- **Bumping `assignment_epoch` on terminal transitions.** Breaks the trailing-log flush
  (2.5). This is the constraint the item names and it is confirmed live.
- **A `FOR UPDATE` re-read, or a transaction spanning `GetTask` and the retry.** Buys the
  same atomicity as the epoch predicate at the cost of a round trip and a lock held across
  application logic on the recv goroutine. The predicate is strictly cheaper and strictly
  more local.
- **Making `h.q` an interface so the route-A interleaving can be injected in a test.** A
  production structural change for test convenience, in a package PR #119 and #120 both
  deliberately left concrete. See 8.3 for what is tested instead.
- **A job-cancelled check inside the retry** (the item's "plus a job-not-cancelled check").
  Unnecessary: `CancelJobTasks` makes every affected task terminal *and* bumps its epoch, so
  the task-level predicates already reject. A join to `jobs` would add a second source of
  truth for a state the task row already carries.

## 4. Design

### 4.1 `IncrementTaskRetryCount`

```sql
-- name: IncrementTaskRetryCount :one
-- Burns one retry on a task whose CURRENT generation just failed, and returns it
-- to the queue. Three predicates, each answering a different question; none is
-- redundant with the others and none may be deleted:
--   assignment_epoch - CURRENCY. The caller decided to retry from a row it read
--     earlier; this proves that generation is still the current one. It is what
--     makes the retry atomic with respect to that read, so a cancel or a requeue
--     landing in the caller's TOCTOU window wins instead of being clobbered, and
--     what makes the retry exactly-once per generation when two callers race.
--   worker_id      - IDENTITY. The task must still be assigned to the caller's
--     worker. Must stay a plain `=`: tasks.worker_id is NULLABLE, so `=` makes a
--     never-claimed task (worker_id NULL, epoch 0 - a free guess) reject every
--     retry, and makes a caller that lost its identity (a zero-value pgtype.UUID
--     binds SQL NULL) fail closed. `IS NOT DISTINCT FROM` would re-open exactly
--     that. Do not "fix the NULL bug" here. Same rule as UpdateTaskStatus and
--     AppendTaskLog.
--   status         - TERMINALITY. A finished task has no generation to fail.
--     Without this, a task's own assignee can send DONE at epoch N and then
--     FAILED at epoch N - both gates legitimately pass, because a terminal
--     transition deliberately does NOT bump the epoch and does not clear
--     worker_id - and resurrect a completed task while its dependents are
--     already running. The terminal set is the one RecomputeJobStatus uses
--     (internal/store/query/jobs.sql:98); keep the two in lockstep.
-- The fix for that last case is this predicate and NOT an epoch bump on terminal
-- transitions: the assignment must survive completion so a trailing log chunk
-- from the agent that just finished still passes AppendTaskLog's fence. See
-- TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist.
-- pgx.ErrNoRows means "one of the three failed": drop, do not recompute the job
-- status, do not wake the dispatcher.
-- This statement is for the AGENT-DRIVEN retry only, and its preconditions are
-- the exact opposite of an operator re-run. POST /v1/jobs/{id}/retry must NOT
-- call it: that endpoint reopens tasks that ARE terminal and has no worker
-- identity to supply, so it needs its own statement with an explicit
-- `status IN ('failed','timed_out')` allow-list and its own epoch bump. See
-- docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md.
UPDATE tasks
SET retry_count = retry_count + 1,
    status = 'pending',
    worker_id = NULL,
    started_at = NULL,
    finished_at = NULL,
    assignment_epoch = assignment_epoch + 1
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)
  AND status NOT IN ('done', 'failed', 'timed_out')
RETURNING *;
```

`make generate` produces `IncrementTaskRetryCountParams{ID, AssignmentEpoch, WorkerID}` and
changes the Go signature to `IncrementTaskRetryCount(ctx, arg IncrementTaskRetryCountParams)`.
Follow CLAUDE.md's CRLF procedure after generating: `git diff --ignore-all-space`, keep the
content change, `git checkout --` the LF-only hunks.

### 4.2 `UpdateTaskStatus`

One new predicate and one new comment paragraph; the rest of the statement and its existing
comment block are untouched.

```sql
  AND status NOT IN ('done', 'failed', 'timed_out')
```

Comment addition:

```
-- A task's status machine is one-way into a terminal state. This predicate makes
-- that structural: an already-finished task cannot be written again, so a second
-- terminal message from its own assignee at the same epoch - which both other
-- predicates legitimately accept, because a terminal transition neither bumps the
-- epoch nor clears worker_id - cannot flip a `done` task to `failed` and cascade
-- FailDependentTasks across its still-pending downstream. The terminal set is the
-- one RecomputeJobStatus uses (jobs.sql:98); keep the two in lockstep.
-- Dispatcher.failClaimedTask's target is `dispatched` by construction
-- (ClaimTaskForWorker requires `status='pending'`), so this predicate is
-- tautological there, exactly like the worker predicate above and for the same
-- reason: one statement with no exceptions to remember.
```

### 4.3 Go changes in `handleTaskStatus`

- Pass the params struct at `handler.go:516`, binding `AssignmentEpoch: int32(upd.Epoch)`
  (safe for the same reason `:546` is: the currency gate at `:487` already proved the value
  fits in int32) and `WorkerID: workerID`, the connection's own identity - not
  `task.WorkerID`, matching the reasoning already written at `:535-539`.
- Split the two error branches per 3.3. Add the `errors` import if absent.
- Rewrite the two stale comment paragraphs at `:441-447` and `:449-455` per 3.2.
- No signature change to `handleTaskStatus`, no proto change, no change to `Connect`.

## 5. Failure modes, invariants, load

- **Invariants.** *Epoch fence* is the one in contact, and this change retires the clause in
  CLAUDE.md's bullet that names `IncrementTaskRetryCount` as satisfying the rule vacuously via
  an unconditional bump. That bullet must be amended (in scope): the statement now fences on
  epoch, identity and terminality; the "conditionally end the assignment" branch no longer has
  a live counter-example; and the "a SQL predicate alone is not always enough" sentence needs
  its justification updated, since the Go gate's role changes from correctness to cost.
  *One bounded sender per gRPC stream*, *identity-checked teardown*, *no interior pointers
  across locks*, *single JSON entry point*, *single job-spec pipeline*: not in contact.
- **Load.** No new round trip, no new statement, no new index. Three extra comparisons on a
  row already located by primary key. Under load the change is unmeasurable. Rejections get
  *cheaper* than today, because the log line goes away.
- **Failure mode on rejection.** No state change, no job recompute, no dispatcher wake, no
  publish, no log. A rejected retry is indistinguishable from a stale one, deliberately.
- **What a legitimate caller loses: nothing.** A real agent sends one terminal per generation
  on a non-terminal row it is assigned at the current epoch, which satisfies all three
  predicates. `failClaimedTask` is unaffected (2.6). The trailing-log flush is unaffected
  (2.5). Reconnect inside the grace window is unaffected - the fence binds a worker, not a
  connection.
- **New, deliberate behavior change to record:** a cancelled job now stays `cancelled`. Today
  the resurrected task drags it back to `running` via `RecomputeJobStatus`. Any test asserting
  the old behavior is a finding, not a chore.

## 6. Scope

**In scope**

- `internal/store/query/tasks.sql` - `IncrementTaskRetryCount` (three predicates + the comment
  block in 4.1), `UpdateTaskStatus` (one predicate + the comment addition in 4.2).
- `internal/store/tasks.sql.go` as regenerated by `make generate`.
- `internal/worker/handler.go` - the params struct at the call site, the two error-branch
  splits, and the two stale comment paragraphs.
- `internal/store/store_test.go` - the new store cases (8.2) and the one mechanical call-site
  repair at `:742`.
- `internal/worker/handler_taskstatus_integration_test.go` - the new route-B tests, the
  `Connect`-level test, and the comment repair on
  `TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask` (8.5).
- `CLAUDE.md` - the Epoch fence bullet (section 5).
- `/backlog close bug-2026-06-26-retry-resurrects-cancelled-task`, landing the file in
  `docs/backlog/closed/`. Required scope, not optional cleanup.
- A dated Correction block on section 3.4 of
  `docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md` and on the
  Resolution of `docs/backlog/closed/bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero.md`,
  both of which still carry the refuted "the remaining exposure is the cancel race alone"
  wording. The previous retro logged this as an open doc defect (Findings Triage) and this is
  the iteration that reads those artifacts, so it is the iteration that fixes them.

**Out of scope**

- **`POST /v1/jobs/{id}/retry`.** Tracked in
  `docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md`. Section 11 states what
  it must respect; it is not built here.
- **`UpdateTaskStatusEpoch`.** Test-only, warned in its comment, no production caller. Adding
  predicates to it would only make the fixture writes in `TestRecomputeJobStatus` and
  `TestIncrementTaskRetryCount_BumpsEpochAndFencesStaleRetry` fail for no security gain.
- **Making `RecomputeJobStatus` aware of `cancelled`.** A cancelled job whose tasks are all
  terminal recomputes to `failed`, which is arguably wrong, but it is a job-status-vocabulary
  question with its own blast radius and it is not reachable through this path once the
  predicates land. Propose as a backlog item; do not build it here.
- The dead `'queued'` arm in `CancelJobTasks` (3.1).
- `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` (already broadened to cover this path's
  two pre-gate `log.Printf` calls at `handler.go:426` and `:432`, which this change does not
  touch), `bug-2026-08-12-auto-enroll-hostname-takeover`, per-owner read authorization, and
  rate limiting the gRPC message loop. All unchanged from #119 and #120's scope sections.
- Any proto change. Deduplicating dispatch on the agent side (2.4).

## 7. Corrections to the backlog item

1. **The Proposal's "either/or" is wrong; it must be "and".** The item offers "either fence on
   `assignment_epoch` ... or add `AND status NOT IN (...)` ... or both". Only "both" works, and
   the third predicate the item never mentions - `worker_id` - is also required. Section 3.2
   gives the case that is red for each predicate and only for that predicate.
2. **Route B is understated in one direction.** The "Narrowed, not closed" note describes the
   `retries > 0` shape (B1). The `retries = 0` shape (B2) goes through `UpdateTaskStatus`
   instead, flips a `done` task to `failed`, and cascades `FailDependentTasks` across its
   still-pending downstream. That is why the status predicate is needed on `UpdateTaskStatus`
   and not only on `IncrementTaskRetryCount` - the note asserts both, correctly, but does not
   say why the second one is load-bearing.
3. **Route A has a variant the item does not name.** A *requeue* landing in the TOCTOU window
   leaves the task `pending` or, once re-claimed, `dispatched` - neither is terminal, so the
   status predicate does not reject it and the stale retry evicts a live agent. Only the epoch
   predicate closes it (2.3).
4. **The item's "plus a job-not-cancelled check" is unnecessary.** `CancelJobTasks` makes the
   task terminal and bumps its epoch in the same statement, so the task row already carries
   the state (3.4).
5. **Acceptance criterion 2 asks for something that cannot be tested honestly.** "A regression
   test covers the cancel-during-retry interleaving" implies an interleaving test. There is no
   seam to interleave on and a timing-based one would be flaky. Section 8.3 says what is
   tested instead and why it is equivalent.

## 8. Test strategy

Integration-only, as in both predecessors: `//go:build integration`, testcontainers, and
`make test` is a no-regression gate that exercises none of it. Verification runs
`make test-integration`, or at minimum
`go test -tags integration -p 1 ./internal/store/... ./internal/worker/... ./internal/scheduler/... -timeout 600s`,
plus `go vet -tags integration ./...` as the real compile gate after the signature change.

### 8.1 Staging, so RED is behavioral where behavioral RED exists

Five stages. **Do not collapse them.** Note the asymmetry and do not paper over it: the status
predicate's RED is fully behavioral at both layers, because obtaining it changes no signature;
the epoch and worker predicates on `IncrementTaskRetryCount` cannot have a behavioral RED at
the store layer, because the arguments they test do not exist before the change. Those two are
evidenced by mutation proof instead, and every mutation's discriminating input is permanent by
construction - no test edit is needed to observe any of them, which is exactly what the
previous iteration's Problem 1 demands.

1. **Route-B handler tests (8.4) and the status-predicate store cases (8.2 cases 2, 3 and 8).**
   These three are precisely the cases that compile against today's code: cases 2 and 3 call
   `IncrementTaskRetryCount(ctx, task.ID)` with today's single-argument signature, and case 8
   uses `UpdateTaskStatusParams`, which already exists. All are behaviorally RED. **Capture
   the output verbatim**: the `done` task resurrected to `pending` with `retry_count 1`; the
   cancelled task resurrected; the `done` task flipped to `failed` with its dependent
   cascaded. This is the acceptance evidence and it cannot be reproduced later.
2. **Add the status predicate to `UpdateTaskStatus`, regenerate.** The B2 handler test and
   store case 8 go green. No signature change, so nothing else moves.
3. **Add all three predicates to `IncrementTaskRetryCount`, regenerate,** update the single
   production call site (`handler.go:516`) and the single test call site
   (`store_test.go:742`), and rewrite cases 2 and 3 from exposure assertions into rejection
   assertions. The B1 handler test and store cases 2 and 3 go green. The error-branch split
   (3.3) lands here.
4. **Add the remaining store cases (8.2 cases 1, 4, 5, 6, 7) and the `Connect` test (8.6).**
5. **Run the mutation matrix (8.7), restoring by `git checkout` after each.**

### 8.2 Store layer - `TestIncrementTaskRetryCount_StatusEpochAndAssigneeGuarded`

New test in `internal/store/store_test.go`, modelled on `TestUpdateTaskStatus_AssigneeGuarded`
(`:1044`). Each case names which predicate rejects it, and case 1 is the positive control that
rules out "the statement stopped working at all".

1. **Positive control.** Task claimed by W1 at epoch 1, `Retries: 1`, status `dispatched`.
   Retry with (epoch 1, W1) -> succeeds; `retry_count` 1, status `pending`, epoch 2,
   `worker_id` NULL.
2. **Route A, cancel.** Fresh task claimed by W1 at epoch 1. `CancelJobTasks(jobID)` -> status
   `failed`, `worker_id` NULL, epoch 2. Retry with the arguments the handler captured at T0
   (epoch 1, W1) -> `pgx.ErrNoRows`, and a follow-up `GetTask` proves the row is untouched:
   still `failed`, still epoch 2, `retry_count` still 0. *Rejected by all three; this is the
   filed bug.*
3. **Route B, terminal.** Task claimed by W1 at epoch 1, then `UpdateTaskStatus` to `done` at
   (epoch 1, W1). Retry with (epoch 1, W1) -> `pgx.ErrNoRows`, row still `done` at epoch 1
   with `retry_count` 0. *Rejected by the status predicate alone - epoch and worker both
   match. This is the case that discriminates it.*
4. **Stale epoch, same worker, non-terminal.** Claim to W1 (epoch 1), `RequeueTask` (epoch 2,
   `pending`), `ClaimTaskForWorker` to **W1 again** (epoch 3, `dispatched`). Retry with
   (epoch 1, W1) -> `pgx.ErrNoRows`, and the row is still `dispatched` on W1 at epoch 3.
   *Rejected by the epoch predicate alone - status is not terminal and the worker matches.
   Reclaiming to the same worker is what makes it discriminating; reclaiming to W2 would let
   the worker predicate reject it too, and the case would stop isolating anything.*
5. **Exactly-once per generation.** Immediately after case 1, call the statement again with
   the same (epoch 1, W1) -> `pgx.ErrNoRows`, `retry_count` still 1. *The deterministic proxy
   for the concurrent-double-retry property (3.2); no goroutines, no sleeps. Note honestly
   that this case does not isolate one predicate: the first retry both bumped the epoch and
   NULLed `worker_id`, so epoch and worker each reject it independently. It goes red only if
   both are removed, which is the same defense-in-depth shape as #120's two `.Valid` checks
   and must be commented as such so nobody reads it as an epoch test.*
6. **Never-claimed, real worker.** Fresh unclaimed task, epoch 0, `worker_id` NULL. Retry with
   (epoch 0, W1) -> `pgx.ErrNoRows`, row unchanged. *Rejected by the worker predicate alone -
   `pending` is not terminal and epoch 0 matches.*
7. **Never-claimed, zero-value worker id.** A second unclaimed task, retry with
   (epoch 0, `pgtype.UUID{}`) -> `pgx.ErrNoRows`. *The regression test for `=` versus
   `IS NOT DISTINCT FROM`: only NULL-versus-NULL discriminates the two operators, per #120's
   section 8.3 and the sibling's Problem 5. Case 6 does not catch that rewrite.*

`TestUpdateTaskStatus_AssigneeGuarded` gains one case in the same shape:

8. **Terminal-onto-terminal is rejected.** Task claimed by W1, moved to `done` at (epoch 1,
   W1). A second `UpdateTaskStatus` to `failed` at (epoch 1, W1) -> `pgx.ErrNoRows`; a
   follow-up `GetTask` proves status is still `done` **and `finished_at` is unchanged**.
   *Rejected by the status predicate alone.* Repeat once on a separate task with `timed_out`
   over `failed`, since that is the ordering a reviewer will ask about (2.6).

### 8.3 Route A: what is honestly testable, stated plainly

**The cancel-during-retry *interleaving* is not deterministically testable at the handler
layer, and this spec does not propose a flaky test for it.** The window is between `GetTask`
(`handler.go:430`) and `IncrementTaskRetryCount` (`:516`), inside one function that takes a
concrete `*store.Queries`. Reaching it requires either an injectable seam in production code
(3.4, rejected) or a timing-based interleave (rejected outright). Note also that applying the
cancel *before* the handler runs proves nothing: `GetTask` then reads the post-cancel row,
`worker_id` is NULL, and the Go identity gate rejects before any of the new predicates is
reached.

**The honest test is store case 2**, and it is not a weaker substitute. The handler's entire
contribution to route A is the pair of values it captures at T0 and passes at T1; case 2 calls
the statement with exactly those values against exactly the post-cancel row state. It tests
the predicate that is the fix, in the state that is the bug, with no timing dependence.

**The wiring is pinned separately, and by route B rather than route A.** The B1 handler test
(8.4) reaches `IncrementTaskRetryCount` through the real `handleTaskStatus` with the new
arguments, so if the call site bound the wrong epoch, a zero-value worker, or `task.WorkerID`
in a way that diverged, that test fails. Route B is what makes the wiring observable end to
end; route A is what makes the predicate observable. Together they cover what an interleaving
test would have covered, without the flake.

### 8.4 Handler layer - `internal/worker/handler_taskstatus_integration_test.go`

Both tests seed a job, a worker W1, and a task claimed by W1 at epoch 1, and send the task's
**current** epoch throughout so that identity and currency both pass and only terminality can
reject.

- **`TestHandleTaskStatus_AssigneeCannotResurrectItsOwnCompletedTaskViaRetry`** (B1).
  `Retries: 1`. W1 sends `DONE` at epoch 1; assert `done`. W1 then sends `FAILED` at epoch 1;
  assert the task is **still `done`**, `retry_count` still 0, `assignment_epoch` still 1, and
  `worker_id` still W1. Add a dependent task B: after the `DONE`, B is returned by
  `GetEligibleTasks`; after the second `FAILED`, **B is still returned by
  `GetEligibleTasks`** - that assertion is the one that expresses the actual harm (a
  resurrected task while its dependents are already dispatchable), not a status string.
  *RED today*: the `FAILED` burns the retry, A goes `pending` at epoch 2 with `worker_id`
  NULL, and B drops out of the eligible set because its dependency is no longer `done`.

- **`TestHandleTaskStatus_ASecondTerminalFromTheAssigneeDoesNotOverwriteOrCascade`** (B2).
  `Retries: 0`, dependent task B left `pending`. W1 sends `DONE` at epoch 1, then `FAILED` at
  epoch 1. Assert A is still `done` with its original `finished_at`, and **B is still
  `pending`**. *RED today*: A becomes `failed` and `FailDependentTasks` marks B `failed`.
  The B assertion is the discriminating one; asserting only A's status would leave the cascade
  - the real damage - unpinned.

- **Positive controls, on the same code path, in both tests.** A separate task claimed by W1
  at epoch 1 that has *not* been completed: W1's `FAILED` still burns the retry (B1) and still
  cascades to its dependent (B2). Without these, a `handleTaskStatus` that had stopped
  accepting anything at all would pass every assertion above.

These two tests also isolate the two statements from each other, which is worth stating in
their comments: B1 can only be red for `IncrementTaskRetryCount`'s status predicate (its
`FAILED` takes the retry branch and never reaches `UpdateTaskStatus`), and B2 can only be red
for `UpdateTaskStatus`'s (its `FAILED` skips the retry branch). Removing either predicate
reddens exactly one of them.

### 8.5 An existing test stops discriminating, and that is a finding to record, not to fix silently

`TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask` was added by the
previous iteration specifically as the permanent guard for its mutation proof (retro Problem
1). It sends `FAILED` at epoch 0 with a zero-value worker id to a never-claimed task carrying
`Retries: 1`, and it is discriminating today **only because the retry branch escapes the SQL
fence**. After this change `IncrementTaskRetryCount` rejects that call on its own worker
predicate, so the test stays green even with the Go gate deleted entirely.

This is the previous retro's Problem 3 pattern - *a change can invalidate a test by fixing the
thing the test was guarding* - recurring one iteration later on the very test that was written
to fix Problem 1. Required response:

- **Do not delete it.** It remains a valid guard, at the SQL layer, on the `=`-not-
  `IS NOT DISTINCT FROM` rule reached through the real handler.
- **Amend its comment** to say exactly that its discriminating power moved from the Go gate to
  the SQL predicate on `IncrementTaskRetryCount`, and that it is no longer evidence about the
  Go gate.
- **Record in Known Limitations** that after this change no test in the tree discriminates the
  Go gate's presence, because its remaining value is non-functional. That is honest and it is
  the correct outcome, but it must be written down or the next reviewer will delete the gate
  and see a green suite.

  > **Correction (2026-08-12, Phase 4).** This bullet originally described that remaining
  > value as "zero round trips, zero attacker-keyed log lines". Both halves are wrong, and
  > section 3.2's Correction has the full account: the saving is *one* round trip instead of
  > two (`GetTask` runs ahead of the gate either way), and it is *zero* log lines saved, since
  > this same change wraps both write sites in `!errors.Is(err, pgx.ErrNoRows)`. The gate
  > still earns its place; it just does not earn it for these reasons.

### 8.6 `Connect` wiring

**`TestConnect_ASecondTerminalOverTheRealMessageLoopDoesNotResurrectTheTask`**, modelled on
the existing `TestConnect_TaskStatusIsFencedOnTheConnectionsOwnWorker`. `seedClaimedTask` for
hostname H (auto-enroll upserts by hostname and returns the existing row's id, so the
connection resolves to the task's assignee), `Retries: 1`, drive a real `Connect` with
`AllowAutoEnroll`, include the task in `RunningTasks` at its current epoch so
`reconcileRunningTasks` does not requeue it and move the epoch for an unrelated reason, then
send `DONE` and `FAILED` back to back at that epoch on the one stream. Assert the task ends
`done` with `retry_count` 0, and assert the register response's `worker_id` equals the seeded
assignee, or the test proves nothing.

Be honest about what this adds relative to #120's version: this change threads no *new*
parameter down from `Connect`, so the shim-versus-`Connect` gap is narrower than it was in the
two predecessors. What it does pin is the **scenario** - two messages on one real stream in
sequence, which is precisely how a crash-looping agent produces route B - and it is the only
test that proves route B is unreachable through the real recv loop rather than through the
exported shim. That is worth having and it is cheap.

### 8.7 Mutation matrix

Each row: apply the mutation, run the named tests, confirm the predicted reds, `git checkout`
the file. Every discriminating input below is already permanent - **no test edit is required
to observe any of these**, which is the requirement from the previous iteration's Problem 1.

| # | Mutation | Must go red | Must stay green |
| --- | --- | --- | --- |
| M1 | drop `status NOT IN (...)` from `IncrementTaskRetryCount` | store case 3, handler B1 | store cases 1, 2, 4, 5, 6, 7; handler B2 |
| M2 | drop `assignment_epoch = $` from `IncrementTaskRetryCount` | store case 4 | store cases 1, 2, 3, 5, 6, 7; both handler tests |
| M3 | drop `worker_id = $` from `IncrementTaskRetryCount` | store cases 6 and 7 | store cases 1, 2, 3, 4, 5; both handler tests |
| M4 | rewrite that `worker_id` predicate as `IS NOT DISTINCT FROM` | store case 7 **only** | store cases 1-6; both handler tests |
| M5 | drop `status NOT IN (...)` from `UpdateTaskStatus` | store case 8, handler B2 | `TestUpdateTaskStatus_AssigneeGuarded` cases 1-4, `TestUpdateTaskStatus_EpochGuarded`, the terminal-transition test, handler B1 |
| M6 | drop **both** the epoch and worker predicates from `IncrementTaskRetryCount` | store case 5 (in addition to 4, 6 and 7) | - |

Two rows carry most of the weight and must not be relaxed:

- **M4's "only".** If any case other than 7 also goes red, the suite is not isolating the NULL
  semantics and the cases need re-arranging, not the assertion relaxing.
- **M6.** Case 5 is green under M2 and M3 individually and red only under both, which is the
  point of listing it separately rather than mis-attributing it to one predicate. Do not
  "simplify" the matrix by folding M6 into M2.

### 8.8 Existing tests: predicted changes versus findings

**Predicted and permitted** (mechanical, not assertion changes):

- `store_test.go:742` - `TestIncrementTaskRetryCount_BumpsEpochAndFencesStaleRetry` calls
  `q.IncrementTaskRetryCount(ctx, task.ID)` on a task claimed by `w` at epoch 1, status
  `dispatched`. It gains the params struct with `AssignmentEpoch: claimed.AssignmentEpoch` and
  `WorkerID: claimed.WorkerID`. All three predicates hold, so **every assertion in it must
  stay byte-identical**. If any needs adjusting, that is a finding.
- `handler.go:516` - the one production call site.

**Must stay byte-identical, and any needed edit is a finding, not a chore:**

- `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`
  (`store_test.go:401`). It writes `dispatched -> done`, which the new predicate permits, and
  it is the test that forbids the epoch-bump alternative (2.5). Its passing unchanged is the
  evidence for 3.1.
- `TestUpdateTaskStatus_AssigneeGuarded` cases 1-4 (`:1044`) and
  `TestUpdateTaskStatus_EpochGuarded` (`:206`). Every existing `UpdateTaskStatus` call in the
  tree writes onto a non-terminal source row - audited at `:129`, `:233`, `:242`, `:428`,
  `:1077`, `:1096`, `:1122`, `:1137`.
- Both `internal/scheduler` `failClaimedTask` tests. **Their passing unchanged is the evidence
  for the "tautological on the dispatcher path" claim in 3.1**, exactly as PR #120 used them.
- Everything in `internal/api` (including the cancel-job tests - and note the new behavior in
  section 5, that a cancelled job now stays `cancelled`; if an existing test asserted
  otherwise it was pinning the bug), `internal/worker`'s teardown, reconcile and log-path
  tests, and `TestRecomputeJobStatus` (which writes fixtures through `UpdateTaskStatusEpoch`,
  untouched).

## 9. Assumptions / decisions made autonomously

Autonomous run, unattended `/autopilot` batch, no human available. Each call and its reasoning:

1. **Status predicate on both statements, in deny-list form keyed to `RecomputeJobStatus`'s
   terminal set** (3.1). The alternative allow-list is equivalent today and loses the
   cross-reference that keeps the two definitions in lockstep.
2. **`IncrementTaskRetryCount` gets all three predicates, not just status** (3.2). The status
   predicate alone leaves the requeue variant of route A open, and the epoch predicate alone
   leaves the never-claimed epoch-0 state open. Each has a case in 8.2 that is red for it and
   only for it (cases 3, 4 and 6/7 respectively).
3. **The Go gate stays, and its role is honestly downgraded from correctness to cost** (3.2).
   The uncomfortable half - that the SQL alone would now be sufficient for state correctness -
   is stated rather than hidden, because the comment currently in the tree asserts the
   opposite and a wrong rationale in code is a defect.
4. **Silent drop on `pgx.ErrNoRows` in both of `handleTaskStatus`'s error branches; loud in
   `dispatch.go`** (3.3). Consistent with #119 and #120, and the one argument specific to this
   path (a genuine lost retry might be worth diagnosing) does not survive: the genuine case is
   correct behavior.
5. **No interleaving test for route A; store case 2 is the honest test, and route B pins the
   wiring** (8.3). The instruction not to propose a flaky test is taken literally, and the
   alternative - an injectable seam in `internal/worker` - is a production change for test
   convenience that both predecessors declined.
6. **The existing zero-value-worker-id test is kept, re-commented and recorded as no longer
   discriminating the Go gate** (8.5) rather than deleted or reinforced with a log-capture
   test. A log-capture assertion would work (there is precedent in
   `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`) but it would pin a
   non-functional property with a globally-scoped mechanism; recording the limitation is the
   proportionate response.
7. **No rename of `IncrementTaskRetryCount`** (3.2), despite its precondition changing
   materially. Comment instead of churn, inside a security fix.
8. **The two stale artifacts from the previous retro's Findings Triage are corrected here**
   (section 6). They are about this item, this iteration reads them, and the retro logged them
   as open. Filing a separate backlog item for two comment blocks would cost more than fixing
   them.
9. **Priority left at the item's `high`.** Route B is reachable by accident with no attacker,
   which argues higher; it needs a misbehaving agent, which argues it is bounded. Recorded
   rather than re-ranked unilaterally.
10. **`RecomputeJobStatus`'s `cancelled`-blindness is proposed for the backlog, not fixed**
    (section 6). It becomes unreachable through this path once the predicates land.

## 10. Acceptance criteria

1. Route A is closed: a cancel landing in the retry TOCTOU window wins, proven by store case 2
   with its stage-1 RED output captured showing the cancelled task resurrected to `pending`.
2. Route B1 is closed: a task's own assignee cannot resurrect its own completed task, proven
   by `TestHandleTaskStatus_AssigneeCannotResurrectItsOwnCompletedTaskViaRetry` including the
   `GetEligibleTasks` assertion on the dependent, with stage-1 RED captured.
3. Route B2 is closed: a second terminal message cannot overwrite a terminal status or cascade
   `FailDependentTasks`, proven by
   `TestHandleTaskStatus_ASecondTerminalFromTheAssigneeDoesNotOverwriteOrCascade` including
   the "dependent still `pending`" assertion, with stage-1 RED captured.
4. The retry is atomic with respect to its T0 read and exactly-once per generation, proven by
   store cases 4 and 5.
5. A caller that lost its identity fails closed, proven by store case 7 and mutation-proved
   against `IS NOT DISTINCT FROM` (M4), with case 7 the *only* case that goes red.
6. Every predicate is individually load-bearing: the full 8.7 matrix runs, each mutation
   produces exactly the predicted red set, and every discriminating input is already in a
   permanent committed test with no test edit required to observe it.
7. The trailing-log flush still works:
   `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`
   passes **with no edit**, which is the evidence that a status predicate and not an epoch
   bump was the right fix.
8. `Dispatcher.failClaimedTask` still terminally fails a poison-payload task, proven by the
   two `internal/scheduler` tests passing **with no edit**.
9. `handleTaskStatus` performs no additional DB round trip; a rejected retry produces no log
   line, no job recompute, no dispatcher wake and no publish.
10. `TestIncrementTaskRetryCount_BumpsEpochAndFencesStaleRetry` passes with only the params-
    struct call-site change; every assertion in it is byte-identical. Every other existing test
    passes unchanged. Any assertion needing adjustment is reported as a finding.
11. The `Connect` test drives two terminal messages over the real message loop and the task
    ends `done` with `retry_count` 0.
12. `make test` and `make test-integration` are both green; `go vet -tags integration ./...`
    is clean.
13. The stale rationale comments at `handler.go:441-455` are rewritten, and
    `TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask`'s comment
    records that its discriminating power moved to the SQL layer.
14. CLAUDE.md's Epoch fence bullet no longer names `IncrementTaskRetryCount` as satisfying the
    invariant vacuously, and no longer cites this item as open.
15. The two stale artifacts named in section 6 carry dated Correction blocks.
16. The backlog item is closed with `/backlog close`, landing the file in
    `docs/backlog/closed/`.

## 11. Does this close the item, and what must the retry endpoint respect

**It closes the item fully.** Both routes the item documents are closed, plus the requeue
variant of route A that it does not document, plus PR #120's residual "unforgeable but not
atomic". The item's acceptance criteria are met with the one honest deviation recorded in 7.5
and 8.3: the cancel interleaving is proven at the store layer on the statement with the
handler's own T0 arguments rather than by a timing-based interleave, and the handler wiring is
proven through route B instead.

Residual, and it is not this item's: an attacker holding worker W's own token can still drive
W's own tasks through their legal state machine (`RUNNING`, then one terminal). No server-side
check on this path can do better, as #119 section 5 established.

**What `POST /v1/jobs/{id}/retry` must respect when it lands** (tracked in
`docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md`):

1. **It must not call `IncrementTaskRetryCount`.** Its precondition is the exact inverse: it
   reopens tasks that *are* terminal, and it has no worker identity to bind. Both the status
   and the worker predicate would reject every call. This is the correct outcome, not an
   obstacle - the two operations were only ever conflatable because neither had a stated
   precondition. The query comment in 4.1 says so at the statement.
2. **It needs its own statement** with an explicit allow-list - `WHERE id = $1 AND status IN
   ('failed','timed_out')` for `?task=failed`, widened for `?task=all` - that sets
   `status='pending'`, NULLs `worker_id`, clears `started_at`/`finished_at`, and **bumps
   `assignment_epoch`**, satisfying the invariant's "end the assignment" branch. That is the
   operator analogue of `RequeueTaskByID`, not of `IncrementTaskRetryCount`.
3. **It must decide what happens to `retry_count`.** An operator re-run that leaves
   `retry_count` at its exhausted value gives the task zero agent-side retries on the new
   generation. Resetting it to 0 is the likely intent and must be an explicit decision in that
   spec, not a side effect.
4. **It must decide what happens to the job's status,** because `RecomputeJobStatus` is
   `cancelled`-blind (2.3). Reopening tasks on a cancelled job will pull the job to `running`
   through exactly the mechanism this bug exploited. Reopening a `done` job also reactivates
   `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`, which that item already notes.
5. **It must not reopen a task whose dependents already ran,** or it reproduces route B by
   design rather than by accident.

## 12. Known Limitations (recorded during implementation, 2026-08-12)

1. **No test in the tree discriminates the Go identity gate in `handleTaskStatus`** (the
   identity `if` guarding the currency check; cited by symbol because line numbers rot - this
   entry originally said `handler.go:436-476` and was stale within the same PR). After this
   change, deleting that gate outright leaves every test green: a forged terminal from a
   non-assignee is rejected by `IncrementTaskRetryCount`'s or `UpdateTaskStatus`'s own
   `worker_id` predicate, and the observable state is identical.
   `TestHandleTaskStatus_ZeroValueWorkerIdCannotBurnARetryOnANeverClaimedTask` was PR #120's
   permanent guard for it and stopped discriminating it here (spec 8.5); its comment now says
   so. **Verified during implementation, not assumed:** the gate was deleted from
   `handleTaskStatus` and that test was re-run, and it PASSED.

   The gate's remaining value is **one saved database round trip per forged message** (not
   zero round trips - `GetTask` runs ahead of the gate regardless) plus the different question
   it asks. It saves **no log lines**: section 3.3 drops `pgx.ErrNoRows` silently at both
   write sites, so a forged message logs nothing either way. See the Correction on 3.2 point
   2. A test could not pin what remains anyway - a round trip is not observable state - which
   is why this is written down instead: otherwise the next reviewer deletes the gate and sees
   a green suite. The rationale comment at the gate says the same thing, at the same size.
2. **The fence binds a worker, not a connection.** Two concurrent streams registered for the
   same worker row both satisfy every predicate. Deliberate, unchanged from PR #120, and what
   keeps reconnect-within-the-grace-window working.
3. **An attacker holding worker W's own token can still drive W's own tasks through their
   legal state machine** (`RUNNING`, then one terminal). No server-side check on this path can
   do better, as the tasklog spec's section 5 established.
