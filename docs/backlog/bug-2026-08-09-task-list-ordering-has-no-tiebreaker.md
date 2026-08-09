---
title: Task list ordering is unspecified and shifts, because ORDER BY created_at has no tiebreaker
type: bug
status: open
created: 2026-08-09
priority: medium
source: Spec phase of the task-log view iteration (2026-08-09)
---

# Task list ordering is unspecified and shifts, because ORDER BY created_at has no tiebreaker

## Summary
A job's tasks are all inserted in one transaction, and `created_at` defaults to `NOW()`, which is
**transaction-constant** in Postgres - so every task in a job shares an identical `created_at`.
Ordering the task list by `created_at` with no tiebreaker therefore leaves the order fully
unspecified, and Postgres is free to return it differently between calls. In practice the order
shifts as rows are `UPDATE`d, so the job-detail tasks table visibly reorders while a job runs.

## Repro / Symptoms
Open the job detail page for a job with several tasks and watch the tasks table across the 3s
`useJob` poll while tasks change status. Rows move even though nothing about the job's structure
changed. There is no user-visible reason for the reordering, and it makes the table hard to read
precisely when it matters most - while work is progressing.

## Context
Found while specifying the task-log view (2026-08-09). The original intent was a `/jobs/:id/tasks/:n`
route using the task's ordinal position, which turned out to be unimplementable: with no stable
order there is no meaningful `n`. The shipped route uses the task **UUID** instead
(`/jobs/:id/tasks/:taskId`), which sidesteps the routing problem but leaves the display-ordering bug
untouched.

Worth noting the ordering is not merely unstable but has no defined intent: nothing currently
records the order in which a job's tasks were declared in its spec, so "the order the user wrote
them" is not recoverable from the schema as it stands. That is the more interesting half of this
item.

## Proposal
Decide what the intended order actually is, then make the query express it deterministically:

- **Declaration order** is almost certainly what a user expects - the order the tasks appear in the
  job spec. This needs somewhere to put it. Check whether anything already captures it before
  adding a column; if not, a `position`/`ordinal` integer written by `CreateJobFromSpec` would, and
  it must flow through the single job-spec pipeline rather than a parallel path.
- **Failing that**, any stable tiebreaker (e.g. `ORDER BY created_at, id`) at least stops the
  reordering, even though the resulting order would be arbitrary rather than meaningful. This is the
  cheap fix and is strictly better than today.

Find every task-listing query before changing one - the API list, the job-detail response, and
anything the CLI or MCP server uses should not disagree with each other about order.

## Acceptance / Done When
- A job's tasks appear in the same order on every request, proven by a test that would fail against
  today's tiebreaker-less query.
- The order is documented (declaration order, or explicitly "stable but arbitrary").
- Every task-listing surface (REST, CLI, MCP, the SPA job-detail table) agrees.

## Related
- Surfaced by `docs/superpowers/specs/2026-08-09-task-log-view-sse-tailing.md`, which rejected
  index-based task routing because of this
- Source: the task-listing queries in `internal/store/query/tasks.sql` and their consumers in
  `internal/api/`
- If a `position` column is added, it must be written through `CreateJobFromSpec` per the single
  job-spec pipeline invariant

## Notes
The cheap tiebreaker and the meaningful-order fix are different sizes of work; they can ship
separately, and the tiebreaker alone resolves the user-visible reordering.
