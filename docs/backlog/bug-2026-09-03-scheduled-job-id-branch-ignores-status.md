---
title: GET /v1/jobs?scheduled_job_id= ignores a ?status= given alongside it
type: bug
status: open
created: 2026-09-03
priority: medium
source: fan-in of the 2026-09-02 web-frontend batch
---

# GET /v1/jobs?scheduled_job_id= ignores a ?status= given alongside it

## Summary
handleListJobs branches on scheduled_job_id before it reads status, so a request carrying both returns every job of that schedule regardless of status while the response looks filtered. The two parameters are accepted together without a 400.

## Context
Noted in the JB spec during PR #178 and left out of that lane's scope.

## Proposal
Either reject the combination with the same 400 the sort-plus-filter case uses, or thread status through the scheduled_job_id statement. Add the request to jobs_filters_integration_test.go either way.

## Related
- internal/api/jobs.go (the hasFilter and scheduled_job_id branch)
