---
title: Remove dead 'queued'/'dispatched' status vocabulary from CancelJobTasks and CountActiveJobsForSchedule
type: idea
status: open
created: 2026-07-01
priority: low
source: ROADMAP deep-refresh gaps sweep (2026-06-26)
---

# Remove dead 'queued'/'dispatched' status vocabulary from CancelJobTasks and CountActiveJobsForSchedule

## Summary
Two queries filter on status values that the migration 000019 CHECK constraints make unreachable for
their table. It is harmless today (the real states are covered) but it is dead vocabulary that
contradicts the schema and can mislead a reader about what states exist.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps sweep. Migration `000019_status_vocabulary_checks`
constrains task status to `('pending','dispatched','running','done','failed','timed_out')` and job
status to `('pending','running','done','failed','cancelled')`.

## Proposal
- `CancelJobTasks` (`internal/store/query/tasks.sql:181`) filters tasks on
  `status IN ('pending','queued','running','dispatched')` - `'queued'` is not a valid task status.
- `CountActiveJobsForSchedule` (`internal/store/query/scheduled_jobs.sql:75-78`) filters *jobs* on
  `status IN ('pending','queued','running','dispatched')` - `'queued'`/`'dispatched'` are never job
  statuses.

Trim each filter to the vocabulary valid for its table, then `make generate` to regenerate sqlc. No
behavioral change (the reachable states remain covered).

## Acceptance / Done When
- Each query filters only on statuses valid for its table per the 000019 CHECK constraints.
- `make generate` run; the diff is query-only and behavior is unchanged.

## Related
- Found in the same sweep as [[bug-2026-06-26-retry-resurrects-cancelled-task]].
- Source: `internal/store/query/tasks.sql:181`, `internal/store/query/scheduled_jobs.sql:75-78`, `internal/store/migrations/000019_status_vocabulary_checks.up.sql`.

## Notes
Cosmetic/consistency only. Remember the sqlc regeneration and the CRLF/LF hygiene noted in CLAUDE.md
after editing `.sql`.
