# handleTaskStatus pair: fence rejections counted, database-error lines budgeted

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `handleTaskStatus`'s silent write-rejection path visible on `GET /v1/server/counters` as a
three-way partition that separates the healthy floor from the actionable case, and route its three
unbudgeted database-error log lines through the per-connection log budget so `ingest_log_budget` stops
reading zero while they carry volume.

**Architecture:** Two backlog items, one slice, one PR. Both live in the same ~60-line region of
`internal/worker/handler.go` and share one publish checklist. A new `[3]atomic.Uint64` on `*worker.Handler`
counts fence rejections, classified at zero cost from the row `GetTask` already read; three new `logKind`
constants route the three database-error lines through the existing `ingestLogLimiter`. Both surface through
the existing admin-only counters endpoint - one new section (`task_status_fence`) and three new keys under
the existing `ingest_log_budget` section.

**Tech Stack:** Go 1.26, `sync/atomic`, sqlc-generated `store.Queries` over a stub `store.DBTX` (default lane,
no container), `pgx/v5`, testify, `net/http/httptest`, `go/ast` structural guards.

---

## Slice independence declaration

**BACKEND ONLY. ONE LANE. NO PARALLELISM IN PHASE 3.**

- Zero files under `web/`. Verified: `grep -r "server/counters|task_log_fence|ingest_log_budget" web/`
  returns **no files**. The counters payload has no frontend consumer, so `web/e2e/` (which exists:
  `auth.spec.ts`, `layout.spec.ts`, `keyboard.spec.ts`, `fixtures.ts`, `global.setup.ts`, `surfaces.ts`)
  needs nothing and the web CI lane is unaffected. Do not touch `web/dist`.
- Zero migrations, zero proto changes.
- **One `.sql` file IS edited** (`internal/store/query/tasks.sql`, comment text only), so Task 9 carries a
  mandatory `make generate` with the CRLF procedure. `git diff -- internal/store/` must show a doc-comment
  change and **zero SQL statement changes**.
- Dispatch a single `relay-backend-engineer` through Tasks 1-11 in order. There is no frontend slice to run
  beside it.

---

## Verification against HEAD: what is confirmed and what is refuted

Every line number below was re-derived at HEAD (`main` @ `08056cd`, worktree
`D:/dev/relay/.claude/worktrees/pr-merge-session-d3977d`). **Both items and the ROADMAP cite stale numbers -
the region has drifted twice, most recently by the finishRegister slice (#146).**

### Line-number drift (all three documents are wrong)

| Site | Item 1 says | Item 2 says | ROADMAP says | **HEAD** |
| --- | --- | --- | --- | --- |
| identity gate | `:914` | - | - | **`handler.go:1083`** |
| currency gate | `:927` | - | - | **`handler.go:1096`** |
| `IncrementTaskRetryCount` arm | `:964-977` | `:939` | `:976` | **`:1133-1146`** (log at `:1145`) |
| `UpdateTaskStatus` arm | `:1005-1024` | `:984` | `:1021` | **`:1174-1193`** (log at `:1190`) |
| `FailDependentTasks` | - | `:991` | `:1028` | **`:1196-1199`** (log at `:1197`) |
| `handleTaskLog marshal` | - | `:1197` | - | **`:1443`** |
| budgeted `GetTask` | - | `:802` | - | **`:1007`** |
| budgeted bad-task-id | - | `:771` | - | **`:976`** |

### R1. REFUTED, and it is the item's own motivating mechanism: the watchdog does **not** bump the epoch

Item 1 says the live cause of an `UpdateTaskStatus` fence rejection is "the coordinator stale-task watchdog
having already bumped the epoch". It does not bump anything.

- `internal/scheduler/watchdog.go:208-219` passes `AssignmentEpoch: t.AssignmentEpoch` - **a fence, not a
  write**, and the comment at `:211-214` says so.
- `internal/store/query/tasks.sql:121-129`: `UpdateTaskStatus` sets `status`, `started_at`, `finished_at` and
  **nothing else**. Its own comment at `:24-26` states it outright: *"It does not bump assignment_epoch
  either, so a terminal task keeps its assignee."*

**The item's OUTCOME is confirmed and its MECHANISM is wrong, and the correction makes the slice better.**
Trace a task the watchdog swept and whose agent then reports `done`:

1. `GetTask` returns the row: `status='timed_out'`, `worker_id` unchanged, `assignment_epoch` unchanged.
2. Identity gate (`:1083`) passes - the assignee is unchanged.
3. Currency gate (`:1096`) passes - **the epoch was never bumped**.
4. `UpdateTaskStatus` rejects on `AND status IN ('pending','dispatched','running')` (`tasks.sql:128`).

So the rejection arrives through the **terminality predicate**, at the same epoch, in the same shape as the
"expected" duplicate-terminal case the item's nuance (a) warns about. The two are even less distinguishable
than the item thought - **and that is exactly why the counter must not be a single scalar** (see D2).

### R2. REFUTED in emphasis: the number is not uniformly a floor. Two of three keys are EXACT.

Item 1's nuance (b) says the Go-side gates pre-filter, so the count is "a strict subset". True, but the
subset structure is sharper than that and it is what makes the design work:

- The Go **identity** gate (`:1083`) and **currency** gate (`:1096`) run *before* `GetTask`'s row is used, so
  the SQL's `worker_id` and `assignment_epoch` predicates can only reject when something changed **inside the
  read-to-write window** (`GetTask` at `:982` to the write at `:1133`/`:1174`). Those rejections are a
  genuine floor.
- The SQL's **terminality** predicate has **no Go-side pre-filter at all** - nothing between `:982` and the
  write reads `task.Status`. So every T0-terminal report that reaches the write is counted, and the count of
  terminality rejections is **exact**, not a floor.

The design below publishes the partition, so the floor semantics attach to one key (`raced_total`) instead of
being a caveat on the whole number.

### R3. CONFIRMED: three sites, not two arms, and the third is ungated

`FailDependentTasks` (`:1196-1199`) logs **every** error with no `errors.Is` gate. It is `:exec`
(`internal/store/tasks.sql.go:474-477`), so `pgx.ErrNoRows` is not a shape it can return - adding an
`errors.Is` gate there would be cargo-culting. It is also **not** a fence-rejection site in this slice's
sense: per CLAUDE.md it satisfies the epoch fence with a terminal-only `WHERE status = 'pending'`, and an
`:exec` yields no rowcount to inspect (this is the second class in
`internal/scheduler/dispatch.go:483-488`). **It is in scope for the log-budget half only.**

### R4. CONFIRMED: the dispatch partition comment is the enumeration to work from

`internal/scheduler/dispatch.go:461-500` read in full. The `:one`/`pgx.ErrNoRows` class is `AppendTaskLog`,
`IncrementTaskRetryCount`, `UpdateTaskStatus`, `Watchdog.SweepOnce`, `failClaimedTask`,
`Dispatcher.sendTask`'s `ClaimTaskForWorker`. The rowcount class is `RequeueTask`, `RequeueTaskByID`
(`:execrows`) and `RequeueWorkerTasksIfEpoch` (`:many`). This slice touches exactly two of the first class.

### R5. CONFIRMED: `failClaimedTask` is a ready site. **DECIDED OUT.** See D6.

### R6. REFUTED: item 2's proposed dedupe key is wrong for these three sites

Item 2 proposes `logKey` "carrying the canonical task id and the epoch, exactly as `kindTaskLogPersist`
does". The argument against it is already written **twenty-five lines above the first new site**, at
`handler.go:994-997`, for the sibling `kindStatusGetTask`: *"such an episode is not per-task: keying on the
task id would multiply one infra event by the task count."* All three new sites are infrastructure failures
(serialization failure, statement timeout, connection reset), not per-task content failures.
`kindTaskLogPersist` carries an id because a persist failure genuinely *is* per-task (one task's bad byte
sequence). **These three carry no wire value**, matching the sibling site in the same function.

### R7. REFUTED on a detail: there are THIRTEEN `log.Printf` sites now, not twelve, and the new one is in
neither of item 2's two classes

`internal/worker/handler.go` at HEAD: `:284`, `:573`, `:669`, `:977`, `:1008`, `:1145`, `:1190`, `:1197`,
`:1289`, `:1421`, `:1443`, `:1590`, `:1708`. The thirteenth (`:1590`, `markWorkerOffline` failing during
`releaseWorkerGeneration`) was added by the finishRegister slice and is a **teardown-path** line: once per
connection teardown, no `lim` in scope on that call chain. It is neither registration-time nor
post-registration-with-lim-in-scope. Out of scope here; state it in the item-close note.

### R8. CONFIRMED with a caveat that changes the harness: `&Handler{}` is NOT enough

Item 2 hedges correctly ("`handleTaskStatus` is reachable with a bare `&Handler{}` for the sites *above*
it"). The three sites in scope are all **below** `GetTask`, so they need a real `store.Queries` over a stub
`store.DBTX`, exactly as `stubFenceDB` does for `AppendTaskLog`. **Still the DEFAULT lane, still no
container.** Task 3 builds that stub.

### R9. REFUTED: the counter type cannot be declared in `internal/api`

`internal/api/server.go` imports `relay/internal/worker`, so `internal/worker` importing `internal/api` is a
cycle. The watchdog's shape (declare in the consumer) is **structurally unavailable** here and the opposite
direction is forced. See D3.

### R10. CONFIRMED: the Go identity gate materially changes what is countable - and gains a third job

`handler.go:1037-1040` currently argues the identity gate "does NOT save a log line". That stays true. But
once the fence counts, the gate is what keeps the counters **attributable to the task's own assignee**:
delete it and any registered agent can drive `raced_total` up by naming tasks it does not own. This is a new
consequence not in either item, and it must be written at the gate and in the payload doc.

### R11. Wrong prose found at HEAD that this slice must fix

- `internal/store/query/tasks.sql:117-118`: *"handleTaskStatus drops pgx.ErrNoRows from both write sites
  silently"* - false after this slice (it counts).
- `internal/store/query/tasks.sql:33-35` (already false at HEAD, in the file this slice must touch anyway):
  *"AgentMessage_TaskStatus is dispatched unbudgeted (**only log chunks go through ingestLogLimiter**)"*. The
  parenthetical is wrong today - `handleTaskStatus` calls `lim.allow` at `:976` and `:1007`, and
  `handleInventoryUpdate` at `:1707`. The intended claim (status *messages* are not rate-limited) is true.
- `internal/worker/handler.go:948-951`: *"Both pre-gate log lines below run AHEAD of the identity and
  currency gates, so the budget is the only thing bounding them"* - false once three budgeted lines sit
  **after** the gates.
- `README.md:1295`: names `handleTaskStatus`'s three lines as outside the budget.
- `internal/worker/ingest_log_counters.go:27-29` and `internal/api/server_counters.go:358-363`: both say a
  handler that decides not to log without consulting the budget "contributes nothing", naming
  `handleTaskStatus`'s `GetTask` case. Still true; but the fence arms are now a *counted* example and the
  sentence should point at the new section rather than leave the reader to enumerate.

---

## Design decisions, with the arguments the items asked for

### D1. Section name and shape: `task_status_fence`, counts only, **three keys that partition**

```json
"task_status_fence": {
  "counts": { "raced_total": 0, "duplicate_total": 0, "conflicting_total": 0 }
}
```

### D2. One counter or two? **Neither. Split by REASON, not by STATEMENT.**

Item 1 proposes splitting `IncrementTaskRetryCount` from `UpdateTaskStatus` because "a retry was not burned"
and "a terminal report was discarded" mean different things. **Refuted on inspection:**

- The two statements carry the **identical** three predicates (`tasks.sql:126-128` vs `:182-184`).
- Which one runs is decided by `terminal && task.RetryCount < task.Retries` (`handler.go:1132`) - a property
  of the T0 row and the reported status, not of the rejection.
- A retry that was not burned means the task does not go back to the queue, which is *also* "the agent's
  report of this task's outcome was discarded". The asserted difference in meaning does not survive.

What genuinely differs, and what an operator needs, is **what the row said at T0**, and both the row's status
and the reported status are already in registers at the rejection site. Zero round trips, zero allocation,
one predicate and one string compare:

| Key | Condition at the rejection site | Meaning |
| --- | --- | --- |
| `raced_total` | `task.Status` was still writable at T0 | A concurrent writer ended the generation inside this handler's own `GetTask`-to-write window. Rare, and a **floor** (see R2). |
| `duplicate_total` | row unwritable at T0 **and** `task.Status == statusStr` | The agent repeated a terminal report it already delivered. **This is the expected healthy floor** the item warns will read as constant alarm. |
| `conflicting_total` | row unwritable at T0 **and** `task.Status != statusStr` | **The actionable one.** Somebody else recorded a different outcome than the agent is reporting. A watchdog-swept task whose agent then reports `done` lands here (`timed_out` in the row, `done` on the wire) - the exact "a successful task recorded as a timeout" signature item 1 was filed for. |

This answers the item's real question (distinguish expected from alarming) which its own two-counter proposal
does **not**: under the item's split, both the duplicate and the watchdog clobber land in
`status_rejected_total` together.

**No `rejected_total`.** The three keys partition the rejections exhaustively, so a published total is the
sum of its own siblings sitting beside them, where it can only agree or be a bug. That is precisely the
defect slice 4's plan refuted in the joint spec's payload (`swept_workers_tracked` restating
`len(swept_by_worker)`). The comment says the total is the sum and says why it is not published.

**Per-reason splitting BEYOND these three is DECLINED, WITH THE PRICE.** A finer split - which of the three
SQL predicates actually failed at T1 - needs a second round trip (forbidden on this path) or a rewrite of
both statements' result contracts to `RETURNING` a reason. Not impossible; declined on price. The comment
must say "declined, and here is the price" and never "impossible" - that correction was slice 3's headline
finding, and slice 3's own `task_log_fence` comment (`server_counters.go:416-421`) is the model wording.

**Honesty about what the labels mean.** The classification reads the row **as this handler read it at T0**,
not as it stood when the write was evaluated. A `done` report on a `running` row that the watchdog sweeps
inside the window is labelled `raced`, which is honest ("a concurrent writer ended it"), not a terminality
misattribution. Write that in the classifier's doc comment; do not let the key names imply the counter knows
which SQL predicate fired.

### D3. Which package owns the type: **`internal/worker`, and it is forced**

`internal/api/server.go` imports `relay/internal/worker` (verified), so the reverse import is a cycle and the
watchdog's "declare in the consumer" shape is structurally unavailable (R9). The type therefore lives in
`internal/worker`, and - following slice 4's *prefer deleting the antecedent to guarding the consequent* -
`internal/api` **uses it directly** rather than restating it:

```go
// internal/api/server_counters.go
type taskStatusFenceSection struct {
    Counts worker.TaskStatusFenceCounts `json:"counts"`
}
```

This wraps; it does not restate. No field name appears twice, so there is **no mapper and no arity to
drift** - the slice-2 defect (a fully correct sixth kind counted on one side and published under no JSON key)
is unreachable here by construction. `TestTaskStatusFenceSectionRestatesNothing` guards the antecedent by
asserting the section field's type **is** the source method's return type, modelled on
`TestWatchdogSectionRestatesNothing` (`server_counters_test.go:1173`).

Consequence to state in the code: **the JSON tags live in `internal/worker`.** That is already this repo's
shape for a response contract owned by the producing package - `logKind`'s comment
(`ingest_log_limiter.go:118-124`) says the kind NAMES are a response contract and they are declared there.

### D4. Concurrency: **atomics, and the reasons are checked rather than copied**

netlimit (slice 1) chose plain `uint64` under a mutex; `ingestLogCounters` (slice 2) chose atomics;
`watchdogCounters` (slice 4) chose a mutex. Both mutex choices rest on two reasons:

1. **A cross-field invariant only one critical section can hold.** netlimit has
   `max_per_source <= live_total <= cap`; the watchdog has `SweptTotal == sum(SweptByWorker) + SweptOverflow`.
   **This section has none - and that is a consequence of D2's "no total".** With no published sum there is
   no relation between the three numbers for a torn snapshot to violate; each is an independent monotonic
   count and a snapshot reading them microseconds apart is unsynchronised in a way nothing can observe.
2. **A mutable container that cannot be updated atomically.** The watchdog has a map. This section has three
   words.

Against that, the increment site is the **gRPC recv goroutine**, whose standing constraint is documented at
`handler.go:1259-1263` and `ingest_log_limiter.go:72-77`: no new lock, queue, goroutine or round trip. An
atomic add is one locked exchange-add, no allocation, no scheduling. **Atomics.** Storage:
`[fenceReasonCount]atomic.Uint64` on `*worker.Handler` as a **value** field, so the zero value works and
there is no nil case (matching `ingestDrops` and `taskLogFenceRejects`, `handler.go:171-205`).

**The enum starts at 0, not `iota + 1`.** Slice 2's plan caught a crash in the item's own sketch: a
`[5][2]` array indexed by constants that start at 1 panics on the recv goroutine for the last kind.
`ingestLogCounters` survives it because its array is sized `[kindCount]` with `kindCount = last + 1`, wasting
slot 0. Here the reasons index directly from 0 and `fenceReasonCount` is the exact length. `record` still
bounds-checks and **fails closed rather than panicking**, for the same reason
`ingestLogCounters.record` does (`ingest_log_counters.go:116-124`): a panic on the recv goroutine kills the
process, and Connect has no recover.

### D5. The log-budget half: **three kinds, not one**

Item 2 leaves this open. Three, and the argument goes in the const block:

- The three sites are **mutually exclusive per message**. The retry arm `return`s; the `UpdateTaskStatus` arm
  `return`s; `FailDependentTasks` only runs *after* a successful `UpdateTaskStatus`. So one message reaches
  at most one of them and the distribution across the three keys is a clean attribution rather than a
  mixture.
- They fail for genuinely different reasons and the remedies differ. `FailDependentTasks` is a **recursive
  CTE** - the most expensive statement on this path and the first to deadlock or hit `statement_timeout`
  under contention - while `UpdateTaskStatus` is a single-row update. "Your recursive cascade is timing out"
  and "your simple updates are failing" are different incidents.
- The cost is bounded and guarded: each kind is 8 mechanical edits, and **three shipped guards fire on an
  omission** (`TestIngestLogKindsAreADenseRunFromOne`,
  `TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished`,
  `TestIngestLogKindCountsPublishesEveryWorkerSideField`). The checklist is mechanical precisely because
  slice 2 paid for it.

Names follow the shipped `status_get_task` precedent: `kindStatusRetryWrite` / `status_retry_write`,
`kindStatusUpdateWrite` / `status_update_write`, `kindStatusFailDependents` / `status_fail_dependents`.

### D6. `Dispatcher.failClaimedTask`: **OUT of this slice**, and here is why

Slice 4 left its `pgx.ErrNoRows` arm silent and deliberately uncounted so as not to pre-empt this design. It
stays out:

1. **This slice's partition is not available there.** `failClaimedTask`'s target is `dispatched` by
   construction (`ClaimTaskForWorker` requires `status='pending'`), which `tasks.sql:92-95` states makes the
   status predicate **tautological** at that call site. So every rejection there is `raced`, always. A
   one-valued partition is not a partition, and publishing it into `task_status_fence` would produce a
   section whose three keys mean different things depending on which producer moved them - an operator
   cannot decompose that.
2. **It counts a different noun.** An agent's report of its own task being discarded, versus the dispatcher
   failing to record a terminal *it* decided. Merging them makes the section mean "somebody's
   `UpdateTaskStatus` was refused", which is not actionable.
3. It is on the scheduler goroutine, so it would need a fifth `CounterSources` field and a fifth section for
   one number - the price slice 4 already recorded.

**The cost of leaving it out, stated rather than glossed:** `task_status_fence` is **not** a census of epoch-
fence rejections in the process. Say so in the section's doc comment and in README ("this section covers the
agent-reported status path only"), and recommend the follow-up item (Phase 6) rather than filing silently.

### D7. `handleTaskLog marshal` (`:1443`): **left unbudgeted, with the reason written down**

Item 2 requires an explicit decision. Leave it, and add a comment: its input is a `taskLogEvent` whose only
caller-supplied field is `string(chunk.Content)`, and `encoding/json` replaces invalid UTF-8 with U+FFFD
rather than failing; the only other error-capable field is `row.CreatedAt.Time`, a `time.Time` from a
`NOW()`-defaulted column, which marshals unless the year leaves `[0,9999]`. **No input is known to reach it.**
Not "impossible" - if a future field is added to `taskLogEvent`, revisit.

### D8. The budget stays INSIDE the `errors.Is` gate, and the two arms become structurally exclusive

Item 2's constraint: the `!errors.Is(err, pgx.ErrNoRows)` gate is unchanged and still short-circuits before
`allow`, so a fence rejection is never counted as a budget drop. Preserved, and made structural - the shipped
`if !errors.Is(...)` becomes `if errors.Is(...) { record } else if lim.allow(...) { log }`. The polarity flip
is deliberate: the two items' relationship ("no input executes both") becomes a property of the branch
structure rather than a claim in two comments. The short-circuit is preserved exactly - `lim.allow` is still
evaluated only on the non-`ErrNoRows` path.

---

## File structure

**Create**

- `internal/worker/taskstatus_fence_counters.go` - the reason enum, the counter storage, `record`,
  `snapshot`, the exported `TaskStatusFenceCounts`, the writability predicate and the classifier. One file
  because these five things change together and are read together.
- `internal/worker/taskstatus_fence_counters_test.go` - the default-lane stub `store.DBTX`, the three
  classification tests, the real-database-error test, the exhaustiveness guard, the SQL lockstep guard, the
  `-race` exactness test.

**Modify**

- `internal/worker/handler.go` - `:171-206` (a counter field), `:214-227` (an accessor), `:941-951` (the doc
  comment R11 falsifies), `:1037-1049` (the identity gate's third job, R10), `:1133-1157` (the retry arm),
  `:1174-1199` (the update arm and the dependency cascade).
- `internal/worker/ingest_log_limiter.go` - `:102-147` (three kinds plus the three-versus-one argument),
  `:5-84` (what the budget now covers).
- `internal/worker/ingest_log_counters.go` - `:22-67` (three fields plus the pointer to the new section),
  `:146-158` (three `byKind` lines).
- `internal/worker/ingest_log_counters_test.go` - `:34-61`, `:279-296` fixtures (the kind lists).
- `internal/worker/ingest_log_limiter_test.go` - `:279-296` (the kind map).
- `internal/api/server_counters.go` - the new source interface, `CounterSources` field, response field,
  handler branch, and the doc-block amendments.
- `internal/api/server_counters_test.go` - `:580-601` (leaves), `:711-770` (the bytes walk fixture),
  `:820-831` (`tenDistinctDrops`), `:861` (the kinds list), plus the new section's tests.
- `cmd/relay-server/http_server.go` - one assignment inside the existing `if d.agentHandler != nil`.
- `internal/store/query/tasks.sql` - comment text only (`:33-35`, `:117-118`), then `make generate`.
- `internal/worker/handler_taskstatus_integration_test.go` - counter assertions on two existing tests.
- `README.md` - `:1260-1302`.

**Critical files** (read these before writing anything): `internal/worker/handler.go:940-1200`,
`internal/worker/ingest_log_limiter.go`, `internal/worker/ingest_log_counters.go`,
`internal/api/server_counters.go`, `internal/store/query/tasks.sql:12-186`,
`internal/worker/tasklog_fence_counter_test.go` (the harness this slice generalises).

---

## Task 1: the reason enum, the counter storage and the accessor

**Files:**
- Create: `internal/worker/taskstatus_fence_counters.go`
- Create: `internal/worker/taskstatus_fence_counters_test.go`
- Modify: `internal/worker/handler.go:205` (add a field), `:227` (add an accessor)

- [ ] **Step 1: Write the failing test**

Create `internal/worker/taskstatus_fence_counters_test.go`:

```go
package worker

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskStatusFenceReasonsAreADenseRunFromZero pins the property the counter
// array depends on. UNLIKE logKind, THESE START AT 0: the array is sized
// [fenceReasonCount] and indexed directly, so a run starting at 1 would put the
// last reason one past the end. record fails closed rather than panicking (a
// panic on the recv goroutine kills the process), so getting this wrong is a
// SILENT loss of that reason's counts - the exact defect this slice closes.
func TestTaskStatusFenceReasonsAreADenseRunFromZero(t *testing.T) {
	run := []taskStatusFenceReason{
		fenceReasonRaced,
		fenceReasonDuplicate,
		fenceReasonConflicting,
	}
	for i, r := range run {
		require.Equal(t, taskStatusFenceReason(i), r,
			"reason #%d is %d. The reasons index the counter array directly, so they must stay a DENSE "+
				"RUN starting at 0.", i, r)
	}
	require.Equal(t, taskStatusFenceReason(len(run)), fenceReasonCount,
		"this test's `run` list has %d entries and fenceReasonCount is %d. IF YOU JUST ADDED A REASON, "+
			"ADD IT TO `run` ABOVE - this compares the hardcoded list's length to the sentinel, so a "+
			"correctly-added reason fails here first. OTHERWISE fenceReasonCount is no longer the length "+
			"of the counter array and a reason at or beyond it is never counted.",
		len(run), int(fenceReasonCount))
}

// TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly drives every
// reason a DIFFERENT number of times and requires the published struct to carry
// exactly those numbers IN ORDER.
//
// ORDERED, NOT ElementsMatch, and for the measured reason recorded in
// TestIngestLogCounters_EveryKindIsPublishedDistinctly: an order-insensitive
// match leaves a crossed pair of assignments in snapshot() green.
func TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly(t *testing.T) {
	var c statusFenceCounters

	var want []uint64
	n := uint64(1)
	for r := taskStatusFenceReason(0); r < fenceReasonCount; r++ {
		for i := uint64(0); i < n; i++ {
			c.record(r)
		}
		want = append(want, n)
		n++
	}

	got := c.snapshot()
	rv := reflect.ValueOf(got)
	require.Equal(t, len(want), rv.NumField(),
		"there are %d reasons and %d published fields. A reason with no field is counted into a cell "+
			"nobody reads - which is slice 2's defect, one package smaller.", len(want), rv.NumField())
	fields := make([]uint64, 0, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		fields = append(fields, rv.Field(i).Uint())
	}
	require.Equal(t, want, fields,
		"every reason must publish its OWN cell, IN ORDER: field i of TaskStatusFenceCounts must read "+
			"reason i. A missing value means a reason is counted but never published; a permutation "+
			"means two fields are crossed.")
}

// TestTaskStatusFenceCounters_AnOutOfRangeReasonIsDroppedNotPanicked. The bounds
// check exists because the alternative on the gRPC recv goroutine is a panic
// that kills the process. It is unreachable while the dense-run test is green,
// and this exists so "unreachable" does not mean "untested".
func TestTaskStatusFenceCounters_AnOutOfRangeReasonIsDroppedNotPanicked(t *testing.T) {
	var c statusFenceCounters
	require.NotPanics(t, func() {
		c.record(fenceReasonCount)
		c.record(taskStatusFenceReason(200))
	})
	require.Equal(t, TaskStatusFenceCounts{}, c.snapshot(),
		"an out-of-range reason must be DROPPED, not folded into some other cell")
}

// TestTaskStatusFenceRejections_TwoHandlersDoNotShareCounts pins the HOME. A
// package-level var would make every exact-count assertion in this package
// order-dependent on every other test in the binary; production has exactly one
// Handler, so a value field IS process-wide there.
func TestTaskStatusFenceRejections_TwoHandlersDoNotShareCounts(t *testing.T) {
	var a, b Handler
	a.statusFence.record(fenceReasonDuplicate)

	require.Equal(t, uint64(1), a.TaskStatusFenceRejections().Duplicate)
	require.Equal(t, TaskStatusFenceCounts{}, b.TaskStatusFenceRejections(),
		"counters are per Handler, not per package")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worker/ -run TestTaskStatusFence -v -timeout 60s`
Expected: FAIL to compile - `undefined: taskStatusFenceReason`, `undefined: statusFenceCounters`,
`undefined: TaskStatusFenceCounts`, `h.statusFence undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/worker/taskstatus_fence_counters.go`:

```go
package worker

import "sync/atomic"

// taskStatusFenceReason partitions the rejections handleTaskStatus's two
// epoch-fenced writes produce, by WHAT THE ROW SAID WHEN THIS HANDLER READ IT -
// which is the only thing available without a second round trip.
//
// THE VALUES ARE ARRAY INDICES AND THEY START AT 0. statusFenceCounters is a
// [fenceReasonCount]atomic.Uint64 indexed by these constants, so they must stay
// a DENSE RUN from 0 with fenceReasonCount immediately after the last one. Note
// this differs deliberately from logKind, which starts at 1 because its array is
// sized [kindCount] and wastes slot 0; a run starting at 1 with an array sized
// by the sentinel puts the last constant one past the end. record fails CLOSED
// rather than panicking, so a gap or a renumbering is a SILENT loss of that
// reason's counts. Pinned by TestTaskStatusFenceReasonsAreADenseRunFromZero.
//
// THE NAMES ARE A RESPONSE CONTRACT: each maps to one JSON key under
// task_status_fence.counts on GET /v1/server/counters. Renaming one renames an
// operator-visible key.
type taskStatusFenceReason uint8

const (
	// fenceReasonRaced: the row was still WRITABLE when GetTask read it, so
	// something else ended the generation inside this handler's own
	// read-to-write window - a cancel, a grace requeue, a sibling replica, the
	// coordinator watchdog. THIS IS THE ONE KEY THAT IS A FLOOR: the Go-side
	// identity and currency gates reject stale and forged messages a round trip
	// earlier, so only the narrow TOCTOU window reaches the SQL fence's
	// worker_id and assignment_epoch predicates.
	fenceReasonRaced taskStatusFenceReason = iota

	// fenceReasonDuplicate: the row was already unwritable and its status is
	// the one being reported. THE EXPECTED HEALTHY FLOOR. UpdateTaskStatus
	// deliberately refuses to write a terminal row (see the terminality
	// paragraph in internal/store/query/tasks.sql), and a terminal transition
	// neither bumps the epoch nor clears worker_id, so a duplicate terminal
	// message from a perfectly healthy assignee lands here. A non-zero value is
	// not an incident; its height depends on agent retry behaviour.
	fenceReasonDuplicate

	// fenceReasonConflicting: the row was already unwritable and its status
	// DISAGREES with what the agent is reporting. THE ACTIONABLE ONE. The
	// coordinator recorded one outcome and the agent is reporting another: a
	// task the stale-task watchdog stamped `timed_out` whose agent then reports
	// `done` lands here, which is the "a successful task recorded as a timeout"
	// case RELAY_TASK_WATCHDOG_MARGIN set too small produces, and before this
	// number there was no runtime signal of any kind for it.
	fenceReasonConflicting

	// fenceReasonCount MUST STAY LAST and is NOT a reason. It is the LENGTH of
	// the counter array.
	fenceReasonCount
)

// TaskStatusFenceCounts is what handleTaskStatus's epoch-fenced writes have
// refused since process start, split by what the row said at T0.
//
// DECLARED HERE, IN internal/worker, AND THAT DIRECTION IS FORCED: internal/api
// imports this package (server.go), so the watchdog's shape - declare the type
// in the consumer - is a compile error here. internal/api's response section
// carries this type DIRECTLY rather than restating its fields, so there is no
// hand-written mapper on either side and no arity to drift. That is slice 2's
// defect (a fully correct sixth kind counted on one side, published under no
// JSON key, all three packages green) made unreachable rather than guarded.
// TestTaskStatusFenceSectionRestatesNothing holds the antecedent.
//
// THE JSON TAGS LIVE IN THIS PACKAGE, which is already how a response contract
// owned by a producer is spelled here - see the logKind block's second bullet.
//
// THERE IS NO TOTAL, AND THAT IS A DECISION. The three fields partition the
// rejections exhaustively, so a published total would be the sum of its own
// siblings sitting beside them, where it can only agree or be a bug. (The joint
// spec's payload made exactly that mistake with swept_workers_tracked and it was
// refuted before slice 4 shipped.) The total is Raced+Duplicate+Conflicting;
// derive it, do not publish it.
//
// A FINER SPLIT - WHICH OF THE THREE SQL PREDICATES ACTUALLY FIRED - IS
// DECLINED, WITH THE PRICE, NOT IMPOSSIBLE. Both statements are single-row
// UPDATE ... WHERE forms that return no row on any predicate failure, so there
// is nothing to carry a reason. Recovering it needs a second round trip
// (forbidden on the recv goroutine) or a rewrite of both result contracts to
// RETURNING a reason. Read internal/api/server_counters.go's task_log_fence
// paragraph before "improving" this.
//
// WHAT THESE NUMBERS DO NOT COVER, said here because it is what an operator will
// get wrong:
//
//   - THE COORDINATOR'S OWN FENCE REJECTIONS ARE NOT HERE. Dispatcher.
//     failClaimedTask and Watchdog.SweepOnce are fenced by the same statement
//     and are counted nowhere. This section is the AGENT-REPORTED status path
//     only; it is not a census.
//   - THEY ARE NOT COMPARABLE WITH task_log_fence.counts.rejected_total. That
//     arm has no Go-side pre-filter; this one runs behind an identity gate and a
//     currency gate. No input moves both.
//   - THEY ARE ATTRIBUTABLE TO THE TASK'S OWN ASSIGNEE, and that is what the Go
//     identity gate at handleTaskStatus buys now that this number exists. A
//     non-assignee's forged report is dropped a round trip earlier and never
//     reaches a counter, so conflicting_total cannot be inflated by a peer
//     naming tasks it does not own.
//   - PER REPLICA, monotonic, zeroed by a restart, and never returned to an
//     agent: the only read path is the admin-authenticated
//     GET /v1/server/counters.
type TaskStatusFenceCounts struct {
	Raced       uint64 `json:"raced_total"`
	Duplicate   uint64 `json:"duplicate_total"`
	Conflicting uint64 `json:"conflicting_total"`
}

// statusFenceCounters is the process-lifetime home. A VALUE FIELD on Handler,
// not a package-level var: there is exactly one Handler per server process, so
// per-Handler IS process-wide in production while every test gets its own.
//
// ATOMICS, NOT A MUTEX, AND THE REASONS ARE CHECKED RATHER THAN COPIED. netlimit
// and the watchdog both took a mutex, on two grounds: a cross-field invariant
// only one critical section can hold, and a mutable container that cannot be
// updated atomically. NEITHER APPLIES. There is no container, and there is no
// invariant precisely BECAUSE no total is published (see TaskStatusFenceCounts)
// - three independent monotonic counts read microseconds apart are not
// inconsistent, merely unsynchronised in a way nothing can observe. Meanwhile
// the increment site is the gRPC recv goroutine, whose standing constraint is no
// new lock, queue, goroutine or round trip; an atomic add is one locked
// exchange-add with no allocation and no scheduling.
//
// COUNTERS, NEVER LOG LINES. A log.Printf on either arm would be caller-driven
// volume on the recv goroutine, firing on the legitimate duplicate-terminal
// case. Do not.
type statusFenceCounters struct {
	n [fenceReasonCount]atomic.Uint64
}

// record adds one rejection. Out of range fails CLOSED - a panic here runs on
// the gRPC recv goroutine, which Connect does not recover and grpc-go does not
// recover either, so it would kill the whole server process. Losing a count is
// the cheaper failure. This branch is UNREACHABLE while
// TestTaskStatusFenceReasonsAreADenseRunFromZero is green, AND THAT TEST IS THE
// ONLY THING KEEPING IT SO.
func (c *statusFenceCounters) record(r taskStatusFenceReason) {
	i := int(r)
	if i < 0 || i >= len(c.n) {
		return
	}
	c.n[i].Add(1)
}

// snapshot reads the three cells. Every field here is one JSON key of the
// endpoint's task_status_fence section; adding a reason without adding a line
// here counts it into a cell nobody reads, which
// TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly turns RED.
func (c *statusFenceCounters) snapshot() TaskStatusFenceCounts {
	return TaskStatusFenceCounts{
		Raced:       c.n[fenceReasonRaced].Load(),
		Duplicate:   c.n[fenceReasonDuplicate].Load(),
		Conflicting: c.n[fenceReasonConflicting].Load(),
	}
}
```

In `internal/worker/handler.go`, add a field to `Handler` immediately after `taskLogFenceRejects`
(currently ending at `:205`):

```go
	// statusFence counts the rejections handleTaskStatus's two epoch-fenced
	// writes produced, split by what the row said when GetTask read it. A VALUE,
	// not a pointer, for the same reason ingestDrops and taskLogFenceRejects are:
	// the zero value works, so a bare &Handler{} in a test has working counters
	// and there is no nil case anywhere. Read through TaskStatusFenceRejections;
	// wired to GET /v1/server/counters by cmd/relay-server's buildHTTPServer
	// under its OWN section and its OWN CounterSources field.
	//
	// A THIRD DISTINCT NOUN. ingestDrops counts LOG LINES THE BUDGET DROPPED;
	// taskLogFenceRejects counts LOG CHUNKS AppendTaskLog's fence rejected; this
	// counts STATUS REPORTS the status fence rejected. No input moves more than
	// one of the three. Do not sum them and do not merge the sections.
	statusFence statusFenceCounters
```

Add the accessor immediately after `TaskLogFenceRejections` (currently `:227`):

```go
// TaskStatusFenceRejections reports what handleTaskStatus's two epoch-fenced
// writes have refused since process start, split by what the row said at T0.
//
// It satisfies api.TaskStatusFenceSource. NOTE THE NEIGHBOUR: TaskLogFence
// Rejections is one letter away in the middle and returns a uint64, so a crossed
// wiring is a compile error rather than a silently wrong section. Per PROCESS,
// monotonic, zeroed by a restart, and never returned to an agent.
func (h *Handler) TaskStatusFenceRejections() TaskStatusFenceCounts {
	return h.statusFence.snapshot()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worker/ -run TestTaskStatusFence -v -timeout 60s`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/worker/taskstatus_fence_counters.go internal/worker/taskstatus_fence_counters_test.go internal/worker/handler.go
git commit -m "feat(worker): a three-reason counter home for handleTaskStatus fence rejections"
```

---

## Task 2: the writability predicate and its lockstep guard against tasks.sql

**Files:**
- Modify: `internal/worker/taskstatus_fence_counters.go` (append)
- Modify: `internal/worker/taskstatus_fence_counters_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/taskstatus_fence_counters_test.go`:

```go
// TestTaskStatusWritableSetMatchesTheSQLAllowList reads the allow-list out of
// internal/store/query/tasks.sql and requires the Go mirror to be exactly it,
// for BOTH statements handleTaskStatus writes through.
//
// WHY A GUARD AND NOT JUST A COMMENT. taskStatusIsWritable restates a set that
// lives in SQL, and this repo's recorded lesson is that a hand-written copy
// needs something comparing it to its source. The parse is deliberately narrow:
// it slices the file between one `-- name: X` header and the next, then reads
// the single `status IN (...)` clause inside, so a predicate added to a
// DIFFERENT statement cannot satisfy it.
//
// STATE THE STAKE HONESTLY, because it is lower than every other status
// allow-list in this tree and a reader who assumes otherwise will over-react to
// a failure here: this set gates NOTHING. It labels a counter. Drift mislabels a
// number; it cannot admit a write. That is exactly why the guard is cheap enough
// to be worth having.
func TestTaskStatusWritableSetMatchesTheSQLAllowList(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "store", "query", "tasks.sql"))
	require.NoError(t, err)
	sql := string(src)

	clause := regexp.MustCompile(`status IN \(([^)]*)\)`)
	quoted := regexp.MustCompile(`'([a-z_]+)'`)

	for _, stmt := range []string{"UpdateTaskStatus", "IncrementTaskRetryCount"} {
		start := strings.Index(sql, "-- name: "+stmt+" ")
		require.GreaterOrEqual(t, start, 0,
			"tasks.sql no longer declares %s. handleTaskStatus writes through it, so either it was "+
				"renamed (update this list) or the write path changed (re-derive this whole guard).", stmt)
		end := strings.Index(sql[start+1:], "-- name: ")
		require.GreaterOrEqual(t, end, 0, "%s is the last statement in tasks.sql; this parse needs a terminator", stmt)
		body := sql[start : start+1+end]

		found := clause.FindAllStringSubmatch(body, -1)
		require.Len(t, found, 1,
			"%s carries %d `status IN (...)` clauses. This guard reads exactly one; if the statement now "+
				"has two, decide which one taskStatusIsWritable mirrors and say so here.", stmt, len(found))

		var want []string
		for _, m := range quoted.FindAllStringSubmatch(found[0][1], -1) {
			want = append(want, m[1])
		}
		require.NotEmpty(t, want, "parsed no statuses out of %s's allow-list; the parse is broken, not the code", stmt)

		for _, s := range want {
			require.True(t, taskStatusIsWritable(s),
				"tasks.sql's %s admits status %q and taskStatusIsWritable says it is NOT writable. The "+
					"two have drifted: a rejection for a %q row would now be labelled `duplicate` or "+
					"`conflicting` when it is in fact a `raced`. Add it.", stmt, s, s)
		}
		for _, s := range []string{"done", "failed", "timed_out"} {
			require.False(t, taskStatusIsWritable(s),
				"%q is not in %s's allow-list but taskStatusIsWritable says it is writable. Every "+
					"terminality rejection would then be labelled `raced` and conflicting_total would "+
					"read zero forever - the actionable key silenced.", s, stmt)
		}
	}
}

// TestClassifyStatusFenceRejection is the classifier's own truth table, with the
// watchdog case named because it is the reason this slice exists.
func TestClassifyStatusFenceRejection(t *testing.T) {
	tests := []struct {
		name           string
		row, reported  string
		want           taskStatusFenceReason
	}{
		{"still writable at T0 is a race", "running", "done", fenceReasonRaced},
		{"dispatched is writable too", "dispatched", "running", fenceReasonRaced},
		{"pending is writable too", "pending", "failed", fenceReasonRaced},
		{"the agent repeats its own terminal", "done", "done", fenceReasonDuplicate},
		{"a repeated failure", "failed", "failed", fenceReasonDuplicate},
		{"watchdog swept it and the agent reports success", "timed_out", "done", fenceReasonConflicting},
		{"watchdog swept it and the agent is still heartbeating", "timed_out", "running", fenceReasonConflicting},
		{"the agent contradicts its own outcome", "done", "failed", fenceReasonConflicting},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyStatusFenceRejection(tc.row, tc.reported))
		})
	}
}
```

Add to that file's imports: `"os"`, `"path/filepath"`, `"regexp"`, `"strings"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worker/ -run "TestTaskStatusWritableSet|TestClassifyStatusFence" -v -timeout 60s`
Expected: FAIL to compile - `undefined: taskStatusIsWritable`, `undefined: classifyStatusFenceRejection`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/worker/taskstatus_fence_counters.go`:

```go
// taskStatusIsWritable mirrors the status allow-list carried by BOTH statements
// handleTaskStatus writes through - UpdateTaskStatus and IncrementTaskRetryCount
// (internal/store/query/tasks.sql; the rule is stated once, at UpdateTaskStatus,
// and the two must change together).
//
// READ THE STAKE BEFORE COPYING THIS SOMEWHERE ELSE. Every other status
// allow-list in this tree is a CONTROL: it decides whether a write happens, and
// CLAUDE.md's allow-list-never-deny-list rule exists because the wrong shape
// fails OPEN on the next status added. This one decides nothing. It LABELS A
// COUNTER, so drift mislabels a number and cannot admit a write. It is written
// as the allow-list anyway, so that its shape matches the SQL it mirrors and so
// that TestTaskStatusWritableSetMatchesTheSQLAllowList can compare the two sets
// directly rather than the complement of one against the other.
//
// A NEW NON-TERMINAL STATUS (`preparing` is the live candidate: TASK_STATUS_
// PREPARING is already in the proto) MUST BE ADDED HERE at the same time it is
// added to those two SQL allow-lists, or a rejection for such a row is labelled
// `duplicate`/`conflicting` when it is in fact a race. The guard test goes RED.
func taskStatusIsWritable(status string) bool {
	switch status {
	case "pending", "dispatched", "running":
		return true
	}
	return false
}

// classifyStatusFenceRejection labels a rejection from the row THIS HANDLER READ
// AT T0 and the status the agent reported, both of which are already in hand at
// the rejection site. No round trip, no allocation.
//
// SAY WHAT IT KNOWS AND WHAT IT DOES NOT. It does not know which SQL predicate
// fired at T1 - the statement yields no row, so nothing can carry that. It knows
// whether the row was ALREADY unwritable when this handler read it, which is a
// sufficient condition for the rejection (a terminal row is one-way: the only
// statement that reopens one, RetryJobTasks, bumps the epoch, so the agent's
// next report is rejected by the currency gate instead). A row that was still
// writable at T0 and rejected at T1 therefore had its generation ended INSIDE
// this handler's own window, which is what `raced` names.
//
// The consequence, stated so the key names are not over-read: a `done` report on
// a `running` row that the watchdog sweeps inside the window is labelled
// `raced`, not `conflicting`. That is honest - a concurrent writer ended it -
// and it is why `raced` is documented as the floor rather than as a measurement.
func classifyStatusFenceRejection(rowStatus, reported string) taskStatusFenceReason {
	if taskStatusIsWritable(rowStatus) {
		return fenceReasonRaced
	}
	if rowStatus == reported {
		return fenceReasonDuplicate
	}
	return fenceReasonConflicting
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worker/ -run "TestTaskStatusWritableSet|TestClassifyStatusFence" -v -timeout 60s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/taskstatus_fence_counters.go internal/worker/taskstatus_fence_counters_test.go
git commit -m "feat(worker): classify a status-fence rejection from the row already read"
```

---

## Task 3: the default-lane stub, and the increments at both arms

**Files:**
- Modify: `internal/worker/taskstatus_fence_counters_test.go` (append the stub and the arm tests)
- Modify: `internal/worker/handler.go:1133-1157` (retry arm), `:1174-1193` (update arm)

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/taskstatus_fence_counters_test.go`:

```go
// stubStatusDB is the narrowest store.DBTX that drives handleTaskStatus's write
// path WITHOUT Postgres, which is what puts this proof in the lane CI actually
// runs (go-ci: `go test -race ./...`, no tag, no container).
//
// IT DISPATCHES ON THE STATEMENT'S OWN `-- name:` HEADER, which sqlc emits as
// the first line of every generated SQL constant. That is a property of the
// generated code rather than of a hand-copied SQL fragment, so a reformatted
// statement cannot silently re-route a call.
//
// Unlike handleTaskLog, this handler is MORE than one statement - GetTask, then
// one of two writes, then (on the success path) FailDependentTasks,
// RecomputeJobStatus and NotifyTaskCompleted - so Exec and Query return benign
// values instead of panicking. calls records what was actually reached, which is
// how the success leg establishes acceptance POSITIVELY rather than through a
// projection every other arm also produces.
type stubStatusDB struct {
	task     store.Task // what GetTask returns
	writeErr error      // what the retry/update statement returns
	calls    []string
	execErr  error
}

func (d *stubStatusDB) note(sql string) string {
	for _, name := range []string{
		"GetTask", "UpdateTaskStatus", "IncrementTaskRetryCount",
		"FailDependentTasks", "RecomputeJobStatus", "NotifyTaskCompleted", "NotifyTaskSubmitted",
	} {
		if strings.Contains(sql, "-- name: "+name+" ") {
			d.calls = append(d.calls, name)
			return name
		}
	}
	d.calls = append(d.calls, "UNKNOWN")
	return "UNKNOWN"
}

func (d *stubStatusDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.note(sql)
	return pgconn.CommandTag{}, d.execErr
}

func (d *stubStatusDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.note(sql)
	return nil, errors.New("stubStatusDB: handleTaskStatus performs no multi-row Query")
}

func (d *stubStatusDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch d.note(sql) {
	case "GetTask":
		return stubTaskRow{task: d.task}
	case "UpdateTaskStatus", "IncrementTaskRetryCount":
		return stubTaskRow{task: d.task, err: d.writeErr}
	case "RecomputeJobStatus":
		return stubStringRow{s: "running"}
	}
	return stubTaskRow{err: errors.New("stubStatusDB: unexpected QueryRow")}
}

// stubTaskRow fills a store.Task BY POSITION, and the positional copy is safe
// for a checked reason rather than by luck: sqlc scans a `SELECT *` row in
// MODEL FIELD ORDER (internal/store/tasks.sql.go: &i.ID, &i.JobID, ... matches
// store.Task's declaration exactly), so reflecting over the value gives the same
// order the generated Scan asks for. The arity assertion is what makes that a
// checked claim: a regenerated model with a new column fails here loudly instead
// of silently shifting every field by one.
type stubTaskRow struct {
	task store.Task
	err  error
}

func (r stubTaskRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	rv := reflect.ValueOf(r.task)
	if len(dest) != rv.NumField() {
		return fmt.Errorf("stubTaskRow: generated Scan wants %d columns and store.Task has %d fields. "+
			"This stub copies by position because sqlc scans in model field order; that assumption no "+
			"longer holds, so re-derive it rather than padding the list", len(dest), rv.NumField())
	}
	for i, d := range dest {
		dv := reflect.ValueOf(d)
		if dv.Kind() != reflect.Pointer || dv.Elem().Type() != rv.Field(i).Type() {
			return fmt.Errorf("stubTaskRow: column %d is %T and store.Task field %d is %s - the scan "+
				"order and the field order have diverged", i, d, i, rv.Field(i).Type())
		}
		dv.Elem().Set(rv.Field(i))
	}
	return nil
}

type stubStringRow struct{ s string }

func (r stubStringRow) Scan(dest ...any) error {
	if len(dest) == 1 {
		if p, ok := dest[0].(*string); ok {
			*p = r.s
		}
	}
	return nil
}

func statusWorkerID() pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{9}, Valid: true} }

const statusTaskID = "3f1c0a2e-7b64-4d8a-9c31-0e5b6a7d8c90"

func statusTaskIDUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(statusTaskID))
	return u
}

// newStatusHandler wires a Handler over the stub with a task the connection's
// worker OWNS at the CURRENT epoch, so both Go-side gates pass and control
// really reaches the write. Any test that wants a gate to reject changes the
// fixture, never the handler.
func newStatusHandler(t *testing.T, rowStatus string, retries, retryCount int32, writeErr error) (*Handler, *stubStatusDB) {
	t.Helper()
	db := &stubStatusDB{
		task: store.Task{
			ID:              statusTaskIDUUID(t),
			JobID:           pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			Status:          rowStatus,
			WorkerID:        statusWorkerID(),
			AssignmentEpoch: 7,
			Retries:         retries,
			RetryCount:      retryCount,
		},
		writeErr: writeErr,
	}
	return &Handler{q: store.New(db), broker: events.NewBroker()}, db
}

func statusUpdate(s relayv1.TaskStatus) *relayv1.TaskStatusUpdate {
	return &relayv1.TaskStatusUpdate{TaskId: statusTaskID, Status: s, Epoch: 7}
}

// TestHandleTaskStatus_TheUpdateArmCountsEachRejectionReasonAndASuccessCountsNothing
// is item 1's own Done-When at the UpdateTaskStatus arm: read the counters
// across each rejection AND across a success.
//
// EACH LEG IS ASSERTED IMMEDIATELY AFTER IT RUNS. A battery that only checks
// totals at the end cannot tell "the success incremented" from "the third
// rejection did not", and a poisoned input observed only at the end cannot
// detect an early-exit mutation.
func TestHandleTaskStatus_TheUpdateArmCountsEachRejectionReasonAndASuccessCountsNothing(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	// CONFLICTING FIRST, because it is the leg this slice exists for and a
	// poisoned input placed last cannot detect an early-exit mutation. The
	// watchdog stamped `timed_out`; the agent reports `done`.
	h, db := newStatusHandler(t, "timed_out", 0, 0, pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Conflicting: 1}, h.TaskStatusFenceRejections(),
		"a task the coordinator marked timed_out whose agent reports done is the ACTIONABLE case: a "+
			"successful task recorded as a timeout. Before this number there was no runtime signal of "+
			"any kind for it.")
	require.Contains(t, db.calls, "UpdateTaskStatus", "fixture: control must reach the write")
	require.NotContains(t, db.calls, "FailDependentTasks",
		"fixture: a rejected write must return before any follow-on effect")

	// DUPLICATE: same row status as the report. The expected healthy floor.
	h, _ = newStatusHandler(t, "done", 0, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Duplicate: 1}, h.TaskStatusFenceRejections(),
		"a duplicate terminal from a healthy assignee is an EXPECTED rejection and must be counted "+
			"under its own key, or the actionable number reads as constant alarm")

	// RACED: the row was still writable at T0, so something ended the generation
	// inside this handler's own window.
	h, _ = newStatusHandler(t, "running", 0, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Raced: 1}, h.TaskStatusFenceRejections())

	// ACCUMULATION on ONE handler: an Add, never a Store.
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Raced: 2}, h.TaskStatusFenceRejections())

	// SUCCESS MUST NOT COUNT, on the SAME handler whose counter has already
	// moved: a counter that increments unconditionally passes a fresh-handler
	// check. Acceptance is established POSITIVELY, by the follow-on effect only
	// the accepted path produces.
	db2 := &stubStatusDB{task: store.Task{
		ID: statusTaskIDUUID(t), JobID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		Status: "running", WorkerID: statusWorkerID(), AssignmentEpoch: 7,
	}}
	h2 := &Handler{q: store.New(db2), broker: events.NewBroker()}
	h2.handleTaskStatus(ctx, statusWorkerID(), newIngestLogLimiter(&h2.ingestDrops),
		statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{}, h2.TaskStatusFenceRejections(),
		"an ACCEPTED status report must not be counted as a rejection. This number is what an operator "+
			"reads as 'reports are being discarded'; incrementing it on the happy path makes it noise.")
	require.Contains(t, db2.calls, "RecomputeJobStatus",
		"THE REPORT WAS ACCEPTED, and this is what says so. Without a positive marker the leg above "+
			"asserts a negative through a projection every other arm shares.")
	require.Contains(t, db2.calls, "NotifyTaskCompleted")

	require.Equal(t, "", logged(),
		"a fence rejection must emit NO log line of any wording, including a budgeted one: it is "+
			"caller-driven volume on the recv goroutine, firing on the legitimate duplicate-terminal case")
}

// TestHandleTaskStatus_TheRetryArmCountsItsOwnRejections. The retry branch is
// reached instead of the update when the report is terminal and a retry is
// left, so it needs its own fixture and its own leg.
func TestHandleTaskStatus_TheRetryArmCountsItsOwnRejections(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	// CONFLICTING FIRST again. The watchdog stamped timed_out; the agent reports
	// failed and still has a retry left.
	h, db := newStatusHandler(t, "timed_out", 3, 0, pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Contains(t, db.calls, "IncrementTaskRetryCount",
		"fixture: terminal + retries remaining must take the RETRY branch")
	require.NotContains(t, db.calls, "UpdateTaskStatus",
		"fixture: the retry branch returns; the two arms are mutually exclusive and no input executes both")
	require.Equal(t, TaskStatusFenceCounts{Conflicting: 1}, h.TaskStatusFenceRejections(),
		"the retry arm's rejections are the SAME noun as the update arm's - the agent's report of this "+
			"task's outcome was discarded - so they share the section and are split by REASON, not by "+
			"statement")

	// DUPLICATE at the retry arm.
	h, _ = newStatusHandler(t, "failed", 3, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Equal(t, TaskStatusFenceCounts{Duplicate: 1}, h.TaskStatusFenceRejections())

	// A SUCCESSFUL retry must not count, and must still wake the dispatcher.
	h, db = newStatusHandler(t, "running", 3, 0, nil)
	lim = newIngestLogLimiter(&h.ingestDrops)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections())
	require.Contains(t, db.calls, "NotifyTaskSubmitted",
		"the accepted retry must still wake the dispatcher; this is the positive marker for this arm")

	require.Equal(t, "", logged(), "no arm of the retry branch logs")
}

// TestHandleTaskStatus_ARealDatabaseErrorIsNotAFenceRejection is the poisoned
// input in its own test, and it is what kills a record() written ABOVE the
// errors.Is check.
func TestHandleTaskStatus_ARealDatabaseErrorIsNotAFenceRejection(t *testing.T) {
	h, _ := newStatusHandler(t, "running", 0, 0,
		errors.New(`ERROR: could not serialize access due to concurrent update (SQLSTATE 40001)`))
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	h.handleTaskStatus(context.Background(), statusWorkerID(), lim,
		statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))

	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"a REAL database error is a different arm with a different meaning. A record() placed above the "+
			"errors.Is check counts every database error and makes the number unreadable: the whole "+
			"value of this section is that it means the FENCE refused something.")
	require.Contains(t, logged(), "handleTaskStatus UpdateTaskStatus",
		"fixture: the other arm still logs, so this test is exercising it rather than falling through")
}
```

Add to that file's imports: `"context"`, `"errors"`, `"fmt"`, `"relay/internal/events"`,
`relayv1 "relay/internal/proto/relayv1"`, `"relay/internal/store"`,
`"github.com/jackc/pgx/v5"`, `"github.com/jackc/pgx/v5/pgconn"`, `"github.com/jackc/pgx/v5/pgtype"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worker/ -run "TestHandleTaskStatus_The|TestHandleTaskStatus_AReal" -v -timeout 60s`
Expected: FAIL. The three counter assertions report `TaskStatusFenceCounts{}` where a non-zero field is
wanted, because nothing calls `record` yet. (The stub, the fixtures and the positive markers all pass - only
the counters are red, which is the RED this task is for.)

- [ ] **Step 3: Write minimal implementation**

In `internal/worker/handler.go`, replace the retry-arm error handling (currently `:1137-1146`) so the branch
reads:

```go
		if _, err := h.q.IncrementTaskRetryCount(ctx, store.IncrementTaskRetryCountParams{
			ID:              taskID,
			AssignmentEpoch: int32(upd.Epoch),
			WorkerID:        workerID,
		}); err != nil {
			// TWO ARMS, MUTUALLY EXCLUSIVE BY CONSTRUCTION, and written as
			// if/else so that no future edit can make both fire. They are the
			// subjects of two separate backlog items and neither number covers
			// any part of the other.
			if errors.Is(err, pgx.ErrNoRows) {
				// The fence rejecting, not a failure: the task finished, was
				// cancelled, or the generation ended between the GetTask above
				// and here. COUNTED, NEVER LOGGED - a line here would be
				// caller-driven volume on the recv goroutine and would fire on
				// the legitimate duplicate-terminal case. The reason is
				// classified from the row this handler already read; see
				// classifyStatusFenceRejection for what that can and cannot
				// establish.
				h.statusFence.record(classifyStatusFenceRejection(task.Status, statusStr))
			} else if lim.allow(logKey{kind: kindStatusRetryWrite}) {
				// Real infrastructure - a serialization failure, a statement
				// timeout, a connection reset - and now under the connection's
				// budget. It runs on the recv goroutine at whatever rate the
				// agent chooses to send, and the read above (GetTask) can
				// succeed while this write fails, so nothing else bounds it.
				// NO WIRE VALUE IN THE KEY: such an episode is not per-task, and
				// keying on the task id would multiply one infra event by the
				// task count - the same argument the GetTask site above makes.
				log.Printf("worker: handleTaskStatus IncrementTaskRetryCount %s: %v", uuidStr(taskID), err)
			}
		} else {
```

(the existing `else` body - `updateJobStatusFromTasks` and `NotifyTaskSubmitted` - and its comment are
unchanged, as is the trailing `return`.)

Replace the update-arm error handling (currently `:1182-1193`):

```go
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The fence rejecting, not a failure: the row is already terminal (a
			// duplicate terminal message, or a task the coordinator's stale-task
			// watchdog already ended), or the generation ended between the
			// GetTask above and here. COUNTED, NEVER LOGGED, for the reason at
			// the retry arm above.
			//
			// NOTE WHICH PREDICATE ACTUALLY BITES HERE, because the obvious
			// reading is wrong: the watchdog does NOT bump assignment_epoch -
			// UpdateTaskStatus writes only status, started_at and finished_at -
			// so a swept task's own agent still passes BOTH Go gates and is
			// refused by the terminality predicate, at the same epoch. That is
			// why the reasons split on the ROW'S STATUS rather than on the
			// statement.
			h.statusFence.record(classifyStatusFenceRejection(task.Status, statusStr))
		} else if lim.allow(logKey{kind: kindStatusUpdateWrite}) {
			log.Printf("worker: handleTaskStatus UpdateTaskStatus %s -> %s: %v", uuidStr(taskID), statusStr, err)
		}
		return
	}
```

**Note:** the `lim.allow` calls will not compile until Task 5 declares the two kinds. Declare them now as a
minimal edit in `ingest_log_limiter.go` (inside the sentinel run, before `kindCount`) and leave the full
checklist to Task 5:

```go
	kindStatusRetryWrite                   // handleTaskStatus's non-ErrNoRows IncrementTaskRetryCount failure
	kindStatusUpdateWrite                  // handleTaskStatus's non-ErrNoRows UpdateTaskStatus failure
	kindStatusFailDependents               // handleTaskStatus's FailDependentTasks failure
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worker/ -run "TestHandleTaskStatus_The|TestHandleTaskStatus_AReal" -v -timeout 60s`
Expected: PASS.

Run: `go test ./internal/worker/ -timeout 300s`
Expected: FAIL on `TestIngestLogKindsAreADenseRunFromOne`, `TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished`
and `TestIngestLogCounters_EveryKindIsPublishedDistinctly` - **the three shipped guards firing on three new
kinds, exactly as designed.** Task 5 closes them. Record the messages; if any of the three is silent here,
that is a finding to report, not to fix.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go internal/worker/ingest_log_limiter.go internal/worker/taskstatus_fence_counters_test.go
git commit -m "feat(worker): count handleTaskStatus fence rejections by reason at both write arms"
```

---

## Task 4: the concurrency proof

**Files:**
- Modify: `internal/worker/taskstatus_fence_counters_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append:

```go
// TestTaskStatusFenceCounters_ConcurrentRejectionsAreExact is what makes
// "atomics, not a mutex" a checked decision rather than a comment: in production
// every connection has its own recv goroutine and they all write these three
// words.
//
// EACH GOROUTINE HAS ITS OWN Handler-FREE FIXTURE? NO - deliberately the
// opposite. They share ONE Handler, because that is the production shape (one
// Handler per process, one limiter per connection) and it is the only
// arrangement in which the mutation this test exists for is observable.
//
// WHAT KILLS WHAT, and the halves are not equally strong. The mutation is
// statusFenceCounters.n changed from atomic.Uint64 to a plain uint64 with `++`,
// WITH the .Load() calls in snapshot dropped to match - leave them in and the
// "kill" is a compile error, which measures nothing. The -race half kills
// through happens-before analysis and does not need true parallelism; the
// exactness half only catches a lost update when two goroutines interleave
// inside the read-modify-write and is inert at GOMAXPROCS=1. Both are live in
// CI, which runs `go test -race ./...` on 2-4 vCPUs. Record measured figures in
// this comment during the mutation matrix run (M6).
func TestTaskStatusFenceCounters_ConcurrentRejectionsAreExact(t *testing.T) {
	h, _ := newStatusHandler(t, "timed_out", 0, 0, pgx.ErrNoRows)
	const goroutines, each = 8, 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One limiter per goroutine, exactly as there is one per connection.
			lim := newIngestLogLimiter(&h.ingestDrops)
			for i := 0; i < each; i++ {
				h.handleTaskStatus(context.Background(), statusWorkerID(), lim,
					statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
			}
		}()
	}
	wg.Wait()

	require.Equal(t, TaskStatusFenceCounts{Conflicting: goroutines * each}, h.TaskStatusFenceRejections(),
		"every rejection from every connection must land, and all of them under ONE reason. A plain "+
			"uint64 here loses counts silently and is a data race -race can see.")
}
```

**Note on the stub's own safety:** `stubStatusDB.calls` is appended from every goroutine. Guard it, or the
test races on the fixture rather than on the subject. Add a `sync.Mutex` to `stubStatusDB` protecting
`calls` **only**, with a comment saying the mutex is fixture bookkeeping and protects nothing the subject
reads - `task` and `writeErr` are written once before any goroutine starts.

Add `"sync"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worker/ -run TestTaskStatusFenceCounters_Concurrent -v -timeout 60s`
Expected: PASS on first run (the implementation is already correct). **This test's RED is a mutation, not a
missing implementation** - it is proven in M6 of the mutation matrix, not here. Say so in the commit message
rather than pretending it went red.

- [ ] **Step 3: Add the fixture mutex**

```go
type stubStatusDB struct {
	task     store.Task
	writeErr error
	execErr  error

	// mu protects calls ONLY. It is fixture bookkeeping: task, writeErr and
	// execErr are written once before any goroutine starts and are read-only
	// afterwards, so the subject under test acquires nothing this stub owns and
	// the no-new-lock constraint on the recv goroutine is not violated by the
	// production path.
	mu    sync.Mutex
	calls []string
}
```

with `note` taking `d.mu` and a `callsSnapshot()` helper the single-threaded tests use instead of reading
`d.calls` directly.

- [ ] **Step 4: Run the whole package under -race**

Run (Git Bash):
```bash
PATH=/c/msys64/mingw64/bin:$PATH CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/worker/ -run TestTaskStatusFence -timeout 300s
```
Expected: PASS, no `WARNING: DATA RACE`.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/taskstatus_fence_counters_test.go
git commit -m "test(worker): concurrent status-fence rejections are exact under -race"
```

---

## Task 5: three new log kinds, end to end, in ONE commit

Item 2's Done-When: *"Add the kind(s) through the full checklist above, in one commit, so no kind is ever
counted on the recv path and published under no JSON key."* This task spans `internal/worker` and
`internal/api` for exactly that reason.

**Files:**
- Modify: `internal/worker/ingest_log_limiter.go:127-147`
- Modify: `internal/worker/ingest_log_counters.go:61-67`, `:150-158`
- Modify: `internal/worker/handler.go:1196-1199` (the third site)
- Modify: `internal/worker/ingest_log_counters_test.go:34-61`
- Modify: `internal/worker/ingest_log_limiter_test.go:279-296`
- Modify: `internal/api/server_counters.go:383-399`
- Modify: `internal/api/server_counters_test.go:580-601`, `:820-831`, `:861-874`

- [ ] **Step 1: Write the failing test**

In `internal/worker/ingest_log_counters_test.go:34-46`, extend `run`:

```go
	run := []logKind{
		kindTaskLogPersist,
		kindBadTaskIDLog,
		kindBadTaskIDStatus,
		kindStatusGetTask,
		kindInventory,
		kindStatusRetryWrite,
		kindStatusUpdateWrite,
		kindStatusFailDependents,
	}
```

In `internal/worker/ingest_log_limiter_test.go:281-287`, extend the map:

```go
	kinds := map[string]logKind{
		"kindTaskLogPersist":       kindTaskLogPersist,
		"kindBadTaskIDLog":         kindBadTaskIDLog,
		"kindBadTaskIDStatus":      kindBadTaskIDStatus,
		"kindStatusGetTask":        kindStatusGetTask,
		"kindInventory":            kindInventory,
		"kindStatusRetryWrite":     kindStatusRetryWrite,
		"kindStatusUpdateWrite":    kindStatusUpdateWrite,
		"kindStatusFailDependents": kindStatusFailDependents,
	}
```

In `internal/api/server_counters_test.go`, rename `tenDistinctDrops` to `sixteenDistinctDrops` and give
every cell a distinct value:

```go
// sixteenDistinctDrops returns a fixed snapshot. SIXTEEN DISTINCT VALUES: the
// mapping from worker.IngestLogDrops into the response types is sixteen
// hand-written assignments, and equal values would hide a crossed one.
func sixteenDistinctDrops() worker.IngestLogDrops {
	return worker.IngestLogDrops{
		Deduped: worker.IngestLogDropsByKind{
			TaskLogPersist: 11, BadTaskIDLog: 22, BadTaskIDStatus: 33,
			StatusGetTask: 44, Inventory: 55,
			StatusRetryWrite: 111, StatusUpdateWrite: 122, StatusFailDependents: 133,
		},
		Suppressed: worker.IngestLogDropsByKind{
			TaskLogPersist: 66, BadTaskIDLog: 77, BadTaskIDStatus: 88,
			StatusGetTask: 99, Inventory: 110,
			StatusRetryWrite: 144, StatusUpdateWrite: 155, StatusFailDependents: 166,
		},
	}
}
```

Update the three call sites (`:719`, `:836`, `:925`), the kinds list at `:861`:

```go
	kinds := []string{
		"task_log_persist", "bad_task_id_log", "bad_task_id_status", "status_get_task", "inventory",
		"status_retry_write", "status_update_write", "status_fail_dependents",
	}
```

and add the six per-key assertions after `:874`:

```go
	assert.Equal(t, float64(111), body.IngestLogBudget.Counts.Deduped["status_retry_write"])
	assert.Equal(t, float64(122), body.IngestLogBudget.Counts.Deduped["status_update_write"])
	assert.Equal(t, float64(133), body.IngestLogBudget.Counts.Deduped["status_fail_dependents"])
	assert.Equal(t, float64(144), body.IngestLogBudget.Counts.Suppressed["status_retry_write"])
	assert.Equal(t, float64(155), body.IngestLogBudget.Counts.Suppressed["status_update_write"])
	assert.Equal(t, float64(166), body.IngestLogBudget.Counts.Suppressed["status_fail_dependents"])
```

and the six new leaves in `counterPayloadLeaves` (after `:591` and `:596` respectively):

```go
	"ingest_log_budget.counts.deduped.status_retry_write",
	"ingest_log_budget.counts.deduped.status_update_write",
	"ingest_log_budget.counts.deduped.status_fail_dependents",
```
```go
	"ingest_log_budget.counts.suppressed.status_retry_write",
	"ingest_log_budget.counts.suppressed.status_update_write",
	"ingest_log_budget.counts.suppressed.status_fail_dependents",
```

Add a new handler-layer test to `internal/worker/taskstatus_fence_counters_test.go`:

```go
// TestHandleTaskStatus_AWriteFailureFloodIsBoundedAndCountedPerSite is item 2's
// own Done-When: a flood of status updates whose write fails must produce at
// most the burst plus the refill rate of log lines, and every drop must be
// counted under the site that lost it.
//
// THE THREE SITES ARE ASSERTED SEPARATELY. One shared kind would have been one
// JSON key; three is the decision recorded in the logKind block, and this is
// what makes it checkable - a mutation that points two sites at one kind leaves
// one number at zero here.
func TestHandleTaskStatus_AWriteFailureFloodIsBoundedAndCountedPerSite(t *testing.T) {
	ctx := context.Background()
	dbErr := errors.New(`ERROR: canceling statement due to statement timeout (SQLSTATE 57014)`)

	// SITE 1: the UpdateTaskStatus arm.
	h, _ := newStatusHandler(t, "running", 0, 0, dbErr)
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)
	const flood = 100
	for i := 0; i < flood; i++ {
		h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	}
	require.Equal(t, 1, strings.Count(logged(), "handleTaskStatus UpdateTaskStatus"),
		"this kind carries NO wire value, so the flood is ONE key and one line. Before this slice it "+
			"was %d lines, one per message, at whatever rate the agent chose to send.", flood)
	got := h.IngestLogDropCounts()
	require.Equal(t, uint64(flood-1), got.Deduped.StatusUpdateWrite,
		"the %d messages folded into that one line must be COUNTED. Until this slice ingest_log_budget "+
			"read all zeros while these lines carried the volume - a control reporting zero is worse "+
			"than one reporting nothing.", flood-1)
	require.Zero(t, got.Deduped.StatusRetryWrite, "the count must be attributed to the RIGHT site")
	require.Zero(t, got.Deduped.StatusFailDependents)
	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"A REAL DATABASE ERROR IS NOT A FENCE REJECTION. The two arms are the subjects of two separate "+
			"items and no input executes both; this assertion is what keeps them disjoint.")

	// SITE 2: the retry arm.
	h, _ = newStatusHandler(t, "running", 3, 0, dbErr)
	lim = newIngestLogLimiter(&h.ingestDrops)
	logged = captureUnitLog(t)
	for i := 0; i < flood; i++ {
		h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	}
	require.Equal(t, 1, strings.Count(logged(), "handleTaskStatus IncrementTaskRetryCount"))
	require.Equal(t, uint64(flood-1), h.IngestLogDropCounts().Deduped.StatusRetryWrite)

	// SITE 3: FailDependentTasks, which is reached only AFTER a successful
	// UpdateTaskStatus and is NOT gated on pgx.ErrNoRows at all - it is an
	// :exec, so ErrNoRows is not a shape it can return.
	db := &stubStatusDB{
		task: store.Task{
			ID: statusTaskIDUUID(t), JobID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			Status: "running", WorkerID: statusWorkerID(), AssignmentEpoch: 7,
		},
		execErr: errors.New(`ERROR: deadlock detected (SQLSTATE 40P01)`),
	}
	h = &Handler{q: store.New(db), broker: events.NewBroker()}
	lim = newIngestLogLimiter(&h.ingestDrops)
	logged = captureUnitLog(t)
	for i := 0; i < flood; i++ {
		h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	}
	require.Equal(t, 1, strings.Count(logged(), "handleTaskStatus FailDependentTasks"),
		"the recursive CTE is the most expensive statement on this path and the first to deadlock under "+
			"contention; its line was outside the budget entirely")
	require.Equal(t, uint64(flood-1), h.IngestLogDropCounts().Deduped.StatusFailDependents)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/worker/ ./internal/api/ -timeout 300s`
Expected: FAIL to compile in `internal/api` (`worker.IngestLogDropsByKind` has no field `StatusRetryWrite`),
and in `internal/worker` `got.Deduped.StatusUpdateWrite` undefined.

- [ ] **Step 3: Write the implementation**

`internal/worker/ingest_log_limiter.go`, the const block (replacing the three stub lines from Task 3):

```go
const (
	kindTaskLogPersist  logKind = iota + 1 // handleTaskLog's non-ErrNoRows persist failure
	kindBadTaskIDLog                       // an unparseable task id on the LOG path
	kindBadTaskIDStatus                    // an unparseable task id on the STATUS path
	kindStatusGetTask                      // handleTaskStatus's non-ErrNoRows GetTask failure

	// kindInventory is handleInventoryUpdate's persist failure.
	kindInventory

	// THE THREE STATUS-WRITE KINDS ARE DELIBERATELY SEPARATE, AND ONE SHARED
	// KIND WOULD HAVE BEEN ONE JSON KEY INSTEAD OF THREE. Two things decided it:
	//
	//   - THEY ARE MUTUALLY EXCLUSIVE PER MESSAGE. The retry branch returns, the
	//     update branch returns, and FailDependentTasks runs only AFTER a
	//     successful UpdateTaskStatus. One message reaches at most one of them,
	//     so the split across three keys is a clean attribution rather than a
	//     mixture an operator has to decompose.
	//   - THE REMEDIES DIFFER. FailDependentTasks is a RECURSIVE CTE - the most
	//     expensive statement on this path and the first to hit a deadlock or a
	//     statement_timeout under contention - while UpdateTaskStatus is a
	//     single-row update. "Your recursive cascade is timing out" and "your
	//     simple updates are failing" are different incidents with different
	//     answers.
	//
	// The price is eight mechanical edits per kind, and it is paid down by the
	// three guards that fire on an omission (see this type's comment).
	kindStatusRetryWrite     // handleTaskStatus's non-ErrNoRows IncrementTaskRetryCount failure
	kindStatusUpdateWrite    // handleTaskStatus's non-ErrNoRows UpdateTaskStatus failure
	kindStatusFailDependents // handleTaskStatus's FailDependentTasks failure (an :exec; no ErrNoRows arm)

	// kindCount MUST STAY LAST and is NOT a kind. It is the length of
	// ingestLogCounters' array. A kind added after it is not counted at all;
	// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished is what makes
	// that a RED test rather than a silent hole.
	kindCount
)
```

Amend `ingestLogLimiter`'s type comment (`:5-84`) - append a paragraph:

```go
// WHAT THE BUDGET COVERS, so the next reader does not have to enumerate call
// sites to find out. EIGHT log sites on the gRPC receive path go through it:
// handleTaskLog's bad-id and persist lines, handleTaskStatus's bad-id, GetTask,
// retry-write, status-write and dependency-cascade lines, and
// handleInventoryUpdate's persist line. WHAT IS STILL OUTSIDE IT, and why:
// registration-time lines (the budget is allocated after
// authenticateAndRegister returns - bug-2026-08-15-registration-log-sites-are-
// outside-the-connection-budget) and markWorkerOffline's teardown line, which
// runs once per connection teardown and so is bounded by the connection caps
// rather than by message volume. handleTaskLog's marshal line is deliberately
// unbudgeted; see its own comment for the argument.
```

`internal/worker/ingest_log_counters.go`, extend `IngestLogDropsByKind`:

```go
type IngestLogDropsByKind struct {
	TaskLogPersist       uint64
	BadTaskIDLog         uint64
	BadTaskIDStatus      uint64
	StatusGetTask        uint64
	Inventory            uint64
	StatusRetryWrite     uint64
	StatusUpdateWrite    uint64
	StatusFailDependents uint64
}
```

and `byKind`:

```go
func (c *ingestLogCounters) byKind(arm int) IngestLogDropsByKind {
	return IngestLogDropsByKind{
		TaskLogPersist:       c.n[kindTaskLogPersist][arm].Load(),
		BadTaskIDLog:         c.n[kindBadTaskIDLog][arm].Load(),
		BadTaskIDStatus:      c.n[kindBadTaskIDStatus][arm].Load(),
		StatusGetTask:        c.n[kindStatusGetTask][arm].Load(),
		Inventory:            c.n[kindInventory][arm].Load(),
		StatusRetryWrite:     c.n[kindStatusRetryWrite][arm].Load(),
		StatusUpdateWrite:    c.n[kindStatusUpdateWrite][arm].Load(),
		StatusFailDependents: c.n[kindStatusFailDependents][arm].Load(),
	}
}
```

Also amend `IngestLogDrops`'s comment (`:25-30`): the `handleTaskStatus` example it uses is now half wrong -
the three write lines ARE budgeted; only the `pgx.ErrNoRows` `GetTask` case and the two fence arms are not,
and the fence arms are now counted in `task_status_fence`.

`internal/worker/handler.go`, the third site (`:1195-1199`):

```go
	if terminal {
		// UNDER THE CONNECTION'S BUDGET, and NOT gated on pgx.ErrNoRows: this is
		// an :exec, so ErrNoRows is not a shape it can return, and adding an
		// errors.Is here would be cargo-culted from the two arms above. It is
		// also NOT a fence-rejection site in task_status_fence's sense -
		// FailDependentTasks satisfies the epoch fence with a terminal-only
		// `WHERE status = 'pending'` predicate and yields no rowcount to inspect
		// (see the partition comment in internal/scheduler/dispatch.go).
		//
		// Reached only AFTER a successful UpdateTaskStatus, which is exactly the
		// condition the sibling item's Repro names: the read succeeds and the
		// WRITE fails, so the budgeted GetTask line above never spends a token
		// and nothing else bounds this one.
		if err := h.q.FailDependentTasks(ctx, taskID); err != nil &&
			lim.allow(logKey{kind: kindStatusFailDependents}) {
			log.Printf("worker: handleTaskStatus FailDependentTasks %s: %v", uuidStr(taskID), err)
		}
	}
```

`internal/api/server_counters.go`, extend `ingestLogKindCounts`:

```go
type ingestLogKindCounts struct {
	TaskLogPersist       uint64 `json:"task_log_persist"`
	BadTaskIDLog         uint64 `json:"bad_task_id_log"`
	BadTaskIDStatus      uint64 `json:"bad_task_id_status"`
	StatusGetTask        uint64 `json:"status_get_task"`
	Inventory            uint64 `json:"inventory"`
	StatusRetryWrite     uint64 `json:"status_retry_write"`
	StatusUpdateWrite    uint64 `json:"status_update_write"`
	StatusFailDependents uint64 `json:"status_fail_dependents"`
}

func ingestLogKindCountsFrom(k worker.IngestLogDropsByKind) ingestLogKindCounts {
	return ingestLogKindCounts{
		TaskLogPersist:       k.TaskLogPersist,
		BadTaskIDLog:         k.BadTaskIDLog,
		BadTaskIDStatus:      k.BadTaskIDStatus,
		StatusGetTask:        k.StatusGetTask,
		Inventory:            k.Inventory,
		StatusRetryWrite:     k.StatusRetryWrite,
		StatusUpdateWrite:    k.StatusUpdateWrite,
		StatusFailDependents: k.StatusFailDependents,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/worker/ ./internal/api/ ./cmd/relay-server/ -timeout 300s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/ internal/api/server_counters.go internal/api/server_counters_test.go
git commit -m "fix(worker): route handleTaskStatus's three database-error lines through the log budget"
```

---

## Task 6: publish the `task_status_fence` section

**Files:**
- Modify: `internal/api/server_counters.go` (source interface, `CounterSources`, response, handler)
- Modify: `internal/api/server_counters_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/server_counters_test.go`:

```go
// fakeTaskStatusFenceSource returns a fixed snapshot. THREE DISTINCT VALUES:
// equal values would hide a crossed field.
type fakeTaskStatusFenceSource struct{ c worker.TaskStatusFenceCounts }

func (f fakeTaskStatusFenceSource) TaskStatusFenceRejections() worker.TaskStatusFenceCounts { return f.c }

func threeDistinctStatusRejections() worker.TaskStatusFenceCounts {
	return worker.TaskStatusFenceCounts{Raced: 3, Duplicate: 41, Conflicting: 7}
}

// TestTaskStatusFenceSectionRestatesNothing guards the ANTECEDENT of the rule
// that produced TestIngestLogKindCountsPublishesEveryWorkerSideField, rather
// than the consequent: "a section that copies a subsystem's snapshot field by
// field needs a cardinality check" is satisfied here by not copying.
//
// internal/api imports internal/worker, so unlike the watchdog (where the import
// direction forced the type into THIS package) the PRODUCER owns the type and
// this package wraps it. Either way there is no mapper and no arity to drift.
func TestTaskStatusFenceSectionRestatesNothing(t *testing.T) {
	iface := reflect.TypeOf((*TaskStatusFenceSource)(nil)).Elem()
	require.Equal(t, 1, iface.NumMethod(), "one method; the reasoning below covers only the one")
	m, ok := iface.MethodByName("TaskStatusFenceRejections")
	require.True(t, ok)
	require.Equal(t, 1, m.Type.NumOut())

	section, ok := reflect.TypeOf(taskStatusFenceSection{}).FieldByName("Counts")
	require.True(t, ok, "taskStatusFenceSection must carry a Counts field")
	require.Equal(t, m.Type.Out(0), section.Type,
		"taskStatusFenceSection.Counts is %s and the source returns %s. THIS SECTION MUST NOT RESTATE "+
			"THE PRODUCER'S FIELDS. If a restatement is genuinely necessary, an arity assertion between "+
			"the two types must ship IN THIS COMMIT - see "+
			"TestIngestLogKindCountsPublishesEveryWorkerSideField, which exists because a fully correct "+
			"sixth log kind was counted on the recv path and published under no JSON key with all three "+
			"packages green.", section.Type, m.Type.Out(0))
}

func TestServerCounters_ReportsTheTaskStatusFenceSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{TaskStatusFence: fakeTaskStatusFenceSource{c: threeDistinctStatusRejections()}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		TaskStatusFence *struct {
			Counts map[string]any `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"task_status_fence"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.TaskStatusFence, "a wired section must be present")
	require.Nil(t, body.TaskStatusFence.Levels,
		"COUNTS ONLY. There is no current 'level' of discarded status reports.")

	// Key-set equality, not per-key assertions alone: a renamed key decodes as a
	// missing value and reads as zero.
	assert.ElementsMatch(t,
		[]string{"raced_total", "duplicate_total", "conflicting_total"},
		counterMapKeys(body.TaskStatusFence.Counts))
	assert.Equal(t, float64(3), body.TaskStatusFence.Counts["raced_total"])
	assert.Equal(t, float64(41), body.TaskStatusFence.Counts["duplicate_total"])
	assert.Equal(t, float64(7), body.TaskStatusFence.Counts["conflicting_total"])
	assert.NotContains(t, counterMapKeys(body.TaskStatusFence.Counts), "rejected_total",
		"THERE IS NO TOTAL, BY DECISION. The three keys partition the rejections, so a published total "+
			"would sit beside its own summands where it can only agree or be a bug.")
}

// TestServerCounters_WiredButZeroTaskStatusFenceSectionIsStillPresent. A server
// whose status fence has rejected nothing is the COMMON case and must still emit
// its section: zeros mean "this control ran and stopped nothing", absence means
// "not wired on this build".
func TestServerCounters_WiredButZeroTaskStatusFenceSectionIsStillPresent(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{TaskStatusFence: fakeTaskStatusFenceSource{}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.ElementsMatch(t, []string{"started_at", "task_status_fence"}, counterKeys(top),
		"a WIRED source whose counters are zero must still emit its section, and no OTHER section may "+
			"appear: each source is nil-able on its own")

	var section map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["task_status_fence"], &section))
	require.ElementsMatch(t, []string{"counts"}, counterKeys(section), "counts only; no levels half")

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(section["counts"], &fields))
	require.Len(t, fields, 3, "counts must be an object with the three keys, not an empty object")
	for k, v := range fields {
		assert.Equal(t, "0", string(v), "%s must serialise as an explicit zero, never be elided by omitempty", k)
	}
}
```

Add the three leaves to `counterPayloadLeaves` and the source to the bytes walk fixture at `:721`:

```go
	"task_status_fence.counts.raced_total",
	"task_status_fence.counts.duplicate_total",
	"task_status_fence.counts.conflicting_total",
```
```go
				TaskStatusFence: fakeTaskStatusFenceSource{c: threeDistinctStatusRejections()},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run "TaskStatusFence" -v -timeout 60s`
Expected: FAIL to compile - `undefined: TaskStatusFenceSource`, `undefined: taskStatusFenceSection`,
`CounterSources has no field TaskStatusFence`.

- [ ] **Step 3: Write the implementation**

In `internal/api/server_counters.go`, after `TaskLogFenceSource`:

```go
// TaskStatusFenceSource is whatever can report what handleTaskStatus's two
// epoch-fenced writes have refused - in production, *worker.Handler.
//
// ITS OWN SOURCE FIELD, not a widened TaskLogFenceSource and not a widened
// IngestLogBudgetSource. All three counters live on the same *worker.Handler and
// are wired together today, but they are independent CONTROLS counting different
// nouns, and an interface carrying two would make them appear and disappear
// together as a matter of TYPE rather than as a matter of wiring.
//
// A PRODUCER-OWNED STRUCT, AND THE DIRECTION IS FORCED. internal/api imports
// internal/worker (server.go), so the watchdog's shape - declare the snapshot
// type in this package - is a compile error here. The section therefore WRAPS
// worker.TaskStatusFenceCounts rather than restating its fields, which means
// there is no hand-written mapper on either side and no arity to drift.
// TestTaskStatusFenceSectionRestatesNothing holds that.
type TaskStatusFenceSource interface {
	TaskStatusFenceRejections() worker.TaskStatusFenceCounts
}
```

Add the field to `CounterSources`:

```go
	TaskStatusFence TaskStatusFenceSource
```

Add the response field to `serverCountersResponse`:

```go
	TaskStatusFence *taskStatusFenceSection `json:"task_status_fence,omitempty"`
```

Add the section type after `taskLogFenceCounts`:

```go
// task_status_fence is COUNTS ONLY, and it is THREE KEYS THAT PARTITION rather
// than one scalar. It counts status reports handleTaskStatus's epoch-fenced
// writes refused, split by what the row said when the handler read it:
//
//   - raced_total: the row was still writable at T0, so a concurrent writer
//     ended the generation inside the handler's own read-to-write window. THE
//     ONE KEY THAT IS A FLOOR - the Go-side identity and currency gates reject
//     stale and forged reports a round trip earlier, so only that narrow window
//     reaches the SQL fence's worker_id and assignment_epoch predicates.
//   - duplicate_total: the row was already terminal and its status is the one
//     being reported. THE EXPECTED HEALTHY BASELINE, whose height depends on
//     agent retry behaviour. A non-zero value here is not an incident.
//   - conflicting_total: the row was already terminal with a DIFFERENT status.
//     THE ACTIONABLE ONE, and the reason this section exists: a task the
//     coordinator's stale-task watchdog stamped `timed_out` whose agent then
//     reports `done` lands here. That is a successful task recorded as a
//     timeout, which is what RELAY_TASK_WATCHDOG_MARGIN set too small produces
//     and which had no runtime signal of any kind before this number.
//
// UNLIKE raced_total, THE OTHER TWO ARE EXACT rather than floors, and the
// asymmetry is worth knowing: nothing between GetTask and the write reads the
// row's status, so the terminality predicate has no Go-side pre-filter and every
// T0-terminal report that reaches the write is counted.
//
// THERE IS NO TOTAL, BY DECISION. The three partition the rejections, so a
// published sum would sit beside its own summands where it can only agree or be
// a bug.
//
// WHY THE ARMS ARE NOT SPLIT BY STATEMENT, which is the split the filing item
// proposed: IncrementTaskRetryCount and UpdateTaskStatus carry the IDENTICAL
// three predicates, which of the two runs is decided by the reported status and
// the row's retry budget rather than by anything about the rejection, and both
// mean the same thing to an operator - the agent's report of this task's outcome
// was discarded. Splitting by reason answers the question the item actually
// asked (which of these is alarming); splitting by statement does not.
//
// A FINER SPLIT - WHICH SQL PREDICATE FIRED - IS DECLINED WITH THE PRICE, NOT
// IMPOSSIBLE. Both statements yield no row on any predicate failure, so nothing
// can carry a reason; recovering one needs a second round trip (forbidden on the
// recv goroutine) or a rewrite of both result contracts. See task_log_fence
// above, where the same call was made and the same wording is required.
//
// AND WHAT IT DOES NOT COVER:
//
//   - IT IS NOT A CENSUS OF FENCE REJECTIONS. Dispatcher.failClaimedTask and
//     Watchdog.SweepOnce are fenced by the same statement and are counted
//     nowhere. This is the AGENT-REPORTED status path only.
//   - IT IS NOT COMPARABLE WITH task_log_fence.counts.rejected_total, which has
//     no equivalent Go-side pre-filter. No input moves both, and neither
//     explains any part of the other.
//   - IT DOES NOT RECONCILE WITH watchdog.counts.swept_total, and an operator
//     will try. The two are opposite ends of the same event seen from the
//     coordinator and from the agent, and they will not match: the watchdog also
//     sweeps tasks whose agents are gone and never report at all.
type taskStatusFenceSection struct {
	Counts worker.TaskStatusFenceCounts `json:"counts"`
}
```

Add the handler branch after the `TaskLogFence` branch:

```go
	if src := s.Counters.TaskStatusFence; src != nil {
		// ONE ASSIGNMENT INTO A WRAPPER, NOT A FIELD-BY-FIELD COPY: the
		// producer's type IS the counts half, so a reason added in
		// internal/worker reaches a JSON key with no edit here.
		resp.TaskStatusFence = &taskStatusFenceSection{Counts: src.TaskStatusFenceRejections()}
	}
```

Amend the endpoint's doc block (`:112-142`) so `IngestLogBudgetSource`'s and `TaskLogFenceSource`'s comments
say "three controls on one `*worker.Handler`" rather than two.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -timeout 300s`
Expected: PASS.

Run: `go test ./cmd/relay-server/ -timeout 300s`
Expected: **FAIL** on `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` with
`api.CounterSources has 5 source fields, so a build with every source wired must serve 6 top-level keys ...
This one served 5`. **This is the shipped completeness guard doing exactly its job** - the section is
renderable but nothing assigns the source. Task 7 closes it. If this test is GREEN here, that is a finding to
report, not to fix.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server_counters.go internal/api/server_counters_test.go
git commit -m "feat(api): a task_status_fence section wrapping the worker-owned counts"
```

---

## Task 7: wire it in `cmd/relay-server`

**Files:**
- Modify: `cmd/relay-server/http_server.go:175-178`
- Modify: `cmd/relay-server/counters_wiring_test.go` (a new section test)

- [ ] **Step 1: Write the failing test**

Append to `cmd/relay-server/counters_wiring_test.go`:

```go
// TestBuildHTTPServer_ServesTheWiredHandlersTaskStatusFenceSection is the
// third section fed by the agentHandler deps field, and it is checked the same
// way its two siblings are: through the real route, off a real buildHTTPServer.
//
// NO NEW wiredDep ROW. This section reuses an httpServerDeps field that already
// has one, so it inherits every main.go identifier check that row carries -
// which is exactly what countersAssignmentSources requires and why the
// assignment below must be spelled d.agentHandler.
func TestBuildHTTPServer_ServesTheWiredHandlersTaskStatusFenceSection(t *testing.T) {
	h := worker.NewHandler(nil, nil, worker.NewRegistry(), events.NewBroker(), func() {})
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: h,
	})

	top := countersAsAdmin(t, srv)
	raw, ok := top["task_status_fence"]
	require.True(t, ok, "a wired agentHandler must serve task_status_fence: %v", keysOfRaw(top))

	var section struct {
		Counts map[string]json.Number `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(raw, &section))
	require.ElementsMatch(t,
		[]string{"raced_total", "duplicate_total", "conflicting_total"},
		counterMapKeysNumber(section.Counts),
		"the three keys are the response contract; a rename here is operator-visible")
	for k, v := range section.Counts {
		require.Equal(t, "0", v.String(), "a fresh handler has refused nothing, so %s is an explicit zero", k)
	}

	_, hasIngest := top["ingest_log_budget"]
	_, hasLogFence := top["task_log_fence"]
	require.True(t, hasIngest && hasLogFence,
		"one agentHandler feeds THREE sections under one nil filter, because all three controls live on "+
			"that one object and neither exists without it")
}
```

Add a small helper beside `counterMapKeys` if one does not already exist for `map[string]json.Number`; if the
file already has an equivalent, use it rather than adding a second.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/relay-server/ -run "TaskStatusFence|EverySourceField" -v -timeout 120s`
Expected: FAIL - `a wired agentHandler must serve task_status_fence`, plus the cardinality failure carried
over from Task 6.

- [ ] **Step 3: Write the implementation**

In `cmd/relay-server/http_server.go`, inside the existing `if d.agentHandler != nil` block:

```go
	if d.agentHandler != nil {
		s.Counters.IngestLogBudget = d.agentHandler
		s.Counters.TaskLogFence = d.agentHandler
		s.Counters.TaskStatusFence = d.agentHandler
	}
```

Update that block's comment (`:168-178`): it says "TWO SECTIONS, ONE OBJECT". Now three, and the argument -
one nil filter is honest because all three controls live on one `*worker.Handler` and none exists without it
- is unchanged. Update `httpServerDeps.agentHandler`'s own comment (`:88-92`), which also says two.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/relay-server/ -timeout 300s`
Expected: PASS, including `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` (now 5 fields, 6 keys).

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/http_server.go cmd/relay-server/counters_wiring_test.go
git commit -m "feat(server): serve the task_status_fence section from the registered handler"
```

---

## Task 8: the prose this slice falsifies, in Go

**Files:**
- Modify: `internal/worker/handler.go:941-951`, `:1017-1049`

- [ ] **Step 1: Write the failing test**

There is no test for prose. **This step is a read, and it is not optional** - wrong prose about correct code
is this project's dominant defect class for fifteen consecutive iterations. Re-read `handler.go:941-951` and
`:1017-1049` and list every sentence this slice makes false. Expected list (R11):

1. `:948-951` - "Both pre-gate log lines below run AHEAD of the identity and currency gates, so the budget is
   the only thing bounding them". There are now five budgeted lines and three are *after* the gates.
2. `:1037-1040` - "It does NOT save a log line ... delete this gate and the log volume is unchanged". Still
   true about log volume, and now incomplete: deleting the gate changes what the COUNTERS count.

- [ ] **Step 2: Verify the claims are false**

Run: `grep -n "pre-gate log lines" internal/worker/handler.go` and read the surrounding block against the
five `lim.allow` call sites (`:976`, `:1007`, and the three added in Tasks 3 and 5).
Expected: the sentence says two; the code has five.

- [ ] **Step 3: Write the corrections**

Replace the `lim` paragraph at `:948-951`:

```go
// lim is this connection's log budget, allocated once in Connect. FIVE log lines
// in this function go through it and the split matters: the two above the gates
// (bad task id, GetTask failure) are bounded by the budget ALONE, because a gate
// cannot protect a line placed before it; the three below (retry write, status
// write, dependency cascade) are additionally behind the identity and currency
// gates, so reaching them costs a valid assignment. All five carry NO wire value
// in their dedupe key - each is exactly one key for the connection's whole life,
// re-armed by ingestLogDedupeWindow. See
// docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md.
//
// TWO OF THIS FUNCTION'S ARMS ARE COUNTED RATHER THAN LOGGED. Both epoch-fenced
// writes below drop pgx.ErrNoRows silently and record it in h.statusFence, which
// GET /v1/server/counters publishes as task_status_fence. That arm and the log
// budget are DISJOINT: no input moves both, and neither number covers any part
// of the other.
```

Append to the identity gate's comment, after the existing "Second, and this is the load-bearing reason"
paragraph at `:1051-1055`:

```go
	// FOURTH, AND NEW SINCE task_status_fence SHIPPED: this gate is what makes
	// the fence counters ATTRIBUTABLE. A non-assignee's forged report is dropped
	// here, one round trip before either write, so it never reaches a counter -
	// which is what stops a registered peer inflating conflicting_total by
	// naming tasks it does not own. Deleting the gate would leave the observable
	// TASK STATE unchanged (both statements reject on their own worker_id
	// predicate) and would make the counters peer-drivable noise. The numbers
	// are still not peer-KEYED - the cardinality rule holds either way - but a
	// signal an unrelated agent can move is not the signal an operator is
	// reading.
```

- [ ] **Step 4: Verify**

Run: `go build ./... && go test ./internal/worker/ -timeout 300s`
Expected: PASS.
Run: `grep -n "pre-gate log lines" internal/worker/handler.go`
Expected: no match.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go
git commit -m "docs(worker): correct handleTaskStatus's budget and identity-gate comments"
```

---

## Task 9: the SQL comments, and `make generate`

**Files:**
- Modify: `internal/store/query/tasks.sql:33-35`, `:117-118`
- Regenerate: `internal/store/tasks.sql.go` (doc comments only)

**NEVER edit `*.sql.go` or `models.go` directly.** This task edits the `.sql` and regenerates.

- [ ] **Step 1: Verify the claims are false**

Run: `grep -n "only log chunks go through ingestLogLimiter" internal/store/query/tasks.sql`
Run: `grep -n "drops pgx.ErrNoRows from both write sites silently" internal/store/query/tasks.sql`
Expected: one match each, at `:34` and `:117` respectively.

Then confirm the first is **already** false at HEAD:
Run: `grep -n "lim.allow" internal/worker/handler.go`
Expected: five matches in `handleTaskStatus`/`handleTaskLog` plus one in `handleInventoryUpdate` - so log
chunks are demonstrably not the only thing going through the limiter.

- [ ] **Step 2: Write the corrections**

At `tasks.sql:33-35`, replace the parenthetical:

```sql
--     is its own worker id, at its own epoch. AgentMessage_TaskStatus is itself
--     unbudgeted - ingestLogLimiter bounds LOG LINES, never messages, and it
--     never gated this dispatch - so an agent with timeout_seconds=60 emitting
```

At `tasks.sql:117-118`, replace:

```sql
-- with the other. It does NOT save a log line - handleTaskStatus drops
-- pgx.ErrNoRows from both write sites without logging, and COUNTS it instead:
-- h.statusFence records the rejection, classified by whether this row was still
-- writable when GetTask read it, and GET /v1/server/counters publishes the three
-- reasons as task_status_fence. THE COUNTER IS WHY THE GO GATE NOW MATTERS
-- BEYOND ITS ROUND TRIP: it is what keeps those numbers attributable to the
-- task's own assignee. The round-trip saving is still one statement instead of
-- two, since GetTask has already run before the gate. Do not delete either as
-- redundant with the other, but do not oversell the Go one either.
```

- [ ] **Step 3: Regenerate**

Run: `make generate`

Then the CRLF procedure (sqlc emits LF; this repo is CRLF, so it rewrites line endings across every
generated file):

```bash
git diff --ignore-all-space --stat
```

Expected: **exactly one file with real content changes, `internal/store/tasks.sql.go`, and the change is
comment text only.** Revert every LF-only file:

```bash
git status --porcelain internal/store/ | awk '{print $2}' | while read f; do
  if [ -z "$(git diff --ignore-all-space -- "$f")" ]; then git checkout -- "$f"; fi
done
```

- [ ] **Step 4: Verify no SQL statement changed**

```bash
git diff --ignore-all-space -- internal/store/ | grep -E "^[+-]" | grep -vE "^[+-]{3}" | grep -vE "^[+-]//" | grep -vE "^[+-]--"
```
Expected: **empty output.** Any line here is a statement change and this task did not intend one.

Run: `go build ./... && go test ./internal/store/... ./internal/worker/ -timeout 300s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go
git commit -m "docs(store): correct two false claims in UpdateTaskStatus's comment"
```

---

## Task 10: README, and the integration lane

**Files:**
- Modify: `README.md:1260-1302`
- Modify: `internal/worker/handler_taskstatus_integration_test.go:527`, `:655`

- [ ] **Step 1: Write the failing test**

In `internal/worker/handler_taskstatus_integration_test.go`, after the duplicate-terminal assertions at
`:566-579` in `TestHandleTaskStatus_ASecondTerminalFromTheAssigneeDoesNotOverwriteOrCascade`, add:

```go
	// THE COUNTER, AGAINST REAL POSTGRES. Every other assertion about
	// task_status_fence in this repo runs against a stub store.DBTX, so this is
	// the one place the classification is driven by a real fence rejection from
	// a real statement. The agent reported FAILED at a row that says `done`, so
	// the coordinator and the agent DISAGREE about this task's outcome and the
	// rejection is `conflicting`, not `duplicate`.
	fence := h.TaskStatusFenceRejectionsForTest()
	assert.Equal(t, uint64(1), fence.Conflicting,
		"a second terminal that CONTRADICTS the recorded one must be counted as conflicting: the agent "+
			"says failed and the row says done")
	assert.Zero(t, fence.Duplicate, "the statuses differ, so this is not a duplicate")
	assert.Zero(t, fence.Raced, "the row was already terminal when GetTask read it")
```

Add the same shape to `TestHandleTaskStatus_AssigneeCannotResurrectItsOwnCompletedTaskViaRetry` (`:655`),
which is the retry-statement mirror.

Add the export shim to `internal/worker/export_test.go`:

```go
// TaskStatusFenceRejectionsForTest exposes the fence counters to package
// worker_test. The production accessor is exported already; this exists only so
// the integration lane's assertions read the same way its siblings do.
func (h *Handler) TaskStatusFenceRejectionsForTest() TaskStatusFenceCounts {
	return h.TaskStatusFenceRejections()
}
```

**If `TaskStatusFenceRejections` is already exported and reachable from `worker_test`, use it directly and
add no shim.** An unnecessary shim is a second name for one thing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/worker/... -run "TestHandleTaskStatus_ASecondTerminal|TestHandleTaskStatus_AssigneeCannotResurrect" -v -timeout 900s`
Expected: FAIL to compile if a shim is needed, then FAIL on `Conflicting` before the increments are reached...
**it must PASS** if Tasks 1-3 landed correctly. Record which: if it passes immediately, say so - the RED for
these counters was Task 3's, and this task's value is that the classification is driven by real SQL rather
than by a stub. If it FAILS, the stub and Postgres disagree and that is a finding.

- [ ] **Step 3: Write the README**

Update the JSON block (`:1260-1286`) to add the three ingest keys and the new section:

```json
  "ingest_log_budget": {
    "counts": {
      "deduped":    { "task_log_persist": 4127, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 12,
                      "status_retry_write": 0, "status_update_write": 0, "status_fail_dependents": 0 },
      "suppressed": { "task_log_persist": 39984, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 0,
                      "status_retry_write": 0, "status_update_write": 0, "status_fail_dependents": 0 }
    }
  },
  "task_log_fence": {
    "counts": { "rejected_total": 0 }
  },
  "task_status_fence": {
    "counts": { "raced_total": 2, "duplicate_total": 118, "conflicting_total": 9 }
  },
```

Rewrite the `ingest_log_budget` bullet (`:1295`) - it currently names these three sites as outside the budget:

> - **Reading `ingest_log_budget`.** Agents can drive log volume, so the eight per-message log sites on the
>   gRPC receive path - the eight keys below - are rate-limited per connection; these are the lines that
>   limiter dropped, since `started_at`. **The budget covers those eight sites and no others.**
>   Registration-time lines (auto-enrollment, inventory replace at reconnect) and the teardown line
>   `markWorkerOffline` emits are outside it; the first two because the budget does not exist yet when they
>   run, the third because it fires once per connection teardown and so is bounded by the connection caps
>   rather than by message volume. **`status_retry_write`, `status_update_write` and `status_fail_dependents`
>   are new**: they are `handleTaskStatus`'s three database-error lines, which used to sit outside the budget
>   entirely, so a condition where the *read* succeeds and the *write* fails - a serialization failure, a
>   `statement_timeout`, a connection reset - drove one unbudgeted line per message at whatever rate an agent
>   chose to send, while every number in this section read zero. `status_fail_dependents` is worth its own
>   key because that statement is a recursive CTE, the most expensive on the path and the first to deadlock
>   under contention. [... the existing deduped/suppressed paragraphs unchanged ...]

Add the new section's bullets after the `task_log_fence` bullet (`:1297`):

> - **Reading `task_status_fence`.** These count status reports the coordinator refused to record since
>   `started_at`, split three ways by what the task row said when the handler read it. **`duplicate_total` is
>   the expected healthy baseline and its height depends on your agents' retry behaviour** - a terminal task
>   is deliberately not writable, so an agent that repeats a terminal message it already delivered is refused
>   by design and lands here. Do not treat a rising `duplicate_total` as an incident. **`conflicting_total`
>   is the actionable number**: the row was already final with a *different* outcome than the agent is
>   reporting. The signature to look for is a task the stale-task watchdog stamped `timed_out` whose agent
>   then reports `done` - **a successful task recorded as a timeout**, which is what
>   `RELAY_TASK_WATCHDOG_MARGIN` set too small produces and which had no runtime signal of any kind before
>   this number. Check it against `watchdog.counts.swept_by_worker` and against the sweep summary in the log.
>   **`raced_total`** is the narrow case where something ended the assignment between the handler's read and
>   its write - a cancel, a grace requeue, a job retry, a sibling replica - and is expected to move in bursts
>   around requeues and reconnects.
> - **Two of the three are exact; `raced_total` is a floor.** `handleTaskStatus` checks the sender's identity
>   and the assignment's generation *before* either write, so most stale or forged reports are dropped a
>   round trip earlier and never reach a counter - which is what makes `raced_total` an under-count and what
>   keeps all three attributable to the task's own assignee, so no unrelated agent can move them. Nothing
>   checks the row's *status* before the write, so `duplicate_total` and `conflicting_total` are complete
>   counts of that refusal.
> - **What `task_status_fence` does NOT cover.** It is not a census of fence rejections: the dispatcher's own
>   `failClaimedTask` and the watchdog's own sweep write are refused by the same statement and are counted
>   nowhere. It is **not comparable with `task_log_fence`**, which has no equivalent pre-filter - no input
>   moves both numbers and neither explains any part of the other. And **it will not reconcile with
>   `watchdog.counts.swept_total`**, though it is tempting to subtract them: the two are opposite ends of the
>   same event seen from the coordinator and from the agent, and the watchdog also sweeps tasks whose agents
>   are gone and never report at all. There is deliberately **no total** - the three keys partition the
>   rejections, so the sum is yours to compute and is not published beside its own parts.

- [ ] **Step 4: Verify**

Run: `go test ./... -timeout 300s`
Expected: PASS.
Run: `go test -tags integration -p 1 ./internal/worker/... -timeout 900s`
Expected: PASS (Docker Desktop must be running).

- [ ] **Step 5: Commit**

```bash
git add README.md internal/worker/handler_taskstatus_integration_test.go internal/worker/export_test.go
git commit -m "docs: task_status_fence and the three new ingest_log_budget keys"
```

---

## Task 11: gates and the mutation matrix

**Files:** none changed unless a mutation survives. **A mutation proof must leave a test behind** - if a
mutation survives, the fix is a permanent test with the discriminating input in it, not a reverted mutation.

- [ ] **Step 1: Run every gate**

```bash
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./... -timeout 300s
```
Expected: all green. Record the top-level pass count against the 626 baseline from slice 4.

```bash
PATH=/c/msys64/mingw64/bin:$PATH CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/worker/ ./internal/api/ ./cmd/relay-server/ -timeout 900s
```
Expected: green, no `WARNING: DATA RACE`. If Windows TSan is broken, reproduce the breakage against
`origin/main` before attributing it, then run in a `golang:1.26` Linux container.

```bash
go test -tags integration -p 1 ./internal/worker/... -timeout 900s
go test -tags integration -p 1 ./internal/api/... -timeout 900s
```

- [ ] **Step 2: Verify the tree**

```bash
git status --porcelain
git diff --stat origin/main
git diff --ignore-all-space --stat -- internal/store/
git diff --stat -- web/
```
Expected file set: `internal/worker/taskstatus_fence_counters.go`,
`internal/worker/taskstatus_fence_counters_test.go`, `internal/worker/handler.go`,
`internal/worker/ingest_log_limiter.go`, `internal/worker/ingest_log_counters.go`,
`internal/worker/ingest_log_counters_test.go`, `internal/worker/ingest_log_limiter_test.go`,
`internal/worker/export_test.go`, `internal/worker/handler_taskstatus_integration_test.go`,
`internal/api/server_counters.go`, `internal/api/server_counters_test.go`,
`cmd/relay-server/http_server.go`, `cmd/relay-server/counters_wiring_test.go`,
`internal/store/query/tasks.sql`, `internal/store/tasks.sql.go`, `README.md`.
**`git diff --stat -- web/` must be empty.** No migration, no proto, no `models.go`.

- [ ] **Step 3: Run the mutation matrix in an ISOLATED worktree**

Never mutate the shared tree - sibling agents read it. `git worktree add --detach <scratch>` first.

| # | Mutation | Must go RED |
| --- | --- | --- |
| M1 | `record` moved above the `errors.Is` check at the update arm (count every error) | `TestHandleTaskStatus_ARealDatabaseErrorIsNotAFenceRejection` |
| M2 | `classifyStatusFenceRejection` returns `fenceReasonDuplicate` unconditionally | `TestClassifyStatusFenceRejection`, both arm tests |
| M3 | `taskStatusIsWritable` returns `false` always | `TestClassifyStatusFenceRejection` (the raced rows), `TestTaskStatusWritableSetMatchesTheSQLAllowList` |
| M4 | `taskStatusIsWritable` adds `"done"` to the writable set | `TestTaskStatusWritableSetMatchesTheSQLAllowList` |
| M5 | `snapshot()` swaps the `Duplicate` and `Conflicting` reads | `TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly` (ordered, not ElementsMatch) |
| M6 | `statusFenceCounters.n` becomes a plain `[3]uint64` with `++`, **with `snapshot`'s `.Load()` dropped to match** | `TestTaskStatusFenceCounters_ConcurrentRejectionsAreExact` under `-race`. Record kill rates at `-cpu=1` and `-cpu=2` for both halves, plus an unmutated green baseline, and put the figures in that test's comment |
| M7 | the reason enum renumbered to `iota + 1` | `TestTaskStatusFenceReasonsAreADenseRunFromZero`, `..._EveryReasonIsPublishedDistinctly` |
| M8 | `record` deleted from the update arm | `TestHandleTaskStatus_TheUpdateArmCounts...` |
| M9 | `record` deleted from the retry arm | `TestHandleTaskStatus_TheRetryArmCounts...` |
| M10 | the update site's `logKey` kind changed to `kindStatusRetryWrite` | `TestHandleTaskStatus_AWriteFailureFloodIsBoundedAndCountedPerSite` (the per-site zero assertions) |
| M11 | `lim.allow` removed from the `FailDependentTasks` site (log unconditionally) | the same test's site-3 leg (`strings.Count == 1`) |
| M12 | a NINTH `logKind` added correctly on the worker side, published nowhere | `TestIngestLogKindCountsPublishesEveryWorkerSideField` |
| M13 | `s.Counters.TaskStatusFence` assignment deleted from `buildHTTPServer` | `TestBuildHTTPServer_EverySourceFieldProducesAServedSection`, `..._ServesTheWiredHandlersTaskStatusFenceSection` |
| M14 | `taskStatusFenceSection.Counts` retyped to a locally declared struct with the same three fields plus a hand-written mapper | `TestTaskStatusFenceSectionRestatesNothing` |
| M15 | `handleServerCounters`' `src != nil` dropped for this section | `TestServerCounters_OmitsUnwiredSections` |
| M16 | a fourth field (`SweptTruncated uint64`) added to `TaskStatusFenceCounts`, incremented nowhere | `TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly` (the arity require), `TestCounterPayloadCarriesNoIdentifiers` (the leaf contract). **This is the M12-of-slice-4 measurement: the field must reach a JSON key with NO mapper edit anywhere - that is the property D3 claims** |
| M17 | `classifyStatusFenceRejection` called with `task.Status` for both arguments | `TestHandleTaskStatus_TheUpdateArmCounts...` (the conflicting leg) |
| M18 | the budget's `&&` short-circuit inverted at the `GetTask` site (`lim.allow(...) && !errors.Is(...)`) | a fence rejection would then spend a token; assert it does not - if nothing goes RED, **add a permanent test**, do not revert quietly |
| M19 | `stubTaskRow.Scan`'s arity check deleted and a field dropped from the copy | the arm tests (the fixture stops owning the task, the identity gate rejects, and `db.calls` shows no write). Confirms the stub fails LOUDLY rather than silently mis-scanning |

- [ ] **Step 4: Record the results**

Any mutation that survives is a finding: write the discriminating test, place the poisoned input **first**
(a bad input placed last cannot detect an early-exit mutation), and keep the test permanently. Then delete
the scratch worktree.

- [ ] **Step 5: Commit any tests the matrix forced**

```bash
git add -A
git commit -m "test: close the mutation survivors found in the status-fence matrix"
```

---

## Phase 6 proposals (the conductor files; do not file these yourself)

1. **`Dispatcher.failClaimedTask`'s fence rejection is still uncounted** - `idea`, `low`. Slice 4 made it a
   ready site and this slice declined it on merit (D6): its target is `dispatched` by construction, so every
   rejection there is `raced` and a one-valued partition is not a partition; it counts a different noun (the
   dispatcher failing to record a terminal it decided, not an agent's report being discarded); and it would
   need a fifth `CounterSources` field for one number. The honest form is a plain total under its own
   `dispatch_fence` section, or nothing. Record that `task_status_fence` is explicitly **not** a census and
   says so in its own doc comment and in README.
2. **The status-writability mirror is a hand copy of a SQL allow-list** - `idea`, `low`.
   `taskStatusIsWritable` restates `tasks.sql`'s allow-list and is guarded by a regexp parse of the `.sql`
   file. That guard is the last rung of the ladder (match a shape). If a third consumer of that set ever
   appears, the right answer is to generate the set or move the classification into SQL, not a third copy.
3. **`internal/api`'s payload guards still run against fixtures, not producers** - amend the item slice 4
   recommended. `task_status_fence` is now the **fourth** section whose `internal/api` walks see only
   literals declared in `server_counters_test.go`; `cmd/relay-server`'s new section test is the only place
   real producer bytes reach the route, and it asserts key names and zeros rather than a producer-driven
   value.
4. **Amend `bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget`** - the site inventory
   is now **thirteen**, and `markWorkerOffline`'s teardown line (`handler.go:1590`, added by the
   finishRegister slice) belongs to neither of that item's two classes: it runs once per connection teardown
   with no `lim` on its call chain.

**Both items close.** `/backlog close idea-2026-08-21-handletaskstatus-fence-rejections-are-uncounted` and
`/backlog close bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget`, each with the
`git mv` into `docs/backlog/closed/`.

---

## Self-review

**Item 1 Done-When coverage.** Counter on a fence-rejected `UpdateTaskStatus` proven by a handler-layer test
across a rejection and a success, default lane (Task 3). Same for the retry arm (Task 3). Success establishes
acceptance positively via `RecomputeJobStatus`/`NotifyTaskCompleted`/`NotifyTaskSubmitted` in `db.calls`, not
by a shared projection (Task 3). Readable through the endpoint with unwired ABSENT and wired-but-zero PRESENT
(Task 6). No new log line, round trip, goroutine, queue or lock (Task 1's atomics decision; the arm tests
assert an empty captured log). One-versus-two decided with the reason recorded where the counters are
declared (D2, in `TaskStatusFenceCounts`'s comment). The healthy baseline documented (README, and the
`fenceReasonDuplicate` comment). Floor semantics documented, and refined - two of three keys are exact (R2,
README). The payload states what it does not cover and does not invite comparison with `task_log_fence`
(`taskStatusFenceSection`'s comment). `TestHandleTaskStatus_*` and the `internal/store` fence guards
unweakened (Task 11's gates).

**Item 2 Done-When coverage.** A write-failure flood bounded to burst-plus-refill, proven by a handler-layer
test RED against today's code (Task 5). Every drop counted and published, proven through
`GET /v1/server/counters` and by `TestIngestLogKindCountsPublishesEveryWorkerSideField` (Tasks 5, 11/M12).
The `!errors.Is` gate preserved and still short-circuiting before `allow` (D8, M18). One-versus-three decided
with the reason in the const block (D5). `:1443` decided explicitly (D7). `ingest_log_limiter.go`'s comment
says what the budget covers (Task 5). README's bullets updated (Task 10).
`TestConnect_TwoConnectionsDoNotShareTheLogBudget` and the ingest-counter tests unweakened (Task 11).

**Type consistency.** `taskStatusFenceReason` / `fenceReasonRaced|Duplicate|Conflicting|Count` /
`statusFenceCounters` (storage) / `TaskStatusFenceCounts` (published, worker) / `taskStatusFenceSection`
(wrapper, api) / `TaskStatusFenceSource` (interface, api) / `CounterSources.TaskStatusFence` /
`Handler.statusFence` / `Handler.TaskStatusFenceRejections()`. `kindStatusRetryWrite` /
`kindStatusUpdateWrite` / `kindStatusFailDependents` with fields `StatusRetryWrite` / `StatusUpdateWrite` /
`StatusFailDependents` and JSON keys `status_retry_write` / `status_update_write` /
`status_fail_dependents`, spelled identically in `worker.IngestLogDropsByKind`, `ingestLogKindCounts`,
`ingestLogKindCountsFrom`, `counterPayloadLeaves`, `sixteenDistinctDrops` and the two kind lists.

**No placeholders.** Every code step carries the code. Every verify step carries the command and the expected
result, including the two deliberate REDs (Task 3 Step 4's three guard failures, Task 6 Step 4's cardinality
failure) that are the shipped guards working.
