---
title: Repeated watchdog sweeps against one worker are unsurfaced, so a wedged worker becomes a silent sink for queued work
type: idea
status: closed
created: 2026-08-20
updated: 2026-08-21
closed: 2026-08-24
resolution: fixed
priority: medium
source: Phase 4 security lens of the 2026-08-20-coordinator-stale-task-watchdog slice; the diagnosability cost that slice accepted
---

# Repeated watchdog sweeps against one worker are unsurfaced, so a wedged worker becomes a silent sink for queued work

## Summary

The coordinator stale-task watchdog (`internal/scheduler/watchdog.go`) ends an overdue assignment by
stamping `timed_out`, which **frees the worker's slot** - `CountActiveTasksByAllWorkers` counts only
`status IN ('dispatched','running')`, so the moment the row goes terminal the dispatcher considers
that worker to have capacity again.

The coordinator has no way to **compel** the agent to stop. `Watchdog.sendCancel` calls
`Registry.SendCancel(workerID, taskID, false)` and **discards the return value**, deliberately: the
watchdog is registry-blind by design, the agent may be connected to a different replica, and
`CancelTask` is a message to an untrusted peer that is free to ignore it.

Put together, a wedged or hostile worker changes shape rather than getting better. Before the
watchdog it held a **fixed** set of tasks forever. Now it **drains** queued work at roughly
(slots / max-assignment) and fails each item, indefinitely. Neither behaviour is clearly worse than
the other - the second at least keeps the job status machine moving and lets an operator see failures
- but **nothing surfaces the pattern**, and the pattern is the actionable part.

**Repeated sweeps against the same `worker_id` are the tell that a worker should be disabled**, and
there is no counter, no metric and no aggregated log line that exposes it. `SweepOnce` logs one line
per swept task, which names the worker, but nothing aggregates by worker and nothing survives the
process. An operator has to read the raw log and notice a repeating UUID.

## Repro / Symptoms

1. Run an agent patched to accept dispatches, report `RUNNING`, and never report terminal (or simply
   ignore `CancelTask`). Give it a slot count of 4.
2. Submit a stream of tasks. Every ~`RELAY_TASK_MAX_ASSIGNMENT` (24h by default; set it to `1h` to
   observe in an afternoon), the watchdog sweeps that worker's four tasks, marks them `timed_out`,
   cascades their transitive dependents to `failed`, and frees four slots.
3. The dispatcher immediately hands the same worker four more tasks.
4. Observed: an unbounded number of jobs fail over time, attributable to one machine, and the only
   evidence is N lines per sweep in the server log with the same worker UUID in each. Nothing in
   `GET /v1/workers`, `GET /v1/workers/stats` or `GET /v1/workers/{id}/metrics` reflects it, and the
   worker's `last_seen_at` stays fresh because its stream is healthy.

Expected: something an operator can query that says "worker X has had 37 assignments swept in the
last 24 hours", which is a disable decision with one number behind it.

## Context

Found by the Phase 4 security lens of the watchdog slice, while pricing what that slice's fix does
**not** buy. The slice's own Known Limitations record the mechanism ("the freed slot is optimistic -
the coordinator releases the worker's slot while the subprocess may still be running, so a machine
with a wedged task can be handed more work"); this item is the observability half of that sentence.

**This is a sibling of two open items, not an amendment to either, and the split is deliberate.**

- [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] is scoped to `handleTaskLog`'s
  `pgx.ErrNoRows` arm - a **chunk rejected by the fence** - and its acceptance criteria are about a
  rejection counter. (**Closed 2026-08-21** by slice 3.)
- [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] is scoped to `ingestLogLimiter.allow`'s two
  `return false` paths - a **log line dropped** - across five kinds and three handlers.
  (**Closed 2026-08-21** by slice 2.)
- This one counts a **third noun** in a **fourth place**: an *assignment terminated by the
  coordinator*, on a periodic writer in `internal/scheduler`, not on the gRPC recv path at all.

All three are instances of the same shape - "the system now silently drops or kills something and
nobody can see it" - and all three want the same read surface. **Spec them in one sitting and ship
them separately**, exactly as the 2026-08-15 sibling already recommends for the first two. Folding
this into either would widen it from one arm in one handler to a different package, and this project
keeps finding items that are wrong about their own scope precisely because somebody grew one by
amendment.

## 2026-08-21: the read surface exists. This item is now the LAST slice of four, not the first.

`docs/superpowers/specs/2026-08-21-silent-drop-observability.md` specced all four items in one
sitting, and **slice 1 shipped the shared mechanism** (`docs/retros/2026-08-21-silent-drop-observability-slice1.md`).
The expensive shared part every version of this item deferred to is settled. What changed for this
item specifically:

**What now exists and must be extended, not reinvented:**

- **`GET /v1/server/counters`**, `auth(admin(...))`, in `internal/api/server_counters.go`. Admin-only
  deliberately: these numbers describe adversary activity and internal control state, so it is NOT
  modelled on `/v1/jobs/stats` or `/v1/workers/stats`, which are `auth`-only database censuses.
- **`api.CounterSources`** - a struct of nil-able per-subsystem source fields, set at the wiring
  boundary. A **nil field means the section is ABSENT from the payload, never zero-valued**: a section
  of zeros means "this control ran and stopped nothing", an absent section means "this build or this
  replica does not have that control wired". Do not collapse the two.
- **The counts/levels contract.** `counts` are monotonic since `started_at`; `levels` are current. A
  reporter may consult `counts` to decide whether to speak and may **never** consult `levels`. This
  item's aggregate sweep line is exempt from the comparison problem because it is driven by the sweep
  itself rather than by a counter-move test - but the monotonic-versus-current classification is part
  of the payload contract and applies here regardless.
- **Per replica, per process, zeroed by a restart**, with `started_at` always present. The watchdog is
  multi-replica-safe by first-write-wins, so a sweep of worker X may be counted on either replica. Say
  so in the field documentation.

**THE HARD CONSTRAINT, and it is the one thing here that will otherwise be rediscovered as a compile
error under time pressure: `internal/scheduler` ALREADY IMPORTS `internal/api`** (`scheduler/dispatch.go`,
`scheduler/source_proto.go`). So `internal/api` can **never** import `internal/scheduler`, and this
section therefore **cannot** follow slice 1's pattern of "the source interface returns the subsystem's
own type". The required shape is:

- declare the watchdog snapshot type (`WatchdogCounters` or similar) **inside `internal/api`**, next to
  the other response types;
- declare `type WatchdogSource interface { CounterSnapshot() WatchdogCounters }` **inside
  `internal/api`**;
- have `scheduler.Watchdog` return that type - legal, because scheduler already imports api.

`CounterSources` is a struct of independent fields precisely so each section can make that choice
separately. The note is already in `server_counters.go`'s doc comment; it is repeated here so this
item carries it.

**THE TYPED-NIL TRAP, which is not hypothetical for this section.** The watchdog is legitimately
disable-able (`RELAY_TASK_WATCHDOG_MARGIN=0`, `RELAY_TASK_MAX_ASSIGNMENT=0`), so
`var wd *scheduler.Watchdog; if enabled { wd = ... }; CounterSources{Watchdog: wd}` is the natural
shape **and it panics**: a typed nil pointer stored in an interface is not `== nil`, so the handler's
`src != nil` is true and the snapshot call dereferences a nil receiver - a goroutine stack trace to
the log per admin request, inside the feature whose subject is bounding log volume. Filter the typed
nil at the wiring boundary where the concrete type is still visible (`cmd/relay-server`'s
`buildHTTPServer` is the live example, guarded by
`TestBuildHTTPServer_TypedNilListenerLeavesTheSectionAbsent` and, since slice 2, by
`TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent`). Do **not** instead make the
snapshot method nil-tolerant: returning a zero snapshot turns an unwired control into a section of
zeros, which is the one distinction this payload exists to preserve.

**THE EXEMPTION-PREDICATE RULE, and `swept_by_worker` was DELIBERATELY DE-AUTHORIZED.** Slice 1's spec
pre-blessed `watchdog.counts.swept_by_worker` in the payload's non-integer allow-list, against code
nobody had written. **That entry was removed during slice 1's review**, and the removal is the point:
pre-authorizing it reduced its only forcing function to a one-line edit with the justification already
supplied. A map keyed on server-resolved worker UUIDs may well still be the right answer here - but it
now costs a `counterPayloadExemption{why, typeOK, jsonOK}` argued **in the same commit that can be read
against the code**, including whether unbounded key cardinality is acceptable. The admin-authentication
argument (this route is not an attacker-writable site, so a worker UUID admissible HERE stays
inadmissible in any log line reachable from the gRPC recv path) is one input to that decision, not a
standing grant.

**And the residual that entry must be written knowing:** exemptions are shape-checked but
**NON-DESCENDING** - both payload walks stop at an exempted path once the predicate passes. That is
right for a scalar like `started_at`, whose predicate examines the whole value. It is **wrong for a
container**: a `jsonOK` that merely accepted `map[string]string` would leave every key and every value
uninspected, which is the total exemption the predicate mechanism replaced, re-entered through the
predicate. A `swept_by_worker` exemption must do the descending itself inside `typeOK`/`jsonOK` -
checking key shape, value shape and cardinality - or the walks must first be taught to recurse past it.
Slice 1 proved this is not theoretical: a `map[string]string` at an exempted path, with a
newline-injected RTL-override key and an IP-address value, passed both guards with zero failures.

**What the spec decided about this item's own design** (`spec` sections 3.1, 7.2 and 10.4), to be
verified against code rather than adopted, per this project's standing rule:

- **The item's "genuinely easier answer than the other two" framing is REFUTED in emphasis.** The
  premise (the event is already durable in `tasks`) is true and the conclusion does not follow. This is
  the **hardest** of the four: the only one whose correct-by-construction route is blocked and whose
  fallback needs the only unbounded-in-principle key in the cluster.
- **The DB-query route is rejected for now, with a revisit condition to record in source.** A windowed
  `COUNT(*)` is better on every axis except one, and that one is fatal: an **agent** writes `timed_out`
  itself (`handler.go` maps `TASK_STATUS_TIMED_OUT` straight through), and the two writers mean opposite
  things about the worker's health. Distinguishing them needs a new terminal status (which must then be
  threaded through every status allow-list, including the two that must be read backwards -
  `AppendTaskLog`'s first arm and `ListOverdueAssignedTasks` - plus `TestTasksStatusVocabularyIsExactly`)
  or a nullable `timed_out_by`-style column plus a migration on an epoch-fenced write path. **If such a
  column is ever added for another reason, revisit this** - write that in the comment where the counter
  lives.
- **The in-process per-worker map is capped**: `sweptByWorker map[string]uint64` at 256, first-come
  rather than top-K, with a `sweptOverflow` plain total for sweeps attributable to untracked workers and
  a `sweptTotal` that always counts every sweep so the two reconcile. Cumulative since process start, no
  rolling window. Read under the `Watchdog`'s own mutex and **copied out** - no interior pointer escapes
  the lock. Unbounded is not an option while
  [[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]] is open.
- **`bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` folds into this slice.** Both items
  want a once-per-sweep aggregate line; shipping them separately produces two lines and a third item to
  reconcile them.

## 2026-08-21 (later): slice 2 shipped the second section. Copy its pattern; do NOT repeat its two gaps.

`docs/retros/2026-08-21-silent-drop-observability-slice2.md`. Slice 2 added `ingest_log_budget`, so
there are now **two** shipped sections rather than one, and the differences between them are what this
item should read for.

**The section pattern, now established by two consumers, all six parts:**

1. **Its own field on `api.CounterSources`.** Never widen an existing source interface: one interface
   carrying two controls makes them appear and disappear together.
2. **A CONCRETELY typed field on `httpServerDeps`** (`*scheduler.Watchdog`, not `api.WatchdogSource`),
   so the typed nil is filtered where the concrete type is still visible. **This is the section for
   which that matters most** - it is the only one of the four that is legitimately disable-able by
   configuration, so the typed nil is the natural shape here rather than a hypothetical.
3. **A per-section `if d.x != nil` in `buildHTTPServer`** - per section, never per struct.
4. **A row in `TestServerCountersIsWiredByMain`'s `wiredDep` table**, naming the deps field and the
   constructor its value must derive from.
5. **A typed-nil test.** `TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent` is the twin
   to copy.
6. **A wired-but-zero test.** A watchdog that has swept nothing is the healthy case and must still emit
   its section. Note that slice 2's version had to walk **two levels** because its `counts` half
   contains objects - `TestServerCounters_WiredButZeroSectionIsStillPresent`'s scalar loop would have
   failed. **This section has the same problem and worse**, because `swept_by_worker` is a map: copy
   `TestServerCounters_WiredButZeroIngestSectionIsStillPresent`, not the `grpc_admission` one.

**THE CARDINALITY CHECK WILL BE RED BEFORE ANYTHING ELSE IS.** (Superseded in mechanism by the slice 3
section below - the relation was rewritten and the RED now comes from an EXECUTED test. The rule "do not
relax it, and do not satisfy it by padding a list" is unchanged and is what carries forward.)

**The two gaps slice 2 shipped and had to be told about. Neither may recur here:**

- **THE CROSS-PACKAGE ARITY GAP, and this section is the one where it bites hardest.** In slice 2, a
  fully correct sixth log kind left all three packages green while `internal/api`'s hand-written
  mapping function never published it - counted on one side, published under no JSON key on the other.
  `counterPayloadLeaves` cannot catch that class: it is an `ElementsMatch` against a list derived from
  the api-side struct, so it reddens on an EXTRA api leaf and never on a MISSING source-side one. The
  rule: **any section whose payload struct restates fields owned by another package needs a `NumField`
  assertion between the two types, written where both are visible**
  (`TestIngestLogKindCountsPublishesEveryWorkerSideField` is the live example, and cardinality alone
  suffices because a by-name mapping already makes a rename a compile error). **The twist for THIS
  section is that the import direction inverts the usual shape**: the snapshot type is declared in
  `internal/api`, so `scheduler.Watchdog` is the side doing the hand-written copy, and the assertion
  has to compare the watchdog's internal counter set against the published struct **in
  `internal/scheduler`**. A `sweptOverflow` that exists on the `Watchdog` and reaches no JSON key is
  exactly the sixth-kind defect with the packages swapped - and it is the field whose whole purpose is
  to make a loss visible.
- **THE `buildHTTPServer` FORWARDING GAP.** Nothing checked that the function forwards the source it
  was GIVEN; substituting a freshly constructed handler compiled, vetted clean and left everything
  green, serving a permanently-zero section. **Two questions, two guards**: does main pass what it
  built (syntactic, `TestServerCountersIsWiredByMain`), and does `buildHTTPServer` forward what it was
  given (executable). Slice 1's `TestBuildHTTPServer_ServesTheRealListenersCounters` and slice 2's
  `TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers` are the two precedents.
  **This section may be the easiest of the three to check at the top rung**, because a sweep can be
  driven from a test without a gRPC stream - `SweepOnce` needs a store, not a recv goroutine - so the
  forwarding proof may be able to move a real number through the real route in a lane closer to CI's
  than slice 2 managed. Check that rather than assuming it, and state the lane either way.

**One more thing slice 2 settled that this item inherits.** The payload must say what it does NOT
count, in the same place it says what it does. Slice 2's numbers exclude a `&&`-short-circuited log
decision and the entire fence-rejection arm, and the section documents that in three places. The
equivalent sentence here is that a **watchdog** sweep count says nothing about agent-written
`timed_out` rows, which is the writer ambiguity this item has carried since it was filed - the counter
does not resolve it, it side-steps it, and the payload has to say so.

## 2026-08-21 (slice 3): the forward guidance the last review produced. THIS IS THE MOST IMPORTANT SECTION FOR SCOPING.

`docs/retros/2026-08-21-silent-drop-observability-slice3.md` and
`docs/superpowers/plans/2026-08-21-silent-drop-observability-slice3.md`. Slice 3 shipped
`task_log_fence`, so **three of four sections exist and this item is the last one.** Nothing in it is
blocked. Four things changed that this item must scope around, and the first is the one that will cost
an afternoon if it is discovered during implementation.

### 1. `swept_by_worker` reddens the payload guard by CONSTRUCTION, and the allow-list entry needs a hard cardinality bound

`TestCounterPayloadCarriesNoIdentifiers` (`internal/api/server_counters_test.go`) walks
`serverCountersResponse`'s type tree and calls **`t.Fatalf` on any leaf that is not a struct and not an
unsigned integer**. Its message names the reason:

> a string, a map key, a slice element or a signed value is where a caller-supplied byte or an
> unbounded cardinality gets in - this repo has already shipped an attacker-keyed counter once

**A `worker_id`-keyed section is a `map`. There is no version of it that passes without an explicit
`counterPayloadAllowList` entry**, and the same is true of the JSON-bytes walk next door. So this is not
a risk to manage, it is a required, argued artifact that ships in the same commit. Budget for it.

**The security question to settle BEFORE implementation, because it splits in two and only one half is
satisfied:**

- **The caller-supplied-byte half is SATISFIED.** Worker ids are server-assigned UUIDs, re-encoded
  canonically; a peer does not choose their bytes. `typeOK`/`jsonOK` should still *check* that (key
  parses as a canonical uuid) rather than assert it - the exemption is granted to a SHAPE, and slice 1's
  finding was that an exemption granted to a name is an exemption from every question.
- **The unbounded-cardinality half is NOT.** With `RELAY_ALLOW_AUTO_ENROLL` on, a peer can cause
  **worker rows to be created** ([[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]], open),
  and therefore can drive the number of distinct keys in a map that an admin-facing payload serialises on
  every request. Server-assigned does not mean server-limited. The 256-cap plus `sweptOverflow` design
  above is the answer to this and it must be presented as such - **a hard bound argued inside
  `typeOK`/`jsonOK`, not a comment beside them** - because the exemption predicates are the only thing
  in the payload machinery that will still be checking this in a year.
- Remember the residual: exemptions are **non-descending**. A predicate that merely accepts
  `map[string]uint64` inspects nothing inside it. Descend, check key shape, check value kind, check
  `len`.

### 2. README already says "there is no per-worker split and there will not be one" - and its REASON does not transfer

`README.md:1288`, scoped to `ingest_log_budget`:

> The five keys name the log site, not the worker: **there is no per-worker split and there will not be
> one**, because keying these counters on anything the recv goroutine would have to look up needs a
> shared map write on the one path that must stay lock-free.

That sentence is correct **and its reason is specific to the gRPC recv goroutine**. This item's counter
is written by `SweepOnce` on the **scheduler goroutine**, under the `Watchdog`'s own mutex, on a periodic
path with a Postgres round trip already in it. None of the stated reason applies.

**Slice 4 must say so explicitly, in README, next to whichever bullet it adds** - or a reader lands on
two adjacent sections of one payload where one says per-worker keying will never happen and the other
does it, with nothing reconciling them. That is precisely the "wrong prose about correct code" class
this project has recorded for fourteen consecutive iterations, and it would be self-inflicted by the
slice that had the most warning.

### 3. The wiring relation was rewritten AGAIN, and the shape of the predicted RED changed

The `wiredDep` `sections []string` column that slice 3's plan introduced was **proved decorative by both
review lenses** and has been removed. Nothing ever read those strings against any code. Two measured
evasions: swapping which row claimed which section left the package `ok`, and **a fourth
`CounterSources` field wired end to end EXCEPT for the `s.Counters.X = d.x` assignment went green
module-wide once one string was appended to an existing row.** That failure scenario was, literally,
this slice.

What replaced it, and what this item now hits:

- **`TestBuildHTTPServer_EverySourceFieldProducesAServedSection` (EXECUTED).** Builds a real server with
  every deps source wired and counts served top-level keys against `1 + NumField(api.CounterSources)`.
  A fourth `CounterSources` field makes this RED, and **it cannot be satisfied by editing a fixture
  alone**: the source must be passed, `buildHTTPServer` must assign it, and `handleServerCounters` must
  render it, before the count comes out right. That is the check to expect first.
- **`countersAssignmentSources` inside `TestServerCountersIsWiredByMain`.** It reads
  `buildHTTPServer`'s own assignments and requires every deps field reaching `s.Counters` to have a
  `wiredDep` row. It **fails closed**: the assignment must be spelled `d.<field>` exactly, so reaching a
  section through a local, a helper or a conversion is RED. This item adds a real new deps field
  (`*scheduler.Watchdog`), so it needs a real new row - `{"watchdog", "<constructor>", "..."}` - and the
  `distinct == len(deps)` equality still forbids a duplicated row.

**Do not relax either.** And take the lesson with them: **a guard's failure message must be read as an
instruction, and the cheapest compliance with it must not be the defect.** The removed relation's message
said "EVERY SECTION needs to be named by exactly one row", and following that literally *was* the
evasion.

### 4. Two prose rules that apply directly to this section's own comments

- **Write "declined, and here is the price", never "impossible".** Slice 3's headline finding: the item
  and the joint spec both said per-reason splitting was "structurally impossible"; the premise was true
  and the word was false, and a one-round-trip variant existed at a stated price. **This item has the
  exact same shape waiting for it** - the DB-query route is *rejected with a revisit condition*, not
  impossible, and the writer ambiguity is *side-stepped*, not unresolvable. Write both that way, with
  the price, in the code comment where the counter is declared.
- **Say what the number does NOT cover in the same place it says what it does**, and make sure a negative
  assertion in a test is provable through a projection only the claimed path produces. Slice 3 shipped a
  success leg that established nothing because every other arm produced the same observation.

### 5. A new sibling counts the OTHER END of the same event

[[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]] proposes counting fence rejections
in `handleTaskStatus`, whose live non-adversarial cause **is this watchdog bumping the epoch**. A sweep
counted here and a discarded terminal report counted there are the coordinator's view and the agent's
view of one event. **They will not reconcile** - the watchdog also sweeps tasks whose agents are gone and
never report at all - so whichever ships second must say how the two numbers relate, or an operator will
try to subtract them.

## Proposal

To be argued at spec time rather than adopted as written. **Superseded in part by the 2026-08-21
sections above**; where they disagree, the later reasoning is the better-evidenced one, but verify it
against the code rather than inheriting it.

- **Prefer the query to the counter, if the numbers reconcile.** A swept task is a durable row with a
  worker id and a `finished_at`; a windowed count over `tasks` needs no process state, survives
  restarts, and is correct across replicas. The catch to settle: a `timed_out` row written by the
  **agent** (`handleTaskStatus`) is indistinguishable in the table from one written by the
  **watchdog**, and they mean opposite things about the worker's health - the first is the agent
  behaving correctly. If the query route is taken, the two must be distinguishable, which probably
  means a column or a distinct status and is the reason this may not be as cheap as it looks.
  **(2026-08-21: rejected on exactly this, with a revisit condition. See above.)**
- **Otherwise, an in-process per-worker counter on the `Watchdog`**, flushed nowhere and read through
  the endpoint. Note that it is per replica and say so, since a fleet with two relay-servers splits
  its sweeps arbitrarily between them.
- **Aggregate the log line as well as, or instead of, counting.** One "watchdog: swept N tasks across
  M workers; worst: worker X with K" line per sweep is close to free and is the smallest thing that
  makes the pattern visible in an existing log pipeline. It also interacts with
  [[bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick]] - both are about what one sweep
  should say - so the two should be looked at together even though they are separate items.
- **Do not auto-disable a worker on a sweep count.** Tempting and wrong at this stage: the threshold
  is a product decision nobody has made, the failure mode of a wrong threshold is taking a healthy
  machine out of a fleet, and the existing `handleDisableWorker` path already gives an operator a
  one-call remedy once they can see the number. Surface first, automate later if asked.
- **Answer "per worker or global" once, for all three sibling items.** Per worker is the useful
  diagnostic and matches where `metrics.Store` already keys. **(2026-08-21: answered. This is the only
  one of the four with a per-worker key; the other three are process-wide or level-only. And
  `metrics.Store` is the WRONG home for any of them - `Append` no-ops for an untracked worker and
  `Clear` deletes the entry on teardown, so a counter there is destroyed by the disconnect that caused
  it. The `Metrics` WIRING pattern is the precedent; the type is not.)**

## Acceptance / Done When

- A repeated sweep against one worker is visible to an operator through an endpoint, not only by
  reading raw log lines, with at least a count over a stated window.
- A watchdog-written `timed_out` is distinguishable from an agent-written one, or the chosen design
  explains why conflating them is acceptable for this signal.
- Whatever is added is per worker, and its per-replica versus fleet-wide semantics are documented.
- No new lock, goroutine or round trip on the gRPC recv path (this item does not touch it, and the
  constraint is stated so a "unify all three counters" refactor cannot quietly violate it).
- The counters or query results cannot be read by an agent - server-side observability, never a
  response on the worker stream.
- The read surface is the one the two sibling items use, or the divergence is deliberate and written
  down. **(2026-08-21: it is `GET /v1/server/counters`. The divergence budget is spent.)**
- **(2026-08-21) The watchdog snapshot type is declared in `internal/api`**, because
  `internal/scheduler` imports `internal/api` and the reverse import is impossible.
- **(2026-08-21) A disabled watchdog leaves the section ABSENT and does not panic**, with the typed nil
  filtered at the wiring boundary rather than by making the snapshot method nil-tolerant.
- **(2026-08-21) Any non-integer field added to the payload - `swept_by_worker` included - ships with a
  `counterPayloadExemption` whose `typeOK`/`jsonOK` predicates descend into the container**, argued in
  the same commit. `swept_by_worker` carries no standing pre-authorization.
- **(2026-08-21) `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` is closed by the same
  slice**, with ONE aggregate line covering both the swept set and the failed-write set.
- **(2026-08-21, from slice 2) Every field the `Watchdog` counts reaches a JSON key**, proven by a
  `NumField` arity assertion between the watchdog's own counter set and the api-declared snapshot type,
  in `internal/scheduler` where both are visible. `sweptOverflow` in particular must not be countable
  and unpublishable.
- **(2026-08-21, from slice 2) `buildHTTPServer` is proven to FORWARD the watchdog it was given**, by
  an executed test, with the lane it runs in stated rather than implied.
- **(2026-08-21, from slice 2) The wired-but-zero test walks the section's full depth**, not the
  `grpc_admission` scalar loop, because this section's `counts` half contains a map and an object.
- **(2026-08-21, from slice 2) The section documents what it does NOT count** - specifically that an
  agent-written `timed_out` contributes nothing - in the payload's own documentation and in README.
- **(2026-08-21, from slice 3) The `swept_by_worker` allow-list entry carries a HARD CARDINALITY BOUND
  enforced inside `typeOK`/`jsonOK`**, not merely described beside them, and the entry's `why` states
  both halves of the security question: worker ids are server-assigned (so no caller-supplied bytes) but
  their COUNT is peer-drivable under `RELAY_ALLOW_AUTO_ENROLL` (so the bound is the control).
- **(2026-08-21, from slice 3) README reconciles this section with the `ingest_log_budget` bullet's
  "there is no per-worker split and there will not be one"**, by naming why that reason (a shared map
  write on the lock-free recv goroutine) does not apply to a scheduler-goroutine counter.
- **(2026-08-21, from slice 3) The new `httpServerDeps.watchdog` field gets its own `wiredDep` row**,
  and `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` goes green only because the source is
  passed, assigned AND rendered - never by editing a fixture or padding a list. Neither guard is
  relaxed.
- **(2026-08-21, from slice 3) The DB-query rejection and the writer ambiguity are written as
  "declined, and here is the price", never as "impossible"**, in the comment where the counter is
  declared.

## Related

- Source: `internal/scheduler/watchdog.go` (`SweepOnce`'s per-task log line; `sendCancel`, which
  discards `SendCancel`'s error and says why), `internal/worker/registry.go` (`SendCancel`),
  `internal/store/query/tasks.sql` (`CountActiveTasksByAllWorkers`, which is what makes the slot free
  the moment the row goes terminal), `internal/api/workers.go` (`handleDisableWorker`, the existing
  operator remedy)
- **The read surface, shipped 2026-08-21**: `internal/api/server_counters.go` (the payload contract for
  all four sections, the import-direction note, the typed-nil note),
  `internal/api/server_counters_test.go` (`counterPayloadAllowList`, `counterPayloadExemption`, and the
  two payload walks - read `TestCounterPayloadCarriesNoIdentifiers`'s `default:` branch before designing
  the map), `internal/api/server.go` (the route), `cmd/relay-server/http_server.go` (`buildHTTPServer`,
  the wiring boundary)
- **The section pattern, established by three consumers**: `internal/worker/ingest_log_counters.go`,
  `internal/api/server_counters.go` (`IngestLogBudgetSource`, `TaskLogFenceSource`),
  `internal/api/server_counters_test.go` (`TestIngestLogKindCountsPublishesEveryWorkerSideField`,
  `TestServerCounters_WiredButZeroIngestSectionIsStillPresent`,
  `TestTaskLogFenceSourceReturnsAScalar`),
  `cmd/relay-server/counters_wiring_test.go`
  (`TestBuildHTTPServer_EverySourceFieldProducesAServedSection` - the EXECUTED completeness relation that
  will be this item's first RED - the `wiredDep` table, and `countersAssignmentSources`),
  `cmd/relay-server/grpc_admission_e2e_integration_test.go`
- Siblings on the same shape, both now shipped:
  [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]] (**closed 2026-08-21**),
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] (**closed 2026-08-21**)
- The other end of the same event, filed 2026-08-21:
  [[idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted]]
- Adjacent, on what one sweep should say, and **to be folded into this slice**:
  [[bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick]]
- Why the per-worker map must be capped: [[bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded]]
- The wiring guard this section adds a row to, and its own open generalization:
  [[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]]
- The joint spec and the slices that settled the mechanism:
  `docs/superpowers/specs/2026-08-21-silent-drop-observability.md` (sections 3.1, 7.2, 10.4),
  `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice1.md` (R2, the import direction),
  `docs/retros/2026-08-21-silent-drop-observability-slice1.md`,
  `docs/retros/2026-08-21-silent-drop-observability-slice2.md`,
  `docs/retros/2026-08-21-silent-drop-observability-slice3.md`
- The slice that created this gap: `docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md`
  (section 11, "the freed slot is optimistic"),
  `docs/retros/2026-08-20-coordinator-stale-task-watchdog.md`
- The item the slice closed: [[bug-2026-08-14-no-coordinator-watchdog-on-a-stale-running-task]]

## Notes

The rule worth recording, and it is the same one the ingest-limiter item recorded from the other
direction: **a mechanism that quietly cleans up after a bad actor converts a loud failure into a
quiet one.** Before the watchdog, a wedged worker announced itself by holding a job in progress
forever - ugly, but impossible to miss. After it, the jobs complete (as failures) and the fleet looks
like it is working. That is a real improvement and a real regression in detectability, and the second
half only gets recorded if somebody writes it down at the time.

Filed at medium rather than low because the sink behaviour is unbounded in the number of jobs it can
fail, and because two sibling items are already waiting on the same endpoint decision. If the
endpoint work happens for any other reason, all three become small. **(2026-08-21: the endpoint work
has now happened. This item did not become small - it became the LAST of the four, because the
endpoint was never its hard part. Its hard part is the per-worker key and the writer ambiguity, both
untouched.)**

**2026-08-21 second addendum, from slice 2:** the mechanism built because controls silently stop
reporting shipped a counter that could silently stop counting - a correct new kind, counted on one
side and published under no JSON key on the other, with every package green. This section has the same
shape with the packages swapped and one extra hazard: `sweptOverflow` exists precisely to make a loss
visible, so a `sweptOverflow` that reaches no JSON key is the defect eating its own remedy.

**2026-08-21 third addendum, from slice 3:** this is now the only slice left and it remains the hardest
one. Nothing is blocked - the read surface, the section pattern, the typed-nil filter and the wiring
relation are all settled and none of them needs designing. What is untouched by all three shipped slices
is exactly what this item has always carried: the inverted import direction, the legitimately-disabled
control, the writer ambiguity, and the only unbounded-in-principle key in the cluster. Scope the
`swept_by_worker` exemption BEFORE writing code; it is a required artifact, not a risk.

## Resolution

Shipped as slice 4 of 4 of the silent-drop observability cluster (2026-08-24,
silent-drop-observability-slice4).

`internal/scheduler/watchdogCounters` records one entry per assignment the coordinator watchdog
ended, keyed by worker uuid, capped first-come at `api.WatchdogSweptWorkerMax` (256) with a
`swept_overflow` total for what the cap and any non-canonical key displaced. The reconciliation
`swept_total == sum(swept_by_worker) + swept_overflow` holds on every branch and is read in one
critical section. It is published as the fourth `watchdog` section of admin-only
`GET /v1/server/counters`, and aggregated once per sweep into a single log line.

Two design points differ from this item's proposal, both argued in the plan
(`docs/superpowers/plans/2026-08-24-silent-drop-observability-slice4.md`):

- The counter type is declared in `internal/api`, not `internal/scheduler` - `internal/scheduler`
  imports `internal/api`, so the reverse import is impossible. That inverts slice 2's arity rule and
  removes the mapper on both sides, which is what closes the counted-but-unpublished gap by
  construction rather than by assertion.
- The section ships counts-only. This item's `levels` half was refuted: `swept_workers_max` is a
  compile-time constant that would have to move when a `limits` classification is added, and
  `swept_workers_tracked` restates `len(swept_by_worker)`.

Verification found, and the slice fixed, a defect in its own deliverable: the aggregate line asserted
an unqualified "worst since process start" while never reading `swept_overflow`, so a real offender
displaced into overflow left the line naming an innocent worker as worst. The line now stays silent
below two sweeps and names the worst TRACKED worker with the unattributed count when attribution is
incomplete. The lesson generalises and is recorded in the retro: a lossy aggregate must disclose its
loss wherever it is READ, not only where it is published.

Closed alongside `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick`, folded in per the
joint spec's D12 - one aggregate line covers both the swept set and the failed-write set.
