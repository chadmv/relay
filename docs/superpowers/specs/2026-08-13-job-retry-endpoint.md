# POST /v1/jobs/{id}/retry - operator re-run of a terminal job's tasks - Design

Date: 2026-08-13
Status: Draft (autonomous cycle; conductor review)

## Overview

One write endpoint: `POST /v1/jobs/{id}/retry?task=failed|all`. It returns a finished job's
tasks to the queue so the farm re-runs them, and it is the lone backend dependency of
[[feature-2026-07-01-job-retry-action]].

Backlog item: `docs/backlog/feature-2026-08-13-job-retry-endpoint.md`, split out of
`feature-2026-06-26-web-enabler-backend-endpoints` on 2026-08-13 when that item's two
read-only list endpoints shipped. The item carries a six-bullet constraint block accumulated
over seven weeks and states that no bullet is advisory. This spec answers every bullet, and
where a bullet's premise does not survive verification against the tree it says so with
evidence rather than designing around a claim.

This is a **fenced multi-row write on `tasks`**. Every high-severity finding in this
repository has come from a path that sidestepped the epoch fence, and the closest relative of
this endpoint - `bug-2026-06-26-retry-resurrects-cancelled-task` - took three PRs (#119, #120,
#121) to close. The design below is therefore weighted toward failing closed and toward
statements a reviewer can hold in their head, at the cost of refusing some requests a more
permissive endpoint would accept.

Backend only. One new HTTP handler, three new store statements, one one-word change to
`handleCancelJob`, two comment corrections. **No migration** (the required indexes already
exist). No frontend change; the consuming tab is a separate slice.

Written in AUTONOMOUS gate mode: every design question is decided here with a stated
rationale and the alternatives that were rejected, rather than asked.

## Decision summary

| # | Question | Decision |
|---|---|---|
| 1 | Auth gating | Owner-or-admin in the handler, 404 on deny - the cancel precedent, which is **not** what the item says it is |
| 2 | `?task` parsing | Required, exactly one value, exact match `failed` or `all`; anything else 400. No default, ever |
| 3 | `retry_count` on reopen | Reset to 0 |
| 4 | A `cancelled` job | Refuse (409). Retry requires `job.status IN ('done','failed')`. `RecomputeJobStatus` is not touched |
| 5 | Dependents that already ran | A dependent blocks unless it is `pending` **or is itself in the selected set**; guard is all-or-nothing; any exclusion is a 409 with nothing written |
| 6 | Response shape / 0 matched | 200 `{...job, "tasks_retried": N}` with N >= 1; 0 matched is a **409**, three distinguishable messages |
| 7 | Transaction + wake | One tx: lock job -> select -> update -> recompute -> `NotifyTaskSubmitted` **inside** the tx; SSE publish after commit; both gated on rows affected |
| 8 | jobs-stats `updated_at` proxy | **Scoped out and accepted in writing** - and the bug item's premise is weaker than it claims; the wrong prose at the query is corrected here |
| 9 | Concurrency | The status allow-list must stay in the UPDATE's own `WHERE`, not only in a CTE, or EvalPlanQual cannot re-check it and two retries double-bump the epoch |

## Where the backlog item is wrong, incomplete, or right

Every claim below was re-derived from the tree at HEAD. The standing project lesson is that a
backlog item's Proposal is not a contract; three of the eight findings here change the shape
of the work.

1. **WRONG, and it is the item's own framing question.** The item says "the sibling
   force-cancel is admin-only while ordinary cancel is not, so 'operator re-run' needs an
   explicit answer rather than an inherited one" (Proposal, Route). There is no admin-only
   force-cancel. `DELETE /v1/jobs/{id}` is registered `auth(http.HandlerFunc(s.handleCancelJob))`
   with no `admin(...)` in the chain (`internal/api/server.go:122`); `force` is a query
   parameter parsed by `strconv.ParseBool` inside that one handler (`internal/api/jobs.go:683`),
   and the single owner-or-admin gate at `internal/api/jobs.go:707-715` covers both graceful
   and forced cancel identically. The SPA does not gate the Force cancel button on admin
   either (`web/src/jobs/JobActions.tsx:43-59` gates on job status only). The premise that
   there are two differently-gated siblings to choose between is false; there is exactly one
   precedent for a destructive job-scoped write, and decision 1 adopts it.
2. **CORRECT - every source citation verifies.** `IncrementTaskRetryCount` is at
   `internal/store/query/tasks.sql:95-145` with its three predicates at `:141-145` and its
   "POST /v1/jobs/{id}/retry must NOT call it" note at `:126-133`. `RequeueTaskByID` is at
   `:262-273`. `RecomputeJobStatus` is at `internal/store/query/jobs.sql:89-107` and its CASE
   really is cancelled-blind (`:98` counts anything not in `('done','failed','timed_out')` as
   unfinished, and the CASE can only ever produce `running`, `done` or `failed` - never
   `cancelled`, never `pending`).
3. **INCOMPLETE in the one place that decides correctness.** Constraint 2 gives the statement
   shape as "an explicit allow-list `WHERE job_id = $1 AND status IN ('failed','timed_out')`".
   That is right, but the load-bearing detail is *where* the status predicate lives. Under
   READ COMMITTED, Postgres re-checks a blocked UPDATE's **row-level** qual against the
   updated tuple (EvalPlanQual); it does **not** re-execute CTEs or uncorrelated subplans. A
   plausible and much tidier implementation computes the selected set in a CTE and writes
   `WHERE t.id IN (SELECT id FROM selected)` - which moves the status test out of the
   row-level qual and silently destroys the concurrency property the item asks to confirm.
   See decision 9; this is the single most likely way to ship this endpoint subtly broken.
4. **INCOMPLETE - "widened for `?task=all`" needs a bound.** Widened to *what* is not stated.
   Widening to "every task" would let a retry evict an agent that is running right now. `all`
   means the terminal set and nothing else (decision 2).
5. **WRONG as worded - constraint 5 would block the normal case.** "It must not reopen a task
   whose dependents already ran" taken literally forbids the primary use case: when T fails,
   `FailDependentTasks` marks its still-`pending` dependents `failed`, so on any healthy
   failed job every failed task has failed dependents. A guard that treats those as
   "already ran" refuses every retry. The guard has to be relative to the selected set: a
   dependent that is itself being reopened by this same request does not block. The item does
   not say this and a literal implementation would be dead on arrival (decision 5).
6. **MISSING, and it is the resurrection route this whole item family descends from.** The
   item does not mention `handleCancelJob`. Retry and cancel are the only two multi-statement
   writers that touch both `tasks` and `jobs`, and today they would take their row locks in
   opposite orders (cancel: `CancelJobTasks` then `UpdateJobStatus`, i.e. tasks-then-job,
   `internal/api/jobs.go:740-748`; retry as designed: job-then-tasks). That is an ABBA
   deadlock pair, and worse, a cancel committing inside the retry's window leaves either
   (job `cancelled`, tasks `pending` - the dispatcher then runs work on a cancelled job,
   because `GetEligibleTasks` does not look at job status) or a job dragged out of
   `cancelled` by `RecomputeJobStatus`. Decision 7 makes both handlers take the job row lock
   first; this is required scope, not hardening.
7. **MISSING - retrying a task does not reset its logs.** `task_logs` has no attempt or epoch
   column (`internal/store/migrations/000001_initial.up.sql:76-82`), and nothing deletes
   rows. After a retry the task-log view shows the previous run's output followed by the new
   run's with no separator. Scoped out with a proposed follow-up item (see Scoped out).
8. **Constraint 6's premise is weaker than stated; see decision 8.** Related pre-existing
   wart, noted and *not* fixed here: `CancelJobTasks` carries a dead `'queued'` literal in
   its status list (`tasks.sql:333`) - `queued` is not in the tasks vocabulary that migration
   000019 pins. Already tracked by `docs/backlog/idea-2026-07-01-dead-status-vocabulary.md`.

## Verified facts this design rests on

- **Tasks vocabulary is exactly six values**, pinned by `tasks_status_check`
  (`000019_status_vocabulary_checks.up.sql:21-23`): `pending, dispatched, running, done,
  failed, timed_out`. There is no task-level `cancelled`; `CancelJobTasks` squashes
  cancellation onto `failed` (`tasks.sql:328-333`).
- **Jobs vocabulary is exactly five values** (`000019:12-14`): `pending, running, done,
  failed, cancelled`.
- **`assignment_epoch`** is `INT NOT NULL DEFAULT 0` (`000004_assignment_epoch.up.sql:1`).
- **`tasks.worker_id` is nullable** and every fence comparing it uses a plain `=` so a NULL
  fails closed (`tasks.sql:107-113`).
- **A terminal task keeps its `worker_id` and its epoch by design**, so trailing log chunks
  still pass `AppendTaskLog`'s fence (`tasks.sql:18-24`). Retry ends that deliberately.
- **`GetEligibleTasks` requires every dependency to be `done`** (`tasks.sql:147-157`) and
  **does not consult job status** - which is why a `cancelled` job with `pending` tasks is a
  live dispatch hazard, not a cosmetic inconsistency.
- **`ClaimTaskForWorker` requires `status='pending'`** and bumps the epoch (`tasks.sql:228-233`),
  so `dispatched` is the first status that proves a task was ever claimed.
- **Dependency edges are intra-job and acyclic**: `jobcreate` resolves `depends_on` names
  within one spec (`internal/jobcreate/jobcreate.go:107-116`), `000015_no_self_dep` forbids
  self-edges, and cycle validation ships in `jobspec` (spec 2026-06-11).
- **The agent status path holds no cross-statement locks.** `handleTaskStatus` writes through
  `h.q` on the pool - `IncrementTaskRetryCount`, `UpdateTaskStatus`, `FailDependentTasks`,
  `RecomputeJobStatus` are each autocommit (`internal/worker/handler.go:551,592,614,619`).
  `internal/scheduler/dispatch.go` likewise opens no transaction. The only multi-statement
  writers over `jobs`+`tasks` are `handleCancelJob` and this new handler.
- **Indexes needed already exist**: `idx_tasks_job_id` (`000001:97`) for the selection and
  `idx_task_deps_depends_on` (`000018:7-8`) for the dependents walk. No migration in this slice.
- **Dispatcher wake is `NotifyTaskSubmitted`** (`tasks.sql:275-278`), called inside the
  transaction on the requeue path (`internal/api/workers.go:488`) and gated on rows affected
  on the enable path (`internal/api/workers.go:551`). Postgres queues `pg_notify` payloads
  until commit, so an in-transaction call is gated on the commit as well as on the row count.

## The endpoint

### Route, method, gating

```go
// internal/api/server.go, in the Jobs block beside :122
mux.Handle("POST /v1/jobs/{id}/retry", auth(http.HandlerFunc(s.handleRetryJob)))
```

`auth(...)` only in the chain; the authorization decision is **owner-or-admin inside the
handler**, returning 404 (not 403) on deny, byte-for-byte the shape of `handleCancelJob`
(`internal/api/jobs.go:707-715`).

The block comment at `server.go:105-117` currently reads "Only the destructive cancel
(DELETE) is owner-or-admin gated." That sentence becomes false the moment this route lands.
Correcting it is in scope: wrong prose about correct code is this project's most repeated
defect class.

No request body. `?task=` is a query parameter, matching `?force=` on the sibling. `readJSON`
is therefore never called, and the live obligation from the **Single JSON entry point**
invariant is the negative one: this handler must not acquire a body reader.

### Request

| Parameter | Where | Required | Values |
|---|---|---|---|
| `id` | path | yes | job UUID; unparseable -> 400 `invalid job id` |
| `task` | query | **yes** | exactly `failed` or `all`; anything else -> 400 |

`task=failed` selects `status IN ('failed','timed_out')`.
`task=all` selects `status IN ('done','failed','timed_out')` - the terminal set, and nothing
wider. A `pending`, `dispatched` or `running` task is never reopened by either mode.

### Responses

| Status | When | Body |
|---|---|---|
| 200 | >= 1 task reopened | `{...jobResponse, "tasks_retried": N}` |
| 400 | bad job id, or `task` absent / repeated / unrecognized | `{"error": "..."}` |
| 401 | no bearer token | middleware |
| 404 | job does not exist, **or** caller is neither owner nor admin | `{"error":"job not found"}` |
| 409 | job status is not `done` or `failed` | `{"error":"job is not finished; retry is available for a done or failed job"}` |
| 409 | job is `cancelled` | `{"error":"job was cancelled; retry is not available for a cancelled job"}` |
| 409 | nothing matched the mode | `{"error":"no tasks matched task=failed; this job has no failed or timed_out tasks"}` |
| 409 | selection blocked by dependents, or raced | see decision 6 |
| 500 | db error | generic sentence, no detail |

The 200 body embeds `jobResponse` and adds one key, exactly like `disableWorkerResponse`
embeds `workerResponse` and adds `requeued_tasks` (`internal/api/workers.go:520-523`):

```go
type retryJobResponse struct {
    jobResponse
    TasksRetried int `json:"tasks_retried"`
}
```

`tasks_retried` is >= 1 on every 200 (decision 6), so a client never has to distinguish a
successful no-op from a real re-run.

## The nine decisions

### 1. Auth gating: owner-or-admin in the handler, 404 on deny

The item asks for an explicit answer rather than an inherited one, on the strength of a
premise that is false (finding 1). With that premise removed, the real precedent set is:

- Job **reads** are global to any authenticated user, deliberately - shared render-farm
  semantics, written down at `server.go:107-109`.
- The one job **write** that destroys work, `DELETE /v1/jobs/{id}` (with or without `force`),
  is owner-or-admin with a 404 on deny (`jobs.go:707-715`).
- Fleet-wide administration - workers, reservations, invites, enrollments, users - is
  `auth(admin(...))` at the route.

Retry belongs in the second class. It is job-scoped, it is destructive in the specific sense
that it discards the recorded outcome of finished tasks and re-runs them, and it consumes
farm capacity. Considered and rejected:

- **Admin-only.** Wrong on both practical and security grounds. The user who submitted a job
  is the one who wants to re-run its failed tasks; requiring an admin for the single most
  routine operator action in a render farm makes the feature useless and drives everyone to
  share an admin token, which is a net security *loss*. Admin-only is also inconsistent: the
  same user can already cancel that job and can resubmit it wholesale via `POST /v1/jobs`,
  which is strictly more capacity than a retry.
- **Any authenticated user**, matching the reads. Rejected: a read cannot destroy a recorded
  result nor consume a slot. The house already drew this line at cancel and drew it in the
  right place.

404 rather than 403 on deny follows the sibling, and `server.go:111-117` already records the
honest caveat that this is defense-in-depth rather than a true existence secret, since the
GET routes leak existence anyway. Nothing about retry changes that reasoning.

**Implementation note.** The gate is six lines and would now exist twice. Extract it as
`func (s *Server) jobOwnerOr404(w http.ResponseWriter, ctx context.Context, job store.Job) bool`
in `jobs.go`, taking a job row already fetched inside the caller's transaction, and call it
from both handlers. Duplicating an authorization gate is how the two copies diverge. This
refactor is gated on the standing rule: **every existing `handleCancelJob` test must stay
byte-identical**; an assertion that needs adjusting is a finding, not a fixup.

### 2. `?task` parsing: required, single-valued, exact match

```go
vals := r.URL.Query()["task"]
if len(vals) != 1 || (vals[0] != "failed" && vals[0] != "all") {
    writeError(w, http.StatusBadRequest,
        `query parameter "task" is required and must be exactly "failed" or "all"`)
    return
}
```

- **Absent -> 400.** The item requires no silent default and is right to. The two modes
  differ enormously in blast radius: `all` discards and re-runs successful work.
- **Empty (`?task=`) -> 400**, same message; it is not a third mode.
- **Repeated (`?task=failed&task=all`) -> 400.** `r.URL.Query().Get()` silently returns the
  first value; a client that sends both is confused about what it is asking for and must be
  told, not guessed at.
- **Exact, case-sensitive match.** `?task=Failed` is a 400. No `strconv.ParseBool`-style
  leniency: `force` gets away with it because its failure mode is "graceful instead of
  forced", while this parameter's failure mode is "re-ran everything".
- Parsing happens **before** any database work, so a malformed request costs nothing and
  leaks nothing - a 400 is returned identically for an existing and a non-existent job.

Rejected: a JSON body `{"tasks":"failed"}`. The sibling destructive action uses a query
parameter, the waiting frontend item already specifies `?task=failed|all`
(`feature-2026-07-01-job-retry-action.md:13,45`), and a body would pull `readJSON` and a
decode policy into a request with one enum in it.

Naming note: `failed` selects timed-out tasks too. Slightly loose, kept because the item, the
frontend item and spec 2026-08-12 section 11 all use that spelling and a timeout is a failure
mode. The README row states the mapping explicitly.

### 3. `retry_count` resets to 0

The reopened row gets `retry_count = 0`.

`retry_count` has exactly one behavioral consumer: `terminal && task.RetryCount < task.Retries`
in `handleTaskStatus` (`internal/worker/handler.go:550`), which decides whether the agent-side
retry budget is exhausted. Leaving the counter at its exhausted value hands the operator a
re-run with **zero** agent retries, so the new generation dies on the first transient error -
which is precisely the situation the operator pressed Retry to escape. `retries` is a
per-attempt budget in intent, and an operator re-run is a new attempt.

Cost, stated plainly: the lifetime attempt count is no longer recoverable from the row. The
only other reader is cosmetic (`taskResponse.retry_count`, `jobs.go:48`), so nothing breaks;
if lifetime accounting is ever wanted it belongs in an attempts table, not in a counter that
also gates behavior. There is no runaway risk: each retry grants at most `retries` more
agent attempts and every additional round requires a fresh operator action.

Rejected: preserve the counter (fails immediately, above); decrement or halve it (arbitrary,
unexplainable to an operator).

### 4. A `cancelled` job is refused; `RecomputeJobStatus` is not touched

**Retry requires `job.status IN ('done','failed')`.** Everything else is a 409 with a message
naming the actual status class.

The three options the item names:

- **(a) Refuse. CHOSEN.** `cancelled` is an operator's recorded decision that this work must
  not run. Un-cancelling and re-running-what-failed are different intents and must not share
  a spelling. Concretely, `CancelJobTasks` squashes cancellation onto `failed`
  (`tasks.sql:328-333`), so on a cancelled job `?task=failed` selects every task that was
  in flight when the cancel landed - retry-on-a-cancelled-job would silently mean
  "un-cancel everything", the most surprising available reading of a Retry button. It is
  also unrepairable from the row: a task that was cancelled mid-flight and a task that
  genuinely failed are indistinguishable in the schema.
- **(b) Permit, and deliberately move the job out of `cancelled`.** Coherent, but it needs a
  job-status write that contradicts a recorded operator decision, and it inherits the
  indistinguishability problem above.
- **(c) Teach `RecomputeJobStatus` about `cancelled` first.** Rejected as wildly
  disproportionate: that statement runs after *every* task status transition in the system
  (`handler.go:571,619`; `dispatch.go:370`), and getting its terminal partition wrong is
  exactly the split-brain `TestTasksStatusVocabularyIsExactly` exists to catch. Changing the
  hottest status statement in the repo to serve one new endpoint, in the same PR as a new
  fenced multi-row write, is not a trade this slice should make.

What refusal buys, stated as an invariant the reviewer can check: **because the gate admits
only `done` and `failed`, the only job-status transition this endpoint can cause is
`done|failed -> running`.** `RecomputeJobStatus` cannot be reached from `cancelled` through
this path at all, so its cancelled-blindness stays exactly as latent as it is today. That is
a stronger property than a fix to the CASE would give, and it is verifiable by reading eight
lines of handler.

`pending`/`running` jobs are also refused ("job is not finished"). Retrying failed tasks of a
still-running job is a plausible want, but it makes the dependents analysis live and racy and
`RecomputeJobStatus` would leave the job at `running` regardless; the item's title says "a
terminal job's tasks". Scoped out with a proposed follow-up.

### 5. Dependents: what "already ran" means, and what happens on exclusion

**Definitions.** `task_dependencies(task_id, depends_on_task_id)` reads "task_id depends on
depends_on_task_id", so the dependents of T are the rows with `depends_on_task_id = T.id` and
the dependent task is `td.task_id`. This is the same direction `FailDependentTasks` walks
(`tasks.sql:212-217`).

**"Already ran" = any status other than `pending`.** `pending` is the only status that proves
a task was never claimed, because `ClaimTaskForWorker` requires `status='pending'` and moves
the row to `dispatched` (`tasks.sql:228-233`). Everything else - `dispatched`, `running`,
`done`, `failed`, `timed_out` - is consistent with having executed.

**Why this predicate is written as a negation, and why that does not violate the allow-list
rule.** The house rule is that a status predicate must fail *closed* when the vocabulary
grows. Here the predicate authorizes **blocking**, not writing, so its safe direction is
inverted: `dep.status <> 'pending'` blocks on any newly added status, which is fail-closed.
The mechanically "allow-list" spelling - `dep.status IN ('dispatched','running','done',
'failed','timed_out')` - would fail *open*, letting a future status through unblocked. The
rule is about which way the failure falls, not about the syntax. This must be stated at the
statement, or a reviewer applying the rule by pattern-match will break it.

**Rejected: `started_at IS NULL` as a never-ran oracle.** It is tempting, because
`FailDependentTasks` sets only `status` and `finished_at`, so a cascade-failed dependent has
a NULL `started_at`. It does not hold: `RequeueTaskByID` and `RequeueWorkerTasks` NULL
`started_at` on tasks that definitely ran (`tasks.sql:270,344`). Recorded here so it is not
rediscovered as an improvement.

**A dependent that is itself being reopened does not block.** This is the correction to the
item's wording (finding 5). On any healthy failed job, T's dependents were cascade-failed by
`FailDependentTasks` and are therefore `failed` - and therefore in the selected set under
both modes. Reopening T and D together restores a consistent DAG, which is the whole point.
The predicate is `dep.status <> 'pending' AND dep.id NOT IN (selected)`.

**Depth: the full descendant closure, not just direct edges.** The closure is what makes the
answer a single boolean (below), and it costs one `UNION` over an index that already exists.
It is worth recording that direct edges would very nearly do, because a `dispatched` or
`running` descendant implies its entire ancestor chain was `done` at claim time, and a `done`
direct dependent that is not selected already trips the guard. The closure is kept because
"very nearly" turns on an intermediate task not having been requeued from `done` back to
`pending` in the meantime, which `RequeueWorkerTasks` can do at any moment.

**All-or-nothing, and this is the answer to "what happens when some selected tasks are
excluded".** If the guard excludes anything, **the whole request is refused and nothing is
written**. The reason is not tidiness, it is a stranding hazard:

> Let S be the selected set and X the excluded subset. Take d in S\X whose dependency p is in
> X. Applying the retry partially leaves p terminal and d `pending`. `GetEligibleTasks`
> requires every dependency to be `done`, so d is never eligible again: a permanently
> `pending` task, and a job stuck at `running` forever because `RecomputeJobStatus` counts it
> as unfinished. Silent partial exclusion converts a corrupted-DAG diagnosis into a wedged
> job.

Because the guard is all-or-nothing, it is expressible as one uncorrelated `NOT EXISTS` in
the UPDATE, evaluated once per statement. That in turn produces a free and exact
classification of the failure modes (decision 6): the guard can only ever yield "all rows" or
"no rows", so a **partial** result is proof of concurrency rather than of the guard.

**How reachable is exclusion?** On a DAG the healthy state machine produces, essentially
never: a `done` or `running` descendant of a `failed` task requires a state the dependency
rules forbid. The guard is a fail-closed assertion against a corrupted DAG and against future
statuses, not a routine branch - and that is exactly why it must be tested with both a
negative and a positive control, or a guard that blocks everything will look green.

### 6. Response shape, and the zero-match outcome is a 409

**Success is 200 with `tasks_retried` >= 1.** Never 0.

**Zero matched (`task=failed` on a job with no failed tasks) is a 409, not a 200 with 0.**
The item explicitly leaves this open. Reasons for 409:

1. It matches the sibling. `handleCancelJob` returns 409 "job is already in a terminal state"
   when the requested state change is a no-op (`jobs.go:717-720`).
2. Nothing was written, so a 200 would report success for an action that did not happen. The
   waiting frontend item wires a confirm dialog plus a three-key invalidation on success
   (`feature-2026-07-01-job-retry-action.md:46-47`); a 200-with-0 gives the operator a success
   toast and three pointless refetches for a job that did not change.
3. A specific message ("this job has no failed or timed_out tasks") is a better answer to the
   operator's actual question than a zero they must interpret.

The item's stated requirement - that a client can tell a zero from a real re-run - is
satisfied more strongly by a distinct status code than by a distinct number.

**Three distinguishable 409s, all using `writeError` so the error shape stays uniform:**

| Case | Condition | Message |
|---|---|---|
| A | `len(selected) == 0` | `no tasks matched task=<mode>; this job has no <failed or timed_out \| finished> tasks` |
| B | `len(reopened) == 0` and `len(selected) > 0` | `no tasks were reopened: a selected task has dependents that have already run, or the job changed while the request was in flight; nothing was applied` |
| C | `0 < len(reopened) < len(selected)` | `the job changed while the retry was in flight; nothing was applied - try again` |

Case C is **provably** a concurrency outcome, not a guard outcome, because the guard is
all-or-nothing. That structural argument is why no extra query is needed to classify.

Rejected: a structured error body carrying `blocked_tasks: [...]`. Every handler in the
codebase errors through `writeError` into `{"error": string}`, and the SPA's fetch wrapper
reads exactly that; inventing a second error shape for one endpoint is a bigger change than
the diagnosis is worth, and the per-task detail is one `GET /v1/jobs/{id}` away (the job
detail response already lists every task with its status). The blocked ids **are** logged
server-side on cases B and C, which is where operator-triage detail belongs.

### 7. One transaction, job row locked first, wake gated on rows and on commit

Handler order:

1. Parse `id` -> 400. Parse `task` -> 400. (No DB work yet.)
2. `tx := s.pool.Begin(ctx)`; `defer tx.Rollback(ctx)`; `q := s.q.WithTx(tx)`.
3. `job := q.GetJobForUpdate(ctx, id)` -> 404 on `pgx.ErrNoRows`, 500 otherwise.
4. `s.jobOwnerOr404(w, ctx, job)` -> 404 on deny.
5. `job.Status` gate -> 409 unless `done` or `failed` (decision 4).
6. `selected := q.SelectRetryableTaskIDs(ctx, {job_id, include_done})`; empty -> 409 case A.
7. `reopened := q.RetryJobTasks(ctx, {job_id, include_done})`;
   `len(reopened) == 0` -> 409 case B (log the selected ids);
   `len(reopened) != len(selected)` -> 409 case C (log the difference).
8. `status := q.RecomputeJobStatus(ctx, id)` -> 500 on error. By construction it returns
   `running`: at least one task is now `pending`.
9. `q.NotifyTaskSubmitted(ctx)` - **inside** the transaction.
10. `tx.Commit(ctx)` -> 500 on error.
11. `s.broker.Publish(events.Event{Type:"job", JobID:..., Data: []byte(`{"status":"running"}`)})`
    - **after** commit, matching `handleCancelJob` (`jobs.go:773-777`).
12. 200 with the recomputed job row and `len(reopened)`.

Every 4xx/5xx path returns before the commit, so the deferred rollback undoes any write. That
is what makes "no tasks were reopened" and "nothing was applied" literally true.

**Why `NotifyTaskSubmitted` goes inside the transaction.** Postgres queues `pg_notify`
payloads until commit, so an in-transaction call is gated on *both* the row count (we only
reach step 9 with `len(reopened) == len(selected) >= 1`) and on the transaction actually
committing. That is a strictly stronger form of the invariant's "gate any side effect on the
fence having matched" than a post-commit call. Precedent: the requeue path does exactly this
(`workers.go:483-495`); the enable path shows the row-count gate in its explicit form
(`workers.go:551`).

**Why the job row is locked first, and why `handleCancelJob` must change.** This is finding 6.
`GetJobForUpdate` is a new `SELECT * FROM jobs WHERE id = $1 FOR UPDATE`, used at step 3
**and** substituted for the plain `GetJob` at `internal/api/jobs.go:694`. It buys two things
that cannot be had otherwise:

- **Correct interleaving with cancel, in both orders.** Cancel-then-retry: cancel commits, the
  retry's locked read then returns a `cancelled` job and step 5 refuses it. Retry-then-cancel:
  cancel blocks at its own locked read until the retry commits, then sees a `running` job with
  `pending` tasks and cancels them properly. Without the lock on the cancel side, cancel's
  `CancelJobTasks` runs against a pre-retry snapshot, matches nothing (the tasks are still
  terminal there), and then stamps the job `cancelled` while the retry has left its tasks
  `pending` - and `GetEligibleTasks` does not look at job status, so the farm runs work on a
  cancelled job. That is `bug-2026-06-26` reproduced at the job level by a new route.
- **A single lock order.** Both handlers become job-then-tasks. Today cancel is
  tasks-then-job; adding a job-then-tasks writer without changing it creates an ABBA deadlock
  pair between two operator actions on the same job. No other code path holds two locks
  across statements (see Verified facts), so this is the complete set.

`GetJobForUpdate` is behavior-preserving for cancel in the single-threaded case, so **every
existing cancel test must stay byte-identical**; an assertion that needs adjusting is a
finding about the change, not a test to update.

`FOR UPDATE` is deliberately **not** taken on the task rows. Agents cannot write a terminal
row at all any more (the `UpdateTaskStatus` / `IncrementTaskRetryCount` allow-lists), so there
is no writer to lock out; the only competing writer is another retry, and decision 9 shows the
row-level predicate already handles that correctly and more cheaply than a lock.

### 8. The jobs-stats `updated_at` proxy: scoped OUT, accepted in writing

**Decision: this slice does not fix `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`, and the
residual inaccuracy is accepted.** The item's acceptance list permits exactly this, provided
it is in writing with a rationale. Here it is - and the premise turns out to be weaker than
either document claims.

What was verified:

- `JobStatusCounts` windows `done_24h`/`failed_24h` on `jobs.updated_at`
  (`jobs.sql:282-292`).
- Its own comment asserts "the only writer of `updated_at` is `UpdateJobStatus`". **That is
  false today, independent of this work**: `RecomputeJobStatus` also sets `updated_at = NOW()`
  (`jobs.sql:95`), unconditionally, on every call - and it is called after every task status
  transition. So `updated_at` already means "time of the last task-level event", not "time of
  the last job-status transition".
- That wider meaning does not break the proxy, because a job only *has* status `done` or
  `failed` when its last task event was the one that finished it. The proxy holds.
- **Retry does not falsify it either.** A retried job leaves both buckets the instant it
  becomes `running` (it is no longer `done`/`failed`), and re-enters the appropriate bucket
  when it finishes again, with `updated_at` equal to that new finish. There is no state in
  which a job is counted in a 24h bucket with an `updated_at` that is not its most recent
  finish.
- The only genuine change is a **transient undercount**: a job that finished 20 minutes ago
  and is retried stops counting toward `done_24h` while it re-runs. That is defensible on the
  merits - the job is running - and it self-corrects on completion.

Accepting it is therefore not accepting a regression so much as declining to act on a
prediction that does not reproduce. Keeping the fix out also keeps this slice's diff inside
one statement family; adding a `jobs.finished_at` column or rewriting the buckets onto
`MAX(tasks.finished_at)` is a migration plus a change to the dashboard's hottest query, which
deserves its own review and does not belong behind the same gate as a fenced multi-row write.

**In scope here, because wrong prose is a defect:** correct the `JobStatusCounts` comment to
name both writers of `updated_at` and to state the actual invariant that makes the proxy work
("a terminal job's last task event is the one that finished it, and a terminal task is
unwritable"), and record that retry does not violate it. That is a comment-only edit to
`jobs.sql`, and `make generate` runs for this slice anyway - with the standing hazard that the
CRLF cleanup can silently discard a regenerated doc comment. Verify `jobs.sql.go` after.

**Proposed for Phase 6, not done here:** amend the bug item's Context, which asserts the
proxy "would become inaccurate if a `POST /v1/jobs/:id/retry` endpoint is added". Per the
standing rule, backlog edits are proposed for human accept, never auto-filed. The item stays
open; its trigger condition simply did not fire.

### 9. Concurrency, stated for the multi-row form

The item asks whether `IncrementTaskRetryCount`'s single-row reasoning (`tasks.sql:99-106`)
carries over. **It does, but only if the statement is written correctly, and the natural
tidier spelling breaks it.**

Under READ COMMITTED, when an UPDATE blocks on a row another transaction is updating, it waits
for that transaction, then re-evaluates its qual against the **updated tuple** (EvalPlanQual)
and skips the row if the qual no longer holds. The crucial detail: EPQ re-checks the
**row-level** qual. It does not re-execute CTEs or uncorrelated subplans - those were
materialized from the statement's original snapshot. Therefore:

> The status allow-list must appear in the UPDATE's own `WHERE`, on the target table's
> columns. If it appears only inside a `selected` CTE and the `WHERE` reads
> `t.id IN (SELECT id FROM selected)`, then the row-level qual is id-membership only, EPQ
> re-checks nothing meaningful, and a second concurrent retry re-updates rows the first one
> already reopened - bumping `assignment_epoch` twice and resetting `retry_count` on a task
> another operator's generation may already be running.

With the predicate in the right place, the two cases the item asks for:

**Two concurrent retries.** A's UPDATE takes row locks; B blocks, then re-checks each row and
finds `status = 'pending'`, which fails the allow-list. B updates zero of A's rows. If B's mode
is a superset of A's (B=`all`, A=`failed`), B updates the strict subset A did not touch, gets
`0 < len(reopened) < len(selected)`, rolls back, and returns 409 case C. In no ordering does a
task receive two epoch bumps from two retries, and in no ordering is a partial write committed.
The endpoint is not idempotent and does not pretend to be: the second caller is told the job
changed.

**An agent reporting a terminal status concurrently with a retry.**

- *Agent commits first.* Its write is autocommit, so the retry's UPDATE sees the settled row.
  If the new status is in the mode's allow-list the task is reopened (the operator retried a
  task that had just failed - correct); if not (`done` under `?task=failed`), the row is not
  reopened, the counts mismatch, and the retry is refused with 409 case C rather than applying
  a partial. Correct.
- *Retry commits first.* The task is `pending` at epoch N+1 with `worker_id` NULL. The agent's
  late `UpdateTaskStatus` carries epoch N and its own worker id, and fails **two** of the
  statement's three predicates - epoch (N is no longer N+1) and worker (a plain `=` against a
  NULL column is never true). The status predicate would pass, since `pending` is in its
  allow-list; that is exactly why the epoch bump and the `worker_id` clear are both required
  and neither is decoration. The update matches zero rows, returns `pgx.ErrNoRows`, and
  `handleTaskStatus` drops it silently. `AppendTaskLog` drops its trailing chunks the same way,
  so no output from the dead generation appears in the live view. `IncrementTaskRetryCount` is
  rejected identically. **This is the item's "a late status update from the previous generation
  is proven dropped" criterion.**
- *A retry cannot evict a live agent at all*, in any interleaving, because the allow-list
  admits only terminal rows. That property is what substitutes for the `worker_id` predicate
  this statement cannot have (the operator has no worker identity to bind): the identity
  predicate exists to stop one actor clobbering another's live assignment, and a terminal row
  has no live assignment to clobber.

**Deliberate consequence, recorded:** NULLing `worker_id` ends the terminal task's assignment,
which is what makes trailing chunks from the previous run drop. The chunks already written stay
in `task_logs` forever - there is no attempt column - so the log view concatenates runs. See
Scoped out.

**Residual:** two retries on the same job take row locks in the same plan order, so they do not
deadlock each other; a deadlock would surface as a 500 and one aborted transaction. Low
likelihood, no mitigation beyond the single job-first lock order.

## The store statements

Three new statements in `internal/store/query/tasks.sql` and `jobs.sql`. Comments are part of
the deliverable: this file's existing comments are how the last three fence bugs stayed fixed.

### `RetryJobTasks`

```sql
-- name: RetryJobTasks :many
-- OPERATOR re-run of a terminal job's tasks: the analogue of RequeueTaskByID,
-- and explicitly NOT of IncrementTaskRetryCount. Its preconditions are the exact
-- inverse of that statement's: it reopens tasks that ARE terminal and has no
-- worker identity to bind, so that statement's status and worker predicates
-- would reject every call. See the note at IncrementTaskRetryCount.
--
-- Epoch fence: this satisfies the invariant's "conditionally end the assignment"
-- branch. The bump is inside the same UPDATE as the WHERE, so it happens only
-- for rows that actually matched - never unconditionally, which would satisfy
-- the rule vacuously.
--
-- No fence on a CALLER epoch and no worker predicate, deliberately, for the same
-- reason CancelJobTasks has none: an operator has no generation and no worker
-- identity to prove. What replaces the identity predicate is the status
-- allow-list. A terminal row has no live assignment, so there is no agent to
-- evict; a `dispatched` or `running` task is unreachable by this statement in
-- either mode.
--
-- THE STATUS ALLOW-LIST MUST STAY IN THIS WHERE CLAUSE, on tasks' own columns.
-- Do not "simplify" it to `t.id IN (SELECT id FROM selected)`. Under READ
-- COMMITTED a blocked UPDATE re-evaluates its ROW-LEVEL qual against the updated
-- tuple (EvalPlanQual); it does not re-execute CTEs. With the status test only
-- in the CTE, a second concurrent retry re-updates rows the first already
-- reopened and double-bumps assignment_epoch. This is the multi-row analogue of
-- the reasoning in IncrementTaskRetryCount's `assignment_epoch` note.
--
-- The allow-list is an ALLOW-LIST for the same reason UpdateTaskStatus's is:
-- a new status must be un-retryable until somebody decides otherwise.
-- TestTasksStatusVocabularyIsExactly names this statement.
--
-- The dependents guard is all-or-nothing by construction: the NOT EXISTS is
-- uncorrelated, so it is true for every row or for none. Partial application is
-- unrepresentable, which is what keeps a retry from stranding a reopened task
-- behind a dependency that stayed terminal. `dep.status <> 'pending'` is a
-- NEGATION on purpose: this predicate authorizes BLOCKING, so failing closed
-- means blocking on any status added later. The allow-list spelling would fail
-- open here. `pending` is the only status that proves a dependent was never
-- claimed (ClaimTaskForWorker requires status='pending').
--
-- A dependent that is itself in `selected` does not block: FailDependentTasks
-- cascade-fails a failing task's pending dependents, so on any healthy failed
-- job every selected task has failed dependents. Without the NOT IN (selected)
-- exclusion this statement would refuse every ordinary retry.
--
-- UNION (not UNION ALL) in the recursive term dedupes, so the walk terminates
-- even if a cycle is ever introduced. Edges are intra-job by construction
-- (jobcreate resolves depends_on within one spec).
WITH RECURSIVE selected AS (
    SELECT id FROM tasks
    WHERE job_id = sqlc.arg(job_id)
      AND (status IN ('failed','timed_out')
           OR (sqlc.arg(include_done)::bool AND status = 'done'))
), descendants AS (
    SELECT td.task_id AS id
    FROM task_dependencies td
    WHERE td.depends_on_task_id IN (SELECT id FROM selected)
  UNION
    SELECT td.task_id
    FROM task_dependencies td
    JOIN descendants dd ON dd.id = td.depends_on_task_id
)
UPDATE tasks t
SET status           = 'pending',
    worker_id        = NULL,
    started_at       = NULL,
    finished_at      = NULL,
    retry_count      = 0,
    assignment_epoch = t.assignment_epoch + 1
WHERE t.job_id = sqlc.arg(job_id)
  AND (t.status IN ('failed','timed_out')
       OR (sqlc.arg(include_done)::bool AND t.status = 'done'))
  AND NOT EXISTS (
        SELECT 1
        FROM descendants d
        JOIN tasks dep ON dep.id = d.id
        WHERE dep.status <> 'pending'
          AND d.id NOT IN (SELECT id FROM selected)
      )
RETURNING t.id;
```

`retry_count = 0` is decision 3, written at the statement so it is not read as a copied `SET`
clause.

The selection predicate appears twice inside this statement - once in `selected` (which exists
only to compute the exclusion) and once in the row-level `WHERE` (which is the fence). They
must stay identical; the comment says so, and a test proves the row-level copy is load-bearing
by removing it.

Implementation hazard: sqlc's analyzer has previously needed explicit aliases and fully
qualified column references to resolve names across CTEs (see the note at `AppendTaskLog`,
`tasks.sql:189-192`). If generation fails with `column reference "id" is ambiguous`, add
aliases; do not restructure the predicate to make the error go away.

### `SelectRetryableTaskIDs`

```sql
-- name: SelectRetryableTaskIDs :many
-- The UNGUARDED selection for POST /v1/jobs/{id}/retry: exactly the rows
-- RetryJobTasks would reopen if no dependent blocked it. Its only purpose is to
-- let the handler tell three outcomes apart: nothing matched the mode (empty
-- here), the dependents guard blocked everything (non-empty here, empty there),
-- and a concurrent write (a strict subset there). Do NOT add the dependents
-- guard to this statement - that collapses the second case into the first and
-- reports "no failed tasks" for a job that has several.
-- The status predicate must stay byte-identical to RetryJobTasks's; change both
-- or neither.
SELECT id FROM tasks
WHERE job_id = sqlc.arg(job_id)
  AND (status IN ('failed','timed_out')
       OR (sqlc.arg(include_done)::bool AND status = 'done'))
ORDER BY created_at;
```

### `GetJobForUpdate`

```sql
-- name: GetJobForUpdate :one
-- GetJob plus a row lock. Both multi-statement writers over jobs+tasks -
-- handleCancelJob and handleRetryJob - take this FIRST, before touching any task
-- row. Two properties depend on it, and neither is optional:
--   * A cancel and a retry on the same job serialize, in both orders. Without
--     it, a cancel whose CancelJobTasks ran against a pre-retry snapshot matches
--     nothing and then stamps the job `cancelled` over a retry's freshly
--     `pending` tasks - and GetEligibleTasks does not consult job status, so the
--     farm runs work on a cancelled job.
--   * One lock order (job, then tasks) for both handlers. handleCancelJob was
--     tasks-then-job before the retry endpoint; the two orders together are an
--     ABBA deadlock pair reachable by two ordinary operator actions.
-- No other path holds locks across statements: handleTaskStatus and the
-- dispatcher write autocommit.
SELECT * FROM jobs WHERE id = $1 FOR UPDATE;
```

### Comment edits to existing statements (no behavior change)

- `IncrementTaskRetryCount` (`tasks.sql:126-133`): its forward reference currently points at
  `docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md` and says the endpoint
  "needs its own statement". Point it at `RetryJobTasks` **by name**, now that the statement
  exists.
- `JobStatusCounts` (`jobs.sql:282-286`): correct the false "the only writer of `updated_at`
  is `UpdateJobStatus`" and state the real invariant (decision 8).
- `TestTasksStatusVocabularyIsExactly`'s doc comment
  (`internal/store/tasks_status_vocabulary_lockstep_test.go:35-47`) enumerates every site that
  partitions the tasks vocabulary and must be revisited when it moves. `RetryJobTasks` and
  `SelectRetryableTaskIDs` are two new such sites and must be added, with the note that a new
  **terminal** status probably belongs in `?task=all`'s widening and a new **non-terminal**
  status must stay out of both.

## Invariants: which apply, which do not

Read against the CLAUDE.md Invariants block.

**Apply, and how they are satisfied:**

- **Epoch fence.** `RetryJobTasks` writes `tasks.status` and takes the "conditionally end the
  assignment" branch: it bumps `assignment_epoch` and NULLs `worker_id`, predicated on the row
  having matched the status allow-list, so the bump only ever accompanies a generation that is
  actually being ended. It never calls an epoch-fenced query with a zero-value epoch (it calls
  none), and it never returns a task to `pending` without bumping. The consequence - late
  status updates and trailing log chunks from the previous generation are rejected - is the
  proof obligation in Testing.
- **Write status predicates as allow-lists.** Both new task-touching predicates are
  allow-lists on `tasks.status`. The one negation in the statement (`dep.status <> 'pending'`)
  is on the blocking side, where the negation is the fail-closed direction; that reasoning is
  written at the statement so it is not "fixed".
- **Gate any side effect on the fence having actually matched.** `NotifyTaskSubmitted`, the
  SSE publish, and the 200 all require `len(reopened) == len(selected) >= 1`; the notify is
  additionally inside the transaction, so a rollback un-sends it.
- **Single JSON entry point.** No request body; `readJSON` is not called and must not be added.
  Responses go through `writeJSON`/`writeError`.
- **Authorization is resolved server-side.** Owner-or-admin from `AuthUser` in the context; the
  caller does not name the rows it affects beyond the job id it must own.

**Do not apply, stated rather than left silent:**

- **End the generation before releasing the resource.** No async lifecycle, no stream, no
  abortable continuation. One request-scoped handler. (The database-level analogue - bump the
  epoch in the same statement that reopens the row - is honored above.)
- **Single job-spec pipeline.** No job spec is parsed, validated or created; this endpoint
  reuses existing task rows and must not grow a path that creates new ones. If a future
  "re-run as a new job" variant is wanted it goes through `jobcreate.CreateJobFromSpec` like
  every other creator.
- **One bounded sender per gRPC stream.** No gRPC surface is touched. Note that unlike cancel,
  retry sends **no** agent signal: there is no live agent to talk to (every reopened row was
  terminal), so `sendCancelSignals` has no analogue here.
- **Identity-checked teardown.** No connection state, no registry.
- **No interior pointers across locks.** No shared registry is read or written.

## Architecture

| File | Change |
|---|---|
| `internal/store/query/tasks.sql` | Add `RetryJobTasks`, `SelectRetryableTaskIDs` with their comment blocks; amend `IncrementTaskRetryCount`'s forward reference |
| `internal/store/query/jobs.sql` | Add `GetJobForUpdate`; correct the `JobStatusCounts` comment |
| `internal/api/jobs.go` | Add `handleRetryJob`, `retryJobResponse`, `jobOwnerOr404`; `handleCancelJob` uses `jobOwnerOr404` and `GetJobForUpdate` |
| `internal/api/server.go` | Register `POST /v1/jobs/{id}/retry`; correct the Jobs block comment |
| `internal/store/tasks_status_vocabulary_lockstep_test.go` | Doc comment gains the two new sites |
| `internal/store/*.sql.go` | Regenerated by `make generate`. Never hand-edited |
| `README.md` | One row in the REST table beside `:1200`, stating that `failed` includes `timed_out` and that `task` is required |
| new `internal/store/incrementtaskretrycount_guard_test.go` | Structural guard, see Testing |

No migration. No new file in `internal/api` - `handleRetryJob` belongs beside the job handlers
it shares converters and the ownership gate with, and `jobs.go` is a resource file, not a
grab-bag.

**The sqlc CRLF hazard.** `make generate` emits LF across every generated file in this CRLF
repo. Per CLAUDE.md: after generating, `git diff --ignore-all-space`, keep the real content
changes, `git checkout -- <file>` the LF-only hunks. Both `tasks.sql.go` and `jobs.sql.go`
carry real content changes here (new functions **and** edited doc comments), so neither may be
blanket-reverted, and the standing lesson applies with force: the revert has previously
discarded a regenerated doc comment, leaving generated prose contradicting its source. Re-read
both files afterward and confirm the new functions and every edited comment are present.
`models.go` must not change - no column is added.

## Security and system design

- **Threat model.** The asset is farm capacity and the integrity of recorded job outcomes. The
  capabilities granted are "spend slots" and "discard a recorded result". Both are bounded to
  jobs the caller owns (or any job, for an admin), which is the same bound cancel already has.
  A caller who can retry a job can already cancel it and resubmit it, so the endpoint adds no
  new privilege class - only a cheaper way to exercise one.
- **Enumeration.** The 404-on-deny mirrors cancel and, as `server.go:111-117` already records,
  is defense-in-depth rather than a real existence secret, because the GET routes are global.
  No new oracle: the 400s for a malformed `task` are returned before any lookup, so they are
  identical for existing and non-existent jobs.
- **Denial of service.** The realistic abuse is a loop of retries on a large job. Each call is
  bounded by one job's task count, indexed on `idx_tasks_job_id` and `idx_task_deps_depends_on`,
  and takes row locks only within that job. A retry on an unchanged job is refused (the tasks
  are `pending`, not terminal) with no write, so a tight loop after the first call is pure
  reads. No rate limiting is added, consistent with `ratelimit.go` being applied to login and
  register only (`server.go:82-94`); a per-job cooldown would be an unforced complication.
  Worth stating: the loop's *cost to the farm* is bounded by the dispatcher, not by this
  endpoint, and the operator doing it owns the job.
- **Failure modes.** A DB error at any step returns a generic 500 and rolls back; a crash
  between the task reopen and the job recompute is impossible because they share a transaction
  (the item's Transaction bullet). A crash after commit but before the SSE publish loses only a
  UI nudge; the notify already committed, so the dispatcher still wakes.
- **Load.** Two indexed statements plus a recursive walk over one job's dependency edges, in
  one short transaction holding one job row lock. The recursive term visits each descendant
  once (`UNION` dedupes). For a 1000-task job this is milliseconds; nothing about it scales
  with fleet size.
- **What an operator can still do wrong, and the endpoint does not prevent:** `?task=all` on a
  mostly-successful job re-runs the successful tasks too. That is the mode's purpose; the
  confirm dialog in the consuming frontend item is where that warning belongs.

## Testing

Integration tests are the gate. Almost nothing here is exercisable without Postgres, and
`make test` on Windows runs none of it. The real command is `make test-integration`, or
`go test -tags integration -p 1 ./internal/api/... -run TestRetryJob -v -timeout 120s`.

Plan-supplied test bodies are guesses until they have been run. Every RED-proof below must be
demonstrated RED against the stated mutation and the result recorded in the PR - a green test
that was never seen to fail proves nothing.

### Structural (no build tag, runs in the plain gate)

- **`TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath`**, modeled line-for-line on
  `internal/store/updatetaskstatusepoch_guard_test.go` (which already anticipates this endpoint
  by name at `:32-34`). Walks `internal/`, skips `_test.go` and the generated
  `internal/store/tasks.sql.go`, and fails if `IncrementTaskRetryCount` appears anywhere other
  than `internal/worker/handler.go`. This is the item's "asserted structurally rather than by
  inspection if that is cheap to do", and it is cheap. Its weakness (a rename defeats it) is
  acceptable and is the same weakness the existing guard carries.

### Store-layer integration (`internal/store`)

1. **RED PROOF - the status allow-list.** A job with one `done` and one `failed` task.
   `RetryJobTasks(include_done=false)` reopens the `failed` task and leaves the `done` task
   untouched (status, epoch and `retry_count` all unchanged). **Proven RED** by deleting the
   `t.status IN ('failed','timed_out')` conjunct from the row-level `WHERE`. This is item
   acceptance criterion 2.
2. **RED PROOF - the row-level predicate is where concurrency is decided.** Reopen a task,
   then call `RetryJobTasks` again on the same job with no other change: the second call must
   return zero rows and the task's epoch must be unchanged. **Proven RED** by rewriting the
   `WHERE` as `t.id IN (SELECT id FROM selected)` with the status test only in the CTE - the
   mutation that a well-meaning simplification would produce. Without this test that mutation
   ships green.
3. **Epoch and field semantics.** Every reopened row: `status='pending'`, `worker_id` NULL,
   `started_at` NULL, `finished_at` NULL, `retry_count=0`, `assignment_epoch` exactly
   `old + 1`. Assert the increment per row, not that it merely changed. Item criterion 3.
4. **The previous generation is dead.** Capture `(epoch N, worker W)` from a terminal task,
   retry it, then:
   - `UpdateTaskStatus{epoch:N, worker:W, status:'done'}` -> `pgx.ErrNoRows`, row still
     `pending`;
   - `AppendTaskLog{epoch:N, worker:W}` -> `pgx.ErrNoRows`, no row in `task_logs`;
   - `IncrementTaskRetryCount{epoch:N, worker:W}` -> `pgx.ErrNoRows`, `retry_count` still 0.
   Item criterion 3, second half.
5. **`?task=all` widens to the terminal set and no further.** A job with one task in each of
   the six statuses. `include_done=true` reopens exactly the three terminal ones; the
   `pending`, `dispatched` and `running` rows are untouched. This is the test that stops a
   retry from evicting a live agent, and it must exist even though decision 4's job gate makes
   the state hard to reach through the HTTP layer.
6. **Dependents guard, negative control.** T `failed`, dependent D planted `done` via the
   test-only `UpdateTaskStatusEpoch` (which exists for exactly this kind of fixture,
   `tasks.sql:298-309`). `include_done=false` returns zero rows; T is untouched. Item
   criterion 5.
7. **Dependents guard, POSITIVE control - the one that catches a guard that blocks
   everything.** T `failed` with D cascade-`failed` by `FailDependentTasks`. `include_done=false`
   reopens **both**. Without this, a guard reading `dep.status <> 'pending'` with no
   `NOT IN (selected)` exclusion passes test 6 and refuses every real retry in production.
   Tests 6 and 7 must be written together and neither is meaningful alone.
8. **Transitive guard.** A -> B -> C with A `failed`, B `failed`, C `done`. `include_done=false`
   returns zero rows (C already ran and is not selected). With `include_done=true` all three
   reopen. Proves the descendant closure, not just direct edges.
9. **All-or-nothing.** In the setup of test 8 with `include_done=false`, assert **no** row
   changed - not merely that the count was zero. A per-row guard would reopen B and strand it.

### API integration (`internal/api`)

10. **Gating.** 401 unauthenticated; 404 for an authenticated non-owner non-admin (and assert
    the job is unchanged - a 404 that still performed the write is the failure this test
    exists for); 200 for the owner; 200 for a non-owner admin. Item criterion 1.
11. **`?task` parsing.** 400 for absent, empty, `?task=Failed`, `?task=everything`, and
    `?task=failed&task=all`. Each asserts the job is unchanged.
12. **`failed` versus `all` select demonstrably different sets.** One job, one `done` and one
    `failed` task: `?task=failed` returns `tasks_retried: 1` and leaves the `done` task
    terminal; a second job identically seeded with `?task=all` returns 2. Item criterion 1.
13. **Cancelled job.** Cancel a running job, then retry -> 409, job still `cancelled`, every
    task's status and `assignment_epoch` unchanged, and no dispatch occurs. Item criterion 4
    (cancelled half).
14. **Non-terminal job.** Retry a `pending` and a `running` job -> 409 each, nothing written.
15. **Zero matched.** An all-`done` job with `?task=failed` -> 409 with the case-A message and
    an unchanged `updated_at`. Decision 6.
16. **`retry_count` reset is functional, not cosmetic.** Seed a task with
    `retries=1, retry_count=1` (exhausted) in a terminal state; retry it; assert `retry_count`
    is 0 **and** that a subsequent agent-reported failure at the new epoch is accepted by
    `IncrementTaskRetryCount` and burns a retry. Asserting the column alone would pass against
    a reset that no consumer honors. Item criterion 4 (`retry_count` half).
17. **The job status is recomputed inside the transaction.** After a successful retry the job
    is `running` and its `updated_at` moved.
18. **Dispatcher wake is gated.** On success, `NotifyTaskSubmitted` fired (observed via a
    `LISTEN` connection, the shape `internal/scheduler/notify_test.go:37-52` already uses); on
    each 409 path, it did **not**. Item requirement "a retry that matched zero tasks must not
    wake the dispatcher and must not report success."
19. **Rollback is total.** Force case C by racing two retries (or by seeding the mismatch
    directly) and assert no task row changed and the job status is untouched.
20. **Cancel/retry serialization.** Cancel and retry the same job from two goroutines released
    together; assert the end state is one of exactly two allowed states - (cancelled job, all
    tasks terminal) or (running job, tasks pending) - and never (cancelled job, pending tasks).
    This is the test that would catch the `GetJobForUpdate` change being dropped as
    "unrelated". If a timing-based interleave proves unstable, follow the precedent set by
    `2026-08-12-retry-resurrect-status-guard.md` section 7.5: prove it at the store layer with
    the exact argument values each handler captures, and record the deviation honestly.
21. **Existing cancel tests are byte-identical.** Not a new test - a gate. `handleCancelJob`
    changes only its read statement and its gate call; any existing assertion that needs
    adjusting is a finding to report, not a line to edit.

### Response shape

22. `tasks_retried` is present on every 200 and is >= 1; the body also carries the full job
    object with `status: "running"`. Key-set equality against `jobResponse` plus one key, so an
    accidentally added field fails.

## Acceptance criteria

1. `POST /v1/jobs/{id}/retry` exists, registered `auth(...)`, owner-or-admin in the handler
   with 404 on deny; `?task=failed` and `?task=all` select demonstrably different task sets.
2. `?task` is required and exact: absent, empty, repeated and unrecognized values are each 400
   with no write.
3. `RetryJobTasks` is proven RED without its row-level status allow-list (a `done` task must
   not be reopened by `?task=failed`) **and** proven RED with the allow-list moved into the CTE
   (a second concurrent retry must not re-bump the epoch).
4. `assignment_epoch` increments by exactly one for every reopened task, and a status update, a
   log chunk and a retry from the previous generation are each proven to be dropped.
5. `retry_count` resets to 0, pinned by a test that also proves the reset restores agent-side
   retry budget.
6. Retry on a `cancelled` job is refused with 409 and changes nothing; `RecomputeJobStatus` is
   unmodified.
7. A task whose dependents already ran does not reopen, and a task whose dependents were
   cascade-failed **does** - both pinned, in the same file.
8. Exclusion is all-or-nothing: no partial application is committed on any path.
9. `IncrementTaskRetryCount` is not called by the new path, asserted structurally by a guard
   test that runs in the untagged gate.
10. `NotifyTaskSubmitted` fires exactly on the success path, from inside the transaction; no
    409 path wakes the dispatcher, publishes an SSE event, or returns 200.
11. `GetJobForUpdate` is used by both `handleRetryJob` and `handleCancelJob`, and every existing
    cancel test passes byte-identical.
12. The jobs-stats interaction is accepted in writing (decision 8) and the false claim in
    `JobStatusCounts`'s comment is corrected; `bug-2026-06-05-jobs-stats-24h-updated-at-proxy`
    remains open and an amendment to its Context is *proposed*, not applied.
13. `TestTasksStatusVocabularyIsExactly`'s doc comment names both new statements.
14. README documents the route, including that `failed` covers `timed_out` and that `task` has
    no default.
15. `make test` and `make test-integration` are green; `go vet -tags integration ./...` clean;
    `make generate` output committed with LF-only hunks reverted and both regenerated files
    re-read to confirm their new functions and edited comments survived.
16. No file outside the Architecture table is modified; `web/` is untouched.
17. `feature-2026-07-01-job-retry-action` can drop its "backend-blocked" caveat - proposed as a
    backlog amendment in Phase 6, not applied here.

## Scoped out, with the enabler to propose

| Element | Why it is out | Enabler |
|---|---|---|
| The `bug-2026-06-05` jobs-stats fix | Decision 8: the predicted regression does not reproduce; the real fix is a migration plus a rewrite of the dashboard's hottest query | **None** - the item stays open; propose amending its Context with this finding |
| The frontend Retry action | Separate slice with its own item | **None** - `feature-2026-07-01-job-retry-action` exists and unblocks |
| `relay job retry` CLI subcommand | The item scopes an HTTP endpoint and a web consumer; the CLI has no retry command today | **Propose:** `feature-2026-08-13-cli-job-retry.md` (low) |
| An MCP `retry_job` tool | `internal/mcp/cancel.go` shows the shape, so it is cheap - but it is a second consumer surface with its own permission questions | **Propose:** `feature-2026-08-13-mcp-job-retry-tool.md` (low) |
| Attempt-scoped task logs | Retrying concatenates the previous run's output onto the new run's with no separator; `task_logs` has no attempt or epoch column and nothing deletes rows | **Propose:** `feature-2026-08-13-attempt-scoped-task-logs.md` (medium) - a column plus a log-view grouping, and it makes the retry feature legible |
| Retry on a `pending`/`running` job | Makes the dependents analysis live and racy; `RecomputeJobStatus` would leave the job `running` regardless | **Propose:** `idea-2026-08-13-retry-failed-tasks-of-a-running-job.md` (low) if an operator asks |
| Retry a single task (`POST /v1/tasks/{id}/retry`) | Not asked for; the same statement generalizes but the dependents guard changes meaning for a single row | **None** - recorded so it is a lookup, not a rediscovery |
| Teaching `RecomputeJobStatus` about `cancelled` | Decision 4(c): disproportionate blast radius for this slice, and refusing cancelled jobs makes it unnecessary here | **Propose:** `idea-2026-08-13-recompute-job-status-cancelled-blind.md` (low) - it stays latent, and the next endpoint that wants to write a cancelled job's tasks must confront it |
| A structured `blocked_tasks` array in the 409 body | Would be the codebase's second error shape for one endpoint; the detail is one `GET /v1/jobs/{id}` away and is logged server-side | **None** - revisit if the consuming tab needs to highlight rows |
| Per-job retry rate limiting / cooldown | A repeated retry on an unchanged job is refused with no write, so the loop is read-only after the first call | **None** |
| The dead `'queued'` literal in `CancelJobTasks` | Pre-existing, unrelated, already tracked | **None** - `idea-2026-07-01-dead-status-vocabulary` owns it |

Per the standing rule these are proposals. Phase 6 files them for human accept; nothing is
auto-filed.

## Risks

- **The CTE simplification.** Moving the status allow-list into `selected` and writing
  `WHERE t.id IN (SELECT id FROM selected)` is tidier, passes every single-threaded test, and
  silently breaks the concurrency property this endpoint's whole family exists to protect.
  Test 2 is the only thing standing in front of it, and it must be proven RED against exactly
  that mutation, not against a straw man.
- **A dependents guard that blocks everything.** `dep.status <> 'pending'` without the
  `NOT IN (selected)` exclusion refuses every ordinary retry while passing the obvious negative
  test. Tests 6 and 7 are a matched pair; shipping only the negative one is worse than shipping
  no guard, because the feature would be inert and look tested.
- **`GetJobForUpdate` being dropped as unrelated scope.** It touches a second handler, so a
  reviewer optimizing the diff will suggest removing it. Without it the endpoint has a live
  route to run work on a cancelled job and an ABBA deadlock pair with cancel. Test 20 and
  acceptance criterion 11 exist to make that visible.
- **`make test` on Windows proves nothing here.** Every behavioral test is integration-tagged.
  Only `make test-integration` is evidence. The one exception is the structural guard, which is
  deliberately untagged.
- **The CRLF revert can discard the regenerated comments.** Both `tasks.sql.go` and
  `jobs.sql.go` carry comment-only changes alongside new functions, which is precisely the
  shape that has been silently lost before.
- **`IncrementTaskRetryCount` is right there.** The cheapest failure mode, named in the backlog
  item, is an implementer who reads the title, finds the existing retry statement, and reuses
  it. Every predicate on it would reject every call, so the symptom is "the endpoint silently
  does nothing" - which a test asserting only a 200 would not catch. The structural guard test
  is the backstop.
- **Autonomous mode.** Decisions 4, 6 and 8 are product judgments (refuse cancelled, 409 on
  zero matched, accept the stats residual) made without a human. Each is reversible in a
  follow-up and each is stated with the alternative it beat, but they are the three most likely
  places the conductor will want to overrule this spec.
