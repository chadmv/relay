# Silent-drop observability slice 4: watchdog sweeps per worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Count what the coordinator stale-task watchdog sweeps, per worker, bounded at 256 keys with an explicit overflow counter, publish it as the fourth and final section of the admin-only `GET /v1/server/counters`, and replace the watchdog's per-row error line with one aggregate line per sweep.

**Architecture:** The snapshot type is declared in `internal/api` (because `internal/scheduler` already imports `internal/api`, so the reverse import is impossible) and is used **directly** as the response type - no hand-written mapper on either side of the boundary. `scheduler.Watchdog` stores an `api.WatchdogCounts` value under its own mutex and returns a copy with the map deep-copied. The `worker_id`-keyed map ships with an argued `counterPayloadExemption` whose `typeOK`/`jsonOK` predicates descend into the map and enforce the cap.

**Tech Stack:** Go 1.x, `sync.Mutex`, `reflect`, `go/ast`, testify, no new dependencies, no SQL, no migration, no proto, no generated file.

---

## Slice independence declaration

**There is NO frontend slice. `web/` is untouched by this plan - zero files, zero tests, zero build.**

This is one backend slice with one lane. The tasks below are **sequential** (Task N+1 depends on Task N compiling), so Phase 3 runs a single `relay-backend-engineer`. Do not attempt to parallelise Tasks 1-10 across agents: Tasks 1-4 create the types that Tasks 5-8 consume, and Task 8's wiring guards only go green once Tasks 1-7 are complete.

The one genuinely parallel thing in Phase 4 is the verification fan-out, which is the conductor's normal four-lens dispatch.

---

## Backlog effects (proposed - the conductor files and closes, not the engineer)

**Closes on merge:**

- `docs/backlog/idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced.md` - via `/backlog close`, never a hand-edited `status:`.
- `docs/backlog/bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick.md` - via `/backlog close`. Folded in per spec D12; both items wanted a once-per-sweep aggregate line and shipping them apart produces two lines and a third item.

**Explicitly DEFERRED, and it must stay open** (see "Scope decision" below):

- `docs/backlog/idea-2026-08-21-counters-payload-cannot-say-not-measured.md`. The ROADMAP "Now" entry names it as a fold-in. This plan declines it, pays the one forward cost that keeps it cheap, and proposes an amendment. **Do not close it. Do not silently drop it.**

**Amend, do not close:**

- `idea-2026-08-21-counters-payload-cannot-say-not-measured` gains: the watchdog is now the SECOND section that cannot say "not measured", but its form is materially weaker than `netlimit`'s (every number a disabled watchdog publishes is literally true), and slice 4 deliberately shipped **no configured constant in any `levels` half** so that a future `limits` classification is purely additive with zero field moves.
- `idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted` gains: `Dispatcher.failClaimedTask`'s `pgx.ErrNoRows` arm is now silent (Task 7), so if that slice decides to count fence rejections coordinator-side, `failClaimedTask` is a ready fifth site with no log-line question left to settle. It is **not** counted here.

---

## Scope decision, and why

Three candidate fold-ins existed. Two are in, one is out.

### IN: `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` (spec D12)

Confirmed at HEAD (`internal/scheduler/watchdog.go:193-203`): the per-row error branch logs one line per failing row per sweep, and a failed write leaves the row in the scan partition, so the next tick returns it again. Both this bug and the counter item want a once-per-sweep aggregate line. Shipping one aggregate line here is strictly cheaper than shipping two and filing a third item to reconcile them. Small: one accumulator, one gated `log.Print`, one comment fix.

### IN: `Dispatcher.failClaimedTask`'s undistinguished `pgx.ErrNoRows` (ROADMAP fold-in)

Confirmed at HEAD (`internal/scheduler/dispatch.go:438-441`). It is the same defect shape as the one above - a log line for an outcome that is correct - two hundred lines away in the same package, on the same goroutine, in a file this slice already touches. Shipping the watchdog's fix while leaving its twin is the inconsistency class this project keeps recording. **Scoped strictly to log hygiene: no counter.** A `dispatch` fence-rejection counter would be a fifth section for one number and would pre-empt `idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted`'s own design.

### OUT: `idea-2026-08-21-counters-payload-cannot-say-not-measured`

Deferred on two independent grounds, both checkable:

1. **Its sequencing argument has nearly lapsed.** The item's own Notes say the fix "as part of slice 2 costs almost nothing extra; doing it after slice 4 means reshaping a payload with four populated sections". Three sections are already shipped. Doing it in slice 4 means reshaping three and designing the fourth; doing it after means reshaping four. **The marginal saving is one section, not three.** That does not justify doubling the scope of the hardest slice in the cluster.
2. **The watchdog does not reproduce the defect's sharp form.** `netlimit`'s case is an *affirmative false statement*: `live_total: 0` on a server holding 800 connections. A disabled watchdog publishes `swept_total: 0`, `swept_overflow: 0`, `swept_by_worker: {}` - **every one of which is literally true.** What is missing is an answer to "is it on?", which is an unanswered question, not a lie. The item's own Summary rests on the falsehood ("an affirmative false statement, not an ambiguity"); that premise does not transfer here.

Doing it properly means a `limits` classification decided for **all four** sections at once (the item's own Done-When), which drags in two more `internal/worker` source methods and therefore two more instances of the exact cross-package arity class that bit slices 2 and 3. That is a second slice.

**The forward cost this slice pays so the deferral stays cheap:** this slice ships **no configured constant in any `levels` half**, which means it ships **no `levels` half at all**. See R1 below. A future `limits` expansion is then purely additive.

---

## Verification mandate: claims checked against the code at HEAD

Every claim in the item, the joint spec and the ROADMAP "Now" line was read against the tree. Cited line numbers are as of this plan's writing.

### Confirmed

| # | Claim | Evidence |
| --- | --- | --- |
| C1 | `internal/scheduler` imports `internal/api`, so the reverse import is impossible and the snapshot type must be declared in `internal/api` | `internal/scheduler/dispatch.go:11` (`"relay/internal/api"`), used at `:314` (`var ss api.SourceSpec`) |
| C2 | `TestCounterPayloadCarriesNoIdentifiers` calls `t.Fatalf` on any map leaf | `internal/api/server_counters_test.go:617-629` - the `switch ft.Kind()` has cases for `Struct` and the unsigned kinds only; `Map` falls to `default: t.Fatalf` |
| C3 | `counterPayloadAllowList` contains exactly ONE entry, `started_at`; `watchdog.counts.swept_by_worker` was removed during slice 1's review | `internal/api/server_counters_test.go:498-512`, with the removal argued at `:469-476` |
| C4 | Exemptions are shape-checked but NON-DESCENDING - both walks stop at an exempted path | `:597-604` (type walk `continue`s) and `:669-675` (JSON walk `return`s) |
| C5 | `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` counts served top-level keys against `1 + NumField(api.CounterSources)` and cannot be satisfied by editing a fixture | `cmd/relay-server/counters_wiring_test.go:220-244` |
| C6 | `countersAssignmentSources` fails closed: every `s.Counters.X = ...` in `buildHTTPServer` must be spelled `d.<field>` exactly | `cmd/relay-server/counters_wiring_test.go:732-775`, especially the `require.True(t, ok, ...)` at `:762` |
| C7 | `wiredDep` is `{field, mustReach, what}` - the `sections []string` column is gone | `cmd/relay-server/counters_wiring_test.go:717-721` |
| C8 | `TestServerCountersIsWiredByMain` requires every chain identifier to be assigned **exactly once** anywhere inside `main` | `cmd/relay-server/counters_wiring_test.go:567-592` |
| C9 | The watchdog's per-row error line repeats every tick for a persistent non-`ErrNoRows` failure | `internal/scheduler/watchdog.go:193-203`; the row stays in the scan partition because the write did not land |
| C10 | `Dispatcher.failClaimedTask` logs on **any** error including `pgx.ErrNoRows` | `internal/scheduler/dispatch.go:438-441` |
| C11 | It is the only one of the five Go-side fence-rejection sites that does not distinguish `pgx.ErrNoRows` | `worker/handler.go:975` (retry), `worker/handler.go:1020` (status), `worker/handler.go`'s task-log fence arm (counted in slice 3), `scheduler/watchdog.go:199` - all four use `errors.Is(err, pgx.ErrNoRows)`; `scheduler/dispatch.go:438` does not |
| C12 | The watchdog is legitimately disable-able | `internal/scheduler/watchdog.go:107-116` (zero margin/maxAssignment) and `:136-144` (both off skips the scan) |
| C13 | README:1288 says "there is no per-worker split and there will not be one" and its stated reason is recv-goroutine-specific | `README.md:1288` verbatim |
| C14 | `watchdogStore` is an unexported interface whose methods are all exported, so `cmd/relay-server` **can** implement it - the forwarding proof runs in the DEFAULT lane | `internal/scheduler/watchdog.go:52-57`; `ListOverdueAssignedTasks`, `UpdateTaskStatus`, `NotifyTaskCompleted`, `FailDependentTasks`, `RecomputeJobStatus` are all exported names |
| C15 | Nothing asserts the `pgx.ErrNoRows` arm is log-silent | `internal/scheduler/watchdog_test.go:151-165` asserts no cascade, no recompute, no notify - and captures no log at all |

### Refuted or corrected

**R1. The spec's own watchdog payload (`spec` section 9, lines 646-649) is wrong in two places, and this plan does not ship it.**

The spec proposes:

```json
"watchdog": {
  "counts": { "swept_total": 0, "swept_overflow": 0, "swept_by_worker": {} },
  "levels": { "swept_workers_tracked": 0, "swept_workers_max": 256 }
}
```

- `swept_workers_max` is a **compile-time constant**, not a level. A level is "current and goes down as well as up" (`internal/netlimit/listener.go:84-85`). Putting a configured cap in `levels` is the exact naming/classification error `idea-2026-08-21-counters-payload-cannot-say-not-measured` predicts, and it would have to be **moved** when that item ships its `limits` half - a breaking change to a published payload.
- `swept_workers_tracked` is exactly `len(swept_by_worker)`. A second field that summarises a container sitting next to it in the same object can only ever agree or be a bug.

**Shipped instead: counts only, no `levels` half**, matching `ingest_log_budget` and `task_log_fence`. The 256 cap is documented in README and in the code comment; the operator's runtime signal that it bound is `swept_overflow != 0`, which is the entire reason that counter exists.

**R2. The item's prescribed remedy for the arity gap is weaker than what is available.**

The item (section "THE CROSS-PACKAGE ARITY GAP") requires "a `NumField` assertion between the watchdog's own counter set and the published struct, in `internal/scheduler`". That is the slice-2 rule applied mechanically. But the rule's antecedent is *"any section whose payload struct **restates** fields owned by another package"* - and restating is a **choice**, not a given.

This plan removes the restatement on both sides:

- `internal/api` declares `WatchdogCounters` and uses it **directly** as the response type (`serverCountersResponse.Watchdog *WatchdogCounters`). `handleServerCounters` does one struct assignment, not a field-by-field copy. A new field on `WatchdogCounts` is published for free.
- `internal/scheduler` stores an `api.WatchdogCounts` **as the watchdog's own counter state** and `CounterSnapshot()` returns `out := c.c` - a whole-struct copy - with only the map deep-copied. A new scalar counter is copied for free.

So there is nothing to compare arities of. What is guarded instead is the **antecedent** (slice 3's `TestTaskLogFenceSourceReturnsAScalar` shape) plus the one remaining way a counter can go unpublished:

- `TestWatchdogSectionRestatesNothing` (in `internal/api`) - the response field's type is identical to the source method's return type. RED the moment anyone introduces a restatement, with a message naming the arity test that must then ship in the same commit.
- `TestWatchdogCountersLiveOnlyInThePublishedStruct` (in `internal/scheduler`) - `watchdogCounters` has exactly two fields, `mu` and an `api.WatchdogCounts`. A counter stored beside it would be counted and unpublishable, which is `swept_overflow`'s own failure mode.
- `TestWatchdog_EveryPublishedCounterIsDrivenByTheSweepFixture` (in `internal/scheduler`) - a real sweep populates **every** field of the returned `api.WatchdogCounts`. A published field nothing increments is RED. This is the mutation-provable exhaustiveness check the brief demands (M12).

**R3. The 256 cap constant cannot live in `internal/scheduler`.**

Neither item nor spec says where it goes; both write it as `watchdogSweptWorkerMax` beside the map. But the payload guard has to enforce the bound **inside `jsonOK`** (item, Acceptance), and `internal/api`'s test file is `package api` - importing `internal/scheduler` from it is the same import cycle C1 forbids. **The constant is therefore declared in `internal/api` as `WatchdogSweptWorkerMax` and consumed by `internal/scheduler`.** One constant, read by both the producer and the guard, so drift is not writable.

**R4. The item's Done-When "A disabled watchdog leaves the section ABSENT and does not panic" is not shippable as written, and the typed-nil shape it calls "the natural shape" is RED under a shipped guard.**

The item says `var wd *scheduler.Watchdog; if enabled { wd = ... }; CounterSources{Watchdog: wd}` is "the natural shape **and it panics**". Both halves need correcting:

- That shape cannot be written in `main` at all. `TestServerCountersIsWiredByMain` requires every identifier on the reachability chain to be **assigned exactly once** across main's entire subtree (C8), and requires the seed to derive from `mustReach` through an **unconditional direct statement** of main's body. A `var` plus a conditional assignment fails both. This is the same reason `main` wraps the gRPC listener unconditionally.
- So the watchdog is constructed **unconditionally** (it is a small struct; when both bounds are zero `SweepOnce` returns before the round trip, `watchdog.go:136-144`) and the section is **present and zero** on a disabled watchdog. Every number in it is true (see the Scope decision). Making it absent would either need the conditional shape above or would collapse "disabled" into "not wired", which `idea-2026-08-21-counters-payload-cannot-say-not-measured`'s Context forbids for every future section.
- The typed-nil filter still ships, with its test, because `buildHTTPServer`'s deps field is concretely typed and a future caller *can* pass a nil - it guards a **shape**, not a live case. Say so in the test's comment rather than implying the panic is reachable from today's `main`.

**R5. "`swept_by_worker` reddens the payload guard by CONSTRUCTION" - confirmed, and the JSON walk reddens for a second reason nobody wrote down.**

`TestCounterPayloadBytesCarryNoIdentifiers`'s `map[string]any` case begins `require.NotEmpty(t, tv, "counter payload object %q is empty...")` (`:678`). A **nil** `SweptByWorker` marshals to `null` and lands in the walk's `default: t.Fatalf` arm; an **empty non-nil** map would hit the NotEmpty. Both are avoided only because the exemption fires first - which means `jsonOK` must itself reject `nil`, and `CounterSnapshot` must **always** allocate the map. That is a real implementation constraint, not a nicety.

**R6. The concurrency choice is forced by an invariant the spec states but never derives from.**

Spec section 7.2 says "`sweptTotal` always counts every sweep, so the tracked map plus overflow always reconciles". That is a **cross-field invariant** - `SweptTotal == sum(SweptByWorker) + SweptOverflow` - which is exactly `netlimit`'s stated reason for a mutex and plain fields over atomics (`internal/netlimit/listener.go:127-141`). Slice 2 chose atomics because its ten numbers have no relation to each other; that reason does not transfer. And a map cannot be updated atomically at all.

**Decision: one `sync.Mutex`, plain `uint64` fields (never `atomic.Uint64`), snapshot taken in one critical section.** Plain fields are what make an unsynchronised read a data race `-race` can see; atomics would make it a legal-but-inconsistent read that no tool reports.

**R7. The forwarding proof runs in CI's DEFAULT lane, which neither slice 2 nor slice 3 achieved.**

The item hoped so ("check that rather than assuming it, and state the lane either way"). Confirmed via C14: `cmd/relay-server`'s test package can implement `watchdogStore` itself, drive a real `SweepOnce`, and read the resulting number back through the real admin-gated route on a server built by the real `buildHTTPServer`. **No Docker, no Postgres, no gRPC stream.** That is a stronger forwarding guarantee than either shipped section has.

**R8. `main` does not bind the watchdog to a local at all, and constructs it AFTER `buildHTTPServer`.**

`cmd/relay-server/main.go:268` is `go scheduler.NewWatchdog(q, registry, broker, watchdogMargin, maxAssignment).Run(ctx)`, and `buildHTTPServer` is called at `:215`. Neither item nor spec mentions this. **`main` must be restructured** (Task 8): the bounds parsing block and the construction move above the `buildHTTPServer` call; `go watchdog.Run(ctx)` stays where the watchdog block is today.

**R9. The ROADMAP's "fifth `UpdateTaskStatus` fence-rejection site" is right about the fifth-ness but not about the statement.**

There are only **three** production `UpdateTaskStatus` call sites (`scheduler/dispatch.go:430`, `scheduler/watchdog.go:181`, `worker/handler.go:1005`). The "five" is over **Go-side fence-rejection sites** across statements: the task-log fence (counted, slice 3), `IncrementTaskRetryCount`, `UpdateTaskStatus` in `handleTaskStatus`, the watchdog, and `failClaimedTask`. Under that enumeration the claim holds exactly (C11). Write the enumeration down in the comment rather than repeating the bare ordinal.

**R10. Silencing `failClaimedTask`'s `ErrNoRows` arm loses no churn signal - checked, not assumed.**

The worry would be that an assignment ended between claim and write, the task returns to `pending`, and the poison JSON gets re-claimed forever with nothing saying so. It cannot go silent: `failClaimedTask`'s **first** statement is an unconditional, unbudgeted `log.Printf("dispatch: failing task %s terminally: %s", ...)` (`dispatch.go:429`), emitted before the write on every attempt. The churn is visible through that line whatever the write does.

The function's doc comment currently says "we log and stop without retry or requeue" (`dispatch.go:426-427`), which becomes **wrong prose about correct code** the moment the arm goes silent. Task 7 corrects it in the same commit.

---

## File structure

**Create:**

- `internal/scheduler/watchdog_counters.go` - `watchdogCounters` (mutex + `api.WatchdogCounts`), `record`, `snapshot`, `worst`. Mirrors `internal/worker/ingest_log_counters.go`'s role for slice 2.
- `internal/scheduler/watchdog_counters_test.go` - counter semantics, the cap, reconciliation, the copy-out, the two exhaustiveness guards, the `-race` concurrency test, and the log-line tests for the aggregate sweep line.
- `internal/scheduler/dispatch_fence_test.go` - the `failClaimedTask` arm.

**Modify:**

- `internal/api/server_counters.go` - `WatchdogSweptWorkerMax`, `WatchdogCounts`, `WatchdogCounters`, `WatchdogSource`, `CounterSources.Watchdog`, `serverCountersResponse.Watchdog`, the handler branch, and the doc-comment paragraph that currently says `swept_by_worker` is de-authorized (`:38-54`).
- `internal/api/server_counters_test.go` - the exemption (`:498-512`), `counterPayloadLeaves` (`:517-535`), the `counterPayloadExemption` doc block (`:469-476`), the every-section fixture (`:645-656`), plus four new tests.
- `internal/scheduler/watchdog.go` - the `counters` field, `CounterSnapshot`, the record call, the aggregate sweep line replacing the per-row error line (`:193-213`), and the comment fix.
- `internal/scheduler/dispatch.go` - `failClaimedTask`'s error arm (`:438-441`) and its doc comment (`:423-427`).
- `internal/scheduler/watchdog_test.go` - one added assertion on `TestWatchdog_FenceRejectionIsASilentNoOp`.
- `cmd/relay-server/http_server.go` - `httpServerDeps.watchdog`, the `scheduler` import, the nil filter and its assignment.
- `cmd/relay-server/main.go` - move the watchdog bounds block above `buildHTTPServer`, bind `watchdog`, pass it.
- `cmd/relay-server/counters_wiring_test.go` - the `wiredDep` row, the every-source fixture, the typed-nil test, and the executed forwarding test.
- `README.md` - the `watchdog` payload block and reading bullets (`:1260-1291`), and the correction to `:1288`.

**Do NOT touch:** anything under `internal/store/` (`git diff -- internal/store/` must be 0 bytes), any `.sql`, any `.proto`, any generated file, anything under `web/`. **If a step seems to need `make generate`, that step is wrong.**

---

## Task 1: The api-side types and the fourth source field

**Files:**
- Modify: `internal/api/server_counters.go`

- [ ] **Step 1: Run the shipped completeness relation to see today's baseline**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_EverySourceFieldProducesAServedSection -v`
Expected: PASS (3 source fields, 4 served keys). Record this - the next step turns it RED.

- [ ] **Step 2: Add the types and the source field**

In `internal/api/server_counters.go`, after the `TaskLogFenceSource` block, add:

```go
// WatchdogSweptWorkerMax bounds how many distinct workers watchdog.counts.
// swept_by_worker will ever name. IT IS DECLARED HERE, IN internal/api, AND
// THAT IS NOT WHERE IT LOOKS LIKE IT BELONGS.
//
// The producer is internal/scheduler, so the constant "wants" to live beside the
// map. It cannot: the bound has to be ENFORCED inside this payload's
// counterPayloadAllowList predicates (an exemption is shape-checked but
// non-descending, so the cap is checked there or nowhere), and that test file is
// package api - importing internal/scheduler from it is the same cycle that
// forces WatchdogCounts to be declared here. One constant read by both the
// producer and the guard means the two cannot drift; two constants would.
//
// 256 is a policy number, not a measurement: it comfortably exceeds any fleet
// this project has seen, and the design is FIRST-COME rather than top-K because
// top-K needs a comparison on every increment to buy an ordering that
// swept_overflow already discloses the absence of.
const WatchdogSweptWorkerMax = 256

// WatchdogCounts is what the coordinator stale-task watchdog has ended since
// started_at. It is declared HERE rather than in internal/scheduler because
// internal/scheduler imports this package (scheduler/dispatch.go), so the
// reverse import is impossible - which INVERTS the shape ingest_log_budget uses,
// where the producing package owned the type.
//
// THAT INVERSION IS WHY THERE IS NO MAPPER ANYWHERE. This type is the response
// type: serverCountersResponse carries *WatchdogCounters directly and
// handleServerCounters assigns it whole, and scheduler.Watchdog stores a
// WatchdogCounts as its OWN counter state and returns a struct copy. A field
// added here is published by both sides for free. That matters because slice 2
// shipped a fully correct sixth log kind that was counted on one side and
// published under no JSON key on the other, with all three packages green; the
// remedy there was an arity assertion between two restated types, and the better
// remedy is not to restate. TestWatchdogSectionRestatesNothing guards the
// antecedent, and TestWatchdogCountersLiveOnlyInThePublishedStruct guards the
// only remaining way a counter can go unpublished.
//
// WHAT THESE NUMBERS DO NOT COVER, said here because it is the question an
// operator will get wrong: they count assignments THE COORDINATOR ended. An
// agent that honours its own timeout writes the same 'timed_out' status
// (worker/handler.go maps TASK_STATUS_TIMED_OUT straight through) and
// contributes NOTHING here, which is deliberate - the two mean opposite things
// about a worker's health and this counter SIDE-STEPS the ambiguity rather than
// resolving it.
//
// WHY NOT A DATABASE QUERY, which would be better on every other axis - no
// process state, survives restarts, correct across replicas: DECLINED, WITH THE
// PRICE, NOT IMPOSSIBLE. Telling a watchdog-written 'timed_out' from an
// agent-written one needs either a new terminal status (threaded through every
// status allow-list, including the two that must be read BACKWARDS -
// AppendTaskLog's first arm and ListOverdueAssignedTasks - plus
// TestTasksStatusVocabularyIsExactly) or a nullable writer column plus a
// migration on a write path that sits under the epoch fence. That is a larger
// and riskier slice than the observability it buys. IF SUCH A COLUMN IS EVER
// ADDED FOR ANOTHER REASON, REVISIT THIS: the query route is genuinely better.
//
// PER REPLICA. The watchdog is multi-replica-safe by first-write-wins, so a
// sweep of worker X may be counted on either replica; add the counts across
// replicas, and expect neither replica's swept_by_worker to be the whole story.
type WatchdogCounts struct {
	// SweptTotal counts every assignment this process's watchdog ended,
	// including the ones attributed to SweptOverflow. It is what makes the
	// section reconcile: SweptTotal == sum(SweptByWorker) + SweptOverflow,
	// always, which is also why these three fields are read in ONE critical
	// section rather than as three independent atomics.
	SweptTotal uint64 `json:"swept_total"`

	// SweptOverflow counts sweeps attributable to a worker the map is not
	// tracking, either because the map was already at WatchdogSweptWorkerMax
	// when that worker was first swept, or because the row's worker id was not
	// renderable as a uuid. NON-ZERO MEANS PER-WORKER ATTRIBUTION IS INCOMPLETE
	// and the worst tracked worker may not be the worst worker. This field
	// exists to make a loss visible; a version of it that were counted and
	// unpublished would be the defect eating its own remedy.
	SweptOverflow uint64 `json:"swept_overflow"`

	// SweptByWorker maps a server-assigned worker uuid to how many of its
	// assignments this process's watchdog ended. NEVER nil - it serialises as
	// {} on a watchdog that has swept nothing, because null is not an object
	// and the payload's walks would have nothing to descend into. Capped at
	// WatchdogSweptWorkerMax; see counterPayloadAllowList for the argument that
	// admits it into a payload where every other leaf is an integer.
	SweptByWorker map[string]uint64 `json:"swept_by_worker"`
}

// WatchdogCounters is the watchdog section. COUNTS ONLY, and the absence of a
// levels half is a decision: the only candidates were len(SweptByWorker), which
// is the map itself restated and can only ever agree or be a bug, and the 256
// cap, which is a CONFIGURED CONSTANT rather than a level. A constant in a
// levels half would have to MOVE when a limits classification is added
// (idea-2026-08-21-counters-payload-cannot-say-not-measured), and that is a
// breaking change to a published payload. It is documented in README instead,
// and swept_overflow is the runtime signal that it bound.
type WatchdogCounters struct {
	Counts WatchdogCounts `json:"counts"`
}

// WatchdogSource is whatever can report the coordinator watchdog's sweep
// counters - in production, *scheduler.Watchdog.
//
// ITS OWN SOURCE FIELD, like every other section. And note the direction: this
// interface is declared here and SATISFIED over there, because internal/api can
// never name internal/scheduler.
type WatchdogSource interface {
	CounterSnapshot() WatchdogCounters
}
```

Then add the field to `CounterSources`:

```go
type CounterSources struct {
	GRPCAdmission   GRPCAdmissionSource
	IngestLogBudget IngestLogBudgetSource
	TaskLogFence    TaskLogFenceSource
	Watchdog        WatchdogSource
}
```

- [ ] **Step 3: Run the completeness relation and watch it go RED**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_EverySourceFieldProducesAServedSection -v`
Expected: FAIL - `api.CounterSources has 4 source fields, so a build with every source wired must serve 5 top-level keys ... This one served 4: [grpc_admission ingest_log_budget started_at task_log_fence]`.

This is the RED the item predicted would come first, and it is an executed one.

- [ ] **Step 4: Commit**

```bash
git add internal/api/server_counters.go
git commit -m "feat(api): declare the watchdog counter types and the fourth source field"
```

---

## Task 2: Render the section, and take the payload guard's predicted RED

**Files:**
- Modify: `internal/api/server_counters.go`
- Modify: `internal/api/server_counters_test.go:517-535` (`counterPayloadLeaves`)

- [ ] **Step 1: Write the failing test**

Add to `internal/api/server_counters_test.go`:

```go
// fakeWatchdogSource returns a fixed snapshot. THREE DISTINCT VALUES and a
// REAL-SHAPED uuid key: equal values would hide a crossed field, and a key that
// is not a canonical uuid would not exercise the allow-list predicate that
// admits this map into a payload of integers.
type fakeWatchdogSource struct{ c WatchdogCounters }

func (f fakeWatchdogSource) CounterSnapshot() WatchdogCounters { return f.c }

func threeDistinctSweeps() WatchdogCounters {
	return WatchdogCounters{Counts: WatchdogCounts{
		SweptTotal:    37,
		SweptOverflow: 4,
		SweptByWorker: map[string]uint64{
			"00000000-0000-0000-0000-0000000000c8": 33,
		},
	}}
}

func TestServerCounters_ReportsTheWatchdogSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{Watchdog: fakeWatchdogSource{c: threeDistinctSweeps()}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Watchdog *struct {
			Counts map[string]any `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"watchdog"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Watchdog, "a wired section must be present")
	require.Nil(t, body.Watchdog.Levels,
		"COUNTS ONLY. The two candidate levels were len(swept_by_worker), which is the map restated, "+
			"and the 256 cap, which is a configured constant and belongs in a limits half nobody has "+
			"designed yet - putting it in levels now would mean moving it later.")

	// Key-set equality, not per-key assertions alone: a renamed key would decode
	// as a missing value and a per-key check would report zero.
	assert.ElementsMatch(t, []string{"swept_total", "swept_overflow", "swept_by_worker"},
		counterMapKeys(body.Watchdog.Counts))
	assert.Equal(t, float64(37), body.Watchdog.Counts["swept_total"])
	assert.Equal(t, float64(4), body.Watchdog.Counts["swept_overflow"])
	assert.Equal(t, map[string]any{"00000000-0000-0000-0000-0000000000c8": float64(33)},
		body.Watchdog.Counts["swept_by_worker"])
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestServerCounters_ReportsTheWatchdogSnapshot -v`
Expected: FAIL - `a wired section must be present` (the response type has no `Watchdog` field yet).

- [ ] **Step 3: Render the section**

In `internal/api/server_counters.go`, add the response field:

```go
type serverCountersResponse struct {
	StartedAt       time.Time               `json:"started_at"`
	GRPCAdmission   *grpcAdmissionSection   `json:"grpc_admission,omitempty"`
	IngestLogBudget *ingestLogBudgetSection `json:"ingest_log_budget,omitempty"`
	TaskLogFence    *taskLogFenceSection    `json:"task_log_fence,omitempty"`

	// *WatchdogCounters, not a *watchdogSection restating it. The source's own
	// type IS the response type, so there is no hand-written mapper and no arity
	// to drift. TestWatchdogSectionRestatesNothing keeps it that way.
	Watchdog *WatchdogCounters `json:"watchdog,omitempty"`
}
```

and the handler branch, at the end of `handleServerCounters` before `writeJSON`:

```go
	if src := s.Counters.Watchdog; src != nil {
		// ONE ASSIGNMENT, NOT A FIELD-BY-FIELD COPY, and that is the whole
		// point: the source's type is the response type, so a counter added on
		// the scheduler side reaches a JSON key with no edit here. Slice 2's
		// mapper needed TestIngestLogKindCountsPublishesEveryWorkerSideField
		// precisely because it was five hand-written assignments.
		snap := src.CounterSnapshot()
		resp.Watchdog = &snap
	}
```

- [ ] **Step 4: Run it, and watch a DIFFERENT test go RED**

Run: `go test ./internal/api/ -v`
Expected:
- `TestServerCounters_ReportsTheWatchdogSnapshot` PASS.
- `TestCounterPayloadCarriesNoIdentifiers` **FAIL** with `counter payload field "watchdog.counts.swept_by_worker" is a map. Every field in this payload must be an UNSIGNED INTEGER unless it is on the allow-list ...`
- `TestCounterPayloadBytesCarryNoIdentifiers` **FAIL** for the same path.

This is the "reddens by construction" the item promised. Do not weaken either guard. Task 3 supplies the argued exemption.

- [ ] **Step 5: Add the three leaves to the contract list**

In `counterPayloadLeaves`, append:

```go
	"watchdog.counts.swept_total",
	"watchdog.counts.swept_overflow",
	"watchdog.counts.swept_by_worker",
```

- [ ] **Step 6: Commit (deliberately RED - the exemption is the next task's argued artifact)**

```bash
git add internal/api/server_counters.go internal/api/server_counters_test.go
git commit -m "feat(api): render the watchdog section and record its leaves in the payload contract"
```

---

## Task 3: The argued exemption, with the descent and the cap inside the predicates

**Files:**
- Modify: `internal/api/server_counters_test.go:454-512` (`counterPayloadExemption` doc, `counterPayloadAllowList`)
- Modify: `internal/api/server_counters.go:38-54` (the doc paragraph that de-authorized this entry)

- [ ] **Step 1: Write the failing test for the predicates themselves**

The exemption is the artifact; a test that only observes it passing on the happy path authorizes nothing. Add to `internal/api/server_counters_test.go`:

```go
// TestWatchdogSweptByWorkerExemptionRejectsHostileShapes is the descent made
// executable.
//
// An exemption is shape-checked but NON-DESCENDING: both walks stop at an
// exempted path once the predicate passes. That is right for started_at, whose
// predicate examines the whole value. It is WRONG for a container - a jsonOK
// that merely accepted map[string]any would leave every key and every value
// uninspected, which is the total exemption the predicate mechanism replaced,
// re-entered through the predicate. Slice 1 proved that is not theoretical: a
// map[string]string at an exempted path, with a newline-injected RTL-override
// key and an IP-address value, passed both guards with zero failures.
//
// So this table is the exemption's real content. Each row is a value that must
// NOT be admitted, and the positive rows prove the predicate is not simply
// `false`.
func TestWatchdogSweptByWorkerExemptionRejectsHostileShapes(t *testing.T) {
	ex, ok := counterPayloadAllowList["watchdog.counts.swept_by_worker"]
	require.True(t, ok, "the watchdog map must be admitted by an ARGUED exemption, not by a walk that "+
		"stopped looking")

	tooMany := map[string]any{}
	for i := 0; i <= WatchdogSweptWorkerMax; i++ {
		tooMany[fmt.Sprintf("00000000-0000-0000-0000-%012x", i)] = json.Number("1")
	}
	require.Len(t, tooMany, WatchdogSweptWorkerMax+1)

	atCap := map[string]any{}
	for i := 0; i < WatchdogSweptWorkerMax; i++ {
		atCap[fmt.Sprintf("00000000-0000-0000-0000-%012x", i)] = json.Number("1")
	}

	cases := []struct {
		name string
		v    any
		want bool
	}{
		// The hostile rows come FIRST. A poisoned input placed last cannot
		// detect an early-exit mutation.
		{"a key that is not a uuid", map[string]any{
			"worker-one": json.Number("1")}, false},
		{"a uuid with an injected newline", map[string]any{
			"00000000-0000-0000-0000-0000000000c8\n10.0.0.7": json.Number("1")}, false},
		{"an uppercase uuid", map[string]any{
			"00000000-0000-0000-0000-0000000000C8": json.Number("1")}, false},
		{"a hostname-shaped key", map[string]any{
			"build-agent-07.corp.example": json.Number("1")}, false},
		{"a string value", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": "33"}, false},
		{"a negative value", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": json.Number("-1")}, false},
		{"a nested object as a value", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": map[string]any{"n": json.Number("1")}}, false},
		{"one over the cap", tooMany, false},
		{"nil (a nil Go map serialises as null)", nil, false},
		{"not an object at all", json.Number("3"), false},

		{"a canonical uuid key with a count", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": json.Number("33")}, true},
		{"empty", map[string]any{}, true},
		{"exactly at the cap", atCap, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ex.jsonOK(c.v))
		})
	}

	// The type half. A cap cannot be read off a type, so typeOK checks the only
	// thing a type carries: that the container is a map of string to unsigned.
	assert.False(t, ex.typeOK(reflect.TypeOf(map[string]string{})),
		"a map of strings at this path is where an address or a hostname gets in")
	assert.False(t, ex.typeOK(reflect.TypeOf(map[string]int64{})),
		"signed values are inadmissible everywhere in this payload")
	assert.False(t, ex.typeOK(reflect.TypeOf("")))
	assert.True(t, ex.typeOK(reflect.TypeOf(map[string]uint64{})))
}
```

Add `"regexp"` to the file's imports (`fmt`, `encoding/json`, `reflect`, `strconv` are already there).

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestWatchdogSweptByWorkerExemptionRejectsHostileShapes -v`
Expected: FAIL - `the watchdog map must be admitted by an ARGUED exemption` (the allow-list has no such key).

- [ ] **Step 3: Add the exemption**

Above `counterPayloadAllowList`, add:

```go
// canonicalUUIDRe is exactly what internal/scheduler's uuidStr emits: lowercase
// hex, 8-4-4-4-12, anchored. Anchored and hex-only is what makes the
// newline-injection and RTL-override class unrepresentable rather than merely
// unlikely.
var canonicalUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
```

and add to `counterPayloadAllowList`:

```go
	"watchdog.counts.swept_by_worker": {
		why: "a map from SERVER-ASSIGNED worker uuids to how many of that worker's assignments the " +
			"coordinator watchdog ended. THE SECURITY QUESTION SPLITS IN TWO AND ONLY ONE HALF IS " +
			"ANSWERED BY 'server-assigned'. (1) CALLER-SUPPLIED BYTES: no. The keys are uuidStr() of a " +
			"worker_id the coordinator's own scan returned, and this route is admin-authenticated, so a " +
			"uuid admissible HERE is still inadmissible in any log line reachable from the gRPC recv " +
			"path. The predicates below CHECK that shape rather than assert it, because an exemption " +
			"granted to a NAME is an exemption from every question. (2) CARDINALITY: server-assigned is " +
			"NOT server-limited. With RELAY_ALLOW_AUTO_ENROLL on, a reachable host creates one " +
			"persistent workers row per hostname it claims " +
			"(bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded, open), so a peer CAN drive " +
			"the number of distinct keys an admin-facing document serialises on every request. The " +
			"control is therefore the producer's hard cap, WatchdogSweptWorkerMax, which is ENFORCED " +
			"in jsonOK below rather than described beside it. DO NOT cite the workers row count as the " +
			"bound - that is the quantity that is unbounded.",
		typeOK: func(t reflect.Type) bool {
			return t.Kind() == reflect.Map &&
				t.Key().Kind() == reflect.String &&
				t.Elem().Kind() == reflect.Uint64
		},
		jsonOK: func(v any) bool {
			// DESCEND. Both walks stop here, so anything this function does not
			// look at is unexamined for the life of the payload.
			m, ok := v.(map[string]any)
			if !ok {
				// nil lands here: a nil Go map serialises as null, and the
				// producer must always allocate so the section reads {} on a
				// watchdog that has swept nothing.
				return false
			}
			if len(m) > WatchdogSweptWorkerMax {
				return false
			}
			for k, val := range m {
				if !canonicalUUIDRe.MatchString(k) {
					return false
				}
				n, ok := val.(json.Number)
				if !ok {
					return false
				}
				if _, err := strconv.ParseUint(n.String(), 10, 64); err != nil {
					return false
				}
			}
			return true
		},
	},
```

- [ ] **Step 4: Run the whole api package**

`TestCounterPayloadBytesCarryNoIdentifiers` wires every section (`:645-656`) and must now wire the watchdog too, with a **non-empty** map - otherwise the walk records the leaf without ever having a key to inspect. Add to that fixture:

```go
			Watchdog: fakeWatchdogSource{c: threeDistinctSweeps()},
```

Run: `go test ./internal/api/ -v`
Expected: PASS, including `TestCounterPayloadCarriesNoIdentifiers` and `TestCounterPayloadBytesCarryNoIdentifiers`.

- [ ] **Step 5: Correct the two prose blocks this task invalidated**

In `internal/api/server_counters.go`, the bullet at `:38-54` currently says `started_at` "is the ONE exemption today, and it is the whole allow-list" and that `swept_by_worker` "has been DELIBERATELY DE-AUTHORIZED". Both are now false. Replace with:

```go
//   - NO FIELD ANYWHERE CARRIES A CALLER-SUPPLIED BYTE. Two paths are exempt
//     today and each was argued in the commit that added it: started_at, as an
//     RFC 3339 instant; and watchdog.counts.swept_by_worker, as a map from
//     server-assigned worker uuids to counts, bounded by
//     WatchdogSweptWorkerMax. The second was written into slice 1's allow-list
//     against code nobody had written, DE-AUTHORIZED during slice 1's review
//     because pre-blessing it reduced its only forcing function to a one-line
//     edit, and re-added in slice 4 with predicates that DESCEND into the map
//     and enforce the cap - see counterPayloadExemption. Anything else
//     non-integer goes RED and forces the same argument.
```

In `internal/api/server_counters_test.go`, the `counterPayloadExemption` doc block at `:469-476` ("AND THE LIST NAMES ONLY PATHS THAT EXIST. It previously carried ...") must be updated to record that the entry is back, with the argument and with descending predicates, rather than reading as if it were still absent.

- [ ] **Step 6: Commit**

```bash
git add internal/api/server_counters.go internal/api/server_counters_test.go
git commit -m "feat(api): admit swept_by_worker with a descending, capped payload exemption"
```

---

## Task 4: Guard the antecedent - the section restates nothing

**Files:**
- Modify: `internal/api/server_counters_test.go`

- [ ] **Step 1: Write the test**

```go
// TestWatchdogSectionRestatesNothing is a forcing function on an ANTECEDENT, in
// the shape of TestTaskLogFenceSourceReturnsAScalar.
//
// The rule this package learned in the ingest slice is that a section whose
// payload struct RESTATES fields owned by another package needs a NumField
// assertion between the two types. The watchdog section restates nothing: the
// source's own return type IS the response type, so handleServerCounters
// assigns it whole and a field added on either side is published for free.
//
// That is only true while the two are the SAME type. Introducing a
// watchdogSection that copies WatchdogCounts field by field is a small,
// reasonable-looking edit that would silently move this section into the class
// that needs the arity check - which is exactly how slice 2 shipped a counted
// but unpublishable log kind with all three packages green.
func TestWatchdogSectionRestatesNothing(t *testing.T) {
	iface := reflect.TypeOf((*WatchdogSource)(nil)).Elem()
	require.Equal(t, 1, iface.NumMethod(),
		"WatchdogSource must stay a ONE-METHOD interface: a second method is a second thing that could "+
			"be restated, and the reasoning below covers only the one")
	m, ok := iface.MethodByName("CounterSnapshot")
	require.True(t, ok, "WatchdogSource must declare CounterSnapshot")
	require.Equal(t, 1, m.Type.NumOut())

	f, ok := reflect.TypeOf(serverCountersResponse{}).FieldByName("Watchdog")
	require.True(t, ok, "serverCountersResponse must carry a Watchdog field")

	require.Equal(t, m.Type.Out(0), f.Type.Elem(),
		"serverCountersResponse.Watchdog is %s and CounterSnapshot returns %s. They must be the SAME "+
			"type. The moment this section restates the source's fields instead of carrying its type, "+
			"a NumField assertion between the two must ship IN THIS COMMIT - see "+
			"TestIngestLogKindCountsPublishesEveryWorkerSideField, which exists because a fully correct "+
			"sixth kind was counted on one side and published under no JSON key on the other, with "+
			"every package green.", f.Type.Elem(), m.Type.Out(0))
}
```

- [ ] **Step 2: Prove it is load-bearing by mutation**

The test passes against the code as written, so mutate. Temporarily change the response field to a restating struct:

```go
type watchdogSection struct {
	Counts WatchdogCounts `json:"counts"`
}
// ... Watchdog *watchdogSection `json:"watchdog,omitempty"`
// ... resp.Watchdog = &watchdogSection{Counts: src.CounterSnapshot().Counts}
```

Run: `go test ./internal/api/ -run TestWatchdogSectionRestatesNothing -v`
Expected: FAIL - `serverCountersResponse.Watchdog is api.watchdogSection and CounterSnapshot returns api.WatchdogCounters. They must be the SAME type.`

Revert the mutation. Re-run.
Expected: PASS.

- [ ] **Step 3: Add the wired-but-zero test**

```go
// TestServerCounters_WiredButZeroWatchdogSectionIsStillPresent. A watchdog that
// has swept nothing is the HEALTHY case and the common one; it must not read as
// "this build has no watchdog".
//
// It walks the section's FULL DEPTH, and the depth is the point: counts carries
// an object (swept_by_worker), so the shipped scalar loop in
// TestServerCounters_WiredButZeroSectionIsStillPresent would fail here. Do not
// copy that loop.
func TestServerCounters_WiredButZeroWatchdogSectionIsStillPresent(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters: CounterSources{Watchdog: fakeWatchdogSource{c: WatchdogCounters{
			// An ALLOCATED empty map, which is what CounterSnapshot must always
			// return: a nil map serialises as null, and null is not an object.
			Counts: WatchdogCounts{SweptByWorker: map[string]uint64{}},
		}}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.ElementsMatch(t, []string{"started_at", "watchdog"}, counterKeys(top),
		"a WIRED source whose every counter is zero must still emit its section, and no OTHER section "+
			"may appear: each source is nil-able on its own")

	var section map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["watchdog"], &section))
	require.ElementsMatch(t, []string{"counts"}, counterKeys(section), "counts only; no levels half")

	var counts map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(section["counts"], &counts))
	require.ElementsMatch(t, []string{"swept_total", "swept_overflow", "swept_by_worker"}, counterKeys(counts))
	assert.Equal(t, "0", string(counts["swept_total"]))
	assert.Equal(t, "0", string(counts["swept_overflow"]))
	assert.Equal(t, "{}", string(counts["swept_by_worker"]),
		"an empty map must serialise as {} and never as null or be elided by omitempty. null is not an "+
			"object, so the payload's own JSON walk has nothing to descend into and the allow-list "+
			"predicate rejects it - which is the check that keeps the producer allocating.")
}
```

- [ ] **Step 4: Run both**

Run: `go test ./internal/api/ -run 'TestWatchdogSectionRestatesNothing|TestServerCounters_WiredButZeroWatchdogSectionIsStillPresent' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server_counters_test.go
git commit -m "test(api): guard the watchdog section's no-restatement antecedent and its zero shape"
```

---

## Task 5: The watchdog's own counters

**Files:**
- Create: `internal/scheduler/watchdog_counters.go`
- Create: `internal/scheduler/watchdog_counters_test.go`
- Modify: `internal/scheduler/watchdog.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/scheduler/watchdog_counters_test.go`:

```go
package scheduler

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nthWorkerUUID makes distinct worker uuids past the 256 that makeUUID's byte
// parameter can express - which is one more than the cap this file has to cross.
// b[13] is a namespace marker so these can never collide with makeUUID's b[0].
func nthWorkerUUID(n int) pgtype.UUID {
	var b [16]byte
	b[13] = 1
	b[14] = byte(n >> 8)
	b[15] = byte(n)
	return pgtype.UUID{Bytes: b, Valid: true}
}

// overdueRowForWorker is overdueRow with the worker id under the test's control,
// which overdueRow (worker 200, fixed) cannot express.
func overdueRowForWorker(id byte, worker pgtype.UUID, now time.Time) store.Task {
	r := overdueRow(id, 7, now.Add(-2*time.Hour), now.Add(-3*time.Hour))
	r.WorkerID = worker
	return r
}

func TestWatchdogCounters_RecordAttributesPerWorkerAndReconciles(t *testing.T) {
	var c watchdogCounters
	a, b := uuidStr(nthWorkerUUID(1)), uuidStr(nthWorkerUUID(2))
	c.record(a)
	c.record(a)
	c.record(b)

	got := c.snapshot().Counts
	assert.Equal(t, uint64(3), got.SweptTotal)
	assert.Equal(t, uint64(0), got.SweptOverflow)
	assert.Equal(t, map[string]uint64{a: 2, b: 1}, got.SweptByWorker)
}

// TestWatchdogCounters_TheCapIsHardAndOverflowIsCounted. The cap is the whole
// security argument for admitting a map into a payload of integers: worker ids
// are server-assigned, but with RELAY_ALLOW_AUTO_ENROLL on their COUNT is
// peer-drivable, so nothing but this bound stops an admin-facing document
// growing without limit.
func TestWatchdogCounters_TheCapIsHardAndOverflowIsCounted(t *testing.T) {
	var c watchdogCounters
	for i := 0; i < api.WatchdogSweptWorkerMax; i++ {
		c.record(uuidStr(nthWorkerUUID(i)))
	}
	require.Len(t, c.snapshot().Counts.SweptByWorker, api.WatchdogSweptWorkerMax)

	// A NEW key at capacity is refused; an ALREADY-TRACKED key keeps counting.
	c.record(uuidStr(nthWorkerUUID(9999)))
	c.record(uuidStr(nthWorkerUUID(0)))

	got := c.snapshot().Counts
	assert.Len(t, got.SweptByWorker, api.WatchdogSweptWorkerMax,
		"the map must never exceed the cap the payload's allow-list predicate enforces")
	assert.Equal(t, uint64(1), got.SweptOverflow,
		"a sweep attributable to an untracked worker must be COUNTED, not dropped: swept_overflow "+
			"being non-zero is the operator's only signal that per-worker attribution is incomplete")
	assert.Equal(t, uint64(2), got.SweptByWorker[uuidStr(nthWorkerUUID(0))],
		"already-tracked keys keep counting at capacity")

	var sum uint64
	for _, v := range got.SweptByWorker {
		sum += v
	}
	assert.Equal(t, got.SweptTotal, sum+got.SweptOverflow,
		"THE RECONCILIATION INVARIANT: swept_total == sum(swept_by_worker) + swept_overflow. It is what "+
			"makes the lossy design honest, and it is why all three fields are read in ONE critical "+
			"section rather than as independent atomics.")
}

// TestWatchdogCounters_AnUnkeyableWorkerGoesToOverflow. Nothing that is not a
// canonical uuid may ever enter this map, because the payload's allow-list
// predicate rejects the whole map on one bad key - which would take the entire
// endpoint's guard RED rather than losing one number.
func TestWatchdogCounters_AnUnkeyableWorkerGoesToOverflow(t *testing.T) {
	var c watchdogCounters
	c.record("") // uuidStr of an invalid pgtype.UUID

	got := c.snapshot().Counts
	assert.Empty(t, got.SweptByWorker, "a key that is not a uuid must never be inserted")
	assert.Equal(t, uint64(1), got.SweptOverflow)
	assert.Equal(t, uint64(1), got.SweptTotal, "the sweep still happened and must still be counted")
}

// TestWatchdogCounters_SnapshotIsACopy. CLAUDE.md: no interior pointers across
// locks. Returning the live map hands a caller a reference the scheduler
// goroutine keeps writing to, which is a data race in the HTTP handler and a
// mutation channel into the watchdog's own state.
func TestWatchdogCounters_SnapshotIsACopy(t *testing.T) {
	var c watchdogCounters
	w := uuidStr(nthWorkerUUID(1))
	c.record(w)

	first := c.snapshot().Counts
	first.SweptByWorker[w] = 4242
	first.SweptByWorker["injected"] = 1

	second := c.snapshot().Counts
	assert.Equal(t, map[string]uint64{w: 1}, second.SweptByWorker,
		"mutating a returned snapshot must not reach the watchdog's own map")
}

// TestWatchdogCounters_SnapshotNeverReturnsANilMap. A nil Go map serialises as
// null, and null is not an object: the payload's JSON walk has nothing to
// descend into and the allow-list predicate rejects it. The empty case is the
// COMMON case - a healthy fleet - so this is the shape that ships most often.
func TestWatchdogCounters_SnapshotNeverReturnsANilMap(t *testing.T) {
	var c watchdogCounters
	got := c.snapshot().Counts
	require.NotNil(t, got.SweptByWorker, "an untouched counter set must snapshot to an ALLOCATED empty map")
	assert.Empty(t, got.SweptByWorker)
}

// TestWatchdogCounters_ConcurrentRecordsAreExactAndTheSnapshotIsConsistent.
//
// TWO PROPERTIES, and the second is the one a mutation to atomics would break.
// Exactness catches a lost update. The reconciliation invariant catches a
// snapshot assembled from more than one critical section: with the scalars as
// atomics and the map under its own lock, a reader between the two writes
// observes a total that does not reconcile.
//
// Run with -race. On Windows that needs CC=/c/msys64/mingw64/bin/gcc.exe.
func TestWatchdogCounters_ConcurrentRecordsAreExactAndTheSnapshotIsConsistent(t *testing.T) {
	var c watchdogCounters
	const writers, perWriter = 8, 500

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			got := c.snapshot().Counts
			var sum uint64
			for _, v := range got.SweptByWorker {
				sum += v
			}
			if sum+got.SweptOverflow != got.SweptTotal {
				t.Errorf("snapshot does not reconcile: total=%d sum=%d overflow=%d",
					got.SweptTotal, sum, got.SweptOverflow)
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := uuidStr(nthWorkerUUID(i))
			for j := 0; j < perWriter; j++ {
				c.record(w)
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	<-readerDone

	got := c.snapshot().Counts
	assert.Equal(t, uint64(writers*perWriter), got.SweptTotal)
	for i := 0; i < writers; i++ {
		assert.Equal(t, uint64(perWriter), got.SweptByWorker[uuidStr(nthWorkerUUID(i))])
	}
}

// TestWatchdogCountersLiveOnlyInThePublishedStruct.
//
// snapshot() copies its api.WatchdogCounts WHOLE, so every field of that struct
// reaches a JSON key for free. That property is what removes the hand-written
// mapper this cluster twice shipped a defect through - and it holds only while
// no counter lives OUTSIDE that struct.
//
// A counter stored beside it would be incremented on the sweep path and
// published under no JSON key: precisely slice 2's sixth-log-kind defect with
// the packages swapped, and the field most likely to be added that way is
// swept_overflow, whose entire purpose is to make a loss visible.
//
// IF YOU NEED STATE HERE THAT IS NOT A COUNTER, this guard can no longer answer
// the question it asks and needs REPLACING, not renumbering. Bumping the 2 is
// not compliance.
func TestWatchdogCountersLiveOnlyInThePublishedStruct(t *testing.T) {
	ct := reflect.TypeOf(watchdogCounters{})
	require.Equal(t, 2, ct.NumField(),
		"watchdogCounters has %d fields. Every number this watchdog keeps must be a field of "+
			"api.WatchdogCounts, which snapshot() copies whole; a counter stored beside it is counted "+
			"and unpublishable.", ct.NumField())
	assert.Equal(t, "mu", ct.Field(0).Name)
	assert.Equal(t, reflect.TypeOf(api.WatchdogCounts{}), ct.Field(1).Type,
		"the second field must BE the published struct, not a lookalike")
}

// TestWatchdog_EveryPublishedCounterIsDrivenByTheSweepFixture is the
// exhaustiveness check, and it is executed rather than tabulated.
//
// It runs a REAL sweep whose fixture is built to move every counter at once -
// repeat sweeps of one worker, and more distinct workers than the cap - then
// walks the RETURNED api.WatchdogCounts with reflection requiring every field to
// have been moved.
//
// WHAT IT CATCHES: a field added to api.WatchdogCounts that nothing increments.
// Because the api side needs no mapper edit, such a field compiles, vets and
// publishes as a permanent zero with every package green - the class this whole
// cluster exists to close, arriving inside the mechanism built to close it.
//
// WHAT IT ASKS OF THE NEXT AUTHOR: a new counter needs a fixture that drives it.
// If the scenario below cannot reach it, the new counter has no test at all and
// THAT is the finding - not a reason to exempt the field.
func TestWatchdog_EveryPublishedCounterIsDrivenByTheSweepFixture(t *testing.T) {
	now := time.Now()
	var rows []store.Task
	// Two sweeps of one worker, so a per-worker count exceeds 1.
	rows = append(rows, overdueRowForWorker(1, nthWorkerUUID(0), now))
	rows = append(rows, overdueRowForWorker(2, nthWorkerUUID(0), now))
	// Enough distinct workers to fill the cap and force exactly one overflow.
	for i := 1; i <= api.WatchdogSweptWorkerMax; i++ {
		rows = append(rows, overdueRowForWorker(byte(i), nthWorkerUUID(i), now))
	}
	require.Less(t, len(rows), WatchdogMaxRowsPerSweep,
		"the fixture must fit in ONE sweep, or the cap is never reached in this test")

	q := &fakeWatchdogStore{overdue: rows}
	w := newTestWatchdog(t, q, now)
	require.NoError(t, w.SweepOnce(context.Background()))

	got := w.CounterSnapshot().Counts
	v := reflect.ValueOf(got)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if f.Kind() == reflect.Map {
			require.NotEmpty(t, f.Interface(), "%s was not populated by this sweep fixture", name)
			continue
		}
		require.NotZero(t, f.Interface(),
			"api.WatchdogCounts.%s is still zero after a sweep built to move every counter. Either "+
				"this fixture must be extended to drive it, or that field is published and counted "+
				"by nothing - a permanent zero on an operator-facing document, with every package "+
				"green. That is slice 2's sixth-log-kind defect with the packages swapped.", name)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/scheduler/ -run TestWatchdogCounters -v`
Expected: FAIL to compile - `undefined: watchdogCounters`.

- [ ] **Step 3: Write the implementation**

Create `internal/scheduler/watchdog_counters.go`:

```go
package scheduler

import (
	"sync"

	"relay/internal/api"
)

// watchdogCounters is the process-lifetime home for what this coordinator's
// watchdog has ended. Cumulative since process start: no rolling window and no
// clear, because a sawtooth reset would make "37 sweeps" mean different things
// at different times of day. The window is process uptime and the payload's
// started_at states it.
//
// A MUTEX AND PLAIN uint64s, AND SLICE 2 DELIBERATELY DID THE OPPOSITE. The
// ingest log budget uses atomics because its ten numbers have no relation to
// each other, so a snapshot that reads them microseconds apart is merely
// unsynchronised in a way nothing can observe. NEITHER REASON TRANSFERS.
//
//   - There is a CROSS-FIELD INVARIANT here: SweptTotal == sum(SweptByWorker) +
//     SweptOverflow. Only one critical section can hold that across a snapshot,
//     which is netlimit's argument, not slice 2's.
//   - A map cannot be updated atomically at all.
//   - Plain fields rather than atomic.Uint64 are what make an unsynchronised
//     access a DATA RACE that -race can see, instead of a legal-but-inconsistent
//     read no tool reports. The compiler does not help either way; -race plus
//     TestWatchdogCounters_ConcurrentRecordsAreExactAndTheSnapshotIsConsistent
//     is the whole enforcement.
//
// The cost is nothing: the writer is the scheduler goroutine, once per swept
// row, on a path that has just made a Postgres round trip. The reader is an
// admin HTTP request.
//
// THE PUBLISHED STRUCT IS THE STORAGE. c is an api.WatchdogCounts, not three
// fields copied into one, so snapshot() is a struct assignment and a counter
// added there is published for free. Slice 2 shipped a hand-written five-field
// mapper and a fully correct sixth kind went counted-but-unpublished with all
// three packages green; the remedy there was an arity assertion, and the better
// remedy is not to restate.
// TestWatchdogCountersLiveOnlyInThePublishedStruct is what keeps a counter from
// being added beside c instead of inside it.
type watchdogCounters struct {
	mu sync.Mutex
	c  api.WatchdogCounts // guarded by mu; the map is never handed out
}

// record attributes one ended assignment to one worker.
//
// FIRST-COME, NOT TOP-K, at capacity. Top-K needs a comparison on every
// increment to buy an ordering that swept_overflow already discloses the
// absence of, and the signal this counter exists for ("worker X has had 37")
// survives first-come in every realistic fleet. The loss is disclosed rather
// than hidden.
//
// A worker id that is not a canonical uuid goes to overflow rather than into the
// map. That is not defensive noise: the payload's allow-list predicate rejects
// the WHOLE map on one non-uuid key, so a single bad key would take the
// endpoint's guard RED rather than lose one number. ListOverdueAssignedTasks
// requires worker_id IS NOT NULL, so this branch is unreachable today - and it
// is what lets the payload guard be written as a shape check rather than as a
// promise.
func (w *watchdogCounters) record(workerID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// SweptTotal counts FIRST and unconditionally, so the reconciliation holds
	// no matter which branch below runs.
	w.c.SweptTotal++

	if !canonicalWorkerKey(workerID) {
		w.c.SweptOverflow++
		return
	}
	if w.c.SweptByWorker == nil {
		w.c.SweptByWorker = make(map[string]uint64)
	}
	if _, tracked := w.c.SweptByWorker[workerID]; !tracked &&
		len(w.c.SweptByWorker) >= api.WatchdogSweptWorkerMax {
		w.c.SweptOverflow++
		return
	}
	w.c.SweptByWorker[workerID]++
}

// snapshot returns a copy. A STRUCT ASSIGNMENT plus a map clone, never a
// field-by-field copy - see the type's comment.
//
// The map is ALWAYS allocated, including when nothing has been swept: a nil Go
// map serialises as null, null is not an object, and the payload's JSON walk and
// allow-list predicate both reject it. The empty case is the healthy case and
// therefore the common one.
func (w *watchdogCounters) snapshot() api.WatchdogCounters {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := w.c // every scalar field, present and future
	out.SweptByWorker = make(map[string]uint64, len(w.c.SweptByWorker))
	for k, v := range w.c.SweptByWorker {
		out.SweptByWorker[k] = v
	}
	return api.WatchdogCounters{Counts: out}
}

// worst reports the most-swept TRACKED worker since process start, for the
// aggregate sweep line. It returns ("", 0) when nothing has been swept.
//
// TRACKED is load-bearing: when SweptOverflow is non-zero the true worst may be
// a worker the map never admitted, and the log line says so rather than
// asserting a maximum it cannot establish.
func (w *watchdogCounters) worst() (string, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var id string
	var n uint64
	for k, v := range w.c.SweptByWorker {
		// Ties broken by the lexically smaller id, so the line is deterministic
		// across map iteration order rather than flapping between equals.
		if v > n || (v == n && id != "" && k < id) {
			id, n = k, v
		}
	}
	return id, n
}

// canonicalWorkerKey reports whether s is the lowercase 8-4-4-4-12 form uuidStr
// emits. Anchored and hex-only, so nothing that is not a server-rendered uuid
// can become a key in a document an operator reads.
func canonicalWorkerKey(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
				return false
			}
		}
	}
	return true
}
```

In `internal/scheduler/watchdog.go`, add `"relay/internal/api"` to the imports, add the field to `Watchdog`:

```go
	// counters is a VALUE field, so the zero value works and a bare &Watchdog{}
	// in a test has a working counter set with no nil case to get wrong.
	counters watchdogCounters
```

and add the accessor:

```go
// CounterSnapshot satisfies api.WatchdogSource. The interface is declared in
// internal/api rather than here because internal/scheduler imports internal/api
// (dispatch.go) and the reverse import is impossible - so for this section, and
// unlike every other, the CONSUMER owns the type.
func (w *Watchdog) CounterSnapshot() api.WatchdogCounters { return w.counters.snapshot() }
```

- [ ] **Step 4: Run the counter-level tests**

Run: `go test ./internal/scheduler/ -run TestWatchdogCounters -v`
Expected: PASS (all six `TestWatchdogCounters_*` plus `TestWatchdogCountersLiveOnlyInThePublishedStruct`).

Run: `go test ./internal/scheduler/ -run TestWatchdog_EveryPublishedCounter -v`
Expected: **FAIL** - `SweptTotal is still zero`. The sweep does not call `record` yet; that is the next step.

- [ ] **Step 5: Record on every matched sweep**

In `internal/scheduler/watchdog.go`'s `SweepOnce`, immediately after the successful `UpdateTaskStatus` (i.e. after the `if err != nil { ... continue }` block, before the existing per-task log line), add:

```go
		// COUNTED ONLY WHEN THE WRITE MATCHED. A fence-rejected write ended
		// nothing, and counting it would inflate the one number an operator uses
		// to decide whether to disable a machine.
		w.counters.record(uuidStr(t.WorkerID))
```

- [ ] **Step 6: Run the package**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS, all tests.

- [ ] **Step 7: Prove the exhaustiveness guard by mutation (M12 - the headline)**

Add a field to `internal/api/server_counters.go`'s `WatchdogCounts`:

```go
	SweptTruncated uint64 `json:"swept_truncated"`
```

Run: `go test ./internal/api/ ./internal/scheduler/ -v`
Expected:
- `internal/api`: FAIL on `TestCounterPayloadCarriesNoIdentifiers` (the leaf set changed) - and note that **no mapper anywhere needed editing to make the field appear in the payload**, which is the property being proved.
- `internal/scheduler`: FAIL on `TestWatchdog_EveryPublishedCounterIsDrivenByTheSweepFixture` - `api.WatchdogCounts.SweptTruncated is still zero after a sweep built to move every counter.`

Record both. Revert the field. Re-run both: PASS.

- [ ] **Step 8: Prove the storage guard by mutation (M13)**

Add a field to `watchdogCounters`:

```go
	sweptSuspicious uint64
```

Run: `go test ./internal/scheduler/ -run TestWatchdogCountersLiveOnlyInThePublishedStruct -v`
Expected: FAIL - `watchdogCounters has 3 fields.`

Revert. Re-run: PASS.

- [ ] **Step 9: Run with the race detector**

Run (Git Bash): `CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/scheduler/ -run TestWatchdogCounters_Concurrent -count=5 -v`
Expected: PASS, no race reports.

Then mutate: move `w.c.SweptTotal++` outside the lock and re-run.
Expected: FAIL with a `DATA RACE` report. Revert.

- [ ] **Step 10: Commit**

```bash
git add internal/scheduler/watchdog_counters.go internal/scheduler/watchdog_counters_test.go internal/scheduler/watchdog.go
git commit -m "feat(scheduler): count watchdog sweeps per worker, capped at 256 with an overflow total"
```

---

## Task 6: One aggregate line per sweep, replacing the per-row error line

**Files:**
- Modify: `internal/scheduler/watchdog.go:158-228`
- Modify: `internal/scheduler/watchdog_test.go:151-165`
- Modify: `internal/scheduler/watchdog_counters_test.go`

This closes `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/scheduler/watchdog_counters_test.go` (adding `bytes`, `errors`, `log`, `strings` and `github.com/jackc/pgx/v5` to its imports):

```go
// captureLog redirects the standard logger for the duration of a test.
func captureLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf.String
}

// TestWatchdog_APersistentWriteFailureIsBoundedToOneLinePerSweep.
//
// The failure class is a PARTIAL DEGRADATION: the scan succeeds and the write
// does not - a statement timeout, a saturated pool, a lock wait. The row then
// stays in the scan partition and the very next tick returns it, so the old
// per-row line reappeared every 60 seconds per overdue row, indefinitely, in
// exactly the conditions where log volume is least welcome.
//
// THE FIRST OCCURRENCE MUST STILL BE PROMPT: a fix that suppresses the condition
// entirely, or reports it only after a delay, is worse than the bug. So the
// assertion is one line per SWEEP, present from the first sweep, and not one
// line per row.
func TestWatchdog_APersistentWriteFailureIsBoundedToOneLinePerSweep(t *testing.T) {
	logged := captureLog(t)
	now := time.Now()

	rows := make([]store.Task, 0, 5)
	errs := map[pgtype.UUID]error{}
	for i := byte(1); i <= 5; i++ {
		r := overdueRowForWorker(i, nthWorkerUUID(int(i)), now)
		rows = append(rows, r)
		errs[r.ID] = errors.New("connection reset by peer")
	}
	q := &fakeWatchdogStore{overdue: rows, updateErr: errs}
	w := newTestWatchdog(t, q, now)

	for i := 0; i < 3; i++ {
		require.NoError(t, w.SweepOnce(context.Background()))
	}

	lines := strings.Split(strings.TrimSpace(logged()), "\n")
	require.Len(t, lines, 3,
		"three sweeps over five permanently failing rows must produce THREE lines, not fifteen. Got:\n%s",
		logged())
	for _, l := range lines {
		assert.Contains(t, l, "5 write(s) FAILED",
			"the aggregate must say how many failed, because '5 of 5 failed' is a diagnosis and five "+
				"separate lines are not")
		assert.Contains(t, l, "connection reset by peer",
			"the first error's text must survive aggregation, or the line reports a count with no cause")
	}
}

// TestWatchdog_AFenceRejectionEmitsNoLogLineAtAll. pgx.ErrNoRows means somebody
// else got there first - the agent finished, a cancel landed, a grace expiry
// requeued, or a sibling replica swept it. That is the CORRECT outcome, and the
// whole-captured-log-is-empty style is deliberate so that ANY future wording on
// that arm reddens.
//
// It is also the test that makes the summary line's `swept > 0 || failed > 0`
// gate load-bearing: an ungated summary would print "0 task(s) swept" every 60
// seconds forever on a healthy fleet, which is the bug this task closes wearing
// the fix's clothes.
func TestWatchdog_AFenceRejectionEmitsNoLogLineAtAll(t *testing.T) {
	logged := captureLog(t)
	now := time.Now()
	row := overdueRowForWorker(1, nthWorkerUUID(1), now)
	q := &fakeWatchdogStore{
		overdue:   []store.Task{row},
		updateErr: map[pgtype.UUID]error{row.ID: pgx.ErrNoRows},
	}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))

	assert.Empty(t, logged(),
		"a fence rejection is the correct outcome, not a failure. The whole captured log must be empty, "+
			"so that any future line on this arm - including a well-meaning summary that counts zero - "+
			"turns this RED.")
	assert.Zero(t, w.CounterSnapshot().Counts.SweptTotal,
		"and a rejected write must not be counted as a sweep")
}

// TestWatchdog_TheAggregateLineNamesTheWorstWorkerSinceStart. This is the item's
// headline signal in an existing log pipeline: "worker X has had 37" is a
// disable decision with one number behind it, and an operator reading raw logs
// should not have to notice a repeating uuid to get it.
func TestWatchdog_TheAggregateLineNamesTheWorstWorkerSinceStart(t *testing.T) {
	logged := captureLog(t)
	now := time.Now()
	hot := nthWorkerUUID(1)
	q := &fakeWatchdogStore{overdue: []store.Task{
		overdueRowForWorker(1, hot, now),
		overdueRowForWorker(2, hot, now),
		overdueRowForWorker(3, nthWorkerUUID(2), now),
	}}
	w := newTestWatchdog(t, q, now)

	require.NoError(t, w.SweepOnce(context.Background()))
	require.NoError(t, w.SweepOnce(context.Background()))

	out := logged()
	assert.Contains(t, out, "worst since process start: worker "+uuidStr(hot)+" with 4",
		"the aggregate must carry the CUMULATIVE worst, not this sweep's worst: the pattern is the "+
			"actionable part and a single sweep does not show it. Got:\n%s", out)
	assert.Contains(t, out, "3 task(s) swept across 2 worker(s)")
}
```

And add one assertion to the shipped `TestWatchdog_FenceRejectionIsASilentNoOp` in `watchdog_test.go`, which today proves no cascade, no recompute and no notify and says nothing about either the log or the counter:

```go
	// The counter half of "silent no-op". The whole-log assertion lives in
	// TestWatchdog_AFenceRejectionEmitsNoLogLineAtAll; this keeps the claim in
	// the test whose name makes it.
	assert.Zero(t, w.CounterSnapshot().Counts.SweptTotal, "a rejected write is not a sweep")
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/scheduler/ -run 'TestWatchdog_APersistentWriteFailure|TestWatchdog_AFenceRejectionEmitsNoLogLine|TestWatchdog_TheAggregateLine' -v`
Expected:
- `...APersistentWriteFailure...` FAIL - 15 lines, not 3.
- `...AFenceRejectionEmitsNoLogLine...` PASS today (the `ErrNoRows` arm is already silent). Keep it: it is the guard that stops the fix introducing a heartbeat line.
- `...TheAggregateLine...` FAIL - no such line.

- [ ] **Step 3: Write the implementation**

In `internal/scheduler/watchdog.go`, inside `SweepOnce`, declare the accumulators before the row loop:

```go
	// ONE AGGREGATE LINE PER SWEEP, covering BOTH the swept set and the
	// failed-write set. Two items wanted a once-per-sweep summary
	// (idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced
	// and bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick) and
	// shipping them separately would have produced two lines and a third item to
	// reconcile them.
	//
	// SAY WHAT THIS SWEEP CAN STILL EMIT, so "one line" is not read as more than
	// it is: the row-cap line above (once per sweep, only when the cap binds),
	// one line per SWEPT task (see its own comment), and this summary. Three
	// kinds, each with its own bound.
	var swept, failedWrites int
	sweptWorkers := make(map[string]struct{}) // bounded by WatchdogMaxRowsPerSweep
	var firstFailID pgtype.UUID
	var firstFailErr error
```

Replace the error branch (`watchdog.go:193-203`) with:

```go
		if err != nil {
			// pgx.ErrNoRows means somebody else got there first - the agent
			// finished, a cancel landed, a grace expiry requeued, or a sibling
			// replica swept it. That is the CORRECT outcome, not a failure, so
			// it is neither logged nor counted. Any other error is real, and is
			// AGGREGATED rather than logged here: the write did not land, so the
			// row stays in the scan partition and the very next tick returns it,
			// which made the old per-row line repeat every 60 seconds per row
			// for as long as the failure persisted - in exactly the conditions
			// (a database already under stress) where log volume is least
			// welcome. Either way, continue to the next row: one bad row must
			// never end the sweep.
			if !errors.Is(err, pgx.ErrNoRows) {
				failedWrites++
				if firstFailErr == nil {
					// FIRST, not last. The first error is the one closest to the
					// cause; "last" is whichever row the loop happened to end
					// on.
					firstFailID, firstFailErr = t.ID, err
				}
			}
			continue
		}
```

Amend the comment above the per-task success line so its argument names the branch it covers:

```go
		// One line per SWEPT task, unbudgeted, and that is safe: the count per
		// sweep is bounded by WatchdogMaxRowsPerSweep, each task can be swept at
		// most once (it is terminal afterwards), and nothing in the line is
		// caller-supplied. A watchdog that kills somebody's work without saying
		// why it decided to is worse than no watchdog - which is also why the
		// line must never assert something false; see watchdogSweptLine.
		//
		// THAT ARGUMENT COVERS THIS LINE AND ONLY THIS LINE. It was read once as
		// covering the error branch above, and it does not: the "swept at most
		// once" clause is precisely what a row whose write FAILED does not get,
		// because that row stayed non-terminal and comes back next tick. The
		// failed set is aggregated into the summary below instead. A correctness
		// argument written next to one of two branches will be read as covering
		// the pair; say which branch.
```

After the success line, accumulate:

```go
		swept++
		sweptWorkers[uuidStr(t.WorkerID)] = struct{}{}
```

And immediately before the final `return nil`:

```go
	// GATED ON SOMETHING HAVING HAPPENED. An ungated summary would print
	// "0 task(s) swept" every WatchdogSweepInterval forever on a healthy fleet -
	// 1440 lines a day - which is the very bug this line closes, wearing the
	// fix's clothes. TestWatchdog_AFenceRejectionEmitsNoLogLineAtAll is the
	// guard.
	if swept > 0 || failedWrites > 0 {
		msg := fmt.Sprintf("watchdog: sweep ended: %d task(s) swept across %d worker(s)",
			swept, len(sweptWorkers))
		if worstID, worstN := w.counters.worst(); worstID != "" && swept > 0 {
			// CUMULATIVE, and the line says so: the pattern is the actionable
			// part and one sweep does not show it. It is the worst TRACKED
			// worker - if swept_overflow on GET /v1/server/counters is non-zero,
			// the true worst may be a worker the capped map never admitted.
			msg += fmt.Sprintf("; worst since process start: worker %s with %d", worstID, worstN)
		}
		if failedWrites > 0 {
			msg += fmt.Sprintf("; %d write(s) FAILED, first: task %s: %v",
				failedWrites, uuidStr(firstFailID), firstFailErr)
		}
		log.Print(msg)
	}
	return nil
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS, all tests, including the shipped `TestWatchdogLogLineIsNeverSelfContradictory` and `TestWatchdog_APoisonedFirstRowDoesNotStopTheSweep`.

- [ ] **Step 5: Prove the gate is load-bearing (M6)**

Mutate: remove the `if swept > 0 || failedWrites > 0` condition so the summary is unconditional.
Run: `go test ./internal/scheduler/ -run TestWatchdog_AFenceRejectionEmitsNoLogLineAtAll -v`
Expected: FAIL - the captured log is not empty.
Revert. Re-run: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scheduler/watchdog.go internal/scheduler/watchdog_test.go internal/scheduler/watchdog_counters_test.go
git commit -m "fix(scheduler): aggregate the watchdog sweep into one line per sweep

Replaces the per-row error line that repeated every tick for as long as a
non-ErrNoRows write failure persisted, and carries the worst worker since
process start so the pattern is visible in an existing log pipeline."
```

---

## Task 7: `Dispatcher.failClaimedTask` stops logging a correct outcome

**Files:**
- Modify: `internal/scheduler/dispatch.go:417-443`
- Create: `internal/scheduler/dispatch_fence_test.go`

**Implementation note, decide this before writing the test.** `Dispatcher.q` is a concrete `*store.Queries`, so `failClaimedTask` cannot be driven with a fake through the existing type. Two routes; **take (a)**:

- **(a) Narrow the store dependency, exactly as `finalizeTerminalTask` already did when it was extracted (`dispatch.go:368-395`).** Add
  ```go
  type failClaimedStore interface {
      terminalTailStore
      UpdateTaskStatus(ctx context.Context, arg store.UpdateTaskStatusParams) (store.Task, error)
  }
  ```
  and extract the body into a package-level `failClaimedTask(ctx context.Context, q failClaimedStore, broker *events.Broker, claimed store.Task, reason string)`, with the method delegating: `func (d *Dispatcher) failClaimedTask(...) { failClaimedTask(ctx, d.q, d.broker, claimed, reason) }`. This is a precedent in this file, not an invention, and it makes the branch executable.
- **(b) Assert the branch structurally with `go/ast`.** **Rejected.** A source-shape check is the class this project has had thirteen evasions of, and it would not execute the branch at all.

- [ ] **Step 1: Write the failing test**

Create `internal/scheduler/dispatch_fence_test.go`:

```go
package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fenceStore is a failClaimedStore that fails UpdateTaskStatus with a chosen
// error. Its cascade methods panic: failClaimedTask must not reach them when the
// write did not land, and a plausible zero value would hide that.
type fenceStore struct{ err error }

func (f fenceStore) UpdateTaskStatus(context.Context, store.UpdateTaskStatusParams) (store.Task, error) {
	return store.Task{}, f.err
}

func (fenceStore) FailDependentTasks(context.Context, pgtype.UUID) error {
	panic("fenceStore: a rejected write must not cascade")
}

func (fenceStore) RecomputeJobStatus(context.Context, pgtype.UUID) (string, error) {
	panic("fenceStore: a rejected write must not recompute the job")
}

func claimedFixture() store.Task {
	return store.Task{
		ID:              makeUUID(1),
		JobID:           makeUUID(99),
		WorkerID:        makeUUID(200),
		AssignmentEpoch: 7,
		StartedAt:       pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

// TestFailClaimedTask_AFenceRejectionIsNotLoggedAsAnError.
//
// failClaimedTask is the FIFTH of relay's Go-side fence-rejection sites and was
// the only one that did not distinguish pgx.ErrNoRows. The other four are
// handleTaskLog's AppendTaskLog arm, handleTaskStatus's IncrementTaskRetryCount
// and UpdateTaskStatus arms, and Watchdog.SweepOnce.
//
// ErrNoRows here means another writer ended this assignment between the claim
// and this write. That is the correct outcome, not a failure, and logging it
// reported a database error for a race the design expects.
//
// NOTHING IS LOST BY THE SILENCE, and that was checked rather than assumed:
// failClaimedTask's FIRST statement is an unconditional, unbudgeted
// "dispatch: failing task ... terminally: ..." line emitted BEFORE the write on
// every attempt, so a poison task being re-claimed in a loop is still visible
// through that line whatever the write does. Both legs assert it is present, so
// a fix that silenced the whole function would be RED.
func TestFailClaimedTask_AFenceRejectionIsNotLoggedAsAnError(t *testing.T) {
	logged := captureLog(t)
	broker := events.NewBroker()

	t.Run("a fence rejection is silent", func(t *testing.T) {
		failClaimedTask(context.Background(), fenceStore{err: pgx.ErrNoRows},
			broker, claimedFixture(), "bad commands JSON")

		out := logged()
		assert.Contains(t, out, "failing task",
			"the attempt line is unconditional and must stay - it is what keeps a re-claim loop visible")
		assert.NotContains(t, out, "UpdateTaskStatus(failed)",
			"a fence rejection is the correct outcome and must not be reported as a database error")
	})

	t.Run("a real error still speaks", func(t *testing.T) {
		before := len(logged())
		failClaimedTask(context.Background(), fenceStore{err: errors.New("connection reset")},
			broker, claimedFixture(), "bad source JSON")

		out := logged()[before:]
		require.Contains(t, out, "UpdateTaskStatus(failed)")
		assert.Contains(t, out, "connection reset",
			"suppressing ErrNoRows must not suppress a genuine failure - asserted through the error's "+
				"own text, which no other arm of this function produces")
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/scheduler/ -run TestFailClaimedTask -v`
Expected: FAIL to compile (`undefined: failClaimedTask` as a package-level function) until the extraction lands; after the extraction and before the fix, FAIL on `a fence rejection is the correct outcome and must not be reported as a database error`.

- [ ] **Step 3: Write the implementation**

Perform extraction (a), then change the error arm:

```go
	if err != nil {
		// THE FIFTH GO-SIDE FENCE-REJECTION SITE, and until now the only one
		// that did not distinguish pgx.ErrNoRows. The other four are
		// handleTaskLog's AppendTaskLog arm, handleTaskStatus's
		// IncrementTaskRetryCount and UpdateTaskStatus arms, and
		// Watchdog.SweepOnce.
		//
		// ErrNoRows means another path ended this assignment between the claim
		// and here - a cancel, a grace requeue, a sibling replica. That is the
		// CORRECT outcome, not a failure, so it is not logged. Any other error
		// is real.
		//
		// THE SILENCE COSTS NO SIGNAL: the unconditional "failing task ...
		// terminally" line above is emitted before this write on every attempt,
		// so a poison task being re-claimed in a loop stays visible through it.
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("dispatch: UpdateTaskStatus(failed) for task %s: %v", uuidStr(claimed.ID), err)
		}
		return
	}
```

And correct the doc comment at `:423-427`, which currently says "we log and stop without retry or requeue" and would otherwise become wrong prose about correct code:

```go
// Epoch fence: the write goes through UpdateTaskStatus fenced on the claim's own
// assignment_epoch (a real, non-zero value from ClaimTaskForWorker). 'failed' is
// terminal, so the assignment ends and the epoch is intentionally NOT bumped. If
// another path ended the assignment between claim and here, UpdateTaskStatus
// affects zero rows (pgx.ErrNoRows); we stop SILENTLY, without retry, requeue or
// a log line - the race is expected, and the attempt is already reported by the
// unconditional line at the top of this function.
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/dispatch.go internal/scheduler/dispatch_fence_test.go
git commit -m "fix(scheduler): failClaimedTask stops reporting a fence rejection as a database error"
```

---

## Task 8: Wiring - deps field, nil filter, main, and the executed forwarding proof

**Files:**
- Modify: `cmd/relay-server/http_server.go`
- Modify: `cmd/relay-server/main.go:208-268`
- Modify: `cmd/relay-server/counters_wiring_test.go`

- [ ] **Step 1: Write the failing test**

Add to `cmd/relay-server/counters_wiring_test.go` (adding `time`, `relay/internal/scheduler` and `github.com/jackc/pgx/v5/pgtype` to its imports):

```go
// sweepableStore is a Postgres-free watchdogStore. internal/scheduler's
// watchdogStore is an UNEXPORTED INTERFACE WHOSE METHODS ARE ALL EXPORTED, so
// this package can implement it - which is what puts this proof in the DEFAULT
// lane rather than behind //go:build integration. A sweep needs a store, not a
// gRPC recv goroutine and not a container, which is something neither slice 2's
// nor slice 3's forwarding proof could say.
type sweepableStore struct{ overdue []store.Task }

func (s *sweepableStore) ListOverdueAssignedTasks(context.Context, store.ListOverdueAssignedTasksParams) ([]store.Task, error) {
	return s.overdue, nil
}

func (s *sweepableStore) UpdateTaskStatus(_ context.Context, p store.UpdateTaskStatusParams) (store.Task, error) {
	return store.Task{ID: p.ID, JobID: p.ID, Status: p.Status, WorkerID: p.WorkerID,
		AssignmentEpoch: p.AssignmentEpoch}, nil
}

func (s *sweepableStore) NotifyTaskCompleted(context.Context) error             { return nil }
func (s *sweepableStore) FailDependentTasks(context.Context, pgtype.UUID) error { return nil }
func (s *sweepableStore) RecomputeJobStatus(context.Context, pgtype.UUID) (string, error) {
	return "failed", nil
}

// nopCanceller: the watchdog's cancel fan-out is best-effort and irrelevant here.
type nopCanceller struct{}

func (nopCanceller) SendCancel(string, string, bool) error { return nil }

func countersTestUUID(b byte) pgtype.UUID {
	var raw [16]byte
	raw[15] = b
	return pgtype.UUID{Bytes: raw, Valid: true}
}

// TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters is EXECUTED, and it
// moves a REAL number through the REAL route.
//
// It is the strongest forwarding proof any section in this cluster has: a
// substituted scheduler.NewWatchdog inside buildHTTPServer produces a section of
// zeros here and this test FAILS on the count, with no container. The two
// remaining questions live elsewhere - whether main passes the watchdog it runs
// is TestServerCountersIsWiredByMain (syntactic), and whether the assignment is
// spelled d.<field> is countersAssignmentSources.
func TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters(t *testing.T) {
	wid := countersTestUUID(200)
	q := &sweepableStore{overdue: []store.Task{{
		ID: countersTestUUID(1), JobID: countersTestUUID(99), Status: "running", WorkerID: wid,
		AssignmentEpoch: 7,
		AssignedAt:      pgtype.Timestamptz{Time: time.Now().Add(-48 * time.Hour), Valid: true},
	}}}
	wd := scheduler.NewWatchdog(q, nopCanceller{}, events.NewBroker(), 30*time.Minute, 24*time.Hour)

	srv := buildHTTPServer(httpServerDeps{
		addr:     "127.0.0.1:0",
		q:        store.New(stubAdminDB{}),
		watchdog: wd,
	})

	before := countersAsAdmin(t, srv)
	require.Contains(t, before, "watchdog",
		"a wired watchdog must produce the section from the moment the server is built. An absent "+
			"section reads as 'this build has no watchdog', which is false.")

	require.NoError(t, wd.SweepOnce(context.Background()))

	after := countersAsAdmin(t, srv)
	var section struct {
		Counts struct {
			SweptTotal    uint64            `json:"swept_total"`
			SweptOverflow uint64            `json:"swept_overflow"`
			SweptByWorker map[string]uint64 `json:"swept_by_worker"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(after["watchdog"], &section))
	require.Equal(t, uint64(1), section.Counts.SweptTotal,
		"the served endpoint must report THIS watchdog's sweeps. A stub, a second watchdog or an "+
			"unwired section cannot produce this number.")
	require.Equal(t, uint64(0), section.Counts.SweptOverflow)
	require.Len(t, section.Counts.SweptByWorker, 1,
		"and the sweep must be attributed to the worker the row named")
}

// TestBuildHTTPServer_TypedNilWatchdogLeavesTheSectionAbsent.
//
// SAY WHAT THIS GUARDS, because the item that asked for it overstated the case.
// `var wd *scheduler.Watchdog` conditionally assigned stores a TYPED nil in
// api.WatchdogSource, which is not == nil, so the handler's `src != nil` is true
// and CounterSnapshot dereferences a nil receiver. That shape is NOT what main
// writes and cannot be: TestServerCountersIsWiredByMain requires exactly one
// unconditional assignment on the chain, so the watchdog is constructed
// unconditionally even when both its bounds are zero. This test guards the SHAPE
// against a future caller, not a live panic.
//
// The fix belongs at the wiring boundary where the concrete type is still
// visible, and NOT in a nil-tolerant CounterSnapshot: returning a zero snapshot
// would turn an unwired control into a section of zeros, and "not wired" versus
// "ran and stopped nothing" is the one distinction this payload exists to keep.
func TestBuildHTTPServer_TypedNilWatchdogLeavesTheSectionAbsent(t *testing.T) {
	var unwired *scheduler.Watchdog
	srv := buildHTTPServer(httpServerDeps{
		addr:     "127.0.0.1:0",
		q:        store.New(stubAdminDB{}),
		watchdog: unwired,
	})

	top := countersAsAdmin(t, srv)
	require.NotContains(t, top, "watchdog",
		"a nil watchdog must leave the section ABSENT, never present-and-zero, and must never panic")
	require.Contains(t, top, "started_at")
}
```

Also extend the every-source fixture in `TestBuildHTTPServer_EverySourceFieldProducesAServedSection`:

```go
		watchdog: scheduler.NewWatchdog(nil, nil, nil, 0, 0),
```

and add the `wiredDep` row in `TestServerCountersIsWiredByMain`:

```go
		{"watchdog", "NewWatchdog", "the scheduler.Watchdog bound in main's body"},
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/relay-server/ -v`
Expected: compile failure - `unknown field watchdog in struct literal of type httpServerDeps`.

- [ ] **Step 3: Add the deps field and the nil filter**

In `cmd/relay-server/http_server.go`, add `"relay/internal/scheduler"` to the imports and the field to `httpServerDeps`:

```go
	// watchdog is the coordinator stale-task watchdog main runs, typed
	// CONCRETELY rather than as api.WatchdogSource for the same reason
	// grpcAdmission and agentHandler are: a (*scheduler.Watchdog)(nil) stored in
	// that interface is NOT nil, so the counters handler's `src != nil` would be
	// true and CounterSnapshot would dereference a nil receiver.
	//
	// IT MUST BE THE SAME Watchdog main RUNS. A second one would count its own
	// (permanently zero) sweeps while the real ones went unread - a confident
	// zero, which is worse than an absent section.
	//
	// NOTE WHAT IS AND IS NOT GUARDED, so nobody trims one of the three. That
	// main passes the watchdog it runs is syntactic
	// (TestServerCountersIsWiredByMain). That the assignment below is spelled
	// d.<field> is countersAssignmentSources. That buildHTTPServer forwards what
	// it was GIVEN is EXECUTED and, unlike the other two sections, in the
	// DEFAULT lane: scheduler's watchdogStore is an unexported interface with
	// exported methods, so a sweep can be driven from here with no Postgres and
	// no gRPC stream - see
	// TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters.
	watchdog *scheduler.Watchdog
```

and in `buildHTTPServer`, after the `agentHandler` block:

```go
	// ITS OWN `if`, because it is its own deps field. agentHandler feeds two
	// sections under one filter because both controls live on one object; the
	// watchdog is a separate object with a separate lifetime.
	if d.watchdog != nil {
		s.Counters.Watchdog = d.watchdog
	}
```

- [ ] **Step 4: Restructure main**

In `cmd/relay-server/main.go`, move the watchdog bounds block (currently `:252-267`: the comment, both `parseWatchdogDuration` calls, both warning logs, and `watchdogBoundsLine`) to sit immediately **before** the `srv := buildHTTPServer(...)` call at `:215`, and bind the watchdog there:

```go
	// Bound how long a task may hold an assignment. tasks.timeout_sec is
	// otherwise enforced only by the agent, so a wedged or lying agent holds its
	// task - and its worker slot, and its job - forever.
	//
	// CONSTRUCTED HERE, ABOVE buildHTTPServer, because the counters endpoint
	// reports this watchdog's sweeps and buildHTTPServer is the only place the
	// api.Server is built. It is constructed UNCONDITIONALLY even when both
	// bounds are zero (SweepOnce then returns before the round trip): a
	// `var wd *scheduler.Watchdog` assigned inside an `if` is a typed nil in the
	// source interface AND is RED under TestServerCountersIsWiredByMain, which
	// requires exactly one unconditional assignment on the chain. A disabled
	// watchdog therefore serves an honest section of zeros rather than
	// vanishing, which would say "this build has no watchdog".
	watchdogMargin, marginWarning := parseWatchdogDuration(
		"RELAY_TASK_WATCHDOG_MARGIN", os.Getenv("RELAY_TASK_WATCHDOG_MARGIN"),
		scheduler.DefaultWatchdogMargin, minWatchdogMarginDur)
	if marginWarning != "" {
		log.Printf("WARNING: %s", marginWarning)
	}
	maxAssignment, maxAssignmentWarning := parseWatchdogDuration(
		"RELAY_TASK_MAX_ASSIGNMENT", os.Getenv("RELAY_TASK_MAX_ASSIGNMENT"),
		scheduler.DefaultMaxAssignment, minMaxAssignmentDur)
	if maxAssignmentWarning != "" {
		log.Printf("WARNING: %s", maxAssignmentWarning)
	}
	log.Print(watchdogBoundsLine(watchdogMargin, maxAssignment))
	watchdog := scheduler.NewWatchdog(q, registry, broker, watchdogMargin, maxAssignment)
```

Add `watchdog: watchdog,` to the `httpServerDeps` literal. At the old site (`:252-268`), leave only:

```go
	// The watchdog itself is constructed above, next to buildHTTPServer, because
	// the counters endpoint reports its sweeps.
	go watchdog.Run(ctx)
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./cmd/relay-server/ -v`
Expected: PASS, including `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` (5 served keys), `TestServerCountersIsWiredByMain`, and both new tests.

- [ ] **Step 6: Prove the guards by mutation**

Run each, confirm the named test is RED, then revert:

1. Delete `s.Counters.Watchdog = d.watchdog` from `buildHTTPServer`.
   Expected RED: `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` (served 4, want 5).
2. Replace it with `s.Counters.Watchdog = scheduler.NewWatchdog(nil, nil, nil, 0, 0)`.
   Expected RED: `TestServerCountersIsWiredByMain` via `countersAssignmentSources` ("must be assigned from a plain httpServerDeps field"), **and** `TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters` on `swept_total`.
3. In `main.go`, add `if os.Getenv("X") != "" { watchdog = nil }` after the construction.
   Expected RED: `TestServerCountersIsWiredByMain` - `"watchdog" is assigned 2 times inside main`.
4. Remove the `wiredDep` row for `watchdog`.
   Expected RED: `TestServerCountersIsWiredByMain` - `buildHTTPServer feeds an api.CounterSources field from httpServerDeps.watchdog, which has no row in this table`.

- [ ] **Step 7: Commit**

```bash
git add cmd/relay-server/http_server.go cmd/relay-server/main.go cmd/relay-server/counters_wiring_test.go
git commit -m "feat(server): wire the watchdog counters into GET /v1/server/counters"
```

---

## Task 9: Documentation - README, including the sentence that must be corrected

**Files:**
- Modify: `README.md:1260-1291`

- [ ] **Step 1: Add the section to the example payload**

In the JSON block at `README.md:1260-1279`, after `task_log_fence`:

```json
  "watchdog": {
    "counts": {
      "swept_total": 41,
      "swept_overflow": 0,
      "swept_by_worker": { "6b1f...": 37, "9c02...": 4 }
    }
  }
```

- [ ] **Step 2: Correct the `ingest_log_budget` sentence**

`README.md:1288` currently ends:

> The five keys name the log site, not the worker: **there is no per-worker split and there will not be one**, because keying these counters on anything the recv goroutine would have to look up needs a shared map write on the one path that must stay lock-free.

That sentence is correct **about `ingest_log_budget`** and its reason is specific to the gRPC recv goroutine. Left as it stands next to a per-worker `watchdog` section, a reader lands on two adjacent parts of one payload where one says per-worker keying will never happen and the other does it. **Rewrite it - do not append to it:**

> The five keys name the log site, not the worker: **`ingest_log_budget` has no per-worker split and will not get one**, because keying *these* counters on anything the recv goroutine would have to look up needs a shared map write on the one path that must stay lock-free. That reason is specific to that path and does not generalise - `watchdog` below *is* keyed per worker, because it is written on the scheduler goroutine under its own mutex, on a periodic path that already makes a database round trip.

- [ ] **Step 3: Add the reading bullets**

After the `task_log_fence` bullet at `:1290`:

> - **Reading `watchdog`.** `swept_total` counts assignments the coordinator's stale-task watchdog ended since `started_at`, and `swept_by_worker` splits that by worker. **Repeated sweeps against the same worker are the tell that a machine should be disabled**: the watchdog frees the worker's slot the moment it stamps `timed_out`, so a wedged or hostile agent stops holding a fixed set of tasks and starts *draining* queued work and failing it, indefinitely. "Worker X: 37" is that pattern as one number; `GET /v1/workers`, `/v1/workers/stats` and `/v1/workers/{id}/metrics` all show nothing, and the worker's `last_seen_at` stays fresh because its stream is healthy. The same summary appears once per sweep in the server log, naming the worst worker since process start.
> - **`swept_by_worker` is capped at 256 distinct workers, first-come, and `swept_overflow` is how you know.** Worker ids are server-assigned, but their *count* is not server-limited - with `RELAY_ALLOW_AUTO_ENROLL` on, a reachable host creates one persistent worker row per hostname it claims - so an admin-facing document that serialised one key per worker on every request would be unbounded. At capacity a **new** worker's sweeps are added to `swept_overflow` instead; already-tracked workers keep counting, and `swept_total` always equals the sum of the map plus the overflow. **A non-zero `swept_overflow` means per-worker attribution is incomplete and the worst listed worker may not be the worst worker.**
> - **What `watchdog` does NOT count.** It counts assignments *the coordinator* ended. An agent that honours its own timeout writes the same `timed_out` status and contributes **nothing** here. That is deliberate: the two mean opposite things about a worker's health, and the table cannot tell them apart, so this counter side-steps the ambiguity rather than resolving it. Telling them apart would need a new terminal status, or a writer column plus a migration on an epoch-fenced write path; if such a column is ever added for another reason, this counter should be replaced by a windowed query, which is better on every other axis. **Per replica**, and more so than the other sections: the watchdog is multi-replica-safe by first-write-wins, so a sweep of worker X may be counted on either replica and neither one's `swept_by_worker` is the whole story.
> - **A watchdog disabled by `RELAY_TASK_WATCHDOG_MARGIN=0` and `RELAY_TASK_MAX_ASSIGNMENT=0` still reports its section, with every number zero.** Unlike `grpc_admission` with both caps off, nothing there is false - the watchdog genuinely ended nothing - but the payload cannot tell you it is switched off. The startup line does.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document the watchdog counters section and scope the ingest per-worker sentence"
```

---

## Task 10: Full gates

- [ ] **Step 1: Unit suite**

Run: `make test`
Expected: PASS. Record the top-level test count for the retro (HEAD is 603).

- [ ] **Step 2: Race detector, module-wide**

Run (Git Bash): `CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./... -timeout 300s`
Expected: PASS, no `DATA RACE` reports. This is the lane CI runs (`.github/workflows/go-ci.yml` is `go test -race ./...` with no tag), so it must be clean.

- [ ] **Step 3: Integration suites compile and pass**

Run: `go vet -tags integration ./...`
Expected: clean.

Run (Docker Desktop up): `go test -tags integration -p 1 ./internal/scheduler/... ./internal/api/... ./cmd/relay-server/... ./internal/worker/... -timeout 900s`
Expected: PASS. No integration test should change behaviour; if one does, that is a **finding to report, not to fix**.

- [ ] **Step 4: Prove the store and the frontend are untouched**

Run: `git diff origin/main...HEAD -- internal/store/`
Expected: **0 bytes.** This slice adds no SQL, no migration and no generated file. If this prints anything, something ran `make generate` and the change must be reverted.

Run: `git diff --stat origin/main...HEAD -- web/`
Expected: **empty.**

- [ ] **Step 5: Confirm the exact file set**

Run: `git diff --stat origin/main...HEAD`
Expected exactly: `README.md`, `cmd/relay-server/counters_wiring_test.go`, `cmd/relay-server/http_server.go`, `cmd/relay-server/main.go`, `internal/api/server_counters.go`, `internal/api/server_counters_test.go`, `internal/scheduler/dispatch.go`, `internal/scheduler/dispatch_fence_test.go`, `internal/scheduler/watchdog.go`, `internal/scheduler/watchdog_counters.go`, `internal/scheduler/watchdog_counters_test.go`, `internal/scheduler/watchdog_test.go`, plus this plan doc. Anything else is a stray artifact.

---

## Mutation matrix

A test is load-bearing only if a mutation kills it. Run each in an **isolated detached worktree**, never the shared tree - sibling agents read the shared one. Every one of these must be executed and its result recorded.

| # | Mutation | Must go RED |
| --- | --- | --- |
| M1 | `record` called before the `UpdateTaskStatus` error check | `TestWatchdog_AFenceRejectionEmitsNoLogLineAtAll` (its `SweptTotal` assertion) |
| M2 | overflow branch inserts the new key anyway | `TestWatchdogCounters_TheCapIsHardAndOverflowIsCounted` |
| M3 | `snapshot()` returns `w.c` without cloning the map | `TestWatchdogCounters_SnapshotIsACopy` |
| M3b | `SweptTotal++` moved outside the mutex | `-race` on `TestWatchdogCounters_Concurrent...` |
| M4 | `snapshot()` returns the map as-is when nil | `TestWatchdogCounters_SnapshotNeverReturnsANilMap`, `TestServerCounters_WiredButZeroWatchdogSectionIsStillPresent` |
| M5 | `SweptTotal++` moved inside the tracked-key branch | the reconciliation assertion in `TestWatchdogCounters_TheCapIsHardAndOverflowIsCounted` |
| M6 | summary line emitted unconditionally | `TestWatchdog_AFenceRejectionEmitsNoLogLineAtAll` |
| M7 | per-row error line restored inside the loop | `TestWatchdog_APersistentWriteFailureIsBoundedToOneLinePerSweep` |
| M8 | `failClaimedTask` logs on `ErrNoRows` | `TestFailClaimedTask_AFenceRejectionIsNotLoggedAsAnError` |
| M8b | `failClaimedTask` silences the whole error arm | the same test's "a real error still speaks" leg |
| M9 | `s.Counters.Watchdog = scheduler.NewWatchdog(...)` in `buildHTTPServer` | `TestServerCountersIsWiredByMain` (`countersAssignmentSources`) **and** `TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters` |
| M10 | watchdog assignment deleted from `buildHTTPServer` | `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` |
| M11 | `watchdog = nil` inside an `if` in `main` | `TestServerCountersIsWiredByMain` (assigned twice) |
| M12 | **add `SweptTruncated uint64` to `api.WatchdogCounts` and increment nothing** | `TestWatchdog_EveryPublishedCounterIsDrivenByTheSweepFixture` - and record that **no mapper needed editing** for the field to reach a JSON key |
| M13 | add a counter field to `watchdogCounters` beside `c` | `TestWatchdogCountersLiveOnlyInThePublishedStruct` |
| M14 | `jsonOK` relaxed to `func(any) bool { return true }` | `TestWatchdogSweptByWorkerExemptionRejectsHostileShapes` (every negative row) |
| M15 | drop the `len(m) > WatchdogSweptWorkerMax` check from `jsonOK` | the same test's "one over the cap" row |
| M16 | drop the uuid check from `jsonOK` | the same test's newline / hostname / uppercase rows |
| M17 | `record` inserts a non-uuid key instead of counting overflow | `TestWatchdogCounters_AnUnkeyableWorkerGoesToOverflow` |
| M18 | `json:"swept_by_worker,omitempty"` | `TestServerCounters_WiredButZeroWatchdogSectionIsStillPresent` |
| M19 | introduce a restating `watchdogSection` | `TestWatchdogSectionRestatesNothing` |
| M20 | `worst()` reports this sweep's worst instead of the cumulative worst | `TestWatchdog_TheAggregateLineNamesTheWorstWorkerSinceStart` |

**M12 is the headline mutation of this slice.** Run it, record that it needed no mapper edit anywhere, and record which test caught it. That single measurement is the evidence for the claim that the cross-package arity gap which bit slices 2 and 3 is closed *by construction* here rather than merely guarded.

---

## Existing tests

Only these may change, and only as stated:

- `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` - one fixture field added. **No assertion may weaken.**
- `TestServerCountersIsWiredByMain` - one `wiredDep` row added. **No check may be relaxed**, and specifically: do not resolve a cardinality failure by repeating a row, and do not reach a section through anything other than `d.<field>`.
- `TestCounterPayloadBytesCarryNoIdentifiers` - one fixture field added.
- `counterPayloadLeaves` - three entries added.
- `counterPayloadAllowList` - one entry added, with its argument.
- `TestWatchdog_FenceRejectionIsASilentNoOp` - one assertion **added**.

**Any other existing test whose result changes is a finding to report, not to fix.**

---

## Constraint checks

- **Epoch fence.** No SQL, no migration, no generated file, no write to `tasks.status` or `task_logs`. The watchdog's `UpdateTaskStatus` call is byte-identical; only the handling of its error changes. `git diff -- internal/store/` must be 0 bytes.
- **Status vocabulary.** No status is added. The slice explicitly declines the new-terminal-status route (see the `WatchdogCounts` comment), so the two inverted allow-lists (`AppendTaskLog`'s first arm, `ListOverdueAssignedTasks`) are untouched and `TestTasksStatusVocabularyIsExactly` is unaffected.
- **No interior pointers across locks.** `snapshot()` returns a value struct with the map cloned under the lock; the live map's pointer never escapes. Pinned by `TestWatchdogCounters_SnapshotIsACopy`.
- **One bounded sender per gRPC stream.** Untouched. No counter reaches an agent: the only read path is an admin-authenticated HTTP route, and `AgentService` has one RPC this slice does not touch.
- **Single JSON entry point.** The endpoint is a `GET` with no body, so `readJSON` is not involved; the response goes through `writeJSON`.
- **No new attacker-driven log site.** The aggregate line is emitted at most once per `WatchdogSweepInterval`, on the scheduler goroutine, and carries only server-rendered uuids and integers. Nothing is added on the gRPC recv path.
- **Identity-checked teardown / end the generation before releasing the resource.** No generation, no teardown ordering, no async continuation in scope.

---

## Prose honesty checklist (read before Phase 4)

This project's dominant recorded defect class is wrong prose about correct code, and slice 3's headline was a comment that said "impossible" where the truth was "declined, and here is the price". Every one of these must be true of the shipped comments:

- [ ] The DB-query route is written as **declined, with the price and the revisit condition**, never as impossible.
- [ ] The writer ambiguity is written as **side-stepped**, never as resolved or unresolvable.
- [ ] The 256 cap's argument cites **the cap itself**, never the `workers` row count - that is the quantity that is unbounded.
- [ ] Every guard's failure message was read **as an instruction**, and the cheapest literal compliance with it is not the defect. In particular `TestWatchdogCountersLiveOnlyInThePublishedStruct` must not read as "bump the 2".
- [ ] The "one aggregate line per sweep" claim names the **other two** lines a sweep can still emit (the row-cap line and the per-task success line), so "one line" is not read as more than it is.
- [ ] The per-task success line's safety argument says **which branch it covers**.
- [ ] `failClaimedTask`'s doc no longer says "we log and stop".
- [ ] `server_counters.go`'s allow-list paragraph no longer says `started_at` is the whole list, and no longer says `swept_by_worker` is de-authorized.
- [ ] `counterPayloadExemption`'s doc block no longer reads as though `swept_by_worker` is absent.
- [ ] README's `ingest_log_budget` "there will not be one" sentence is **scoped**, not appended to.
- [ ] The typed-nil test says it guards a **shape**, not a live panic reachable from today's `main`.
- [ ] Every section that says what it counts also says, **in the same place**, what it does not (agent-written `timed_out` contributes nothing).
