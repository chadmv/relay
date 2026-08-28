# Retry bounds and the retry-budget predicate

**Date:** 2026-08-27
**Backlog item:** `docs/backlog/bug-2026-08-12-retries-unvalidated-and-budget-only-in-go.md`
**Scope confirmed by the user:** both halves, A and B.
**Status:** design, awaiting gate review.

## 1. What this closes

Two ends of the retry path, filed as one item because a 2026-08-12 audit of that path left one
defect at each end.

**A. An unvalidated input at the front.** `jobspec.Validate` bounds neither `Retries` nor
`TimeoutSeconds`. Verified at HEAD by reading the whole function: it checks `Name`, task count,
`Priority`, per-task `Name`, command form (`normalizeTaskCommands`), duplicate task names,
`depends_on` targets, dependency cycles (`detectCycle`) and `validateSourceSpec`. It never reads
either field. Migration `000001_initial.up.sql` declares `timeout_seconds INT` and
`retries INT NOT NULL DEFAULT 0`, neither with a `CHECK`.

**B. An unenforced budget at the back.** The retry budget is decided in exactly one place,
`internal/worker/handler.go`'s `terminal && task.RetryCount < task.Retries`.
`IncrementTaskRetryCount` carries exactly three predicates (`assignment_epoch`, `worker_id`,
`status IN ('pending','dispatched','running')`) and no `retry_count < retries`, so the statement
will take a task past its budget for any caller that asks.

## 2. What I refuted in the backlog item

The item is accurate on its diagnosis. It is wrong in three places, one of which would have shipped
a severe regression if implemented as written.

**R1. `POST /v1/jobs/{id}/retry` is not future work; it exists, and it already resolved the
interaction the item leaves open.** The item's Context paragraph treats the operator retry endpoint
as unbuilt and its `retry_count` decision as still open, and its final Acceptance bullet asks for a
constraint to be recorded on `feature-2026-06-26-web-enabler-backend-endpoints`. Verified at HEAD:
`internal/api/jobs.go:984` calls `SelectRetryableTaskIDs` and `internal/api/jobs.go:1002` calls
`RetryJobTasks`, which sets `retry_count = 0` under a comment naming
`terminal && task.RetryCount < task.Retries` in `handleTaskStatus` as its one behavioral consumer
and its explicit reason. So the item's conditional ("if the operator endpoint resets `retry_count`
to 0, the two stay independent") is already settled in favour of independence.
**That Acceptance bullet is retired.** It becomes a cross-reference in this spec (section 8) and no
new constraint is filed against that feature item.

**R2. "The existing `pgx.ErrNoRows` silent-drop branch already handles the rejection correctly with
no restructuring" is false, and acting on it is a severe regression.** The item offers demoting the
Go gate from "the decision" to "an early return" once the SQL predicate exists. The retry branch in
`handleTaskStatus` ends with an unconditional `return`:

```go
if terminal && task.RetryCount < task.Retries {
    if _, err := h.q.IncrementTaskRetryCount(...); err != nil {
        if errors.Is(err, pgx.ErrNoRows) { h.statusFence.record(...) } else if lim.allow(...) { log... }
    } else { updateJobStatusFromTasks(...); _ = h.q.NotifyTaskSubmitted(ctx) }
    return
}
```

Drop the `task.RetryCount < task.Retries` term and every terminal report from a budget-exhausted
task enters the branch, gets `pgx.ErrNoRows`, and **returns before `UpdateTaskStatus` ever runs**.
The task is never marked `failed`: no `finished_at`, no `FailDependentTasks` cascade, no
`RecomputeJobStatus`, no SSE frame, no `NotifyTaskCompleted`. It sits `running` until the
coordinator watchdog stamps `timed_out` at `RELAY_TASK_MAX_ASSIGNMENT` (24h default). And because
`retries` defaults to 0, *every ordinary failing task in the system* takes that path. The item's
"with no restructuring" is exactly backwards: the Go gate is load-bearing and stays.

**R3. The anti-coupling warning is aimed at the wrong audience.** The item says to state
"this cap is not `RELAY_TASK_MAX_ASSIGNMENT`" in the error message "or the next reader will try to
couple them". The next reader who might couple them is a developer editing the constant, not the
authenticated user receiving a 400. The warning belongs at the constant's declaration and in
README's configuration prose; the wire string states the bound only. See Q3.

**Two things the item and the brief both missed.**

**M1. This change is retroactive on stored scheduled-job specs, and the failure is invisible.**
`schedrunner.fireOne` re-validates the stored spec on every fire, because
`jobcreate.CreateJobFromSpec` calls `jobspec.Validate` (`internal/jobcreate/jobcreate.go:32`). A
schedule stored before this change with `retries: 50` stops firing the moment this ships, and
`TickOnce` logs one line and calls `advanceNextRun`, so `next_run_at` keeps marching and the API,
CLI and SPA all show a healthy schedule.
`docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md` describes exactly this and names
this item as its trigger; `ROADMAP.md` line 39 says "Ship the two together." This is the single
biggest risk in the slice and it is a *deployment* risk, not a code-correctness one. Section 7
handles it, and section 10 raises the sequencing decision for the human.

**M2. The half-A severity statement understates it, in one direction that matters.** The item says
an unbounded retry loop "occupies a worker slot until an operator cancels the job." There is no
backoff anywhere between a retry and its next dispatch: `IncrementTaskRetryCount` returns the task
to `pending` and `handleTaskStatus` immediately calls `NotifyTaskSubmitted`, which wakes the
dispatcher. So a deterministically-failing command (a missing binary, a permission error) fails in
milliseconds and the loop runs at full rate, producing per iteration a `ClaimTaskForWorker` UPDATE,
a dispatch, an `IncrementTaskRetryCount` UPDATE, a `RecomputeJobStatus`, a `pg_notify`, and a fresh
set of `task_logs` rows. Nothing in this repo prunes `task_logs` (stated at `AppendTaskLog`). So the
primitive is a sustained write loop against Postgres and unbounded log-table growth, not only a held
slot. That raises the value of the cap and does not change the remedy.

## 3. Scope

**In scope.**

- `jobspec.Validate` bounds `Retries` and `TimeoutSeconds`, with two named constants.
- `AND retry_count < retries` on `IncrementTaskRetryCount`, keeping the Go gate unchanged.
- The tests both halves are missing, at the layers named in section 9.
- Two README rows corrected (`tasks[].retries`, `tasks[].timeout_seconds`).

**Explicitly out of scope, each with its reason.**

- **A `CHECK` constraint on the two columns.** Declined; see Q6.
- **Retry backoff.** The absence of any delay between retries (M2) is a separate design with its own
  failure modes. Proposed as a backlog item in section 10; not decided here.
- **Per-user quotas.** The item is right that this defect is one instance of a missing-quota story.
  A cap on one field is not a quota and this spec does not pretend otherwise; section 6 states the
  residual.
- **The `last_error` surface for un-fireable schedules.** That is
  `bug-2026-08-23-unfireable-schedule-is-invisible`, and it is a sibling item, not scope creep to
  absorb here.
- **Bounding any other unvalidated field** (`Env`, `Requires`, `Labels`, task name shape). Those are
  their own items; do not widen.

## 4. The six design decisions

### Q1. The `retries` cap: **10**

`maxRetries = 10`.

Relay is a render and task farm. The failures a retry actually rescues are the ones that clear
*between dispatches*: a flaky network mount, a p4 sync that hit a transient server error, a node
that is unhealthy in a way the next node is not. With `retries: 10` a task is attempted on up to
eleven dispatches, which comfortably covers "one bad worker in the fleet" and "the mount was
unavailable for a few minutes" - the two dominant real causes.

The contended-license-server case, which is the strongest-sounding argument for a bigger number,
argues *against* it once you account for M2. There is no backoff, so N retries against a saturated
license pool are N immediate failures inside a few seconds; a large N buys no waiting, it only burns
the budget faster. The right instrument for license contention is a reservation or a semaphore
(relay has reservations), not a retry count. A cap that cannot be argued up by its best case should
not be set by that case.

Rejected:

- **5.** Defensible and probably sufficient, but it leaves no headroom for the legitimate policy
  "try this on every worker in a small pool", and the difference in blast radius between 5 and 10 is
  nil.
- **20.** The item's upper suggestion. Nothing concrete becomes possible between 10 and 20 that is
  not better served by a different feature, and it doubles the worst-case loop.
- **100 or 1000.** The entire value of the cap is that the pathological case becomes bounded in
  *operator* time. A hot loop of 1000 immediate retries is still an incident.
- **Env-configurable (`RELAY_MAX_TASK_RETRIES`).** Rejected deliberately, and this is the one that
  looks most attractive. Because `jobspec.Validate` runs on stored schedule specs at fire time (M1),
  an env-tunable bound makes retroactive schedule invalidation *environment-dependent*: the same
  stored spec fires on one replica's configuration and silently stops on another's, and lowering the
  knob disables schedules with no signal. A validation vocabulary shared by four ingest paths should
  be a property of the binary, exactly as the `priority` set is. Constant, in
  `internal/jobspec/jobspec.go`.

### Q2. Negative `retries`: **reject**

`retries: -1` is a typo class, not an expression of intent. Nobody attaches meaning to a negative
retry count, so "accept and document" buys a README sentence and a permanent obligation, in exchange
for nothing.

The obligation is the real argument. Today negatives are inert only because the comparison is
`task.RetryCount < task.Retries` with `RetryCount` starting at 0. Accepting them makes that spelling
load-bearing forever: rewrite the gate as `!=`, or write the new SQL predicate as
`retry_count <= retries - 1`, and a negative value becomes an unbounded retry loop rather than zero
retries. Rejecting at ingest removes that coupling instead of documenting it.

The bound is therefore `0 <= retries <= 10`. Zero stays valid and is the default.

### Q3. The `timeout_seconds` cap: **604800 (7 days)**

`maxTimeoutSeconds = 604800`.

The longest legitimate relay task is a full P4 sync of a workspace that can exceed 1 TB, followed by
a heavy bake, cook or render. That is plausibly 24 to 72 hours. Seven days sits comfortably above the
outer edge of plausible and far below the ~68 years that `2147483647` buys today.

**Seven days being above `RELAY_TASK_MAX_ASSIGNMENT`'s 24h default is a feature, not an oversight.**
The two knobs are independent bounds and must not be coupled. `timeout_seconds` is the task's own
execution deadline, enforced by the agent (`newRunner`) and by the watchdog's execution arm
(`ListOverdueAssignedTasks`); `RELAY_TASK_MAX_ASSIGNMENT` is the coordinator's absolute
assignment bound and sweeps the task regardless. A task whose own timeout exceeds the absolute cap is
simply swept by the other arm. Choosing a cap *below* 24h would make the two look like they agree,
which is the misreading to prevent. Choosing 7 days makes the independence visible in the numbers.

**Where the anti-coupling warning goes** (refuting the item, per R3): at the constant's doc comment,
where a developer who wants to change the number will read it, and in README's job-spec table. Not in
the 400 body. The user-facing string states the bound and nothing else. The project's recent
experience is that operator strings and prose are where the cost lands; a validation error is not the
place to hold a design argument.

Rejected:

- **86400 (24h).** Reads as coupled to `RELAY_TASK_MAX_ASSIGNMENT` and will be maintained as if it
  were. It also retroactively rejects any stored spec written for a legitimate multi-day bake, which
  is the M1 hazard at its worst.
- **2592000 (30 days).** Nothing legitimate lives between 7 and 30 days. A task that genuinely needs
  8 days needs an operator conversation, not a validator concession.
- **Deriving the cap from `RELAY_TASK_MAX_ASSIGNMENT` at runtime.** The exact coupling the item warns
  about, plus the env-dependence hazard from Q1.

### Q4. Negative `timeout_seconds`: **reject; and document that 0 and omitted both mean "no deadline"**

The bound is `0 <= timeout_seconds <= 604800`, with `nil` accepted.

Three values must stay accepted: `nil` (the documented "no deadline", the field is `*int32`), `0`
(today's second, undocumented spelling of the same thing), and any value up to the cap. Rejecting
`0` would break stored specs and clients for no benefit, so it stays.

Negatives are rejected. Today `-1` is a silent synonym for "no deadline" in two independent places
that must agree for that to be true: `newRunner` sets a deadline only `if timeoutSec > 0`
(`internal/agent/runner.go:42`), and `ListOverdueAssignedTasks`'s execution arm requires
`timeout_seconds IS NOT NULL AND timeout_seconds > 0`. Documenting the equivalence means committing
both sites, plus any third consumer, to keep agreeing forever. A third consumer is already close:
`overdueReason` computes `time.Duration(*t.TimeoutSeconds)*time.Second`, which for a negative value
yields a negative duration and a nonsense operator string. Rejecting at ingest costs one comparison
and removes the obligation.

**The `0` case is still a documentation defect and this slice fixes it.** README's job-spec table
says only "Kill task after this many seconds"; the "0 means no deadline" behaviour lives only in
`newRunner`'s doc comment. A wrong or incomplete contract in docs is a defect in this project's
terms. The row becomes explicit: omitted or `0` means no deadline.

### Q5. Half B: **add the predicate AND keep the Go gate exactly as it is**

Both, and the "both" is the whole decision. `AND retry_count < retries` goes on
`IncrementTaskRetryCount`; `terminal && task.RetryCount < task.Retries` in `handleTaskStatus` is not
touched.

**Does `RetryJobTasks` strengthen the predicate case? Yes, and in a sharper way than the item
imagined.** The item worried that a predicate would constrain the operator endpoint's `retry_count`
decision. R1 shows that endpoint exists and already zeroes `retry_count`, so the predicate constrains
nothing that is not already decided - and the argument flips into a positive one: `RetryJobTasks`'s
own comment says `retry_count = 0` is "a stated decision, not a copied SET clause", justified
entirely by the Go gate in `handleTaskStatus` being the sole consumer. That is a second statement
whose correctness argument rests on a budget check living in one Go `if`. Putting the precondition on
the statement is what stops the third such statement from getting it wrong.

**What the predicate changes about production behaviour today: nothing, provably.** For the budget
predicate to be the *sole* reason a row fails to match, `retry_count` would have to advance (or
`retries` shrink) between the `GetTask` at T0 and the UPDATE at T1 without the epoch moving. Searched
by shape across `internal/store/query/`: `retry_count` has exactly two writers,
`IncrementTaskRetryCount` (`retry_count + 1`) and `RetryJobTasks` (`retry_count = 0`), and **both
bump `assignment_epoch` in the same statement**; `retries` has no UPDATE writer at all, only the two
INSERTs. So whenever the new predicate would reject, the currency predicate rejects too, and the
rowcount is identical. The predicate is a precondition completion for a future caller, not a
behaviour change.

**That is also exactly why it is safe with respect to the fence counters, and the analysis has to be
written down because the near-miss is real.** `handleTaskStatus`'s `pgx.ErrNoRows` arm now feeds
`h.statusFence.record(classifyStatusFenceRejection(task.Status, statusStr))`. Read
`classifyStatusFenceRejection`: it returns `fenceReasonRaced` whenever the T0 row status was writable
(`pending`/`dispatched`/`running`). A budget-exhausted task at T0 is `running`. **So a
budget-exhausted rejection reaching that arm would be counted as `raced_total`** - a key whose doc
comment defines it as "something else ended the generation inside this handler's own read-to-write
window - a cancel, a grace requeue, a sibling replica, the coordinator watchdog" and describes it as
a FLOOR on concurrent-writer activity. A budget exhaustion is none of those things: it is
deterministic, single-writer, and the normal end of a task's life. Counting it there would put a
steady, agent-driven, unbudgeted increment onto an operator signal whose whole value is that it is
near zero. That is the CLAUDE.md hazard ("when a fence rejection starts feeding a counter, ask what a
peer who can move this signal gains") arriving through a change that looks like it only touches SQL.

Keeping the Go gate is what makes that unreachable: the branch is never entered when the budget is
spent, so the statement is never called, so the counter is never touched. **This is a new job for an
existing line**, which is precisely the shape this project keeps rediscovering, so it gets a test
with that job as its subject (section 9, T-B3).

Rejected:

- **Predicate, demote the Go gate to an early return.** Refuted in R2: it strands every failing task
  as non-terminal for 24h, and with `retries: 0` as the default that is every failing task.
- **Test only, leave the budget in Go.** Leaves the statement's precondition incomplete, which is the
  item's actual thesis and the one part of the 2026-08-12 audit that was left behind. It also fails
  the item's own observation that the predicate makes the store-layer test trivial to write.
- **Predicate plus a `retries` re-read in Go.** A second round trip on the gRPC recv goroutine, which
  is a standing constraint in this package. No.

### Q6. A `CHECK` constraint on `tasks.retries` / `tasks.timeout_seconds`: **no, not in this slice**

This is the highest-risk part of the item's proposal and the answer is to decline it, with the price
stated rather than the risk hand-waved.

**1. The migration can refuse to start the binary, on exactly the population that has the bug.**
Migrations are embedded and run on startup. A plain `ALTER TABLE tasks ADD CONSTRAINT ... CHECK (...)`
validates every existing row. A deployed relay whose user once submitted `retries: 2000000000` - the
item's own repro - has such a row, so the migration fails, so `relay-server` does not start. The
remedy requires direct SQL against a database the operator may not have hands on, during an outage
the upgrade caused. The scan also holds `ACCESS EXCLUSIVE` on `tasks` for its duration, which on a
large farm's task table is not free.

**2. `NOT VALID` avoids the scan and buys a worse failure.** `ADD CONSTRAINT ... CHECK (...) NOT VALID`
skips the existing-row scan, but Postgres still enforces the constraint on any *updated* row. A
pre-existing out-of-range task that is still live then fails its next `UPDATE` inside
`handleTaskStatus`, which lands in the non-`ErrNoRows` arm: one budgeted log line, no status
transition, and a task stuck until the 24h watchdog. A startup outage is loud; this is silent.

**3. The "any future writer" argument is much weaker here than it is for `tasks_status_check`.**
`tasks.status` has many writers across three packages, which is why a constraint earns its keep
there. `tasks.retries` and `tasks.timeout_seconds` have exactly two writers, `CreateTask` and
`CreateTaskWithSource`, and both have exactly one non-test caller: `internal/jobcreate/jobcreate.go`,
which calls `jobspec.Validate` first.
*The axis of that count:* Go call sites of the two sqlc-generated insert methods across every `*.go`
file in the repo, plus a case-insensitive search for `INSERT INTO tasks` across the whole tree. Every
hit outside `internal/jobcreate` is a `_test.go` file or a historical plan document; `web/e2e` writes
no tasks by raw SQL. *Axes not enumerated:* SQL executed outside the repo (an operator's `psql`), and
future migrations that `UPDATE` either column. Neither is what a `CHECK` is being proposed to catch.

**4. A numeric tuning knob is a worse lockstep obligation than a vocabulary.** Migration `000019`
already carries "This set MUST stay identical to the priority switch in `jobspec.Validate`" - an
acceptable trade for a finite, near-frozen vocabulary. A cap of 10 is far more likely to move than
`{low,normal,high}`, and each move would need a migration plus a second value kept in agreement.

**What replaces it, at a fraction of the risk.** The project's own established pattern for "this
statement must keep its single caller" is a guard test:
`TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath` fails if an identifier appears in any
non-test Go file outside one named file. Section 9 (T-A5) proposes the same shape for
`CreateTask`/`CreateTaskWithSource` outside `internal/jobcreate`. It is a compile-free, deploy-free
guard against exactly the "future writer" case, and it fails at test time rather than at startup.

If the human wants the database-level guarantee anyway, it is a separate item with its own design:
it must decide clamp-versus-fail for pre-existing rows (migration `000019` set the precedent by
running a normalizing `UPDATE` before constraining), and it must not ship in the same commit as the
validator, so a rollback of one is not a rollback of both.

## 5. The change, concretely

**`internal/jobspec/jobspec.go`.** Two constants with doc comments carrying the arguments from Q1 and
Q3 (including the "this is not `RELAY_TASK_MAX_ASSIGNMENT`" paragraph), and two checks appended to
the body of the existing first per-task loop, after `nameSet[ts.Name] = struct{}{}`, so that
command-form and duplicate-name errors keep their current precedence:

- `retries` outside `[0, maxRetries]` returns `task <name>: retries must be between 0 and 10`.
- `timeout_seconds` non-nil and outside `[0, maxTimeoutSeconds]` returns
  `task <name>: timeout_seconds must be between 0 and 604800 (0 or omitted means no deadline)`.

Error style follows the file: a named task, a stated range, `fmt.Errorf`, first problem wins.
Nil `TimeoutSeconds` is skipped, not defaulted.

**`internal/store/query/tasks.sql`.** `IncrementTaskRetryCount` gains `AND retry_count < retries`.
Its doc comment currently opens "Three predicates, each answering a different question; none is
redundant with the others and none may be deleted" and enumerates them. It becomes four, with the
fourth documented as BUDGET, plus the Q5 paragraph stating (a) that the predicate changes no
production rowcount today and why, and (b) that the Go gate must stay because a budget rejection
reaching the `ErrNoRows` arm would be counted as `raced`.

**Implementation note that has bitten this repo before.** After editing the query comment, run
`make generate`, then `git diff --ignore-all-space`, keep only the real content change, revert LF-only
hunks - and then **verify `internal/store/tasks.sql.go`'s doc comment actually changed**. The CRLF
revert has previously discarded a regenerated `.sql.go`, leaving a generated doc comment contradicting
its own source.

**`internal/worker/handler.go`.** No code change. One comment addition at the retry gate recording
that the gate now also keeps budget exhaustion off `task_status_fence`, naming the test that pins it.

**`README.md`.** Two rows in the job-spec table:

- `tasks[].retries`: "Retry up to this many times on failure (default 0, max 10)".
- `tasks[].timeout_seconds`: "Kill task after this many seconds (max 604800 = 7 days; omitted or 0
  means no deadline). Independent of `RELAY_TASK_MAX_ASSIGNMENT`, which bounds the assignment rather
  than the execution."

## 6. Load, failure modes, threat model

**Cost of the fix.** Two integer comparisons per task at validation, no allocation, no I/O. One extra
column comparison on an already-single-row, primary-key-qualified UPDATE. Nothing new on the gRPC
recv goroutine, no new query, no new lock, no new log site.

**What the bound actually buys.** The amplification factor available to one authenticated user drops
from unbounded-per-task to `tasks-per-job x 11 dispatches`. The number of tasks per job is bounded
only by the request-body limit in `readJSON`. **This is a reduction, not a quota**, and the spec says
so rather than implying the DoS is closed. The broader missing-per-user-quota story stays open and
unclaimed by this item.

**Residual, named:** no backoff between retries (M2). Ten immediate retries of a millisecond-failing
command is still a burst; it is bounded and short, where today it is neither.

**Threat model of the change itself.** The new refusals are pre-authorization-irrelevant (they run
after `BearerAuth`), carry no caller-controlled value beyond the task name already echoed by five
existing errors in the same function, and disclose only a constant compiled into the binary and
documented in README. No new counter, no new log line, so no new attacker-driven volume anywhere.

**The one behaviour a user loses:** a spec that was previously accepted is now rejected at
submission with a 400 and a clear message. That is the point. The dangerous case is the one where the
same rejection happens with no message, which is M1.

## 7. Deployment risk: retroactive invalidation of stored schedules

This is the part that needs an explicit decision rather than a mitigation.

`jobspec.Validate` is not only an ingest check. `schedrunner.fireOne` calls
`jobcreate.CreateJobFromSpec` on every fire, which re-validates the *stored* spec. Any
`scheduled_jobs` row whose spec has `retries > 10`, `retries < 0`, `timeout_seconds > 604800` or
`timeout_seconds < 0` stops producing jobs the moment this deploys. `TickOnce` logs one line and
advances `next_run_at`, and there is no `last_error` column and no failure field on
`scheduledJobResponse`, so `GET /v1/scheduled-jobs/{id}`, `relay schedules` and the SPA all show a
healthy schedule whose `last_run_at` has quietly stopped moving.

`ROADMAP.md` pairs this item with
`docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md` and says to ship them together.
**That pairing is not in the scope the user confirmed for this slice**, so it is raised as a gate
decision (section 10) rather than silently absorbed or silently ignored.

What this slice does regardless of that decision:

- **T-A6** pins the composed behaviour: a stored schedule spec that exceeds the new bound fails at
  fire time, `next_run_at` still advances, and nothing user-visible records the failure. It is
  written as a documented-hazard test with a comment naming the sibling item, so the hazard is in the
  suite rather than in prose, and so the sibling item's fix has a test to turn into a better
  assertion.
- The generous caps (Q1, Q3) are chosen partly so that the population of stored specs this can break
  is as close to "only the pathological ones" as a bound can make it.
- The retro and the PR body must state the upgrade note explicitly: operators with stored schedules
  should check for out-of-range `retries`/`timeout_seconds` before upgrading.

## 8. Invariants

- **Single job-spec pipeline.** The bound lands in `jobspec.Validate` and nowhere else, so REST
  (`internal/api/jobs.go:199`), CLI, MCP (`internal/mcp/submit.go:23`,
  `internal/mcp/schedules_write.go:45`) and schedrunner (via `jobcreate.go:32`) all inherit it. **Do
  not add a mirroring check** in `internal/api`, in `web/src/jobs/`, or in the Python SDK; the server
  stays the validator of record, as `docs/plans/2026-07-01-job-submit-form-plan.md` already requires.
- **Epoch fence.** `IncrementTaskRetryCount` stays in the "conditionally end the assignment" branch:
  the bump is inside the same UPDATE as the WHERE. A fourth predicate can only narrow the matched
  set, so the fence cannot be weakened by it. The statement keeps its identity (`worker_id`, plain
  `=`) and terminality (allow-list) predicates unchanged.
- **Fence rejection feeding a counter.** Analysed in Q5. The design's answer is that the rejection
  must never reach the counter, and the line that guarantees that is pinned by T-B3.
- **One bounded sender per gRPC stream, identity-checked teardown, no interior pointers across locks,
  single JSON entry point.** Untouched; no code on any of those paths changes.
- **Status vocabulary.** No status predicate is added, removed or widened, so
  `TestTasksStatusVocabularyIsExactly` and its named sites are unaffected.

Cross-reference (replacing the retired Acceptance bullet from R1):
`docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints` needs no new constraint. The operator
retry endpoint shipped, `RetryJobTasks` zeroes `retry_count`, and its comment already names the Go
gate as the reason. The predicate added here is consistent with that and constrains nothing further.

## 9. Acceptance criteria and the test plan

Restated from the item and sharpened. Every test below must be demonstrated RED before it is green,
against the code at HEAD.

**Half A.**

- **T-A1 (unit, `internal/jobspec`).** Table-driven, RED at HEAD. Rejects: `retries: 11`,
  `retries: -1`, `retries: 2000000000`, `timeout_seconds: 604801`, `timeout_seconds: -1`,
  `timeout_seconds: 2147483647`. Each assertion checks the error *names the offending task*, not just
  that an error occurred - a spec with two tasks, the bad one **first**, so an early-exit mutation
  cannot pass by rejecting for the wrong reason.
- **T-A2 (unit, boundary positive controls).** Accepted: `retries: 0`, `retries: 10`,
  `timeout_seconds: 0`, `timeout_seconds: 604800`, `timeout_seconds` **omitted entirely** (the `*int32`
  nil case, which the item calls out by name). These are what a mutation of either constant by one
  must break.
- **T-A3 (integration, `internal/api`).** The real REST entry point: `POST /v1/jobs` with
  `retries: 11` returns 400 and the body carries the per-task message; the same spec at `retries: 10`
  returns 201 and the created task's `retries` is 10. This is what makes the rejection more than a
  library property.
- **T-A4 (integration, `internal/cli`).** `relay submit` of an out-of-range spec against the real
  server started by `startRelayServer` surfaces the server's refusal to the user. Cheap now that the
  lane exists at HEAD, and it is the only test that proves the message reaches a human. Note the
  lane's standing rule: this is a real-server assertion, so it belongs in the integration lane and
  not behind an `httptest` fixture.
- **T-A5 (default lane, guard).** `CreateTask` and `CreateTaskWithSource` have no caller in any
  non-test Go file outside `internal/jobcreate`. Same shape as
  `TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath`. This is what stands in for the
  declined `CHECK` (Q6), so it must resolve import paths rather than matching identifiers, and it must
  be demonstrated to fail against a planted call site.
- **T-A6 (integration, `internal/schedrunner`).** The documented-hazard test from section 7.

**Half B.**

- **T-B1 (integration, `internal/store`).** RED at HEAD, and it is the item's own repro: a task with
  `retries = 1`, `retry_count = 1`, `status = 'running'`, assigned to worker W at epoch N. Call
  `IncrementTaskRetryCount` with the correct id, epoch and worker. Expect `pgx.ErrNoRows` and
  `retry_count` still 1. At HEAD this succeeds and leaves 2.
- **T-B2 (integration, `internal/store`, positive control).** Same row at `retry_count = 0`: the call
  succeeds, `retry_count` is 1, status is `pending`, `worker_id` is NULL, and `assignment_epoch` is
  N+1. This is what stops T-B1 being satisfied by a statement mutated into rejecting everything.
- **T-B3 (integration, `internal/worker`) - the one that carries Q5's new job.** A task with
  `retries = 1`, `retry_count = 1`, `running`, whose assignee reports `FAILED`. Assert all four:
  the row ends `failed`; `finished_at` is set; `retry_count` is still 1; and
  `TaskStatusFenceRejections()` is `{0,0,0}`. The counter assertion is the discriminating one - it is
  what goes RED under the item's proposed "demote the Go gate to an early return", which the other
  three would also catch but for the wrong reason. The test's name should say what it protects.
- **T-B4 (integration, `internal/worker`, positive control on the whole loop).** A task with
  `retries = 2` whose assignee reports `FAILED` three times across three generations: retried exactly
  twice, `retry_count` ends at 2, the row ends `failed`, and the dependent cascade and job-status
  recompute both fire on the third. "Exactly as many times as configured, then terminal", which is
  the item's own wording.

**Mutation battery required by the plan** (each must be shown to kill a named test, plus one control
mutation that must die, because a mutation that silently fails to apply reports as survived):

1. Delete `task.RetryCount < task.Retries` from the Go gate -> T-B3.
2. Delete `AND retry_count < retries` from the SQL -> T-B1.
3. `<` to `<=` in either -> T-B4 (a `retries: 1` task must retry once, not twice).
4. `maxRetries` 10 -> 11, and 10 -> 9 -> T-A2 and T-A1 respectively.
5. `maxTimeoutSeconds` off by one in each direction -> T-A2 and T-A1.
6. Change the `timeout_seconds` guard to reject nil -> T-A2's omitted case.

**Gates.** `make test` (all packages), `make test-integration`, `make test-cli-integration`, and
`-race` through the Linux container. If `-race` does not run, say so plainly rather than substituting
`-count=N`.

## 10. Open decisions for the human at the gate

1. **Sequencing with `bug-2026-08-23-unfireable-schedule-is-invisible`.** `ROADMAP.md` says ship the
   two together; the confirmed scope for this slice is this item only. Options: (a) ship this alone
   with T-A6 pinning the hazard and an upgrade note in the PR body - my recommendation, because the
   caps are generous enough that the affected population is close to empty and the sibling item is a
   real feature with its own migration; (b) ship the sibling first; (c) widen this slice. This is the
   only decision in the spec I am not recommending my way past without saying so.
2. **The four numbers.** `maxRetries = 10`, `maxTimeoutSeconds = 604800`, reject negatives on both.
   Each is argued in section 4; each is a product call.
3. **Proposed backlog item, not filed:** *no backoff between a failed task and its redispatch*
   (section 2, M2). Specific, high-confidence, and out of scope here. Awaiting the human's accept
   before it is written to `docs/backlog/`.
4. **Proposed backlog item, not filed:** *the database-level `CHECK` on `tasks.retries` /
   `tasks.timeout_seconds`*, if the human wants the guarantee that Q6 declines. It must carry the
   pre-existing-row decision (clamp versus fail) and must not share a commit with the validator.

## 11. Related

- Item: `docs/backlog/bug-2026-08-12-retries-unvalidated-and-budget-only-in-go.md`
- Ship-together sibling: `docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md`
- The audit that left both halves: `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md`
- The watchdog that gave `timeout_seconds` its coordinator-side consequence:
  `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md`
- The `CHECK`-constraint precedent, including its pre-constraint normalizing UPDATE:
  `internal/store/migrations/000019_status_vocabulary_checks.up.sql` and
  `docs/superpowers/specs/2026-06-20-status-vocabulary-drift-design.md`
- The lane T-A4 uses: `docs/superpowers/plans/2026-08-27-cli-real-server-integration-lane.md`
