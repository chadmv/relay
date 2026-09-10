---
title: A wall-clock deadline on the boot sweep, the only thing that would bound its duration
type: feature
status: closed
created: 2026-09-04
priority: medium
source: Decision 4 of docs/superpowers/specs/2026-09-04-per-owner-schedule-cap.md, which settled the duration question OUT of that slice
closed: 2026-09-10
resolution: fixed
---

# A wall-clock deadline on the boot sweep, the only thing that would bound its duration

## Summary

Nothing bounds how long `ValidateStoredSpecsOnStartup` runs before `ListenAndServe`. Keyset paging bounded its peak MEMORY to one page; a per-owner schedule cap bounds its STARTING work set. Neither bounds its DURATION.

## Context

This is the residue of the boot-sweep item's duration question, carried out of the per-owner cap slice deliberately rather than left implicit. That spec's Decision 4 settled it: **a count cap is not a duration bound and its acceptance criteria must not claim to be one.**

Two reasons duration stays open after the cap lands:

- **The cap bounds N0, not N.** The paged sweep takes a fresh MVCC snapshot per page, so a row INSERTed above the cursor joins the work set mid-pass with probability equal to the unswept fraction of the key space. An owner sitting at the cap can `DELETE` one schedule and `POST` another indefinitely. The pass still converges, since the unswept fraction only shrinks - so this is duration amplification, not non-termination.
- **The starting set itself is not bounded by the cap alone.** It is `(rows existing when the cap lands) + owners x cap`, grandfathering leaves the first term untouched, and the owner population is itself unbounded where `RELAY_ALLOW_SELF_REGISTER` is on.

**A page ceiling is the tempting alternative and it is the wrong instrument**: it bounds round trips, not seconds. One slow page under load costs more wall clock than many fast ones.

## Proposal

Sketch only; the hazard below is the design work.

A wall-clock deadline on the sweep, after which the boot proceeds and the remainder is handled elsewhere.

**The opening constraint, and it is what makes this a spec rather than a ticket: a truncated sweep under-reports.** The sweep exists to tell an operator which stored specs no longer validate. A deadline means some specs are never checked on that boot, and the honest failure mode is a boot that says "N invalid specs" when the true number is higher and unknown. That is worse than a slow boot if it is not stated plainly - a partial count read as a total is the shape this project calls a lossy aggregate, and it must disclose its loss where it is READ, not only where it is computed. Decide what the boot line says when the deadline fires, before deciding the number.

Also decide where the remainder goes: a later pass, the ticker, or nowhere.

## Acceptance / Done When

- The sweep cannot delay `ListenAndServe` past a bound that does not depend on the number of stored schedules.
- A truncated sweep says so wherever its result is read, and never presents a partial count as a total.
- What happens to unswept specs is decided and documented.

## Related

- `internal/schedrunner/startup_validation.go` (`ValidateStoredSpecsOnStartup`)
- [[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]] - the parent; its memory half shipped
- [[feature-2026-09-04-per-owner-schedule-cap]] - bounds the starting work set, explicitly not the duration
- [[bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded]] - the other unbounded pre-listener read, found beside this

## Resolution

Fixed in PR #210, squashed as `fb99ca02`. `RELAY_STARTUP_VALIDATION_DEADLINE`, default 45s,
deliberately above `RELAY_DB_STATEMENT_TIMEOUT`'s 30s so one slow statement cannot eat the pass. The
signature is now
`ValidateStoredSpecsOnStartup(ctx, q, budget time.Duration) (SweepResult, error)`.

**This item's second acceptance criterion is not achievable as worded, and the replacement is sharper.**
"A truncated sweep says so wherever its result is read" would require writing to every row the deadline
declined to check - the exact unbounded work being cut. What is both achievable and checkable: the sweep
only ever ADDS records and has no clearing sibling, so **truncation produces false negatives and can
never produce a false positive. A recorded failure stays trustworthy; only its ABSENCE loses meaning.**

The item also names one reader of the count. There are four, and the one that matters is
`GET /v1/scheduled-jobs/stats`' `failing` key, which README documents as "Not windowed" - the literal
instance of a partial count read as a total that this item's own opening constraint is about. It is a
live `count(*)` rather than the sweep's number and already under-counts for a pre-existing reason, so
this slice adds a sentence rather than a fix.

**A page ceiling is confirmed the wrong instrument, with a stronger argument than the item's.** Not
because of load, but structurally: `job_spec` up to 1 MiB and 1-to-101 statements per page mean per-page
cost varies by four orders of magnitude. And it carries the identical under-reporting cost while buying
strictly less.

**The mechanism was chosen by a guard.** `TestSchedrunnerStartupSweepIsWiredInOrderByMain` parses
`main`'s own statement list and requires exactly one call to the sweep, so the obvious encapsulation - a
local helper holding the bounded context - makes that count zero. The budget therefore travels as a
value and the timeout is created inside the sweep, which also keeps a bounded context out of `main`'s
scope where the next five statements hand `ctx` to long-lived goroutines.

`Truncated` is derived from the child context's `Err()` and set at both non-nil return sites, never in a
`defer` over named returns: a `defer` merges the page-query site with the row-loop site so no mutation
can distinguish them, and it would mark a complete pass truncated when the budget expires after the last
short page. A zero budget is an EXPIRED deadline rather than an absent one, so a zero value cannot
restore the unbounded boot.

**The remainder goes nowhere, argued on the sweep's own terms** rather than inherited from the sibling
reconcile slice: a pass resumed after the listener would run concurrently with the runner, and
`AdvanceScheduledJob` clears `last_error` while touching none of the three columns
`RecordScheduledJobFailure` fences on, so a row fired between that read and write gets its fresh clear
stamped over with a stale verdict. That needs a fence the tree does not have, and the follow-up item
must not prescribe one.

**Measured, and the default did not move.** Loopback, 2000 rows per regime: all-healthy-small 85,726
rows/s; all-healthy fat-spec (19,115 bytes/row) 2,751 rows/s, about 123,780 rows in 45s; all-broken 802
rows/s, about 36,077 in 45s. The re-scope threshold was 10,000 fat-spec rows. These are loopback numbers
on an idle machine - a remote database adds a round trip per page and per UPDATE, which is the reason the
knob exists.

Review added a guard the spec had forbidden: an assertion on the call's third argument is RED at HEAD
because the call had two, and `main` holds at least four `time.Duration` locals that all compile in that
position. Verified by swapping `staleAfter` in and watching the new guard fail.

`store.Migrate` remains the one pre-listener step with no duration bound and no knob -
`applyStatementTimeout`'s header records that the statement timeout deliberately does not reach
migrations. Filed separately.
