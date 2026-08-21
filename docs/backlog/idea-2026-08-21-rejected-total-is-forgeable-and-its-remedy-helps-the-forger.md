---
title: task_log_fence.counts.rejected_total is caller-inflatable, and the misconfiguration it detects has a remedy that helps the forger
type: idea
status: open
created: 2026-08-21
priority: medium
source: Phase 4 of the 2026-08-21-silent-drop-observability slice 3; the residual that slice disclosed in README rather than fixed, plus the plan's own priced R1
---

# `rejected_total` is caller-inflatable, and its documented remedy helps the forger

## Summary

Slice 3 shipped `task_log_fence.counts.rejected_total` on `GET /v1/server/counters` and documented, in
README, what it is FOR:

> a count that climbs steadily on a fleet whose jobs look healthy is the signature of a trailing window
> set too small - a units mistake such as `15s` for `15m` is the likely one

**Any enrolled agent can manufacture that exact signature at will**, and the operator's documented
response to it makes the system less safe. Three facts compose:

1. **Only `worker_id` is authenticated on an incoming `TaskLogChunk`.** It is resolved at registration
   and never taken from the wire. The **task id is from the wire**, unconstrained.
2. **Nothing rate-limits task-log CHUNKS.** The per-connection ingest budget
   (`internal/worker/ingest_log_limiter.go`) bounds log *lines*, on a different arm; the fence-rejection
   arm never consults it at all. A chunk naming a task the sender does not own costs one Postgres round
   trip, stores nothing, and increments the counter.
3. **The counter cannot say which of the four fence predicates failed** (see the plan's R1 and the arm's
   comment). A forged chunk and a legitimate late flush produce the identical increment.

So a smooth, indefinite, entirely-forged climb is indistinguishable from the misconfiguration the number
exists to detect - and the README-documented remedy for that misconfiguration is to **raise**
`RELAY_TASKLOG_TRAILING_WINDOW`, which widens the window bounding how long a finished task's assignee
may keep appending. **A forgeable signal whose remedy helps the forger.**

The exposure is **griefing and misdirection of one signal, not disclosure**: an agent cannot read
`/v1/server/counters` (admin-only), the payload carries no identifiers, and the number never reaches the
worker stream. Nothing is leaked; one operator signal is polluted, and acting on it opens a security
window.

## Repro / Symptoms

1. Enroll an agent normally. It needs no assignment and no task of its own.
2. Stream `TaskLogChunk`s naming well-formed uuids that match no task (or tasks it does not own), at any
   rate the connection sustains. Each is a `t.id = task_id` or `t.worker_id = worker_id` fence miss.
3. `task_log_fence.counts.rejected_total` climbs smoothly and indefinitely while every job in the fleet
   is healthy. `ingest_log_budget` stays at zero throughout, because that arm never consults the budget.
4. An operator following README's reading guidance concludes the trailing window is too small and raises
   it - widening the post-terminal append window for every task in the deployment.

Slice 3's own integration test (`TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServing
Handlers`) drives step 2 already: it needs no seeded task precisely because a well-formed uuid naming
nothing reaches the fence arm.

## Context

Found by Phase 4 of the slice that shipped the counter, and **disclosed in-slice rather than fixed**.
README `:1290` now carries the warning ("That signature is forgeable, so confirm the window against its
configured value before raising it") and names the mechanism. That closes the documentation half. What
remains is that the number itself is unbounded and unsplit.

**Slice 3's plan already priced the split, and this item exists because that pricing should not be
re-derived.** The plan's R1 refuted the item-and-spec claim that per-reason splitting is "structurally
impossible": the premise is true (the fence is a CTE that yields no row when any predicate fails, so
nothing can carry a reason column) but a **one-round-trip variant exists** - replace the `fence` CTE with
a task CTE exposing the predicates as booleans and LEFT JOIN the insert onto it. It was declined on a
stated price:

- it deletes the `pgx.ErrNoRows` signal that every caller, every comment and every test of this fence is
  written against, and which five other statements' contracts share;
- it makes `AppendTaskLogRow`'s three columns nullable on the success path, so the publish must
  re-derive "did the insert happen" from a NULL check - **a new way to publish an unstored chunk**,
  which the arm forbids absolutely;
- it puts a rewrite of the most security-sensitive statement in the repo inside an observability change.

**That decision was right for an observability slice and is not obviously right forever.** Splitting
`closed window` from `wrong assignee / unknown task` is exactly what turns a forgeable aggregate into a
signal that says *which knob is wrong*: a forged climb lands on the identity predicates, a genuine
too-small window lands on the recency one. The two halves of this item are therefore the same design
question - **a split would resolve the forgeability**, and a rate limit would bound it without
distinguishing anything.

**Not a duplicate of [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]], and the distinction is
mechanical.** That item is about a genuine assignee writing unbounded **durable rows** for a task it
owns; every remedy it proposes (a per-task row cap, a byte total, a counter column on `tasks`) counts
rows that a rejected chunk **never creates**. A per-task cap cannot bound chunks naming tasks that do not
exist. The two share one sentence - neither path is rate-limited - and nothing else. They should be read
together; if a chunk-rate limit ships for either, it likely serves both, and whichever ships first should
say so.

## Proposal

Three routes, to be argued at spec time. They are not mutually exclusive and the first is the cheapest.

- **Bound the rate of fence-rejected chunks per connection.** The natural shape is a token bucket like
  `ingestLogLimiter`'s, keyed on nothing (per connection, stack-local), consulted **on the rejection arm
  only** so the accept path pays nothing. Note what it must NOT do: it must not suppress the count (a
  suppressed rejection is still a rejection, and hiding it re-creates the blind spot the counter closed),
  and it must not close the stream on a legitimate late flush across a coordinator outage - the case the
  trailing window exists to serve. A second counter for "rejections after the bucket emptied" is the
  honest shape, following `ingest_log_budget`'s two-arm precedent.
- **Split the counter by predicate class, taking R1's priced rewrite.** Two keys is enough:
  `rejected_stale_or_unknown` (the three identity/currency/unknown-task predicates - all forgeable, all
  "the system working") and `rejected_window_closed` (the one legitimate, operator-configurable case).
  The price above is the thing to weigh, and it should be weighed with the `pgx.ErrNoRows` contract's
  five other consumers enumerated first, not estimated.
- **Do neither, and change the README guidance instead.** If the signal is only ever read alongside the
  configured window value, the misdirection is bounded by procedure. This is the "correct the
  advertisement rather than the product" option and it is already half-taken; record it as a deliberate
  choice with its cost (procedure is not a control) rather than leaving it as the default outcome.

**Do not** make the counter admin-visible per worker in response to this. Attributing rejections to a
`worker_id` puts an unbounded-cardinality map keyed on a value a peer can create into an admin-facing
payload - the exact hazard
[[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]] must solve for its own
section, and it is a much larger question than this one.

## Acceptance / Done When

- A single enrolled agent cannot drive `rejected_total` (or its successors) at an unbounded rate, OR the
  decision not to bound it is recorded with its reason where the counter is declared.
- If the split ships: a fence rejection caused by a closed trailing window is distinguishable in the
  payload from one caused by an unknown task, a wrong assignee or a stale epoch, proven by a
  handler-layer test driving all four.
- If the split ships: every caller of `AppendTaskLog` that depends on `pgx.ErrNoRows` is enumerated in
  the commit and its contract is either preserved or migrated, and **no path can publish an unstored
  chunk** - guarded by a test, not by a comment.
- README's `task_log_fence` reading guidance matches whatever ships, including whether raising the
  trailing window is still the recommended response.
- No new DB round trip, goroutine, queue or lock on the gRPC recv goroutine.
- The counter still cannot be read by an agent and still tells a prober nothing about why its chunk was
  rejected.

## Related

- Source: `internal/worker/handler.go` (`handleTaskLog`'s `pgx.ErrNoRows` arm and its four-reason
  enumeration; the arm's "declined, and here is the price" paragraph, which is this item's design input),
  `internal/store/query/tasks.sql` (`AppendTaskLog`'s four-conjunct fence and the prose stating the
  zero-row contract), `internal/worker/ingest_log_limiter.go` (the bucket shape to copy, and the reason
  it does not already cover this arm), `README.md:277` and `:1290`
- The item that shipped the counter: [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]
  (**closed 2026-08-21**), and `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice3.md`
  (R1 - the priced rewrite, and the plan's own "candidate new item, conductor's call")
- The retro that found the forgeability: `docs/retros/2026-08-21-silent-drop-observability-slice3.md`
- Adjacent on rate, NOT a duplicate (that one is about durable rows a legitimate assignee writes):
  [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]]
- Why an attacker-keyed counter is not the answer: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]]
  (closed 2026-08-15)
- The knob whose remedy this compromises: `RELAY_TASKLOG_TRAILING_WINDOW`
  (`cmd/relay-server/main.go`, `parseTrailingLogWindow`), and the slice that introduced it:
  `docs/retros/2026-08-14-tasklog-terminal-append-bound.md`
- The sibling that must not solve this with a per-worker map:
  [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]

## Notes

Filed at **medium**. Nothing is broken and nothing leaks; a counter shipped one day earlier is being
recorded as inflatable on the day it shipped, which is the right time rather than a criticism of it.

The rule worth recording: **when you ship an operator signal, write down what an adversary who can move
it would gain - and check whether the signal's own documented remedy is in their favour.** This one's is.
The disclosure went into README the same day for exactly that reason, but disclosure is not a bound, and
a warning in a document is read by the operator who is already suspicious rather than by the one being
misled.
