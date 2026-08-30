---
title: parseUUID formats the whole rejected input into an error that is immediately discarded, so a 1 MiB ?job_id= costs ~6 ms and ~9 MB
type: idea
status: open
created: 2026-08-30
priority: low
source: Phase 4 review of the 2026-08-30 ?job_id= canonicalisation slice; the slice added the second instance, ?task_id= was already the first
---

# `parseUUID` formats the whole rejected input into a discarded error

## Summary

On the REJECTED path, `pgtype.UUID.Scan`'s `default:` arm formats the entire input into an error
string, and `internal/api.parseUUID` wraps that again with `%q`. Both errors are discarded:
`canonicalJobIDFilter` returns `raw` on any error and `handleEvents`' `?task_id=` arm turns it into
a fixed `"invalid task id"`. The work is proportional to the request line, and none of it is read.

## Repro / Symptoms

Measured in `internal/api` on a 1 MiB (`1<<20`) all-`a` input, `-benchtime 200x`, Ryzen 9 5900X:

```
BenchmarkZZScanOnly1MiB-24               219664 ns/op    2115018 B/op     6 allocs/op
BenchmarkZZParseUUID1MiB-24             5910658 ns/op    8924362 B/op    15 allocs/op
BenchmarkZZCanonicalJobIDFilter1MiB-24  5834567 ns/op    8924541 B/op    15 allocs/op
BenchmarkZZPassthrough1MiB-24                 1 ns/op          0 B/op     0 allocs/op
```

Read those four rows in order. `Scan` alone is 0.22 ms / 2.1 MB; `parseUUID`'s one `%q` wrap adds
**27x the CPU and 4x the allocations on top of it**, and dominates. `canonicalJobIDFilter` is
`parseUUID` plus nothing measurable, because the render only runs on success.

**State the baseline honestly.** The fourth row is what `?job_id=` did before 2026-08-30: the raw
string was used as the filter untouched, at zero cost. So this is not a regression measured against
a comparable predecessor - the truthful framing is that `?job_id=` now pays a cost `?task_id=` on
the same handler has always paid, on the same input, one parameter over. Two instances of one shape,
not a new exposure: authenticated-only, no new asymptotic class, and bounded by whatever bounds the
request line.

(A Phase 4 lens reported 1.1 ms / 352 KB -> 4.6 ms / 11.4 MB for the same comparison. Those numbers
did not reproduce here and the "before" figure has no counterpart in the code this replaced, which
allocated nothing. The rows above are the ones to trust; re-measure before quoting either.)

## Proposal

Cheap fix: a length pre-check in front of `Scan` - if `len(raw)` is neither 32 nor 36, it cannot
parse, so return early without touching `Scan`.

**Do not take it without solving the coupling first, which is why this is an idea and not a bug.**
That pre-check restates pgx's grammar in a second place. A pgx upgrade that widened `Scan` to accept
a new length would be silently NARROWED by our own guard, and the whole point of
`TestCanonicalJobIDFilter` is that it is the one place a pgx grammar change is caught - and it would
stay green, because every row in its table is already 32 or 36 bytes. The mitigation would blind the
instrument that exists to detect the thing it depends on.

So closing this needs a test row that a narrowing would redden: a spelling that a hypothetical wider
`Scan` accepts and the length guard would reject. Deriving `TestCanonicalJobIDFilter`'s accepted set
from `Scan` itself rather than from a literal table would do it, at the cost of the table's value as
a hard-coded statement of today's grammar. Decide that tradeoff before writing the guard.

## Acceptance / Done When

- The rejected path no longer formats the input, proven by a benchmark whose `B/op` is independent
  of input size.
- A test reddens if the guard's accepted lengths and `pgtype.UUID.Scan`'s disagree in EITHER
  direction. Proven once, by widening one and observing the RED.

## Related

- `internal/api/server.go` (`parseUUID`), `internal/api/events.go` (`canonicalJobIDFilter`,
  `handleEvents`'s `?task_id=` arm)
- `internal/api/events_test.go` (`TestCanonicalJobIDFilter` - the table this interacts with)
- `internal/api/ratelimit.go` and the HTTP admission bounds that decide how large a request line can
  be at all: [[bug-2026-08-23-http-listener-has-no-admission-bounds]]
