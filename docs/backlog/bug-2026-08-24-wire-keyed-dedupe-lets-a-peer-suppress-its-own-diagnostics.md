---
title: A wire-keyed dedupe key lets a connection drain the shared log budget and suppress its own other seven kinds
type: bug
status: open
created: 2026-08-24
priority: medium
source: Phase 4 security lens of the 2026-08-24 handletaskstatus-pair slice; reproduced against real Postgres
---

# A wire-keyed dedupe key lets a connection suppress its own diagnostics

## Summary

`ingestLogLimiter` is one bucket of `ingestLogBurst` (16) tokens per `Connect`, shared by all eight
log kinds. Seven of the eight carry no wire value, so each costs at most one token per dedupe window
and a peer cannot drain the bucket through them.

`kindTaskLogPersist` is the exception: its key carries `chunk.Epoch` straight off the wire, so distinct
keys are free **if** a peer can produce a non-`pgx.ErrNoRows` `AppendTaskLog` error. It can. A `\x00`
byte in `chunk.Content` makes Postgres reject the parameter at Bind (SQLSTATE 22021) **before the
fence's `WHERE` is ever evaluated**, so neither the task id nor the epoch constrains anything - and
`chunk.Content` is entirely agent-chosen.

Sixteen such chunks at distinct epochs drain the burst. Roughly six a minute hold it at zero for the
life of the connection, after which every other kind's `lim.allow` returns false.

## Repro / Symptoms

Reproduced twice independently against a real `postgres:16`, once by a review lens and once by the
implementing engineer:

1. Open an agent stream. Send 16+ `TaskLogChunk` messages whose `content` contains a `\x00` byte, each
   with a different `epoch`.
2. Observe `ingest_log_budget.counts.suppressed.task_log_persist` climbing while `deduped` stays zero -
   each malformed chunk mints a fresh dedupe key rather than collapsing onto one.
3. Send any message that would produce one of the other seven kinds' log lines. It is suppressed. The
   identical message on a fresh connection is not.

## Context

The 2026-08-24 handletaskstatus-pair slice brought three previously-unbudgeted `handleTaskStatus` log
sites inside this budget - correctly, since an agent streaming status updates during a database outage
drove unbounded volume through them. But at `origin/main` those three lines fired **unconditionally**,
so an operator was guaranteed to see them when an agent's status writes were failing. After that slice,
an agent *causing* those failures can pre-drain its own bucket and the server logs nothing about it.

Scope, stated honestly: the bucket is per connection and dies with it, so an agent suppresses only its
**own** diagnostics and cannot silence the fleet. The suppression is itself counted and admin-visible.
That is why this is medium rather than high - but it is anti-forensics, and the party with the motive
is exactly the party with the ability.

The slice half-saw this. `internal/worker/ingest_log_limiter_test.go` notes that the test is "only
meaningful while `len(kinds) <= ingestLogBurst`" - the shared-bucket fact was noticed in a test and
never carried across into "can a peer hold it at zero".

## Proposal

Cheapest first; the first two are alternatives, not a sequence.

- **Drop `epoch` from the `kindTaskLogPersist` key.** The comment beside it says the wire keying is "on
  purpose and is NOT the bound" - true for the flood it was written about, but that bound is now shared
  with three diagnostics that previously had none.
- **Gate `lim.allow` on the error not being a parameter-encoding rejection** (SQLSTATE 22021 / 22P05),
  so a caller-supplied byte cannot mint a fresh dedupe key.
- **Give the three `status_*` kinds their own small bucket**, so the assurance the README now implies is
  one a peer cannot revoke.

Whichever is chosen, the acceptance criterion is the repro above: after the fix, a connection that
floods malformed chunks must not be able to suppress its own `status_*` lines.

## Acceptance / Done When

- A test drives the repro and asserts the other kinds still log after the flood.
- `ingest_log_budget`'s README bullet stops needing the caveat this item's shipping added to it.

## Related

- `internal/worker/ingest_log_limiter.go` - the bucket, `ingestLogBurst`, `allow`
- `internal/worker/handler.go` - the `kindTaskLogPersist` key and the three `status_*` sites
- [[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]] - the slice that surfaced it
- [[bug-2026-08-24-failclaimedtask-is-a-ready-uncounted-fence-site]] - the other thing that slice left open
