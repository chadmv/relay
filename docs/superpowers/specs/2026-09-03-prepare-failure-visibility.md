---
date: 2026-09-03
topic: prepare-failure-visibility
status: draft
covers:
  - docs/backlog/bug-2026-09-03-prepare-failure-error-message-is-discarded.md
  - docs/backlog/feature-2026-09-03-classify-out-of-disk-p4-errors.md
  - docs/backlog/feature-2026-09-03-agent-task-lifecycle-logging.md
---

# Prepare-failure visibility: three slices

## 1. Why one spec

ROADMAP.md's Now section lists these three at its head and says the first three are independent of
one another. They are independent as *changes*, and they are not independent as a *story*: all three
answer the same operator question, which is "my P4 task failed at prepare and I cannot see why".

- Slice 1 puts the cause where an operator already looks - the task log, through the API, the CLI
  and the SPA.
- Slice 2 makes the most common cause on a render farm (a full workspace volume) say what to do
  about it instead of echoing p4.
- Slice 3 makes the same lifecycle visible on the worker host, for the case where the message never
  reached the coordinator at all.

They are specified together because two decisions cannot be made correctly slice by slice: what
stream a synthesized coordinator line lands on (slice 1), and whether the provider's own
sync-failure line should repeat the error text that slice 1 now writes (slice 3). Both are settled
below.

Gate mode for this spec was autonomous, so the questions that would have been put to the human one
at a time are answered here with a recommendation and the reasoning, and are listed again in
section 8 so they are easy to overturn.

## 2. What this batch does NOT do

Stated first, because three of these are things a reader of the backlog items will expect.

1. **It does not add a `prepare` value to `task_logs.stream`.** See 4.1; this is a migration plus
   three consumers and it is not in scope.
2. **It does not add, rename or re-scope any published counter.** `task_log_fence.counts.rejected_total`
   and the three `task_status_fence` keys keep exactly the meanings
   `internal/api/server_counters.go` and README state today. Slice 1 deliberately introduces no new
   counted arm; see 4.5.
3. **It does not close the command-argument exposure.** `Runner.sendStepMarker` already writes
   `strings.Join(argv, " ")` into `task_logs`, so a secret passed as a command argument is already
   stored verbatim and readable through the API. Slice 3's `argv[0]` narrowing bounds the **new**
   host-log surface only; it closes nothing that is open today. This needs its own backlog item and
   its own decision (redact at the agent, or refuse secrets in argv at ingest, or accept and
   document) - it is not a line change and it does not belong inside a logging slice.
4. **It does not bound total task-log volume.** The message bound in 4.4 bounds one message. The
   general per-task cap is [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]] and stays open.
5. **It does not persist `TASK_STATUS_PREPARING`.** That is roadmap item 4
   ([[feature-2026-09-03-preparing-task-status]]) and it interacts with every status allow-list in
   the tree. Slice 1 must be correct **before** and **after** that lands; see 4.6.
6. **It does not touch the em dashes in the four existing `classifyP4Error` messages.** New text
   uses hyphens; existing operator-visible strings are left alone.
7. **It does not add a "sync skipped, workspace already at baseline" progress line.** Useful, out of
   scope, mentioned so the omission is deliberate rather than forgotten.

## 3. The invariants lens, up front

The three that are load-bearing here, checked against the tree rather than recited:

- **Epoch fence.** Slice 1 adds a **new write to `task_logs`**, which the Invariants name explicitly.
  It goes through `AppendTaskLog` with the caller's epoch, the connection's authenticated worker id
  and a `MinFinishedAt` cutoff - the same four arguments `handleTaskLog` passes. It does not end an
  assignment and does not bump an epoch. The side effect (the SSE publish) is gated on the fence
  having matched, exactly as `handleTaskLog` gates it.
- **Identity is not honesty.** The write sits behind `handleTaskStatus`'s identity gate, so only the
  task's own assignee can cause it. The assignee can still lie about the content. Section 4.7 states
  what that buys an attacker and why the answer is "nothing it does not already have".
- **Single JSON entry point / single job-spec pipeline.** Untouched by all three slices.

Slices 2 and 3 add no writes, no locks and no shared state. Slice 3's host log lines run on the
runner goroutine and are bounded by dispatch rate.

---

## 4. Slice 1: the coordinator stores the agent's prepare-failure message

**Files:** `internal/worker/handler.go`, its integration tests, `README.md`.
**Item:** [[bug-2026-09-03-prepare-failure-error-message-is-discarded]] (bug, high).

### 4.0 The defect, restated from the tree

`internal/agent/runner.go` sets `ErrorMessage` on exactly two sends, both
`TASK_STATUS_PREPARE_FAILED`: line 150 (a source-bearing task on a worker with no provider) and
line 172 (`provider.Prepare` returned an error). `handleTaskStatus` maps `PREPARE_FAILED` onto the
string `"failed"` and never reads `ErrorMessage` - the identifier appears nowhere under
`internal/worker/`. The task shows `failed` and `GET /v1/tasks/{id}/logs` is empty.

**No existing test would go RED for this change.** `ErrorMessage` is set at exactly two sites in the
whole tree, both in the agent, and is read at zero. That is not a gap in the plan, it is the reason
the defect shipped, and it means the RED must be a new test written before the handler is touched.

### 4.1 The stream is `stderr`, and `prepare` is not available

The backlog item says to set `Stream` to "whatever stream name `handleTaskLog` stores for
`LOG_STREAM_PREPARE` chunks". That resolves to **`stdout`**: `handleTaskLog` maps only
`LOG_STREAM_STDERR` to `"stderr"` and everything else - `LOG_STREAM_PREPARE` included - to
`"stdout"`.

`prepare` is not merely absent, it is **forbidden**: migration `000019_status_vocabulary_checks.up.sql`
carries `task_logs_stream_check CHECK (stream IN ('stdout','stderr'))`, and
`internal/store/status_vocabulary_constraints_test.go` pins both directions of that constraint.
Writing `"prepare"` would fail at the database. Introducing it would be a migration plus
`web/src/jobs/api.ts` (the type is `'stdout' | 'stderr'`), `web/src/jobs/logBuffer.ts`
(`normalizeStream` folds anything else to `stdout`), `internal/api/tasks.go`'s `logEntry`, and the
CLI's `relay logs`, which prints the stream name verbatim. Out of scope, and if anyone wants it, it
is its own item.

So the choice is between the two legal values.

**Decision: `stderr`.** Grouping with the sync output does not depend on the stream. Both consumers
order by `seq`, not by stream: the SPA's `logBuffer` keeps one ordered line list with a per-line
stream tag and only the *partial* buffers are per stream, and `relay logs` prints one interleaved
stream. So the line lands immediately after the last prepare-progress line either way. What `stderr`
buys on top is that `web/src/jobs/LogView.tsx` renders a `stderr` row in the error colour, so the
cause is visually distinct from the sync chatter it follows, and `relay logs` prints
`[<task> stderr] [failed] ...`. Nothing in the tree filters or aggregates by stream, so there is no
consumer to break: searched `internal/api`, `internal/cli` and `web/src`, and the only uses are the
colour choice above and the CLI's verbatim print.

Because the prepare progress chunks always end their content with a newline
(`makePrepareProgressFn`'s `doFlush` joins with `"\n"` and appends one), there is never a dangling
`stdout` partial for a `stderr` line to be rendered ahead of.

**README must say the true thing:** the line lands on the **stderr** stream. Do not write "prepare".

### 4.2 Where the write goes, and why the ordering is the whole point

Inside `handleTaskStatus`, **after** the identity gate, the currency gate and the enum-to-string
mapping (both `statusStr` and `terminal` are needed), and **before the retry branch**.

"Before the terminal write" is not sufficient as an instruction, because **the retry branch
returns**. On a task with retries left, `IncrementTaskRetryCount` bumps `assignment_epoch` and
returns the task to `pending`, then `handleTaskStatus` returns without ever reaching
`UpdateTaskStatus`. An append placed after the retry branch would never run on that path, and an
append placed after the *epoch bump* could not run at all - the caller's epoch is stale the instant
the bump commits. A prepare failure that is going to be retried is exactly the case where an
operator most needs the cause of attempt N recorded. So: **one write site, above the retry branch,
covering both exits.**

Two orderings must hold and both are consequences of that position:

1. **Store before the terminal status write.** At this point the row is still non-terminal
   (`dispatched`, or `running`), so `AppendTaskLog`'s first predicate arm admits the chunk on its
   own and the trailing window is a backstop rather than the thing the feature depends on. Note the
   server never sees `preparing` today: `handleTaskStatus`'s mapping switch has no case for
   `TASK_STATUS_PREPARING` and returns on `default`, so the row stays `dispatched` for the whole
   prepare phase.
2. **Publish the log event before the status event.** The CLI's log follower stops at the terminal
   frame and the SPA's tail stops on a terminal status, so a line published after the status event
   is a line the live view never shows - it appears only on a refresh, which is the "it was empty
   and then it was not" report this item exists to end. Since the append and its publish both happen
   above the retry branch, and the status event is published near the end of the non-retry path,
   this ordering is structural rather than a rule someone has to remember. It is still worth an
   assertion (4.8) because a future refactor could move the append down.

A subscriber with `events.Filter{JobID: J, TaskID: T}` receives both frame types on one channel in
publish order (`internal/events/broker.go`'s delivery matrix, and
`TestBroker_JobAndTaskSubscriberReceivesBoth`), so the ordering is directly observable in a test.

### 4.3 Which reports carry a line

**Condition: `upd.ErrorMessage != ""` and `terminal`.**

`terminal` is the variable the function already computes (`statusStr == "failed" || statusStr ==
"timed_out"`), so the handler still does not know that `PREPARE_FAILED` exists as a special case -
which is what the backlog item's step 4 was actually protecting. This deviates from that step's
literal wording ("any status carrying a message"), and the reason is a bound:

- A terminal report **ends the generation**. It either writes the terminal status (after which the
  task is finished) or bumps the epoch through the retry path (after which the caller's epoch is
  stale). Either way the assignee cannot repeat it indefinitely at the same epoch and keep getting
  new rows, except inside the trailing window (see 4.7).
- A **non-terminal** report does not. `RUNNING` leaves `status`, `worker_id` and `assignment_epoch`
  untouched, and nothing rate-limits status messages, so `RUNNING` + a 4 KiB `ErrorMessage` would be
  an unbudgeted `task_logs` insert the assignee can repeat forever at one gRPC message per row.
- No producer sets `ErrorMessage` on a non-terminal status, and both that do are terminal. If a
  future non-terminal status needs to carry a message, that decision comes with its own bound.

**Considered and declined: also require the row read at T0 to be writable**
(`taskStatusIsWritable(task.Status)`, which already exists in this package). It would cap the write
at one line per assignment generation by refusing a duplicate terminal report. Declined because it
would give a helper that today only *labels a counter* a job as a *control*, inverting its failure
mode: today a status missing from its list mislabels a number, and as a control a missing status
would **silently drop a real prepare-failure line** - the exact hazard CLAUDE.md flags for
`AppendTaskLog`'s own allow-list, and `preparing` is the live candidate for being that missing
status. The behaviour it would prevent is cosmetic (see 4.7). Do not add it without re-deriving
that comment.

### 4.4 The message: bound, sanitisation, and the exact content

**Content:** `"[" + statusStr + "] " + message + "\n"`. For every reachable case today that is
`[failed] <cause>` followed by a newline, which is the item's shape; the generalisation costs
nothing and avoids a second decision if `timed_out` ever carries a message.

The stored content is the coordinator's own synthesis of a value the agent supplied. It must
satisfy three properties, and each has a test in 4.8:

1. **At most N bytes**, N a named package constant in `internal/worker`, on the order of 4 KiB.
   `ErrorMessage` is a proto string bounded only by gRPC's 4 MiB receive limit. The truncation
   applies to the message, not to the prefix and newline, so the constant reads as "the longest
   agent-supplied message we will store".
2. **Valid UTF-8.** Truncation at a byte offset can cut a multi-byte rune in half, and
   `task_logs.content` is Postgres `TEXT`: invalid UTF-8 is rejected at Bind
   (`invalid byte sequence for encoding "UTF8"`), which is a *real* error, not `pgx.ErrNoRows`.
   Truncate at a rune boundary, and do not rely on the input being valid - make the result valid
   regardless of what arrived.
3. **No NUL byte.** A proto3 string may legally contain `\x00`; Postgres `TEXT` may not
   (SQLSTATE 22021). Strip it. This is the only caller-reachable cause of a non-`ErrNoRows` failure
   at this call site, and removing it is what lets 4.5 not add a log line.

None of these is theoretical: `handleTaskLog`'s existing persist-failure comment names the UTF-8
case as the realistic failure it was written for.

### 4.5 The `pgx.ErrNoRows` arm: drop it, count nothing, log nothing

`AppendTaskLog` returning `pgx.ErrNoRows` means the fence refused. **Drop the chunk, do not publish
it, do not count it, do not log it, and continue to the status write.** The item suggested joining
`taskLogFenceRejects`; that is refused, and so is `statusFence`.

- **`taskLogFenceRejects` is the wrong home because its meaning is published.**
  `internal/api/server_counters.go` and README both describe `task_log_fence.counts.rejected_total`
  as chunks the fence refused **with no Go-side pre-filter**, and both say in as many words that
  this is what makes it not comparable with `task_status_fence`. The new site sits behind the
  identity gate *and* the currency gate, so folding it in would make that published sentence false.
  A counter's documented meaning is part of its contract.
- **`statusFence` is the wrong home because it partitions status-report rejections**, in three keys
  that are each a JSON key of the response, classified by `classifyStatusFenceRejection`. A log
  append is not a status report.
- **A fourth counter is not worth it, because the event is already counted one statement later.**
  `AppendTaskLog`'s fence is **strictly weaker** than the fence of the write that follows it: the
  identity, currency and task-id predicates are identical, and where `UpdateTaskStatus` and
  `IncrementTaskRetryCount` require `status IN ('pending','dispatched','running')`, `AppendTaskLog`
  accepts that **or** a `finished_at` inside the trailing window. So if the append is refused, the
  following write is refused too, and *that* refusal is recorded in `task_status_fence` with a
  reason. The converse does not hold, which is the duplicate case in 4.7.
- The intervening window cannot invert this: every statement that reopens a terminal row bumps
  `assignment_epoch`, so a row that was unwritable at the append cannot become writable at the
  caller's own epoch by the time of the status write. This is the same argument
  `classifyStatusFenceRejection`'s comment makes, including its one carve-out - the test-only
  epoch-only status write, which `internal/store/updatetaskstatusepoch_guard_test.go` keeps out of
  non-test code.

**Trap for the implementer:** that guard fails if the identifier of the test-only statement appears
in **any** non-test Go file under `internal/`. If the new comment needs to refer to it, name the
*file*, as `classifyStatusFenceRejection`'s comment deliberately does. Spelling the identifier turns
a green guard red.

A non-`ErrNoRows` error (a genuine infrastructure fault) is also dropped silently, and the comment
must say why rather than leaving it as an omission: the only caller-reachable cause is removed by
4.4's sanitisation, and a genuine fault on this connection is logged one statement later by the
`UpdateTaskStatus` (or `IncrementTaskRetryCount`) error arm, under the connection's existing budget
key. Adding a line here would mean a ninth `ingest_log_budget` key, which is a published response
contract plus a README bullet, for a condition that is already visible.

### 4.6 One definition of the fence arguments and one of the publish

There will be two call sites of `AppendTaskLog` in `internal/worker` after this slice. They must not
drift, and the arguments that could drift silently are the trailing-window resolution
(`h.TrailingLogWindow` falling back to `DefaultTrailingLogWindow`, resolved **per call**, never
cached) and the shape of the published `taskLogEvent`.

**Requirement:** exactly one definition of each, shared by both callers. Error handling stays
per-caller - `handleTaskLog` counts and logs, the new site does neither - so the shared part is the
cutoff and the publish, not the whole body. The extraction is behaviour-preserving for
`handleTaskLog` and must be gated as such: the existing `handleTaskLog` tests keep a zero-line diff
across it, including `TestHandleTaskLog_TheWindowIsReadFromTheHandlerFieldAtEveryCall` and
`TestHandleTaskLog_AZeroWindowMeansTheDefaultNotAZeroLengthWindow`, which are the two that pin the
resolution.

**Forward compatibility with `preparing`.** When [[feature-2026-09-03-preparing-task-status]] lands,
`preparing` must be added to `AppendTaskLog`'s first arm at the same time it enters the vocabulary,
or a prepare-failure line for a row sitting in `preparing` is dropped with no error and no log line.
This slice does not create that requirement - the existing prepare-progress chunks have it already -
but it doubles what is lost if it is missed, and the `preparing` spec should cite this one.

### 4.7 What an attacker gains, stated at its true size

Only the task's own assignee can reach this write. What it gains:

- **One bounded row per terminal message it sends**, for as long as the task's `finished_at` stays
  inside `RELAY_TASKLOG_TRAILING_WINDOW` (15m by default). After the window closes the fence refuses.
- **Nothing it does not already have.** The same agent can send `TaskLogChunk` messages through
  `handleTaskLog` with content of any size, on a live task, forever, subject to the identical fence.
  The new site is strictly more restricted than the channel already open beside it. The real bound
  is the missing per-task volume cap, [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]], and
  this slice does not narrow or widen it.
- **No new signal it can move.** Nothing is counted here, so there is no number whose documented
  remedy could be turned against an operator.

The visible consequence for an honest agent that repeats its terminal message: the same `[failed]`
line appears more than once in the task log, while `task_status_fence.counts.duplicate_total`
records the refused status write. That is honest, self-explanatory and expected. Say so in README
rather than engineering it away.

### 4.8 Acceptance criteria

Tests live in `internal/worker/handler_taskstatus_integration_test.go` (or a new
`handler_taskstatus_errormessage_integration_test.go` in the same lane and build tag), beside the
existing `seedTaskAndTwoWorkers` / `seedClaimedTask` helpers and the `captureLog` helper already in
`handler_tasklog_integration_test.go`. The first one is written RED at HEAD.

| # | Property | Discriminates |
|---|---|---|
| A1 | The assignee reports `PREPARE_FAILED` with a message at the current epoch: exactly one `task_logs` row exists, its content contains the message, its stream is `stderr`, and the task is `failed`. | The whole feature. RED at HEAD. |
| A2 | A `task_log` broker frame for that task is delivered **before** the `task` status frame, on one subscriber filtered on both the job and the task. | The ordering in 4.2. A publish moved below the status publish reddens it. |
| A3 | The same report from a **different registered worker**: zero `task_logs` rows, and all three `task_status_fence` counters unmoved. | The identity gate still covering the new write, and the "no new counted arm" decision. |
| A4 | A **stale epoch**: zero rows. | The currency gate. |
| A5 | A task **with a retry left**: the line is stored, the task returns to `pending` with a bumped epoch, and the line survives the requeue. | The above-the-retry-branch position. An append moved below the branch reddens it. |
| A6 | A message **longer than the bound**: exactly one row, content truncated to the bound, and the stored content is valid UTF-8 when the cut falls inside a multi-byte rune. | 4.4.1 and 4.4.2 together. Use a message whose byte at the bound is mid-rune. |
| A7 | A message containing a **NUL byte**: one row, content stored without it, no error path taken. | 4.4.3. Without the strip this is a Bind failure, not a stored row. |
| A8 | An **empty** message on a terminal report: zero rows (no blank line). | The `!= ""` condition. |
| A9 | A **`RUNNING`** report carrying a message: zero rows. | The `terminal` condition in 4.3. |
| A10 | A fence-refused append (drive it by expiring the trailing window on an already-terminal row) writes nothing, publishes nothing, logs nothing (`captureLog` returns empty) and leaves `task_log_fence` flat. | 4.5, all four clauses. |

Plus, unchanged and re-run rather than rewritten: the existing `handleTaskLog` suite, as the gate on
4.6's extraction.

**README.** Two edits, both saying the true thing:

- **Source workspaces** (the `### Source workspaces` section): when prepare fails, the provider's
  error is stored as the last line of the task's log, on the **stderr** stream, prefixed
  `[failed] `, and is readable through `GET /v1/tasks/{id}/logs`, `relay logs` and the SPA's task
  log view, live and on refresh.
- **Tasks / task log paging** (beside the `GET /v1/tasks/{id}/logs` table): note that not every log
  line comes from the subprocess - the coordinator synthesizes this one, and an agent that repeats a
  terminal message can produce it more than once.

Nothing in the `GET /v1/server/counters` bullets changes. If a diff touches them, the counter
decision in 4.5 was not followed.

### 4.9 Comment discipline

One comment at the new write site. It states the hazards the code cannot show and nothing else:

- the ordering ("above the retry branch, because that branch returns and bumps the epoch; publish
  before the status event or the live view never shows it"), naming A2 and A5 as the tests that pin
  it;
- why the `ErrNoRows` arm is silent and uncounted, in one sentence with the strictly-weaker-fence
  argument, naming the *file* of the test-only statement rather than its identifier.

Not in the comment: the census of `ErrorMessage` producers, the history of the fork, the byte
counts, or any claim about how many other call sites `AppendTaskLog` has. The bound constant carries
its own short doc: what it bounds, and that the field is agent-controlled and unbounded on the wire.

---

## 5. Slice 2: `classifyP4Error` recognises an out-of-disk failure

**Files:** `internal/agent/source/perforce/diagnostics.go`, `diagnostics_test.go`.
**Item:** [[feature-2026-09-03-classify-out-of-disk-p4-errors]] (feature, low).

### 5.1 The match

One new `case` in the existing `switch`, matching against `msg`, which the function has already
lower-cased. Four substrings:

| Substring | Source |
|---|---|
| `no space left on device` | Linux `ENOSPC` |
| `not enough space` | Windows, which phrases it `There is not enough space on the disk` |
| `disk full` | p4d and several filesystem layers |
| `insufficient disk space` | p4 client-side check |

The item lists `there is not enough space on the disk` **and** `not enough space` as separate
substrings. The first contains the second, so matching both is dead weight; match `not enough space`
and keep the full Windows sentence as a **test case** so the coverage claim is still honest.

**Match the phrase, never the words.** The fork matched `insufficient` and `space` as two
independent substrings. `workspace` contains `space`, so "insufficient permissions on workspace"
would be reported to an operator as a full disk, sending them to free disk space on a machine whose
disk is fine while the real cause is a permissions problem. This is the reason the negative tests in
5.3 exist; the positive cases alone pass under the fork's version and prove nothing.

### 5.2 The message

```
out of disk space on this agent's workspace volume - free space, raise
RELAY_WORKSPACE_MIN_FREE_GB so the sweeper evicts idle workspaces sooner, or reduce the sync
paths: %w
```

- Hyphen, not an em dash. The four existing messages carry em dashes; do not copy them and do not
  fix them here.
- `RELAY_WORKSPACE_MIN_FREE_GB` exists (`cmd/relay-agent/main.go`, documented in README's
  **Eviction** bullets as the free-disk threshold below which the sweeper evicts LRU workspaces), so
  the remedy names a real knob. The item's wording ("let the sweeper evict idle workspaces
  (RELAY_WORKSPACE_MIN_FREE_GB)") does not say which direction to move it; **raise** is the
  direction that causes eviction, and an operator reading a remedy under time pressure should not
  have to derive that.
- No studio-specific path, product name or hostname.
- `%w` preserves the original, as all four existing cases do.
- Do not mention sync-path exclusions. That feature does not exist
  ([[idea-2026-09-03-sync-spec-exclusion-paths-design]]); "reduce the sync paths" is achievable
  today by editing the spec.

### 5.3 Acceptance criteria

Table-driven, in the existing `TestClassifyP4Error` table in `diagnostics_test.go`, which already
carries the `errors.Is` passthrough and wrapping assertions:

- One positive case per phrasing: the Linux `ENOSPC` text, the full Windows sentence
  `There is not enough space on the disk.`, a `disk full` phrasing, an `insufficient disk space`
  phrasing. Each is rewrapped with the remedy and still satisfies `errors.Is(got, in)`.
- Case-insensitivity is exercised by giving at least one positive case in the capitalisation p4
  actually emits, since the classifier lower-cases before matching.
- **Negative:** `File(s) not in client view.` (already there), `workspace not found`, and
  `insufficient permissions on workspace`. Each must pass through **unchanged**, asserted by
  `errors.Is(got, in)` on the same pointer, which is how the existing passthrough arm is written.
- No studio-specific string appears in the new message. This is checkable by eye in review; it does
  not need a test.

Slice 2 has no README change: `classifyP4Error`'s messages are not documented anywhere.

### 5.4 Comment discipline

At most one line above the new case, stating the constraint the code cannot show: match the phrase,
not the words, because `workspace` contains `space`, and naming the negative test that pins it.
Nothing about the fork, nothing about how many cases exist.

---

## 6. Slice 3: the agent says what it is doing

**Files:** `internal/agent/runner.go`, `internal/agent/source/perforce/perforce.go`, and tests in
both packages.
**Item:** [[feature-2026-09-03-agent-task-lifecycle-logging]] (feature, low).

### 6.1 Host log lines: four, in `Runner.Run`

Today `Run` logs on exactly two conditions, both in the finalize defer: a skipped finalize on a
forced cancel, and a finalize failure. Between accepting a dispatch and finalizing, the host log is
silent.

Four new `log.Printf` calls, following the existing `runner: ` prefix and carrying the task id:

1. Immediately before `provider.Prepare`: preparing workspace.
2. On the `Prepare` error, before the `PREPARE_FAILED` send: the error text. This is the line whose
   whole purpose is that it survives when the send does not.
3. Before each `exec.CommandContext`: the step index, the step total, and **`argv[0]` only**.
4. After `cmd.Wait` returns for each step: the step index, the exit code and the wait error.

The no-provider `PREPARE_FAILED` at `runner.go:150` returns before line 1's position; give it its
own line or accept that it is covered by the coordinator-side line from slice 1. Either is fine;
state which in the plan.

**Volume.** These are per task and per step, bounded by how fast the coordinator dispatches to this
agent. Line 2's text derives from p4 output, which is influenced by whoever can write to the depot;
it is one line per task attempt on the agent's own host log, which is an acceptable price for the
only record that survives a lost connection.

### 6.2 The `argv[0]` narrowing, and what it does not close

**Log the step index and `argv[0]`, never the full `argv`.** The fork logs the whole vector. Nothing
in relay sanitises command arguments - the per-task identity slice strips reserved *environment*
names and says nothing about argv - so a token passed as an argument would land in the host log
verbatim.

**The narrowing bounds the new surface only. It closes nothing.** `Runner.sendStepMarker` already
writes `"=== relay step i/n === " + strings.Join(argv, " ")` into the task log stream, so that same
token is already in `task_logs`, verbatim, and readable through `GET /v1/tasks/{id}/logs`,
`relay logs` and the SPA. A spec that implied otherwise would be worse than silent. The residual
exposure needs its own item and its own decision; see section 2, point 3. Do not fix it here, and do
not write a comment claiming the narrowing protects anything but the host log.

### 6.3 Provider lifecycle lines go through `progress`, and carry no error text

The fork adds sync start/complete/failed lines inside the Perforce provider as `log.Printf`. Route
them through the `progress` callback instead: `makePrepareProgressFn` batches them into
`LOG_STREAM_PREPARE` chunks, which reach `task_logs` and therefore the API and the SPA, not only the
host.

**Three lines, one each, no per-file output** (that is
[[feature-2026-09-03-p4-sync-progress-heartbeat]]'s job), placed around the `SyncStream` call inside
the `if needsSync` block in `Provider.Prepare`:

- before the call: sync starting, with the number of paths;
- after a successful return: sync complete;
- on the error path, before `classifyP4Error` and the return: sync failed, **with no error text**.

The last one is the cross-slice decision. The error the provider is about to return travels to
`Runner.Run`, becomes `ErrorMessage`, and slice 1 writes it into the same task log as
`[failed] <cause>` - carrying the classified, wrapped text, including slice 2's disk-full remedy.
Repeating it in the progress line would put the cause in the log twice, in two spellings (raw versus
classified), with nothing saying which is authoritative. The progress lines are **brackets around
the p4 output**; the cause has exactly one home.

Ordering works out without any new mechanism: `flushProgress()` runs after `Prepare` returns and
before the `PREPARE_FAILED` send, and `sendCh` is FIFO, so the coordinator stores the sync lines,
then the `[failed]` line, then the terminal status.

`progress` is dereferenced unconditionally today (the crash-recovery branch already calls it), and
the only production caller passes a non-nil function. The new calls add no assumption that is not
already there. If a planner wants that precondition stated on `source.Provider`, it is a one-line
doc edit, not a guard, and not required by this slice.

### 6.4 Acceptance criteria

**In `internal/agent`** (default lane), capturing the log with `log.SetOutput` and restoring it in
`t.Cleanup`. Measured: **no test in `internal/agent` calls `t.Parallel`** - zero occurrences in the
package - so a process-global log capture is safe today. The new tests must not add `t.Parallel`,
and the restore must be unconditional. The package already has `fakeProvider` in `runner_test.go`
for driving a prepare failure.

| # | Property | Discriminates |
|---|---|---|
| B1 | A command `["tool", "--token", "SECRET"]` produces a step line containing `tool` and **not** containing `SECRET`. | The narrowing. Written FIRST; it is the one the item exists to carry. |
| B2 | A provider whose `Prepare` returns an error produces a host log line containing that error's text. | Line 2 of 6.1. |
| B3 | A successful multi-step run produces one start line and one exit line per step, with the right indices. | Lines 3 and 4. |

**In `internal/agent/source/perforce`** (default lane): the package has a `fakeRunner` fixture
(`fixtures_test.go`, `newFakeP4Fixture`) and `perforce_test.go` already drives `Prepare` through it
without p4d, so:

| # | Property | Discriminates |
|---|---|---|
| B4 | A `Prepare` that syncs emits exactly one start and one complete progress line, and no per-file line. | 6.3's shape and the "one line each" bound. |
| B5 | A `Prepare` whose `SyncStream` fails emits a failure progress line that does **not** contain the error text, and returns the classified error. | The no-duplicate-cause decision. If someone puts the error back into the line, this reddens. |

**README:** no change is required by slice 3. The host log is not a documented interface. If the plan
adds one anyway, it must not claim the narrowing protects secrets from the task log.

### 6.5 Comment discipline

- At the step-line site: one sentence saying `argv[0]` only and why - arguments are unsanitised -
  naming B1. It must **not** claim this keeps secrets out of the log generally; `sendStepMarker` is
  four lines away doing the opposite.
- At the provider progress sites: one sentence saying the cause is deliberately not repeated here
  because the coordinator stores it from `ErrorMessage`, naming B5.
- Nothing about the fork, the rebase, or how many lines were ported.

---

## 7. Lane independence

| Slice | Files |
|---|---|
| 1 | `internal/worker/handler.go`, `internal/worker/*_integration_test.go`, `README.md` |
| 2 | `internal/agent/source/perforce/diagnostics.go`, `diagnostics_test.go` |
| 3 | `internal/agent/runner.go`, `internal/agent/*_test.go`, `internal/agent/source/perforce/perforce.go`, `perforce_test.go` |

**Slice 1 is independent of the other two** at the file level and at the test level. Its gate is
`make test` plus the `internal/worker` integration lane; nothing it touches is compiled by the agent
packages. It can run in its own worktree, in parallel, and it should land first - it is what makes
every later Perforce failure diagnosable.

**Slices 2 and 3 are NOT independent of each other.** They touch different *files*
(`diagnostics.go` versus `perforce.go`) but the **same Go package**, so they share a test binary and
a compile unit: two agents editing them concurrently in one worktree race on the package's build and
test state, and either one's red compile blocks the other's gate. They must share a lane, or run in
separate worktrees and be sequenced at integration.

**Order within that lane: 2, then 3.** Slice 2 is small and self-contained, and its improved message
is what slice 3's failure path ends up delivering to the operator, so landing it first means the
combined behaviour is exercised once rather than assembled at the end.

**Cross-slice interaction to hold in mind while reviewing slice 3:** the `[failed]` line is slice
1's, the `[sync] ...` brackets are slice 3's, and the decision that only one of them carries the
cause (6.3) is invisible in either diff alone. A reviewer looking at slice 3's diff will see a
failure line with no error in it and want to add one. It is deliberate; B5 is the guard.

---

## 8. Decisions a human may want to overturn

Autonomous gate mode, so these were decided rather than asked. Each is cheap to reverse before
implementation and expensive after.

1. **`stderr` rather than `stdout` for the synthesized line** (4.1). Reversing costs one constant and
   two README words. Choose `stdout` if you want the line to look like ordinary prepare output;
   choose `stderr` (recommended) if you want it to render in the error colour.
2. **Terminal reports only** (4.3), rather than the item's "any status carrying a message". Reversing
   widens an unbudgeted write path to the `RUNNING` message; do not reverse without a bound.
3. **No T0-writability pre-filter** (4.3), so a repeated terminal message repeats the line inside the
   trailing window. Reversing means giving `taskStatusIsWritable` a control job and rewriting its
   comment.
4. **No counter on the fence-rejection arm** (4.5). Reversing means either changing a published
   counter's documented meaning or adding a section to `GET /v1/server/counters`.
5. **The provider's sync-failure line carries no error text** (6.3). Reversing puts the cause in the
   log twice.

## 9. What was refuted in the handed-down items

Read once each for self-contradiction, contradiction with the tree, and prescriptions for things
that do not exist.

1. **Item 1, step 1 is wrong about the stream, and the correction is larger than a wording fix.**
   It says to use "whatever stream name `handleTaskLog` stores for `LOG_STREAM_PREPARE` chunks",
   which resolves to `stdout`, and step 5 then says to "name the stream it lands on" in README - so
   following the item literally would have produced a README sentence saying `prepare`. Worse,
   `task_logs.stream` carries a CHECK constraint admitting only `stdout` and `stderr`
   (migration `000019`), pinned by `internal/store/status_vocabulary_constraints_test.go`, so
   `prepare` is not merely unused, it is unwritable. Resolved in 4.1: `stderr`, with the migration
   and its three consumers declared out of scope.
2. **Item 1, step 2 prescribes the wrong counter.** `taskLogFenceRejects` is published as
   `task_log_fence.counts.rejected_total`, whose documented meaning in both
   `internal/api/server_counters.go` and README rests on that arm having **no** Go-side pre-filter.
   The new site sits behind two gates. Resolved in 4.5: count nothing, with the
   strictly-weaker-fence argument for why nothing is lost.
3. **Item 1's "before the terminal write" is not sufficient as stated**, though its reasoning is
   sound. The retry branch returns and bumps the epoch, so an append after it never runs on the
   retry path and cannot run after the bump at all. Resolved in 4.2.
4. **Item 1 omits sanitisation entirely.** Truncating an `ErrorMessage` at a byte offset can produce
   invalid UTF-8, and a proto3 string may carry a NUL; both are Postgres `TEXT` failures and neither
   is `pgx.ErrNoRows`. Resolved in 4.4.
5. **Item 1's test list is right but its premise about existing tests is not checkable from the
   item.** `ErrorMessage` is set at two sites in the tree and read at none, so there is no existing
   test to turn RED - the RED must be new. Stated in 4.0.
6. **Item 2 lists a redundant substring.** `there is not enough space on the disk` contains
   `not enough space`. Kept as a test case, dropped as a match. Its central claim - the `workspace`
   contains `space` trap - is correct and is the reason the negative tests exist. Verified that
   `RELAY_WORKSPACE_MIN_FREE_GB` exists in `cmd/relay-agent/main.go` and is documented in README.
   Its remedy wording omits the direction to move the knob; corrected to "raise" in 5.2.
7. **Item 3's rationale for the `argv[0]` narrowing is incomplete in a way that matters.** The
   narrowing is correct; the implied benefit is not. `sendStepMarker` already writes the full argv
   into `task_logs`, so the secret is already stored and API-readable. Resolved in 6.2 and in
   section 2, point 3: the narrowing bounds the new host-log surface only, and the residual exposure
   gets its own item.
8. **Item 3's test hedge is inaccurate.** It says the package's tests "run in parallel only where
   they already do"; measured, `internal/agent` contains **zero** `t.Parallel` calls, so the global
   `log.SetOutput` capture is unconditionally safe today. Restated as a constraint in 6.4: the new
   tests must not introduce the first one.
9. **Item 3's rebase note is stale-shaped but harmless.** It claims only one commit has touched
   `runner.go` since the fork's base. That is a claim about history that this spec does not depend
   on and that nothing in the tree can confirm; the slice is specified against HEAD, so the note is
   simply unused.

**What was checked and found correct:** the two `PREPARE_FAILED` sends and their `ErrorMessage`
values (`runner.go:150`, `runner.go:172`); that `handleTaskStatus` maps `PREPARE_FAILED` onto
`failed`; that `handleTaskLog` resolves the trailing window from `h.TrailingLogWindow` with
`DefaultTrailingLogWindow` as the fallback and passes an absolute `MinFinishedAt`; that the row is
non-terminal during prepare because `TASK_STATUS_PREPARING` falls to the mapping switch's `default`
and returns; that `AppendTaskLog`'s first arm admits `dispatched`; that a single broker subscriber
filtered on job and task receives both frame types in publish order; that `LOG_STREAM_PREPARE` and
`TASK_STATUS_PREPARING` are both in `proto/relayv1/relay.proto`; that `classifyP4Error` has exactly
the four documented cases and returns the original unchanged on `default`; that
`RELAY_WORKSPACE_MIN_FREE_GB` and `relay workers evict-workspace` both exist; that `progress` is
already dereferenced unconditionally in `Prepare`'s recovery branch; and that the Perforce package
has a `fakeRunner` fixture that drives `Prepare` without p4d.

## 10. Open question

One, and it does not block implementation of any slice.

**Should the no-provider `PREPARE_FAILED` at `runner.go:150` get its own host-log line?** It returns
before the position of slice 3's first line, so under 6.1 it produces no host record, only the
coordinator-side line slice 1 stores. The tree does not settle it: it is a configuration error on
the worker (no `RELAY_WORKSPACE_ROOT`, or a failed p4 preflight), which argues for a host line since
the operator fixing it is standing at that host - but it is also the one prepare failure that is
identical on every task the worker accepts, which argues for not repeating it per task. Either
answer is defensible; the plan should pick one and say which.
