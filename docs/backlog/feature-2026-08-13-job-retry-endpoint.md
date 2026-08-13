---
title: "POST /v1/jobs/{id}/retry - operator re-run of a terminal job's failed or all tasks"
type: feature
status: open
created: 2026-08-13
priority: medium
source: split out of feature-2026-06-26-web-enabler-backend-endpoints during the 2026-08-13-web-enabler-list-endpoints slice
---

# POST /v1/jobs/{id}/retry - operator re-run of a terminal job's failed or all tasks

## Summary

The third endpoint of [[feature-2026-06-26-web-enabler-backend-endpoints]], split into its own item
because it is a fenced multi-row write on `tasks` that shares nothing with the two read-only list
endpoints that shipped alongside it. It re-runs a terminal job's tasks, selected by
`?task=failed|all`, and it is the lone backend dependency of [[feature-2026-07-01-job-retry-action]].

The parent item's Notes sanctioned this split in advance ("Three independent endpoints under one
item for scheduling convenience; split later if one grows"). **This file carries the parent's entire
accumulated constraint block, because that block is the whole reason the split was safe.** Do not
treat any bullet below as advisory.

## Context

Per-task retry already exists agent-internally: when a task's current generation fails, the server
burns one retry and returns it to the queue through `IncrementTaskRetryCount`
(`internal/store/query/tasks.sql:95-145`). That is an **agent-driven** retry and its preconditions
are the exact inverse of an operator's. The two operations were only ever conflatable because
neither had a stated precondition; both now do.

The endpoint is a write inside the CLAUDE.md epoch fence, which is where every high-severity finding
in this repository has come from. It needs its own spec.

## The constraint block (carried from the parent item, plus one addition)

1. **It must NOT call `IncrementTaskRetryCount`.** That statement fences on three predicates -
   `assignment_epoch`, `worker_id`, and `status IN ('pending','dispatched','running')`
   (`internal/store/query/tasks.sql:141-145`) - which are the exact inverse of this endpoint's
   preconditions: it reopens tasks that **are** terminal and has no worker identity to supply, so
   both the status and the worker predicate would reject every call. That is the correct outcome,
   not an obstacle. The statement says so itself, by name, at
   `internal/store/query/tasks.sql:126-133`, which is the citation to read before writing any code.

2. **It needs its own statement, the operator analogue of `RequeueTaskByID`**
   (`internal/store/query/tasks.sql:262-273`), not of `IncrementTaskRetryCount`. Shape: an explicit
   allow-list `WHERE job_id = $1 AND status IN ('failed','timed_out')` for `?task=failed`, widened
   for `?task=all`, that sets `status='pending'`, NULLs `worker_id`, clears `started_at` and
   `finished_at`, and **bumps `assignment_epoch`**. The epoch bump is what satisfies the invariant's
   "conditionally end the assignment" branch, and it must be predicated on the generation actually
   being ended - never unconditional, or the rule is satisfied vacuously. Write the status predicate
   as an **allow-list, never the equivalent deny-list**: the two are interchangeable against today's
   vocabulary, but a deny-list fails open on the next status added.
   `TestTasksStatusVocabularyIsExactly` goes RED when the vocabulary moves, and this statement must
   be one of the places that revisits.

3. **It must decide explicitly what happens to `retry_count`.** Leaving it at its exhausted value
   gives the reopened task **zero** agent-side retries on the new generation, which is almost
   certainly not what an operator pressing Retry expects. Resetting to 0 is the likely intent. Either
   way it must be a stated decision in the spec with its rationale, not a side effect of whichever
   `SET` clause got copied.

4. **It must decide explicitly what happens to a `cancelled` job's status.** `RecomputeJobStatus`
   (`internal/store/query/jobs.sql:89-107`) is cancelled-blind: its CASE counts anything not in
   `('done','failed','timed_out')` as unfinished, so a single reopened task on a cancelled job pulls
   the **job** to `running` through exactly the mechanism
   `bug-2026-06-26-retry-resurrects-cancelled-task` exploited. Decide whether retry on a cancelled
   job is refused, permitted with the job moved out of `cancelled` deliberately, or permitted with
   `RecomputeJobStatus` taught about `cancelled` first. Do not discover the answer from a test.

5. **It must not reopen a task whose dependents already ran.** New relative to the parent item;
   lifted from `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md:895-896`. Reopening
   a task underneath dependents that already executed reproduces route B of that bug **by design**
   rather than by accident. `task_dependencies` and the `FailDependentTasks` recursive CTE are the
   existing machinery to reason against.

6. **Reopening a terminal job reactivates [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]].** The
   `done_24h` / `failed_24h` buckets window on `updated_at` as a finish proxy, which breaks the
   moment a terminal job re-opens. Schedule the two together or state explicitly that the stats
   regression is accepted for the interval between them.

## Proposal

- **Route.** `POST /v1/jobs/{id}/retry`, registered in the jobs block of `internal/api/server.go`.
  Decide the gating: the sibling force-cancel is admin-only while ordinary cancel is not, so
  "operator re-run" needs an explicit answer rather than an inherited one. `?task=failed|all` is
  parsed from `r.URL.Query()`; an absent or unrecognized value is a 400, not a silent default to
  `all`.
- **Store.** One new statement per the shape in constraint 2, in `internal/store/query/tasks.sql`,
  with a comment block at the statement stating its preconditions and its relationship to both
  `IncrementTaskRetryCount` and `RequeueTaskByID`. The existing comments at `:126-133` already point
  forward at this work; make them point at the new statement by name once it exists.
- **Transaction.** The task reopen and the job-status recomputation must be one transaction
  (`q.WithTx(tx)`), or a crash between them leaves a job whose status contradicts its tasks.
- **Dispatcher wake.** After a successful reopen, trigger the dispatcher the same way every other
  requeue path does, and gate that side effect on the write having actually affected rows - the
  invariant's "gate any side effect on the fence having matched" rule. A retry that matched zero
  tasks must not wake the dispatcher and must not report success.
- **Response.** Report how many tasks were reopened. A 200 with a count of 0 is a legitimate outcome
  (nothing matched the allow-list) and the client must be able to tell it from a real re-run; decide
  whether that is a 200 with `{"tasks_retried": 0}` or a 409.
- **Concurrency.** Two operators pressing Retry at once, and an agent reporting a terminal status
  concurrently with a retry, both need a stated outcome. Under READ COMMITTED the second UPDATE
  re-evaluates its WHERE against the already-updated row and affects zero rows, which is the
  behaviour `IncrementTaskRetryCount`'s comment relies on (`tasks.sql:99-106`) - confirm the same
  reasoning holds for the multi-row form, where "zero rows" becomes "some subset of rows".

## Acceptance / Done When

- The endpoint exists with auth gating, and `?task=failed` versus `?task=all` select demonstrably
  different task sets.
- The new statement is proven RED against a version without the status allow-list: a `done` task
  must not be reopened by `?task=failed`.
- `assignment_epoch` is proven to increment for every reopened task, and a late status update from
  the previous generation is proven to be dropped rather than applied.
- The `retry_count` decision and the `cancelled`-job decision are each stated in the spec with a
  rationale and each pinned by a test.
- A task with already-executed dependents is proven not to reopen (constraint 5).
- `IncrementTaskRetryCount` is not called by the new path, asserted structurally rather than by
  inspection if that is cheap to do.
- The jobs-stats interaction is either fixed with
  [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]] or explicitly accepted in writing.
- [[feature-2026-07-01-job-retry-action]] can drop its "backend-blocked" caveat.

## Related

- Split out of [[feature-2026-06-26-web-enabler-backend-endpoints]]; the other two endpoints of that
  item shipped in the 2026-08-13-web-enabler-list-endpoints slice.
- Frontend consumer, currently blocked on this: [[feature-2026-07-01-job-retry-action]]
- Correctness dependency: [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]]
- The constraint block's origin: `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md`
  section 11 (`:874-897`), and `docs/backlog/closed/bug-2026-06-26-retry-resurrects-cancelled-task.md`
- Source: `internal/store/query/tasks.sql:95-145` (`IncrementTaskRetryCount` and its "must NOT" note
  at `:126-133`), `:262-273` (`RequeueTaskByID`), `internal/store/query/jobs.sql:89-107`
  (`RecomputeJobStatus`, cancelled-blind), `internal/api/jobs.go`, `internal/api/server.go`
- CLAUDE.md **Invariants**, "Epoch fence" - read in full before writing the statement.

## Notes

Medium rather than high: the feature is wanted and its consumer is already carved out and waiting,
but nothing is broken today by its absence.

The reason this item is long is that seven weeks of accumulated constraints were about to be
separated from the endpoint they constrain. Every bullet above was learned by fixing something else,
and the cheapest possible failure mode for this work is an implementer who reads only the title,
finds `IncrementTaskRetryCount`, and reuses it.
