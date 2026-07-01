---
title: Publish task-log lines to the SSE event broker for live tailing
type: feature
status: open
created: 2026-06-26
priority: high
source: ROADMAP deep-refresh gaps pass (2026-06-26)
---

# Publish task-log lines to the SSE event broker for live tailing

## Summary
`handleTaskLog` persists log chunks to the DB but never publishes them to `events.Broker`, and the
`/v1/events` SSE stream carries only task/job status payloads. There is therefore no live-log source
at all today - the only way to read logs is polling `GET /v1/tasks/{id}/logs`. This is the backend
enabler that makes any live task-log tailing possible.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps pass. It is the shared prerequisite behind two
consumer items - the web full-screen task-log view and the MCP live-log streaming idea - both of
which currently assume a live source that does not exist.

## Proposal
Publish each appended log chunk to `events.Broker` keyed by job id (and task index), extending the
event payload with a log-line variant distinct from the status payload. Decide the delivery shape (a
new SSE event type on `/v1/events?job_id=`, or a dedicated `?follow=1` on the task-logs endpoint) and
keep DB persistence as the source of truth for backfill. Bound the publish so a slow SSE subscriber
cannot block `handleTaskLog` (mirror the one-bounded-sender invariant).

## Acceptance / Done When
- Appended task-log lines are published to the broker without blocking the ingest path.
- A client can subscribe and receive live log lines, with a documented backfill path for history.
- Status events on `/v1/events` are unaffected; tests cover the new payload and the non-blocking guarantee.

## Related
- Unblocks [[feature-2026-06-26-task-log-view-sse-tailing]] and [[idea-2026-05-09-mcp-live-task-log-streaming]]
- Source: `internal/worker/handler.go:509-526` (handleTaskLog, no publish), `internal/api/events.go`, `internal/events/broker.go`, `internal/api/tasks.go:63-137` (polling logs)

## Notes
The SSE-vs-`?follow=1` shape decision is shared with the web task-log item; settle it here, since this
is the backend half.
