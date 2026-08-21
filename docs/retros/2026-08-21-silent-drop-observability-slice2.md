---
date: 2026-08-21
topic: silent-drop-observability-slice2
slice: 2026-08-21-silent-drop-observability (slice 2 of 4)
branch: claude/pr-merge-session-961184
range: origin/main..HEAD (backend only; Go plus README; zero SQL, zero migration, zero proto, zero generated file, zero files under web/; green, not yet merged, no PR opened at the time of writing)
closes: idea-2026-08-15-ingest-log-suppression-is-uncounted
enables-only: idea-2026-08-14-tasklog-fence-rejection-is-unobservable, idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced
---

# Session Retro: 2026-08-21 - the mechanism built because controls silently stop reporting shipped a counter that could silently stop counting

**TL;DR:** `internal/worker/ingest_log_counters.go` (new) holds a `[kindCount][2]atomic.Uint64` as a
**value field on `*worker.Handler`**; `Connect` threads a pointer to it into each connection's
stack-local `ingestLogLimiter`, which records on its two suppression arms and nothing on its
`l == nil` arm; `*worker.Handler` satisfies `api.IngestLogBudgetSource` and `buildHTTPServer` wires
the same handler identifier `RegisterAgentServiceServer` is called with, publishing an
`ingest_log_budget` section of the admin-only `GET /v1/server/counters`. Unit 574 -> 594 top-level;
all three touched integration packages green; `-race` clean module-wide in a Linux container.

**This is the direct sequel to slice 1 and the third slice of this batch.** Slice 1's durable rule was
"a guard that matches a SPELLING is evadable by respelling; a guard that counts a PROPERTY is not".
This slice found the next rung down, and it is recursive.

---

## The headline, and it is recursive: the observability mechanism shipped a counter that could silently stop counting

A **fully correct sixth log kind** - the const declared inside the sentinel, a real `lim.allow` call
site, its own array cell, a field on `worker.IngestLogDropsByKind`, a line in `byKind`, the dense-run
guard's list updated - left **all three packages green** with `go vet -tags integration` clean, while
`internal/api`'s hand-written `ingestLogKindCountsFrom` never mapped it. The drops were recorded on
the recv path and **published under no JSON key**.

**Found independently by all three reviewers, each with a different sixth kind.** That is not a
coincidence; it is the shape being obvious once anyone asks "what compares these two types".

`counterPayloadLeaves` **structurally cannot catch it**, and the reason is worth stating precisely
because it looks like the guard that should have. It is an `ElementsMatch` against a fixed list
derived from the api-side response struct, so it reddens on an **EXTRA** api leaf and never on a
**MISSING** worker-side one. It is a contract check on this package's own payload; the missing field
is not in the payload to be noticed.

**The sharpest framing came from the engineer at the end, and it is the durable form:**

> **This defect and the `buildHTTPServer` forwarding defect below are THE SAME SHAPE ONE LAYER APART -
> a hand-written copy between two types with nothing comparing their arity.** One copies
> `worker.IngestLogDropsByKind` into `api.ingestLogKindCounts`; the other copies
> `httpServerDeps.agentHandler` into `api.CounterSources.IngestLogBudget`. Neither had a cardinality
> check; every other boundary in the chain did.

A security lens walked every other boundary and confirmed that: kinds against the array
(`TestIngestLogKindsAreADenseRunFromOne`), array against the published struct
(`TestIngestLogCounters_EveryKindIsPublishedDistinctly`'s final `require.Len`), `CounterSources`
against the wiring rows (`TestServerCountersIsWiredByMain`), response against the handler's branches
(`counterPayloadLeaves`). **All had cardinality checks. This was the one that was missed, in a slice
that used the technique three times.**

The fix is `TestIngestLogKindCountsPublishesEveryWorkerSideField`, `internal/api` being the one place
both types are visible. **Cardinality alone is enough and the comment says why nobody should "improve"
it into a name comparison:** `ingestLogKindCountsFrom` assigns BY NAME, so a worker-side rename is
already a compile error there. Only the arity can drift silently.

> **A completeness claim cannot be checked by reading the thing it is about.** Every hand-written copy
> between two types needs something comparing their arity, and the guard must live where both types
> are visible - which is usually neither type's own package.

---

## Conductor mutation discipline: a mutation that reddens something is not yet evidence for the claim you are making

It took **four attempts** to isolate the headline defect, and the first three were real reds for the
wrong reason:

1. The added field duplicated an existing cell, so the red was about the duplicate, not the missing
   mapping.
2. The dense-run guard's own hardcoded `run` list was stale, so the red was about the list.
3. (Same class, different placement.)

Only the fourth attempt produced a failure whose **mechanism matched the claim being made** - a kind
counted on the recv path and published under no key, with everything else green.

> **A mutation that reddens something is not yet evidence for the claim you are making.** The kill
> must fail through the mechanism being asserted, or the mutation has proved a different test's value.
> This sits next to the existing lessons that a mutation proof must leave a test behind, that its
> strength depends on the poisoned input's POSITION, and that a battery needs a green baseline.

---

## A guard whose failure message names a condition that is SATISFIED at the moment it fires

Adding a correct sixth kind reddens `TestIngestLogKindsAreADenseRunFromOne` with:

> `kindCount must be the sentinel immediately after the last kind`

**And it is.** The const block is correct; the real cause is the test's own hardcoded `run` list,
which has not been updated. An author who follows the message inspects a correct const block and is
misled.

This matters more than an ordinary bad message because of **where it sits in the trail**: it is the
first of three guards that fire on a new kind, and the work the author still owes is on the API side
two packages away. A message that sends them to the wrong file first is a message that makes the
remaining two failures look like noise.

> **A guard's failure message must name the thing that is WRONG, not the property being defended.**
> The test asserts two things - the constants are dense, and this list enumerates them - and only the
> second can fail while the first reads correct.

---

## The engineer's own documented measurements were BACKWARDS, and a lens re-measured rather than accepting them

`TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact`'s comment originally claimed the
`-race` half kills the plain-`uint64` mutation **1/10 at `-cpu=1`** and that the test is "very nearly
inert" there.

Re-measured in a container pinned to one CPU (`docker run --cpus=1 --cpuset-cpus=0`): **10/10, every
one a `WARNING: DATA RACE`.** TSan's vector clocks do not need true parallelism to see two sibling
goroutines writing one word with no happens-before edge between them.

It is the **EXACTNESS** assertion that is inert at one CPU: **0/20 at `-cpu=1`**, 12/20 at `-cpu=2`,
13/20 at `-cpu=4`. Green baseline confirmed: unmutated, 0/10 races and 0/20 exactness failures.

Two things make this worth a heading rather than a line:

- **As written, the comment licensed a maintainer on a constrained runner to dismiss the test.** That
  is slice 1's "prose that misidentifies WHICH artifact is load-bearing" defect class, pointed at the
  *strong* half instead of the weak one.
- **The lens also found the original "kills" were measured WITHOUT removing the `.Load()` calls**, so
  they were **compile errors, not behavioural kills** - this repo's own recorded distinction, applied
  to its own author, one slice after it was recorded.

The corrected comment states both halves with their numbers, says the container detail is
load-bearing, and records that a `done`-channel busy-spin reader was tried and is worse (it starves
the writers and drops the `-cpu=2` race kill from 10/10 to 4/10).

---

## A guard that fails closed on one argument and panics on another, with a comment implying it covers both

`ingestLogCounters.record`'s bounds check reads:

```go
i := int(k)
if i <= 0 || i >= len(c.n) || arm < 0 || arm >= ingestDropArms {
    return
}
```

**It never dereferences its receiver.** `len(c.n)` has an array-typed operand, so it is a compile-time
constant. On a nil receiver an **out-of-range** kind therefore returns safely while an **in-range**
one - the shape production takes - **panics on the gRPC recv goroutine**, which `Connect` does not
recover and grpc-go does not recover either. That is the single failure mode this file exists to
avoid.

Unreachable today (`Connect`, `newIngestLogLimiter` and `shimLimiterFor` all pass `&h.ingestDrops`,
and `Handler`'s field is a value so its zero value works), but `newIngestLogLimiterAt(now, nil)` and a
bare `&ingestLogLimiter{...}` both compile, and `allow` already guards `l == nil` without guarding
`l.drops == nil`. Fixed with one `if c == nil` and a test.

> **The interesting part is WHY the guard looked complete: the safe-looking branch was safe for a
> reason unrelated to the guard.** An out-of-range kind on a nil receiver survived because of Go's
> constant-folding of `len` over an array type, not because the check handled it. When a check appears
> to cover two arguments, ask which of them it actually touches.

---

## Guard evasions continued and the ladder held - three more, total now twelve

All three were caught, and the ladder from slice 1 (make it unwritable, then execute it, then count a
property, then match a shape) is what caught them.

**(a) `buildHTTPServer` was never checked to FORWARD the handler it was given.** Substituting a fresh
`worker.NewHandler` for `d.agentHandler` compiled, vetted clean and left everything green, serving a
permanently-zero section. **The asymmetry is what proved it was a gap rather than a judgement call:**
the `grpcAdmission` twin IS execution-checked against real sockets by
`TestBuildHTTPServer_ServesTheRealListenersCounters`. Two questions had been run together in one
comment - "does main pass the handler it registered?" (syntactic, `TestServerCountersIsWiredByMain`)
and "does `buildHTTPServer` forward what it was GIVEN?" (executable, unchecked) - and the second was
left unguarded by the sentence that claimed the first.

**The engineer fixed it at the TOP rung**, not by adding a tenth parse-based guard:
`TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers` floods a real registered gRPC
stream and reads the numbers back through the real admin route.

**(b) The new cardinality check counted ROWS, not DISTINCT FIELDS.** Proved evadable in two steps:
`agentHandler = nil` inside an `if` in main is correctly RED ("assigned 2 times"), and then replacing
the `agentHandler` row with a **second `grpcAdmission` row** makes the whole package green again - two
rows, `NumField` is 2, and `agentHandler` is now outside `chainNames`, losing the plain-identifier
check, the derives-from check and the assigned-exactly-once check **at a stroke**. Now counts distinct
fields, with a field-exists check so a typo'd row fails on the typo rather than four assertions later.

**(c) The call-site AST walk was blind to POSITIONAL `logKey` literals.** The loop `continue`d past
anything that was not a `KeyValueExpr`, so `logKey{someLocalVar, "", 0}` skipped every assertion and
the site was never checked at all - compiling, vetting clean, spending the shared token bucket, and
having every drop it caused discarded by `record`'s fail-closed branch. `require.NotEmpty` on the
collected kinds does not save it: the four untouched keyed sites satisfy that on their own. Keyed form
is now required, and an absent `kind` key is rejected too (it leaves kind at 0, which `record` drops).

---

## An honest coverage limit the engineer stated rather than implied

The top-rung fix in (a) is **integration-only**, because moving an ingest counter needs `Connect`'s
message loop and therefore Postgres, and go-ci runs `go test -race ./...` with **no tag**. So CI only
**compiles** it.

That is written into the test's own comment rather than left for someone to discover, and the engineer
**declined to add an AST fallback on top rather than guess which the conductor wanted**. Both halves
are the right behaviour: slice 1's lesson was that a build-tag placement should be *checked* rather
than assumed, and it was checked here - the default-lane half (Task 4's flood tests, a bare
`&Handler{}` with no database) is in the lane CI runs, and only the part that genuinely needs Postgres
is tagged.

---

## A README claim that was false and mattered operationally

The slice's own README line read: *"every caller-driven log line on the gRPC receive path is
rate-limited per connection"*.

**Six reachable `log.Printf` sites on the recv goroutine are outside the budget**, and
`ingest_log_limiter.go` says so correctly in its own comment - so the two documents disagreed with
each other in the same PR. The operational consequence is specific: under a database outage an agent
streaming status updates drives unbounded volume through `handler.go:984`
(`handleTaskStatus UpdateTaskStatus ...`) while `ingest_log_budget` reads **all zeros**, so an
operator following the README concludes there is no caller-driven flood.

Corrected in-slice: the section now says the budget covers those five sites and no others, names the
classes that are outside it, and the "what it does NOT count" bullet points back at them. The
underlying gap is filed (below) rather than fixed here.

> **Wrong prose about correct code is the dominant defect class - thirteenth consecutive iteration**,
> and this instance had the tell the project keeps recording: the code was already correct about
> itself two files away.

---

## The plan refuted nine item and spec claims, one of which was a crash

Recorded because the verification chain earned its length again, and because one of the nine would
have shipped a process kill.

- **R1 is the crash.** The spec's `[5][2]` array "indexed by `logKind`" **does not compile safely**:
  `logKind`'s constants are `iota + 1`, so `kindInventory == 5` indexes out of range of a `[5]` array
  - a panic on the recv goroutine, which is the exact failure mode the nil arm's own comment exists to
  avoid, reintroduced by the array it was guarding. The obvious repair (`k.kind - 1`) is **worse**:
  `logKind` is `uint8`, so an unset kind 0 wraps to 255. Landed as `[kindCount][2]` with a sentinel.
- **R4 settled atomics-versus-mutex AGAINST slice 1's precedent, with reasons rather than by copying.**
  `netlimit` moved to plain `uint64` under an existing mutex because its snapshot carries a cross-field
  invariant. Neither condition holds here: no cross-field invariant among ten independent monotonic
  totals, and the recv goroutine's standing no-new-lock constraint. The reason is in the code next to a
  sentence saying why `netlimit` did the opposite, so nobody "fixes" the inconsistency.
- **R5 is the honesty constraint on the payload.** `handler.go:802`'s
  `if !errors.Is(err, pgx.ErrNoRows) && lim.allow(...)` **short-circuits before the budget**, so a
  `GetTask` `ErrNoRows` never reaches `allow` and is never counted - correctly, because the decision
  not to log was made upstream. The payload therefore documents **"log lines the budget dropped"**,
  never "diagnostics lost", in three places.
- R2 (the package-level home, refuted on test isolation and on there being no object for
  `CounterSources` to hold), R3 (the "values may still be renumbered freely" half), R6 ("one array,
  one pointer, two increments"), R7 (a hidden required edit to a shipped test), R8 (a counts-only
  section is tolerated by both guards, checked rather than assumed), R9 (the acceptance flood test does
  NOT need Docker), R10 (the nil arm is not merely uncounted but **uncountable**).

---

## What Was Built

- **`internal/worker/ingest_log_counters.go`** (new) - `ingestLogCounters` (`[kindCount][ingestDropArms]atomic.Uint64`),
  `record`, `snapshot`, `byKind`, and the two exported value types `IngestLogDrops` /
  `IngestLogDropsByKind`. Read `ingestLogCounters`'s comment for the atomics-versus-`netlimit`
  contrast and `record`'s for the two fail-closed branches and why the bounds check does not cover the
  nil receiver.
- **`internal/worker/ingest_log_limiter.go`** - `kindCount` sentinel appended to the const block; the
  `logKind` doc comment rewritten (both halves of "may be renumbered freely" were false); an
  `ingestLogLimiter.drops` field whose comment names it as the one shared thing in the type; the two
  suppression arms record; the `l == nil` arm's comment extended to say nothing is counted there and
  why it is unreachable to count.
- **`internal/worker/handler.go`** - `Handler.ingestDrops` as a **value** field (zero value ready, so a
  bare `&Handler{}` in a test has working counters and there is no nil case), the exported
  `IngestLogDropCounts()`, and the allocation site's comment amended so it no longer claims nothing
  escapes the frame.
- **`internal/api/server_counters.go`** - `IngestLogBudgetSource` (one method, and its comment says why
  slice 3's counter on the SAME `*worker.Handler` must get its own source field), the
  `CounterSources.IngestLogBudget` field, `ingestLogBudgetSection` / `ingestLogBudgetCounts` /
  `ingestLogKindCounts` (a struct, not a map, so both payload walks keep full reach), and
  `ingestLogKindCountsFrom`.
- **`cmd/relay-server/http_server.go`** - `httpServerDeps.agentHandler`, typed concretely so the typed
  nil is filtered where it is still visible, and a per-section `if d.x != nil` rather than a per-struct
  one. Its comment now splits the two wiring questions and names which guard answers each.
- **Guards.** `TestIngestLogKindsAreADenseRunFromOne`,
  `TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished` (parses the package, requires keyed
  literals), `TestIngestLogKindCountsPublishesEveryWorkerSideField` (the arity check),
  `TestServerCountersIsWiredByMain` generalized to a `wiredDep` table with a distinct-field cardinality
  check and a same-object check against `RegisterAgentServiceServer`,
  `TestBuildHTTPServer_ServesTheWiredHandlersIngestSection`,
  `TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent`, and
  `TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers` (integration).
- **README** - the `ingest_log_budget` payload, both arms' meaning, the corrected coverage sentence,
  and the "what it does NOT count" bullet.
- **Zero SQL, zero migration, zero proto, zero generated file, zero files under `web/`.**

## Key Decisions

- **The counters are a VALUE FIELD on `*worker.Handler`, not a package-level var.** Production has one
  Handler, so per-Handler IS process-wide there, and it is a property the wiring guard can *check*
  rather than one a package-level var merely asserts. Every test gets its own, which is what keeps
  exact-count assertions in a 21-file package deterministic.
- **`atomic.Uint64`, deliberately the opposite of what `netlimit` did one day earlier**, with the
  reason written next to the contrast so the inconsistency is not "fixed".
- **`[kindCount][2]` with a sentinel, direct indexing, and a fail-closed bounds check** - never
  `[5]` and never `k - 1`.
- **A struct per arm, never a `map[string]uint64`.** The kind set is compile-time closed, so named
  fields make unbounded key cardinality impossible AND keep both payload walks at full reach; a map
  would need an exemption whose predicates descend themselves.
- **Counts only, no `levels` half, and that is a decision rather than an omission.** Every limiter is a
  per-connection stack local, so a process-wide "current" figure would need exactly the shared registry
  the type refuses to have.
- **The `l == nil` arm is uncountable, not merely uncounted.** Make it unwritable, then say so.

## Findings Triage

- **1 cross-package arity gap** (a correct sixth kind counted and published nowhere), found
  independently by all three reviewers with three different sixth kinds.
- **3 more guard evasions**, all caught: the `buildHTTPServer` forwarding gap (fixed at the top rung
  with a real stream through the real route), rows-not-distinct-fields, and the positional-`logKey`
  blindness. **Twelve total across the batch.**
- **1 nil-receiver panic** on the recv goroutine, unreachable today, one line.
- **1 backwards measurement in a test's own comment**, re-measured by a lens rather than accepted, and
  the original numbers were compile errors rather than behavioural kills.
- **1 misleading guard failure message** naming a condition that is satisfied when it fires.
- **1 false README claim** with a concrete operational consequence under a database outage.
- **9 item/spec claims refuted by the plan**, one of them a process-killing panic.
- **0 findings against the shipped behaviour after remediation.**

## What Remains Open

- **The forwarding check is INTEGRATION-ONLY, so CI compiles it and never runs it.** Moving an ingest
  counter needs `Connect`'s message loop and therefore Postgres, and go-ci runs `go test -race ./...`
  with no tag. `TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers` is the top rung
  of the ladder and the default lane cannot climb it. What CI does cover is the presence/absence pair
  (`TestBuildHTTPServer_ServesTheWiredHandlersIngestSection` and its typed-nil twin) plus
  `TestServerCountersIsWiredByMain`'s identifier and assignment-count properties. Stated in the test's
  comment. Not filed: the alternative is an AST fallback on top of an executed check, which is the
  rung the slice deliberately climbed off.
- **`buildHTTPServer`'s two `if d.x != nil` assignments are the same unguarded-copy shape as the three
  unconditional ones, and slice 3 adds a third.** Deleting `s.Counters.IngestLogBudget = d.agentHandler`
  is caught (by the executed presence test); *conditionalizing* the pair further, or adding a third
  section whose assignment is simply absent, is caught only by the distinct-field cardinality check,
  which is itself a hand-maintained table. Carried into
  `idea-2026-08-14-generalize-the-env-to-field-wiring-guard` by amendment rather than filed anew.
- **`ingest_log_budget` covers only the five `lim.allow` sites**, which is now stated in README and in
  the payload's own documentation. Three registration-time sites are already tracked by
  `bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget`; the three post-registration
  `handleTaskStatus` DB-error lines were tracked nowhere and are filed below. Under a database outage
  those three carry the volume while every number in this section stays at zero.
- **The counters do not count every silent drop, by design.** The `&&` short-circuit at
  `handler.go:802` means a `GetTask` `ErrNoRows` never reaches the budget, and the fence-rejection arm
  never consults it at all (slice 3). The payload says "log lines the budget dropped", not "diagnostics
  lost", in three places.
- **`TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact`'s exactness half is inert at one
  CPU** (0/20), while its `-race` half is 10/10 at every core count measured. CI has 2-4 vCPUs so both
  are live there. Recorded in the test's comment with the numbers. Not filed - there is no action short
  of pinning `-cpu` for one test.
- **A sixth kind declared as an UNTYPED constant** in the same block escapes the AST count for the
  "not a counted constant" reason rather than the "outside the sentinel" one. The failure message says
  both, so the diagnosis is not misleading. Disclosed, not guarded.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **eighteenth iteration**.
  The plan refuted nine claims across the item and the spec, one of which was a panic on the recv
  goroutine.
- **A backlog proposal is not a contract** - eighteen for eighteen.
- **The verification chain is worth its length only if each stage treats the previous stage's output as
  untrusted** - honored in both directions: the plan refuted the spec, and a lens re-measured the
  engineer's own documented `-race` numbers and found them backwards.
- **A guard that counts a PROPERTY, not a spelling** - honored and extended: the rows-to-distinct-fields
  fix is exactly that lesson applied to a guard written one slice after it was recorded.
- **A mutation proof must leave a test behind** - honored; every evasion left a permanent check.
- **A mutation battery needs a green baseline** - honored explicitly, with the unmutated 0/10 and 0/20
  numbers in the test's comment.
- **A test can be robust and inert on the same machine** - honored, and this time with corrected
  numbers rather than plausible ones.
- **Wrong prose about correct code is the dominant defect class** - **thirteenth consecutive
  iteration** (the README claim, the backwards measurements, the two-questions-in-one comment).
- **Say what a fix does not buy in the same sentence that says what it does** - honored; the
  integration-only coverage limit and the `&&` short-circuit are both stated where they will be read.
- **Backlog housekeeping is required scope** - the close of the source item belongs to the conductor.

New from this iteration:

- **A hand-written copy between two types needs something comparing their ARITY, and the check belongs
  where both types are visible** - usually neither type's own package. **Candidate for durable memory**:
  this slice's two worst findings were the same shape one layer apart, and every other boundary in the
  chain already had the check.
- **A completeness claim cannot be checked by reading its subject.** `counterPayloadLeaves` reddens on
  an extra api leaf and never on a missing worker-side one. This is the "a uniqueness claim is a claim
  about the complement" lesson in a second dress: search for the shape, count the hits.
- **A mutation that reddens something is not yet evidence for the claim you are making.** Four attempts,
  three real reds for the wrong reason. The kill must fail through the asserted mechanism.
- **A guard's failure message must name what is WRONG, not the property being defended.** A message
  describing a condition that is satisfied when it fires sends the author to the wrong file first.
- **When a check appears to cover two arguments, ask which of them it actually touches.** `len(c.n)` on
  an array type is a compile-time constant, so the bounds check protected the receiver from exactly the
  case that could not hurt it.
- **State a coverage limit rather than implying it, and do not pile a weaker rung on top of a stronger
  one to hide it.** The forwarding check is executed and tagged; the honest sentence is better than an
  AST fallback nobody asked for.

## Files Most Touched

- `internal/worker/ingest_log_counters.go` - `record`'s two comments are the ones to read: the nil
  receiver versus the bounds check, and why the out-of-range test asserts on the whole array rather
  than through `snapshot()` (kind 0 lands in `c.n[0]`, which no published field reads, so relaxing
  `i <= 0` to `i < 0` is invisible to any snapshot assertion - a live mutation survivor, not a
  hypothetical).
- `internal/worker/ingest_log_limiter.go` - the rewritten `logKind` block (both properties now
  load-bearing) and the `drops` field's comment.
- `internal/worker/ingest_log_counters_test.go` - `TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact`'s
  comment for the corrected `-race`/exactness asymmetry and the green baseline, and
  `TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished`'s positional-literal block.
- `internal/api/server_counters_test.go` - `TestIngestLogKindCountsPublishesEveryWorkerSideField`'s
  comment is the headline lesson written where the next section author will hit it.
- `cmd/relay-server/http_server.go` - the `agentHandler` field's "TWO SEPARATE QUESTIONS, TWO SEPARATE
  GUARDS" block.
- `cmd/relay-server/counters_wiring_test.go` - the "DISTINCT FIELDS, NOT ROWS" block, which already
  names what slice 3 will hit (`NumField` goes to 3 while the natural table still has 2 rows).
- `README.md` - the `ingest_log_budget` bullets around lines 1285-1286.
- `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice2.md` - R1-R10 and the mutation
  battery M1-M24.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`,
  no `git diff`, no test run. Every claim below that could be checked by reading was checked against
  the worktree.
- **Verified by reading:** `internal/worker/ingest_log_counters.go` in full; `ingest_log_limiter.go`
  lines 70-210 (the `drops` field, the rewritten `logKind` block, the const block with `kindCount`
  last); `ingest_log_counters_test.go`'s full test list plus the positional-literal and concurrency
  comments; `internal/api/server_counters.go`'s `IngestLogBudgetSource`, `CounterSources`, the section
  types and the handler branch; `internal/api/server_counters_test.go`'s
  `TestIngestLogKindCountsPublishesEveryWorkerSideField` in full and the three new endpoint tests;
  `cmd/relay-server/http_server.go` in full; `counters_wiring_test.go`'s `wiredDep` table and the
  distinct-field cardinality block; `README.md` lines 1263-1287; the plan in full; the spec sections
  3.3, 7.3, 9, 10.2 and 17; the closing item and the three sibling items.
- **Confirmed against code, not inferred:** the five budgeted `lim.allow` log sites
  (`handler.go:771, 802, 1083, 1175, 1380`) and the seven unbudgeted `log.Printf` sites
  (`:233, 522, 553, 939, 984, 991, 1197`), of which three are registration-time and already tracked;
  that README's coverage sentence was corrected in-slice; that `ingestLogKindCounts` is a struct with
  five `uint64` fields and no allow-list entry was added; that the section is counts-only.
- **Reported by the implementing and verifying lanes, not re-run here:** unit 574 -> 594 top-level, the
  three integration packages, the module-wide `-race` container run, and every mutation result
  including M1-M24, the four-attempt isolation of the arity gap, the three guard evasions and the
  10/10 versus 0/20 `-cpu` figures.
- **Not verified:** all test results, the commit set and diff stat, and the change set as `git` sees
  it. Each is attributed above.
- **No PR number appears anywhere in this retro or in the filed items**, by instruction. The work is
  referenced by date and slug.
- **Outstanding and belonging to the conductor:** the close of
  `idea-2026-08-15-ingest-log-suppression-is-uncounted` (`/backlog close`, never a hand-edited
  `status:`), the exact-file-set check, the final gates, all commits, and a ROADMAP refresh.
