---
title: Persist and expose step_index/step_total on task_logs
type: feature
status: open
created: 2026-06-26
priority: low
source: ROADMAP deep-refresh gaps pass (2026-06-26)
---

# Persist and expose step_index/step_total on task_logs

## Summary
The agent sends `StepIndex`/`StepTotal` on task-log messages, but they are dropped at persist time and
never exposed by the logs API, so a synthetic text marker line is the only per-step signal consumers
get. Persisting and exposing the structured fields is the prerequisite that lets the synthetic marker
be retired.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps pass; this is the long-standing prerequisite that
blocks the remove-synthetic-step-marker idea. The fields exist on the wire but die at the DB boundary.

## Proposal
Add nullable `step_index`/`step_total` columns to `task_logs` (migration), thread them through
`AppendTaskLog` (currently inserts only task_id/stream/content) and `handleTaskLog` (currently drops
them), and surface them on the logs API `logEntry` (currently seq/stream/content/created_at). Keep the
synthetic marker until consumers read the structured fields; retiring it is the follow-on item.

## Acceptance / Done When
- `task_logs` stores step_index/step_total and `AppendTaskLog` writes them under the epoch fence.
- The logs API returns the structured fields.
- Once consumers use them, the synthetic-marker deletion is unblocked.

## Related
- Unblocks [[idea-2026-04-26-remove-synthetic-step-marker]]
- Source: `internal/store/query/tasks.sql:48-56` (AppendTaskLog), `internal/store/migrations/000001_initial.up.sql:76-82` (no step columns), `internal/worker/handler.go:520-525` (drops fields), `internal/api/tasks.go:56-61` (logEntry)

## Notes
Touches a migration plus the epoch-fenced AppendTaskLog path; keep the existing marker line until
consumers migrate.
