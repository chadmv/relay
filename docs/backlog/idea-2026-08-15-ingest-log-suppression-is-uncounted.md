---
title: Nothing counts what the ingest log budget dropped, so a flood is now invisible rather than noisy
type: idea
status: open
created: 2026-08-15
priority: medium
source: Phase 6 of the 2026-08-15-tasklog-err-limiter-keying slice; the diagnosability cost that slice accepted
---

# Nothing counts what the ingest log budget dropped, so a flood is now invisible rather than noisy

## Summary

`ingestLogLimiter.allow` (`internal/worker/ingest_log_limiter.go`) returns `false` on two distinct
paths and **records nothing on either**:

1. **Dedupe collapse** - the key was logged inside `ingestLogDedupeWindow`, so this occurrence is
   folded into the earlier line.
2. **Budget suppression** - the key is new (or re-armed) but `tokens == 0`, so the line is dropped
   entirely and, deliberately, the key is not even recorded.

Both are correct. Both are silent. The operator-visible signature of the attack this limiter defends
against is therefore **fewer log lines than normal**, which is indistinguishable from a healthy fleet.
Before the slice, a flood announced itself at one line per message; after it, a flood settles at 6
lines per minute per connection and nothing anywhere says "and 40,000 more were dropped".

This applies to all five kinds - `kindTaskLogPersist`, `kindBadTaskIDLog`, `kindBadTaskIDStatus`,
`kindStatusGetTask`, `kindInventory` - across three handlers (`handleTaskLog`, `handleTaskStatus`,
`handleInventoryUpdate`).

## Repro / Symptoms

Open one `Connect` stream and send 100,000 `TaskLogChunk`s carrying an embedded NUL in `Content` for a
task the sender legitimately owns. The bind-time `22021` fires on every one. The log shows 16 lines
immediately and then 6 per minute. Nothing in the process, in any endpoint, or in any metric indicates
that ~99,900 log lines were suppressed, or that one connection is responsible for all of them.

The dedupe arm has a milder but more common version: a single task streaming binary output produces one
line per 5-minute window, and an operator reading that line has no way to tell whether it represents 3
chunks or 3 million.

## Context

**Why this is a sibling of [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] and not an
amendment to it.** The two items are genuinely adjacent, and it is worth being precise about the split,
because the 2026-08-15 spec had to refute a ROADMAP rationale that assumed they were one thing.

That item is scoped to `handleTaskLog`'s `pgx.ErrNoRows` arm - a **chunk rejected by the fence**. This
item is the **complementary arm** of the same `if`: a chunk (or a status update, or an inventory update)
whose error was real and whose **log line** was dropped. No input executes both. They count different
nouns (rejections versus suppressed lines), they live in different branches, one of them covers three
handlers, and their acceptance criteria do not overlap.

Amending that item to cover this would silently widen it from one arm in one handler to five kinds in
three handlers, and would falsify its own Done-When ("a rejected chunk increments a counter, proven by a
handler-layer test that reads the counter across a rejection and a success"). This project keeps finding
items that are wrong about their own scope; growing one by amendment is how that happens.

**What the two genuinely share, and it is the expensive part:** a read surface. Verified at `ee88de0`
and unchanged: `internal/api/server.go` routes `GET /v1/config`, `GET /v1/jobs/stats`,
`GET /v1/workers/stats` and `GET /v1/workers/{id}/metrics`, and nothing that would carry a server-wide
counter. Both items therefore either extend `GET /v1/workers/stats` or depend on
[[feature-2026-08-09-server-info-allowlist-endpoint]]. **They should be specced in one sitting and
shipped as two slices**, so the read surface is designed once.

## Proposal

To be argued at spec time rather than adopted as written.

- **Counters, not log lines.** Stating the obvious because the next person will "improve" a counter into
  a `log.Printf`, which hands back the exact vector [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]]
  closed. Put that sentence in the code.
- **Count both arms separately.** "Deduped" and "budget-suppressed" mean different things: the first is
  a healthy repeating failure, the second is either an attack or a misconfiguration. One number for both
  would be uninterpretable.
- **Where the counter lives is the hard part.** `ingestLogLimiter` is a **stack local in `Connect` with
  no mutex**, by design, and that is the property that lets it be lock-free on the recv goroutine.
  Counters that survive the connection must therefore be either (a) atomics on a shared struct that the
  limiter is handed a pointer to, which adds the first cross-connection write to this path, or (b)
  accumulated in the limiter and flushed **once at teardown**, which costs nothing on the hot path but
  loses the numbers for a connection that is still open. Option (b) is probably right and is worth
  arguing explicitly; option (a) must not reintroduce a shared mutex.
- **Per worker or global?** Per worker is the useful diagnostic ("worker X is dropping 40k lines/min")
  and matches where `metrics.Store` already keys. Global is cheaper. Note the same open question in the
  sibling item and answer it once for both.
- **Consider counting by kind.** Five kinds, and which one is flooding is exactly what an operator needs
  to know. A `[5]uint64` on the limiter is free. Do not add a map.
- **Do not add a round trip, a goroutine, a queue or a lock to the recv path.** Standing constraint on
  this handler, unchanged.

## Acceptance / Done When

- A dropped log line increments a counter, split at minimum into deduped versus budget-suppressed,
  proven by a handler-layer test that drives a flood and reads the counters.
- The counters are readable by an operator through an endpoint, not only from a test.
- `ingestLogLimiter` keeps its no-mutex, no-shared-state property on the hot path, or the change of that
  property is a deliberate, documented decision with a `-race` run behind it.
- No new log line anywhere on the ingest path, and no new DB round trip, goroutine, queue or lock on the
  recv goroutine.
- The counters cannot be read by an agent (server-side observability, never a response).
- The read surface is the same one [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] uses, or
  the divergence is deliberate and written down.

## Related

- Source: `internal/worker/ingest_log_limiter.go` (`allow`'s two `return false` paths, and the type
  comment explaining why it is lock-free), `internal/worker/handler.go` (`Connect`'s allocation site and
  the five `lim.allow` call sites), `internal/api/server.go` (the route table, which has no server-wide
  counters endpoint), `internal/metrics/store.go` (the existing per-worker seam)
- Sibling on the complementary arm, to be specced together and shipped separately:
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]
- Possible dependency for the read surface: [[feature-2026-08-09-server-info-allowlist-endpoint]]
- The slice that created this gap: `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md`,
  `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`
- Must not regress: the closed [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] - the reason this
  is a counter and not a log line
- The bound that makes the counters interpretable per fleet rather than per connection:
  [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]

## Notes

The rule worth recording: **a rate limiter that drops silently converts a noisy attack into a quiet
one.** That is a real improvement in cost and a real regression in detectability, and the second half
only shows up if somebody writes it down at the time. The limiter's own comments are careful about every
other trade it makes and say nothing about this one.

Filed at medium rather than low because the numbers are cheap and because the sibling item is already
waiting on the same endpoint decision. If the endpoint work happens for any other reason, both of these
become small.
