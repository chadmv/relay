---
title: Full-screen task log view with live SSE tailing
type: feature
status: closed
created: 2026-06-26
closed: 2026-08-09
resolution: fixed
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

## Resolution
Fixed 2026-08-09 (task-log-view-sse-tailing). The SPA now tails a task's log live, consuming the
`GET /v1/events?task_id=` source that [[feature-2026-06-26-sse-task-log-publishing]] added earlier the
same day. Both the job-detail Log tab and a full-screen route show a live view; `useTaskLogs` is gone.

**Route is `/jobs/:id/tasks/:taskId`, not the design's `/jobs/:id/tasks/:n`.** The ordinal form is
unimplementable: a job's tasks are inserted in one transaction where `created_at` defaults to a
transaction-constant `NOW()`, and `ORDER BY created_at` has no tiebreaker, so there is no stable `n`.
Filed as [[bug-2026-08-09-task-list-ordering-has-no-tiebreaker]].

**Transport: `fetch` + `ReadableStream` with a hand-rolled SSE parser, no backend change.**
`EventSource` cannot carry the bearer header, and a token in a query param was rejected outright - it
would put a long-lived unscoped credential into access logs, history and `Referer`. Losing
`EventSource`'s auto-reconnect is a benefit, since a *bounded* retry was required anyway and the
server honours no `Last-Event-ID`. `apiStream` sits beside `apiFetch` so token attachment and the 401
notifier stay in one place.

**One `?task_id=`-only connection, not `?job_id=&task_id=`.** `useJob`'s existing 3s poll already
supplies the full task list that `{id,status}` frames cannot, plus the terminal signal a task-only
stream lacks - so a combined connection would have added a second thinner source of truth and
inherited the shared-64-slot-buffer coupling for nothing. A terminal task opens no connection at all.

Correctness details worth preserving: the subscription opens **before** the first history page (the
ordering is load-bearing and guarded by a test asserting recorded request order); `seq` is used only
as a dedupe threshold and resume cursor, never for gap detection or offset arithmetic, because it is
a table-wide `BIGSERIAL` and so non-contiguous; and a **log entry is not a line** - `chunkWriter`
copies arbitrary byte ranges, so entries hold many lines and lines straddle entries, which the old
one-div-per-entry rendering split mid-content. Client-side reassembly (plus `\r` collapse and ANSI
strip) fixes that pre-existing defect.

Review returned 2 high / 2 medium / 8 low, both highs empirically reproduced with probes:

- A failing backfill page aborted the stream **without ending the assignment**, so the dying
  connection's own promise called `recover('closed')` - overwriting `error` with `reconnecting`,
  adding a bogus drop marker, and retrying a 400/404 the code documents as non-transient. Six
  connections and six backfill requests per viewer for a deleted task, with the Retry button
  unreachable. The same shape the backend epoch fence exists to prevent.
- Repeated `dropped` frames re-subscribed immediately with no cap - the "exactly one re-backfill"
  guarantee was per-frame, and server-side drop is *caused* by slow consumption, so it self-recurs on
  exactly the high-volume tasks where it hurts. 25 drop cycles produced 26 connections.

Also caught: eight unrelated test cases had been deleted when `jobs/api.test.ts` was replaced
wholesale (leaving `listJobs`' `limit=50` asserted nowhere), and the log-secrecy test was
serialization-blind - `JSON.stringify` on an `Error` yields `{}`, so a `console.error(err)` carrying
log content would have passed the very test written to prevent it.

Empirically settled for future reference: MSW 2.7 + undici under jsdom 29 **does** deliver
`ReadableStream` bodies incrementally. The injected `fetchImpl` seam was kept regardless, and every
frame-delivery, retry-timing and leak assertion goes through it rather than MSW.

Deferred to [[idea-2026-08-09-task-log-tail-and-paging-improvements]]: no tail fetch (paging is
forward-only and `seq` yields no offset), no virtualization, no export, no ANSI colour, no in-log
search. `step_index`/`step_total` remain unexposed - [[feature-2026-06-26-persist-expose-step-index-total]]
owns those. Web suite green at 530 tests; verified live against a real backend.
