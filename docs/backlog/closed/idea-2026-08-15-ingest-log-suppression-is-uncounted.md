---
title: Nothing counts what the ingest log budget dropped, so a flood is now invisible rather than noisy
type: idea
status: closed
created: 2026-08-15
closed: 2026-08-21
resolution: fixed
updated: 2026-08-21
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

**What the two genuinely share, and it is the expensive part:** a read surface. **(2026-08-21: it now
exists. See below.)**

### Update 2026-08-21 - the read surface exists, and this item's own preferred option is REFUTED

`docs/superpowers/specs/2026-08-21-silent-drop-observability.md` specced this item with its three
siblings in one sitting, and **slice 1 shipped the shared mechanism**
(`docs/retros/2026-08-21-silent-drop-observability-slice1.md`). This item is **slice 2 of four** - the
first consumer to add a counter - and it stays open: slice 1 added no counter anywhere in
`internal/worker`, so none of the acceptance criteria below is met.

**What now exists and must be extended, not reinvented:**

- **`GET /v1/server/counters`**, `auth(admin(...))`, in `internal/api/server_counters.go`. Admin-only
  deliberately: these numbers describe adversary activity and internal control state, so it is NOT
  modelled on `/v1/jobs/stats` or `/v1/workers/stats`, which are `auth`-only database censuses.
- **`api.CounterSources`** - a struct of nil-able per-subsystem source fields, set at the wiring
  boundary (`cmd/relay-server`'s `buildHTTPServer`). A **nil field means the section is ABSENT from the
  payload, never zero-valued.** A wired section of zeros means "this control ran and stopped nothing";
  an absent one means "not wired on this build or this replica". Do not collapse them, and do not make
  a snapshot method nil-tolerant to dodge a typed-nil panic - filter the typed nil at the wiring
  boundary where the concrete type is still visible.
- **The counts/levels contract.** `counts` are monotonic since `started_at`; `levels` are current. This
  section is counts only, in the shape the spec fixed:
  `{"ingest_log_budget": {"counts": {"deduped": {<kind>: N, ...}, "suppressed": {<kind>: N, ...}}}}`.
- **Per replica, per process, zeroed by a restart**, with `started_at` always present.
- **The import direction is FREE for this item.** `internal/api` already imports `internal/worker`
  (`server.go`), so the source interface may return the worker package's own snapshot type. That is
  **not** true for the watchdog sibling, which is why `CounterSources` is a struct of independent
  fields.

**REFUTED, and it is this item's own preferred option: "(b) accumulate in the limiter and flush once at
teardown ... is probably right".** Read it against this item's own Repro, which is a single **open**
stream sending 100,000 chunks. Under option (b) the operator sees **nothing at all for as long as the
attack continues**, and the numbers appear only after the attacker chooses to disconnect. An
observability control that is blind exactly during the attack it exists to reveal is not a control. It
would also add the one thing the limiter's comment is proud of not having: a teardown to get wrong.

**The shape, settled: `[5][2]atomic.Uint64` package-level in `internal/worker`** - five kinds, two arms
(deduped, budget-suppressed) - indexed by `logKind`, with a pointer threaded into `ingestLogLimiter`.
This item is right that "a `[5]uint64` is free, do not add a map" and **wrong about the location**: the
array belongs on a process-lifetime home, not on a stack local that dies with the connection. An atomic
add is not a lock - one locked exchange-add, no allocation, no map, no scheduling - so the limiter keeps
its documented **no-mutex** property verbatim, and cross-connection cache-line contention is bounded by
`RELAY_GRPC_MAX_CONNS` writers each doing far more expensive work on the same call.

**REFUTED on a detail that will otherwise be got wrong: `allow` has THREE `return false` paths, not
two.** The third is a `l == nil` fail-closed guard, deliberately unreachable in production (one
allocation site). **It must NOT be counted**: no event was suppressed on that path, because there was no
limiter. Counting it counts a phantom, and it is exactly the kind of thing an implementer adds while
"covering all the return-false arms".

**CONFIRMED: `ingestLogBurst = 16` and `ingestLogRefill = 10s` give 6 lines/min.** The Repro's "16 lines
immediately and then 6 per minute" is exact.

**ONE CONSEQUENCE TO HANDLE IN THIS SLICE, or it becomes wrong prose about correct code - the defect
class this project has led with for a dozen consecutive iterations.** `logKind`'s comment currently says
"Values are never persisted or sent anywhere, so they may be renumbered freely". Publishing per-kind
counts makes the **names** part of a response contract. Values may still be renumbered; **renaming a
kind changes a JSON key.** Amend that comment and pin the name mapping with a test in the same commit.

**Per worker or global: ANSWERED, globally, for all four items.** Per-worker keying is rejected **at the
increment site**: it needs a map write behind a shared lock on the recv goroutine, which is the thing
the standing constraint forbids, and it buys attribution the aggregate plus the existing per-connection
log lines already approximate. Note also that `metrics.Store` is the wrong home for any of these
counters - `Append` no-ops for an untracked worker and `Clear` deletes the entry on teardown, so a
counter there is destroyed by the disconnect that caused it. The `Metrics` **wiring** pattern is the
precedent; the type is not.

**One payload constraint to inherit.** Every non-integer value anywhere in the counters payload needs a
`counterPayloadExemption{why, typeOK, jsonOK}` argued in the same commit; `started_at` is the only
exemption today, and the pre-blessed `watchdog.counts.swept_by_worker` entry was deliberately
de-authorized. **This section is the first to ship a keyed object**, so read the residual carefully:
exemptions are shape-checked but **NON-DESCENDING** - both payload walks stop at an exempted path once
the predicate passes. If the `deduped`/`suppressed` objects are modelled as Go structs with one field
per kind (the recommended shape, since the kind set is compile-time closed), no exemption is needed at
all and the guards keep full reach. If they are modelled as a `map[string]uint64`, the exemption must do
the descending itself inside `typeOK`/`jsonOK` - checking key shape, value shape and cardinality. Slice
1 proved the difference is not theoretical: a `map[string]string` at an exempted path, with a
newline-injected RTL-override key and an IP-address value, passed both guards with zero failures.
**Prefer the struct.**

## Proposal

To be argued at spec time rather than adopted as written. **Superseded in part by the 2026-08-21
section above**; where the two disagree, verify the later reasoning against the code rather than
inheriting either.

- **Counters, not log lines.** Stating the obvious because the next person will "improve" a counter into
  a `log.Printf`, which hands back the exact vector [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]]
  closed. Put that sentence in the code.
- **Count both arms separately.** "Deduped" and "budget-suppressed" mean different things: the first is
  a healthy repeating failure, the second is either an attack or a misconfiguration. One number for both
  would be uninterpretable. **(2026-08-21: confirmed, and it is the `[2]` in `[5][2]`.)**
- **Where the counter lives is the hard part.** **(2026-08-21: option (b) REFUTED - blind for the whole
  duration of an ongoing flood. Option (a) taken, in the form that adds no mutex: package-level
  `atomic.Uint64`s, a pointer threaded in. The limiter's no-mutex property is preserved verbatim.)**
- **Per worker or global?** **(2026-08-21: ANSWERED - global at the increment site, for all four items.
  Per-worker keying needs a shared-map write on the recv goroutine.)**
- **Consider counting by kind.** Five kinds, and which one is flooding is exactly what an operator needs
  to know. A `[5]uint64` on the limiter is free. Do not add a map. **(2026-08-21: right about the array,
  wrong about the location - it must survive the connection.)**
- **Do not add a round trip, a goroutine, a queue or a lock to the recv path.** Standing constraint on
  this handler, unchanged.

## Acceptance / Done When

- A dropped log line increments a counter, split at minimum into deduped versus budget-suppressed,
  proven by a handler-layer test that drives a flood and reads the counters.
- The counters are readable by an operator through an endpoint, not only from a test. **(2026-08-21: the
  endpoint exists; this bullet now means the `ingest_log_budget` section is populated and served.)**
- `ingestLogLimiter` keeps its no-mutex, no-shared-state property on the hot path, or the change of that
  property is a deliberate, documented decision with a `-race` run behind it.
- No new log line anywhere on the ingest path, and no new DB round trip, goroutine, queue or lock on the
  recv goroutine.
- The counters cannot be read by an agent (server-side observability, never a response).
- The read surface is the same one [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] uses, or
  the divergence is deliberate and written down. **(2026-08-21: it is `GET /v1/server/counters`. The
  divergence budget is spent.)**
- **(2026-08-21) The `l == nil` arm is NOT counted**, and the reason is stated where the counter is
  incremented.
- **(2026-08-21) `logKind`'s "may be renumbered freely" comment is amended in the same commit**, and the
  kind-name-to-JSON-key mapping is pinned by a test, because the names become a response contract.
- **(2026-08-21) The per-kind objects are Go structs with a field per kind**, or - if a map is used
  instead - a `counterPayloadExemption` whose predicates DESCEND into it ships in the same commit.
- **(2026-08-21) An unwired section is ABSENT from the payload, not zero-valued**, matching the contract
  slice 1 fixed.

## Related

- Source: `internal/worker/ingest_log_limiter.go` (`allow`'s three `return false` paths - only two of
  which are events - and the type comment explaining why it is lock-free),
  `internal/worker/handler.go` (`Connect`'s allocation site and the five `lim.allow` call sites),
  `internal/metrics/store.go` (the existing per-worker seam, and the `Append`/`Clear` semantics that
  rule it out as a home)
- **The read surface, shipped 2026-08-21**: `internal/api/server_counters.go` (the payload contract for
  all four sections), `internal/api/server_counters_test.go` (`counterPayloadExemption` and the two
  payload walks), `internal/api/server.go` (the route), `cmd/relay-server/http_server.go` (the wiring
  boundary)
- Sibling on the complementary arm, to be shipped separately, and AFTER this one:
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]
- Siblings on the same shape: [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]
- The joint spec and the slice that settled the mechanism:
  `docs/superpowers/specs/2026-08-21-silent-drop-observability.md` (sections 3.3, 7.3, 10.2),
  `docs/retros/2026-08-21-silent-drop-observability-slice1.md`
- The slice that created this gap: `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md`,
  `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`
- Must not regress: the closed [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] - the reason this
  is a counter and not a log line
- The bound that makes the counters interpretable per fleet rather than per connection:
  [[bug-2026-08-15-grpc-connection-admission-is-unbounded]] (**closed 2026-08-21**; the caps now exist,
  and `idea-2026-08-21-per-stream-log-budget-renewal-is-unpriced` records what they do not bound)

## Notes

The rule worth recording: **a rate limiter that drops silently converts a noisy attack into a quiet
one.** That is a real improvement in cost and a real regression in detectability, and the second half
only shows up if somebody writes it down at the time. The limiter's own comments are careful about every
other trade it makes and say nothing about this one.

Filed at medium rather than low because the numbers are cheap and because the sibling item is already
waiting on the same endpoint decision. If the endpoint work happens for any other reason, both of these
become small. **(2026-08-21: the endpoint work has happened, and this item genuinely did become small -
it is one array, one pointer, two increments and one comment amendment. It is sequenced FIRST of the
three remaining consumers because it establishes the array cardinality class and creates the
`internal/worker` counters home the fence-rejection sibling then reuses.)**


## Resolution

Closed by the 2026-08-21 silent-drop-observability slice 2 (see
`docs/retros/2026-08-21-silent-drop-observability-slice2.md`).
`internal/worker/ingest_log_counters.go` (new) holds a `[kindCount][2]atomic.Uint64` as a value field
on `*worker.Handler`; `Connect` threads a pointer into each connection's stack-local
`ingestLogLimiter`, which records on both suppression arms - deduped and budget-suppressed, kept
separate because they mean opposite things - and nothing on its `l == nil` arm. `*worker.Handler`
satisfies `api.IngestLogBudgetSource`, and `buildHTTPServer` wires the same handler identifier
`RegisterAgentServiceServer` is called with, publishing
`ingest_log_budget.counts.{deduped,suppressed}` with one JSON key per kind on the admin-only
`GET /v1/server/counters`.

All ten Done-When bullets are met. The flood proof is a handler-layer test in the **default lane**
(`TestHandleTaskLog_ABadTaskIDFloodCountsTheDedupedArm`, 100 chunks, 1 log line, 99 counted drops)
because the bad-id arms return before `h.q` is touched; the counts are read back through the real
admin route; the numbers survive the connection and aggregate across connections, proven against a
real registered stream in the integration lane.

**Three of the item's own claims were refuted before any code was written.** The `[5]` array indexed
by `logKind` would have **panicked on the gRPC recv goroutine** - the constants are `iota + 1`, so
`kindInventory == 5` is out of range, and the obvious repair `kind - 1` is worse because `logKind` is
`uint8` and kind 0 wraps to 255; shipped as `[kindCount][2]` with a sentinel and a fail-closed bounds
check. The package-level home was refuted on test isolation and on there being no object for
`CounterSources` to hold. And "values may still be renumbered freely" was wrong in **both**
directions, not just the names half - the values are array indices now, so they must stay a dense run.

**`ingestLogLimiter`'s no-mutex property is preserved verbatim; its no-shared-state property is not,
and that is the deliberate change this item required.** The `drops` pointer is the one shared thing in
the type, written only by atomic adds, documented at the type, at the field and at the allocation
site, with `-race` green module-wide in a Linux container and a dedicated concurrency test whose
`-race` half kills the plain-`uint64` mutation 10/10 at every core count measured.

**What this section does NOT cover, stated because a counter that reads zero is worse than no
counter.** (1) It counts *log lines the budget dropped*, never *diagnostics lost*: `handleTaskStatus`'s
`pgx.ErrNoRows` `GetTask` is `&&`-short-circuited **before** `allow` and is therefore never counted -
correctly, because the decision not to log was made upstream of the budget. (2) The `AppendTaskLog`
fence-rejection arm never consults the budget at all; that counter is
[[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]], slice 3. (3) Six reachable `log.Printf`
sites on the recv goroutine are outside the budget entirely - three registration-time
([[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]) and three inside
`handleTaskStatus` with the limiter already in scope
([[bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget]], filed by this slice
after its own README claimed the opposite). README now names all three exclusions. (4) No `levels`
half, no per-worker split, per replica, zeroed by a restart, and never reachable by an agent.

One consequence to carry forward: **the kind names are now a response contract.** Adding a kind means
a const inside the sentinel, an array cell, a `worker.IngestLogDropsByKind` field, a `byKind` line, an
`api.ingestLogKindCounts` field and tag, a line in `ingestLogKindCountsFrom`, two
`counterPayloadLeaves` entries and the kinds list in `TestServerCounters_ReportsTheIngestLogSnapshot`.
A fully correct sixth kind was measured to leave all three packages green while being published under
no JSON key; `TestIngestLogKindCountsPublishesEveryWorkerSideField` is what makes that RED.
