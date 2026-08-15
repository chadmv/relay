---
title: "`retries` is unvalidated end to end, and the retry budget is enforced only in Go"
type: bug
status: open
created: 2026-08-12
priority: medium
source: Phase 4 review of the retry-resurrect status-guard iteration (2026-08-12)
---

# `retries` is unvalidated end to end, and the retry budget is enforced only in Go

Two halves of one defect on the retry path. They are **separable** - either can ship without the
other - but they are filed together because each one alone leaves the other's failure mode reachable,
and because the second is the part the 2026-08-12 retry hardening did not close.

## Summary

### A. `retries` is bounded nowhere between the wire and the database

`internal/jobspec/jobspec.go` declares:

```go
Retries        int32             `json:"retries"`
```

`jobspec.Validate` bounds it nowhere. It checks name, task names, duplicate names, command form,
`depends_on` targets, dependency cycles, priority vocabulary and the source spec - and never looks at
`Retries`. The value flows through `internal/jobcreate/jobcreate.go` into `store.CreateTaskParams`
and into the `tasks.retries` column unchanged, and the column has no check constraint.

Because all job-spec ingestion goes through the single pipeline (REST, CLI, MCP, schedrunner - the
project's Single job-spec pipeline invariant), **every** entry point inherits the gap. A user
submitting `retries: 2000000000` on a task that fails deterministically - a bad command, a missing
binary, a permission error - gets an effectively unbounded dispatch -> fail -> requeue loop that
occupies a worker slot until an operator cancels the job. Negative values are equally unchecked;
they are inert today only because the Go comparison `task.RetryCount < task.Retries` is false, but
they are storable.

This is an authenticated-user denial of service. It is not a privilege escalation: any authenticated
user can already submit work, and the project has no per-user quota anywhere, so this is one concrete
instance of a broader missing-quota story. What distinguishes it is the leverage - one small,
well-formed request produces unbounded work that never completes and therefore never frees the slot.

### B. The retry budget lives only in Go, at one call site

The budget check exists in exactly one place, `internal/worker/handler.go`:

```go
if terminal && task.RetryCount < task.Retries {
    if _, err := h.q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{...
```

`IncrementTaskRetryCount` itself has **no** `retry_count < retries` predicate. The 2026-08-12
retry-resurrect iteration moved currency (`assignment_epoch`), identity (`worker_id`) and terminality
(`status IN ('pending','dispatched','running')`) into that statement precisely so the retry path
stops depending on the caller reading a row and deciding correctly - and the budget is the one part
of that decision left behind in Go. The statement will happily take `retry_count` past `retries` for
any caller that asks.

Nothing is exploiting it today: there is one production caller and it checks. The value of closing it
is the same value the other three predicates bought - the statement states its own precondition, and
a second caller (an operator retry endpoint, a future re-dispatch path) cannot get it wrong by
omission.

**The integration lane confirmed plainly that no existing test would catch a task exceeding its
retry budget.** Whichever half is done first, that gap is the thing to close.

## Repro / Symptoms

**A.** Submit a job whose task has `"retries": 2000000000` and a command that always fails. The task
cycles pending -> dispatched -> failed -> pending indefinitely, holding a worker slot and producing a
task-log row set and a dispatch cycle per iteration. No API response, validation error or log line
marks it as abnormal.

**B.** Not reachable through the production caller. Demonstrable at the store layer: on a task with
`retries = 1, retry_count = 1`, `IncrementTaskRetryCount` at the current epoch and assignee succeeds
and leaves `retry_count = 2`.

## Context

Found by the Phase 4 review of `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md`.
Half B is what makes this belong to that iteration rather than to a generic input-validation sweep:
that change's whole argument was that the retry statement must re-check the state its caller decided
from, and it enumerated three predicates while a fourth condition from the same `if` stayed in Go.

Worth noting what the same iteration established about this statement, because it constrains the
fix: `IncrementTaskRetryCount` is for the **agent-driven** retry only, and its query comment says so.
`POST /v1/jobs/{id}/retry` (tracked in `feature-2026-06-26-web-enabler-backend-endpoints`) must not
call it and needs its own statement, which will have to decide what happens to `retry_count` on an
operator re-run. A `retry_count < retries` predicate here interacts with that decision: if the
operator endpoint resets `retry_count` to 0, the two stay independent; if it does not, an operator
re-run of an exhausted task gets zero agent-side retries, and the predicate makes that structural
rather than incidental.

## Proposal

Two independent changes.

**A. Bound `Retries` in `jobspec.Validate`,** alongside the existing vocabulary checks:

```go
if ts.Retries < 0 || ts.Retries > maxRetries {
    return fmt.Errorf("task %s: retries must be between 0 and %d", ts.Name, maxRetries)
}
```

The cap is a product decision, not a technical one; something in the range 5 to 20 covers every
legitimate use (a flaky network mount, a contended license server) and anything higher is a user
asking for a different feature. Follow the file's existing error style - a named task and a stated
range - and consider a matching check constraint on `tasks.retries` in a migration, so the bound is
enforced for any future writer the way `tasks_status_check` is. `timeout_seconds` sits in the same
struct with the same absence of bounds and should be looked at in the same pass, but is not claimed
by this item.

**B. Either add the predicate, or pin the Go gate with a test.** Both are defensible; pick one
deliberately rather than by omission:

- `AND retry_count < retries` on `IncrementTaskRetryCount`, which makes the statement's precondition
  complete and matches what the other three predicates did. Note it changes the Go branch from "the
  decision" to "an early return", and the existing `pgx.ErrNoRows` silent-drop branch already handles
  the rejection correctly with no restructuring.
- Or leave enforcement in Go and add the regression test that does not exist: a task at
  `retry_count == retries` whose assignee reports `FAILED` must end terminal, not requeued.

Doing B by predicate makes the test trivial to write at the store layer as well, which is an argument
for it.

## Acceptance / Done When

- **A:** A job spec with `retries` outside the accepted range is rejected at submission with a clear
  per-task error, proven by a `jobspec` unit test (RED against today's code) and asserted through at
  least one real entry point so the rejection is not merely a library property. A spec at the
  boundary value is still accepted.
- **B:** A task whose retry budget is exhausted cannot burn another retry, proven by a test that is
  RED against today's code - at the store layer if the predicate lands, at the handler layer if the
  Go gate stays.
- Positive control on both: a task with a normal budget still retries exactly as many times as
  configured and then goes terminal.
- No change to `POST /v1/jobs/{id}/retry`'s design space beyond what is written in the Context above;
  if the predicate lands, the constraint it puts on that endpoint's `retry_count` decision is
  recorded on `feature-2026-06-26-web-enabler-backend-endpoints`.

## Related

- Source: `internal/jobspec/jobspec.go` (the `Retries` field and `Validate`),
  `internal/jobcreate/jobcreate.go` (the two `Retries: ts.Retries` bindings),
  `internal/store/query/tasks.sql` (`CreateTask`, `IncrementTaskRetryCount`),
  `internal/worker/handler.go` (the sole budget check, `task.RetryCount < task.Retries`)
- The iteration that fenced everything else on this path:
  `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md`;
  [[bug-2026-06-26-retry-resurrects-cancelled-task]] (closed 2026-08-12,
  `docs/backlog/closed/bug-2026-06-26-retry-resurrects-cancelled-task.md`)
- Interacts with: [[feature-2026-06-26-web-enabler-backend-endpoints]] (the operator retry endpoint
  and its `retry_count` decision), [[feature-2026-07-01-job-retry-action]] (its frontend)
- Invariant in contact: Single job-spec pipeline (CLAUDE.md) - a bound added in `jobspec.Validate` is
  inherited by REST, CLI, MCP and schedrunner for free, which is why it belongs there and not in a
  handler.

## Notes

Half A is cheap and self-contained; half B is a one-line predicate plus a test, or a test alone. The
reason to keep them on one item is the framing: the retry path was audited end to end on 2026-08-12
and this is what the audit left, at both ends - an unvalidated input at the front and an unenforced
budget at the back. Splitting them would lose that, and each half alone reads like a nit.
