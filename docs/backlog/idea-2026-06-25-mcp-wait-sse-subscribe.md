---
title: relay_wait_for_job - subscribe to job SSE instead of polling, poll as fallback
type: idea
status: open
created: 2026-06-25
source: mcp-wait-adaptive-poll spec (Phase 1 grounding, deferred alternative)
---

# relay_wait_for_job - subscribe to job SSE instead of polling, poll as fallback

## Summary

`relay_wait_for_job` (`internal/mcp/wait.go`) now uses an adaptive client-side poll
(500ms x4, then 2s) that returns within ~500ms of a job reaching a terminal state. An
authenticated server-push path already exists end to end and would cut that residual ~500ms to
near-zero: `GET /v1/events?job_id=<id>` (`internal/api/events.go` `handleEvents`, which subscribes
the request to the broker scoped to that job) is consumed client-side by
`relayclient.Client.StreamEvents` (`internal/relayclient/client.go`), and job state changes are
published as `"job"` events on done/failed/cancelled (`internal/api/jobs.go` `s.broker.Publish`).

This is a low-priority refinement: the adaptive poll already captured most of the latency win,
so the marginal gain is sub-second.

## Proposal

Wire `relay_wait_for_job` to subscribe to the job's event stream and return as soon as a
terminal `"job"` event arrives, falling back to the existing adaptive poll. Specifically:

- Open `StreamEvents(ctx, "/v1/events?job_id=<id>", onSubscribed, handler)`. Stop the stream
  (handler returns false) when a terminal status is observed in a `"job"` event's payload.
- **Close the already-terminal-before-subscribe race.** The broker
  (`internal/events/broker.go`) only fans out new `Publish` calls - there is no replay - so a
  job that reached terminal *before* the subscribe lands would never re-fire and the wait would
  hang to timeout. Do an immediate terminal re-check right after the subscription is
  established. The `onSubscribed` hook on `StreamEvents` (fires after HTTP 200, before any event
  is read) is the natural seam: GET `/v1/jobs/<id>` once there, and if already terminal, return
  without entering the read loop.
- **Keep the adaptive poll as a mandatory fallback.** `Broker.Publish` closes and removes any
  subscriber whose 64-buffer fills (`internal/events/broker.go`), so a slow MCP consumer can
  have its stream closed mid-wait. On stream close/error before terminal, fall back to the
  current `nextWaitInterval` poll loop rather than failing the wait. The poll path therefore
  stays in place; SSE is an accelerator layered on top, not a replacement.
- Preserve all current contract behavior: deadline clamp, `timed_out` shape, ctx-cancel, and
  the `s.waitPoll` flat-interval test override.

## Acceptance / Done When

- `relay_wait_for_job` returns near-instantly (no ~500ms poll latency) for a job that reaches a
  terminal state while the tool is subscribed.
- A job that is already terminal before the subscribe lands still returns promptly via the
  post-subscribe re-check (does not hang to timeout) - covered by a regression test that
  reaches terminal before the subscription is established.
- When the SSE stream closes early (e.g. simulated 64-buffer drop) before terminal, the tool
  falls back to the adaptive poll and still returns the terminal state - covered by a test.
- Deadline/timeout, ctx-cancel, and terminal-status return shape are unchanged from the
  adaptive-poll version.
- No Invariant touched (pure client-side composition of an existing endpoint and client method).

## Related

- `internal/mcp/wait.go` - current adaptive-poll implementation and the `nextWaitInterval` helper
- `internal/api/events.go` - `handleEvents`, the `GET /v1/events?job_id=` SSE endpoint
- `internal/relayclient/client.go` - `StreamEvents` (with the `onSubscribed` post-200 hook)
- `internal/events/broker.go` - `Subscribe`/`Publish`; the no-replay and 64-buffer-drop behaviors
- `internal/api/jobs.go` - `s.broker.Publish` of `"job"` events on state change
