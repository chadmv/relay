---
title: Per-task timing (started_at / finished_at / duration on the task response)
type: feature
status: open
created: 2026-07-01
priority: low
source: job-detail Holo relayout - hi-fi tasks table shows per-task DUR/% the API does not provide
---

# Per-task timing (started_at / finished_at / duration on the task response)

## Summary
The task response inside `GET /v1/jobs/{id}` carries no per-task timing (`started_at` /
`finished_at` / duration), so the tasks table cannot show a DUR column or a per-task
progress %. The hi-fi `HoloJobDetail` tasks table shows both; the 2026-07-01 relayout omits
them (no fake data) pending this.

## Proposal
Expose per-task `started_at` / `finished_at` (from the task lifecycle) on `taskResponse`, and
let the client derive DUR (terminal) / elapsed (running). A per-task % only makes sense if a
task exposes progress; relay tasks do not have a progress signal today, so the % is likely out
of scope (the job-level progress bar stays derived from done/total task counts).

## Acceptance / Done When
- `taskResponse` includes per-task `started_at` / `finished_at` (nullable).
- The job-detail tasks table shows a per-task DUR/elapsed column.

## Related
- Omitted by the 2026-07-01 job-detail Holo relayout (docs/superpowers/specs/2026-07-01-job-detail-relayout-design.md)
- Pairs with [[feature-2026-07-01-job-detail-timing-enrichment]]
- Source: `internal/api/jobs.go` (`taskResponse`, `toTaskResponse`), task lifecycle in the store
