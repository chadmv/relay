# Bound the trailing-log window on a finished task

- Date: 2026-08-14
- Backlog item: `docs/backlog/bug-2026-08-12-tasklog-terminal-task-append-unbounded.md`
- Verified against: worktree `sad-mccarthy-6053a2`, branch `claude/pr-merge-main-2d2fc3`, even with `origin/main` @ `ee88de0`
- Gate mode: autonomous. Every call recorded in section 11.

---

## 1. Problem

A terminal transition deliberately keeps a task's `worker_id` and its `assignment_epoch`, so
`AppendTaskLog`'s fence keeps matching for the agent that just finished the task. That is correct and
load-bearing: it is what lets a trailing log chunk land after the terminal status instead of being
silently discarded. What is missing is the other end of that window. There is no bound of any kind,
so an agent authenticated as worker W can append rows to a task W finished at epoch N for as long as
the row exists. Nothing in the repo deletes from `task_logs`, so those rows are permanent.

This spec adds the missing bound: a chunk for a task that finished longer ago than a configurable
window is rejected by the same fence that already rejects a stale epoch or a wrong assignee, with the
same silent drop, in the same single round trip.

---

## 2. What the code actually does, verified at HEAD

Every claim in this section was read in the tree, not inferred from the item.

### 2.1 The fence today

`internal/store/query/tasks.sql`, `AppendTaskLog` (statement body):

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

Two predicates, exactly as the item quotes it. No terminality predicate, no time bound.
`internal/worker/handler.go` `handleTaskLog` is the only production caller; there are nine test call
sites in `internal/store` and one in `internal/store/retry_job_tasks_integration_test.go`.

### 2.2 A terminal task really does keep its assignee and its epoch

`UpdateTaskStatus` (`internal/store/query/tasks.sql`) writes `status`, `started_at` and `finished_at`
only. It does not write `worker_id` (the argument is a fence, not a value) and does not bump
`assignment_epoch`. Its WHERE is `id`, `assignment_epoch`, `worker_id`, and
`status IN ('pending','dispatched','running')`.

So after `handleTaskStatus` writes `done`, the row still has `worker_id = W` and
`assignment_epoch = N`, and both of `AppendTaskLog`'s predicates still match for W at N, forever. The
hole is real exactly as filed.

### 2.3 The test that pins the trailing flush exists and says what the item says it says

`internal/store/store_test.go:401`,
`TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`. The
load-bearing lines are 428 to 448: it claims a task for worker W (epoch 1), writes `done` through
`UpdateTaskStatus`, asserts

```go
assert.Equal(t, claimed.AssignmentEpoch, done.AssignmentEpoch,
    "UpdateTaskStatus must not bump the epoch - the assignment is retained, not ended")
```

then appends a chunk at `done.AssignmentEpoch` as `done.WorkerID` and requires

```go
require.NoError(t, err, "a trailing chunk from the assignee must still persist after a terminal status")
```

then reads the row back and asserts its content. The constraint the item builds on is not imaginary.
A conjunctive status predicate on `AppendTaskLog` would turn that `require.NoError` red, and the
item's core design claim survives.

Note the timing: the append happens microseconds after the terminal write, so any positive time
window keeps this test's *assertions* green. Section 8.4 covers the one mechanical edit it does need.

### 2.4 `finished_at` exists and is set on every terminal transition that matters

`tasks.finished_at TIMESTAMPTZ` is declared in `internal/store/migrations/000001_initial.up.sql:64`.
Nullable, no default. Writers:

| Writer | Sets `finished_at` | Clock | Reachable through the fence? |
| --- | --- | --- | --- |
| `UpdateTaskStatus` via `handleTaskStatus` (`internal/worker/handler.go:582-599`) | `time.Now()` for `done`, `failed`, `timed_out`; NULL for `running` | relay-server Go clock | Yes, this is the case that matters |
| `UpdateTaskStatus` via `Dispatcher.failClaimedTask` (`internal/scheduler/dispatch.go:355-360`) | `time.Now()` | relay-server Go clock | Yes |
| `CancelJobTasks` | `NOW()` | database clock | No: it also nulls `worker_id` and bumps the epoch |
| `FailDependentTasks` | `NOW()` | database clock | No: it only touches `status = 'pending'` rows, and a pending row always has `worker_id IS NULL` (see below) |
| `IncrementTaskRetryCount`, `RequeueTaskByID`, `RetryJobTasks` | `NULL` on reopen | n/a | n/a |

"A pending row always has `worker_id IS NULL`" was checked against every statement that writes
`status = 'pending'`: `IncrementTaskRetryCount`, `RequeueTask`, `RequeueTaskByID`,
`RequeueWorkerTasks`, `RequeueWorkerTasksIfEpoch` and `RetryJobTasks` all set `worker_id = NULL` in
the same UPDATE, and `CreateTask` never sets one. `ClaimTaskForWorker` sets `worker_id` and
`status = 'dispatched'` atomically.

Two consequences, both used by the design:

1. The item's prescribed mechanism compiles. `finished_at` is a real column, reliably populated on
   terminal transition.
2. **Every `finished_at` value that can be read through this fence was written by relay-server's own
   Go clock**, never by the database's `NOW()`. That is what makes a Go-computed cutoff (section 6.2)
   strictly better than `NOW() - interval`: the comparison stays inside one clock domain.

`RequeueWorkerTasks` and `RequeueWorkerTasksIfEpoch` do not null `finished_at`, unlike their
siblings. Harmless, because they only match `dispatched`/`running` rows whose `finished_at` is
already NULL by the chain above, and the design does not depend on `finished_at` being NULL for a
live task (section 6.1). Recorded so a future reader does not mistake it for a hole.

### 2.5 Retention: nothing prunes `task_logs`, and the one cascade has no caller

`grep -n "DELETE FROM" internal/store/query/*.sql` returns eleven statements; none names
`task_logs`. The janitor in `cmd/relay-server/main.go:245-258` deletes only from
`agent_enrollments`. No migration prunes the table. `idx_task_logs_task_id_id` (migration 000018)
is the only maintenance the table has ever received.

There is one indirect path the item does not mention: `task_logs.task_id` is
`REFERENCES tasks(id) ON DELETE CASCADE` and `tasks.job_id` is
`REFERENCES jobs(id) ON DELETE CASCADE`, so `DeleteJob` (`internal/store/query/jobs.sql:110`) would
cascade all the way down. **`DeleteJob` has no production caller** - the only reference in the tree is
its own generated method, `internal/store/jobs.sql.go:105`. So there is no operator-reachable way to
reclaim `task_logs` storage at all, short of raw SQL against the database. That strengthens the
item's retention point rather than weakening it.

### 2.6 How late a legitimate trailing chunk can actually be, with numbers

This is the number the window has to clear, and the item does not have it. Derived from the shipped
agent:

- **In the ordinary case, a trailing chunk is not even possible.** `internal/agent/runner.go:202-215`
  hands `exec` two `chunkWriter`s as `cmd.Stdout`/`cmd.Stderr`, so `exec` owns the copy goroutines and
  `cmd.Wait()` returns only after they finish. `sendFinalStatus` is called after the command loop
  (`runner.go:240`). All chunks are therefore enqueued on `sendCh` before the status, `sendCh` is
  drained FIFO by the single sender goroutine (`internal/agent/agent.go:167`), gRPC preserves stream
  order, and the server's recv goroutine handles messages sequentially. Ordering holds end to end.
- **Case 1, WaitDelay.** `cmd.WaitDelay = 5 * time.Second` (`runner.go:190`). If a leaked grandchild
  still holds the write end, `Wait` force-closes the descriptors and returns while a copy goroutine
  may still be parked in `chunkWriter.Write`. That goroutine can enqueue one more chunk after the
  status. Bound: **5 s**.
- **Case 2, a full `sendCh`.** `sendCh` is capacity 64. Two blocked senders on one channel have no
  FIFO guarantee, so a parked chunk can lose to the status send. Its lateness is then bounded by how
  long the connection can stay stalled, which gRPC keepalive caps at ping 30 s + timeout 10 s
  (`cmd/relay-server/main.go:181-184`). Bound: **40 s**, after which the transport closes.
- **Case 3, reconnect.** Messages still sitting in `sendCh` survive a reconnect (the channel is
  shared across connections; only the one message already pulled is lost, and task-log chunks are not
  replayed - `agent.go:159-166`). The wait is the reconnect backoff, capped at **60 s**
  (`nextReconnectBackoff`), plus a registration round trip.

Worst case composed: 5 s + 40 s + 60 s + registration, call it **under 2 minutes**. Nothing in the
agent buffers log output across a task boundary, and no path emits a chunk after `Finalize`
(`runner.go:134-143` sends an inventory message there, not a log chunk).

### 2.7 What runs before the fence, per CLAUDE.md's "enumerate what runs before it" rule

`handleTaskLog` (`internal/worker/handler.go:726-772`) does exactly three things before the statement:
`taskID.Scan(chunk.TaskId)` (silent return on failure), the stream enum mapping, and the
`AppendTaskLog` call. `workerID` is the connection's authenticated identity, resolved once in
`Connect` (`handler.go:115-119`) and never read off the wire. Nothing else touches the database, and
the publish is strictly after a successful insert. The new predicate is evaluated inside the same
statement, so nothing new runs before the fence.

---

## 3. Discrepancies between the item and HEAD

Ordered by importance.

1. **"After that change no production statement can modify a terminal task's row at all" is now
   false.** `RetryJobTasks` (shipped 2026-08-14 as PR #127, `POST /v1/jobs/{id}/retry`) reopens
   exactly the terminal rows `status IN ('failed','timed_out')`, plus `done` under `include_done`,
   and bumps `assignment_epoch`. The item was written on 2026-08-12, two days before that landed.
   The consequence is favourable and worth stating in the spec rather than discovering later: an
   operator retry ends the old assignment, so it *does* close this window for the tasks it touches.
   It closes nothing for a `done` job nobody retries, which is the overwhelming majority, so the bug
   stands. But the item's framing of "the last remaining writer" needs the correction.
2. **The acceptance criterion "`TestUpdateTaskStatus_...StillPersists` passing with no edit" cannot be
   met by any design that parameterizes the window.** That test calls `AppendTaskLog` directly with a
   keyed struct literal, so a new `AppendTaskLogParams` field defaults to the zero
   `pgtype.Timestamptz`, which binds SQL NULL, which fails the new predicate closed. The test goes RED
   until it passes a cutoff. The right criterion, and the one this spec adopts, is: **green with a
   one-line mechanical parameter addition and no assertion changed**. An assertion needing adjustment
   is still a finding. Section 8.4 spells this out.
3. **The item's prescribed predicate is subtly the wrong shape.** `(t.finished_at IS NULL OR
   t.finished_at > cutoff)` fails **open** on any terminal row whose `finished_at` is NULL - a
   pre-existing row from an older schema, or a row written by a future terminal writer that forgets
   the timestamp. This project's standing rule is that a fence must fail closed on a missing value
   (the whole `=` versus `IS NOT DISTINCT FROM` argument on the same statement). Section 6.1 uses a
   status allow-list as the live-task arm instead, which fails closed on a NULL `finished_at`.
4. **"A status predicate is the wrong fix" is right about conjunction and wrong as a blanket rule.**
   The chosen design contains a status allow-list, as a *disjunct* covering live tasks. Conjoining it
   would break the flush; disjoining it is what makes the whole predicate fail closed. The item's
   Notes section, which warns that "anybody who fixes this with a status predicate will pass every
   existing test except one", should be read as being about the conjunctive spelling only.
5. **The item omits the number that justifies the window.** Section 2.6 derives it (under 2 minutes,
   from three independent agent-side timers). The item asks for "generous by default" with no
   reference quantity, which is how a knob gets a number nobody can defend later.
6. **"No statement in the repo ever deletes from it" is literally true but incomplete.** There is a
   cascade path via `DeleteJob`, which has no caller (section 2.5). The correction makes the retention
   argument stronger, not weaker.
7. **Everything else in the item checks out.** The quoted SQL is byte-accurate, the pinning test says
   what the item says it says, `finished_at` exists and is populated, no retention exists, and the
   repro sequence is exactly right. This is a materially accurate item, which is not the recent norm.

---

## 4. Threat model and honest exposure

**Who can reach it.** A principal holding worker W's long-lived agent token, which is stored at
`<state-dir>/token` mode 0600 on the agent host. That is either W itself misbehaving, or an attacker
who has compromised W's host or exfiltrated that file. Alternatively, a deployment running with
`RELAY_ALLOW_AUTO_ENROLL=true` (off by default) lets anyone open a `Connect` stream, and
`bug-2026-08-12-auto-enroll-hostname-takeover` describes how that can be turned into *becoming* an
existing worker. That is a separate filed bug and an amplifier here, not part of this exposure.

**What the principal can reach.** Only tasks W actually ran, at the epoch it ran them. The 2026-08-12
assignee fence removed every other task from its reach. This is not a privilege-boundary bug: the
attacker already owned that task's log content while it ran.

**What it costs.** Durable rows in `task_logs`, permanently, at a rate bounded only by Postgres. Each
`TaskLogChunk` is capped by gRPC's 4 MB default receive size; each insert is one round trip on the
attacker's own recv goroutine, so the practical ceiling is thousands of small rows per second per
stream. There is no per-task cap and no retention, and section 2.5 shows no operator-reachable way to
reclaim the space.

**The honest framing.** The marginal defect is **duration, not rate**. A *live* task's log stream is
equally uncapped today, and always has been; the difference is that a live task ends. This bug is
that the window never closes. Sizing the window is therefore a correctness question about real
output, not a throughput control - which is why section 6.5 picks a generous default rather than a
tight one, and why a per-task volume cap is proposed separately (section 10).

**Severity.** Medium, as filed. It requires a valid worker credential, produces no incorrect state
and no cross-tenant reach, and its cost is unbounded storage plus SSE noise on a task detail view
that anyone tailing that task would see. Real and bounded.

---

## 5. Scope decision: this spec covers ONE item, not two

ROADMAP.md places this item and `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` together in Now
with the rationale "both are `handleTaskLog` and both fence the same wire-supplied fields, so one
slice can carry the pair". **Decision: two slices. This spec covers the terminal-append bound only.**

The roadmap's stated rationale does not survive contact with either item:

- **The limiter item is not confined to `handleTaskLog`.** Its half B is two unconditional
  `log.Printf` calls at the top of `handleTaskStatus` (`handler.go:426` and `:432`), a different
  handler with a different message type, and the item explicitly says to fix both halves together
  because they share a budget. So "one slice can carry the pair" would actually be a three-handler
  slice.
- **The limiter fences nothing.** It is a logging rate limiter; there is no fence, no predicate and
  no wire-supplied field being validated. The shared-shape claim is a coincidence of file, not of
  mechanism.
- **The layers do not overlap.** This slice is a SQL predicate plus a regenerated store layer plus
  one struct field plus an env var. That slice is pure Go on the logging path, and its own item flags
  that the honest fix may move limiter state onto the connection, which "touches `Connect`'s shape".
  Neither change constrains the other.
- **The test strategies do not interfere, and do not help each other either.** This slice's RED is
  behavioural at the handler level with a backdated `finished_at`. That slice's RED is a log-line
  count driven by NUL-bearing content, which fails during bind-parameter decode (SQLSTATE 22021)
  *before* the fence is ever evaluated - so the new predicate is invisible to it and it is invisible
  to the new predicate. The one thing to check at integration time is that
  `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` stays green here, which section 8.4
  covers.
- **Attribution.** Combining them means a trailing-flush truncation regression and a log-volume
  regression both point at one PR touching one file. Two of the last three iterations in this project
  found the item itself was wrong about its own premise; keeping the blast radius one item wide is
  what made that recoverable.

They should still be scheduled adjacently, and the limiter slice should go **second**, because it can
then reuse this slice's handler fixtures and because whoever writes it will have just read
`handleTaskLog` closely.

---

## 6. Design

### 6.1 The fence gains a third predicate, in two arms

```sql
WITH fence AS (
    SELECT t.job_id FROM tasks t
    WHERE t.id = sqlc.arg(task_id)
      AND t.assignment_epoch = sqlc.arg(assignment_epoch)
      AND t.worker_id = sqlc.arg(worker_id)
      AND (t.status IN ('pending', 'dispatched', 'running')
           OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
), ins AS (
    INSERT INTO task_logs (task_id, stream, content)
    SELECT sqlc.arg(task_id), sqlc.arg(stream), sqlc.arg(content) FROM fence
    RETURNING id, created_at
)
SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;
```

Read it as: **a live task always accepts logs; a finished task accepts logs for a while longer.**

- **The first arm is an ALLOW-LIST**, identical in spelling and in reasoning to `UpdateTaskStatus`'s
  and `IncrementTaskRetryCount`'s, and it is the complement of the terminal set `RecomputeJobStatus`
  counts. It must stay an allow-list: under the deny-list spelling a status added later would be
  admitted by default, and here that default is the *permissive* one.
- **The second arm is the bound.** A terminal row is accepted only while its `finished_at` is inside
  the window.
- **Every failure mode of the pair is closed.** A terminal row with a NULL `finished_at` (an old row,
  or a future writer that forgets the timestamp) fails both arms: `NULL > cutoff` is NULL, which is
  not true, so it is rejected. A caller that omits the cutoff binds NULL and rejects every terminal
  append. A status added later is in neither arm and is rejected until somebody decides otherwise.
  Contrast the item's `t.finished_at IS NULL OR ...` spelling, which fails open on all three.
- **The disjunction is what preserves the trailing flush.** The arms are OR-ed, never AND-ed. A
  `done` task with a recent `finished_at` passes on the second arm. Section 8 pins this in both
  directions.

**The trap this creates, which must be written into the statement's comment.** A new *non-terminal*
status omitted from the first arm silently drops 100% of that state's log output, because a
non-terminal row has `finished_at IS NULL` and therefore fails the second arm too. That is not
hypothetical: `TASK_STATUS_PREPARING` already exists in the proto and the agent already streams
prepare-phase progress as `LOG_STREAM_PREPARE` chunks (`internal/agent/runner.go:372-396`) while the
row is `dispatched`. If `preparing` ever becomes a persisted status and is not added here, every
workspace-sync log line in the system disappears with no error anywhere. This has to go in the
comment and in the vocabulary test's site list (section 6.7).

### 6.2 Pass a cutoff timestamp, not an interval

`min_finished_at` is an absolute `timestamptz` computed in Go as `time.Now().Add(-window)`, not an
interval evaluated against the database's `NOW()`.

- **It stays in one clock domain.** Section 2.4 established that every `finished_at` reachable
  through this fence was written by relay-server's Go clock. Comparing it against relay-server's Go
  clock means skew between the app host and the database host cannot shift the window at all.
  `NOW() - interval` would compare the database's clock against a Go-written timestamp.
- **It makes the store layer a pure function of its arguments**, so a store-level test can pin the
  predicate at an exact boundary without sleeping and without backdating.
- **It avoids `pgtype.Interval`**, whose sqlc mapping is the fiddlier of the two.

Residual: skew between two relay-server instances shifts the effective window by that skew. On
NTP-synced hosts that is milliseconds against a window of minutes. Recorded, not mitigated.

The `::timestamptz` cast on the parameter follows the existing idiom in this file
(`sqlc.arg(include_done)::bool` in `RetryJobTasks`) and pins the inferred Go type. The plan must
verify after `make generate` that the emitted field is `MinFinishedAt pgtype.Timestamptz` and not
`interface{}`.

### 6.3 The bound is enforced in SQL, in the fence, and that is the only place it can go

`AppendTaskLog`'s fence CTE is the statement's only WHERE clause, so there is no CTE-versus-row-level
choice to make here. **The `RetryJobTasks` / EvalPlanQual lesson does not apply to this statement**
and must not be cargo-culted into its comment: that lesson is about an `UPDATE` whose row-level qual
is re-checked after it unblocks, and this statement performs no UPDATE and takes no row lock. Stating
this explicitly because a reviewer who has read that comment will reach for it.

What *is* true and unchanged: the fence is a non-locking `SELECT`, so under READ COMMITTED the whole
statement sees one snapshot and a concurrent terminal transition committing mid-statement is not
observed. A chunk can therefore be admitted against a snapshot that is microseconds stale. That
window exists today for the epoch and worker predicates, is unchanged in kind and in size by this
change, and is the correct trade against the standing "exactly one round trip, no locks on the recv
goroutine" constraint.

The side effect is already gated on the fence having matched: no fence row means no inserted row
means zero result rows means `pgx.ErrNoRows`, and `handleTaskLog` returns before the publish. That is
CLAUDE.md's requirement, and it is satisfied by construction rather than by a new check.

### 6.4 What the agent sees when the window has closed: nothing

The rejection joins the existing `pgx.ErrNoRows` case. `handleTaskLog` already drops it silently and
returns **before** the publish, so a chunk outside the window is stored nowhere and shown nowhere -
never the "appears in a live view and then vanishes on refresh" failure the epoch-fence invariant
names. **Zero Go changes on the rejection path.**

Three consequences, all deliberate:

- The three rejection reasons - stale generation, wrong assignee, closed window - stay deliberately
  indistinguishable to the caller, exactly as the 2026-08-12 spec argued for the first two.
- **No new log line.** A log line here would be caller-driven volume on the recv goroutine and would
  hand back the flood vector `bug-2026-08-12-tasklog-err-limiter-attacker-keyed` is about. It would
  also fire on the legitimate late-flush case, which is the one an operator would most want quiet.
- The cost is diagnosability: an operator whose window is set too small sees output silently
  truncated with no signal anywhere. That asymmetry is the entire reason the default is generous
  (section 6.5) and the reason the README row has to say what a too-small value does. Detection
  belongs with the audit-log work, as with every other drop on this path.

### 6.5 The knob and the default

`RELAY_TASKLOG_TRAILING_WINDOW`, a Go duration, default **`15m`**.

The name matches the sibling knobs (`RELAY_WORKER_GRACE_WINDOW`, `RELAY_TELEMETRY_WINDOW`).

**The default is justified by a number, not a feeling.** Section 2.6 derives the worst case for a
legitimate trailing chunk from three independent agent-side timers - `cmd.WaitDelay` 5 s, gRPC
keepalive 30 s + 10 s, reconnect backoff capped at 60 s - which compose to under 2 minutes. 15
minutes is roughly 8x that ceiling. It is large enough that no realistic agent-side delay truncates
real output, and small enough that "forever" is genuinely closed: a compromised agent's write window
per finished task drops from unbounded to 15 minutes.

Handling of a bad value follows the `RELAY_TELEMETRY_WINDOW` idiom (`d > 0` or keep the default) with
one addition: **log a warning at startup** when the variable is set and unusable, rather than
ignoring it silently. This is a security-relevant knob and a silently-ignored typo would leave an
operator believing they had tightened something they had not. Startup-only, so it costs nothing on
the hot path. Do not `log.Fatalf`: an unparseable duration should not stop a server from booting when
a safe default exists.

An operator who needs today's behaviour back can set `8760h`. That is an intentional escape hatch and
should be named as such in the README row.

### 6.6 Threading the window

`Handler` gains an exported field, set by `cmd/relay-server` after construction, exactly like the
existing `Metrics` and `AllowAutoEnroll`:

```go
// DefaultTrailingLogWindow is how long after a task's finished_at its assignee
// may still append log chunks. See RELAY_TASKLOG_TRAILING_WINDOW.
const DefaultTrailingLogWindow = 15 * time.Minute

// TrailingLogWindow bounds the post-terminal append window. Non-positive means
// DefaultTrailingLogWindow.
TrailingLogWindow time.Duration
```

`handleTaskLog` resolves it per call:

```go
window := h.TrailingLogWindow
if window <= 0 {
    window = DefaultTrailingLogWindow
}
// ... MinFinishedAt: pgtype.Timestamptz{Time: time.Now().Add(-window), Valid: true},
```

Rationale for an exported field over a constructor argument: `NewHandler` and `NewHandlerWithGrace`
are called from every test in `internal/worker`, and a signature change would be pure churn. The
non-positive-means-default rule keeps every existing construction site correct without edits, and
lets a test set a 50 ms window to pin the boundary.

One extra `time.Now()` and one 24-byte struct per chunk. No allocation, no round trip, no goroutine.

### 6.7 Comments and docs that must move with the code

- **`AppendTaskLog`'s comment block** gains: why the bound exists; why it is a disjunction and never
  a conjunction, with a pointer to the pinning test by name; why the first arm is an allow-list; the
  `preparing` trap from section 6.1; and the sentence the item asked to be preserved in the code,
  that the assignment outliving the task is load-bearing and a conjunctive status predicate would
  silently truncate the tail of every task's output.
- **`TestTasksStatusVocabularyIsExactly`'s comment** (`internal/store/tasks_status_vocabulary_lockstep_test.go:35-62`)
  gains a sixth site. The guidance for it is the opposite of the other five and must be spelled out:
  a new NON-terminal status must be ADDED here or that state's log output is silently dropped
  entirely; a new TERMINAL status must stay OUT, and is then bounded by `finished_at` like every
  other terminal status.
- **README** gains a `RELAY_TASKLOG_TRAILING_WINDOW` row in the env table (around line 276, beside
  `RELAY_WORKER_GRACE_WINDOW`), saying what it bounds, what a too-small value does silently, and that
  a very large value restores the old unbounded behaviour.
- **CLAUDE.md** needs no amendment. This does not change the epoch-fence invariant, it adds a
  predicate underneath it. Recorded as a decision, since the last three slices in this family each
  amended that bullet.

---

## 7. Alternatives considered and rejected

- **A conjunctive status predicate** (`AND t.status IN ('pending','dispatched','running')`).
  Rejected: turns `TestUpdateTaskStatus_...StillPersists` red and silently truncates the tail of every
  task's output in production. The item is right, and section 2.3 verified the test that proves it.
- **A per-task row or byte cap.** Rejected for this slice, proposed as its own item. It needs either
  a `COUNT` subquery on the recv path or a counter column on `tasks` (a migration plus a write on
  every chunk), it does not bound the indefinite case any better than time does, and its natural
  value is a product question about how large a legitimate build log is. Critically, it is **not
  specific to terminal tasks**: a live task is equally uncapped today. It belongs in an item about
  log volume, not in a fix for a window that never closes.
- **A retention job that prunes old `task_logs`.** Rejected as the fix, proposed as its own item. It
  is complementary rather than alternative: it bounds total storage but does nothing about a
  finished task remaining writable, and this bug would still let an attacker keep a task's log stream
  alive in the SSE tail forever. It is also a different risk profile (a reaper that deletes the wrong
  rows is worse than the bug) and pairs naturally with
  `idea-2026-08-13-reap-expired-invites-and-tokens`.
- **A background sweeper that bumps `assignment_epoch` on tasks finished more than N ago.** This is
  the tempting no-new-predicate option: it closes the window using the fence that already exists.
  Rejected on three counts. It reintroduces a production writer to terminal rows, which the
  2026-08-12 retry-resurrect slice deliberately eliminated. It is a whole background job, a periodic
  scan and a write amplification of one UPDATE per finished task, against one predicate. And it makes
  the bound eventually-consistent and invisible in the statement, so a reader of `AppendTaskLog` would
  have no way to know a bound exists.
- **Closing the stream or disabling the worker on a rejected chunk.** Rejected: the server cannot
  distinguish a forged chunk from a legitimately late one, and both the "zombie agent reconnecting"
  and "slow network" cases would take down a healthy worker. Rejection must stay as cheap and as quiet
  as it is today.
- **A hard-coded window with no env var.** Genuinely arguable, since every quantity the window must
  clear is itself hard-coded in the agent (section 2.6), and it would let the pinning test stay
  byte-identical. Rejected on the project's standing preference for env-configurable operational
  timeouts, and because the failure mode of a too-short window is silent truncation of real output -
  operators need an escape hatch that does not require a rebuild.

---

## 8. Test strategy

### 8.1 Staging, so RED is behavioural and not a compile error

Adding a field to `AppendTaskLogParams` makes ten call sites bind NULL at once, which would turn
several existing store tests red for a mechanical reason and destroy the signal. Stage it:

1. **Write the exposure test first, at the handler layer**, where the new parameter is internal and
   the test does not name it. It backdates `finished_at` with raw SQL and calls
   `h.HandleTaskLog(...)`. Against today's code the chunk is stored, so the test's "zero rows"
   assertion is **RED for the right reason**. Run it, record the failure output.
2. **Then** the SQL change and `make generate`.
3. **Then** the mechanical parameter additions at the store test call sites, and only then the
   store-layer specification tests.

`make generate` on this repo emits LF into a CRLF tree: after generating, run
`git diff --ignore-all-space`, keep only the real content change, and `git checkout --` the LF-only
hunks. Then confirm the regenerated `AppendTaskLog` doc comment in `internal/store/tasks.sql.go`
actually matches the new comment in `query/tasks.sql` - the CRLF revert has silently discarded a
regeneration in this repo before.

### 8.2 Handler layer, the exposure and its controls

In `internal/worker/handler_tasklog_integration_test.go`, reusing `seedClaimedTask`:

- `TestHandleTaskLog_RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow`. Claim, write `done`
  through `UpdateTaskStatus`, backdate the row with
  `UPDATE tasks SET finished_at = NOW() - interval '1 hour' WHERE id = $1`, then `HandleTaskLog` as
  the genuine assignee at the correct epoch. Assert zero rows in `task_logs` **and** that nothing was
  published (subscribe first, assert the channel stays empty). This is the RED test.
- `TestHandleTaskLog_TrailingChunkJustAfterATerminalStatusIsStillStored`. Same setup, no backdating.
  Assert the row is stored and published. This is the regression control for the flush, at the layer
  the flush actually happens.
- `TestHandleTaskLog_LiveTaskWithNoFinishedAtIsUnaffected`. A `running` task, default window. Assert
  stored and published. This is the positive control the item asks for, and it is what goes red if
  the arms are ever conjoined.
- **Boundary**, using the field rather than the clock: set `h.TrailingLogWindow = 50 * time.Millisecond`,
  finish the task, assert one chunk lands immediately, sleep 150 ms, assert the next chunk does not.
  This is the only test that proves the knob is wired to the predicate rather than merely existing;
  per the standing lesson, asserting the constant proves nothing.

### 8.3 Store layer, the predicate specification

In `internal/store/store_test.go`, one table-driven test over the fence with an explicit
`MinFinishedAt` so it needs no sleeping:

| Case | Row state | `MinFinishedAt` | Expect |
| --- | --- | --- | --- |
| live task | `dispatched`, `finished_at` NULL | now | stored (first arm) |
| running task | `running`, `finished_at` NULL | now | stored (first arm) |
| just finished | `done`, `finished_at` = now | now - 15m | stored (second arm) |
| long finished | `done`, `finished_at` = now - 1h | now - 15m | `pgx.ErrNoRows` |
| terminal, NULL `finished_at` | `done` forced via raw SQL with `finished_at = NULL` | now - 15m | `pgx.ErrNoRows` (fails closed) |
| cutoff omitted | `done`, `finished_at` = now | zero value (NULL) | `pgx.ErrNoRows` (fails closed) |

The last two are the cases that discriminate this spelling from the item's prescribed one, so they
are the ones that must not be dropped for brevity.

### 8.4 Existing tests: predicted changes, and what counts as a finding

- `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist`
  (`internal/store/store_test.go:401`) gains **one line**, `MinFinishedAt` set to a live cutoff, on the
  `AppendTaskLog` call at line 439. **No assertion may change.** If any assertion in it needs
  adjusting, stop and report it as a finding: that would mean the design broke the flush.
- The eight other `AppendTaskLog` call sites in `internal/store` and the one in
  `retry_job_tasks_integration_test.go` gain the same mechanical line. Most append to live tasks and
  would pass on the first arm regardless; set the field anyway so no call site models a caller that
  omits it.
- `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` must stay green **with no edit at
  all**: it drives failures with NUL-bearing content, which Postgres rejects during bind-parameter
  decode before the fence is evaluated, so the new predicate is unreachable from it. If it changes,
  something is wrong with the understanding in section 5.
- Every other test in `handler_tasklog_integration_test.go` and
  `handler_tasklog_e2e_integration_test.go` operates on live tasks and must stay green with no edit.
  These are the tests that go red if the arms are conjoined, so their staying green is the flush
  guarantee at the handler layer.

### 8.5 Mutation matrix

| Mutation | Must turn RED |
| --- | --- |
| Delete the `finished_at` arm entirely | `..._RejectsAChunkForATaskThatFinishedOutsideTheTrailingWindow`, store case "long finished" |
| Delete the status arm | every live-task test in `handler_tasklog_integration_test.go`, store cases "live"/"running" |
| Change `OR` to `AND` | live-task tests, and `..._TrailingChunkJustAfterATerminalStatusIsStillStored` |
| Rewrite the second arm as the item's `finished_at IS NULL OR finished_at > cutoff` | store cases "terminal, NULL finished_at" and "cutoff omitted" |
| Hard-code `DefaultTrailingLogWindow` at the call site, ignoring the field | the 50 ms boundary test |
| Ignore `RELAY_TASKLOG_TRAILING_WINDOW` in `main.go` | a `cmd/relay-server` unit test on the parse helper (a non-positive and an unparseable value both keep the default) |

---

## 9. Constraint checks

- **Epoch fence.** `AppendTaskLog` satisfies the invariant's **first branch**: it fences on
  `assignment_epoch` matching the caller's epoch. That is unchanged. The new predicate is additive and
  changes no branch: it neither ends a generation nor guards a terminal-only writer. The identity
  predicate on `worker_id` stays a NULL-rejecting plain `=`. The side effect stays gated on the fence
  having actually matched, since `pgx.ErrNoRows` still short-circuits before the publish.
- **Status predicates are allow-lists.** The first arm is one, in the same spelling as
  `UpdateTaskStatus` and `IncrementTaskRetryCount`, and the vocabulary guard gains it as a named site
  with its own (inverted) guidance.
- **One bounded sender per gRPC stream.** Untouched. No send is added, and the change is on the recv
  path.
- **Identity-checked teardown.** Untouched.
- **No interior pointers across locks.** Untouched; the new state is a `time.Duration` value on
  `Handler`, read-only after startup.
- **Single JSON entry point / single job-spec pipeline / `tokenhash.Hash`.** Not implicated. No HTTP
  body, no job spec, no hashing.
- **Never edit `*.sql.go` or `models.go`.** The change is in `query/tasks.sql` plus `make generate`,
  with the CRLF review dance in section 8.1.
- **Load.** No new query, statement, index, goroutine or queue. The predicate is evaluated on a row
  already located by primary key, so the plan does not change and no index is needed. Per chunk the
  cost is one `time.Now()` and one extra bound parameter.
- **Security.** The change only removes reach; it grants nothing. The rejection path emits no log
  line, so it introduces no new attacker-driven volume. The knob is read once at startup from the
  process environment, never from a request.

---

## 10. Scope

**In scope**

1. `AppendTaskLog`'s fence gains the two-arm predicate, plus its comment.
2. `make generate` and the CRLF review.
3. `Handler.TrailingLogWindow`, `DefaultTrailingLogWindow`, and the cutoff computed in
   `handleTaskLog`.
4. `RELAY_TASKLOG_TRAILING_WINDOW` parsed in `cmd/relay-server/main.go`, with a startup warning on an
   unusable value.
5. README env-table row.
6. `TestTasksStatusVocabularyIsExactly`'s comment gains `AppendTaskLog` as a sixth site.
7. Tests per section 8.
8. Closing `docs/backlog/bug-2026-08-12-tasklog-terminal-task-append-unbounded.md` via
   `/backlog close`, which `git mv`s it to `docs/backlog/closed/`. Required scope, not cleanup.

**Out of scope**

- The err-limiter item (section 5). Separate slice, scheduled next.
- `task_logs` retention. Proposed as a new item below.
- A per-task log volume cap. Proposed as a new item below.
- `handleTaskLog`'s `int32(chunk.Epoch)` truncation
  (`bug-2026-08-12-tasklog-epoch-int32-truncation`). Same call site and one line, but a different
  defect with its own filed item; folding it in would blur attribution. Note for the plan: if the
  implementer is already editing that struct literal, flag it rather than fixing it silently.
- Any change to what a rejected chunk reports to the agent.

**Proposed backlog items (NOT filed; for human accept)**

1. *`task_logs` has no retention and no operator-reachable delete path.* `DeleteJob` exists,
   cascades correctly, and has no caller; nothing else prunes the table. Pairs with
   `idea-2026-08-13-reap-expired-invites-and-tokens`. The trap to record up front is that a naive
   reaper keyed on `created_at` would delete the logs of a long-running task that is still writing.
2. *No per-task cap on log volume, live or finished.* A single task can insert unbounded rows at up
   to 4 MB per chunk, and this is true today for a running task; the terminal-append bound does not
   address it. Needs a decision on a counter column versus a count subquery, and on what a legitimate
   build log's size ceiling is.

---

## 11. Decisions taken autonomously

Gate mode was autonomous. Each of these would have gone to a human.

1. **One item, not two.** Section 5. Called: separate slices, scheduled adjacently, limiter second.
   The roadmap's combining rationale is factually wrong and is the main thing to sanity-check here.
2. **The default window, `15m`.** Derived in section 2.6 as roughly 8x the worst-case legitimate
   lateness. A human might prefer `1h` for extra margin, or `5m` for a tighter bound. Any value above
   ~2 minutes is defensible; the arithmetic is written down so the number can be re-argued rather
   than re-derived.
3. **Env-configurable rather than a hard-coded constant.** Section 7. Called on the project's
   standing preference plus the silent-truncation failure mode, against a real argument that every
   quantity it must clear is hard-coded in the agent anyway.
4. **The predicate's shape differs from the item's prescription.** Section 6.1 uses a status
   allow-list as the live-task arm instead of `finished_at IS NULL`, to fail closed. This is the
   design decision most worth a human's eye, because it puts a status allow-list into a statement the
   item explicitly said should not get one - for a different reason than the item was arguing against.
5. **The pinning test's acceptance criterion is relaxed** from "no edit" to "one mechanical parameter
   line, no assertion changed" (section 3, item 2). The stricter version is unachievable.
6. **Two follow-on items proposed, not filed** (section 10), per the never-auto-file rule.
7. **CLAUDE.md is not amended.** Recorded because the last three slices in this family each amended
   the epoch-fence bullet, so silence here should read as a decision.

---

## 12. Acceptance criteria

1. A chunk from a task's genuine assignee, at the correct epoch, more than the window after
   `finished_at`, is stored nowhere and published nowhere - proven by a handler-layer test that was
   RED against HEAD before the SQL changed, with the RED output recorded.
2. A trailing chunk arriving just after a terminal status IS stored and IS published, proven at the
   handler layer, and
   `TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` is green
   with one mechanical parameter line and **no assertion changed**.
3. A live task at the current epoch from its assignee is still stored and still published, with a
   NULL `finished_at` and any cutoff.
4. A terminal row with a NULL `finished_at`, and a caller that omits the cutoff, are both rejected -
   the fence fails closed.
5. `handleTaskLog` still performs exactly one DB round trip and one statement. No new query,
   goroutine or queue on the recv goroutine.
6. No new log line on any rejection path, and
   `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` is green with no edit.
7. `RELAY_TASKLOG_TRAILING_WINDOW` changes the observed boundary (proven by reading the window off the
   handler at the call site, not by asserting the constant), and an unparseable or non-positive value
   keeps the default and logs one startup warning.
8. `TestTasksStatusVocabularyIsExactly` is green and its comment names `AppendTaskLog` with the
   inverted guidance from section 6.7.
9. The regenerated `internal/store/tasks.sql.go` doc comment matches `query/tasks.sql`, and the diff
   contains no LF-only hunks.
10. README documents the new variable. The backlog item is closed via `/backlog close` and lives in
    `docs/backlog/closed/`.
