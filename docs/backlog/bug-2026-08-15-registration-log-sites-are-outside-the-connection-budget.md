---
title: Two registration-time log sites sit outside the connection's log budget, because the budget is allocated after registration
type: bug
status: open
created: 2026-08-15
priority: low
source: Phase 4 lens of the 2026-08-15-tasklog-err-limiter-keying slice; the structural half of a finding whose injection half shipped
---

# Two registration-time log sites sit outside the connection's log budget, because the budget is allocated after registration

## Summary

`Connect` (`internal/worker/handler.go`) allocates the per-connection log budget **after**
`authenticateAndRegister` has returned:

```
Connect
  -> authenticateAndRegister
       -> autoEnrollAndRegister   log.Printf "auto-enrolled worker %s (hostname=%q) from %s"
            -> finishRegister     log.Printf "register inventory replace failed for %s: %v"
  -> lim := newIngestLogLimiter()          <- the budget starts here
  -> message loop (handleTaskStatus / handleTaskLog / handleInventoryUpdate)
```

So the two caller-reachable log sites inside registration take **no budget key**. Each is bounded to one
line per connection by registration itself, which is why the 2026-08-15 slice left them alone - but the
bound is a **property of where they happen to sit**, not a rule the code enforces. Anything that moves,
loops or retries inside registration inherits an unbudgeted `log.Printf` on the recv goroutine, and
nothing reddens.

**This item is the structural gap only.** The content half of the auto-enroll line shipped on
2026-08-15: it now renders `reg.Hostname` with `%q` + `clipID`, because that hostname is validated
nowhere and bounded only by gRPC's 4 MiB default receive limit. Do not re-file the injection.

## Repro / Symptoms

No live flood today. The demonstration is structural: comment out `lim.allow(...)` guards and the whole
new integration battery reddens; move a `log.Printf` into a loop inside `finishRegister` and nothing
does. The `finishRegister` inventory line is the more interesting of the two - `applyInventory` swallows
`time.Parse` failures on `LastUsedAt` and binds SQL NULL into a `TIMESTAMPTZ NOT NULL` column, so it is
reachable with no NUL trick at all, purely from a malformed timestamp string in the register message.

The real multiplier is [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]: one line per
connection is only a bound if connections are bounded, and they are not.

## Context

Found by a Phase 4 lens while checking that the new budget covered the surface the spec's section 2.6
enumerated. The spec's table classified both of these rows as "capped at one per connection - unchanged",
which is accurate as a statement about today's code and is exactly the kind of claim that goes stale
without anything noticing.

The lens named the clean structural option, which is why this is worth a file rather than a shrug:
**allocate the limiter at the top of `Connect`, before `authenticateAndRegister`, and thread it into
`finishRegister`.** That brings all three sites (`Connect`'s worker-id parse failure,
`autoEnrollAndRegister`'s audit line, `finishRegister`'s inventory line) under one budget, and turns
"these sites happen to run once" into "every caller-driven log line on this goroutine is budgeted, by
construction".

One thing to settle rather than assume: **the auto-enroll audit line should probably stay unbudgeted on
purpose.** It is the only record anywhere that a token-less enrollment happened, and suppressing it
would corrupt the audit trail of the mechanism it documents. If so, the right shape is an explicit
opt-out with a comment saying why, not an accident of allocation order. That distinction is the whole
content of this item.

## Proposal

- Move `lim := newIngestLogLimiter()` to the top of `Connect`, before the first `stream.Recv()`.
- Thread it through `authenticateAndRegister` -> the three register paths -> `finishRegister`.
- Budget `finishRegister`'s inventory-replace line under a new kind (`kindRegisterInventory`), or under
  the existing `kindInventory` if a spec argues they are the same event - they are the same statement
  family (`applyInventory` versus `applyInventoryUpdate`) but different phases.
- Decide the auto-enroll audit line deliberately: budgeted, or explicitly exempt with a comment stating
  that an audit record must not be suppressible and that its **volume** defence is `clipID` while its
  **count** defence is registration itself.
- Add a comment at the allocation site stating the invariant the move establishes: every caller-driven
  log line on this goroutine goes through the budget, and a new one that does not is a finding.

## Acceptance / Done When

- The limiter is allocated before any code that can emit a caller-driven log line on the connection's
  goroutine, proven by a test that drives a registration-time log site through a `LimiterHandle` and
  observes the spend.
- `finishRegister`'s inventory-replace line is budgeted, proven by a test that is RED against today's
  code (N registrations with a malformed `LastUsedAt` produce at most the burst).
- The auto-enroll audit line's treatment is a stated decision with a comment, not an allocation
  accident, and whichever way it goes there is a test pinning it.
- `TestConnect_TwoConnectionsDoNotShareTheLogBudget` still passes unchanged - the move must not make the
  limiter reachable from anywhere but its own connection's goroutine.
- No new DB round trip, goroutine, queue or lock on the registration or recv path.

## Related

- Source: `internal/worker/handler.go` (`Connect`'s allocation site, `authenticateAndRegister`,
  `autoEnrollAndRegister`'s audit line, `finishRegister`'s inventory-replace line, `applyInventory`)
- The budget this extends: `internal/worker/ingest_log_limiter.go`
- The slice that created the gap and shipped the injection half:
  `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md` section 2.6 (the per-site table),
  `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`
- The reason "one line per connection" is not a bound:
  [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]
- Adjacent, same hostname value: [[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]],
  [[bug-2026-08-12-auto-enroll-hostname-takeover]]

## Notes

Filed at **low** because there is no live exposure: both sites are one line per connection today and the
content defences shipped. The value of the item is that it converts a coincidence into a rule. The
2026-08-15 spec's own table is the evidence for why that is worth doing - it had to reason, per site,
about whether each line was caller-forceable, and got one of the rows right for the wrong reason (the
`finishRegister` row's stated justification was the wrong mechanism, corrected in a dated amendment
inside the spec). A rule at the allocation site replaces that per-site reasoning with one check.
