---
title: Job-detail timing enrichment (started_at / finished_at / elapsed on GET /v1/jobs/{id})
type: feature
status: open
created: 2026-07-01
priority: low
source: job-detail Holo relayout - hi-fi HoloJobDetail shows started/elapsed/ETA/duration the detail endpoint does not return
---

# Job-detail timing enrichment (started_at / finished_at / elapsed on GET /v1/jobs/{id})

## Summary
`GET /v1/jobs/{id}` (`handleGetJob` -> `toJobResponse`) does not populate `started_at` /
`finished_at` (those are list-only enrichment via `applyJobEnrichment`), so the job-detail page
cannot show STARTED, elapsed, ETA, or overall duration. The hi-fi `HoloJobDetail` surfaces all
of these; the 2026-07-01 relayout omits them (no fake data) pending this enrichment.

## Proposal
Populate the same enrichment fields on the single-job response that the list already derives
(`started_at` = min task start, `finished_at` = max terminal task finish), or add them to the
job row. Then the job-detail header can show STARTED + a derived ELAPSED (running) / DURATION
(terminal). ETA is a further estimate (out of scope unless a duration model is added).

## Acceptance / Done When
- `GET /v1/jobs/{id}` returns `started_at` / `finished_at` (nullable) consistent with the list.
- The job-detail page shows STARTED and a derived elapsed/duration, replacing the omitted mock fields.

## Related
- Omitted by the 2026-07-01 job-detail Holo relayout (docs/superpowers/specs/2026-07-01-job-detail-relayout-design.md)
- Pairs with [[feature-2026-07-01-per-task-timing]] (per-task duration for the tasks table)
- Source: `internal/api/jobs.go` (`handleGetJob`, `toJobResponse`, `applyJobEnrichment`)
