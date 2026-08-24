---
date: 2026-08-24
topic: silent-drop-observability-slice4
slice: 2026-08-21-silent-drop-observability (slice 4 of 4 - the cluster closes here)
branch: claude/pr-merge-session-d3977d
range: origin/main..HEAD (18 commits; backend only; Go plus README; zero SQL, zero migration, zero proto, zero generated file, zero files under web/; green, not yet merged)
pr: not yet opened - reference this work by date and slug, never by a predicted number
closes: idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced, bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick
deferred-and-must-stay-open: idea-2026-08-21-counters-payload-cannot-say-not-measured
---

# Session Retro: 2026-08-24 - the deliverable asserted a maximum it could not establish, and the guard that admitted its keys had never seen one

**TL;DR:** `internal/scheduler/watchdog_counters.go` (a `sync.Mutex` plus an `api.WatchdogCounts`
value, 256-key first-come map, `swept_overflow`, `record`/`snapshot`/`worst`); `api.WatchdogCounts` /
`WatchdogCounters` / `WatchdogSource` / `WatchdogSweptWorkerMax` declared **in the consumer package**
and used **directly** as the response type; a fourth `watchdog` section on admin-only
`GET /v1/server/counters` with an argued, descending, capped `counterPayloadAllowList` entry; the
watchdog's per-row error line replaced by **one aggregate line per sweep** covering both the swept set
and the failed-write set; and `Dispatcher.failClaimedTask` no longer reporting a fence rejection as a
database error. Unit 603 -> 626 passing (627 run, 1 pre-existing Windows-only skip); integration green;
`-race` clean in a Linux container; `git diff -- internal/store/` 0 bytes; `web/` untouched.

**This is slice 4 of 4 and it closes the cluster** that ran across the four preceding iterations. Each
slice of this batch produced one durable rule: slice 1, *a guard that matches a spelling is evadable by
respelling, a guard that counts a property is not*; slice 2, *a hand-written copy between two types
needs something comparing their arity*; slice 3, *a guard can count a property and count the wrong one,
and its failure message can be the instruction manual for evading it.* Slice 4's is the one that had
been missing, and it is not about guards at all:

> **A lossy aggregate must disclose its loss wherever it is READ, not only where it is published.**
> `swept_overflow` was designed, counted, published, documented and tested - and the one sentence built
> to spare an operator from noticing a repeating uuid asserted a maximum over the tracked set as if it
> were a maximum, under a condition a peer can drive.

---

## The headline: the deliverable itself asserted something it could not establish

The whole point of this slice's log line is the item's own sentence: *"worker X has had 37" is a
disable decision with one number behind it.* The plan shipped it as:

```
watchdog: sweep ended: 3 task(s) swept across 2 worker(s); worst since process start: worker <uuid> with 4
```

Unqualified. And `SweptOverflow` - the field whose entire declared purpose is to make a loss visible -
**was never read anywhere in `watchdog.go`.** Two comments claimed the line disclosed the caveat; the
`worst()` doc said "TRACKED is load-bearing" and then returned two values, neither of which was the
overflow.

**The security lens supplied the reachable path, and it is not exotic.** `swept_by_worker` is
first-come at 256. Fill the 256 slots with workers swept once each - which a fleet does by itself, and
which `RELAY_ALLOW_AUTO_ENROLL` lets a reachable host do on purpose, one persistent `workers` row per
hostname it claims ([[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]], still open). A
genuine offender that first appears **after** the map is full is then swept 40 times and every one of
those sweeps lands in `swept_overflow`. The line names an innocent machine with `1` as the worst worker
since process start, and it is the only place an operator reading raw logs sees this signal at all. The
one sentence built to point at a machine points at the wrong machine, precisely in the scenario the
counter exists for.

**The fix is a return-value change, and its shape is the finding.** `worst()` now returns three values
from the **one** lock acquisition (`internal/scheduler/watchdog_counters.go:121-138`):

- **not a convenience.** A caller that read `SweptOverflow` separately would take a second critical
  section, and the pair could then disagree - a maximum qualified by an overflow measured at a
  different instant is a new, subtler false statement.
- `SweepOnce`'s clause became a **three-arm switch** (`internal/scheduler/watchdog.go:295-323`), and two
  of the three arms are new honesty rather than new information:
  - `worstN <= 1`: **say nothing.** "worst: worker X with 1" beside "256 swept across 256 workers" names
    an arbitrary member of a set in which nothing stands out - the opposite of the repeating-uuid
    signal. `worstID == ""` is the same case and needs no arm.
  - `overflow > 0`: `worst TRACKED worker since process start: X with N (M sweep(s) unattributed -
    per-worker attribution is incomplete)`.
  - otherwise: attribution is complete, so the unqualified sentence is true and stays.

Three lenses converged on this independently. Two tests now hold it:
`TestWatchdog_TheAggregateLineDoesNotAssertAWorstItCannotEstablish` and
`TestWatchdog_TheWorstClauseIsOmittedWhenNobodyHasMoreThanOne`.

> **The generalizable form.** This cluster's cardinality rule was always stated on the **producer**
> side: a counter key must come from a set the server enumerates, and where it cannot, bound the set and
> publish the loss. Slice 4 found the consumer side of the same rule. Publishing `swept_overflow` in
> JSON discharged nothing for the reader who never opens the JSON. **Every place a bounded aggregate is
> rendered into a sentence is a place the bound has to be re-disclosed** - and the sentence is exactly
> where it will not be, because a sentence is written to be short.

---

## The arity lesson was closed BY CONSTRUCTION, and the closure was measured

Slice 2's headline was a fully correct sixth log kind that went counted-but-unpublished with all three
packages green, because a hand-written five-field mapper never learned about it. Its remedy was an
arity assertion between two restated types, and the item for this slice inherited that remedy verbatim:
*"a `NumField` assertion between the watchdog's own counter set and the published struct, in
`internal/scheduler`."*

**The plan refused the remedy and attacked the antecedent instead.** The rule reads *"any section whose
payload struct **restates** fields owned by another package needs a `NumField` assertion"* - and
restating is a choice. So:

- `internal/api` declares `WatchdogCounts` and carries `*WatchdogCounters` **as** the response field.
  `handleServerCounters` does one struct assignment.
- `internal/scheduler` stores an `api.WatchdogCounts` **as the watchdog's own counter state**;
  `snapshot()` is `out := w.c` plus a map clone.

There is no mapper on either side, so there is no arity to drift. **Note the inversion that forced
this**: `internal/scheduler` imports `internal/api`, so unlike every other section the **consumer**
package owns the type. The rule that bit slice 2 could not have been applied here in its original
shape anyway.

**M12 is the measurement, and it is what makes this a claim rather than a design preference.** Adding
`SweptTruncated uint64` to `api.WatchdogCounts` and incrementing nothing:

- needed **no mapper edit anywhere** and still reached a JSON key - the property being proved;
- left `internal/scheduler` RED on exactly one test,
  `TestWatchdog_EveryPublishedCounterIsDrivenByTheSweepFixture`, which runs a real sweep whose fixture
  is built to move every counter and then walks the returned struct by reflection.

Two more guards hold the antecedent rather than the consequent: `TestWatchdogSectionRestatesNothing`
(the response field's type IS the source method's return type; its failure message names the arity test
that must ship in the same commit if anyone reintroduces a restatement) and
`TestWatchdogCountersLiveOnlyInThePublishedStruct` (two fields, `mu` and the published struct, so a
counter stored beside it - `swept_overflow`'s own failure mode - is RED).

> **Prefer deleting the antecedent to guarding the consequent.** Three iterations of this batch guarded
> a hand-written copy. This one removed the copy. The guard that remains is smaller, and it asks a
> question about a *type*, which is a question no fixture can satisfy.

---

## A guard that was decorative WITH RESPECT TO THE PRODUCER

`counterPayloadAllowList["watchdog.counts.swept_by_worker"]` is this slice's required, argued artifact:
a descending `jsonOK` that checks key shape against an anchored lowercase-hex uuid regexp, value kind,
and `len(m) > WatchdogSweptWorkerMax`. It was written first, tested against a thirteen-row hostile
table with the poisoned rows placed first, and it works.

**And it had never seen a byte the producer emitted.** `internal/api`'s two payload walks run against
`fakeWatchdogSource{c: threeDistinctSweeps()}`, whose keys are string literals in
`server_counters_test.go`. `internal/api` **structurally cannot** import `internal/scheduler` to fix
that. So the predicate proved a fixture well-formed and said nothing about the code that makes keys.

**Measured:** mutating `internal/scheduler`'s producer so every key was
`"build-agent-07.corp.example\n10.0.0.7"` - a hostname with an injected newline, the exact payload the
exemption's own `why` cites as what must never get in - left **both `internal/api` and
`cmd/relay-server` fully green.** The only assertion in the repo that had ever touched these keys was a
`require.Len(..., 1)` inside the forwarding test: a count, never a shape.

Closed at the top rung, in the only package that can import both sides:
`TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap`
(`cmd/relay-server/counters_wiring_test.go:955`) drives a real `SweepOnce` over
`WatchdogSweptWorkerMax + 4` distinct workers through a real `buildHTTPServer` and the real
admin-gated route, then reads shape and cardinality and overflow off **one real response**. Three
details worth keeping:

- **shape asserted before the bound**, with the reason in the message: a producer that stops rendering
  uuids usually collapses the key set too, so a cardinality assertion placed first would fire and
  report the wrong defect;
- a `require.NotEmpty` guard first, so nothing below can pass vacuously;
- the `why` string in `internal/api` now states its own blindness and names the `cmd/relay-server` test,
  rather than implying it checks the producer.

This is roughly the **sixteenth** structural-guard evasion in this family's ladder (thirteen across
#139-#142, three more here). Its rung is new: the previous fifteen were guards that could be evaded by
*editing the guarded code*. This one could not be evaded at all - it was simply pointed at something
that was not the subject.

---

## Two more decorative guards, both found by mutation, both about a value nobody read

**1. "FIRST, not last" was unfalsifiable.** `SweepOnce` captures the first failing row's id and error
for the aggregate line, with a comment saying why first beats last. The test poisoned all five rows
with the identical string `"connection reset by peer"` and asserted `Contains`. Mutating
`if firstFailErr == nil` to `if true` - capturing the **last** error, the exact thing the comment
forbids - **passed the whole scheduler suite.** And `firstFailID` was asserted nowhere in the repo, so
replacing it with a zero-value uuid passed too.

Closed by giving every row a distinct error text and asserting `Contains(row 1)` **and**
`NotContains(row 5)`, plus `Contains("first: task " + rows[0].ID)`.

**2. The two watchdog bounds were transposable in `main.go` with the wiring guard green.** This slice
had to split `go scheduler.NewWatchdog(...).Run(ctx)` into a bound local plus a `go watchdog.Run(ctx)`,
because the counters endpoint reports this watchdog's sweeps and `buildHTTPServer` is the only place
the `api.Server` is built. Under mutation, swapping the two trailing `time.Duration` arguments compiled
and left `go test ./cmd/relay-server/...` green - **margin 24h, absolute cap 30m, in production**: within
one 60s tick the watchdog stamps every assignment older than half an hour `timed_out` and cascades each
one's transitive dependents to `failed`, with nothing retried.

Note the polarity, which is why it was worth the complexity rather than a disclosure: every other check
in that guard is about a wiring mistake that leaves the watchdog **dead**. This one leaves it **alive
and destroying work**, and it would have made
`TestParseWatchdogDuration_MaxAssignmentFloorIsItsOwn` decorative one argument-swap away. The
discriminator has to be the env var name each argument's chain was parsed from, since nothing about the
two types distinguishes them - which is why the assignment walk now collects string literals alongside
identifiers.

> **When a refactor splits one statement into two, ask what the single statement was forcing.** The old
> `go NewWatchdog(...).Run(ctx)` made "constructed" and "started" one question. Splitting it created a
> new question ("are these the same watchdog?") that the guard now asks explicitly - and nothing was
> relaxed. This is slice 3's *"when you replace a bijection with a many-to-one relation, ask what the
> bijection was forcing"* arriving in a different file.

---

## The predicted recursion happened, and then the engineer caught itself

Slice 3's durable rule was that a comment written to end a question has a permanent audience and no
reader positioned to check it. Slice 4's remediation round wrote one.

**The commit whose entire job was to remove a false claim introduced one.** Correcting
`failClaimedTask`'s fence-rejection comment, both new comments named **`Dispatcher.dispatchTask`** - a
function that has never existed in this repo; it is `Dispatcher.sendTask`. The enumeration those
comments carry exists **to be grepped**. Caught by the re-verify round.

**Then the engineer caught itself in the fix.** The same comment had said "the fifth of five", and the
replacement was going to say "the other four". Both are **uniqueness claims**, which CLAUDE.md already
records as claims about the *complement* - checkable only by searching for the shape, never by opening
the subject. The earlier wording had already missed `ClaimTaskForWorker` in `Dispatcher.sendTask`,
**earlier in the same file**. What shipped instead names the **partition**
(`internal/scheduler/dispatch.go:462-497`): fence rejections that arrive as `pgx.ErrNoRows` from a
`:one` statement, which is the only shape an `err != nil` arm can tell apart, versus those that arrive
as a rowcount or an empty slice, where there is no error to inspect and the site cannot grow this
branch without a statement change.

The same move was applied to `canonicalWorkerKey`'s comment, which said "IT HAS A TWIN" when there was
one restatement and was already stale (a third landed two commits later). It now states the **property**
- every restatement is this rule spelled as an anchored regexp and every one lives in a test file -
and hands the reader the grep.

---

## The plan refuted ten claims, including two in the spec's own payload

Fourth consecutive slice where planning-phase verification caught something material before a line was
written. The two that changed the shipped artifact:

- **`swept_workers_max` is a compile-time constant, not a level.** The joint spec's payload put it in
  `levels`. A level is current and goes down as well as up; a configured constant there would have to
  **move** when [[idea-2026-08-21-counters-payload-cannot-say-not-measured]] ships a `limits` half - a
  breaking change to a published payload.
- **`swept_workers_tracked` restates `len(swept_by_worker)`**, sitting beside it in the same object,
  where it can only agree or be a bug.

Shipped: **counts only, no `levels` half at all**, which is also the forward payment for the deferral
(below).

Two more worth recording:

- **R8: `main` did not bind the watchdog to a local and constructed it AFTER `buildHTTPServer`.**
  Neither the item nor the spec mentions this; both were written as if the wiring were an added
  argument. It is a `main` restructure, and it is what forced the guard split above.
- **R4: the item's own Done-When was not shippable as written.** "A disabled watchdog leaves the
  section ABSENT" plus the `var wd *scheduler.Watchdog; if enabled { wd = ... }` shape it called "the
  natural shape and it panics" is **RED under a shipped guard** - `TestServerCountersIsWiredByMain`
  requires exactly one unconditional assignment on the chain. So the watchdog is constructed
  unconditionally and a disabled watchdog serves an honest section of zeros. The typed-nil filter still
  ships, and its test now says it guards a **shape** against a future caller rather than a live panic.

---

## The deferral, and what it cost to keep it cheap

[[idea-2026-08-21-counters-payload-cannot-say-not-measured]] was named as a fold-in by the ROADMAP
"Now" line. **It was declined, and it must stay open.** Two independent, checkable grounds:

1. **Its sequencing argument has nearly lapsed.** The item's own Notes price it as "almost nothing extra
   as part of slice 2" against "reshaping a payload with four populated sections" afterwards. Three
   sections were already shipped. The marginal saving is **one section, not three**.
2. **The watchdog does not reproduce the defect's sharp form.** `netlimit`'s case is an *affirmative
   false statement*: `live_total: 0` on a server holding 800 connections. A disabled watchdog publishes
   `swept_total: 0`, `swept_overflow: 0`, `swept_by_worker: {}` - **all literally true.** What is
   missing is an answer to "is it on?", which is an unanswered question, not a lie. The item's Summary
   rests on the falsehood, and that premise does not transfer.

**The forward cost paid so the deferral stays cheap:** no configured constant in any `levels` half,
which here means **no `levels` half at all**. A future `limits` classification is purely additive with
zero field moves.

---

## Process shape

- `/code-review` (high effort, conductor-run): **3 findings**.
- Four-lens fan-out (invariants, correctness, security, integration-tester): **1 HIGH + 4 MEDIUM +
  several LOW**. Three lenses independently converged on the worst-worker defect; three independently
  found a README error.
- **Remediation round 1** closed all 13. **The re-verify round found 3 more LOW** - including the
  `dispatchTask` recursion introduced by the remediation itself. **Round 3** closed those.
- **The correctness lens refuted the framing of one of the conductor's own findings, and was itself
  incomplete on the same question**; two other lenses caught what it missed. That is the argument for
  the separate-narrow-lenses design, stated as an observation rather than a preference: a single
  merged reviewer has one position on a question, and this question needed three.

**Every iteration of this batch needed at least one remediation round; three of five needed two or
more.** That number has not improved across the batch, and it is the honest headline for the cluster's
process: the fan-out is what makes the slices correct, not the implementation pass.

---

## What Was Built

- **`internal/scheduler/watchdog_counters.go`** (new) - `watchdogCounters` = `sync.Mutex` + an
  `api.WatchdogCounts` **value**, so the zero value works and a bare `&Watchdog{}` in a test has a
  working counter set. `record` (total first and unconditionally, so the reconciliation
  `SweptTotal == sum(SweptByWorker) + SweptOverflow` holds on every branch; non-uuid to overflow;
  first-come at the cap, already-tracked keys keep counting), `snapshot` (struct assignment plus a map
  clone, **always allocated**), `worst` (three values, one lock), `canonicalWorkerKey`. Read the type
  comment for **why a mutex here when slice 2 chose atomics** - a cross-field invariant, a map that
  cannot be updated atomically, and plain fields so an unsynchronised access is a race `-race` can see.
- **`internal/scheduler/watchdog.go`** - the `counters` value field, `CounterSnapshot()` satisfying
  `api.WatchdogSource`, `record` on **matched writes only**, and the whole aggregate-line block: the
  accumulators, the rewritten `pgx.ErrNoRows`-distinguishing error arm, and the gated summary with its
  three-arm worst clause. The per-task success line's safety argument now says **which branch it
  covers**, because it had been read as covering the pair and the "swept at most once" clause is exactly
  what a failed row does not get.
- **`internal/scheduler/watchdog_counters_test.go`** (new) - twelve tests: counter semantics, the hard
  cap and overflow, the unkeyable-worker path, the copy-out, the never-nil map, the `-race` concurrency
  test asserting **exactness and reconciliation** (the second is what a mutation to atomics breaks),
  the two structural guards, the sweep-fixture exhaustiveness check, and the four log-line tests. Read
  `TestWatchdog_APersistentWriteFailureIsBoundedToOneLinePerSweep`'s comment: it carries two measured
  evasions and the reason its phase 1 exists.
- **`internal/scheduler/dispatch.go`** - `failClaimedStore` + a package-level `failClaimedTask` (the
  extraction `finalizeTerminalTask` already set a precedent for), the `errors.Is(err, pgx.ErrNoRows)`
  arm, the partition comment, and the doc comment that would otherwise have become wrong prose.
- **`internal/scheduler/dispatch_fence_test.go`** (new) - both legs, with the unconditional "failing
  task" line asserted present in each so a fix that silenced the whole function is RED.
- **`internal/api/server_counters.go`** - `WatchdogSweptWorkerMax`, `WatchdogCounts`,
  `WatchdogCounters`, `WatchdogSource`, `CounterSources.Watchdog`, `serverCountersResponse.Watchdog`
  (`*WatchdogCounters` directly), the one-assignment handler branch, and the corrected allow-list
  paragraph that no longer says `started_at` is the whole list nor that `swept_by_worker` is
  de-authorized.
- **`internal/api/server_counters_test.go`** - `canonicalUUIDRe`, the argued exemption, three
  `counterPayloadLeaves` entries, the thirteen-row hostile table, the no-restatement guard, the
  full-depth wired-but-zero test, the snapshot test, and the corrected `counterPayloadExemption` doc
  block.
- **`cmd/relay-server/http_server.go`** - `httpServerDeps.watchdog` (concretely typed), its **own**
  `if d.watchdog != nil` filter, and the comment naming which of the three wiring questions each guard
  answers.
- **`cmd/relay-server/main.go`** - the bounds block and the construction moved above `buildHTTPServer`;
  `go watchdog.Run(ctx)` left where the watchdog block was.
- **`cmd/relay-server/counters_wiring_test.go`** - `sweepableStore` (a **Postgres-free** `watchdogStore`,
  which is what puts the forwarding proof in the DEFAULT lane), the executed forwarding test, the
  typed-nil test, the real-producer-bytes test, one `wiredDep` row, one fixture field.
- **`cmd/relay-server/watchdog_config_test.go`** - **not in the plan's file set**; changed during
  remediation. The `TestWatchdogIsStartedByMain` walk now splits "constructed unconditionally" from
  "started unconditionally", requires the two to name the **same** watchdog, and adds the positional
  check for the two bounds. Its `WHAT IT CANNOT REACH` block is the honest boundary and stays.
- **README** - the `watchdog` payload block, four reading bullets, and the **rewritten** (not appended
  to) `ingest_log_budget` "there is no per-worker split and there will not be one" sentence, now scoped
  to that section with its recv-goroutine reason and pointing at why the reason does not generalise.
- **Zero SQL, zero migration, zero proto, zero generated file, zero files under `web/`.**

## Key Decisions

- **Declare the type in the CONSUMER package and use it as the response type.** Forced by the import
  direction; exploited to delete the mapper on both sides. Guarded at the antecedent, not the
  consequent.
- **A mutex and plain `uint64`s, deliberately opposite to slice 2**, with the reasons written down
  rather than the precedent copied.
- **Counts only, no `levels` half**, so the deferred `limits` classification is purely additive.
- **The 256 cap is enforced in the producer and CHECKED in `jsonOK`** - and the `why` says which is
  which, and says what `jsonOK` cannot see.
- **`worst()` returns the overflow from the same lock acquisition.** Two calls would be two critical
  sections and the pair could disagree.
- **Say nothing when `worstN <= 1`.** A clause that always fires is not a signal.
- **Fold in the log-hygiene bug; scope `failClaimedTask` to log hygiene only.** A dispatch fence
  counter would be a fifth section for one number and would pre-empt
  [[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]]'s own design.
- **Silencing `failClaimedTask`'s `ErrNoRows` arm loses no churn signal** - checked, not assumed: the
  function's first statement is an unconditional line emitted before the write on every attempt, and
  both test legs assert it.
- **Construct the watchdog unconditionally.** A disabled watchdog serves an honest section of zeros;
  making it absent would collapse "disabled" into "not wired", which the payload contract forbids.

## Findings Triage

- **1 HIGH: the aggregate line asserted a maximum it could not establish**, reachable by a peer under
  `RELAY_ALLOW_AUTO_ENROLL`, with two comments claiming the opposite. Found independently by three
  lenses. Closed by a return-value change plus a three-arm switch and two tests.
- **1 decorative guard with respect to its producer** - `jsonOK` had only ever run against literals;
  a newline-injected hostname key from the real producer left two packages green. Closed in
  `cmd/relay-server`, the only package that can import both sides.
- **2 more decorative guards found by mutation** - the unfalsifiable "FIRST, not last" capture, and the
  transposable watchdog bounds (the one whose failure mode is *alive and destroying work*).
- **1 self-inflicted prose defect in the remediation itself** (`Dispatcher.dispatchTask`, a function
  that has never existed, inside an enumeration meant to be grepped), plus one unverifiable uniqueness
  claim caught by the engineer in the commit fixing it.
- **1 README error found independently by three lenses.**
- **10 item/spec/ROADMAP claims refuted by the plan** (R1-R10), including two errors in the spec's own
  payload shape and a `main` restructure neither doc anticipated.
- **3 review rounds**: 3 findings from `/code-review`, 1 HIGH + 4 MEDIUM + several LOW from the fan-out,
  13 closed in round 1, 3 LOW found in re-verify, closed in round 3.
- **0 findings against the shipped behaviour after round 3.**

## What Remains Open

- **`internal/api` structurally cannot see real producer bytes for ANY section**, and slice 4 closed it
  for exactly one. The payload contract's two walks and every allow-list predicate run against fake
  sources whose values are literals in `server_counters_test.go`. The three checks in the repo that read
  real producer bytes - `server_counters_realsocket_integration_test.go` for `grpc_admission`, and
  `grpc_admission_e2e_integration_test.go` for `ingest_log_budget` and `task_log_fence` - are all
  **integration-tagged, which CI never runs.** `watchdog` is now the only section with a default-lane
  real-producer check. **Recommended as an item below**; it is adjacent to
  [[idea-2026-08-23-integration-only-guards-ci-never-runs]] but is not the same gap - fixing the lane
  would not make `internal/api`'s own guards see a producer.
- **The watchdog counter path is never driven by real `ListOverdueAssignedTasks` rows.**
  `internal/worker/handler_watchdog_e2e_integration_test.go` already sweeps against real Postgres in
  three tests and asserts nothing about `CounterSnapshot`; every counter assertion in the repo runs
  against `fakeWatchdogStore` or `sweepableStore`. Low severity (a `pgtype.UUID` from real SQL renders
  canonically, and `ListOverdueAssignedTasks` requires `worker_id IS NOT NULL`), cheap to close.
  **Recommended below.**
- **`TestWatchdogIsStartedByMain`'s two unreachable evasions stay disclosed rather than closed**:
  `watchdogMargin, maxAssignment = 0, 0` inserted above the call, and a pre-cancelled context handed to
  `Run`. Both compile, both leave every package green, both leave the watchdog dead. **Proposed as an
  amendment to [[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]]**, which is also where the
  tenth `cmd/relay-server` wiring copy belongs.
- **The writer ambiguity is side-stepped, not resolved.** An agent that honours its own timeout writes
  the same `timed_out` and contributes nothing here. The DB-query route is written as **declined, with
  the price and the revisit condition** (a new terminal status threaded through every allow-list
  including the two read backwards, or a nullable writer column plus a migration on an epoch-fenced
  write path). If such a column is ever added for another reason, the query route is genuinely better.
- **`idea-2026-08-21-counters-payload-cannot-say-not-measured` stays open**, with two amendments
  proposed below. The watchdog is now the second section that cannot say "not measured", in a
  materially weaker form.
- **The next slice must say how its number relates to this one.**
  [[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]] counts the agent's view of the
  same event; the two **will not reconcile** (the watchdog also sweeps tasks whose agents are gone and
  never report), and an operator will try to subtract them. `failClaimedTask` is now a ready site with
  no log-line question left to settle - it is **not** counted here.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **twentieth iteration**. Ten
  refutations, two of them against the joint spec's own payload.
- **A backlog proposal is not a contract** - twenty for twenty. The item's own Done-When was RED under a
  shipped guard.
- **Each stage treats the previous stage's output as untrusted** - honored in both directions: the plan
  refuted the spec and the item; the lenses refuted the plan's shipped log line; the re-verify round
  refuted the remediation.
- **A guard that counts a PROPERTY, not a spelling** - honored, and extended: this slice found a guard
  that counted the right property **of the wrong subject**.
- **A mutation proof must leave a test behind** - honored; all four mutation findings left permanent
  executed checks.
- **A mutation proof is only as strong as the poisoned input's POSITION** - honored; the exemption's
  hostile rows are first, and the real-producer test asserts shape before cardinality for the same
  reason.
- **Wrong prose about correct code is the dominant defect class** - **fifteenth consecutive iteration**,
  and this time it appeared *inside the remediation commit whose job was to remove one*.
- **Say "declined, and here is the price"** - honored at both required sites (the DB-query route, the
  writer ambiguity).
- **State a coverage limit rather than implying it** - honored: the exemption's `why` states its own
  blindness, and the wiring guard's `WHAT IT CANNOT REACH` block was extended rather than quietly
  outgrown.
- **Backlog housekeeping is required scope** - both closes belong to the conductor, via
  `/backlog close`, never a hand-edited `status:`.

New from this iteration:

- **A lossy aggregate must disclose its loss wherever it is READ, not only where it is published.**
  Publishing `swept_overflow` in JSON discharged nothing for the operator reading a log line. Every
  rendering of a bounded aggregate into a sentence is a place the bound has to be re-disclosed - and it
  is exactly where it will not be, because a sentence is written to be short. **Candidate for durable
  memory.**
- **Prefer deleting a rule's antecedent to guarding its consequent.** "Restating needs an arity check"
  was satisfied by not restating. The guard that remains asks a question about a type, which no fixture
  can satisfy.
- **A guard can be perfectly correct and pointed at the wrong subject.** Ask, of every predicate: *whose
  bytes has this ever actually seen?* If the answer is "a literal in this file", the guard proves the
  fixture well-formed and nothing else - and when the import direction forbids the fix, the check has to
  move to a package that can import both sides.
- **A clause that always fires is not a signal.** `worst: worker X with 1` beside `256 swept across 256
  workers` is noise wearing a diagnosis's clothes. Give a summary sentence a threshold, and say nothing
  below it.
- **When a refactor splits one statement into two, ask what the single statement was forcing.** The
  split created a question the single form could not pose, and the guard had to grow to ask it.

## Files Most Touched

- `internal/scheduler/watchdog.go:283-329` - the gated summary and its three-arm worst clause. This is
  the headline finding written where the next person to touch the line will hit it.
- `internal/scheduler/watchdog_counters.go` - `worst()`'s comment (`:108-120`, why the third return
  value is the point rather than a convenience) and `canonicalWorkerKey`'s (`:140-162`, the uniqueness
  claim replaced by a property plus a grep).
- `internal/scheduler/watchdog_counters_test.go:280-369` -
  `TestWatchdog_APersistentWriteFailureIsBoundedToOneLinePerSweep`, which carries two measured evasions
  and explains why phase 1 exists at all.
- `cmd/relay-server/counters_wiring_test.go:932-1016` - the real-producer-bytes test and the measured
  newline-injection evasion in its comment. This slice's whole guard story is here.
- `internal/api/server_counters_test.go:466-560` - the exemption's `why`, which states both halves of
  the security question **and its own blindness**, plus the descending predicates.
- `internal/scheduler/dispatch.go:461-500` - the partition comment: the shape a uniqueness claim should
  have been written in, in the place the wrong one was twice written.
- `cmd/relay-server/watchdog_config_test.go:99-166` - checks (1)-(4), the slice-4 split, and the honest
  `WHAT IT CANNOT REACH` boundary.
- `docs/superpowers/plans/2026-08-24-silent-drop-observability-slice4.md` - R1 (the spec's two payload
  errors), R2 (the antecedent argument), R4 (the item's unshippable Done-When), R8 (the `main`
  restructure), and the M1-M20 matrix.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`,
  no `git diff`, no test run. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `internal/scheduler/watchdog_counters.go` in full;
  `internal/scheduler/watchdog.go:188-331`; `internal/scheduler/watchdog_counters_test.go` (the test
  inventory and `:280-369` in full); `internal/scheduler/dispatch.go:440-500`;
  `internal/api/server_counters_test.go:466-601` and `:1060-1230`;
  `cmd/relay-server/counters_wiring_test.go:916-1016`; `cmd/relay-server/watchdog_config_test.go:99-220`;
  `README.md:1279-1302`; the slice-4 plan in full; the closing item in full including all four dated
  addenda; the slice-3 retro in full; and
  `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` and
  `idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md` for duplicate checking.
- **Confirmed against code, not inferred:** that `worst()` returns three values from one lock and that
  `SweepOnce` consumes the third; that the inner `swept > 0` gate exists and is pinned by a
  `NotContains(t, l, "worst")`; that `grep` for `dispatchTask` over `*.go` returns only generated
  protobuf hits, so the false name is gone; that no watchdog integration test references
  `CounterSnapshot`; that `cmd/relay-server/grpc_admission_e2e_integration_test.go` contains zero
  occurrences of `watchdog`; that `counterPayloadAllowList` now has the `swept_by_worker` entry with
  descending predicates and the cap; and that `canonicalUUIDRe` and `canonicalWorkerKeyRe` are two
  restatements of the producer's `canonicalWorkerKey`, both in test files.
- **Reported by the implementing and verifying lanes, not re-run here:** the commit count (18), unit
  603 -> 626 passing / 627 run with 1 pre-existing Windows-only skip, the integration suites, the
  `-race` Linux-container run and the `origin/main` reproduction of the broken Windows TSan, the
  `GOMAXPROCS=1 -count=30` flake check, the 0-byte `internal/store/` diff, the empty `web/` diff, and
  every mutation result including M1-M20 and the four evasion demonstrations.
- **Not verified:** all test results, the commit set and diff stat, and the change set as `git` sees it.
  Each is attributed above.
- **One discrepancy the conductor must confirm:** the plan's Task 10 Step 5 enumerates the exact
  expected file set and **does not include `cmd/relay-server/watchdog_config_test.go`**, which this slice
  changed during remediation. That is a legitimate addition (the `main` restructure forced the guard
  split), not a stray artifact - but the exact-file-set check should be run against the corrected list
  rather than the plan's.
- **No PR number appears anywhere in this retro or in the proposed items**, by instruction. The work is
  referenced by date and slug.
- **Outstanding and belonging to the conductor:** the two `/backlog close` runs with their `git mv` into
  `docs/backlog/closed/`, the amendments listed below, the exact-file-set check, the final gates, all
  commits, and a ROADMAP refresh.

## CLAUDE.md verdict

**No amendment.** Neither candidate earns one, and the reasons differ.

- **"Declared in the consumer package" does not belong there.** CLAUDE.md's Invariants are cross-cutting
  rules that new code **must not bypass**. This one cannot be bypassed - the compiler enforces it, since
  `internal/scheduler` imports `internal/api` and the reverse import does not build. It is already
  stated in three code comments, the joint spec and the closing item. Adding a compiler-enforced fact to
  a list of bypassable rules dilutes the list.
- **The consumer side of the cardinality rule is real, but it is a one-instance lesson and the rule it
  would extend is not in CLAUDE.md at all.** The counters cardinality rule lives in the joint spec and
  in `internal/api/server_counters.go`'s doc block. This project's own convention across this batch has
  been to amend an **existing** bullet when a new instance of it appears (the epoch-fence bullet, five
  times), not to open a new bullet from a single occurrence. The lesson is recorded in this retro's
  Improvement Goals and flagged as a durable-memory candidate.

**Revisit condition, stated so this is a decision rather than a deferral:** if a second instance appears
in a different subsystem - a bounded or sampled aggregate rendered into an operator-facing sentence
without its loss indicator - it earns a bullet. If the human wants it now, the wording I would propose
is:

> - **A bounded aggregate discloses its bound at every read site.** Where a counter, map or sample is
>   capped, truncated or sampled, the loss indicator is not discharged by being published alongside the
>   data. Every place the aggregate is rendered into a sentence - a log line, a CLI summary, a UI
>   badge - either carries the qualification or omits the claim. `watchdogCounters.worst` is the live
>   example: it returns `SweptOverflow` from the same lock acquisition as the maximum, because a caller
>   that read them separately could report a maximum qualified by an overflow measured at a different
>   instant. When you write a summary sentence over an aggregate, ask what the aggregate is allowed to
>   not know.

## Recommended Backlog Items

Proposals only - the conductor files via `/backlog`, and the human gives final accept. Both are
high-confidence and specific; neither is a duplicate of an open item, and the adjacency to existing
items is stated in each.

**1. `internal/api`'s counter payload guards are proven against fixtures, not producers**
- type: `idea`, priority: `medium`
- Every `counterPayloadAllowList` predicate and both payload walks in
  `internal/api/server_counters_test.go` run against fake sources whose values are literals in that
  file, and `internal/api` structurally cannot import the producing packages to fix it. Slice 4 proved
  this is not theoretical: mutating `internal/scheduler`'s producer to emit
  `"build-agent-07.corp.example\n10.0.0.7"` as every `swept_by_worker` key - the exact payload the
  exemption's own argument cites - left both `internal/api` and `cmd/relay-server` green, because the
  only assertion that had ever touched those keys was a `require.Len`. It was closed for `watchdog`
  only, by `TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap` in
  `cmd/relay-server`, which can import both sides. The other three sections have no default-lane
  equivalent, and their real-producer checks
  (`internal/api/server_counters_realsocket_integration_test.go`,
  `cmd/relay-server/grpc_admission_e2e_integration_test.go`) are integration-tagged.
- Adjacent to [[idea-2026-08-23-integration-only-guards-ci-never-runs]] but **not the same gap**: that
  item is about the lane, and fixing the lane would still leave `internal/api`'s own predicates
  checking fixtures. Say so in the Related section so the two are not merged by a future refresh.

**2. The watchdog sweep counters are never driven by real `ListOverdueAssignedTasks` rows**
- type: `idea`, priority: `low`
- `internal/worker/handler_watchdog_e2e_integration_test.go` already drives `SweepOnce` against real
  Postgres rows in three tests and asserts nothing about `CounterSnapshot`; every counter assertion in
  the repo runs against `fakeWatchdogStore` (`internal/scheduler`) or `sweepableStore`
  (`cmd/relay-server`), both hand-written fakes. A `worker_id` shape that real SQL returns and
  `canonicalWorkerKey` rejects would go silently to `swept_overflow` with every package green.
- Genuinely low: `uuidStr` over a valid `pgtype.UUID` always renders canonically and
  `ListOverdueAssignedTasks` requires `worker_id IS NOT NULL`, so no live defect is claimed. The value
  is that the expensive fixture already exists and the assertion is a few lines - it closes "the fake
  agrees with the SQL" for the one section whose key comes out of a database row.

**Amendments (no new file - the conductor edits the existing items):**

- **[[idea-2026-08-21-counters-payload-cannot-say-not-measured]]** (deferred, stays open) - record that
  the watchdog is now the **second** section that cannot say "not measured", in a **materially weaker
  form** than `netlimit`'s (every number a disabled watchdog publishes is literally true; what is
  missing is an answer to "is it on?", which is an unanswered question rather than an affirmative false
  statement, and the item's Summary rests on the falsehood). Record that slice 4 shipped **no configured
  constant in any `levels` half, and therefore no `levels` half at all**, so a future `limits`
  classification is purely additive with zero field moves - and that the item's own sequencing argument
  is now worth **one section, not three**.
- **[[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]]** - record that
  `Dispatcher.failClaimedTask`'s `pgx.ErrNoRows` arm is now silent, so it is a ready site with no
  log-line question left to settle; that it is **not** counted here, deliberately, so that slice owns
  its own design; and that the partition comment at `internal/scheduler/dispatch.go:462-497` is the
  enumeration to read rather than re-derive.
- **[[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]]** - record the **tenth** copy
  (`s.Counters.Watchdog = d.watchdog` under its own filter); that `main` now binds the watchdog to a
  local above `buildHTTPServer`, which split `TestWatchdogIsStartedByMain`'s construction and start
  checks into two and forced a new "same identifier" question; that a **positional** check on the two
  `time.Duration` bounds was required because transposing them was measured green (margin 24h, cap 30m
  in production); and that two evasions remain disclosed rather than closed -
  `watchdogMargin, maxAssignment = 0, 0` inserted above the call, and a pre-cancelled context handed to
  `Run`, both of which leave the watchdog dead with every package green.
