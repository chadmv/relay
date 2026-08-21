# Coordinator-side stale-task watchdog

- **Date:** 2026-08-20
- **Type:** backend slice (Go + SQL + one migration)
- **Closes:** `docs/backlog/bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task.md`
- **Blocked on:** nothing. See section 3 for why the requeue-fence prerequisite does not apply.
- **Phase:** 1 (design). Phase 2 writes the plan.

This spec was produced in an unattended run, so every place the brainstorming flow would ask a
human, the call is made here with the reasoning written down. Where a fork was not resolvable by
evidence, the more conservative and more reversible arm was taken and labelled as such.

---

## 1. Verification of the backlog item's claims

The item is unusually complete, and this project has a long record of items whose diagnosis was right
and whose prescribed remedy was wrong. Every load-bearing claim was checked against the tree at HEAD
(`0fc1efc`). Results first; the two refutations change the design.

### Confirmed

| Claim | Evidence |
|---|---|
| `tasks.timeout_seconds` is enforced only on the agent | `internal/agent/runner.go`, `newRunner`: `if timeoutSec > 0 { context.WithTimeout(...) } else { context.WithCancel(parent) }`. No other deadline exists anywhere. |
| `handleTaskStatus` is the **sole** writer of `timed_out` | Every occurrence of the literal across `**/*.{go,sql}` was enumerated. Exactly one *writes* it: `internal/worker/handler.go:703`, the enum mapping `TASK_STATUS_TIMED_OUT -> "timed_out"`, flowing into `UpdateTaskStatus`. All others read or match it (`RecomputeJobStatus`'s terminal count, `RetryJobTasks` / `SelectRetryableTaskIDs` allow-lists, migration 000019's CHECK, `internal/cli/logs.go`, `internal/mcp`), or are tests and comments. Confirmed. |
| Nothing in `internal/scheduler` scans non-terminal assignments | The package is four non-test files: `dispatch.go`, `labels.go`, `notify.go`, `source_proto.go`. The only task read is `GetEligibleTasks`, whose first predicate is `t.status = 'pending'`. `CountActiveTasksByAllWorkers` does read the assigned set, but only to compute free slots; it returns counts, not ids, and no age. Confirmed. |
| `Dispatcher.failClaimedTask` covers dispatch failure only | Its two call sites are the `json.Unmarshal` failures on `commands` and `source`, both inside `sendTask`, both before the dispatch send. Confirmed. |
| `started_at` is written from the Go clock | `handleTaskStatus`: `startedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}` when `statusStr == "running"`, bound into `UpdateTaskStatus`. Confirmed, and it is the only writer that *sets* it; every other statement only nulls it. So the trailing-window fix's argument for a Go-computed cutoff transfers here intact. |
| The epoch fence makes a late agent update a no-op | `UpdateTaskStatus` carries `AND status IN ('pending','dispatched','running')`. A row this design has already stamped `timed_out` matches nothing. Confirmed - and this is *why* the fail shape needs no new machinery for that acceptance criterion. |

### Refuted or corrected

**R1. `GraceRegistry` is armed by teardown *and* by startup reconciliation.** The item says it "is armed
by connection teardown". There are two arming sites: `Handler.teardownConnection` -> `h.grace.Start`
(`internal/worker/handler.go:1042`), and `seedGraceTimersFromActiveTasks` in
`cmd/relay-server/main.go:272`, which walks `ListGraceCandidates` at boot and calls `Start`,
`StartWithDuration` or `ExpireNow` per worker. The item's *conclusion* survives - startup seeding is
still disconnect-derived (a restart means every agent is disconnected), and a reconnecting agent's
`finishRegister` calls `grace.Cancel` before reconcile - so a connected, hung agent is still
invisible to the grace path. But the design must account for the boot window, when grace timers and
the watchdog can both be live over the same rows. Section 6 does.

**R2. There is no timestamp that bounds a `dispatched` row.** This is the finding that changes the
design, and the item's amendment predicted it might ("the row may never have gone `running` at all").
`tasks` (migration 000001, plus 000004/000007/000008) has exactly three timestamps: `started_at`
(set only on the `running` transition), `finished_at` (set only on a terminal transition) and
`created_at` (row insert, i.e. **job submission**). There is no `updated_at` and no `dispatched_at`.
`ClaimTaskForWorker` writes `status`, `worker_id` and `assignment_epoch` and no timestamp at all.

So for a row left `dispatched`:

- `started_at` is NULL,
- `finished_at` is NULL,
- `created_at` says when the *job* was submitted, which is unrelated to when the assignment began. A
  task that queued for six hours behind a busy fleet and was dispatched one minute ago is six hours
  old by `created_at`. Keying the absolute bound on `created_at` would kill healthy, just-dispatched
  work.

**A new column is therefore required**, not a nicety. Section 5.2.

**R3. The `dispatched` state legitimately spans an unbounded workspace sync, so there is no shorter
honest bound for it.** `Runner.Run` sends `TASK_STATUS_PREPARING` and then calls
`provider.Prepare(...)` - the P4 sync - *before* anything reports `RUNNING`. `handleTaskStatus`'s
enum switch has **no case for `TASK_STATUS_PREPARING`**, so it falls to `default: return` and the row
stays `dispatched` for the entire sync. On a 1 TB+ workspace that is hours. This kills the tempting
idea of a short "dispatched but never acked" timeout: any such bound would have to exceed the longest
legitimate sync, which is the absolute cap. One bound, not two.

**R4. A coordinator-side terminal writer over a live assignment is not new; `handleCancelJob` already
is one.** `RetryJobTasks`'s doc comment (see section 3.2) warns that a server-side watchdog would make
that statement a duplicate-execution primitive. That warning is correct in substance but wrong to
imply novelty: `CancelJobTasks` already stamps `failed` on `dispatched`/`running` rows whose agents
may still be executing, and `handleCancelJob` then fires `sendCancelSignals` as a **best-effort**
mitigation. An operator retry of a cancelled job reopens exactly those rows. The hazard, and its
mitigation, are both already in the tree. That gives this slice a precedent to copy rather than a new
risk class to invent - and it is the single most decisive piece of evidence for the fail shape.

**R5 (adjacent, in-scope prose defect).** `TestTasksStatusVocabularyIsExactly`'s comment and failure
message both say "six statements in this repo hard-code a slice of that vocabulary". Counting
`internal/store/query/tasks.sql` alone, at least `GetEligibleTasks`, `ClaimTaskForWorker`,
`RequeueTask`, `RequeueTaskByID`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`,
`GetActiveTasksForWorker`, `ListGraceCandidates`, `CountActiveTasksByAllWorkers`, `CancelJobTasks`
and `FailDependentTasks` also hard-code slices. The six named are the six whose partition choice is
*decision-relevant*, which is a narrower and better claim than the one the comment makes. This slice
edits that comment (section 5.6), so correcting it is in scope. Per the 2026-08-20 retro's own
lesson, prefer **deleting the count** over incrementing it to seven.

---

## 2. What is actually being fixed

A task assigned to a **connected** agent has no coordinator-side bound on its duration. Three inputs
produce it and they are indistinguishable from the coordinator: a wedged agent process, a task
submitted with `timeout_sec = 0` (documented as "no deadline"), and an agent that simply declines to
report terminal. A fourth was added by the 2026-08-20 amendment: a stale-epoch reconcile report puts
a task into `cancelIDs` **and** marks it reported, so it is neither cancelled server-side nor
requeued and sits `dispatched` with a `worker_id` pointing at a worker that has been told to abandon
it.

In all four the row holds its `worker_id` and `assignment_epoch` forever, the job never reaches a
terminal status, the worker slot is never released (`CountActiveTasksByAllWorkers` counts
`status IN ('dispatched','running')`), and the assignment keeps passing `AppendTaskLog`'s fence.

**Non-goals.** This slice does not bound log *volume* (that is
`bug-2026-08-14-task-logs-have-no-per-task-volume-cap`), does not make cancellation reliable against
a wedged agent, does not introduce a task-level `cancelled` status, and does not add automatic
retries for swept tasks (section 11).

---

## 3. Decision 1 - fail (`timed_out`), not requeue

**Chosen: fail.** The sweeper writes `timed_out` through the existing `UpdateTaskStatus` statement.

The alternatives were weighed as three approaches:

**A. Requeue (`RequeueTaskByID`-shaped).** Rejected, on two grounds that are independent of the
missing fence:

1. *It does not terminate.* A requeue returns the task to `pending`; the dispatcher immediately
   re-dispatches it; if the cause is an agent that hangs on this workload, it hangs again and the
   watchdog requeues again. Nothing burns a retry (the sweeper cannot call
   `IncrementTaskRetryCount` - `TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath` is a
   module-wide structural guard that fails if the identifier appears in any non-test Go file outside
   `internal/worker/handler.go`), so the loop is unbounded. The item's headline symptom - "the job
   never completes" - is *not fixed* by requeue in the pathological case; it is converted from one
   stuck task into a permanently churning one.
2. *It makes duplicate execution automatic rather than operator-gated.* Requeue hands the task to a
   second worker with no human in the loop, every time, while the first agent may still be running
   it.

Only after those does the fence question arrive, and it is decisive on its own:
`bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence` would become a hard prerequisite,
because this shape adds a second, periodic, non-agent-driven caller to an unfenced write.

**B. Fail with an epoch bump (a new statement in the `CancelJobTasks` mould).** Rejected as the more
expensive and less reversible arm of a narrow trade. It would buy exactly one thing over C: it closes
the swept task's log write channel *immediately* instead of at
`finished_at + RELAY_TASKLOG_TRAILING_WINDOW` (15m by default). It costs: a new write statement, a new
hard-coded status partition on a write path, the loss of trailing output for an agent that was merely
slow rather than hung, and - the deciding cost - it would make CLAUDE.md's epoch-fence bullet
conditional. That bullet currently reads "the fix for that is a status predicate, **never an epoch
bump on terminal transitions**, which would break the trailing-log flush". Shipping a terminal writer
that bumps requires amending an invariant to buy fifteen minutes. **This is the
conservative-and-reversible call:** adding a bump later is a strictly later decision, whereas shipping
one and discovering it truncates legitimate output means undoing a write path.

**C. Fail through the existing `UpdateTaskStatus` (chosen).** Its fence is already exactly right:
`assignment_epoch = $` (currency), `worker_id = $` (identity, plain `=`, NULL-rejecting), and
`status IN ('pending','dispatched','running')` (terminality). The sweeper reads the row, then binds
the row's own epoch and worker id. That makes the worker predicate a self-comparison against a value
read moments earlier - which is precisely `Dispatcher.failClaimedTask`'s situation, and the
statement's own comment blesses it: *"Dispatcher.failClaimedTask passes claimed.WorkerID from
ClaimTaskForWorker, where the predicate is tautological by design ... one statement with no
exceptions to remember."* The epoch predicate is the real TOCTOU guard and it is not tautological.

**Net: this slice adds zero new statements that write `tasks.status`, and zero new status partitions
on any write path.** Against a file whose invariants are this densely documented, that is the whole
argument.

### 3.1 What failing actually triggers, concretely

Traced end to end, because the item asks and because it is where a wrong answer would flip the
decision.

1. `UpdateTaskStatus(status='timed_out', started_at=<unchanged>, finished_at=now)` matches one row.
   `worker_id` and `assignment_epoch` are **unchanged** - the statement no longer writes `worker_id`
   at all, and terminal transitions deliberately do not bump the epoch.
2. `FailDependentTasks(taskID)` cascades `status='failed', finished_at=NOW()` to every transitively
   dependent task that is still `pending`. Identical to what an agent-reported failure does. The
   dependents become `failed`, not `timed_out` - correct, and matching the existing path exactly.
3. `RecomputeJobStatus(jobID)`: with the swept task and its downstream now terminal, the
   `COUNT(*) FILTER (WHERE status NOT IN ('done','failed','timed_out')) > 0` arm goes false, and the
   job settles on `done` only if every task is `done`, otherwise `failed`. So the job reaches a
   terminal status. **This is the headline symptom fixed.**
4. The worker slot is released: `CountActiveTasksByAllWorkers` counts only
   `status IN ('dispatched','running')`.
5. `NotifyTaskCompleted` wakes dispatchers on every replica.
6. The agent's own late terminal update - if it ever arrives - hits `UpdateTaskStatus`'s status
   allow-list, matches zero rows, returns `pgx.ErrNoRows`, and `handleTaskStatus` drops it silently.
   No resurrection, no second cascade, no double SSE frame, no retry burned. The acceptance criterion
   is satisfied by machinery that already exists.
7. The agent's trailing log chunks keep passing `AppendTaskLog` (epoch and worker id are unchanged)
   until `finished_at + RELAY_TASKLOG_TRAILING_WINDOW`, then stop. That is the policy the 2026-08-14
   slice deliberately chose; the watchdog inherits it rather than reopening it.
8. **The retry budget does not apply.** `task.retries` is consumed only by `handleTaskStatus`'s
   agent-driven branch. A swept task with `retries: 3` gets zero automatic retries. This is a
   deliberate, documented limitation (section 11), not an oversight.
9. `POST /v1/jobs/{id}/retry` will reopen the swept task in either mode (`timed_out` is in
   `RetryJobTasks`'s allow-list). If the original agent is still executing, that is duplicate
   execution - see 3.2.

Nothing in that trace is worse than requeue. Steps 3 and 6 are strictly better.

### 3.2 The `RetryJobTasks` interaction, which must not be skipped

`RetryJobTasks`'s doc comment contains a direct instruction to this slice:

> What would break this is a SERVER-SIDE watchdog: something that stamps `timed_out` on a task the
> agent is still happily running ... Whoever adds a coordinator-side terminal writer must revisit
> this clause, not just the status vocabulary test.

The comment's factual premise - *"`timed_out` has exactly one writer, and it is the assignee
itself"* - **becomes false with this slice.** Amending it is a hard acceptance criterion, not
cleanup. The amendment should say:

- `timed_out` now has two writers: the assignee (via `handleTaskStatus`) and the coordinator watchdog
  (`internal/scheduler/watchdog.go`).
- The watchdog's row *may* be terminal while a subprocess still runs, so this statement can reopen
  such a row and the dispatcher can hand it to a second worker.
- That hazard is **not new with the watchdog** (R4): `CancelJobTasks` already stamps `failed` on live
  assignments and `handleCancelJob` mitigates with a best-effort `sendCancelSignals`. The watchdog
  adopts the identical mitigation (section 7).
- The residual is bounded by: the sweep only fires long past the deadline plus a generous margin, and
  the original agent's own completion is fenced out silently. Eliminating it entirely needs a
  per-assignment fencing token at the agent, which is out of scope and should stay out of this
  comment as anything other than a named non-goal.

---

## 4. Decision 2 - what "too long" means

Two independent bounds. A row is overdue if **either** fires.

### 4.1 The execution bound

`now - started_at > timeout_seconds + RELAY_TASK_WATCHDOG_MARGIN`, evaluated only for rows with
`started_at IS NOT NULL` and `timeout_seconds > 0`.

It is measured from `started_at` because that is when the agent's own `context.WithTimeout` clock
starts (approximately - the agent starts its deadline at `newRunner`, the row records when the server
processed the `RUNNING` message, and the gap is one message hop). It is emphatically **not** measured
from the start of the assignment: prepare can legitimately take hours (R3), so an assignment-relative
execution bound would kill a task that has only just begun running.

The margin absorbs the whole gap between the agent's deadline firing and the coordinator seeing the
terminal update: subprocess kill, proctree cleanup, final log flush, and a gRPC reconnect if the
stream dropped. The README's own analysis of a single agent reconnect puts that at roughly 105s.

**Default `30m`** - about seventeen times the worst case that has been measured, chosen generously
because the failure direction of "too small" is killing healthy work.

### 4.2 The absolute bound

`now - assigned_at > RELAY_TASK_MAX_ASSIGNMENT`, evaluated for every assigned row.

This is the arm that covers `timeout_seconds = 0` (and NULL), covers a row that never reached
`running` at all, and is therefore the arm that recovers the amendment's orphaned-`dispatched` case.

**Default `24h`.** It has to exceed the longest legitimate assignment, which is dominated by a P4 sync
on a 1 TB+ workspace plus the task's own run.

### 4.3 The clock

Both cutoffs are computed in Go and bound as parameters. No `NOW() - interval`. The argument is
`AppendTaskLog`'s, and it transfers with one addition:

- `started_at` is written by the relay-server Go clock (confirmed above) and by nothing else.
- `assigned_at` will be written by the relay-server Go clock too - see 5.2 - specifically so this
  argument does not acquire an exception.
- The one DB-clock write of `assigned_at` is the migration's backfill, which happens once, before any
  sweeper exists.

Cross-replica this is app-vs-app NTP skew (milliseconds) against bounds measured in hours.

### 4.4 Configuration

| Variable | Default | Meaning |
|---|---|---|
| `RELAY_TASK_WATCHDOG_MARGIN` | `30m` | Added to a task's own `timeout_sec` before the coordinator declares it timed out. `0` disables the execution arm entirely. |
| `RELAY_TASK_MAX_ASSIGNMENT` | `24h` | Absolute cap on how long a task may stay assigned, measured from dispatch. Bounds `timeout_sec = 0` tasks and tasks that never report `running`. `0` disables the absolute arm entirely. |

**`0` means "this arm is off" in both variables. One rule, no exceptions.** The ambiguity is real and
was resolved deliberately: a margin of exactly zero is a legitimate aggressive setting, but giving
the same literal two meanings across two adjacent knobs is a footgun, so an operator who genuinely
wants no margin writes `1s`. Setting both to `0` disables the watchdog, which is the documented
escape hatch and must be stated in the README row.

Parsing follows `parseTrailingLogWindow`'s existing shape: unparseable or negative keeps the default
and returns one warning string that `main` logs once at startup. `0` is *accepted*, not rejected, and
logs one informational line naming what is now unbounded.

The sweep **interval** is not configurable - it is not an operational timeout, it is an implementation
cadence. A package constant `SweepInterval = 60 * time.Second`, mirroring `metrics.SweepInterval`,
with `SweepOnce(ctx)` exported for tests. Against hour-scale bounds a 60s tick contributes nothing.

---

## 5. Decision 3 - the query, the column, and the status partition

### 5.1 Key on non-terminal duration, never on `status = 'running'`

The scan predicate is `status IN ('dispatched','running') AND worker_id IS NOT NULL`. This is the
"assigned" partition, and it is already spelled exactly that way in five existing places -
`GetActiveTasksForWorker`, `CountActiveTasksByAllWorkers`, `ListGraceCandidates`,
`RequeueWorkerTasks(IfEpoch)`, and the partial index below - so the watchdog joins an existing family
rather than inventing a partition.

`worker_id IS NOT NULL` is not decoration. `UpdateTaskStatus`'s worker predicate is a plain `=`, so a
row with a NULL `worker_id` can never be written by it; selecting such a row would cost a guaranteed
zero-row round trip every sweep. It also documents the one state this watchdog cannot recover: a
`dispatched` row whose `worker_id` was nulled by `workers`' `ON DELETE SET NULL`. That state is
**unreachable today** - there is no `DELETE FROM workers` statement anywhere in the tree; workers are
revoked, not deleted - and no epoch-fenced writer could touch such a row anyway. Named here as a
non-goal so the next reader does not have to re-derive it; not filed.

**The bound is on assignment age, never on last activity.** A `MAX(task_logs.created_at)` liveness
signal is tempting and wrong: it is agent-controlled, so a hung-but-chatty agent would look healthy
forever, and the volume needed to do that is itself unbounded today.

### 5.2 Migration 000021 - `tasks.assigned_at`

```sql
ALTER TABLE tasks ADD COLUMN assigned_at TIMESTAMPTZ;
UPDATE tasks SET assigned_at = NOW() WHERE status IN ('dispatched', 'running');
```

- Nullable, no default. NULL means "not currently assigned".
- Written by `ClaimTaskForWorker` from a **Go-supplied** parameter (the dispatcher already has
  `time.Now()` in hand at `sendTask`), so section 4.3's clock argument stays exception-free.
- Nulled alongside `started_at` by every statement that returns a task to `pending`: `RequeueTask`,
  `RequeueTaskByID`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`, `IncrementTaskRetryCount`,
  `RetryJobTasks`. Each of those already has a `started_at = NULL` line; this is one line beside it.
  **Correctness does not depend on the nulling** - the scan's status allow-list only admits rows a
  claim produced, and a claim always overwrites `assigned_at` - but doing it makes
  `assigned_at IS NOT NULL` mean exactly "currently assigned", which is a property the next reader
  can rely on. If any one of those edits turns out to cost more than a line, drop that one and say so;
  do not drop the claim-time write, which is the load-bearing half.
- The backfill uses `NOW()` deliberately: it gives every in-flight assignment a **fresh** clock at
  migration time, so a deploy cannot sweep a fleet's worth of healthy long-running work in its first
  minute. This is the only DB-clock write of the column and it happens once, before any watchdog
  exists.
- The down migration drops the column.

**No new index.** Migration 000018 already created
`idx_tasks_worker_active ON tasks(worker_id) WHERE status IN ('dispatched','running')` - a partial
index whose predicate is exactly the scan's status predicate, containing exactly the currently
assigned rows (bounded by the fleet's total slots, i.e. hundreds). The engineer should confirm the
plan uses it and only then consider more; adding an index speculatively is out of scope.

### 5.3 The statement

New, read-only: `ListOverdueAssignedTasks`. Returning `tasks.*` keeps it usable with `store.Task` and
with `UpdateTaskStatus`'s parameter struct.

Semantics (one acceptable spelling; the engineer verifies what sqlc emits):

```sql
SELECT * FROM tasks
WHERE status IN ('dispatched', 'running')
  AND worker_id IS NOT NULL
  AND (
        ( sqlc.arg(absolute_enabled)::bool
          AND assigned_at IS NOT NULL
          AND assigned_at < sqlc.arg(absolute_cutoff)::timestamptz )
     OR ( sqlc.arg(exec_enabled)::bool
          AND started_at IS NOT NULL
          AND timeout_seconds IS NOT NULL AND timeout_seconds > 0
          AND EXTRACT(EPOCH FROM (sqlc.arg(now)::timestamptz - started_at))
              > timeout_seconds + sqlc.arg(margin_seconds)::bigint )
      )
ORDER BY assigned_at NULLS LAST, id;
```

Notes the plan should carry forward:

- The `EXTRACT(EPOCH FROM ...)` form is preferred over `started_at + make_interval(...)` because it
  binds only a `timestamptz` and a `bigint`; sqlc's handling of `interval` parameters is the kind of
  thing that produces a surprising generated type.
- Each arm carries an explicit `..._enabled` bool rather than a sentinel cutoff. Explicit beats
  encoding "disabled" as `-infinity`.
- Every arm fails **closed** on a missing value: a NULL `assigned_at`, a NULL `started_at`, a NULL or
  zero `timeout_seconds` each make their arm false rather than true. The row is left alone. Do not
  "fix" any of these into `IS NULL OR ...`; that is the fail-open direction, exactly as
  `AppendTaskLog`'s comment says about its own second arm.
- `ORDER BY` is for determinism in tests and for oldest-first operator log output; nothing depends on
  it.

### 5.4 The write

For each returned row, in Go:

```
UpdateTaskStatus(
    ID:              t.ID,
    Status:          "timed_out",
    WorkerID:        t.WorkerID,          // fence, not a value
    StartedAt:       t.StartedAt,         // preserved unchanged
    FinishedAt:      now,
    AssignmentEpoch: t.AssignmentEpoch,   // fence: real, non-zero, from the row
)
```

`pgx.ErrNoRows` means some other writer got there first - the agent finished, a cancel landed, a
grace expiry requeued, or a sibling replica swept it. **Drop it silently and continue to the next
row.** Do not log it: it is the correct outcome, not a failure. Any other error logs once and the loop
continues.

The epoch bound is never zero: a row in `('dispatched','running')` has been through
`ClaimTaskForWorker`, which bumps. `UpdateTaskStatus`'s allow-list includes `'pending'`, which cannot
appear between the scan and the write at the same epoch, because every path to `pending` bumps the
epoch.

### 5.5 Invariant compliance, stated explicitly

- *Epoch fence.* The write fences on `assignment_epoch` (branch one). It does not bump, and does not
  need to: it is a terminal transition, and the assignment surviving completion is load-bearing for
  the trailing-log flush.
- *Epoch establishes currency, not identity.* The write also carries `worker_id = $`, plain `=`,
  NULL-rejecting. The scan's `worker_id IS NOT NULL` guarantees the bound value is real.
- *Status predicates are allow-lists.* Both the new scan and the reused write are allow-lists. See
  5.6 for the direction each fails in.
- *One bounded sender per gRPC stream.* The cancel goes through `worker.Registry` -> `workerSender`,
  which is bounded by the 5s `sendTimeout`. No new send path. Section 7.
- *No interior pointers across locks.* The watchdog holds no shared mutable state; `Registry.Send`
  copies the sender out under its own `RLock`.
- *Single JSON entry point / single job-spec pipeline.* Untouched.

### 5.6 `TestTasksStatusVocabularyIsExactly` gains a site, and it is the **second** inverted one

The scan introduces a new hard-coded partition, so the guard test's comment and failure message must
name `ListOverdueAssignedTasks`. The direction matters and must be spelled out there:

> A new **non-terminal** status omitted from this scan is never swept, which silently reopens the
> exact unbounded-assignment hole this statement exists to close, for that status. `preparing` is the
> live candidate. A new **terminal** status must stay OUT.

That is the same inversion `AppendTaskLog` carries. **Consequence for CLAUDE.md:** the Epoch fence
bullet currently calls `AppendTaskLog` "the third status-predicate site and the one carve-out to that
guidance". With this slice there are two carve-outs. The bullet needs a targeted amendment - one
clause, naming `ListOverdueAssignedTasks` as the second site where omitting a new non-terminal status
fails open - and nothing else in the bullet moves. Do not restructure it.

While editing that comment, apply R5: prefer deleting the "six statements" count over incrementing it.

---

## 6. Decision 4 - interaction with `GraceRegistry`

Two timers can fire on one row. The ordering argument, to be written into the watchdog's own doc
comment rather than left to be re-derived:

- **Watchdog first, grace second.** The row is `timed_out`. Grace's `RequeueWorkerTasksIfEpoch`
  matches `status IN ('dispatched','running')` and therefore matches zero rows. The task stays
  terminal, which is correct: it was overdue regardless of whether its worker later dropped.
- **Grace first, watchdog second.** The requeue set `pending`, `worker_id = NULL`, epoch N+1. The
  watchdog's already-issued `UpdateTaskStatus` binds epoch N and the old worker id, so it matches zero
  rows - **on the epoch, first and independently of the others**. `pgx.ErrNoRows`, dropped. The
  requeue wins, which is correct: an assignment that ended is not the watchdog's to finish. The only
  window in which this ordering is even reachable is between the watchdog's scan and its write; the
  scan itself would no longer return the row.
- **Reconnect-and-reconcile.** `finishRegister` calls `grace.Cancel` and then `reconcileRunningTasks`,
  which may `RequeueTaskByID` the row (epoch bump). Identical to the previous case from the
  watchdog's side.
- **Two replicas sweeping at once.** First write wins; the second matches zero rows on the status
  allow-list. No coordination, no advisory lock, no leader election needed.
- **Boot.** `seedGraceTimersFromActiveTasks` (R1) arms grace timers for every worker with active tasks
  at startup, and the watchdog's first tick is 60s later. Both are safe by the two cases above. In
  practice the watchdog will almost never fire on a *disconnected* worker, because grace's default
  window is 2m against the watchdog's hours - which is the correct division of labour and is worth
  saying in the comment: **grace owns disconnect, the watchdog owns duration.**

**The watchdog is deliberately registry-blind when deciding to write.** It does not check whether the
worker is connected to *this* process. Two reasons: under multi-replica operation the agent may be
connected to a different replica, so a local registry miss proves nothing; and the
orphaned-`dispatched` case (amendment case 1) is precisely a row whose agent has been told to abandon
it. The registry is consulted **only** to decide whether a cancel can be delivered, which is why that
send is best-effort by construction.

---

## 7. Decision 5 - the agent is told, in this slice

**Chosen: include the cancel. Do not split.**

The item guesses that telling the agent "touches `Connect`'s send path, so it may be a second slice".
That is refuted: `internal/scheduler/dispatch.go` already calls `d.registry.Send(...)` on the dispatch
path, `internal/api` already has two callers that cancel tasks on connected agents (`handleCancelJob`
and `handleDisableWorker`), and every one of them goes through `worker.Registry.Send` ->
`workerSender.Send`, bounded by the 5s `sendTimeout` package var. There is no new send machinery to
build.

The argument for including it is that excluding it makes the slice net-negative in one dimension:

- Without the cancel, the fix is bookkeeping. The subprocess keeps running against a P4 workspace, the
  worker slot is released to the dispatcher while the worker is in fact still busy (so the fleet
  over-subscribes exactly the machine that is already in trouble), and the slice *creates* the
  `RetryJobTasks` duplicate-execution exposure without adding the mitigation the tree's own precedent
  (`handleCancelJob`) already pairs with it.
- With the cancel, this slice is the same shape as the shipped cancel path, which is a design the
  project has already reviewed and accepted.

Shape:

- New method on `*worker.Registry`, beside the existing `SendEvictCommand` which it mirrors exactly:

  ```go
  func (r *Registry) SendCancel(workerID, taskID string, force bool) error
  ```

- The watchdog fans the sends out concurrently and waits, exactly like `api.sendCancelSignals`, so N
  overdue tasks on one wedged worker cost ~one send timeout rather than N of them. That property has
  a test in `internal/api/cancel_signals_test.go`; the watchdog needs its own equivalent.
- **`force = false`.** This is the conservative arm and it is a genuine trade. `force = true` skips
  workspace finalize and bypasses pipe drain; skipping finalize risks leaving a P4 workspace in a
  state that poisons warm-workspace scoring for every later task on that machine. `force = false`
  still closes `cancelledCh`, which is the escape that frees a log write parked on a full `sendCh` -
  so the main wedge-escape property is retained. It also matches the closest existing analogue:
  `handleDisableWorker`, which is the other place the coordinator unilaterally takes tasks away from a
  still-connected agent, and it uses `force: false`. Escalating to `force` later is a one-line,
  well-understood change; shipping it and discovering it corrupts warm workspaces is not.
- Ordering: **write first, then send.** The DB write is the thing that must be durable; the send is
  advisory. Sending first would mean a failed write leaves an agent told to stop a task the
  coordinator still considers live.
- The send only happens for rows the write actually matched. A row the fence rejected belongs to
  somebody else now, and cancelling it would be exactly the "tear a live assignment off a worker"
  failure this project keeps closing.

**Nothing is split out, so there is no second backlog item to recommend from this decision.**

**One optional reuse edit, explicitly marked droppable:** `api.sendCancelSignals`'s goroutine body
can call `r.SendCancel(...)` instead of constructing the `CoordinatorMessage` inline, leaving one
construction site for `CancelTask` in the tree. It is a three-line pure extraction with no behaviour
change. If it costs anything at all - a test that reaches into the message construction, an import
cycle, a reviewer question - drop it and note it. It is not part of the fix.

---

## 8. Decision 6 - where the sweeper lives

**Chosen: `internal/scheduler/watchdog.go`, its own goroutine, its own ticker.**

| Candidate | Verdict |
|---|---|
| `internal/scheduler` | **Chosen.** It already imports `store`, `events`, `worker` and `api`, so there is no new edge and no cycle risk. It already owns the coordinator's view of task assignment (claim, dispatch, requeue-on-send-failure, `failClaimedTask`). Critically, it already contains the exact terminal-finalization tail this needs - cascade, recompute, publish - so the watchdog reuses code instead of copying it. |
| `internal/schedrunner` | Rejected. It is the cron engine for `scheduled_jobs`; a task-duration watchdog is not a cron trigger. Its documented constraint (must not import `internal/api`) is a symptom of how carefully its dependency surface is kept small, and this would need `events` and `worker` added to it. Co-locating unrelated periodic work in "the package that has a ticker" is the wrong reason to choose a package. |
| `internal/metrics` | Rejected. Its `Sweeper` has the right *shape* and the wrong *job*, and says so in its own doc comment: "It never requeues tasks - a stale worker is still connected; disconnect-driven requeue stays with worker.GraceRegistry." Its subject is worker liveness. |
| `internal/worker` | Rejected, though it is the closest runner-up: it owns `GraceRegistry` and the other `timed_out` writer. But it is the gRPC handler package, and the watchdog is not connection-scoped - making it registry-blind (section 6) is easier to hold onto in a package that has no connection in scope. |

Structure, following `metrics.Sweeper` as the in-tree precedent for exactly this pattern:

- `type Watchdog struct` with a narrow store interface (`ListOverdueAssignedTasks`,
  `UpdateTaskStatus`, `FailDependentTasks`, `RecomputeJobStatus`, `NotifyTaskCompleted`), a narrow
  canceller interface (`SendCancel`), the broker, the two durations, and an injectable
  `now func() time.Time` defaulting to `time.Now`. `*store.Queries` and `*worker.Registry` satisfy the
  interfaces; tests supply fakes. This is what makes the whole thing unit-testable without Docker.
- `Run(ctx)` - ticker loop, returns on `ctx.Done()`. `SweepOnce(ctx) error` - one pass, exported for
  tests.
- Wired in `cmd/relay-server/main.go` next to `go metrics.NewSweeper(...).Run(ctx)`, after the
  dispatcher starts. Both env vars parsed there, warnings logged once.
- **Extract the shared tail.** `Dispatcher.failClaimedTask`'s post-write half (FailDependentTasks ->
  RecomputeJobStatus -> publish task event -> publish job event when terminal) becomes a
  package-level `finalizeTerminalTask(ctx, q, broker, task, status)` taking the narrow interface, so
  the published status string is a parameter (`"failed"` for the dispatcher, `"timed_out"` for the
  watchdog). `failClaimedTask` keeps its own `UpdateTaskStatus` call and its own logging.
  `NotifyTaskCompleted` stays **outside** the helper - the watchdog calls it, `failClaimedTask`
  continues not to - so the extraction is behaviour-preserving for the existing caller.
- **Gate that extraction on byte-identical existing tests.** Every file in
  `internal/scheduler/*_test.go` must be unchanged by the refactor. A test needing adjustment *is* the
  finding. And per the cursor-pager lesson, the zero-diff gate is necessary but not sufficient: mutate
  the helper's `RecomputeJobStatus` call away and confirm an existing dispatcher test reddens, or the
  gate is decorative.

### 8.1 Logging

One `log.Printf` per **swept** task, unbudgeted, and this is safe: the count per sweep is bounded by
the fleet's assigned-task count, each task can be swept at most once (it is terminal afterwards), and
nothing in the line is caller-supplied. Ids are rendered through `uuidStr`, matching every other
id-bearing log site.

The line must carry what an operator needs to act: task id, job id, worker id, **which arm fired**,
the measured age, and the configured bound. A watchdog that kills someone's work without saying why
it decided to is worse than no watchdog.

Fence rejections log nothing (section 5.4).

---

## 9. Testing

**RED-first is not achievable for the headline behaviour** and pretending otherwise would violate
this project's own "a test seam must not destroy the RED" rule. The capability does not exist at
HEAD, so the honest RED is "the test is written first and fails". What *can* be made genuinely
discriminating is the fence and boundary behaviour, and that is done with mutations that must leave a
test behind.

**Store integration (`internal/store`)** - `ListOverdueAssignedTasks`:

1. `running`, past `timeout + margin` -> returned.
2. `running`, inside `timeout + margin` -> not returned, **including one that is still appending
   logs** (the item's second acceptance criterion; proves the bound is on assignment age, not
   activity).
3. `timeout_seconds = 0` and `timeout_seconds IS NULL`, past the absolute cap -> returned.
4. `dispatched`, `started_at IS NULL`, past the absolute cap -> returned. **This is the amendment's
   case 1**, and the test must seed that exact state: `worker_id` set, epoch non-zero, never went
   `running`.
5. `dispatched`, inside the absolute cap -> not returned.
6. **`created_at` 30 days ago, `assigned_at` one minute ago -> not returned.** This single row is what
   pins the whole reason the migration exists (R2); without it the column is untestable and a future
   editor "simplifying" to `created_at` breaks nothing visible.
7. Terminal rows (`done`, `failed`, `timed_out`) -> never returned, regardless of age.
8. `worker_id IS NULL` -> never returned.
9. Each arm disabled -> that arm returns nothing while the other still fires.

Plus: `ClaimTaskForWorker` sets `assigned_at`; each edited requeue statement nulls it.

**Watchdog unit tests (`internal/scheduler`, no Docker)** with fakes and a frozen clock:

10. One sweep writes `timed_out` with the row's own epoch and worker id, preserves `started_at`, sets
    `finished_at`.
11. A store returning `pgx.ErrNoRows` from `UpdateTaskStatus` produces no cascade, no recompute, no
    publish, no notify, and **no cancel send**.
12. The cancel is sent, once, with `force=false`, to the row's worker, and **only after** the write.
13. Fan-out is concurrent: N wedged senders complete in ~one send timeout, not N. Model it on
    `internal/api/cancel_signals_test.go`.
14. Both arms disabled -> nothing is written. Pin whether the scan still runs.

**Worker integration (`internal/worker`)** - the item's headline acceptance criterion, which must use
a **connected** worker or it is vacuous:

15. Seed a task with a backdated `started_at`/`assigned_at` on a live `Connect` stream, run
    `SweepOnce`, assert the row is `timed_out`, the dependents cascaded, and the job is terminal.
16. Then have the agent send its own terminal `TaskStatusUpdate` at the same epoch and assert: zero
    rows changed, no second cascade, no double SSE frame, no retry burned.

**Mutation battery (each must redden a permanent test, and each must leave its discriminating input
behind):**

- `assigned_at` -> `created_at` in the absolute arm. Killed by test 6.
- Bind `AssignmentEpoch: 0` in the `UpdateTaskStatus` call. Must redden.
- Bind `WorkerID: pgtype.UUID{}` (zero value) in the same call. Must redden - this is the
  NULL-rejecting `=` property.
- Drop `worker_id IS NOT NULL` from the scan. Killed by test 8.
- In the per-row loop, `continue` -> `break` on the error path. **Per the 2026-08-20 retro, the
  poisoned row must be FIRST**, with at least two good rows behind it, or this mutation is
  structurally undetectable.
- Send the cancel before the write. Killed by test 12.

---

## 10. Documentation, and the prose that must move

Prose has been the dominant defect class here for nine consecutive iterations, and three of the
artifacts below already contain claims this slice falsifies. They are acceptance criteria, not
cleanup.

1. **`RetryJobTasks`'s doc comment** - its "`timed_out` has exactly one writer" paragraph is false
   after this slice. Amend per section 3.2.
2. **`UpdateTaskStatus`'s doc comment** - "Both callers are fenced by the same statement deliberately"
   becomes three callers. Name the watchdog and state that its worker predicate is tautological for
   the same reason `failClaimedTask`'s is.
3. **`TestTasksStatusVocabularyIsExactly`** - add `ListOverdueAssignedTasks` with its inversion
   direction (5.6); apply R5 to the count.
4. **CLAUDE.md's Epoch fence bullet** - one clause, because `AppendTaskLog` is no longer the only
   carve-out (5.6). Nothing else in the bullet moves.
5. **README** - two rows in the `relay-server` configuration table, after `RELAY_WORKER_GRACE_WINDOW`.
   Each must state the failure direction of a bad value, following the `RELAY_TASKLOG_TRAILING_WINDOW`
   row's example: too small kills healthy work with no way for the agent to object, and `0` disables
   the arm. Also state that a swept task is **not** automatically retried.
6. **The watchdog's own doc comment** - the grace ordering argument (section 6) and the
   registry-blindness rule, stated once, where the next reader will look.

**Both (1) and (2) are comments on sqlc queries, so they exist twice.** After `make generate`, read
back the emitted `internal/store/tasks.sql.go` and confirm the amended doc comments actually landed
there as well as in `query/tasks.sql`. The CRLF revert dance has silently discarded a regeneration in
this repo before, leaving a generated file whose doc comment contradicted its own source.

---

## 11. Known limitations, stated so nobody has to rediscover them

- **The retry budget does not apply to a swept task.** `retries: 3` buys zero automatic attempts if
  the task hangs. Recovery is `POST /v1/jobs/{id}/retry`. This was chosen over a
  requeue-and-burn-a-retry hybrid because an automatic retry of a task that may still be executing is
  duplicate execution on a schedule, and because the hybrid needs a new fenced statement the
  structural guard on `IncrementTaskRetryCount` deliberately prevents reusing. If operators ask for
  it, it is a later slice with a clear shape.
- **A swept task that an operator retries can duplicate-execute** if the cancel did not take. Bounded,
  operator-gated, mitigated by the cancel, and already true of the cancel-then-retry path today (R4).
  Eliminating it needs a per-assignment fencing token at the agent. Out of scope, named as a non-goal.
- **The zombie keeps its log write channel for `RELAY_TASKLOG_TRAILING_WINDOW` after the sweep**, and
  its volume within that window is unbounded until
  `bug-2026-08-14-task-logs-have-no-per-task-volume-cap` is closed.
- **The freed slot is optimistic.** The coordinator releases the worker's slot while the subprocess
  may still be running, so a machine with a wedged task can be handed more work. This is inherent to
  declaring a task over from the coordinator's side; the cancel is the only mitigation.
- **A `dispatched`/`running` row with a NULL `worker_id` is not recoverable** by this or any other
  fenced writer. Unreachable today (5.1).
- **`main()` wiring is untested**, as with `TrailingLogWindow`: nothing constructs `main`. Its only
  protection is sitting adjacent to its siblings.

---

## 12. Backlog recommendations

**None.** Nothing was split out of this slice, and every adjacent defect found while verifying
(R1, R2, R3, R5, the `RetryJobTasks` premise) is either in-scope for this slice or a documentation
correction this slice must make. The two related open items -
`bug-2026-08-20-requeuetaskbyid-has-no-epoch-or-assignee-fence` and
`bug-2026-08-14-task-logs-have-no-per-task-volume-cap` - stay open and unchanged; **neither is a
prerequisite for the chosen shape**, and the first should have its promotion condition marked as *not
triggered*, since that condition was explicitly "if the watchdog item is specced with the requeue
shape".
