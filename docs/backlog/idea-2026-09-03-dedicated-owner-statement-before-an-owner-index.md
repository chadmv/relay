---
title: GET /v1/jobs?mine=true needs a dedicated owner statement before an owner index pays
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# GET /v1/jobs?mine=true needs a dedicated owner statement before an owner index pays

## Summary
Lane JB added and then dropped migration 000023 (an index on jobs (submitted_by, created_at, id)) because the mine=true LIST never used it under the automatic or generic plan at 30k and 300 rows; only the count used it, and only under a custom plan. The list statement folds the owner predicate into the shared filtered-list statement, whose planner shape cannot pick the owner index. An owner-scoped list statement of its own would.

## Context
Measured in lane JB's probe (jobs_filters_cursor_walk_integration_test.go and an uncommitted plan probe). Caveat recorded at the time: the count used the index under the auto or custom plan at execution 7, and the comparison was against the joined count that PR #178 replaced.

## Proposal
Write ListJobsForOwnerPage and CountJobsForOwner as their own sqlc statements, re-add the index, and confirm with EXPLAIN ANALYZE at 30k rows that the list uses it before keeping either.

## Related
- internal/api/job_filters.go, internal/store/query/jobs.sql
