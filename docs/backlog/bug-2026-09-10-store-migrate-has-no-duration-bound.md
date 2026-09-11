---
title: store.Migrate has no duration bound and no knob, and it gates the HTTP listener
type: bug
status: open
created: 2026-09-10
priority: medium
source: Found by the pre-listener enumeration in the startup-validation deadline spec (PR #210), which corrected a sibling spec calling it bounded
---

# store.Migrate has no duration bound and no knob, and it gates the HTTP listener

## Summary

`store.Migrate` runs on startup ahead of everything, and nothing bounds how long it takes.
`RELAY_DB_STATEMENT_TIMEOUT` does not reach it, deliberately, so a long-running DDL statement delays
the HTTP listener without limit and without a way for an operator to cap it.

## Context

A sibling spec's pre-listener enumeration listed this step as "bounded". That is true of its row COUNT
and false of its DURATION, and the distinction is the whole point of the enumeration.

**The exclusion is deliberate and documented**, which is why this is a question rather than an
oversight. `applyStatementTimeout`'s own header states that `store.Migrate` opens its own connection
through `golang-migrate` before `main` ever calls `pgxpool.ParseConfig`, so "a `CREATE INDEX` that runs
for minutes on a large table is unaffected" - and that is the intended behaviour, since a migration
killed halfway is worse than a slow one.

So the tension is real: the same property that protects a long migration from a statement timeout also
means an operator has no bound on boot time, and no signal distinguishing a slow migration from a hung
one.

Note `migrateDSN` is derived from the main DSN by prefix rewriting, so a `statement_timeout` carried in
the DSN WOULD reach migrations - which is exactly why the timeout is applied in Go instead, and why
documenting "put it in your DSN" would be wrong.

## Proposal

Sketch only. A timeout is probably the wrong instrument for the reason above. More likely useful: log a
line when a migration starts and when it finishes, with its name and elapsed time, so a slow boot is
attributable and an operator can tell progress from a hang. Decide whether anything should be bounded
at all before deciding what.

## Acceptance / Done When

- A slow boot attributable to a migration is distinguishable from a hung one, from the log alone.
- If any bound is added, the reason a killed-halfway migration is acceptable is written down.
- The pre-listener enumeration is corrected wherever it calls this step bounded.

## Related

- `internal/store/` (`Migrate`), `cmd/relay-server/dbtimeout_config.go` (`applyStatementTimeout` and
  its header), `cmd/relay-server/main.go` (the call, first on the boot path)
- `docs/superpowers/specs/2026-09-10-startup-validation-deadline.md` - the enumeration
