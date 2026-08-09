---
title: Publish task-log lines to the SSE event broker for live tailing
type: feature
status: closed
created: 2026-06-26
closed: 2026-08-09
resolution: fixed
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

## Resolution
Fixed 2026-08-09 (sse-task-log-publishing). `GET /v1/events` now accepts `?task_id=` and emits a
`task_log` event per persisted chunk, giving relay a live-log source for the first time.

**The shape decision, settled for both consumers: `?task_id=` on `/v1/events`, not `?follow=1`.**
Either shape needed a task-aware broker filter anyway - today a `Filter{}` subscriber would have
received every log line on the cluster and been drop-closed, which would have broken *status*
delivery. Once the filter is task-aware, `?task_id=` is nearly free, while `?follow=1` additionally
needed a second streaming handler, a JSON-vs-SSE content switch on a query param, and a second
connection for any view that also wants status. `?job_id=&task_id=` on one subscription gives both.

Delivery is task-keyed via a second index (task id -> channels), so a log publish iterates only that
task's tailers and there is deliberately no job-wide or cluster-wide log firehose. The acceptance
criteria are met as follows:

- **Non-blocking ingest.** `Broker.Publish` was **already** bounded (`select`/`default`,
  close-and-drop) - the item was wrong that this was new work. What the change actually adds is one
  DB round trip instead of two (`AppendTaskLog` became `:one` via a `fence/ins` CTE returning
  `job_id`/`seq`/`created_at`) plus a `HasLogSubscriber` fast path so nothing is marshalled in the
  steady state where nobody is tailing. `removeLocked` is now the single close-point, guarded on
  presence in `subs`, which makes close-exactly-once and removal-from-both-indexes one invariant
  rather than two - the risk that made the broker land first, under `-race`, before anything
  published.
- **Live subscription with a documented backfill path.** README documents a three-step gapless
  recipe (subscribe first, then page `?since_seq=`, then discard events at or below the highest
  backfilled `seq`) and the `dropped`/`slow_consumer` final frame, without which a Go consumer could
  not distinguish "you fell behind" from a clean end of stream.
- **Status events unaffected**, covered by a delivery-matrix test plus end-to-end assertions.

The epoch fence gained reach rather than changing: a stale chunk matches no fence row, so the
handler gets `pgx.ErrNoRows` and drops it *before* publishing - a zombie agent's output must never
appear in a live view and then vanish on refresh, having correctly never been stored.

Two adjacent defects were fixed as part of this: `relayclient.StreamEvents`'s default 64 KiB
`bufio.Scanner`, which would have failed an entire stream on one oversized log frame (status
payloads are tiny, so it could not bite before `task_log` existed); and persist failures being
swallowed by `_ =`, now reported but bounded to once per task per assignment epoch, since the
realistic failure repeats for a whole task. Chunk content is never logged - it is raw subprocess
output that can contain secrets a job's own script echoed.

`step_index`/`step_total` are deliberately not exposed here; the polling endpoint cannot supply them
and [[feature-2026-06-26-persist-expose-step-index-total]] owns that.

Review returned 0 high / 3 medium / 9 low. The most consequential was documentation rather than
code: the README claimed a `seq` discontinuity signalled a drop, but `seq` is `task_logs.id`, a
table-wide `BIGSERIAL`, so per-task values are ordered but **not** contiguous - a client
implementing "gap means re-backfill" would have fired on nearly every frame on a busy farm.

Two pre-existing security properties this work amplifies were filed rather than fixed here:
[[bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero]] and
[[idea-2026-08-09-sse-revoked-token-keeps-streaming]].

Unblocks [[feature-2026-06-26-task-log-view-sse-tailing]] and
[[idea-2026-05-09-mcp-live-task-log-streaming]], which now have a real live source to consume.
