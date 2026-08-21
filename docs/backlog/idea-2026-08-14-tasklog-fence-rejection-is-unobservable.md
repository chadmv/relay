---
title: A rejected task-log chunk is completely unobservable, and since the trailing window it can mean "legitimately late"
type: idea
status: open
created: 2026-08-14
updated: 2026-08-21
priority: medium
source: Phase 4 of the 2026-08-14-tasklog-terminal-append-bound slice; the diagnosability cost that slice accepted deliberately
---

# A rejected task-log chunk is completely unobservable, and since the trailing window it can mean "legitimately late"

## Summary

`handleTaskLog` (`internal/worker/handler.go`) drops a chunk whose `AppendTaskLog` fence returns
`pgx.ErrNoRows` **silently**: no log line, no counter, no metric, and nothing returned to the agent.
The chunk is stored nowhere and published nowhere, which is correct. What is missing is any way for
an operator to know it happened.

That was defensible while `ErrNoRows` meant one thing. As of the 2026-08-14 trailing-window bound it
means three:

1. **Stale generation** - the assignment ended (epoch mismatch). Forged, or a zombie agent.
2. **Wrong assignee** - the sender is not the task's `worker_id`. Forged.
3. **Closed window** - the task finished longer ago than `RELAY_TASKLOG_TRAILING_WINDOW`. **This one
   happens legitimately**, to a chunk buffered in the agent's `sendCh` across a long coordinator
   outage, and it is the case an operator who set the knob too small will hit constantly.

An operator who sets `RELAY_TASKLOG_TRAILING_WINDOW=15s` (units confusion is the likely mistake, and
the parser already warns about it at startup) gets task output silently truncated with **no signal of
any kind** at runtime. The startup warning fires once, months before anyone reads the log.

## Context

The 2026-08-14 spec took this trade explicitly and wrote down why the obvious fix is wrong: a log
line on this path would be **caller-driven volume on the gRPC recv goroutine**, handing back the
exact flood vector [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] is about, and it would fire
on the legitimate late-flush case - the one an operator would most want quiet. Both the 2026-08-12
assignee-fence spec and this one argued for keeping the three rejection reasons indistinguishable
**to the caller**, which is still right: telling a prober why it was rejected is free information.

Indistinguishable to the caller and invisible to the operator are different properties, and only the
first was decided on purpose.

### Update 2026-08-15 - the arm is now a named branch with a pinning test, and this item is cited from source

The `2026-08-15-tasklog-err-limiter-keying` slice split `handleTaskLog`'s error handling into two
explicit arms and left the `ErrNoRows` arm shaped for exactly this item. **Scope, priority and
acceptance criteria are unchanged.** What changed is that the work got cheaper and the item got a
source citation.

- **The branch exists and is named.** The compound `if !errors.Is(err, pgx.ErrNoRows) && ...` is gone;
  the `ErrNoRows` case is now its own arm with its own `return` and its own comment block. The counter
  is a one-line insertion into a branch that already exists, with no re-litigation of the condition.
- **The arm's comment cites this item by name, in source**, and states that the answer here is a
  COUNTER and not a log line. Leaving this item unfiled or closing it without doing the work would
  strand that citation.
- **It is pinned by a test.** `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` asserts the
  **whole captured log is empty** across a fence rejection, so any future wording on that arm reddens
  it. That is a gift and a constraint: the counter must not log, and the test will say so.
- **The "no log line" argument survives, in a modified form.** Every other caller-driven log line on
  this goroutine is now budgeted by a per-connection token bucket
  (`internal/worker/ingest_log_limiter.go`), so a line here would be bounded rather than unbounded.
  It is still the wrong mechanism: it would fire on the legitimate late-flush case, it would spend a
  token that a genuine infra failure needs, and the arm's whole value is that it is silent. Keep the
  counter.
- **Half of the "where does an operator read it" question now has a second stakeholder.**
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] was filed on the **complementary arm** of the
  same `if` - counting log lines the budget dropped, across five kinds in three handlers. It has the
  same read-surface dependency as this item. **Spec the two together and ship them as two slices**, so
  the endpoint decision is taken once. They are deliberately separate items because no input executes
  both arms, they count different nouns, and merging them would falsify this item's Done-When.

### Update 2026-08-21 - the read surface exists, and this item's own leading proposal is REFUTED

`docs/superpowers/specs/2026-08-21-silent-drop-observability.md` specced this item with its three
siblings in one sitting, and **slice 1 shipped the shared mechanism**
(`docs/retros/2026-08-21-silent-drop-observability-slice1.md`). The read-surface question this item
has deferred since 2026-08-14 is answered. This item is **slice 3 of four** and stays open: slice 1
settled the surface and added no counter anywhere in `internal/worker`, so none of the acceptance
criteria below is met.

**What now exists and must be extended, not reinvented:**

- **`GET /v1/server/counters`**, `auth(admin(...))`, in `internal/api/server_counters.go`. Admin-only
  deliberately: these numbers describe adversary activity and internal control state, so it is NOT
  modelled on `/v1/jobs/stats` or `/v1/workers/stats`, which are `auth`-only database censuses.
- **`api.CounterSources`** - a struct of nil-able per-subsystem source fields, set at the wiring
  boundary (`cmd/relay-server`'s `buildHTTPServer`). A **nil field means the section is ABSENT from the
  payload, never zero-valued.** A wired section of zeros means "this control ran and stopped nothing";
  an absent one means "not wired on this build or this replica". Do not collapse them, and do not make
  a snapshot method nil-tolerant to avoid a typed-nil panic - filter the typed nil at the wiring
  boundary where the concrete type is still visible.
- **The counts/levels contract.** `counts` are monotonic since `started_at`; `levels` are current. This
  section is `{"task_log_fence": {"counts": {"rejected_total": N}}}` - counts only, no levels.
- **Per replica, per process, zeroed by a restart**, with `started_at` always present.
- **The import direction is FREE for this item.** `internal/api` already imports `internal/worker`
  (`server.go`), so the source interface may return the worker package's own snapshot type. That is
  **not** true for the watchdog sibling, which is why `CounterSources` is a struct of independent
  fields.

**REFUTED, and it is this item's own leading proposal: "Surfaced through the existing `Handler.Metrics`
seam".** The refutation is mechanical rather than aesthetic, and it is decisive:

- **`metrics.Store.Append` is a no-op for an untracked worker.** A counter routed through that map
  inherits a silent-drop path, which is the exact defect class this item exists to close.
- **`metrics.Store.Clear` deletes the whole entry on teardown**, called when a worker goes offline. A
  cumulative counter stored there is **destroyed at the moment the operator would go looking for it**:
  a worker that floods and then disconnects leaves zero.
- `Store` is also lifecycle-coupled to registration (`Activate` at registration), which is the wrong
  lifecycle for a process-lifetime count.

**The `Metrics` WIRING pattern - an exported field set by `cmd/relay-server` after construction and
nil-checked at every use - is the right precedent and the mechanism adopts it. `metrics.Store` itself
is the wrong home and must not gain a counter method.**

**CLOSED, not left open: per-reason splitting is structurally impossible within the one-round-trip
constraint.** This item asks the next reader to "find a way to get the reason out of the existing
statement". There is none. `AppendTaskLog`'s own query comment says a chunk failing any predicate
"matches no fence row, inserts nothing, and returns zero rows" - **there is no row to carry a reason
column on**, so the reason is recoverable only by a second query, which the handler's comment forbids
in as many words. **One number. Say so in the code comment. Nobody should spend an afternoon on this
again.**

**The shape, settled:** one process-wide `atomic.Uint64` in `internal/worker`, incremented in the
existing named arm before its return. An atomic add is not a lock - one locked exchange-add, no
allocation, no map, no scheduling - and it sits next to a Postgres round trip, so the standing
constraint is respected in substance and not merely in letter. Cross-connection cache-line contention
is bounded by `RELAY_GRPC_MAX_CONNS` writers, each doing far more expensive work on the same call.
Per-worker keying is rejected at the increment site: it needs a map write behind a shared lock on the
recv goroutine, which is the thing the constraint forbids.

**Sequencing: this is slice 3, deliberately after the ingest-suppression sibling and NOT merged with
it.** Not because it is harder - it is the smallest of the four - but because of the home: it is one
atomic add plus one payload field, and doing it first would create the `internal/worker` counters
struct for a single number and then reshape it in slice 2. The two remain separate items because they
count different nouns in different branches of the same `if`, no input executes both, and merging
falsifies this item's own Done-When.

**One payload constraint to inherit:** every non-integer field anywhere in the counters payload needs a
`counterPayloadExemption{why, typeOK, jsonOK}` argued in the same commit. `started_at` is the only
exemption today. This section adds a plain `uint64`, so nothing here triggers it - but note the
residual for anyone tempted to add structure: exemptions are shape-checked and **non-descending**, so a
container exemption must do the descending itself inside its predicates.

The read-surface finding below was re-checked at 2026-08-15 and was unchanged then;
**it is superseded as of 2026-08-21** by the endpoint above.

### Update 2026-08-21 (later) - slice 2 shipped the second section. Copy its pattern; do NOT repeat its two gaps.

`docs/retros/2026-08-21-silent-drop-observability-slice2.md` and
`docs/superpowers/plans/2026-08-21-silent-drop-observability-slice2.md`. Slice 2 added the
`ingest_log_budget` section and a counters home in `internal/worker`. **This item is still slice 3 and
still open** - slice 2 added no counter on the fence-rejection arm, and none of the criteria below is
met.

**The section pattern to copy, all six parts, because slice 2 needed every one of them:**

1. **Its own field on `api.CounterSources`**, never a widened existing interface. `IngestLogBudgetSource`'s
   comment says this in as many words and names *this item* as the reason: both counters live on the
   same `*worker.Handler`, and one interface carrying both would make two independent controls appear
   and disappear together.
2. **A CONCRETELY typed field on `httpServerDeps`** (`*worker.Handler`, not the interface), so the
   typed nil is filtered where the concrete type is still visible.
3. **A per-section `if d.x != nil` in `buildHTTPServer`** - per section, never per struct.
4. **A row in `TestServerCountersIsWiredByMain`'s `wiredDep` table.**
5. **A typed-nil test** (`TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent` is the twin
   to copy).
6. **A wired-but-zero test.** A server whose fence has rejected nothing is the COMMON case and must
   still emit its section: zero means "the control ran and stopped nothing", absent means "not wired".

**THE ONE THING THAT WILL GO RED FIRST, and it is already written into the guard's own comment.**
`cmd/relay-server/counters_wiring_test.go` asserts that the number of **distinct** `httpServerDeps`
fields named by the `wiredDep` table equals `reflect.TypeOf(api.CounterSources{}).NumField()`. This
item adds a **third** `CounterSources` field fed by the **same** `*worker.Handler`, so `NumField` goes
to 3 while the natural table still has 2 rows - RED. **The cheapest path to green is a duplicated
`agentHandler` row, and that is the exact evasion the check was rewritten to stop**: counting rows
rather than distinct fields was proved to let a displaced field fall out of the plain-identifier check,
the derives-from check and the assigned-exactly-once check at a stroke. Resolve it deliberately -
either a second deps field for the same handler, or a cardinality relation that expresses "N sections
over M deps fields" - and say which, in the commit. **Do not relax the check.**

**The two gaps slice 2 shipped and had to be told about. Neither may recur here:**

- **THE CROSS-PACKAGE ARITY GAP.** A fully correct sixth log kind - const inside the sentinel, real
  call site, own array cell, published in `worker.IngestLogDrops` - left all three packages green while
  `internal/api`'s hand-written `ingestLogKindCountsFrom` never mapped it: counted on the recv path,
  published under no JSON key. `counterPayloadLeaves` **cannot** catch that class - it is an
  `ElementsMatch` against a list derived from the api-side struct, so it reddens on an EXTRA api leaf
  and never on a MISSING worker-side one. The rule: **any section whose payload struct restates fields
  owned by another package needs a `NumField` assertion between the two types, written in the package
  where both are visible** (`TestIngestLogKindCountsPublishesEveryWorkerSideField` is the live
  example). Cardinality alone is enough, because a hand-written by-name mapping already makes a rename
  a compile error. **If this section's source returns a bare `uint64`** - the shape settled above -
  there is no restated struct and the rule does not apply; **if it returns a struct of any width, the
  assertion ships in the same commit.**
- **THE `buildHTTPServer` FORWARDING GAP.** Nothing checked that the function forwards the source it
  was GIVEN. Substituting a freshly constructed `worker.NewHandler` for `d.agentHandler` compiled,
  vetted clean and left everything green, serving a permanently-zero section. The wiring guard cannot
  see it: that guard is about `main.go`'s identifiers, which is a different question. **Two questions,
  two guards** - does main pass what it registered (syntactic), and does `buildHTTPServer` forward what
  it was given (executable). Slice 2's answer is
  `TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers`: a real registered stream,
  real drops, read back through the real admin route. **It is integration-tagged**, because moving the
  counter needs `Connect`'s message loop and therefore Postgres, so CI compiles it and does not run it;
  the default-lane presence/absence pair carries what CI can see. Expect the same split here, and state
  it rather than implying it.

**One thing slice 2 makes cheaper for this item, and one it does not.** Cheaper: `internal/worker` now
has a counters home, a snapshot-method precedent (`Handler.IngestLogDropCounts`), and a value-field
convention (the zero value is ready to use, so a bare `&Handler{}` in a test has working counters and
there is no nil case anywhere). Not cheaper: this counter is a **different noun in a different branch**,
and slice 2's own `&&`-short-circuit finding is the proof - `handleTaskStatus`'s `pgx.ErrNoRows`
`GetTask` never reaches the budget at all, and this item's arm never consults it either.
`ingest_log_budget` counts *log lines the budget dropped*; `task_log_fence` counts *rejections*.
Neither number covers any part of the other, and the payload must not imply otherwise.

## Proposal

A counter, not a log line. Shape, to be argued at spec time rather than adopted as written.
**Superseded in part by the 2026-08-21 section above**; where the two disagree, verify the later
reasoning against the code rather than inheriting either.

- **One atomic add on the rejection path.** `taskLogFenceRejects` incremented where `ErrNoRows` is
  handled, before the existing early return. No allocation, no lock, no round trip - the standing
  constraint on this handler is one DB round trip and nothing else, and an `atomic.Uint64` respects
  it. **(2026-08-21: confirmed as the shape. Package-level in `internal/worker`.)**
- **Surfaced through the existing `Handler.Metrics` seam**, not test-only. **(2026-08-21: REFUTED for
  the TYPE and CONFIRMED for the WIRING PATTERN. `metrics.Store.Append` no-ops for an untracked worker
  and `Clear` deletes the entry on teardown, so the counter would be destroyed by the disconnect that
  caused it. Use the exported-field-set-by-main pattern; do not add a counter method to
  `metrics.Store`.)** The three sub-options this item originally listed - a counter method on
  `metrics.Store`, a per-worker key, or a separate counters type - resolve to the third.
- **Explicitly NOT a log line.** State this in the code comment where the counter is incremented, or
  the next person will "improve" the counter into a `log.Printf`. Cite the limiter item. **(2026-08-15:
  the arm's existing comment already says this and names this item; extend that comment rather than
  writing a second one, and note that a budgeted line is also not acceptable here.)**
- **Consider splitting the counter by reason.** **(2026-08-21: CLOSED as structurally impossible - the
  fence returns no row at all, so there is nothing to carry a reason on and the only route is a second
  round trip the constraint forbids. One number, with the reason written in the comment.)**
- **Where an operator reads it.** **(2026-08-21: ANSWERED. `GET /v1/server/counters`, admin-only,
  shipped by slice 1. This item does not extend `GET /v1/workers/stats` - that is a database census,
  `auth`-only, and mixing process-lifetime in-memory counters into it makes one payload with two
  incompatible truth models.)**

## Acceptance / Done When

- A rejected chunk increments a counter, proven by a handler-layer test that reads the counter across
  a rejection and a success.
- The counter is readable by an operator through an endpoint, not only from a test. **(2026-08-21: the
  endpoint exists; this bullet now means the `task_log_fence` section is populated and served.)**
- No new log line on the rejection path - **including a budgeted one** (2026-08-15) - and
  `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` still passes with no assertion changed.
- `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` still passes (2026-08-15).
- `handleTaskLog` still performs exactly one DB round trip and one statement, with no goroutine, no
  queue and no lock added to the recv goroutine.
- The counter cannot be used by an agent to learn why its chunk was rejected (it is server-side
  observability, never a response).
- **(2026-08-21) The counter is ONE number**, and the arm's comment states that per-reason splitting is
  structurally impossible rather than merely unimplemented.
- **(2026-08-21) The counter does not live in `metrics.Store`**, and the reason is recorded where the
  counter is declared.
- **(2026-08-21) An unwired section is ABSENT from the payload, not zero-valued**, matching the contract
  slice 1 fixed.
- **(2026-08-21, from slice 2) The section gets its OWN `CounterSources` field**, not a widened
  `IngestLogBudgetSource`, and its own `wiredDep` row, typed-nil test and wired-but-zero test.
- **(2026-08-21, from slice 2) The `wiredDep` cardinality check is satisfied by a deliberate decision,
  not by a duplicated row.** A third `CounterSources` field fed by the same `*worker.Handler` makes
  `NumField` disagree with the table's distinct-field count; the resolution is stated in the commit.
- **(2026-08-21, from slice 2) `buildHTTPServer` is proven to FORWARD the source it was given**, by an
  executed test, and the lane that test runs in is stated rather than implied.
- **(2026-08-21, from slice 2) If the source method returns a struct rather than a scalar, a `NumField`
  arity assertion between the worker-side and api-side types ships in the same commit.**

## Related

- Source: `internal/worker/handler.go` (`handleTaskLog`'s `pgx.ErrNoRows` branch - now its own named
  arm, whose comment cites this item - and the `Metrics` field beside
  `AllowAutoEnroll`/`TrailingLogWindow`), `internal/metrics/store.go` (the seam's current per-worker
  shape, and the `Append`/`Clear` semantics that rule it out as a home),
  `internal/api/worker_metrics.go` (how metrics reach the API today)
- **The read surface, shipped 2026-08-21**: `internal/api/server_counters.go` (the payload contract for
  all four sections), `internal/api/server_counters_test.go` (`counterPayloadExemption`),
  `internal/api/server.go` (the route), `cmd/relay-server/http_server.go` (the wiring boundary)
- **The section pattern to copy, shipped 2026-08-21 by slice 2**:
  `internal/worker/ingest_log_counters.go` (the counters home and the snapshot method),
  `internal/api/server_counters.go` (`IngestLogBudgetSource`, whose comment names this item),
  `internal/api/server_counters_test.go` (`TestIngestLogKindCountsPublishesEveryWorkerSideField`),
  `cmd/relay-server/counters_wiring_test.go` (the `wiredDep` table and the DISTINCT-FIELDS block, which
  already predicts what this item will hit), `cmd/relay-server/grpc_admission_e2e_integration_test.go`
  (`TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers`)
- The slice that added the third meaning: `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`
  section 6.4 ("What the agent sees when the window has closed: nothing"),
  `docs/retros/2026-08-14-tasklog-terminal-append-bound.md`
- The slice that shaped the arm for this work and pinned it with a test:
  `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md` section 5 ("what this spec owes the
  counter item"), `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`
- The joint spec and the slices that settled the mechanism:
  `docs/superpowers/specs/2026-08-21-silent-drop-observability.md` (sections 2.1, 3.2, 7.3, 10.3),
  `docs/retros/2026-08-21-silent-drop-observability-slice1.md`,
  `docs/retros/2026-08-21-silent-drop-observability-slice2.md`
- **Sibling on the complementary arm, shipped FIRST**: [[idea-2026-08-15-ingest-log-suppression-is-uncounted]]
  (**closed 2026-08-21**)
- Siblings on the same shape: [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]
- Must not regress: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] - the reason this is a
  counter and not a log line. **Closed 2026-08-15**; now in `docs/backlog/closed/`.
- Would add a fourth silent rejection reason: [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]].
  Landing that first without this makes the diagnosability problem worse; landing this first makes
  that one cheaper.
- The knob whose misconfiguration this detects: `RELAY_TASKLOG_TRAILING_WINDOW`
  (`cmd/relay-server/main.go`, `parseTrailingLogWindow`)

## Notes

The generalizable rule this item exists to record: **a silent rejection path needs a counter the day
a second reason to reject is added to it.** One reason is diagnosable by reasoning about the code -
an operator seeing missing logs can conclude "the agent is stale". Three reasons, one of which is
legitimate and configuration-dependent, is not. The counter is cheap; the moment to add it was the
moment the meaning stopped being singular, which has already passed.

**2026-08-15 addendum to that rule, from the sibling item:** a rate limiter that drops silently has
the same shape and the opposite direction. This arm is silent because nothing was ever said; the
budget's arm is silent because something was said and then suppressed. Both convert a diagnosable
condition into an absence of evidence.

**2026-08-21 addendum:** this item was accurate about the defect for seven days and wrong about the
remedy the whole time - `metrics.Store` would have destroyed the number on the disconnect that
produced it. That is the project's recorded "an accurate item can prescribe a wrong remedy" pattern,
and the refutation was mechanical (two function bodies), not a matter of taste. Verify the prescribed
fix separately from the diagnosis.

**2026-08-21 second addendum, from slice 2:** the mechanism built because controls silently stop
reporting shipped a counter that could silently stop counting. Whatever this slice adds, ask what
compares the two ends of every hand-written copy it introduces - that was the single worst finding of
slice 2 and it was found three times independently, once the question was asked.
