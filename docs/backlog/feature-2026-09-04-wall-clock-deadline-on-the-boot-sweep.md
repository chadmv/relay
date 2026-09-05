---
title: A wall-clock deadline on the boot sweep, the only thing that would bound its duration
type: feature
status: open
created: 2026-09-04
priority: medium
source: Decision 4 of docs/superpowers/specs/2026-09-04-per-owner-schedule-cap.md, which settled the duration question OUT of that slice
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
