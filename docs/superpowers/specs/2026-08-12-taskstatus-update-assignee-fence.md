# Fence task status updates on the sending worker's identity, not the epoch alone

- Date: 2026-08-12
- Backlog item: `docs/backlog/bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero.md`
- Status: design approved (autonomous mode - see section 12)
- Owner phase: Phase 1 (SPEC) of the agent-team pipeline
- Sibling, already shipped: `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` (PR #119)

## 1. Problem, and what does not need re-deriving

`handleTaskStatus` is the status-path twin of the log hole closed by PR #119. It reads the
task, compares the wire epoch against `tasks.assignment_epoch`, and proceeds. It never
asks whether the sending connection is the task's assignee.

Everything the sibling spec settled applies here unchanged and is **cited, not repeated**:

- **The epoch establishes currency, not identity.** Sibling spec sections 1 and 9.4.
- **The exposure window is every task, not just never-claimed ones.** The fence compares an
  integer; epoch 0 is merely the cheapest guess and epochs advance by one per
  requeue/retry/cancel. Sibling section 5's four-row state table transfers verbatim.
- **Threat model.** Any principal holding a long-lived agent token (a 0600 file on a host
  that by design runs untrusted payloads), plus anyone who can reach `:9090` under
  `RELAY_ALLOW_AUTO_ENROLL`, which is a full worker-identity takeover by hostname
  (`bug-2026-08-12-auto-enroll-hostname-takeover`). Task ids are discoverable by any
  authenticated user. Sibling section 5.
- **The NULL-comparison trap.** `tasks.worker_id` is nullable, so the comparison operator
  *is* the security control: `=`, never `IS NOT DISTINCT FROM`, so a caller that lost its
  identity fails closed. Sibling section 6.2.
- **Identity comes from registration, never from the wire.** No proto change. Sibling 6.3.
- **Staged implementation so RED is behavioral, not a compile error.** Sibling 8.1.
- **The amended CLAUDE.md Epoch fence bullet** already names `UpdateTaskStatus` as the
  remaining unfenced writer; this change is what retires that clause.

The rest of this spec covers only what is genuinely different, and there is more of it than
the sibling's shape suggests.

## 2. Verified facts (read in the tree at `aae22fe`)

### 2.1 The write path today

`internal/store/query/tasks.sql:26-29`:

```sql
UPDATE tasks
SET status = $2, worker_id = $3, started_at = $4, finished_at = $5
WHERE id = $1 AND assignment_epoch = $6
```

Two predicates, no worker predicate. Note it also *writes* `worker_id` (see 4).

### 2.2 Every caller of `UpdateTaskStatus`

Repo-wide grep. Exactly two in production, four in tests:

| Caller | File | Worker id it passes |
| --- | --- | --- |
| `handleTaskStatus` | `internal/worker/handler.go:481-488` | `task.WorkerID`, from the `GetTask` five lines earlier |
| `Dispatcher.failClaimedTask` | `internal/scheduler/dispatch.go:355-362` | `claimed.WorkerID`, from `ClaimTaskForWorker` |
| `TestTaskDependencyAndEligibility` | `internal/store/store_test.go:118` | none (zero value) on a **never-claimed** task at epoch 0 |
| `TestUpdateTaskStatus_EpochGuarded` | `store_test.go:220, 228` | none (zero value) on a task claimed by `w` |
| `TestUpdateTaskStatus_TerminalTransitionKeepsTheAssignee...` | `store_test.go:400` | `claimed.WorkerID` |

`claimed.WorkerID` is non-NULL by construction: `ClaimTaskForWorker` (`tasks.sql:119-124`)
is the only statement that ever sets a non-NULL `worker_id`, it sets it from `w.ID` in the
dispatcher's worker loop, and it `RETURNING *`. The backlog item's claim here is confirmed.

Also confirmed and worth recording so a reviewer does not flag it: `UpdateTaskStatusEpoch`
(`tasks.sql:189-195`) is a second epoch-fenced status writer with **zero production
callers** - it is used only by tests. It is deliberately untouched by this change.

### 2.3 `handleTaskStatus`'s side effects, mapped

`internal/worker/handler.go:418-520`. In program order after the epoch gate at `:433`:

| # | Side effect | Line | Gated on the fence actually matching? |
| --- | --- | --- | --- |
| 1 | `IncrementTaskRetryCount` | 462 | **No.** Bare `WHERE id = $1`; runs *before* `UpdateTaskStatus` and `return`s at 468 |
| 2 | `updateJobStatusFromTasks` (retry branch) | 465 | **No.** Same branch |
| 3 | `NotifyTaskSubmitted` (wakes the dispatcher) | 466 | **No.** Same branch |
| 4 | `UpdateTaskStatus` | 481 | Yes - the fence itself |
| 5 | `FailDependentTasks` (the DAG cascade) | 495 | Yes - `err != nil { return }` at 489-492 precedes it |
| 6 | `updateJobStatusFromTasks` | 500 | Yes, via `updated.JobID` |
| 7 | `broker.Publish` task event | 502 | Yes |
| 8 | `broker.Publish` job event | 509 | Yes |
| 9 | `NotifyTaskCompleted` | 518 | Yes |

Effects 5 to 9 are already correctly gated - `handleTaskStatus` follows the log path's
lesson today. **Effects 1 to 3 are not, and no SQL change to `UpdateTaskStatus` can reach
them.** This is the finding that changes the shape of the work; see 3.

### 2.4 The three consequences

All three verified. Corrections and additions:

1. **`DONE`** - confirmed. `terminal := statusStr == "failed" || statusStr == "timed_out"`
   (`:458`) excludes `done`, so the retry branch is skipped and `UpdateTaskStatus` writes
   `status = 'done'`. `GetEligibleTasks` (`tasks.sql:38-48`) unblocks a dependent on
   `dep.status != 'done'` being false, so the DAG proceeds against work that never ran, and
   `RecomputeJobStatus` (`jobs.sql.go:1526-1541`) reports the job `done`.
2. **`FAILED`** - confirmed, and **understated by the item**. On a task with `retries = 0`
   (the column default) the cascade fires as described: `FailDependentTasks` walks the
   recursive CTE and marks the whole transitive downstream `failed`. But on a task that
   *did* opt into retries, the forged FAILED instead takes the retry branch, which is
   strictly worse in a different way: `IncrementTaskRetryCount` flips a live task back to
   `pending`, NULLs its `worker_id` and **bumps `assignment_epoch`** - which ends the
   generation of the agent that is genuinely running it, silently killing its log ingest and
   its own status updates. Repeat until retries are exhausted, then the cascade fires anyway.
   So the item's "one-message DoS" is really "one message per retry, then the DoS", and the
   intermediate messages also disrupt a legitimate worker.
3. **`RUNNING`** - confirmed in full, including the item's own correction. Verified:
   - `tasks_status_check` (`migrations/000019:23`) permits `running`; there is no constraint
     tying `running` to a non-NULL `worker_id`. The row is writable.
   - Every query that could move a `running` task is keyed on `worker_id`
     (`GetActiveTasksForWorker` `:141`, `ListGraceCandidates` `:151`,
     `CountActiveTasksByAllWorkers` `:181`, `RequeueWorkerTasks` `:232`,
     `RequeueWorkerTasksIfEpoch` `:245`) except `RequeueTaskByID` (`:164`, keyed by id but
     only reachable from `reconcileRunningTasks`, whose candidate set is
     `GetActiveTasksForWorker`) and `CancelJobTasks` (`:219`, keyed by job id).
   - There is no age-based task reaper. The only `Sweeper` in the tree
     (`internal/metrics/sweep.go`) marks *workers* stale; it never touches tasks.
   - **The item's correction holds.** `CancelJobTasks` matches
     `status IN ('pending','queued','running','dispatched')` and is reachable from
     `handleCancelJob` (`internal/api/jobs.go:740`). Cancel clears the wedge by failing the
     task and every non-terminal sibling. One reinforcing detail the item does not state:
     the cancel handler's 409 gate at `jobs.go:717` rejects only `cancelled`/`done` jobs, and
     the wedged task keeps `RecomputeJobStatus` returning `running`, so the cancel path stays
     open. Do not re-introduce the "only deletion recovers it" framing.
   - Also note `handleCancelJob` collects agent cancel signals only for tasks with
     `t.WorkerID.Valid` (`jobs.go:732`), so the wedged task gets no signal - harmless, since
     no agent was ever running it.

### 2.5 The identity is already in scope

`Connect` (`handler.go:115-119`) computes `workerUUID` from the authenticated registration
and already hard-fails the connection if the `Scan` errors - PR #119 made that change, so
this spec inherits it and needs no new work there. `workerUUID` is already passed to
`handleTaskLog` (`:135`) and `applyInventoryUpdate` (`:137`). `handleTaskStatus` (`:133`) is
the only message handler on the loop that still does not receive it.

`export_test.go:18` already exposes a `HandleTaskStatus` shim, used by three tests
(`handler_test.go:254, 266, 318`).

## 3. The decision the item asked for: one query, and a Go gate ahead of the retry branch

### 3.1 Question 1 - one fenced query for both callers

**Decision: one query, `UpdateTaskStatus`, gaining a `worker_id = sqlc.arg(worker_id)`
predicate, used by both callers. No second query, and emphatically no sentinel.**

The three options and the trade:

- **A sentinel meaning "server-internal, skip the check" is the dangerous option.** A
  zero-value `pgtype.UUID` binds SQL NULL. Under `IS NOT DISTINCT FROM` it matches a NULL
  `worker_id` and fails **open** - and it is reachable not only by the dispatcher but by any
  future caller that simply failed to resolve its identity, which is precisely the hole this
  spec closes. Under `=` it fails **closed** and silently breaks whichever caller uses it.
  Either way the sentinel's meaning is invisible at the call site. Rejected.
- **A separate un-fenced query for the dispatcher** is the item's "conservative" option, and
  it is the one that looks safe and is not. It leaves a second, un-fenced writer to
  `tasks.status` in the codebase - the exact shape that made this bug possible. Two queries
  means a future caller picks one, and the wrong pick is the unsafe one, silently. Rejected.
- **One query** makes the fence structural: there is no way to write a task's status without
  naming a worker, and the invariant "every write to `tasks.status` fences on
  `assignment_epoch`" gets a companion clause with no exceptions to remember.

The trade to state explicitly: `failClaimedTask` gains a predicate that can never reject in
practice. Its `claimed.WorkerID` is non-NULL by construction (2.2), and the only way
`worker_id` can change between the claim and the call is a requeue or cancel, all of which
bump the epoch - so the epoch predicate already rejects those. The worker predicate is
therefore tautological there. That is acceptable, and better than acceptable for two reasons:

1. It **fails closed and loudly**. `failClaimedTask` already logs on any error from
   `UpdateTaskStatus` including `pgx.ErrNoRows` (`dispatch.go:363-366`), so if someone later
   adds a dispatcher path that fails a task it did not claim, they get a log line rather than
   a silent no-op.
2. It documents the contract at the call site: the dispatcher must name the worker it
   claimed for.

Write this reasoning into the SQL comment, as the item asks.

### 3.2 Question 2 - remove `worker_id` from the SET list

**Decision: yes. Verified against both callers.**

- `handleTaskStatus` passes `task.WorkerID`, read by `GetTask` at `:425` and written back
  unmodified at `:484`.
- `failClaimedTask` passes `claimed.WorkerID`, from the `ClaimTaskForWorker` that just
  returned it.

Neither ever writes a *different* value, so the SET is provably a no-op today. Removing it
converts PR #119's documented contract ("callers MUST pass the task's existing worker_id
through, because clearing it here would strand a live agent forever") into a structural
guarantee: the statement can no longer clear `worker_id` at all, so the failure mode the
comment warns about becomes unrepresentable.

There is no TOCTOU gap in that claim: the value is re-read between `GetTask` and
`UpdateTaskStatus` only by statements that also bump the epoch, and the epoch predicate
rejects those.

The generated `UpdateTaskStatusParams` keeps all six field names (`ID`, `Status`,
`WorkerID`, `StartedAt`, `FinishedAt`, `AssignmentEpoch`), so every call site still compiles
while `WorkerID` silently changes meaning from "value to write" to "identity to match". That
is a review hazard, and it is mitigated by the SQL comment, by the store-layer tests in 8.3,
and by the fact that both production callers pass the same value under either meaning.

### 3.3 The finding: the SQL fence alone does not close this bug

Per 2.3, the retry branch runs *before* `UpdateTaskStatus` and returns. So a forged
`TASK_STATUS_FAILED` at the current epoch on a task with `retries > 0` never reaches the
fenced statement at all. It reaches `IncrementTaskRetryCount`, which has a bare
`WHERE id = $1` - no epoch fence, no worker fence.

**Therefore the identity check must be in Go, adjacent to the existing epoch gate, ahead of
every side effect.** The SQL predicate stays as the structural backstop for
`failClaimedTask` and for any future caller, but the Go gate is what actually closes the
reported bug.

This also removes a flood vector the SQL-only design would have introduced.
`handleTaskStatus` logs unconditionally when `UpdateTaskStatus` errors (`:489-492`), and
`pgx.ErrNoRows` is an error. An SQL-only fix would have turned every forged status message
into one `log.Printf` on the recv goroutine, keyed on attacker-supplied input - the same
shape as `bug-2026-08-12-tasklog-err-limiter-attacker-keyed`. With the Go gate returning
silently first (matching the epoch gate's existing behavior at `:433-435`), the SQL
rejection is reachable only in a genuine TOCTOU race, where a log line is wanted.

### 3.4 Question 5 - `IncrementTaskRetryCount` stays a separate item

`bug-2026-06-26-retry-resurrects-cancelled-task` (bug/high) stays open and is **not**
absorbed here. Justification:

- Different threat, different predicate. That item is about a *legitimate* assignee's retry
  racing an operator cancel, and its fix is a status-or-epoch guard inside
  `IncrementTaskRetryCount` plus a decision about job-cancelled state. This spec's job is
  "only the assignee may drive this task's status machine at all".
- Its acceptance test is a cancel-during-retry interleaving, which this change does not
  produce and cannot pass.
- Its own notes tie it to the `POST /v1/jobs/{id}/retry` endpoint, which will be a second
  entry point into that query; fixing it there gets both entry points at once.

What this change *does* do for it is narrow it. `IncrementTaskRetryCount` has exactly one
production caller (`handler.go:462`, confirmed by repo-wide grep). After this change, that
caller is gated on the sender being the assignee, so the forged-from-a-stranger route into
that query is closed. Record that in this spec's Resolution note when the work lands, so
whoever picks up the 06-26 item knows it. Do not close it.

> **Correction (2026-08-12, Phase 4 invariants lens).** This section originally said the
> remaining exposure is "exactly the cancel race described in that item and nothing else."
> That is wrong, and the error matters because it undersizes the 06-26 item for whoever
> picks it up. A second route remains, and unlike the cancel race it needs one actor and no
> race at all: a terminal transition does not bump `assignment_epoch`, and after this change
> `worker_id` structurally survives it, so the task's own assignee can send `DONE` at epoch
> N - dependents become eligible and dispatch - and then `FAILED` at epoch N. Both gates
> still pass, `terminal && RetryCount < Retries` holds, and `IncrementTaskRetryCount` moves
> the *completed* task back to `pending` and re-dispatches it while its dependents are
> already running. The structural fix is a status predicate
> (`AND status NOT IN ('done','failed','timed_out')` on both `UpdateTaskStatus` and
> `IncrementTaskRetryCount`), **not** an epoch bump on terminal - bumping there would break
> the trailing-log flush that section 4 relies on. Both routes are now recorded in
> `docs/backlog/bug-2026-06-26-retry-resurrects-cancelled-task.md`.

### 3.5 Question 7 - no int32 truncation, and none introduced

Confirmed. `handleTaskStatus` compares at int64 width (`int64(task.AssignmentEpoch) !=
upd.Epoch`, `:433`), widening the stored value rather than narrowing the wire value, so it
has no equivalent of `bug-2026-08-12-tasklog-epoch-int32-truncation`. Its later
`AssignmentEpoch: int32(upd.Epoch)` (`:487`) is safe precisely because that equality already
proved the value fits in int32.

This change introduces no new narrowing: the new parameter is a `pgtype.UUID`, and the
epoch handling is untouched. The Go gate must be placed so this property survives - see the
ordering note in 5.1.

## 4. Design

### 4.1 The Go gate

`handleTaskStatus` gains the connection's worker as its second parameter, matching the
`handleTaskLog` and `applyInventoryUpdate` convention:

```go
func (h *Handler) handleTaskStatus(ctx context.Context, workerID pgtype.UUID, upd *relayv1.TaskStatusUpdate)
```

Immediately after the `GetTask` at `:425`, and **before** the existing epoch gate, the
assignment gate:

```go
// Assignment gate, part one: identity. The task's status machine may only be
// driven by the agent the task is assigned to. workerID is resolved at
// registration and never read off the wire, so a sender cannot claim to be
// somebody else. Both Valid checks are load-bearing: pgtype.UUID is a
// comparable struct, so a zero-value workerID (a caller that lost its identity)
// would compare EQUAL to a never-claimed task's NULL worker_id and fail open.
// This is the Go form of the "= , never IS NOT DISTINCT FROM" rule on
// AppendTaskLog; see internal/store/query/tasks.sql.
if !workerID.Valid || !task.WorkerID.Valid || task.WorkerID.Bytes != workerID.Bytes {
    return
}

// Assignment gate, part two: currency. (existing epoch check, comment extended)
```

Silent return, matching the epoch gate. Rationale for staying silent is the sibling spec's
section 7 verbatim, reinforced by 3.3 above: a log line here is attacker-controlled volume
on the recv goroutine with no sink to send it to. Detection belongs with
`feature-2026-06-26-audit-log-admin-console-actions`.

The two checks stay as two separate `if`s with two comments, not one boolean. A future
reader must be able to see that they answer different questions, and must not be able to
delete one as redundant.

### 4.2 The SQL

```sql
-- name: UpdateTaskStatus :one
-- Updates a task's status only if BOTH fence predicates hold: the task is
-- currently assigned to the caller's worker (identity), and the caller's epoch
-- matches the current assignment (currency). The epoch answers "is this
-- generation current"; the worker id answers "are you who you say you are".
-- Neither substitutes for the other - do not delete either.
-- This statement no longer writes worker_id; the argument is a fence, not a
-- value. It does not bump assignment_epoch either, so a terminal task keeps its
-- assignee and trailing log chunks from the agent that just finished still pass
-- AppendTaskLog's fence.
-- The worker_id comparison must stay a plain `=`. tasks.worker_id is NULLABLE,
-- so `=` makes a never-claimed task reject every update, which is the hole this
-- predicate closes, and makes a caller that lost its identity (a zero-value
-- pgtype.UUID binds SQL NULL) fail closed. `IS NOT DISTINCT FROM` would let a
-- NULL parameter match a NULL worker_id and re-open it. Do not "fix the NULL
-- bug" here.
-- Both callers are fenced by the same statement deliberately. Dispatcher.
-- failClaimedTask passes claimed.WorkerID from ClaimTaskForWorker, which is
-- non-NULL by construction, so the predicate is tautological there - that is the
-- point. A separate un-fenced query for the server-internal path would leave a
-- second, unfenced writer to tasks.status that a future caller could pick by
-- mistake, and a sentinel meaning "skip the check" would be reachable by any
-- caller that merely failed to resolve its identity. See the spec.
UPDATE tasks
SET status = sqlc.arg(status),
    started_at = sqlc.arg(started_at),
    finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id)
  AND assignment_epoch = sqlc.arg(assignment_epoch)
  AND worker_id = sqlc.arg(worker_id)
RETURNING *;
```

`make generate` regenerates `internal/store/tasks.sql.go`. Follow CLAUDE.md's CRLF
procedure: sqlc emits LF, so after generating run `git diff --ignore-all-space`, keep only
the content change and `git checkout --` the line-ending-only hunks.

### 4.3 Threading and call sites

- `Connect` (`handler.go:133`) passes the `workerUUID` it already holds.
- `export_test.go:18` `HandleTaskStatus` shim gains the parameter.
- `handleTaskStatus`'s existing three test call sites (`handler_test.go:254, 266, 318`) pass
  the seeded assignee.
- No proto change. Adding a worker id to `TaskStatusUpdate` would be worse than useless.
- No change to `failClaimedTask`'s Go code beyond the params struct still compiling; it
  already passes `claimed.WorkerID`.

## 5. Failure modes and constraint checks

### 5.1 Ordering

The identity gate goes **before** the epoch gate so that the int64-width comparison at
`:433` remains the first thing that touches `upd.Epoch`. Nothing between the two may read or
narrow the wire epoch. This is a small point but it is how 3.5 stays true.

### 5.2 What a legitimate caller loses

Nothing. Enumerated:

- Every `TaskStatusUpdate` originates in `internal/agent/runner.go` for a task the server
  dispatched to that agent via `ClaimTaskForWorker`, which sets `worker_id` and bumps the
  epoch atomically. There is no window where a task is at the dispatch epoch with a NULL
  assignee.
- Reconnect inside the grace window keeps working: the fence is on the *worker*, not the
  connection. A worker row is stable across reconnects and `finishRegister` does not touch
  `tasks.worker_id`. Deliberately not fencing on `connection_epoch` is what preserves this.
- Terminal transitions keep the assignee (4.2), so `AppendTaskLog`'s fence still passes for
  trailing chunks. `TestUpdateTaskStatus_TerminalTransitionKeepsTheAssignee...` pins this
  and must stay byte-identical.
- `failClaimedTask` is unaffected (3.1).

### 5.3 Invariants

- **Epoch fence** - extended, not replaced. This change retires the "remaining unfenced
  writer" clause in CLAUDE.md's bullet. Amend that bullet to say `UpdateTaskStatus` now
  fences on `tasks.worker_id` as well, and that `handleTaskStatus` checks identity in Go
  *before* the retry branch because `IncrementTaskRetryCount` is not reachable through the
  fenced statement.
- **One bounded sender per gRPC stream** - not in contact. No stream sends are added or
  moved; `handleTaskStatus` writes to Postgres and publishes to the broker.
- **Identity-checked teardown** - reinforced in spirit: state that belongs to a worker may
  only be written by that worker.
- **No interior pointers across locks** - not in contact.
- **Single JSON entry point** - not in contact.

### 5.4 Load and failure behavior

No new round trip. The Go gate compares two structs already in memory (`handleTaskStatus`
already does its own `GetTask`, unlike `handleTaskLog`), and the SQL predicate is a third
comparison on a row already fetched by primary key. No new index, no new scan. Under load
the change is unmeasurable, which is the point: this runs once per status transition per
task.

Failure mode on rejection: silent drop, no state change, no publish, no notify. Same posture
as a stale epoch today, with the hole closed.

## 6. Scope

**In scope**

- `internal/store/query/tasks.sql` - `UpdateTaskStatus`: drop `worker_id` from SET, add it
  to WHERE, rewrite the comment.
- `internal/store/tasks.sql.go` as regenerated by `make generate`.
- `internal/worker/handler.go` - `handleTaskStatus` signature, the identity gate, the
  `Connect` call site, comment updates.
- `internal/worker/export_test.go` - the `HandleTaskStatus` shim.
- `internal/worker/handler_test.go` - three mechanical call-site updates, plus the new
  handler-layer tests (8.2) and the Connect-wiring test (8.4).
- `internal/store/store_test.go` - two predicted fixture repairs (8.5) and four new
  assignee cases (8.3).
- `CLAUDE.md` - the Epoch fence bullet (5.3).
- `git mv docs/backlog/bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero.md` to
  `docs/backlog/closed/` via `/backlog close`. Required scope, not optional cleanup.

**Out of scope**

- `bug-2026-06-26-retry-resurrects-cancelled-task`. Stays open; see 3.4.
- `UpdateTaskStatusEpoch`. Test-only, already epoch-fenced, no production caller (2.2).
- Any proto change.
- `bug-2026-08-12-auto-enroll-hostname-takeover`. It widens who can hold an identity; this
  change constrains what an identity may write. Separate, and the fence is bounded by
  auto-enroll's trust model, not a substitute for it.
- `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` and
  `bug-2026-08-12-tasklog-epoch-int32-truncation`. Both are on the log path.
- Per-owner authorization on `GET /v1/tasks/{id}`, which is why task ids are discoverable.
  Read-path policy across the whole API surface.
- Rate limiting the gRPC message loop.
- An age-based reaper for `running` tasks (2.4). Once this fix lands, the only way to reach
  a `running` task with a NULL assignee is a bug elsewhere; a reaper would be defense in
  depth against a state we are making unreachable, and it needs its own design (what timeout,
  whose clock, what happens to a slow-but-alive agent). Propose as a backlog item, do not
  build it here.
- Audit-log detection of "an agent tried to write to a task that is not its own", for this
  path and the log path together. Belongs with
  `feature-2026-06-26-audit-log-admin-console-actions`.

## 7. Corrections to the backlog item

1. **Consequence 2 is understated.** The item traces FAILED only for `retries = 0`. On a
   task with retries the forged message instead burns a retry, bumps the epoch and evicts the
   legitimately running agent - repeatably. See 2.4.
2. **The item's Proposal is incomplete.** "`handleTaskStatus` gains the `pgtype.UUID` ... so
   the identity comes from registration" plus a SQL predicate does not close the bug, because
   the retry branch returns before the fenced statement. The check must be in Go, ahead of
   every side effect. See 2.3 and 3.3.
3. **The item's RUNNING correction is confirmed** and slightly strengthened (2.4): the wedge
   keeps `RecomputeJobStatus` returning `running`, which is what keeps the cancel handler's
   409 gate open. Do not re-introduce the "only deletion recovers it" version.
4. **The item's "verify before designing" note about `claimed.WorkerID` is confirmed** (2.2).

## 8. Test strategy

All of this is integration-only. `handleTaskStatus` takes a concrete `*store.Queries` with
no injectable seam, and `internal/worker/handler_test.go` and `internal/store/store_test.go`
are both `//go:build integration`. `make test` exercises none of it and is a no-regression
gate only. Verification must run `make test-integration`, or at minimum
`go test -tags integration -p 1 ./internal/store/... ./internal/worker/... ./internal/scheduler/... -timeout 600s`.

### 8.1 Sequencing, so that RED is behavioral

Four stages. **Do not collapse them.**

1. **Thread the identity only.** Parameter on `handleTaskStatus`, the shim, the `Connect`
   call site, and the three existing test call sites (passing the real assignee). No gate,
   no SQL. Full suite green; behavior provably unchanged.
2. **Add the handler tests (8.2) and the Connect test (8.4).** They compile and are
   behaviorally RED: the forged DONE lands, the forged FAILED cascades, the forged RUNNING
   wedges, the forged FAILED burns a retry. **Capture this output verbatim** - it is the
   acceptance evidence and it cannot be reproduced after stage 3.
3. **Add the Go gate.** The handler tests go green.
4. **Add the store cases (8.3), confirm RED, then change the SQL and regenerate.** Repair
   the two predicted fixtures (8.5) in the same step.

A note this path gets for free that the log path did not: the store-layer RED is
**behavioral here too**, not a compile error, because `UpdateTaskStatusParams.WorkerID`
already exists today. Case 2 of 8.3 currently succeeds *and writes the attacker's worker id
into the row*. There is no excuse for a compile-error RED anywhere in this change.

### 8.2 Handler layer, in `internal/worker/handler_test.go`

Each test seeds a job, two workers W1 and W2, and a task claimed by W1 (epoch 1), and sends
the task's **current** epoch so the epoch predicate matches and only identity can reject.

- **`TestHandleTaskStatus_RejectsDoneFromANonAssignee`.** W2 sends DONE at epoch 1. Assert
  the task is still `dispatched` and `finished_at` is still invalid. Positive control on the
  same code path: W1 sends DONE and it lands. *Only the fix produces this*: today the epoch
  matches, so `UpdateTaskStatus` writes `done`. The positive control rules out the vacuous
  failure where the handler stopped accepting anything.

- **`TestHandleTaskStatus_RejectsFailedFromANonAssigneeAndDoesNotCascade`.** Task B depends
  on task A; A claimed by W1, B pending. W2 sends FAILED for A at A's current epoch. Assert
  A is still `dispatched` **and B is still `pending`**. The B assertion is the one that
  matters: it asserts the absence of the `FailDependentTasks` cascade, which is the actual
  harm, not just that a status string did not change.

- **`TestHandleTaskStatus_NonAssigneeCannotBurnARetry`.** Task with `Retries: 1` claimed by
  W1 at epoch 1. W2 sends FAILED at epoch 1. Assert `retry_count` is still 0, status is
  still `dispatched`, and `assignment_epoch` is still 1. **This is the test the SQL fence
  alone does not pass**, and it is the reason 3.3 exists - it must be present, and a reviewer
  should be able to confirm it goes red if the Go gate is removed while the SQL predicate
  stays. Positive control: W1 sends FAILED and gets `retry_count` 1, status `pending`, epoch
  2.

- **`TestHandleTaskStatus_RejectsRunningForANeverClaimedTask`.** The item's literal repro and
  the worst consequence. Task created and **not** claimed: epoch 0, `worker_id` NULL. W1
  sends RUNNING at epoch 0. Assert status is still `pending`, `worker_id` still NULL, and -
  the assertion that proves the wedge is gone rather than that a string is unchanged - the
  task **is still returned by `GetEligibleTasks`**. Positive control: claim it to W1, then
  W1's RUNNING at the new epoch lands.

  This test also pins the Go NULL trap from 4.1. It is the case where both sides could be
  zero-valued, which is the only case that discriminates a NULL-tolerant comparison from a
  NULL-rejecting one - the lesson from the sibling iteration's Problem 5. To make it
  discriminating for the *Go* gate, the implementer must confirm it goes red when the two
  `.Valid` checks are dropped (leaving a bare `task.WorkerID != workerID`) and a zero-value
  worker id is passed. Add that as an explicit mutation check, not an assumption.

### 8.3 Store layer, in `internal/store/store_test.go`

New `TestUpdateTaskStatus_AssigneeGuarded`, mirroring `TestAppendTaskLog_EpochGuarded`'s
four-case shape. Against a task claimed by W1 at epoch 1:

1. correct epoch + W1 -> succeeds, and `worker_id` is unchanged (this also pins 3.2: the
   statement no longer writes the column, so it must still read back as W1).
2. correct epoch + W2 -> `pgx.ErrNoRows`, **and a follow-up `GetTask` proves the row did not
   move** - status unchanged and `worker_id` still W1. The mutation proof matters here
   because today this case does not merely succeed, it overwrites `worker_id` with W2.
3. never-claimed task at epoch 0 + a valid worker -> `pgx.ErrNoRows`, row unchanged.
4. never-claimed task at epoch 0 + **zero-value `pgtype.UUID`** -> `pgx.ErrNoRows`. This is
   the regression test for the `IS NOT DISTINCT FROM` rewrite. Per the sibling iteration's
   Problem 5, a claimed task would *not* discriminate (both operators reject when one side is
   a real UUID); only NULL-versus-NULL does. **Mutation-prove it**: switch the SQL to
   `IS NOT DISTINCT FROM`, confirm exactly case 4 goes red and 1 to 3 stay green, then
   restore and re-run.

### 8.4 Connect wiring

**`TestConnect_TaskStatusIsFencedOnTheConnectionsOwnWorker`**, modelled directly on
`TestConnect_TaskLogChunkIsFencedOnTheConnectionsOwnWorker`
(`handler_tasklog_integration_test.go:536`). A test that drives only the exported shim leaves
`Connect`'s wiring unpinned - the sibling iteration added exactly this test for exactly that
reason, and it must be repeated here.

Shape: `seedClaimedTask` for hostname H (auto-enroll upserts by hostname and returns the
**existing** row's id, so the connection resolves to the task's assignee), drive a real
`Connect` with `AllowAutoEnroll`, include the task in `RunningTasks` at its current epoch so
`reconcileRunningTasks` does not requeue it and bump the epoch for an unrelated reason, then
send a `TaskStatusUpdate{DONE}` at that epoch and assert the task reaches `done`. If
`Connect` passed a zero-value or wrong worker, the update is rejected and the test fails.
Assert the register response's `worker_id` equals the seeded assignee, as the log-path test
does, or the test proves nothing.

### 8.5 Existing tests: what may change, and what is a finding

**Predicted, and permitted** (fixture repairs, not assertion changes - the same shape as the
sibling iteration's Problem 3):

- `store_test.go:118` (`TestTaskDependencyAndEligibility`) marks a **never-claimed** task
  `done` with a zero-value `WorkerID` at epoch 0. After the fence this returns
  `pgx.ErrNoRows` and the `require.NoError` fails. Repair the fixture: claim the task to a
  worker and pass that assignee at the claimed epoch. Do not weaken the fence.
- `store_test.go:220` and `:228` (`TestUpdateTaskStatus_EpochGuarded`) pass a zero-value
  `WorkerID` on a task claimed by `w`. Both calls gain `WorkerID: claimed.WorkerID`. This is
  required by the backlog item's acceptance criterion that the stale-epoch case keep the
  epoch as its **only** failing predicate - without the repair, case `:228` would pass for
  two reasons and would stay green even if the epoch predicate were deleted.

**Must stay byte-identical, and any needed edit is a finding, not a chore:**

- `store_test.go:400` `TestUpdateTaskStatus_TerminalTransitionKeepsTheAssignee...`, including
  its `assert.Equal(t, w.ID, done.WorkerID)` - which must still hold with `worker_id` removed
  from the SET list, because the column is simply left alone.
- `internal/scheduler/dispatch_test.go`'s `failClaimedTask` tests (the poison-commands test
  at `:520` and `TestDispatcher_FailClaimedTask_PublishesJobEventOnTerminal` at `:532`). They
  drive `RunOnce`, so the claim is real and the worker id is genuine. **These passing
  unchanged is the evidence for the section 3.1 decision.** If either needs an edit, the
  single-query choice is wrong and must be re-opened, not patched.
- `handler_test.go`'s `TestHandleTaskStatus_EpochGate` and
  `TestHandleTaskStatus_PrepareFailedIsTerminal` - argument addition only.
- Everything in `internal/api` (the cancel-job tests), `internal/worker`'s teardown and
  reconcile tests, and the log-path tests.

## 9. Assumptions / decisions made autonomously

Autonomous run, no human available. Each call and its reasoning:

1. **One fenced query for both callers, no sentinel, no second query** (3.1). The sentinel
   fails open under a NULL-tolerant comparison and is reachable by any caller that lost its
   identity; a second query leaves an unfenced writer that a future caller can pick by
   mistake. The tautological predicate on the dispatcher path is the cheap side of the trade
   and it fails closed and loudly there.
2. **Remove `worker_id` from the SET list** (3.2), making PR #119's documented contract
   structural. Verified against both callers: neither ever writes a different value.
3. **The identity check goes in Go, ahead of the retry branch, with the SQL predicate as a
   backstop** (3.3). This is the deviation from the item's Proposal, and it is not optional:
   the retry branch returns before the fenced statement, so an SQL-only fix leaves the
   `FAILED`-with-retries route wide open. It also avoids introducing an attacker-keyed log
   flood on the recv goroutine.
4. **Both `.Valid` checks in the Go comparison are mandatory** (4.1). `pgtype.UUID` is a
   comparable struct, so a bare `!=` is the Go form of `IS NOT DISTINCT FROM` and fails open
   when both sides are zero-valued. Pinned by a mutation check in 8.2.
5. **Silent drop, no forged-versus-stale signal** (4.1). Sibling spec section 7's reasoning
   transfers unchanged; detection routed to the audit-log item.
6. **`bug-2026-06-26-retry-resurrects-cancelled-task` stays open** (3.4), with a note that
   this change narrows its remaining exposure to the cancel race alone.
7. **No `running`-task reaper** (6). Defense in depth against a state this change makes
   unreachable, and it needs its own design. Proposed for the backlog, not built.
8. **Two existing store tests get fixture repairs** (8.5). Predicted here so they are not
   mistaken for the gate firing on an unpredicted behavior change; the gate still applies to
   everything else.
9. **Priority left at the item's `high`.** Consequence 3 is an availability bug with no
   automatic recovery, which argues for higher, but it is bounded by needing an agent token
   plus a task id. Recorded rather than re-ranked unilaterally.

## 10. Acceptance criteria

1. An agent that is not a task's assignee cannot change that task's status, proven by
   `TestHandleTaskStatus_RejectsDoneFromANonAssignee` sending the task's **current** epoch,
   with the stage-2 RED output captured showing the status written before the fix.
2. A never-claimed task at epoch 0 rejects DONE, FAILED and RUNNING from every worker, each
   with its own named test, and the RUNNING case additionally asserts the task remains
   dispatchable via `GetEligibleTasks`.
3. A forged FAILED cannot burn a retry, bump the epoch, or evict a live agent, proven by
   `TestHandleTaskStatus_NonAssigneeCannotBurnARetry`, which is red against an SQL-only fix.
4. A caller that loses its identity fails closed, pinned twice: at the store layer by case 4
   of `TestUpdateTaskStatus_AssigneeGuarded` with a mutation proof against
   `IS NOT DISTINCT FROM`, and at the Go layer by a mutation proof against dropping the
   `.Valid` checks.
5. `Connect` passes its own authenticated worker, pinned by
   `TestConnect_TaskStatusIsFencedOnTheConnectionsOwnWorker` driving the real message loop.
6. The existing stale-epoch rejection still works, with its fixture arranged so the epoch is
   the only failing predicate (`store_test.go:228`).
7. `Dispatcher.failClaimedTask` still terminally fails a poison-payload task, proven by the
   existing `internal/scheduler` tests passing **with no edit**, and the single-query choice
   is documented in the SQL comment.
8. Every other existing test in `internal/worker`, `internal/scheduler`, `internal/store` and
   `internal/api` passes with only a mechanical argument addition or the two fixture repairs
   predicted in 8.5. Any other test needing an assertion changed is reported as a finding.
9. `handleTaskStatus` performs no additional DB round trip; no goroutine or queue is added to
   the recv path.
10. No `Sender`, `Registry` or `sendCh` code is touched; `.proto` unchanged.
11. `make test` and `make test-integration` are both green; `go vet -tags integration ./...`
    is clean (the real compile gate after a signature change).
12. CLAUDE.md's Epoch fence bullet is amended and no longer names `UpdateTaskStatus` as an
    unfenced writer.
13. The backlog item is closed with `/backlog close`, landing the file in
    `docs/backlog/closed/`.
