# Fence task-log appends on the sending worker's identity, not the epoch alone

- Date: 2026-08-12
- Backlog item: `docs/backlog/bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero.md`
- Status: design approved (autonomous mode - see "Assumptions / decisions made autonomously")
- Owner phase: Phase 1 (SPEC) of the agent-team pipeline

## 1. Problem

`AppendTaskLog` decides whether to persist a log chunk using only the task id and
the `assignment_epoch` the agent put on the wire. It never asks whether the agent
sending the chunk is the agent the task is assigned to. The epoch fence was built
to reject a *stale generation*, and it does that correctly. It was never an
authorization check, and nothing else on this path performs one.

The result is that any principal that can open a `Connect` stream (any valid
long-lived agent token, or no credential at all when `RELAY_ALLOW_AUTO_ENROLL` is
on) can write arbitrary content into any task's log, provided it can guess that
task's current `assignment_epoch`. Since the SSE task-log work landed, that
content is also fanned out live to every operator tailing the task.

This is a missing second check, not a defect in the fence. The fix must be stated
that way so the epoch fence's purpose does not get muddled.

## 2. What the code actually does (verified, with citations)

Every claim below was read in the tree at `c081660`, not taken from the backlog
item.

### 2.1 The fence today

`internal/store/query/tasks.sql:62-70`:

```sql
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id) AND t.assignment_epoch = sqlc.arg(assignment_epoch)
), ins AS (
    INSERT INTO task_logs (task_id, stream, content)
    SELECT sqlc.arg(task_id), sqlc.arg(stream), sqlc.arg(content) FROM fence
    RETURNING id, created_at
)
SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;
```

Two predicates. No worker predicate. The `t` alias and the qualified column
references are load-bearing for sqlc's analyzer (documented in the query's own
comment); any edit must preserve that shape.

### 2.2 The column that records a claimed task's worker

The backlog item told us not to trust its own sketch here. Checked:
`ClaimTaskForWorker` (`internal/store/query/tasks.sql:95-100`) is
`UPDATE tasks SET status = 'dispatched', worker_id = $2, assignment_epoch = assignment_epoch + 1 WHERE id = $1 AND status = 'pending'`.
So the column really is `tasks.worker_id`, and the sketch was right. The schema
(`internal/store/migrations/000001_initial.up.sql:62`) declares it
`worker_id UUID REFERENCES workers(id) ON DELETE SET NULL` - **nullable**. That
nullability is the single most important property in this design; see 6.2.

### 2.3 The epoch default

`internal/store/migrations/000004_assignment_epoch.up.sql:1` is a one-liner:
`ALTER TABLE tasks ADD COLUMN assignment_epoch INT NOT NULL DEFAULT 0;`.
Confirmed: a task that has never been claimed sits at epoch `0`, and
`ClaimTaskForWorker` makes the first dispatch epoch `1`.

### 2.4 The connection's own worker identity in `handleTaskLog`

`internal/worker/handler.go:591` is `func (h *Handler) handleTaskLog(ctx context.Context, chunk *relayv1.TaskLogChunk)`.
It receives the chunk and nothing else. But the identity is already resolved and
in scope one frame up: `Connect` (`handler.go:100-133`) does

```go
workerID, sender, err := h.authenticateAndRegister(ctx, stream, reg)
...
var workerUUID pgtype.UUID
_ = workerUUID.Scan(workerID)
```

and then dispatches the message loop. `workerID` comes from
`finishRegister` -> `uuidStr(updated.ID)`, where `updated` is the row returned by
`RegisterWorkerConnection` for the authenticated worker. `workerUUID` is already
passed to `applyInventoryUpdate` at `handler.go:126`, so "pass the connection's
worker UUID into a message handler" is an established local convention, not a new
one. No new lookup, no new query, no proto change is needed to get the identity.

Note the `_ =` on the `Scan`: today an unparseable worker id silently produces an
invalid (SQL NULL) `pgtype.UUID` and only breaks inventory updates. After this
change it would silently disable *all* log ingest for that connection. See 6.4.

### 2.5 Every caller of `AppendTaskLog`

Repo-wide grep. There are exactly two, plus documentation references:

| Caller | File | Note |
| --- | --- | --- |
| `handleTaskLog` | `internal/worker/handler.go:602` | the only production caller |
| `TestAppendTaskLog_EpochGuarded` | `internal/store/store_test.go:241-299` | `//go:build integration`; pins the fence's semantics at the store layer |

Nothing in `internal/api`, `internal/scheduler`, `internal/schedrunner`,
`internal/mcp`, or `internal/cli` calls it. There is no second insert path into
`task_logs` in production code: the only other `INSERT INTO task_logs` statements
in the repo are raw-SQL test fixtures
(`internal/api/tasks_integration_test.go:55`,
`internal/store/status_vocabulary_constraints_test.go:64,98`), which bypass the
query entirely and are therefore unaffected by a fence change.

Indirect call sites that will need the new argument threaded through them:
`internal/worker/export_test.go:24` (`HandleTaskLog` shim) and its 11 invocations
across `internal/worker/handler_tasklog_integration_test.go` and
`internal/worker/handler_tasklog_e2e_integration_test.go`, plus the shared
`seedClaimedTask` helper (`handler_tasklog_integration_test.go:35`), which
currently returns `(jobID, taskID, epoch)` and discards the worker id it creates.

## 3. Corrections to the backlog item's Proposal

The Proposal is a sketch. Against the tree:

1. **Correct.** The column is `tasks.worker_id`, and
   `WHERE t.id = $1 AND t.assignment_epoch = $2 AND t.worker_id = $3` is the right
   shape.
2. **Correct.** It stays one statement, one round trip. See 9.1.
3. **Understated, and this changes the shape of the work.** The item's title and
   Summary frame the hole as "any *never-claimed* task via Epoch 0". The fence has
   no notion of "never claimed" - it compares an integer. Epoch 0 is simply the
   easiest value to guess. A task on its first dispatch is at epoch 1, and epochs
   only ever advance by 1 per requeue/cancel/retry, so the guessable space for any
   task is a handful of small integers, probed one cheap stream message at a time
   with no rate limit on the gRPC message loop. The reachable set is therefore
   **every task in the database**, including tasks that are live on another worker
   right now, which is the case where a forged line does the most damage (it
   interleaves with genuine output in an operator's live tail). Section 5 states
   the window precisely.
4. **Open question in the item, now answered.** "Check whether any legitimate path
   appends logs for a task with no assignee." Audited in section 4: there is no
   such path. No blanket epoch-0 allowance is needed, and none will be granted.
5. **Silent on the failure mode.** The item does not say what should happen on a
   non-match. Decided in section 7.

## 4. Audit: is there any legitimate append for a task with no assignee?

No. Enumerated exhaustively, because a single missed producer would silently stop
storing real output.

**Producers.** All `TaskLogChunk` messages originate in `internal/agent/runner.go`
and every one of them is stamped with `r.taskID` / `r.epoch`, taken from the
`DispatchTask` the agent received:

- `chunkWriter.Write` (`runner.go:285-303`) - subprocess stdout/stderr.
- `sendStepMarker` (`runner.go:247-259`) - the synthetic `=== relay step n/m ===`
  delimiter. Note this is generated *on the agent*, for its own assigned task. It
  is not a server-side synthetic line.
- `makePrepareProgressFn`'s `doFlush` (`runner.go:381-396`) - `LOG_STREAM_PREPARE`
  progress lines, emitted after dispatch while the workspace syncs.

A `Runner` only exists because the server dispatched that task to that worker, and
dispatch goes through `ClaimTaskForWorker`
(`internal/scheduler/dispatch.go:280-283`), which sets `worker_id` and bumps the
epoch in the same atomic `UPDATE`. There is no window in which a task is at the
dispatch epoch with a NULL `worker_id`.

**Server-side synthetic lines.** None exist. The one server path that could
plausibly want to write an explanatory line for a task that failed before it ever
ran - `failClaimedTask` (`dispatch.go:353+`), used for poison `commands` JSON -
writes nothing to `task_logs`; it emits a `log.Printf` to the server log and marks
the task failed via `UpdateTaskStatus`, preserving `claimed.WorkerID`. So even
that task retains its assignee.

**Every path that clears `worker_id` also ends the generation.** `RequeueTask`,
`RequeueTaskByID`, `RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch`,
`IncrementTaskRetryCount` and `CancelJobTasks` all set `worker_id = NULL` *and*
`assignment_epoch = assignment_epoch + 1` in the same statement. That is the epoch
fence invariant already being honored ("never return a task to `pending` without
bumping the epoch"), and it means the two predicates never disagree: a chunk
rejected by the new assignee predicate on one of these tasks is already rejected
by the epoch predicate. The assignee predicate adds rejections only where the
epoch predicate lets something through.

**Terminal tasks keep their assignee.** `UpdateTaskStatus`
(`tasks.sql:16-19`) is called by `handleTaskStatus` with
`WorkerID: task.WorkerID`, so `done`/`failed`/`timed_out` tasks retain `worker_id`
and their epoch is not bumped. Trailing chunks that arrive just after the terminal
status - a real and common ordering - still match both predicates. No regression.

**Reconnect within the grace window keeps working.** Identity is fenced on the
*worker*, not on the connection. A worker row is stable across reconnects
(`UpsertWorkerByHostname` keys on hostname; `reconnectAndRegister` resolves the
same row from the agent token), and `finishRegister` cancels the grace timer
without touching `tasks.worker_id`. So an agent that drops and reconnects mid-task
resumes streaming into the same task under the same `worker_id`. Deliberately
*not* fencing on `connection_epoch` is what preserves this.

**Existing test fixtures that seed logs on unassigned tasks** stay valid:
`internal/api/tasks_integration_test.go:55` inserts into `task_logs` with raw SQL
for a task nobody claimed. The read path already tolerates that state, and raw
inserts do not go through the fence.

Conclusion: after the change, "no assignee" means "no appends", with zero
legitimate paths affected.

## 5. Threat model and the precise forgery window

**Who can attack.** Any principal holding a long-lived agent token, i.e. any
machine that was ever enrolled; plus, when `AllowAutoEnroll` is set, anyone who
can reach `:9090` and pick an unused hostname
(`authenticateAndRegister`, `handler.go:136-148`). Agent tokens are host-local
files (`internal/agent/credentials.go`, mode 0600) on machines that by design run
untrusted job payloads, so "a job escaped and read the agent token" is the
realistic acquisition path, not an exotic one.

**What they need.** A task UUID and the task's current epoch.

- The epoch is effectively free: `0` for anything never dispatched, `1` for
  anything on its first dispatch, and small integers thereafter. Each guess is one
  stream message; there is no per-stream rate limit on the `Connect` message loop
  (`ratelimit.go` is HTTP-only and per-IP).
- Task UUIDs are `gen_random_uuid()` and not guessable, but they are *not secret*:
  `GET /v1/tasks/{id}` and `GET /v1/tasks/{id}/logs` are `auth(...)`-only with no
  per-owner gate, a property `internal/api/events.go:25-29` states explicitly. Any
  authenticated user can enumerate jobs and read every task id in the system. On a
  build farm the same human commonly has both a relay login and shell on an agent
  host, so both halves are routinely held by one principal.

**The window, stated precisely.** Not "epoch-0 tasks only". A chunk is accepted
whenever `chunk.epoch == tasks.assignment_epoch` for the named task, regardless of
who sends it, in every task state:

| Target task state | Epoch to guess | Accepted today | Consequence |
| --- | --- | --- | --- |
| Created, never dispatched (`pending`, `worker_id` NULL) | `0` | yes | forged output on a task that has not run; visible in the live tail and the polling endpoint |
| Dispatched or running on worker W (`worker_id` = W) | `1` on first dispatch | yes | forged lines interleave with W's genuine output in real time - the highest-impact case, and the one the item's framing misses |
| Terminal (`done`/`failed`, epoch retained) | last epoch | yes | after-the-fact tampering with a completed task's record |
| Requeued/cancelled generation | the *old* epoch | no | correctly rejected by the existing fence; unchanged by this work |

**Impact.** Integrity of the log record, and operator trust in the live tail. Not
confidentiality: nothing here lets a token read anything it could not already read
through the polling endpoint. Not availability, beyond log volume.

**After the fix.** Every row in that table except the last collapses to "rejected",
because the attacker would additionally have to *be* the worker the task is
assigned to. The remaining reachable surface is a compromised agent forging into
tasks that same agent legitimately owns, which is not a boundary this or any
server-side check can restore.

## 6. Design

### 6.1 The fence gains a third predicate

`AppendTaskLog` becomes (preserving the `t` alias, the qualified references and
the `sqlc.arg` naming that keep the analyzer and the generated field names
stable):

```sql
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id)
      AND t.assignment_epoch = sqlc.arg(assignment_epoch)
      AND t.worker_id = sqlc.arg(worker_id)
), ins AS (
    INSERT INTO task_logs (task_id, stream, content)
    SELECT sqlc.arg(task_id), sqlc.arg(stream), sqlc.arg(content) FROM fence
    RETURNING id, created_at
)
SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;
```

`make generate` adds `WorkerID pgtype.UUID` to `AppendTaskLogParams`. The CTE
shape, the `:one` cardinality and the `pgx.ErrNoRows`-means-rejected contract are
all unchanged.

### 6.2 Use `=`, never `IS NOT DISTINCT FROM`

This is the trap in the whole change and it must be called out in the code
comment. `tasks.worker_id` is nullable. With plain `=`, a NULL `worker_id`
compares NULL, the row is excluded, and an unassigned task rejects every append -
which is exactly the desired behavior and is what closes the reported epoch-0
hole.

A reviewer or an implementer "fixing a NULL bug" might reach for
`t.worker_id IS NOT DISTINCT FROM sqlc.arg(worker_id)`. That would make a NULL
parameter match a NULL `worker_id` and **re-open the exact hole this spec closes**,
because a caller that failed to resolve its identity binds NULL. The rule is the
direct analogue of the existing invariant "never call an epoch-fenced query with a
zero-value epoch": never call it with a zero-value worker id either, and keep the
comparison NULL-rejecting so a zero-value argument fails closed instead of
matching.

### 6.3 Threading the identity

`handleTaskLog` gains the worker UUID as its second parameter, matching the
existing `applyInventoryUpdate(ctx, workerUUID, ...)` convention:

```go
func (h *Handler) handleTaskLog(ctx context.Context, workerID pgtype.UUID, chunk *relayv1.TaskLogChunk)
```

`Connect`'s message loop passes the `workerUUID` it already computed. The value
comes from the authenticated registration, never from the wire, so the chunk's
sender cannot influence it. The proto is untouched: adding a `worker_id` field to
`TaskLogChunk` would be worse than useless, since an attacker would simply fill it
in.

`export_test.go`'s `HandleTaskLog` shim gains the same parameter, and
`seedClaimedTask` starts returning the worker id it already creates so the tests
can pass a real assignee and a real non-assignee.

### 6.4 Fail loudly if the connection's identity does not resolve

`Connect`'s `_ = workerUUID.Scan(workerID)` (`handler.go:105-106`) must stop
discarding the error. On failure, return `status.Errorf(codes.Internal, ...)` and
close the stream. Rationale: the string is produced by `uuidStr` from a UUID the
server just read out of Postgres, so a failure here means something is badly
wrong; and after this change an invalid value would fail closed by silently
dropping 100% of that worker's log output, which is a miserable thing to debug.
Failing the connection turns a silent, permanent data-loss mode into a loud,
immediate one.

Considered and rejected: threading `updated.ID` (a real `pgtype.UUID`) out of
`finishRegister` to avoid the string round-trip entirely. It is marginally purer
but widens the signature of `authenticateAndRegister`, `enrollAndRegister`,
`reconnectAndRegister`, `autoEnrollAndRegister` and `finishRegister` for no
behavioral gain, since `uuidStr` -> `Scan` is provably lossless. Not worth the
diff in a security fix that should be easy to review.

### 6.5 Comment updates

The doc comment on `AppendTaskLog` and on `handleTaskLog` currently describe the
rejection as "a stale chunk (from a reassigned or cancelled generation)". Both
must be rewritten to say the fence now establishes *two independent things* - that
the sender is the task's current assignee, and that the sender's generation is
current - and that `pgx.ErrNoRows` means "one or both failed; drop, do not
publish". A future reader who believes `ErrNoRows` means only "stale" is one step
from deleting the assignee predicate as redundant.

## 7. What happens when the fence does not match

**Decision: drop silently, exactly as today, with no attempt to distinguish a
forged chunk from a stale one.**

The rejection *is* the security control; it is complete on its own. The question
is only whether to emit a distinguishing signal, and the answer is no, for three
reasons.

1. **Distinguishing costs either a round trip or the invariant.** The fence
   returns zero rows for "wrong epoch", "wrong assignee" and "no such task"
   alike. A second query to classify is forbidden by the item's own constraint and
   by the standing comment on this path ("Do not add a query, a goroutine, or a
   queue here"). The single-statement alternative is a `LEFT JOIN` against the
   target row returning `epoch_ok` / `assignee_ok` booleans and a nullable inserted
   id. That works, but it makes the statement always return a row, which destroys
   the property CLAUDE.md's epoch-fence invariant leans on: "gate any side effect
   on the fence having actually matched". Today "zero rows" is a structurally
   un-ignorable stop sign; a nullable id is an `if` an implementer can forget, and
   forgetting it publishes forged content to live tailers. Trading a strong
   structural guarantee for a diagnostic is the wrong trade on this path.
2. **The signal would be attacker-controlled volume on the recv goroutine.**
   `handleTaskLog` runs synchronously on the `Connect` recv goroutine that also
   carries that worker's status, inventory and telemetry. A logged rejection lets
   an attacker convert a write-forgery attempt into a log-flood and an ingest-delay
   vector for a legitimate worker. The codebase already learned this lesson once,
   which is why `taskLogErrs` exists and bounds persist-failure logging to one line
   per task per epoch. Reusing that limiter would work, but it is machinery in
   service of a signal we cannot even classify (see 1).
3. **There is nowhere for the signal to go.** `internal/metrics` is a per-worker
   utilization ring buffer, not a counter registry, and there is no alerting
   pipeline. A counter nobody reads is not detection.

Detection of "an agent tried to write to a task that is not its own" is real
value, but it belongs with the audit-log work the backlog item itself lists as
adjacent (`feature-2026-06-26-audit-log-admin-console-actions`), where there is a
durable sink, a retention story and an admin surface. Recorded there rather than
bolted onto the hottest ingest path in the server.

Consequence to accept explicitly: after this change, a forged chunk and a zombie
chunk are indistinguishable in the server log, and both are invisible. That is the
same posture as today, with the security hole closed.

## 8. Test strategy

All of this is database-fence behavior. `handleTaskLog` takes a concrete
`*store.Queries` with no injectable seam, so there is no honest unit-test layer
here - both layers are integration tests under `//go:build integration`, run with
testcontainers. `make test` will not exercise any of it; verification must run
`make test-integration`, or at minimum
`go test -tags integration -p 1 ./internal/store/... ./internal/worker/... -timeout 300s`.

### 8.1 Sequencing, so that RED is behavioral and not a compile error

Adding a parameter makes any new test fail to compile against today's code, and a
compile failure proves nothing about behavior. Implementation must therefore be
staged:

1. **Thread the identity only.** Add the parameter to `handleTaskLog`, the
   `export_test.go` shim and `seedClaimedTask`; pass the correct assignee at every
   existing call site. Do **not** touch the SQL. Full suite stays green; behavior
   is provably unchanged.
2. **Add the new tests.** They now compile, and they are RED *behaviorally*: the
   forged chunk is stored and published, because the SQL still ignores the argument
   they pass. Capture that output as the RED evidence.
3. **Change the fence and regenerate.** Tests go GREEN.

Step 2's RED output is the artifact the acceptance criterion asks for. A plan that
collapses steps 1 and 3 cannot produce it.

### 8.2 Handler layer - the exposure test

New in `internal/worker/handler_tasklog_integration_test.go`:

`TestHandleTaskLog_RejectsAChunkFromANonAssignee`
- Seed a task claimed by worker W1 (epoch becomes 1). Create a second worker W2.
- Subscribe to the task's log stream.
- Call `HandleTaskLog(ctx, W2, chunk{taskID, epoch: 1})`.
- Assert nothing published (non-blocking receive; `HandleTaskLog` is synchronous
  and `Publish` delivers into the subscriber buffer before returning, so this is
  exact and needs no sleep - the same technique the existing stale-epoch test
  uses) and `GetTaskLogs` is empty.
- **Positive control on the same code path**: `HandleTaskLog(ctx, W1, <same chunk>)`
  is stored and published.

Why only the fix produces this: the epoch passed is the task's *current* epoch, so
the existing two-predicate fence matches and today the chunk is both stored and
published. Nothing except an assignee predicate can make the assertion pass. The
contrast case matters - a test that passed a stale epoch would be green today and
therefore vacuous. The positive control rules out the other vacuous failure, a
`handleTaskLog` that stopped ingesting anything at all.

`TestHandleTaskLog_RejectsAChunkForANeverClaimedTask`
- Seed a task and do **not** claim it: `assignment_epoch` 0, `worker_id` NULL.
- Call `HandleTaskLog(ctx, W1, chunk{taskID, epoch: 0})` - the literal repro from
  the backlog item.
- Assert nothing stored, nothing published.
- Positive control: claim the task to W1, then the same worker at the new epoch is
  stored and published.

This one also pins the NULL semantics from 6.2. If someone later writes
`IS NOT DISTINCT FROM` *and* the caller happens to bind a valid W1, the test still
catches it, because `NULL IS NOT DISTINCT FROM W1` is false; to be thorough the
store-layer test below covers the NULL-parameter case directly.

### 8.3 Store layer - the fence specification

Extend `TestAppendTaskLog_EpochGuarded` (`internal/store/store_test.go:241`), which
is where this fence's semantics are pinned, with three cases against a task claimed
by W1 at epoch 1:

- correct epoch + **W2** -> `pgx.ErrNoRows`, nothing inserted;
- correct epoch + **zero-value `pgtype.UUID`** (binds SQL NULL) -> `pgx.ErrNoRows`.
  This is the regression test for 6.2: it fails if anyone switches the comparison
  to `IS NOT DISTINCT FROM`, and it proves a caller that loses its identity fails
  closed;
- an unclaimed task at epoch 0 + a valid worker -> `pgx.ErrNoRows`.

The existing stale-epoch case stays exactly as it is, unmodified, and must remain
green: that is the acceptance criterion "the existing stale-epoch rejection still
works".

### 8.4 Existing tests that must stay green with only mechanical changes

`TestHandleTaskLog_PublishesToATaskScopedSubscriber`,
`TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished`,
`TestHandleTaskLog_NoSubscriberSkipsMarshalButStillPersists`,
`TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`, and both
`handler_tasklog_e2e_integration_test.go` tests. The only permitted edit to these
is passing the seeded worker id. **Any test whose assertions need adjusting is a
finding, not a chore** - it means the change altered behavior somewhere this spec
did not predict, and it must be reported rather than papered over.

One subtlety for the e2e file: `seedClaimedTaskForUser` delegates to
`seedClaimedTask`, so it must forward the new return value.

## 9. Constraint checks

### 9.1 No extra DB round trip

Confirmed achievable. The identity is a third predicate in the existing `WHERE`
clause of the existing `fence` CTE. The statement count, the round-trip count and
the `:one` cardinality are all unchanged. The parameter is already in memory in
`Connect` and requires no query to obtain.

Query plan: the fence still resolves by primary key on `tasks.id`, then evaluates
two integer/UUID comparisons on the fetched row. No new index, no new scan. Under
load the change is unmeasurable, which is the point - this runs once per log chunk
on every task on every worker.

### 9.2 One bounded sender per gRPC stream - untouched

`handleTaskLog` performs no stream sends at all. It writes to Postgres and calls
`broker.Publish`, which is non-blocking. It does not touch `Registry`,
`workerSender` or `sendCh`. The change adds a value parameter and one SQL
predicate. The invariant is not in contact with this diff.

### 9.3 Identity-checked teardown - reinforced, not weakened

Teardown is untouched. The change is in the same spirit: state that belongs to a
worker may only be written by that worker. Worth noting in the implementation that
we fence on worker identity rather than `connection_epoch` precisely so that a
reconnect inside the grace window keeps streaming (see section 4).

### 9.4 Epoch fence invariant - extended, not replaced

The epoch predicate stays and keeps doing its job. The spec's framing, which must
survive into the code comments: *the epoch answers "is this generation current"; the
assignee answers "are you who you say you are". Neither substitutes for the other.*

Recommended (small, optional, conductor's call) amendment to CLAUDE.md's Epoch
fence bullet, so the next reader does not have to rediscover this: add
"`AppendTaskLog` additionally fences on `tasks.worker_id` matching the connection's
authenticated worker - the epoch establishes currency, not identity, and the
comparison must stay NULL-rejecting so a zero-value worker id fails closed."

## 10. Scope

**In scope**
- `internal/store/query/tasks.sql`: the `AppendTaskLog` fence predicate and its
  comment.
- `internal/store/tasks.sql.go`, `internal/store/models.go` as regenerated by
  `make generate`. Follow CLAUDE.md's CRLF procedure: sqlc emits LF, so after
  generating run `git diff --ignore-all-space`, keep only the real content change
  and `git checkout --` the LF-only hunks.
- `internal/worker/handler.go`: `handleTaskLog` signature and call site, the
  `workerUUID.Scan` error check, comment updates.
- `internal/worker/export_test.go`: the `HandleTaskLog` shim.
- `internal/worker/handler_tasklog_integration_test.go`: `seedClaimedTask` return
  value, mechanical call-site updates, two new tests.
- `internal/worker/handler_tasklog_e2e_integration_test.go`: mechanical call-site
  updates.
- `internal/store/store_test.go`: three new cases in
  `TestAppendTaskLog_EpochGuarded`.
- `git mv docs/backlog/bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero.md`
  to `docs/backlog/closed/` via `/backlog close`. Required scope, not optional
  cleanup.

**Out of scope**
- Any proto change. The identity must not come from the wire.
- Per-owner authorization on `GET /v1/tasks/{id}/logs` or `GET /v1/events`. It is
  the reason task ids are broadly discoverable and it is a real design question,
  but it is a read-path policy decision affecting the whole API surface, not part
  of closing a write-forgery hole.
- Re-checking bearer auth on a long-lived SSE connection (noted in the item's
  Context). Separate lifetime concern, separate item.
- Rate limiting the gRPC message loop.
- The `handleTaskStatus` hole below. Same root cause, different query, worse
  impact, and folding it in would triple the blast radius of a fix that should be
  easy to review. It gets its own spec.

## 11. Adjacent finding: `handleTaskStatus` has the same missing check, with worse impact

Found while verifying this item; **proposed** as a separate backlog item, not
filed, and not fixed here.

`handleTaskStatus` (`internal/worker/handler.go:407-509`) reads the task, compares
`int64(task.AssignmentEpoch) != upd.Epoch`, and proceeds. Like `handleTaskLog` it
never checks the sender. So any enrolled agent can send
`TaskStatusUpdate{TaskId: <any pending task>, Epoch: 0, Status: TASK_STATUS_DONE}`
and the fence matches, because a never-claimed task is at epoch 0.

Consequences, traced through the code:

- `done` is not in the `terminal` set, so the retry branch is skipped and
  `UpdateTaskStatus` writes `status = 'done'` on a task that never ran.
  `GetEligibleTasks` unblocks dependents on `dep.status = 'done'`, so the rest of
  the DAG dispatches against work that did not happen, and `RecomputeJobStatus`
  reports the job green.
- `TASK_STATUS_FAILED` on a task with no retries left runs `FailDependentTasks`,
  which cascades the whole downstream DAG to `failed`. That is a one-message
  denial of service against any job in the system.

This is a data-integrity and availability bug, where the log one is an integrity
bug, so it should be prioritized above the item this spec addresses. The fix is
not a copy-paste: `UpdateTaskStatus` has a second caller,
`Dispatcher.failClaimedTask` (`internal/scheduler/dispatch.go:355`), which is
server-internal and has no agent identity to supply, so it needs either a separate
query or a deliberate sentinel. Hence a separate spec.

Proposed item: `bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero`,
type bug, priority high.

## 12. Assumptions / decisions made autonomously

Autonomous run, no human available. Each call and its reasoning:

1. **Fence on `worker_id` in SQL rather than pre-checking in Go.** A Go pre-check
   would need a `GetTask` round trip and would be a TOCTOU window against a
   concurrent requeue. One statement, atomically, is both cheaper and stronger.
2. **`=`, not `IS NOT DISTINCT FROM`** (6.2). Fails closed on a zero-value
   argument, and NULL-rejection is precisely what closes the reported hole.
3. **Silent drop; no forged-vs-stale distinction** (7). The alternative costs
   either a round trip or the "zero rows means no side effect" structural
   guarantee, and the signal would be attacker-controlled volume on the recv
   goroutine with no sink to send it to. Detection routed to the audit-log item.
4. **Fail the connection when `workerUUID.Scan` errors** (6.4), rather than
   preserving today's silent discard. After this change a silent discard is a
   silent 100% log-loss mode.
5. **Do not extend `finishRegister`'s signature to return the UUID** (6.4). The
   string round-trip is lossless; a five-function signature change is not worth it
   inside a security fix.
6. **Staged implementation so RED is behavioral** (8.1). The project's standing
   lesson is that a green test can be vacuous; a compile-error RED is the same
   failure in a different costume.
7. **`handleTaskStatus` deferred to its own spec** (10, 11) rather than folded in,
   despite the shared root cause, because its second caller needs a different
   answer and a bigger diff is a worse-reviewed diff.
8. **Priority.** The backlog item says `medium`. Given section 5 (the window is
   every task, not just never-claimed ones, and live tails are affected), this is
   at least `high` on impact but is bounded by needing an agent token plus a task
   id. Left at the item's stated priority for the ROADMAP's ordering, with the
   corrected impact recorded here so a human can re-rank with evidence.
9. **No proto change, no read-path authz change, no gRPC rate limit** (10). Each
   is a defensible separate piece of work; bundling any of them turns a reviewable
   fix into a subsystem change.

## 13. Acceptance criteria

1. An agent that is not a task's assignee cannot append log lines to it. Proven by
   `TestHandleTaskLog_RejectsAChunkFromANonAssignee` sending the task's *current*
   epoch, with captured RED output from step 2 of 8.1 showing the chunk stored and
   published before the fence change.
2. A never-claimed task at epoch 0 rejects appends from every worker, proven by
   `TestHandleTaskLog_RejectsAChunkForANeverClaimedTask`.
3. A zero-value worker id binds NULL and is rejected, proven at the store layer, so
   a caller that loses its identity fails closed.
4. The existing stale-epoch rejection still works: the unmodified stale-epoch cases
   in `TestAppendTaskLog_EpochGuarded` and
   `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished` stay green.
5. Every other existing test in `internal/worker` and `internal/store` passes with
   only the mechanical addition of a worker-id argument. Any test needing an
   assertion changed is reported as a finding.
6. `handleTaskLog` still performs exactly one DB round trip and one statement; no
   query, goroutine or queue is added to the recv goroutine path.
7. No `Sender`, `Registry` or `sendCh` code is touched.
8. `make test` and `make test-integration` are both green.
9. The backlog item is closed with `/backlog close`, landing the file in
   `docs/backlog/closed/`.
10. The adjacent `handleTaskStatus` finding is surfaced to the human for backlog
    acceptance; it is not filed automatically and not fixed in this change.
