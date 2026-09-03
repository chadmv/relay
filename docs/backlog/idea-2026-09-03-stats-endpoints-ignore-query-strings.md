---
title: The /stats endpoints ignore query strings, and the scheduled-jobs stats read two snapshots
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# The /stats endpoints ignore query strings, and the scheduled-jobs stats read two snapshots

## Summary
GET /v1/scheduled-jobs/stats and the worker stats endpoint accept and ignore any query string, so a caller who passes a filter gets an unfiltered answer that looks filtered. The scheduled-jobs stats also read failed_runs_24h in a second statement, so the two numbers come from different snapshots.

## Context
From the SB review (PR #180).

## Proposal
A shared reject-all-params helper applied to every endpoint that takes no parameters, and the failure count folded into the single stats statement.

## Related
- internal/api/scheduled_jobs.go, internal/store/query/scheduled_jobs.sql
