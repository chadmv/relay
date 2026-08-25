---
title: Two registration-time log sites sit outside the connection's log budget, because the budget is allocated after registration
type: bug
status: open
created: 2026-08-15
updated: 2026-08-21
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

### Update 2026-08-21 - the census, and the OTHER unbudgeted class is a separate item

The ingest-counters slice (`docs/retros/2026-08-21-silent-drop-observability-slice2.md`) counted every
`log.Printf` in `handler.go` while checking a README claim that turned out to be false. Twelve sites,
five budgeted, seven not - and the seven are **two different problems**:

- **Registration-time (`:233`, `:522`, `:553`) - this item.** The budget does not exist yet. The fix is
  allocation order plus threading, exactly as proposed above.
- **Post-registration, inside `handleTaskStatus` (`:939`, `:984`, `:991`) - NOT this item.** The budget
  is a parameter of that function and is used twice in it; those three lines just do not call it. Filed
  as [[bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget]]. Read the two
  together - they may well ship together - but do not merge them: this item's whole content is
  allocation order and the audit-line decision, and widening it to "all unbudgeted log lines" is how an
  item ends up wrong about its own scope.
- `:1197` (`handleTaskLog marshal`) is the twelfth site and is claimed by neither item as an exposure;
  no input is known to reach it.

**Two things that changed the cost of this work.** README now names both unbudgeted classes explicitly,
so the false "every caller-driven log line is rate-limited" sentence is gone and any fix here must
update that text. And **the `logKind` names are now a response contract** - each is a JSON key under
`ingest_log_budget.counts` - so `kindRegisterInventory`, if this item adds it, is a payload change with
a checklist: the const inside the `kindCount` sentinel, an array cell, a field on
`worker.IngestLogDropsByKind`, a line in `byKind`, a field and json tag on `api.ingestLogKindCounts`, a
line in `ingestLogKindCountsFrom`, two `counterPayloadLeaves` entries and the kinds list in
`TestServerCounters_ReportsTheIngestLogSnapshot`. Slice 2 proved a kind can be added correctly on the
worker side and published nowhere with every package green; three guards now fire on a new kind, and
`TestIngestLogKindCountsPublishesEveryWorkerSideField` is the one that catches the arity drift.

**And a decision this item now inherits rather than invents:** if the auto-enroll audit line stays
unbudgeted deliberately, that is a *third* state - not budgeted, not an accident - and the counters
payload says nothing about it either way. Whatever ships, README's "the budget covers these sites and no
others" sentence has to stay true.

## Proposal

- Move `lim := newIngestLogLimiter()` to the top of `Connect`, before the first `stream.Recv()`.
- Thread it through `authenticateAndRegister` -> the three register paths -> `finishRegister`.
- Budget `finishRegister`'s inventory-replace line under a new kind (`kindRegisterInventory`), or under
  the existing `kindInventory` if a spec argues they are the same event - they are the same statement
  family (`applyInventory` versus `applyInventoryUpdate`) but different phases. **(2026-08-21: whichever
  is chosen, a NEW kind is now a JSON key; see the checklist above.)**
- Decide the auto-enroll audit line deliberately: budgeted, or explicitly exempt with a comment stating
  that an audit record must not be suppressible and that its **volume** defence is `clipID` while its
  **count** defence is registration itself.
- Add a comment at the allocation site stating the invariant the move establishes: every caller-driven
  log line on this goroutine goes through the budget, and a new one that does not is a finding.
  **(2026-08-21: that invariant is not true until the sibling item ships too. Do not write the sentence
  before it is.)**

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
- **(2026-08-21) If a new `logKind` is added, it is counted AND published**, proven by reading it back
  through `GET /v1/server/counters`; and README's list of what the budget does and does not cover is
  updated to match.

## Related

- Source: `internal/worker/handler.go` (`Connect`'s allocation site, `authenticateAndRegister`,
  `autoEnrollAndRegister`'s audit line, `finishRegister`'s inventory-replace line, `applyInventory`)
- The budget this extends: `internal/worker/ingest_log_limiter.go`; the counters it now feeds:
  `internal/worker/ingest_log_counters.go`
- **The other unbudgeted class, a separate item:**
  [[bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget]]
- The slice that created the gap and shipped the injection half:
  `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md` section 2.6 (the per-site table),
  `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`
- The slice that made the kind names a response contract:
  `docs/retros/2026-08-21-silent-drop-observability-slice2.md`
- The reason "one line per connection" is not a bound:
  [[bug-2026-08-15-grpc-connection-admission-is-unbounded]] (**closed 2026-08-21**; see
  [[idea-2026-08-21-per-stream-log-budget-renewal-is-unpriced]] for what the caps do not bound)
- Adjacent, same hostname value: [[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]],
  [[bug-2026-08-12-auto-enroll-hostname-takeover]]

## Notes

Filed at **low** because there is no live exposure: both sites are one line per connection today and the
content defences shipped. The value of the item is that it converts a coincidence into a rule. The
2026-08-15 spec's own table is the evidence for why that is worth doing - it had to reason, per site,
about whether each line was caller-forceable, and got one of the rows right for the wrong reason (the
`finishRegister` row's stated justification was the wrong mechanism, corrected in a dated amendment
inside the spec). A rule at the allocation site replaces that per-site reasoning with one check.

**2026-08-21:** still low, and the census above is the argument for keeping it open rather than closing
it as theoretical. A README sentence asserting the rule this item would establish was written before the
item shipped, and it was false in two different ways at once. The rule is worth having precisely because
somebody will write that sentence again.

## 2026-08-24: this item's inventory is stale twice over

Two slices have moved under it since it was filed.

**The three sites it named are closed.** The 2026-08-24 handletaskstatus-pair slice brought all three
`handleTaskStatus` write-error lines inside the per-connection budget via three new `logKind`s. Measured
at that HEAD: **thirteen** `log.Printf` sites in `internal/worker/handler.go`, **eight** budgeted against
five before, and no existing site lost its budget.

**A fourth site exists that belongs to neither of this item's two classes.** `markWorkerOffline`'s
teardown line was added 2026-08-24 by the finishRegister slice. It runs once per connection teardown
with no `lim` on its call chain, so it is bounded by the connection admission caps rather than by
message volume - a third category this item's framing (pre-budget registration lines vs budgeted
message lines) does not have a slot for.

So the remaining scope is the registration-window lines only, and the item's own count should be
re-derived rather than trusted. Note also that the budget it would place them in is now known to be
drainable by the connection's own peer -
[[bug-2026-08-24-wire-keyed-dedupe-lets-a-peer-suppress-its-own-diagnostics]] - which is worth settling
first, since moving a line into a bucket a peer can empty is not obviously an improvement.

## 2026-08-25: a FIFTH category - a refusal path that logs nothing, by decision

The auto-enroll guards slice (`docs/superpowers/specs/2026-08-25-auto-enroll-guards.md`) added two new
refusal paths inside `authenticateAndRegister` - a hostname that already has a `workers` row, and a
fleet at `RELAY_AUTO_ENROLL_WORKER_CEILING` - and gave them **no log site at all**. They are counted
instead, on `Handler`, split by cause.

**This is an amendment, not a closure.** This item stays open and its remaining scope is unchanged: the
registration-window lines that DO exist are still outside the budget. What moves is the census's shape.
The item frames sites as budgeted or unbudgeted; 2026-08-24 already added a third category (bounded by
the connection caps rather than by message volume). This is a fourth: **deliberately absent**. The
reasoning is the item's own, run forwards - the limiter is allocated at `handler.go:350`, after
`authenticateAndRegister` has returned, so a `log.Printf` on an attacker-reachable refusal *cannot* be
budgeted where it stands, and the refusal is unboundedly repeatable by the same caller with the same
hostname. Not adding the site is a stronger outcome than budgeting it, and it needs no new `logKind`.

So the log-site count in `internal/worker/handler.go` is unchanged by that slice, and README's
`ingest_log_budget` list correctly did not move - checked by reading it, not by reasoning about it.

The cost is recorded where it is paid rather than here: a legitimately refused agent produces no
server-side line naming it, so the operator's naming signal is the AGENT's exit message. If the rule
this item proposes ever lands at the allocation site, it should have a slot for "this path deliberately
does not log", or it will read as an omission to whoever audits it next.
