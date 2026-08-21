---
title: netlimit reports refusals but not occupancy, so a saturated connection cap is indistinguishable from fleet growth
type: idea
status: open
created: 2026-08-21
priority: medium
source: Phase 4 of the 2026-08-20-grpc-admission-bounds slice; the diagnosability cost that slice accepted
---

# netlimit reports refusals but not occupancy, so a saturated connection cap is indistinguishable from fleet growth

## Summary

`netlimit.Stats` is **refusal counts only**:

```go
type Stats struct {
	RefusedTotal uint64
	RefusedPerIP uint64
}
```

`refusalReporter` turns those into at most one log line per minute, and only when a counter moved. So
the only thing an operator ever sees is that connections were refused, cumulatively, since startup.

That answers "did the cap fire" and nothing else. It cannot answer any of the questions an operator
actually has when it does fire:

- **How full is the cap right now?** A `RefusedTotal` that stopped moving means either the pressure
  ended or the fleet settled at exactly the ceiling. Those need opposite responses.
- **Is this one adversary or many hosts?** A distributed source pattern and a legitimately growing
  fleet produce the same `RefusedTotal`.
- **Which cap is the binding one?** `admit` checks the total first, so a connection over **both** caps
  is counted against `RefusedTotal` only, by design. `RefusedPerIP` therefore under-reports whenever
  the fleet cap is also saturated - exactly the situation in which per-source information matters most.

**This is a gap the admission slice created**, in the same shape the coordinator watchdog created one
on 2026-08-20: a control that quietly refuses work converts a loud failure (file-descriptor exhaustion,
a crash, an alert) into a quiet one (agents that never come online while the server logs a tidy
once-a-minute summary).

## Repro / Symptoms

1. Run a server at the defaults (`RELAY_GRPC_MAX_CONNS=1024`, `RELAY_GRPC_MAX_CONNS_PER_IP=64`).
2. Open 1024 connections from 16 distinct source prefixes, 64 each. Every connection is admitted; no
   cap fires; `RefusedTotal` and `RefusedPerIP` are both `0` and the reporter never speaks.
3. Start a legitimate agent from a seventeenth address. It is refused, backs off, and never comes
   online. `RefusedTotal` increments.
4. Observed: one log line per minute saying N connections were refused over the total cap. Nothing
   says that 1024 slots are held, that they come from 16 sources, or that each source holds exactly
   the per-source maximum. The operator cannot distinguish this from "our fleet grew past 1024".

Step 2 is the shape of a real adversary rather than a contrived one: `hostKey` aggregates IPv6 to a
`/64`, and a `/56` delegation is 256 of them, so sixteen prefixes is a small ask.

## Context

Found during Phase 4 of the admission slice. The **counts-only, no-addresses** constraint on `Stats`
and on the log line is **deliberate and must be preserved by anything that closes this item**: the
refusal path is reachable by any unauthenticated peer, so a summary that could carry caller-supplied
bytes would be a new attacker-driven log site inside the control that exists to bound attacker-driven
log volume. `Stats`'s own doc comment says exactly that. This item asks for **more numbers, never
identifiers.**

**This is the fourth instance of one shape, and the roadmap wants them specced together.** The three
open siblings:

- [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] - a task-log chunk rejected by the fence.
- [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] - a log line dropped by `ingestLogLimiter`.
- [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]] - an assignment
  terminated by the coordinator.

All four are "the system now silently drops, refuses or kills something and nobody can see it", and
**all four want the same read surface**. `internal/api/server.go` routes `GET /v1/config`,
`GET /v1/jobs/stats`, `GET /v1/workers/stats` and `GET /v1/workers/{id}/metrics`, and nothing that
carries a server-wide counter, so each of the four either extends `GET /v1/workers/stats` or depends
on [[feature-2026-08-09-server-info-allowlist-endpoint]]. **Spec the four in one sitting and ship them
separately.** That instruction is already in the watchdog sibling and it applies unchanged here.

**This one is the easiest of the four and differs from all three in one respect:** it is the only one
whose subject is not per worker. `netlimit` runs before authentication and never learns a worker
identity, by design. So the "answer per worker or global, once, for all the sibling items" question in
the watchdog item has an exception here, and the spec should notice that rather than force a shape.

## Proposal

To be argued at spec time rather than adopted as written.

- **Add occupancy to `Stats`, read under the existing mutex in ONE snapshot.** Three fields:
  - `LiveTotal` - the current `l.total`.
  - `DistinctSources` - `len(l.perIP)`.
  - `MaxPerSource` - the largest value in `l.perIP`.

  **They must be read in one critical section**, not by three separate calls, or a reader can observe a
  combination that never existed and draw a conclusion from it. `Stats` already returns a value struct,
  so this is a shape change to one method rather than a new API.
- **`MaxPerSource == 1` with `DistinctSources` near the cap is the distributed-attacker signature**,
  and it is the reason `MaxPerSource` earns its place over a simple occupancy gauge. A healthy fleet
  behind NAT has a few sources with many connections each; a distributed source pattern has many
  sources with one connection each. Those are distinguishable with two integers and no addresses.
- **Cost check, because `MaxPerSource` is the one field that is not free.** It is O(number of distinct
  sources) under the lock. At the defaults that is at most 1024 entries, on a once-a-minute reporter
  tick, on a mutex whose other holders are `admit` and `release`. Confirm that before shipping; if it
  is a concern, maintain the maximum incrementally in `admit`/`release` rather than sampling the map,
  and note that a decremented maximum is not exactly recoverable without a scan.
- **Decide the read surface with the three siblings, not alone.** A log line is the cheap half and
  probably ships first; an endpoint is the useful half and is the shared expensive part.
- **Say whether it is per replica.** It is: these are in-process counters on one listener. A two-server
  fleet splits its connections arbitrarily, so a fleet-wide number needs aggregation this item does not
  provide, and the field names or the docs must not imply otherwise.
- **Do not add addresses, prefixes or hostnames to any of it**, and say so in the type's comment so a
  future "which IP is it" request is answered on the record rather than by relaxing the rule.

## Acceptance / Done When

- An operator can see current occupancy of both caps, not only cumulative refusals, through the
  reporter line and through whatever read surface the sibling items settle on.
- The distributed-source case is distinguishable from the NAT case by the reported numbers alone.
- Every occupancy figure in one report comes from a single critical section, proven by a test that
  mutates the read into separate lock acquisitions and goes RED.
- Nothing added carries an address, a prefix, a hostname or any other caller-supplied byte, and the
  constraint is stated in `Stats`'s doc comment as a rule rather than as a description.
- The reporter still emits at most one line per interval and only when something moved. **Occupancy
  changes constantly on a live fleet**, so an occupancy field naively included in the
  "did anything move" comparison would make the reporter speak every single minute forever - the exact
  property `TestRefusalSummaryLogsOnlyWhenCountersMove` exists to protect. Settle the trigger rule
  explicitly: probably still refusals-only, with occupancy carried in the line when it speaks.
- The per-replica semantics are documented.
- No new lock, goroutine or allocation on `Accept`'s hot path.

## Related

- Source: `internal/netlimit/listener.go` (`Stats`, `admit`, `release`, and the total-first ordering
  that makes `RefusedPerIP` under-report), `cmd/relay-server/grpc_config.go` (`refusalReporter.tick`,
  `runRefusalReporter`, `grpcRefusalReportInterval`)
- Siblings on the same shape, to be specced together and shipped separately:
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]],
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]],
  [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]
- Possible dependency for the read surface: [[feature-2026-08-09-server-info-allowlist-endpoint]]
- The slice that created the gap: `docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md`
  section 6.5, `docs/retros/2026-08-21-grpc-admission-bounds.md`
- The item that slice closed: [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]

## Notes

**This item also carries the actionable half of the IPv6 delegation residual, which is deliberately
not filed separately.** `hostKey` aggregates IPv6 to a `/64` because that is the smallest delegation
anybody receives, and the source comment and README both state plainly where that stops: it raises the
bar to "one host per /64 the attacker holds" and no further, so at the defaults sixteen distinct `/64`s
fill the 1024 fleet cap and a `/56` or `/48` escapes in proportion to its size. There is no better
prefix length available - going coarser collapses unrelated operators into one bucket - so the residual
has no code fix worth filing. **What it does have is a detection story, and it is exactly
`MaxPerSource` plus `DistinctSources`.** Sixteen prefixes each holding 64 connections is a shape those
two numbers show and `RefusedTotal` does not. If this item is specced, that case belongs in its test
matrix.
</content>
