---
title: The Jobs timeline filters on created time, so a long job active inside the window but created before it is not drawn
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# The Jobs timeline filters on created time, so a long job active inside the window but created before it is not drawn

## Summary
The timeline (PR #184) walks GET /v1/jobs?since=&until= on created_at, which is what the endpoint offers. A job created before the window and still running inside it is not in the result, so the six-hour view of a busy farm can show an empty axis while renders are active. The spec recorded the gap as a known limitation.

## Context
From the JF spec.

## Proposal
Either an active_in filter on the endpoint (started_at or finished_at inside the window) or a second query for running jobs merged client-side, with the caption stating which.

## Related
- web/src/jobs/useJobTimeline.ts, internal/api/job_filters.go
