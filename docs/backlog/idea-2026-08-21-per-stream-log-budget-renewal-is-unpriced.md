---
title: The per-stream log budget renews on every stream, and under auto-enroll the renewal is free
type: idea
status: open
created: 2026-08-21
priority: low
source: Phase 4 of the 2026-08-20-grpc-admission-bounds slice; the residual that slice wrote into ingestLogLimiter's own comment
---

# The per-stream log budget renews on every stream, and under auto-enroll the renewal is free

## Summary

`ingestLogLimiter` is allocated inside `Connect`, as a stack local, once per **`Connect` call** -
that is, once per **stream**, not once per connection:

```go
lim := newIngestLogLimiter()
```

`grpc.MaxConcurrentStreams(1)` caps how many streams a connection may hold **at once**. It does not
cap how many a connection may open over its lifetime. So a caller that opens a stream, spends its
16-token burst, closes the stream and opens another gets a **fresh 16-token burst every cycle**,
without ever needing a second connection and without ever touching either connection cap.

The steady-state figure the limiter advertises (6 lines per minute) is therefore a per-stream rate
that holds only while a stream stays open, and multiplying `RELAY_GRPC_MAX_CONNS` by 6 to get a
fleet-wide lines-per-minute ceiling would be wrong. The limiter's doc comment says this correctly
today, under "WHAT IS NOT BOUNDED, STATED RATHER THAN GLOSSED".

**What prices the cycle is not this file.** Reaching any of the budgeted log sites requires passing
`authenticateAndRegister` first, which runs **before** the limiter is allocated, so each cycle costs
the caller a valid credential and several Postgres round trips. **That is a real cost, not a bound** -
and it is the whole of the defence.

**Under `RELAY_ALLOW_AUTO_ENROLL=true` the credential half of that cost is zero.** On that path there
is no credential at all: any host able to reach the port registers by claiming a hostname. The
Postgres round trips remain, as do the connection caps and `RELAY_GRPC_REGISTRATION_TIMEOUT`, but the
"costs a valid credential" sentence describes the credentialed paths only.

## Repro / Symptoms

No live flood today, and the demonstration is structural rather than dramatic.

1. Hold one admitted connection.
2. Open a `Connect` stream, register, emit enough malformed messages to spend the 16-token burst on any
   of the five budgeted kinds, then close the stream.
3. Reopen. The new `Connect` call allocates a new `ingestLogLimiter` with a full burst.
4. Repeat. Server log volume scales with cycle rate, bounded only by how fast
   `authenticateAndRegister` completes.

Under auto-enroll, step 2's registration needs no credential, so the cycle rate is bounded by the
round trips alone. `RELAY_DB_MAX_CONNS` (25 by default) throttles the aggregate, which is a real
brake and is not a bound on log volume.

## Context

Found during Phase 4 of the admission slice, while doing the arithmetic the source item's acceptance
bullet 5 demanded (`ingestLogLimiter`'s comment must cite whatever connection bound landed). Doing it
out loud is what exposed that only the **burst** figure is a genuine ceiling: the three admission
knobs bound the number of limiters **alive at once** (1024 x 16 = 16384 lines fleet-wide,
64 x 16 = 1024 per source prefix), and nothing bounds how many limiters exist over time.

**This is not a new defect and the slice did not introduce it.** The renewal property predates the
admission work; what the admission work did was make it visible, by forcing somebody to multiply the
numbers. The item exists so the residual is tracked rather than only described in a doc comment that a
future rewrite may shorten.

**Checked against the two nearest open items; neither covers it.**

- [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]] is about allocation
  **order**: two registration-time log sites run before `lim` exists and so take no budget key. Its
  proposed fix (allocate at the top of `Connect` and thread it through) moves the allocation earlier in
  the **same call**, so it does not change renewal at all. Note that the two interact in the right
  direction, though: if that item lands, the budget covers the register path, and each renewal cycle
  then spends budget on the way in rather than starting clean.
- [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] is observability for the suppression the
  limiter already performs.

## Proposal

To be argued at spec time. **The honest first question is whether to fix this at all or to record the
price and stop**, and the answer probably depends on the auto-enroll half.

- **Do nothing, and make the price a stated invariant.** The credential-plus-round-trips cost is real
  for both credentialed paths. The comment already says so. If auto-enroll's zero-credential case is
  accepted under that flag's documented trust model, this item closes as a written decision. That is a
  legitimate outcome and should be considered first rather than last.
- **Hoist the budget to the connection.** The limiter would have to outlive the `Connect` frame, which
  is exactly what its current comment forbids and for good reasons: as a stack local it dies with the
  frame, so there is no teardown to get wrong and no way for a stale connection to reach a fresh one.
  Any design that hoists it has to answer where it lives (grpc-go exposes no per-connection hook a
  service handler can hang state on), how it is torn down, and how the "owned by one goroutine, no
  mutex" property survives. **Weigh that against `MaxConcurrentStreams(1)`**: with a cap of one
  concurrent stream, per-connection and per-stream differ only across time, so a hoist buys exactly
  the renewal fix and nothing else.
- **Bound stream count per connection instead.** A counter in `Connect` refusing the Nth stream on one
  connection is simpler than hoisting the budget, and it is a different control with its own value (it
  also prices re-registration). It needs the same "where does per-connection state live" answer.
- **Or price the cycle rather than the budget.** The cheapest thing that closes the auto-enroll case
  specifically is a bound on auto-enroll registrations, which is
  [[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]]. If that item lands with a rate
  limit, this one may close with it. **Check that before speccing this independently.**

Whatever is chosen, do **not** merge the dedupe map and the token bucket back together, and do not
delete the bucket on the grounds that the map limits things. The limiter's comment explains at length
why the predecessor failed exactly that way.

## Acceptance / Done When

- Either the fleet-wide **steady-state** log rate has a stated ceiling that survives a caller cycling
  streams, proven by a test that cycles streams and observes the budget is not renewed; or the decision
  to leave it priced rather than bounded is written down in `ingestLogLimiter`'s comment **and** in
  README's auto-enrollment cost paragraph, in the terms this item uses.
- If a bound lands, `TestConnect_TwoConnectionsDoNotShareTheLogBudget` still passes unchanged: two
  distinct connections must never share a budget, whatever happens within one connection.
- The auto-enroll case is addressed explicitly, not by a sentence about the credentialed paths.
- `ingestLogLimiter`'s "WHAT IS NOT BOUNDED" paragraph is corrected if it is falsified, and left alone
  if the decision is to price rather than bound.
- No new lock, goroutine, queue or DB round trip on the recv path, and the limiter is not made
  reachable from any goroutine but its own connection's.

## Related

- Source: `internal/worker/handler.go` (`Connect`'s `lim := newIngestLogLimiter()` allocation site and
  its comment), `internal/worker/ingest_log_limiter.go` (the "WHAT IS NOT BOUNDED" and "THAT PRICE IS
  ZERO UNDER RELAY_ALLOW_AUTO_ENROLL" paragraphs), `cmd/relay-server/grpc_config.go`
  (`grpcMaxConcurrentStreams` and why it is 1)
- Different defect, same allocation site, interacts in the right direction:
  [[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]
- May close this one as a side effect if it lands with a rate limit:
  [[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]]
- Observability for the same limiter: [[idea-2026-08-15-ingest-log-suppression-is-uncounted]]
- The slice that surfaced it: `docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md` section 6.6,
  `docs/retros/2026-08-21-grpc-admission-bounds.md`
- The item that slice closed: [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]

## Notes

Filed at **low** because there is no live exposure at defaults - auto-enroll is off, and on the
credentialed paths the attacker already holds a valid agent token and already gets task dispatch.

The reason to keep it open anyway is the shape, which this project keeps rediscovering: **a bound
stated per unit is only a bound if the unit is bounded.** The 2026-08-20 slice bounded connections
because `ingestLogLimiter`'s budget was stated per connection. Doing that arithmetic revealed that the
budget is not actually per connection at all - it is per stream - and that the unit **below** the one
just bounded is still free. One layer down, same sentence.
</content>
