---
title: "Push the retries / timeout_seconds bounds into the database as CHECK constraints"
type: idea
status: open
created: 2026-08-27
priority: low
source: Q6 of the 2026-08-27 retry-bounds spec, which declined the constraint for that slice
---

# Push the retries / timeout_seconds bounds into the database as CHECK constraints

## Summary

The retry-bounds slice bounds `retries` and `timeout_seconds` in `jobspec.Validate`, which every
ingest path inherits via the Single job-spec pipeline invariant. It deliberately does NOT add the
matching `CHECK` constraints to `tasks.retries` and `tasks.timeout_seconds`, so the bound holds for
callers of the validator and for nobody else. This item is the remaining guarantee: make the column
refuse the value the way `tasks_status_check` refuses an unknown status.

## Context

Proposed and declined as Q6 of `docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`.
The decline was about deployment, not about whether the constraint is desirable:

Migrations are embedded in the binary and run on startup. A plain `ALTER TABLE ... ADD CONSTRAINT`
validates existing rows, so it fails on exactly the population that has the bug - a deployment that
already stored `retries: 2000000000` cannot boot the binary carrying the fix. `NOT VALID` avoids
that, but converts a loud startup failure into a silently stuck task, which is worse in the way this
project has repeatedly found worse: the failure stops being attached to the deploy that caused it.

So the constraint needs a pre-existing-row decision made deliberately, and that decision is the
whole item. It is not four lines of SQL.

The slice replaced the constraint with a caller-side guard test instead: `CreateTask` and
`CreateTaskWithSource` have exactly one non-test caller, `internal/jobcreate`, and a test pins that.
That guard is strictly weaker than a constraint - it protects the shape of the call graph, not the
column - but it fails closed at review time rather than at boot time.

## Proposal

Sketch. The decision to make first is what happens to rows already violating the bound:

- **Clamp.** An `UPDATE tasks SET retries = LEAST(retries, 10)` in the same migration, ahead of the
  constraint. Boots cleanly, silently rewrites user data. Needs a log line at minimum, and arguably
  should not be silent at all.
- **Fail to boot.** The plain `ADD CONSTRAINT`. Honest and loud, and it strands an operator with a
  binary that will not start and no obvious remedy unless the release notes carry the query to find
  the offending rows.
- **`NOT VALID` plus a later `VALIDATE CONSTRAINT`.** Two migrations across two releases, with the
  bound enforced for new writes immediately. The most operationally careful and the most work.

Whichever lands, the constraint's bound and `jobspec`'s constant must be visibly tied to each other,
or they will drift. The project's existing precedent is `jobs_priority_check` in migration
`000019_status_vocabulary_checks`, whose vocabulary `jobspec.Validate` mirrors with a comment saying
the two must stay identical, plus `TestTasksStatusVocabularyIsExactly` going RED when the set moves.
A numeric bound needs the analogous mechanism and does not have one.

## Acceptance / Done When

- `INSERT`ing a task row with `retries` or `timeout_seconds` outside the accepted range fails at the
  database, proven by a test that bypasses `jobspec.Validate` entirely - the point is that the
  guarantee does not depend on the validator.
- The pre-existing-row behaviour is chosen deliberately, stated in the migration's comment, and
  tested: a database seeded with an out-of-range row is brought to the new schema without a silent
  surprise, whichever surprise was chosen.
- Something goes RED if the constraint's bound and `jobspec`'s constant drift apart.

## Related

- Source: `internal/store/migrations/000001_initial.up.sql` (`retries INT NOT NULL DEFAULT 0`,
  `timeout_seconds INT`), `internal/jobspec/jobspec.go` (the constants), and
  `internal/store/migrations/000019_status_vocabulary_checks.up.sql` for the precedent
- Declined in: `docs/superpowers/specs/2026-08-27-retry-bounds-and-budget-predicate.md`, Q6
- The item whose slice raised it: [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]]
- Same family, a different unvalidated column with no CHECK:
  [[bug-2026-08-23-patch-worker-max-slots-unvalidated]]
