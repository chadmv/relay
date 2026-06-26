---
title: Full-screen task log view with live SSE tailing
type: feature
status: open
created: 2026-06-26
priority: high
source: ROADMAP web-frontend deep review against design_handoff_relay_holo (2026-06-26)
---

# Full-screen task log view with live SSE tailing

## Summary
The Holo design's `HoloTaskLog` (route `/jobs/:id/tasks/:n`) streams a single task's log live,
and the job-detail page's log tab needs the same streaming primitive. The SPA has no
log-streaming UI today, and the backend's task-logs endpoint is polling-only. This is the SPA's
first EventSource/SSE client and a prerequisite for the job-detail live-log tab.

## Context
Surfaced by the 2026-06-26 `/roadmap web-frontend deep` review against `design_handoff_relay_holo/`.
The handoff README lists `GET /v1/jobs/:id/tasks/:n/logs?follow=1`, but that `?follow=1` does not
exist; the per-screen spec (`reference/screens/job-detail.js`) instead points the live log at the
existing `GET /v1/events?job_id=` SSE stream.

## Proposal
First, a decision: consume the existing job-scoped `GET /v1/events?job_id=` SSE and filter
client-side per task, OR add `?follow=1` to the task-logs endpoint (`internal/api/tasks.go`,
currently `?limit`/`?since_seq` polling, no flusher). Then build a shared EventSource hook and the
full-screen `HoloTaskLog` view: header (job/task/worker/status), "Follow tail" toggle, auto-scroll,
and a polling backfill for history before the live tail.

## Acceptance / Done When
- The SSE-vs-`?follow=1` decision is made and recorded.
- A reusable log-stream hook exists and is covered by tests (msw or equivalent).
- `/jobs/:id/tasks/:n` renders a full-screen, auto-scrolling, follow-tail log.
- The job-detail page's log tab reuses the same primitive.

## Related
- Design: `design_handoff_relay_holo/reference/screens/job-detail.js`, `hifi3-holo-pages.jsx` (`HoloTaskLog`)
- Prerequisite for the log tab of [[idea-2026-06-05-job-detail-page-row-click]]
- Distinct from the MCP-side [[idea-2026-05-09-mcp-live-task-log-streaming]] (same data, different surface)
- Source: `internal/api/tasks.go`, `internal/api/events.go`, `internal/events/`

## Notes
Decision needed before implementation; the existing `/v1/events` SSE may make a new `?follow=1`
endpoint unnecessary.
