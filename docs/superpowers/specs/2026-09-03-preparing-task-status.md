---
date: 2026-09-03
topic: preparing-task-status
status: draft
covers:
  - docs/backlog/feature-2026-09-03-preparing-task-status.md
---

# Persist the `preparing` task status

## 0. How this spec was produced

Gate mode was `autonomous`, so every place the brainstorming flow would put a question to a human,
the call is made here with the reasoning written down, and every such call is listed again in
section 9 so it is cheap to overturn. The backlog item's Proposal was treated as a proposal: every
bullet was checked against the tree at HEAD (`01d3179`) before anything was scoped. Section 1 is the
result, refutations first.

Two numbering schemes appear below and they are deliberately distinct. **F1-F5** are the findings
from verifying the backlog item (section 1). **R0-R9** are the red-first implementation steps
(section 6). **D1-D10** are decisions (section 9). Where the coordinator-stale-task-watchdog spec's
own R-numbers are cited they are named as "the watchdog spec's R3".

---

## 1. Verification of the backlog item

### 1.1 The headline claim is CONFIRMED, and the enumeration is exact

The item says the site count in `internal/store/query/tasks.sql` is **thirteen**. Verified by
searching the SHAPE (`status IN (`, `status = '`, `status != '`, `status <> '` on `tasks` columns)
across the whole file rather than by opening the thirteen named statements, because a completeness
claim is a claim about the complement. `tasks.sql` carries **twenty-one** statements that name a
`tasks.status` value. Thirteen of them slice the set this change widens, and they are exactly the
thirteen the item names, with exactly the predicates it implies:

| # | Statement | Predicate at HEAD | Change |
|---|---|---|---|
| 1 | `UpdateTaskStatus` | `status IN ('pending', 'dispatched', 'running')` | **add `preparing`** |
| 2 | `IncrementTaskRetryCount` | `status IN ('pending', 'dispatched', 'running')` | **add** |
| 3 | `AppendTaskLog` | `(t.status IN ('pending', 'dispatched', 'running') OR t.finished_at > min_finished_at)` | **add to the FIRST ARM only** |
| 4 | `GetActiveTasksForWorker` | `status IN ('dispatched', 'running')` | **add** |
| 5 | `ListGraceCandidates` | `t.status IN ('dispatched', 'running')` | **add** |
| 6 | `RequeueTaskByID` | `status IN ('dispatched', 'running')` | **add** |
| 7 | `CountActiveTasksByAllWorkers` | `status IN ('dispatched', 'running')` | **add** |
| 8 | `ListOverdueAssignedTasks` | `status IN ('dispatched', 'running')` | **add** |
| 9 | `CancelJobTasks` | `status IN ('pending', 'queued', 'running', 'dispatched')` | **add** |
| 10 | `RequeueWorkerTasks` | `status IN ('dispatched', 'running')` | **add** |
| 11 | `RequeueWorkerTasksIfEpoch` | `status IN ('dispatched', 'running')` | **add** |
| 12 | `ListActiveTasksForWorkerPage` | `status IN ('dispatched', 'running')` | **add** |
| 13 | `CountActiveTasksForWorker` | `status IN ('dispatched', 'running')` | **add** |

The eight that are deliberately **not** changed, named so the complement is on the record rather
than implied:

| Statement | Predicate at HEAD | Why it stays |
|---|---|---|
| `GetEligibleTasks` | `t.status = 'pending'`, and `dep.status != 'done'` | Selects the queue. A `preparing` task is claimed, not queued. The dependency arm is a negation that already blocks on `preparing`, which is the fail-closed direction. |
| `ClaimTaskForWorker` | `status = 'pending'` | The only route into the assigned partition. Nothing else may claim. |
| `FailDependentTasks` | `status = 'pending'` | Terminal-only cascade over never-claimed rows. A `preparing` task has a live agent; cascading it would strand a running subprocess. |
| `RequeueTask` | `status = 'dispatched'` | Decision **D1**, section 9. |
| `UpdateTaskStatusEpoch` | no status predicate | Test-only, guarded by `internal/store/updatetaskstatusepoch_guard_test.go`. |
| `SelectRetryableTaskIDs` | `status IN ('failed','timed_out') OR (include_done AND status = 'done')` | Terminal-only operator selection. |
| `RetryJobTasks` | the same twice, plus `dep.status <> 'pending'` | Same. Admitting a non-terminal status would let an operator retry evict a live agent. The dependents guard is a negation and already blocks on `preparing`. |
| `CountTerminalTasksForWorker` | `status IN ('done', 'failed', 'timed_out')` | Terminal-only. A `preparing` row is rescued by `RequeueWorkerTasks`, so it must stay out of the attribution count or it is double-counted. |

### 1.2 FINDING F1 (refutation): there is a FOURTEENTH site, and it is not a statement

`idx_tasks_worker_active` (`internal/store/migrations/000018_hot_path_indexes.up.sql:10-11`) is

```sql
CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');
```

The backlog item does not mention it anywhere. `TestTasksStatusVocabularyIsExactly` does - its
failure message ends "...and the partial index `idx_tasks_worker_active` (migration 000018). Revisit
ALL OF THEM" - and its comment states the consequence precisely: "a status added to the statements
but not to the index turns the two panel queries into sequential scans rather than making them
wrong."

That understates it slightly, and the understatement matters. Postgres uses a partial index only
where the planner can prove the query predicate **implies** the index predicate.
`status IN ('dispatched','preparing','running')` does not imply `status IN ('dispatched','running')`,
so widening the eight assignment-partition statements without widening the index makes the index
unusable **for all eight**, not only for the panel. The worst of them is
`CountActiveTasksByAllWorkers`, which the dispatcher runs **every dispatch cycle** and which
aggregates the whole `tasks` table; today it is served by a partial index covering a tiny live
fraction, and it would fall back to a scan over every task row the system has ever created. No test
in the tree can see this, which is why one is added (section 6, R6).

**The index widening is in scope and lands in the same migration.**

### 1.3 FINDING F2 (refutation): the Python SDK cannot raise, so there is no compatibility case

The item asks to "check whether an unknown value raises on parse, because an old SDK against a new
server is the compatibility case." It does not raise. `python/src/relay/models.py` declares

```python
class Task(BaseModel):
    ...
    status: Optional[str] = None
```

`Task.status` is a plain `Optional[str]`, not `Optional[TaskStatus]`. The sibling `JobStatus`
docstring states the policy explicitly - "Job.status is typed as str on response models so unknown
future values parse cleanly; this enum exists for IDE autocomplete and comparison" - and `Task`
follows it. An SDK built before this change parses `"preparing"` without complaint.

Two consequences. First, the SDK is **not** a compatibility risk and needs no defensive work; the
enum member is added for autocomplete only. Second, the property is load-bearing and pinned by
nothing, so this slice adds a test that pins it (section 6, R8) - the risk is not an old SDK, it is
a future edit that "tightens" `status` to the enum type and turns every unknown value into a parse
error at every consumer at once.

Noted and NOT fixed here: `TaskStatus` already carries `QUEUED`, `BLOCKED` and `CANCELLED`, none of
which the server can produce (`tasks_status_check` admits six values and none of those three is
among them). That is `idea-2026-07-01-dead-status-vocabulary`'s subject, not this slice's.

### 1.4 FINDING F3 (refutation): `taskStatusColor` is not in `api.ts`, and its test's name is a count

The item says "`web/src/jobs/api.ts` `TaskStatus` union and `taskStatusColor`". The union is in
`api.ts:153`; `taskStatusColor` and `isTerminalTask` are in **`web/src/jobs/taskStatus.ts`**, and
their tests are in `web/src/jobs/taskStatus.test.ts`, whose first case is named
`maps each of the six task statuses to a dot class`. Adding a seventh makes that name false, and a
name is not something any check will redden. Delete the cardinal rather than incrementing it, per
this project's own recorded lesson about corrections regenerating the defect at the next member.

`taskStatusColor` has a `default:` arm returning `text-fg-mute`/`bg-fg-mute`, so an un-cased status
renders muted grey rather than crashing - the SPA has the same non-raising property the SDK has, in
its own idiom. That is why the union widening and the `switch` case must land together: the union
alone changes nothing a user sees, and the compiler will not complain, because the `default` arm
makes the `switch` non-exhaustive by construction.

### 1.5 FINDING F4 (refutation): the item reads several sites as fixes; they are regression prevention

The item's Context says `internal/api/jobs.go`'s cancel handler "collects only `running` and
`dispatched` tasks for agent cancel signals, so a `preparing` task would be cancelled in the
database and never told to stop." The clause is true, and the framing invites the wrong reading.

**At HEAD a syncing task's row IS `dispatched`**, so it is collected today, and `CancelJobTasks`
matches it today, and `RequeueWorkerTasks` requeues it today, and the watchdog's absolute arm sweeps
it today, and the panel shows it today. Not one of the fourteen sites is broken at HEAD.

They break the moment the row stops being `dispatched`. So:

> **This slice is a single atomic partition widening. There is no site on the list where omitting
> `preparing` merely fails to improve something; every omission is a live regression against
> today's behaviour, and eleven of the fourteen are silent.**

That is the load-bearing framing for the plan. It sets the failure mode of a partial implementation
(a syncing task that holds its worker slot and its job forever, invisible to reconcile, to the grace
timers, to the requeues, to the dispatcher's slot arithmetic, to the watchdog and to the panel, with
its log output discarded entirely), and it is why no intermediate commit may ship a widened
vocabulary alongside an un-widened partition.

### 1.6 FINDING F5: the guard names are right, but their lanes differ and the item does not say so

The item says "`TestTasksStatusVocabularyIsExactly` names every SQL site, and
`TestTaskStatusWritableSetMatchesTheSQLAllowList` pins the Go side." Both exist under those exact
names. Their **lanes** are different and the item does not say so:

| Guard | File | Lane |
|---|---|---|
| `TestTasksStatusVocabularyIsExactly` | `internal/store/tasks_status_vocabulary_lockstep_test.go` | `//go:build integration` - **Docker required** |
| `TestTaskStatusWritableSetMatchesTheSQLAllowList` | `internal/worker/taskstatus_fence_counters_test.go` | **no build tag** - `make test` |
| `tasksStatusVocabulary` (helper) | same file | no build tag |
| `TestStatusVocabularyConstraints_*` | `internal/store/status_vocabulary_constraints_test.go` | integration |
| `TestListActiveTasksForWorkerPage_*` | `internal/store/list_active_tasks_for_worker_integration_test.go` | integration |
| `TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition` | `internal/api/workers_tasks_integration_test.go` | integration |
| `TestHotPathIndexes`, `TestHotPathIndexesDownUp` | `internal/store/hot_path_indexes_integration_test.go` | integration |

Every RED in section 6 names its lane. This matters because
`idea-2026-08-23-integration-only-guards-ci-never-runs` is open: an integration-only RED is a RED
nobody will see in CI, so where a property can be pinned in the default lane it is, and where it
cannot, the spec says so.

### 1.7 Everything else in the item, confirmed against the tree

| Claim | Evidence |
|---|---|
| `handleTaskStatus` has no `TASK_STATUS_PREPARING` case | `internal/worker/handler.go`, the enum switch: cases for `RUNNING`, `DONE`, `FAILED`, `TIMED_OUT`, `PREPARE_FAILED`, then `default: return`. A `PREPARING` report is discarded with no write and no log line. Confirmed. |
| `started_at` is stamped under a `running` condition | Same function: `startedAt := task.StartedAt` then `if statusStr == "running" { startedAt = ...time.Now()... }`. Confirmed, and it is the only site in the tree that SETS the column. |
| `taskStatusIsWritable` mirrors the allow-list in Go | `internal/worker/taskstatus_fence_counters.go`, `case "pending", "dispatched", "running": return true`. Its own comment already names `preparing` as "the live candidate" and states that either order of edit goes RED. Confirmed. |
| `tasksStatusVocabulary` requires exactly one `ADD CONSTRAINT tasks_status_check` | `require.Len(t, from, 1, "expected exactly one up-migration to ADD CONSTRAINT tasks_status_check, found %d (%v)...")`. A drop-and-re-add in `000023` makes it two. Confirmed. It scans `*.up.sql` only, so the down migration's re-added narrow constraint is invisible to it. |
| `internal/api/jobs.go` filters to `running`/`dispatched` | `if (t.Status == "running" \|\| t.Status == "dispatched") && t.WorkerID.Valid`. Confirmed. |
| `web/src/jobs/api.ts` `TaskStatus` union is the six values | `export type TaskStatus = 'pending' \| 'dispatched' \| 'running' \| 'done' \| 'failed' \| 'timed_out'`. Confirmed. |
| The `logs.go` comment names `CancelJobTasks` as omitting `preparing` | `internal/cli/logs.go`, `emitSnapshot`: "CancelJobTasks' allow-list omits `preparing` ... so a cancelled job with a preparing task reaches this line the day that status lands." Confirmed, and section 7.1 says what it becomes. |
| README's `RELAY_TASK_MAX_ASSIGNMENT` row says "spends its entire workspace sync in `dispatched`" | README line 280, verbatim. It also says "one still syncing its workspace, has no other bound" in the same row. Confirmed - **two** clauses, not one. |
| README's `GET /v1/workers/{id}/tasks` row says "(`dispatched` or `running`)" | README line 1693, verbatim. Confirmed. |
| CLAUDE.md's Invariants paragraph calls `preparing` "the live candidate" | CLAUDE.md, the epoch-fence bullet, twice - once at the `AppendTaskLog` carve-out and once at `ListOverdueAssignedTasks`. Confirmed. |
| The next migration number is `000023` | `internal/store/migrations/` runs `000001` to `000022_scheduled_jobs_last_error`. Confirmed. |
| The agent already sends `PREPARING`, so no agent change | `internal/agent/runner.go`: sent once, only when `task.Source != nil && r.provider != nil`, strictly before `provider.Prepare` and strictly before the `RUNNING` send. `TestRunner_...` pins the phase sequence `[PREPARING, RUNNING, DONE]`. Confirmed. |
| `RecomputeJobStatus` treats it as non-terminal by construction | `internal/store/query/jobs.sql`: `COUNT(*) FILTER (WHERE status NOT IN ('done','failed','timed_out')) > 0 THEN 'running'`. A deny-list on the terminal set, so any new status counts as non-terminal without an edit. Confirmed. This is the one place in the tree where a deny-list is the correct shape, and it is correct here because the complement it fails open into is the harmless one. |

### 1.8 A third README site the item does not name

README line 965, in the worker-delete section: "`tasks.worker_id` is `ON DELETE SET NULL` for
**every** row, but only `dispatched`/`running` tasks are requeued". After this slice that sentence
is wrong in the direction that matters - it under-describes what is rescued - and it is the prose
half of the `CountTerminalTasksForWorker` / `RequeueWorkerTasks` pairing that
`TestTasksStatusVocabularyIsExactly`'s comment calls "the one gap this pairing cannot self-detect."
In scope.

### 1.9 What the watchdog spec's R3 obliges this slice to preserve

`docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md`, R3:

> **The `dispatched` state legitimately spans an unbounded workspace sync, so there is no shorter
> honest bound for it.** ... This kills the tempting idea of a short "dispatched but never acked"
> timeout: any such bound would have to exceed the longest legitimate sync, which is the absolute
> cap. **One bound, not two.**

The ordering property this slice must preserve, stated as an obligation:

> **A workspace sync must remain bounded by exactly one arm - the absolute arm, keyed on
> `assigned_at` - and by no other. `PREPARING` arrives BEFORE the sync starts, so any clock the
> coordinator starts on that message is a clock that runs for the whole sync.**

Two edits discharge it, and they pull in opposite directions, which is why both must be explicit:

1. `preparing` **must** enter `ListOverdueAssignedTasks`'s partition, or the absolute arm stops
   covering the sync and the unbounded-assignment hole the watchdog exists to close silently
   reopens for exactly the state that most needs it.
2. `started_at` **must not** be stamped at `preparing`, or the execution arm starts during the sync
   and a task with a thirty-minute `timeout_sec` and a two-hour sync is swept `timed_out` mid-sync,
   with no way for the agent to object. This is the fork's regression, and the item is right about
   it. README line 279 states the contract in operator-facing words - "Applies only to tasks with
   `timeout_sec > 0` that have reported `running`" - and that sentence is **unchanged by this
   slice**, deliberately. If a reviewer finds themselves editing it, the design has been violated.

The watchdog spec itself is a record of a moment and stays as written, including its now-historical
sentence about `handleTaskStatus` having no `PREPARING` case. Live prose derived from it (README
line 280) is not a record of a moment and does change.

---

## 2. What this slice does NOT do

Stated first, because several are things a reader of the backlog item will expect.

1. **No agent change.** The agent already sends `PREPARING`. An older server ignores it at
   `default: return` exactly as today, so a new agent against an old server is unchanged.
2. **No new statement that writes `tasks.status`.** The watchdog slice's discipline is kept: every
   write still goes through `UpdateTaskStatus`, so the epoch fence, the identity predicate and the
   terminality allow-list are unchanged as machinery. This is what makes the whole slice a set
   widening rather than a new write path.
3. **No `preparing` for a task with no `source`.** The agent sends `PREPARING` only when
   `task.Source != nil && r.provider != nil`, so a plain command task goes `dispatched -> running`
   as it does today. The acceptance criterion is correctly scoped to source-bearing tasks.
4. **No elapsed-time column on the worker-tasks panel.** Decision **D2**.
5. **No `prepare` value for `task_logs.stream`.** Prepare progress already lands on `stdout` via
   `handleTaskLog`; `2026-09-03-prepare-failure-visibility` section 4.1 settled that and nothing
   here reopens it.
6. **No fix for `CancelJobTasks`'s dead `'queued'` literal.** Decision **D7**.
7. **No task-level `cancelled` status.** It is the other live candidate named by every guard in this
   area, and it needs its own spec: it is terminal, so it partitions the opposite way at nine sites.

---

## 3. The invariants lens, up front

Checked against CLAUDE.md's Invariants before any design choice was made.

- **Epoch fence.** Untouched. `UpdateTaskStatus` keeps all three predicates; the status allow-list
  gains a member and stays an allow-list. `preparing` is non-terminal, so it enters the *writable*
  half of the terminal/non-terminal partition at every site that splits on it, and the complement
  (`RecomputeJobStatus`, `CountTerminalTasksForWorker`, `RetryJobTasks`, `SelectRetryableTaskIDs`)
  is left alone. The two halves stay exact complements, which is the property
  `TestTasksStatusVocabularyIsExactly` exists to force somebody to check.
- **The `AppendTaskLog` carve-out.** `preparing` goes into the **first arm only**. The disjunction
  must never become a conjunction. This is stated three times in the tree already
  (`tasks.sql`'s own comment, the lockstep guard's comment, CLAUDE.md) and it is stated again in the
  plan's step, because the edit is one character away from the catastrophic version: writing
  `AND status IN (...)` in place of the arm closes the trailing-log flush for every task in the
  system with no error and no log line. Section 6 requires the conjunction mutant to be run and to
  redden `TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored`.
- **Single job-spec pipeline.** Not touched; no spec type, no task-creation path.
- **One bounded sender per gRPC stream.** Not touched. A `preparing` report is handled on the recv
  goroutine like every other status, and adds no send, no lock and no goroutine.
- **Identity-checked teardown.** Not touched.
- **No interior pointers across locks.** Not touched.
- **Single JSON entry point.** Not touched.

### 3.1 Load and cost

Today a `PREPARING` message costs zero database round trips (it is discarded before any query).
After this change it costs the same two statements a `RUNNING` report costs: one `UpdateTaskStatus`
and one `RecomputeJobStatus`, plus one SSE publish. The agent sends it **once per source-bearing
task per assignment**, so the steady-state cost is one extra pair of statements per source task
dispatched - negligible against the dispatch itself.

Nothing rate-limits status messages, so a misbehaving agent can send `PREPARING` repeatedly. That is
not a new class: `tasks.sql`'s own comment already records that `AgentMessage_TaskStatus` is
unbudgeted and that a repeated `RUNNING` drives the identical pair of statements today. The one
genuinely new capability is the backward transition, which is **D5**.

### 3.2 Threat model delta

An agent gains the ability to move **its own currently-assigned task** into `preparing` at its own
current epoch. It could already move that task to `running`, `done`, `failed` and `timed_out` at the
same epoch, so the set of rows it can write is unchanged and the set of values it can write grows by
one non-terminal member. It gains nothing over a task it does not own: the Go identity gate and the
SQL `worker_id` predicate are both upstream of the enum switch and are untouched.

It does **not** gain a way to extend the execution bound, because `started_at` is not stamped at
`preparing` and `UpdateTaskStatus`'s `COALESCE` makes the column write-once per assignment either
way. It does **not** gain a way to escape the absolute bound, because `assigned_at` is written only
by `ClaimTaskForWorker` and `preparing` enters the swept partition. It does **not** move any fence
counter it could not already move: `classifyStatusFenceRejection` labels from the row's status at T0
and `preparing` becomes writable at T0, so a rejection for a `preparing` row is labelled `raced`,
which is the honest label - a concurrent writer ended the generation.

---

## 4. Design

### 4.1 Migration `000023_task_preparing_status`

Two objects move, in one transaction (golang-migrate wraps each migration; that is also why
`CREATE INDEX CONCURRENTLY` is unavailable, exactly as 000018's own header notes).

**Up**, in this order:

```sql
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','preparing','running','done','failed','timed_out'));

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'preparing', 'running');
```

**The three-line `ALTER ... ADD CONSTRAINT ... CHECK (status IN (` shape is load-bearing**, not
formatting. `tasksStatusVocabulary`'s regex is
`ADD CONSTRAINT tasks_status_check\s+CHECK \(status IN \(([^)]*)\)`; written on one line, or with
the `CHECK` on the same line as the table name, the parse silently misses this migration and the
guard keeps reporting the OLD vocabulary while passing. That is the failure the guard's own comment
predicts, and it is a fail-open, so the plan pins it: after writing the migration, the guard must be
observed to see **two** definitions before the helper is rewritten (section 6, R1).

**Down**, in this order, and the order IS the correctness argument:

```sql
UPDATE tasks SET status = 'dispatched' WHERE status = 'preparing';

DROP INDEX IF EXISTS idx_tasks_worker_active;

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;

ALTER TABLE tasks
  ADD CONSTRAINT tasks_status_check
  CHECK (status IN ('pending','dispatched','running','done','failed','timed_out'));
```

The `UPDATE` must precede the narrowed `ADD CONSTRAINT` or the constraint add fails against any
existing `preparing` row and the down migration is simply unrunnable - the version of this file that
"looks right" is the version that has never been run with data. `TestMigration000023_Down...`
(section 6, R7) seeds a `preparing` row precisely so that the ordering is the thing under test.

Demotion to `dispatched` and not to `pending` is deliberate: the row still has a live agent, a
`worker_id` and an `assignment_epoch`, and `dispatched` is the state that describes it truthfully in
the old vocabulary - which is exactly what the row's state was before this migration existed. A
demotion to `pending` would end a live assignment without bumping the epoch, which is an epoch-fence
violation performed by a migration.

The `.down.sql` file re-adds a constraint by the same name. That is invisible to
`tasksStatusVocabulary`, which filters on `.up.sql`, so it does not re-break the count.

**Operational note.** `CREATE INDEX` without `CONCURRENTLY` takes a lock that blocks writes to
`tasks` for the duration of the build, and migrations run at server startup. The index is partial
over the currently-assigned rows only, which is a small fraction of any real `tasks` table, so the
build is bounded by live work rather than by history. Say so in the migration's header comment.

### 4.2 `handleTaskStatus`

One case, in the existing enum switch, between `RUNNING` and `DONE`:

```go
case relayv1.TaskStatus_TASK_STATUS_PREPARING:
    statusStr = "preparing"
```

Everything downstream is already correct for it and must not be touched:

- `terminal := statusStr == "failed" || statusStr == "timed_out"` - `preparing` is non-terminal, so
  the error-message append, the retry branch, `FailDependentTasks` and the `NotifyTaskCompleted`
  wake are all skipped, correctly.
- `startedAt := task.StartedAt; if statusStr == "running" { ... }` - **stays exactly as written.**
  A `preparing` report passes the row's existing `started_at` straight back through
  `UpdateTaskStatus`'s `COALESCE`, which for a `dispatched` row is NULL in and NULL out.
- `finishedAt` stays the zero value, binding SQL NULL.
- The SSE `task` frame publishes with `"status":"preparing"`, so the SPA's job view and the CLI's
  log follower both see the transition live. Neither treats it as terminal.
- `updateJobStatusFromTasks` runs and returns `running`, because `RecomputeJobStatus`'s deny-list
  counts `preparing` as non-terminal.

The item's instruction to reject the fork's Go-side `!task.StartedAt.Valid` guard is adopted: it
duplicates the `COALESCE`, and a second guard on a property the statement already enforces is the
shape that goes stale when the statement changes. `UpdateTaskStatus`'s comment already states the
`COALESCE` is a fence in its own right and names the two tests that pin both directions.

### 4.3 The SQL edits

Thirteen predicates gain `'preparing'`. Written in the order the existing lists use so the diff
reads as an insertion: `('pending', 'dispatched', 'preparing', 'running')` for the writable set,
`('dispatched', 'preparing', 'running')` for the assignment partition, and
`('pending', 'queued', 'preparing', 'running', 'dispatched')` for `CancelJobTasks` (whose ordering
is already non-canonical; leave it alone beyond the insertion - D7).

`AppendTaskLog` gains it in the **first arm of the disjunction only**:

```sql
AND (t.status IN ('pending', 'dispatched', 'preparing', 'running')
     OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
```

Then `make generate`, then the CRLF revert procedure in CLAUDE.md: `git diff --ignore-all-space`,
keep only the real content change, `git checkout --` the LF-only hunks, and **verify the regenerated
`tasks.sql.go` survived the revert** by grepping it for `preparing` at each of the thirteen
statements. The repo's own recorded failure here is a revert that discarded the regeneration while
`git diff` reported nothing to revert; the diffstat and `git ls-files --eol` checks apply.

### 4.4 `taskStatusIsWritable`

```go
case "pending", "dispatched", "preparing", "running":
```

Spelled inline. Moving the set to a var, a const or a helper is not a refactor here - the guard's
`taskStatusWritableLiterals` reads the function's own AST and fails closed with a message saying so.

### 4.5 `internal/api/jobs.go`, the cancel-signal collection

```go
if (t.Status == "running" || t.Status == "dispatched" || t.Status == "preparing") && t.WorkerID.Valid {
```

`internal/agent/agent.go`'s `handleCancel` looks the runner up in `a.runners`, which is populated
before `Runner.Run` sends `PREPARING`, so the agent side already handles a cancel arriving mid-sync:
`r.Cancel(force)` cancels the context that `provider.Prepare` is running under. Nothing on the agent
changes.

### 4.6 What is deliberately left alone in the assignment path

`GetActiveTasksForWorker` feeds `reconcileRunningTasks`, which requeues any assigned task the
reconnecting agent did not report. A preparing task **is** reported: `buildRegisterRequest` walks
`a.runners`, which holds the runner for the whole of `Run`, including the sync. So widening
`GetActiveTasksForWorker` does not create a new requeue of live work - it preserves today's
behaviour, in which the row is `dispatched` and already in the partition. Stated here because "add
`preparing` to the reconcile scan" reads like a new exposure and is not one.

---

## 5. The seven-status partition, as a single table

The artifact a future reader needs. Every site in the tree that splits `tasks.status`, and which
side `preparing` lands on.

| Site | Kind | Side | Fails how if wrong |
|---|---|---|---|
| `tasks_status_check` (000023) | vocabulary | member | writes rejected loudly |
| `idx_tasks_worker_active` (000023) | partial index | **in** | silent scans on 8 statements |
| `UpdateTaskStatus` | write allow-list | **in** | agent's own transitions dropped silently |
| `IncrementTaskRetryCount` | write allow-list | **in** | a preparing task cannot burn a retry |
| `AppendTaskLog` first arm | log fence | **in** | 100% of sync log output discarded, silently |
| `AppendTaskLog` recency arm | log fence | out | never conjoin the arms |
| `GetActiveTasksForWorker` | assignment | **in** | reconcile never sees it |
| `ListGraceCandidates` | assignment | **in** | no grace timer covers it |
| `RequeueTaskByID` | assignment (write) | **in** | reconcile cannot end the assignment |
| `RequeueWorkerTasks` | assignment (write) | **in** | disconnect/disable never releases it |
| `RequeueWorkerTasksIfEpoch` | assignment (write) | **in** | same, on the epoch-fenced path |
| `CountActiveTasksByAllWorkers` | assignment | **in** | dispatcher overcommits the worker |
| `ListOverdueAssignedTasks` | assignment (watchdog) | **in** | never swept; holds slot and job forever |
| `ListActiveTasksForWorkerPage` | assignment (read) | **in** | absent from the panel |
| `CountActiveTasksForWorker` | assignment (read) | **in** | Slots KPI under-reports |
| `CancelJobTasks` | non-terminal (write) | **in** | cancel leaves the row live |
| `RequeueTask` | dispatch-failure revert | **out** | D1 |
| `ClaimTaskForWorker` | queue exit | out | - |
| `GetEligibleTasks` | queue | out | - |
| `FailDependentTasks` | cascade | out | would strand a running subprocess |
| `RecomputeJobStatus` | terminal deny-list | non-terminal, automatically | - |
| `CountTerminalTasksForWorker` | terminal | out | would double-count with the requeue |
| `RetryJobTasks` / `SelectRetryableTaskIDs` | terminal | out | operator retry would evict a live agent |
| `RetryJobTasks` dependents guard | negation | blocks, automatically | - |
| `taskStatusIsWritable` (Go) | counter label | **in** | a real race labelled `duplicate`/`conflicting` |
| `handleTaskStatus` enum switch | wire mapping | **in** | the whole feature |
| `internal/api/jobs.go` cancel collection | Go | **in** | cancelled in the DB, never told to stop |
| `internal/cli/logs.go` `taskIsTerminal` | terminal | out | - |
| `internal/cli/logs.go` `jobIsTerminal` | jobs vocabulary | n/a | - |
| `web/src/jobs/api.ts` `TaskStatus` | TS union | **in** | - |
| `web/src/jobs/taskStatus.ts` `taskStatusColor` | display | **in** | renders muted grey |
| `web/src/jobs/taskStatus.ts` `TERMINAL` | terminal | out | - |
| `python/.../models.py` `TaskStatus` | SDK enum | **in** | autocomplete only |

---

## 6. The red-first sequence

Every guard must be shown to go RED before it goes green. Three of the properties below cannot go
red at HEAD, because the state they describe is unrepresentable until the migration lands; those are
labelled and their RED is obtained by a named mutation instead. A mutation proof must leave a test
behind, and the discriminating input goes first, not last.

**R0 - baseline, both ways.** Run `make test` and the `internal/store` + `internal/worker` +
`internal/api` integration packages at HEAD and record green. Nothing below may be diagnosed against
an unmeasured baseline.

**R1 - the migration-parsing helper. Default lane, and it must come first.**
Write `000023_task_preparing_status.up.sql` and `.down.sql` and nothing else. Expected RED:
`tasksStatusVocabulary` fails `require.Len(t, from, 1)` with `found 2 ([000019... 000023...])`,
which fails `TestTaskStatusWritableSetMatchesTheSQLAllowList` in the **default** lane. Observe that
message and record the two filenames - it is the proof that the migration's formatting matches the
regex, which nothing else checks.
Then rewrite the helper: strip `--` comment lines from each file before matching, keep every match,
and take the **last** in `os.ReadDir` order (which is lexical, so `000023` wins). Keep
`require.NotEmpty(out, ...)`. Add one assertion the item does not ask for: require the chosen file
to be the lexically greatest of the matches, so a future rewrite that silently takes the FIRST match
is caught rather than reading a stale vocabulary forever - the exact fail-open the current
`require.Len` exists to prevent, re-armed in the new shape.
Also RED in the **integration** lane: `TestTasksStatusVocabularyIsExactly`'s six-value `want`.
Add `"preparing"`, and rewrite the comment per section 7.1.

**R2 - the Go mirror. Default lane, both orders measured.**
Add `preparing` to `UpdateTaskStatus` and `IncrementTaskRetryCount` **only**. Expected RED:
`TestTaskStatusWritableSetMatchesTheSQLAllowList`'s SQL-to-Go containment loop, message
"tasks.sql's UpdateTaskStatus admits status "preparing" and taskStatusIsWritable says it is NOT
writable". Green by adding it to `taskStatusIsWritable`.
Then measure the opposite order once, on a scratch copy: Go first, SQL untouched. Expected RED: the
set-comparison rung ("taskStatusIsWritable's own source names a different set of statuses"). Both
directions are asserted by the guard's comment; running both is what turns that claim into a
measurement. Record which message each produced.

**R3 - the handler case. Integration lane.**
New: `TestHandleTaskStatus_APreparingReportMovesTheRowAndLeavesStartedAtNull`. Seed a claimed task,
send `TASK_STATUS_PREPARING` at the current epoch from the assignee, assert `status == "preparing"`
**and** `!row.StartedAt.Valid`. RED at HEAD on the first assertion: the enum falls to
`default: return` and the row is still `dispatched`.
Green with the one `case`. Then run the fork's regression as a mutation: change the stamp condition
to `statusStr == "running" || statusStr == "preparing"` and require the `started_at` half of this
test to redden. If it does not, the test is not discriminating and must be strengthened before
proceeding - a kill must name its guard.

**R4 - the watchdog, both arms. Integration lane. RED by mutation, and say so.**
Two tests, and neither can be red at HEAD because a `preparing` row cannot exist there.

- `TestWatchdog_APreparingTaskWithATinyTimeoutIsNotSweptByTheExecutionArm`: a `preparing` row with
  `timeout_seconds = 1`, `started_at` NULL, `assigned_at` a moment ago; run `SweepOnce` well past
  the margin; assert zero sweeps and the row still `preparing`. Its RED is R3's mutation (stamp
  `started_at` at `preparing`), which makes the execution arm fire. Run it and record the failure.
- `TestWatchdog_APreparingTaskIsStillSweptByTheAbsoluteArm`: the same row with `assigned_at` older
  than `RELAY_TASK_MAX_ASSIGNMENT`; assert it is swept and stamped `timed_out`. **Write and run this
  one BEFORE R5 widens `ListOverdueAssignedTasks`**, so its RED is a real un-widened statement and
  not a mutation. This is the test that pins the watchdog spec's R3 obligation (section 1.9).

**R5 - the assignment partition, eleven statements, each with its own RED. Integration lane.**
Write every test first, against a tree that has `preparing` writable (R2/R3) and the partition
un-widened. All should be red. Then one SQL pass and one `make generate`.

| Test | Statement | RED after R3 |
|---|---|---|
| `TestAppendTaskLog_APreparingTaskAcceptsLogChunks` | `AppendTaskLog` | fence matches nothing, `pgx.ErrNoRows` |
| `TestGetActiveTasksForWorker_IncludesAPreparingTask` | `GetActiveTasksForWorker` | row absent |
| `TestListGraceCandidates_AWorkerWithOnlyAPreparingTaskIsACandidate` | `ListGraceCandidates` | worker absent |
| `TestRequeueTaskByID_RequeuesAPreparingTaskForItsAssignee` | `RequeueTaskByID` | rowcount 0 |
| `TestRequeueWorkerTasks_RequeuesAPreparingTask` | `RequeueWorkerTasks` | id not returned |
| `TestRequeueWorkerTasksIfEpoch_RequeuesAPreparingTask` | `RequeueWorkerTasksIfEpoch` | id not returned |
| `TestCountActiveTasksByAllWorkers_CountsAPreparingTask` | `CountActiveTasksByAllWorkers` | count short by one |
| `TestCancelJobTasks_FailsAPreparingTask` | `CancelJobTasks` | row still `preparing` |
| rename `TestListActiveTasksForWorkerPage_ReturnsBothAssignedStatuses` to `..._ReturnsEveryAssignedStatus`, add a third row | `ListActiveTasksForWorkerPage` | 2 rows, expected 3 |
| extend the same file's count assertion | `CountActiveTasksForWorker` | total 2, expected 3 |
| extend `TestListWorkerTasks_ReturnsOnlyTheAssignmentPartition` (api) | both, end to end | seeds 7 statuses, expects 3 |

Plus, in `internal/api`: `TestCancelJob_SendsACancelSignalForAPreparingTask`, whose RED is the Go
filter in `jobs.go`, not the SQL - it must be run against a tree where `CancelJobTasks` is already
widened, or it goes red for the wrong reason and proves nothing about the filter.

Then the **`AppendTaskLog` conjunction mutant**: replace the disjunction with
`AND t.status IN ('pending','dispatched','preparing','running')` and require
`TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored` to redden. Restore from a
copy, never with `git checkout --`, and re-run the control.

**R6 - the index. Integration lane. New guard, because nothing can see this today.**
`TestActiveTaskIndexPredicateMatchesTheAssignmentPartition`: read the partial index's predicate off
the live catalog (`pg_get_expr(indpred, indrelid)` for `idx_tasks_worker_active`, or the `WHERE`
clause of `pg_indexes.indexdef`), extract its quoted literals, and require the set to equal
`{dispatched, preparing, running}`. RED before the migration's index half is written: the set is
`{dispatched, running}`.
An `EXPLAIN`-based assertion is **declined** (D10): plan choice depends on statistics and table
size, so a green `EXPLAIN` on a small test table proves nothing and a red one is a flake. Pinning
the predicate is the property; the plan is its consequence.
`TestHotPathIndexes` and `TestHotPathIndexesDownUp` assert index **names** only, so both stay green
across this change - which is exactly why R6 is needed, and is worth saying in R6's comment.

**R7 - the down migration. Integration lane.**
`TestMigration000023_DownDemotesPreparingRowsAndNarrowsTheConstraint`, modelled on
`TestMigration000020_DownDropsListEndpointIndexes` and `tasks_assigned_at_integration_test.go`:
seed a `preparing` row; `store.MigrateTo(dsn, 22)`; assert the row reads `dispatched`; assert an
`INSERT ... status = 'preparing'` is now rejected; assert the index predicate no longer names
`preparing`; then `store.Migrate(dsn)` back up and assert a clean re-up (no duplicate-name collision
on the index, constraint present again).
Its RED is the down file written in the wrong order - `ADD CONSTRAINT` before the `UPDATE` - which
fails the `MigrateTo` call itself. Write it that way once, watch it fail, then fix it. The seeded row
is what makes the ordering observable; without it the wrong order passes.
Note for the plan: `TestStatusVocabularyConstraints_RoundTrip` already drives down to 18 and back up
and must stay green, which exercises 000023's down as a side effect.

**R8 - clients. Vitest and pytest.**
- `taskStatusColor('preparing').dot === 'bg-accent'` and `.text === 'text-accent'`. RED at HEAD: the
  `default` arm returns `bg-fg-mute`. Green with the case added beside `running`/`dispatched`.
- Rename `maps each of the six task statuses to a dot class`; delete the cardinal rather than
  writing "seven".
- `isTerminalTask('preparing') === false`: **green at HEAD, and that is stated rather than
  disguised.** It is a regression guard against a future edit that adds `preparing` to `TERMINAL`,
  not a red-first criterion. A replacement criterion that is already green must be labelled as one.
- Python: add `PREPARING = "preparing"` to the enum. New test
  `test_task_status_accepts_an_unknown_server_value`, asserting `Task(name="t", status="preparing")`
  and `Task(name="t", status="a-status-from-the-future")` both parse and round-trip. **Green at
  HEAD, deliberately** - it pins the `Optional[str]` choice (F2) against a future tightening, which
  is the only way this SDK can break.

**R9 - prose.** Section 7. No test; the edit is the deliverable. After every programmatic doc edit:
check the diffstat against the intended size, confirm `git ls-files --eol` reads `i/lf`, confirm the
file still decodes as UTF-8, and confirm no non-ASCII byte was introduced.

---

## 7. The prose sweep

Wrong prose about correct code is this repo's dominant defect class, and eleven passages in the tree
name `preparing` as a *future* candidate. After this slice every one of them is a statement about
the past written in the present tense. Rewriting them is required scope.

### 7.1 Comments and guards that name `preparing` as "the live candidate"

| File | Passage | Becomes |
|---|---|---|
| `internal/store/query/tasks.sql`, `AppendTaskLog` | "The day `preparing` becomes a persisted status and is not added here, every workspace-sync log line in the system disappears." | present tense: `preparing` **is** in this arm; the hazard now belongs to the next non-terminal status, and `TASK_STATUS_PREPARE_FAILED` remains the opposite case. |
| `internal/store/query/tasks.sql`, `ListOverdueAssignedTasks` | "a task spends the whole workspace sync as `dispatched` (handleTaskStatus has no case for TASK_STATUS_PREPARING, so the row does not move)"; "`preparing` is the live candidate." | the sync is `preparing`, it is in this partition, and the absolute arm is still its only bound. Delete the "live candidate" sentence rather than re-pointing it at a hypothetical. |
| `internal/store/query/tasks.sql`, `ListActiveTasksForWorkerPage` | "`preparing` is the live candidate."; "a task spends the whole workspace sync as `dispatched`" (the ORDER BY paragraph) | the ordering argument survives in substance - `started_at` is still NULL through the sync, which is now `preparing` - and the noun changes. |
| `internal/store/tasks_status_vocabulary_lockstep_test.go` | the comment names `preparing` five times as a candidate; the failure message describes the partition as nine statements | rewrite each to the present tense; the assignment-partition group becomes the set that **includes** `preparing`, and the near-term candidate named throughout becomes the task-level `cancelled` the header already names. **Add `CancelJobTasks` to the census** - see 7.2. |
| `internal/worker/taskstatus_fence_counters.go`, `taskStatusIsWritable` | "A NEW NON-TERMINAL STATUS (`preparing` is the live candidate: TASK_STATUS_PREPARING is already in the proto) MUST BE ADDED HERE" | keep the rule, drop the example, or re-point it at `cancelled`, which is terminal and therefore the *counter*-example. |
| `internal/worker/taskstatus_fence_counters_test.go`, `taskStatusUniverse` | "TASK_STATUS_PREPARING is in the proto today ... so `preparing` is a candidate here years before the column can hold it" | this is the paragraph justifying why the proto is a separate source from the vocabulary. The argument survives; its example is now historical, and `TASK_STATUS_PREPARE_FAILED` is the live instance of the same shape. |
| `internal/cli/logs.go`, `taskIsTerminal` header | "`preparing` is harmless at both because it is non-terminal" | still true and no longer hypothetical; keep, minus the future tense. |
| `internal/cli/logs.go`, `emitSnapshot` | "CancelJobTasks' allow-list omits `preparing` ... so a cancelled job with a preparing task reaches this line the day that status lands." | **the reason expires.** Once `CancelJobTasks` admits `preparing`, a cancel makes every such task `failed`, so this branch is no longer reachable by the cancel route. It stays reachable by the ordering route the same file documents two comments below, and by any future non-terminal status. Rewrite to name the ordering route as the live one; do not delete the branch. |
| `web/src/workers/WorkerTasksPanel.tsx` | "A dispatched task spends the whole workspace sync with no started_at" | `preparing`. The `not started` rendering is unchanged and correct. |
| `CLAUDE.md` Invariants, `AppendTaskLog` carve-out | "(`preparing` is the live candidate: `TASK_STATUS_PREPARING` is already in the proto and the agent already streams `LOG_STREAM_PREPARE` chunks)" | describe the current partition: the first arm is `('pending','dispatched','preparing','running')`, `PREPARE_FAILED` is the terminal counter-example, and the rule itself is unchanged. |
| `CLAUDE.md` Invariants, `ListOverdueAssignedTasks` | same shape | same. |

### 7.2 A gap in the guard's own census, found while checking the complement

`TestTasksStatusVocabularyIsExactly` presents a complete list of the statements that slice the
vocabulary. **`CancelJobTasks` is not on it**, in either the comment or the failure message, despite
carrying `status IN ('pending', 'queued', 'running', 'dispatched')` - a non-terminal allow-list on a
statement that WRITES, and one of the thirteen this slice must widen. A reader following that
guard's instruction to "visit every one of these" would miss it, and the omission is silent.

This is the same defect class the guard's own comment describes twice ("a claim about the complement
cannot be checked by opening its subject"), reproduced inside the guard. Adding the entry is in
scope: `CancelJobTasks` joins the list with its own direction - a new **non-terminal** status must
be added or a cancel leaves the row live with its agent still running, and a new **terminal** status
must stay out.

### 7.3 README

| Line | Text at HEAD | Change |
|---|---|---|
| 279, `RELAY_TASK_WATCHDOG_MARGIN` | "Applies only to tasks with `timeout_sec > 0` that have reported `running`" | **no change.** This is the contract the design preserves. Editing it is the tell that `started_at` moved. |
| 280, `RELAY_TASK_MAX_ASSIGNMENT` | "a task spends its entire workspace sync in `dispatched`" | `preparing`. |
| 280, same row | "A `timeout_sec = 0` task, or one still syncing its workspace, has no other bound." | still true; make the state explicit ("or one in `preparing`") so the two rows read as one story. |
| 965, worker delete | "only `dispatched`/`running` tasks are requeued" | `dispatched`/`preparing`/`running`. |
| 1693, `GET /v1/workers/{id}/tasks` | "List the tasks currently assigned to a worker (`dispatched` or `running`)" | add `preparing`. This is a documented API contract, so a stale version here is a defect, not a typo. |

Searched for a task-status vocabulary table elsewhere in README: there is none. The only other
`timed_out` occurrences are in the retry-endpoint row (terminal, unaffected) and the counters
section (unaffected).

### 7.4 Not changed, on purpose

`docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md` R3 says `handleTaskStatus`
"has **no case for `TASK_STATUS_PREPARING`**". That is a record of the tree on 2026-08-20 and stays
as written. Specs and retros are records of a moment; comments and README are live contracts. The
item is right about this, and the distinction is worth restating in the plan, because the temptation
to "fix" the spec will be strong and doing so destroys the record that the decision was made against
that tree.

---

## 8. Acceptance criteria, and which of the item's are false

### 8.1 The item's criteria, assessed

1. *"A source-bearing task shows `preparing` from the agent's first report until `running`, in the
   API, the CLI, the SPA and the worker-tasks panel."* **TRUE and achievable.** The CLI needs no
   change: `internal/cli/jobs.go` prints `t.Status` verbatim in `relay job <id>`'s task table. The
   API returns the column verbatim. The SPA and the panel need the `taskStatusColor` case.
2. *"`started_at` stays NULL through `preparing`; the watchdog's execution arm does not fire during
   a sync; the assignment arm still bounds it."* **TRUE**, and it is the spec's central obligation
   (1.9). Pinned by R3 and R4.
3. *"Cancelling a job with a `preparing` task sends that task's agent a cancel signal."* **TRUE**,
   and note it is preservation, not a new capability (F4).
4. *"Every lockstep and vocabulary guard is green with `preparing` in the set, and each went red
   first."* **PARTLY FALSE as written**, and the plan must not adopt it verbatim. Three properties
   in this slice cannot go red first:
   - `isTerminalTask('preparing') === false` is green at HEAD and always will be.
   - The Python round-trip test is green at HEAD by design (it pins `Optional[str]`).
   - `TestWatchdog_APreparingTaskWithATinyTimeoutIsNotSweptByTheExecutionArm` cannot be red at HEAD
     because a `preparing` row is not representable there; its RED is obtained by mutation, named in
     R4.
   Restated: *every guard whose subject this slice changes went red first, and each of the three
   guards that cannot is labelled with the reason and, where a mutation can produce the red, the
   mutation is run and recorded.*
5. *"The down migration demotes `preparing` rows and restores the old constraint."* **TRUE**, and
   incomplete - it must also restore the narrow index, which the item does not mention (F1).

### 8.2 No prescribed remedy names a hook or command that does not exist

Checked: `make generate`, `make test`, `store.Migrate`, `store.MigrateTo` and the CRLF revert
procedure all exist as named. `/backlog close` exists as the documented closing command. The item
prescribes no hook. One near-miss worth flagging: the item says "a down-migration test in the store
integration lane, like the existing `000020` one" - that test exists
(`TestMigration000020_DownDropsListEndpointIndexes`, using a local `storeMigrateTo` alias over
`store.MigrateTo`), so the model is real.

### 8.3 This spec's criteria

- All 13 SQL predicates plus the partial index admit `preparing`; the eight terminal-side sites do
  not; `RequeueTask` does not.
- `handleTaskStatus` maps `TASK_STATUS_PREPARING` to `"preparing"` and stamps no `started_at`.
- The execution arm does not fire on a `preparing` row with `started_at` NULL, at any margin.
- The absolute arm still sweeps a `preparing` row past `RELAY_TASK_MAX_ASSIGNMENT`.
- `CancelJobTasks` fails a `preparing` row **and** its agent receives a `CancelTask`.
- `AppendTaskLog` accepts chunks for a `preparing` task, and the trailing-window flush still works
  (the conjunction mutant reddens its existing guard).
- `taskStatusIsWritable` equals the SQL allow-list, by both rungs of the two-way guard.
- `tasksStatusVocabulary` reads the 000023 vocabulary, not 000019's, and fails closed if a future
  migration re-adds the constraint again.
- The 000023 down migration runs against a database containing a `preparing` row, and the re-up is
  clean.
- `make test`, the three integration packages, `make test-race` in the `golang:1.26` container, and
  the `web` vitest suite are all green; `vet` clean under both tag sets.
- Every passage in 7.1, 7.2 and 7.3 is rewritten; no count is incremented - each is deleted or
  replaced by a named guard.

---

## 9. Decisions

Made autonomously. Each is stated so it is cheap to overturn.

**D1 - `RequeueTask` does NOT gain `preparing`.**
Its only production caller is `Dispatcher.dispatchOne`'s send-failure path
(`internal/scheduler/dispatch.go`), reached only when `Registry.Send` or `workerSender.Send` returns
an error - and the statement's own comment enumerates all three error values and shows that on every
one the `DispatchTask` was never queued, never written to the stream and never seen by the agent.
`PREPARING` is sent by a `Runner` that exists only because the agent received a `DispatchTask`. So
at this caller's own `(epoch, worker)` pair the task cannot be `preparing`, for the same reason it
cannot be `running`. `TestTasksStatusVocabularyIsExactly`'s own criterion for this statement - "a new
status belongs here only if a task can be in it while its `DispatchTask` is still in flight" - is not
met.
The alternative (add it "for symmetry" with `RequeueTaskByID`) is the fail-open direction for a
statement that ENDS AN ASSIGNMENT, and the statement's comment explicitly forbids exactly that
widening for `running`. Pinned by a new
`TestRequeueTask_APreparingTaskIsNotRequeuedByTheSendFailurePath`, mirroring the existing
`..._RunningTaskIsNotRequeuedByTheSendFailurePath`.
Residual, stated because the statement's own comment states its analogue: a connected agent that is
the current assignee could report `preparing` at an epoch it was never dispatched, since
`handleTaskStatus` takes the epoch off the wire. That needs a task id the agent would not otherwise
know, the watchdog bounds it, and it is unchanged in kind by this slice.

**D2 - the worker-tasks panel gets NO elapsed figure.**
Default per the brief, and there is a positive reason beyond minimalism: the panel's `STARTED`
column already renders `not started` for a row with a NULL `started_at`, and after this slice the
`STATUS` cell answers the operator's actual question ("is it syncing or is it wedged?") directly.
An elapsed-since-`assigned_at` figure is a new column, a new formatting decision and a new
truncation budget on a table whose min-width is already argued in a comment. It belongs with
`feature-2026-09-03-p4-sync-progress-heartbeat`, which is what will make an elapsed number
meaningful. If it is added later, `assigned_at` is the right anchor - `started_at` is NULL for the
whole sync - and the item is right about that.

**D3 - the Python SDK gets the enum member and nothing else.**
`Task.status` is `Optional[str]`, so an unknown value already parses (F2). The member is added for
autocomplete and comparison, matching `JobStatus`'s documented policy, and a test pins the
non-raising property so a future tightening is caught.

**D4 - `started_at` is NOT stamped at `preparing`.**
The watchdog spec's R3, README line 279's contract, and the fork's own regression all point the same
way (1.9). This is the decision with the largest blast radius if reversed.

**D5 - the backward `running -> preparing` transition is ACCEPTED, bounded and pinned.**
New capability, stated plainly: after this slice, `UpdateTaskStatus`'s allow-list admits `running`,
so a second `PREPARING` message from a task's own assignee at its own current epoch will move a
`running` row back to `preparing`. That is unreachable for a well-behaved agent (the runner sends
`PREPARING` once, before `Prepare`, and never after `RUNNING`) but it is reachable for a misbehaving
or compromised one.
Bounded: `started_at` survives (the `COALESCE`), so the execution arm is unaffected; `assigned_at`
and the epoch are untouched, so the absolute arm is unaffected; `finished_at` stays NULL; the job
stays `running`; the row stays in the assigned partition at every one of the eleven sites, so
reconcile, the grace timers, the requeues, the slot count, the watchdog and the panel all continue
to see it. The damage is a misleading status string on one row, driven by that row's own assignee -
which is the "identity is not honesty" shape already documented for the fence counters, not a new
one. Pinned by `TestHandleTaskStatus_APreparingReportAfterRunningDoesNotClearStartedAt`.
Rejected alternative: a separate statement forbidding `preparing` over `running`. It would be a new
writer of `tasks.status`, which the watchdog slice's discipline ("this slice added no new statement
that writes tasks.status") and the Invariants both argue against, and it would need its own fence
argument for a state no honest agent produces.

**D6 - `idx_tasks_worker_active` is widened in the same migration.**
Section 1.2. Splitting it into a follow-up would ship a known, silent, whole-table scan on the
dispatcher's per-cycle query.

**D7 - `CancelJobTasks`'s `'queued'` literal is left alone.**
`jobs_status_check` admits `queued` for jobs; `tasks_status_check` never has, so the literal is dead
in this statement. Removing it is a behaviour-neutral cleanup that belongs to
`idea-2026-07-01-dead-status-vocabulary`, and folding it in here would put an unrelated deletion
inside the diff a reviewer must read as a pure set widening.

**D8 - `taskStatusIsWritable` keeps its statuses spelled inline.**
Its guard reads the function's AST and fails closed on any indirection, with a message saying so.

**D9 - the migration-parsing helper takes the LAST match, not the first, and asserts it.**
The item's proposed rewrite (last definition, after stripping `--` lines) is adopted, plus one
assertion the item does not ask for: that the chosen file is the lexically greatest match. Without
it the rewrite re-creates the fail-open the current `require.Len` closes - a parse that quietly reads
000019 forever.

**D10 - no `EXPLAIN`-based index assertion.**
The property is the predicate; the plan is a consequence of statistics. Pin the predicate.

---

## 10. Lane structure and sequencing

One lane. The slice is not decomposable: F4 shows that any partial application is a live regression,
and the SQL, the Go mirror, the handler and the migration are pinned to each other by two guards
that fail in opposite directions. A second worktree would produce two branches that are each red
until merged, which is the shape the "guard must live in a lane that runs on the breaking commit"
lesson warns about.

Commit order inside the lane follows section 6: R1 (migration + helper), R2 (allow-lists + mirror),
R3 (handler), R4 (watchdog guards), R5 (partition), R6 (index guard), R7 (down migration), R8
(clients), R9 (prose). R4's absolute-arm test is written before R5's `ListOverdueAssignedTasks` edit
so that it has a real RED.

`make generate` runs **once**, after all thirteen SQL predicates are edited, followed immediately by
the CRLF revert procedure and a grep of `internal/store/tasks.sql.go` for `preparing` at all
thirteen statements. A second `make generate` later in the lane is the shape that silently discards
a regeneration.

---

## 11. Open question for the human

None that blocks. The one worth a look on review is **D5**: whether a backward
`running -> preparing` transition by a misbehaving assignee is worth a dedicated statement. The
design says no, with the bound written out; a reviewer who disagrees should say so before the plan
is written, because adding a second `tasks.status` writer afterwards is a structural change, not a
patch.
