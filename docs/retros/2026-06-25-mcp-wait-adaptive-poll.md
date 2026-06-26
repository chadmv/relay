---
date: 2026-06-25
topic: mcp-wait-adaptive-poll
branch: claude/happy-mendel-18687f
pr: autopilot (this branch)
---

# Session Retro: 2026-06-25 - adaptive client-side poll for relay_wait_for_job

**TL;DR:** Fixed the bug "relay_wait_for_job poll interval too coarse for sub-2s jobs".
`relay_wait_for_job` (internal/mcp/wait.go) now drives its inter-poll sleep through a pure
`nextWaitInterval(attempt)` helper: 500ms for the first 4 attempts, then 2s steady-state. A
sub-2s job now returns within ~500ms of completion, while a long-job's GET load stays within
~10% of the prior 2s cadence. First GET is still immediate; deadline clamp, ctx-cancel, and
terminal-status behavior are unchanged; `s.waitPoll` is preserved as a flat-interval test
override. Pure client-side change, no Invariant touched. Review caught a partially-vacuous
fast-job test; the engineer tightened it so the wall-clock bound provably fails under a forced
flat interval and passes as-is. Unit + integration green on Windows and Docker; review
otherwise clean.

## What Was Built

- **Fix** `internal/mcp/wait.go` - `fastWaitPoll` (500ms), `fastWaitCount` (4),
  `defaultWaitPoll` (2s) consts; pure `nextWaitInterval(attempt)` helper; the poll loop now
  calls `nextWaitInterval(attempt)` when no flat override is set, with the deadline clamp and
  ctx-cancel paths preserved.
- **Tests** - deterministic helper-level coverage of the interval schedule plus a non-vacuous
  wall-clock bound on fast-job return latency.

## Lesson: a pure helper is the test seam that kills timing flake

The win here was carving the schedule decision out of the loop into `nextWaitInterval(attempt int)
time.Duration` - a pure function of the attempt counter, no clock, no I/O, no channels. The
interval policy (500ms x4, then 2s) is then asserted directly and deterministically, with zero
sleeps and zero wall-clock dependence. The loop's *timing* still needs one wall-clock test (the
fast-job latency bound), but the *policy* - the part most likely to regress on a future tweak -
is verified without ever touching `time.After`.

The takeaway for next time: **when a timing behavior is really a decision plus a sleep, split
the decision into a pure helper and test that exhaustively; reserve the slow, flake-prone
wall-clock test for the one property the helper cannot express.** This kept the new coverage
deterministic and fast while still proving the user-visible latency improvement.

## Lesson: the review-caught vacuous test is the standing bar working

Code review found the fast-job test was partially vacuous - green, but not actually
distinguishing the fix from the old behavior. The engineer fixed it by anchoring the wall-clock
assertion so it is provably non-vacuous: it **fails at 2.0s under a forced-flat regression**
(the pre-fix behavior) and **passes at ~0.50s as-is**. That RED-against-the-real-exposure step
is exactly the project's standing standard ("a green test can be vacuous; assert a property only
the fix produces and prove RED against the real exposure"). Worth re-stating because a
latency-improvement fix is a classic vacuous-test trap: a test that merely waits for a fast job
to finish passes whether or not the poll is adaptive. The bound has to be tight enough that the
old 2s cadence would blow it.

## Technical decision: small adaptive poll over the larger SSE rewire

During Phase 1 grounding the TPM found that an authenticated SSE path already exists end to end:
`GET /v1/events?job_id=<id>` (`internal/api/events.go` `handleEvents`) plus
`relayclient.Client.StreamEvents` (`internal/relayclient/client.go`), and job events fire on
done/failed/cancelled (`internal/api/jobs.go` `s.broker.Publish`). Wiring `relay_wait_for_job`
to subscribe would deliver near-instant completion notification instead of a ~500ms poll.

It was deferred, not chosen, for these reasons:

- **An already-terminal-before-subscribe job never re-fires.** The broker
  (`internal/events/broker.go`) only fans out new `Publish` calls; there is no replay. A job
  that reached terminal before the subscribe lands would hang until timeout. Correct SSE wiring
  therefore needs a terminal re-check immediately after subscribe (the `StreamEvents`
  `onSubscribed` hook is the natural seam) to close that race.
- **Slow subscribers get dropped at the 64 buffer.** `Broker.Publish` closes and removes any
  subscriber whose buffer fills, so the poll has to stay as a fallback regardless - the SSE path
  cannot be the sole mechanism.
- **Marginal win over the now-adaptive poll.** With the adaptive poll capturing most of the
  latency improvement (~500ms vs the old 2s), the remaining gain from SSE is sub-second for the
  extra complexity of subscribe + post-subscribe terminal re-check + retained poll fallback.

The Phase 1 spec called this out as a backlog-worthy future item. The principle: **prefer the
small change that captures most of the win and touches no Invariant over the larger rewire whose
marginal benefit is sub-second and whose correctness depends on a race fix and a retained
fallback.** The SSE enhancement remains genuinely actionable later (filed to backlog this cycle)
- it is a refinement, not a do-over.

## Backlog Triage

**Filed one item.** `docs/backlog/idea-2026-06-25-mcp-wait-sse-subscribe.md` (type idea,
priority low): wire `relay_wait_for_job` to subscribe to `GET /v1/events?job_id` via
`StreamEvents`, with an immediate post-subscribe terminal re-check to close the
already-terminal race and the adaptive poll retained as fallback (the 64-buffer drop makes the
fallback mandatory). Grounded by reading the route, the client method, and the broker before
filing - all three symbols exist as described. Low priority because the adaptive poll already
captured most of the win.
