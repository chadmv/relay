# Task-log fence rejections: counted and served (silent-drop observability, slice 3 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Count every task-log chunk that `handleTaskLog`'s `pgx.ErrNoRows` fence arm drops, and serve
that one number as `task_log_fence.counts.rejected_total` on the admin-only `GET /v1/server/counters`.

**Architecture:** One `atomic.Uint64` value field on `*worker.Handler`, incremented in the existing
named `pgx.ErrNoRows` arm before its return; one exported scalar accessor satisfying a new
`api.TaskLogFenceSource`; a third `api.CounterSources` field fed by the `*worker.Handler`
`cmd/relay-server` already wires; and the `wiredDep` cardinality relation rewritten from "one row per
deps field" to "N sections over M deps fields", because two sections now share one object.

**Tech Stack:** Go 1.26, `sync/atomic`, `encoding/json`, `net/http`, `go/ast` (guards),
`testify/require`, sqlc-generated `internal/store` (read only - no SQL is touched).

---

## Phase 1 is collapsed into Phase 2, again

There is no separate spec document for this slice. The joint spec
`docs/superpowers/specs/2026-08-21-silent-drop-observability.md` (sections 3.2, 6.1, 7.3, 9, 10.3) plus
two rounds of amendments on `docs/backlog/idea-2026-08-14-tasklog-fence-rejection-is-unobservable.md`
(2026-08-15, 2026-08-21, 2026-08-21-later) carry the design, and this plan absorbs the spec role: the
verification section below re-derives every load-bearing claim from the tree at HEAD and records the
refutations. **Nothing upstream of this document is a contract.**

## Slice independence declaration

- **FRONTEND WORK: NONE.** Zero files under `web/`. The admin console's Server tab is the eventual
  consumer of this payload and is not in any of the four slices (joint spec section 14). Phase 3 has
  exactly one lane: backend.
- **Within this slice the tasks are SEQUENTIAL**, with one exception. Task 2 depends on Task 1's
  exported accessor; Task 3 depends on Task 2's `CounterSources` field; Task 4 depends on Task 3's
  wiring. **Task 5 (README) is independent of Tasks 1-4** and may be done at any point after Task 2
  fixes the payload's shape.
- **Task 2 deliberately leaves `cmd/relay-server` RED and does not commit.** Task 3 closes it. See the
  note in Task 2, Step 8: that red is the shipped guard firing exactly where its own comment predicted,
  and observing it is evidence, not an accident.

## Scope

**In:** `internal/worker/handler.go` (one field, one accessor, one `Add(1)`, comment amendments),
`internal/worker/tasklog_fence_counter_test.go` (new), `internal/api/server_counters.go`,
`internal/api/server_counters_test.go`, `cmd/relay-server/http_server.go`,
`cmd/relay-server/counters_wiring_test.go`, `cmd/relay-server/grpc_admission_e2e_integration_test.go`,
`README.md`.

**Out:** slice 4 (watchdog sweeps) - not implemented and not foreclosed; see "Slice 4 is not
foreclosed" below. No SQL, no migration, no proto, no `make generate`, no `internal/metrics` change, no
`web/`. **If any step of this plan appears to require `make generate`, that step is wrong** (joint spec
section 13).

---

## Verification against HEAD, with refutations

Every line reference below was read in worktree `pr-merge-session-961184` at `main` @ `4b97895`.

### Confirmed

- **C1. The arm exists, is named, is side-effect-free, and cites the item in source.**
  `internal/worker/handler.go:1110-1139`. The `if errors.Is(err, pgx.ErrNoRows)` arm is its own branch
  with its own `return`, and `:1130-1137` says "THIS ARM IS DELIBERATELY SIDE-EFFECT-FREE AND MUST STAY
  SILENT ... whose answer is a COUNTER, not a log line ... Pinned by
  TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll".
  (The joint spec's `handler.go:1084`/`:1114` line numbers have drifted by ~26 lines; the arms are now
  `:1110` and `:1140`.)
- **C2. The fence returns NO ROW on rejection.** `internal/store/query/tasks.sql:301-313`:
  ```sql
  WITH fence AS (
      SELECT t.job_id FROM tasks t
      WHERE t.id = sqlc.arg(task_id)
        AND t.assignment_epoch = sqlc.arg(assignment_epoch)
        AND t.worker_id = sqlc.arg(worker_id)
        AND (t.status IN ('pending', 'dispatched', 'running')
             OR t.finished_at > sqlc.arg(min_finished_at)::timestamptz)
  ), ins AS (
      INSERT INTO task_logs (task_id, stream, content)
      SELECT sqlc.arg(task_id), sqlc.arg(stream), sqlc.arg(content) FROM fence
      RETURNING id, created_at
  )
  SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence;
  ```
  A chunk failing any predicate matches no `fence` row, so `ins` inserts nothing and the final SELECT
  returns zero rows -> `pgx.ErrNoRows` (`internal/store/tasks.sql.go:157-169`). **There is no row on
  which a reason column could ride.**
- **C3. All three rejection meanings are live**, and only the third is legitimate: identity
  (`worker_id` as a NULL-rejecting `=`), currency (`assignment_epoch`), recency (the disjunction).
  The handler binds all three at `handler.go:1102-1109`, including `MinFinishedAt` from
  `h.TrailingLogWindow`.
- **C4. `metrics.Store` is still the wrong home.** `internal/metrics/store.go`: `Append` is a no-op for
  an untracked worker; `Clear` deletes the whole entry and is called from `handler.go`'s teardown when
  a worker goes offline. A cumulative counter there is destroyed by the disconnect that produced it.
  The refutation holds at HEAD.
- **C5. The read surface exists.** `GET /v1/server/counters` is registered `auth(admin(...))` as a
  direct statement of `Handler()`'s body, `internal/api/server.go:161`. `Server.Counters` is at
  `:45`, `startedAt` at `:54`.
- **C6. `IngestLogBudgetSource`'s comment names this item and demands a separate source field.**
  `internal/api/server_counters.go:119-129`.
- **C7. The wiring guard's predicted RED is exact.** `cmd/relay-server/counters_wiring_test.go:358`
  asserts `require.Len(t, distinct, reflect.TypeOf(api.CounterSources{}).NumField())`, and its comment
  at `:324-343` already names this slice: "server_counters.go says the fence counter will live on the
  SAME *worker.Handler with its own CounterSources field, so NumField goes to 3 while the natural table
  still has 2 rows."
- **C8. `counterPayloadLeaves` cannot catch a missing worker-side field.**
  `internal/api/server_counters_test.go:517-534` is an `ElementsMatch` over a list derived from the
  api-side response struct; it reddens on an EXTRA api leaf and is silent on a MISSING worker-side one.
- **C9. CI's lane is `go test -race ./... -timeout 180s` on `ubuntu-latest` with NO tag**, plus
  `make vet-integration` which only COMPILES integration-tagged files
  (`.github/workflows/go-ci.yml:26-34`). No `-cpu` flag.
- **C10. `internal/api` already imports `internal/worker`** (`server_counters.go:8`), so the import
  direction is free for this section.

### Refuted

**R1. "Per-reason splitting is structurally impossible."** (Joint spec section 3.2 and D6; the item's
2026-08-21 amendment; the Proposal's fourth bullet.) **The premise is right and the word is too
strong.** C2 proves there is no row to carry a reason *in this statement as written*, and the
one-round-trip constraint does forbid a second query (`handler.go:1057`: "Do not add a query, a
goroutine, or a queue here"). But a one-round-trip variant is expressible: replace the `fence` CTE with
a task CTE exposing the three predicates as booleans and LEFT JOIN the insert onto it, so the statement
returns a row on the rejection path too. That is rejected on COST, not on possibility:

- it deletes the `pgx.ErrNoRows` signal that every caller, every comment and every test of this fence
  is written against (`tasks.sql:220-229` states the contract in prose);
- it makes `AppendTaskLogRow`'s three columns nullable on the success path, so the publish path has to
  re-derive "did the insert happen" from a NULL check - a new way to publish an unstored chunk, which
  is the one thing the arm's comment forbids absolutely;
- it puts a rewrite of the most security-sensitive statement in the repo inside an observability slice.

**Consequence for the code:** the comment must say what is TRUE. Do not write "structurally
impossible". Write: the reason is not recoverable from this statement's result, and getting it needs
either a second round trip (forbidden here) or a rewrite of `AppendTaskLog`'s result contract (a bigger
and riskier slice than the number is worth). Wrong prose about correct code is this project's dominant
defect class for thirteen iterations; do not add a fourteenth in the comment that exists to stop
someone re-litigating this.

**R2. "This section needs its own CONCRETELY typed field on `httpServerDeps`."** (Item, 2026-08-21
later, pattern part 2.) **Refuted for this slice.** `httpServerDeps.agentHandler`
(`cmd/relay-server/http_server.go:80`) is already that field, already concretely typed
`*worker.Handler`, already nil-filtered at the boundary, and already the object the counter lives on.
A second field for the same object is actively worse: `counters_wiring_test.go:463` compares only
`depArg["agentHandler"]` against the identifier passed to `RegisterAgentServiceServer`, so a second
deps field could legitimately be fed a DIFFERENT `*worker.Handler` with every check green - the exact
"confident zero" the guard exists to prevent, re-created in order to satisfy a bullet. **Reuse
`agentHandler`.** Both sections then inherit the same-object check for free.

**R3. The `wiredDep` arithmetic must be re-expressed, not padded.** Reusing `agentHandler` makes the
shipped relation wrong: `distinct` = 2 (`grpcAdmission`, `agentHandler`) versus `NumField` = 3. That is
the predicted RED (C7). The two resolutions its own comment offers are "a second deps field for the
same handler" (refuted in R2) or "a cardinality relation that expresses N sections over M deps fields".
**Take the second.** `wiredDep` gains a `sections []string` naming the `api.CounterSources` fields that
row feeds, and the relation becomes: every row's field exists on `httpServerDeps`; every named section
exists on `api.CounterSources`; no section is named twice; distinct deps fields == number of rows (so a
duplicated row is RED outright, not merely uncounted); distinct sections == `NumField(CounterSources)`.
That is strictly stronger than what shipped, because the old check could be satisfied by any third deps
field without anyone naming which section it fed.

**R4. "Moving this counter needs Postgres, so the proof is integration-only."** (The natural read of
slice 2's precedent, and of the item's "this arm requires a real `AppendTaskLog` call".) **Refuted, and
this is the most useful finding in the plan.** `AppendTaskLog` is `q.db.QueryRow(...).Scan(...)`
returning the raw error (`internal/store/tasks.sql.go:157-169`) and `q.db` is a `store.DBTX` interface
of three methods (`internal/store/db.go:14-22`). A ~35-line stub `DBTX` whose row `Scan` returns
`pgx.ErrNoRows` drives the REAL fence arm with no container, and `internal/worker` already has the
default-lane precedent for calling the unexported handler directly
(`ingest_log_counters_test.go:463-485`, `h.handleTaskLog(...)` on a bare `&Handler{}`). **So the item's
headline Done-When - a handler-layer test reading the counter across a rejection and a success - runs
in the lane CI executes.** Slice 2 could not do this because its counter needed `Connect`'s message
loop; this one does not. What still needs the integration lane is only the `buildHTTPServer`
FORWARDING proof (see "Lanes" below). Two different questions; do not merge them.

**R5. "Package-level in `internal/worker`."** (Item Proposal, first bullet's 2026-08-21 parenthetical;
the "shape, settled" paragraph says "one process-wide `atomic.Uint64` in `internal/worker`".)
**Refuted on the same grounds slice 2 refuted it** (`ingest_log_counters.go:69-73`,
`ingest_log_counters_test.go:424-439`): a package-level counter makes every exact-count assertion in a
22-file package order-dependent on every other test in the binary, and it gives `CounterSources`
nothing to hold. Production has exactly one `Handler`, so a value field on `Handler` IS process-wide
there, and it is a property the wiring guard can check rather than one a `var` asserts. **Value field
on `Handler`, zero value ready, no nil case.** The item's "process-wide" intent is preserved; its
"package-level" mechanism is not.

**R6. "Its own typed-nil test."** (Item, 2026-08-21 later, pattern part 5.) **Partially refuted.** One
deps field means ONE nil filter and one `if` in `buildHTTPServer`; a second typed-nil test would be a
copied fixture asserting the same branch, and a guard that duplicates another guard is a maintenance
tax that reads as coverage. **Extend the shipped
`TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent` with one more `NotContains`
assertion** (an addition, never a weakening) and say why in its comment. Parts 1, 3, 4 and 6 of that
pattern are taken as written; part 6 (wired-but-zero) gets its own new test because it asserts a new
shape.

**R7. "If the source returns a bare `uint64` there is no restated struct and the arity rule does not
apply."** (Item, 2026-08-21 later, cross-package arity gap.) **CONFIRMED, and made enforceable rather
than assumed.** This section's source returns a scalar, so `internal/api` restates NO field owned by
`internal/worker`, there is no hand-written field-by-field mapper, and no `NumField` assertion is
required. **Stated explicitly, per the brief, rather than left ambiguous.** But "returns a scalar" is a
premise a later commit can silently break, so the antecedent gets a guard:
`TestTaskLogFenceSourceReturnsAScalar` reflects the interface method's return kind and its failure
message names `TestIngestLogKindCountsPublishesEveryWorkerSideField` as the thing that must ship in the
same commit if the return type ever becomes a struct.

**R8. README is about to become wrong in two places.** `README.md:1286` says a fence-rejected chunk
"contributes nothing to these numbers" (true of `ingest_log_budget`, and misleading once a section
exists that does count it), and `README.md:277` says a rejected chunk is dropped "with no error to the
agent and no line in the server log" (still true, now incomplete - it is the operator's only signal
that `RELAY_TASKLOG_TRAILING_WINDOW` is too small). Both are required scope in Task 5.

**R9. Lead 5 (payload shape) settled by reading both walks and the one scalar loop.**
`TestServerCounters_WiredButZeroSectionIsStillPresent`
(`internal/api/server_counters_test.go:98-127`) loops each half's fields and asserts each serialises as
`"0"`; the ingest twin at `:808-848` says in as many words "Do not copy that loop" because its `counts`
half contains OBJECTS. **`task_log_fence.counts` is scalars, so the simple loop is correct here** - it
is the only other section for which that is true. The type walk (`:563-637`) recurses struct -> struct
-> `uint64` and the JSON walk (`:644-701`) recurses object -> object -> number; a one-field `counts`
object satisfies both walks' `NotEmpty` guards. **No `counterPayloadExemption` is needed or wanted**:
the only leaf is a plain `uint64`.

**R10. The response contract is adopted verbatim.** Joint spec section 9 fixed
`{"task_log_fence": {"counts": {"rejected_total": N}}}`, counts only, no levels. Nothing here reshapes
a shipped payload.

### Lead 3 (the caller must not learn why) - confirmed, with the timing argument stated

Nothing in this slice returns anything to an agent. The arm still `return`s with no send, no error and
no status; the only read path is an admin-authenticated HTTP route on `:8080`, and the gRPC service has
exactly one RPC, which this slice does not touch. **Timing:** the added work is one locked
exchange-add on the rejection path only. It sits immediately after a Postgres round trip that both the
accept and reject paths pay, and the ACCEPT path then does strictly more work (a broker map lookup, and
on a subscribed task a marshal plus a publish). So the reject path remains the CHEAPER one by orders of
magnitude, exactly as at HEAD, and the delta is not a discriminating signal a prober could use.

### Lead 4 (no log line) - confirmed, and the default lane gains a guard it did not have

The "no log line" argument holds unchanged: caller-driven volume on the recv goroutine; it would fire
on the legitimate late-flush case, which is the one an operator most wants quiet; and a *budgeted* line
would spend a token from a six-per-minute bucket a genuine infra failure needs
(`internal/worker/ingest_log_limiter.go:133-143`). **This slice adds none.**
`TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` asserts the whole captured log is empty and is
integration-tagged; Task 1's default-lane test asserts the same emptiness across its rejections, so for
the first time CI itself can see that property.

---

## File structure

| File | Change | Responsibility |
| --- | --- | --- |
| `internal/worker/handler.go` | modify `:140-190` (field + accessor) and `:1110-1139` (the arm) | owns the counter and the increment |
| `internal/worker/tasklog_fence_counter_test.go` | **create** | default-lane proof: the counter moves on a rejection and only on a rejection |
| `internal/api/server_counters.go` | modify `:113-157` (source interface, `CounterSources`), `:159-163` (response), add section types, modify `:243-267` (handler) | owns the payload contract for the section |
| `internal/api/server_counters_test.go` | modify `:517-534` (leaves), `:644-657` (fixture), append new tests | owns the section's shape guards |
| `cmd/relay-server/http_server.go` | modify `:50-80` (comment) and `:134-136` (wiring) | owns the wiring boundary and the typed-nil filter |
| `cmd/relay-server/counters_wiring_test.go` | modify `:319-363`, `:587-599`, `:601-609` | owns the "main wired what it registered" guard and its cardinality relation |
| `cmd/relay-server/grpc_admission_e2e_integration_test.go` | append one test + one helper | owns the executed FORWARDING proof (integration lane) |
| `README.md` | modify `:277`, `:1260-1276`, `:1286`, add one bullet | owns the operator-facing contract |

**No new file in `internal/worker`.** Slice 2's `ingest_log_counters.go` exists to hold a type, an
array, `record`, `snapshot`, `byKind` and two exported structs. This slice has one field and one
one-line accessor; a file containing only prose about a field declared in another file is prose that
will drift from its subject. Field and accessor go next to their ingest twins in `handler.go`.

## Lanes, stated rather than implied

| Question | Guard | Lane |
| --- | --- | --- |
| Does the arm increment, and only the arm? | `TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot`, `TestHandleTaskLog_ARealPersistFailureIsNotAFenceRejection` | **DEFAULT** (CI runs it) |
| Is the number safe under concurrent connections? | `TestTaskLogFenceRejections_ConcurrentRejectionsAreExact` | **DEFAULT**, `-race` |
| Does the payload carry the section, with the right shape, present-when-zero and absent-when-unwired? | four `internal/api` tests + both payload walks | **DEFAULT** |
| Does `buildHTTPServer` produce the section from a wired handler? | `TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection` | **DEFAULT** |
| Does main pass the handler it registered? | `TestServerCountersIsWiredByMain` | **DEFAULT** (syntactic) |
| Does `buildHTTPServer` FORWARD the handler it was GIVEN for THIS section? | `TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers` | **INTEGRATION ONLY.** CI compiles it (`make vet-integration`) and never runs it. |

The last row is the same honest limit slice 2 recorded. Moving this counter through a real
`buildHTTPServer` needs `Connect`'s message loop, which is past `authenticateAndRegister`, which is a
Postgres round trip. **Do not pile an AST fallback on top of an executed check to hide the gap** - that
is the rung slice 2 deliberately climbed off.

---

## Task 1: count the rejection

**Files:**
- Modify: `internal/worker/handler.go:171-190` (field and accessor) and `:1110-1139` (the arm)
- Test: `internal/worker/tasklog_fence_counter_test.go` (create)

**Why the RED is behavioural and not a compile error.** The observed behaviour is new, so the
observation surface has to be new too; a test written directly against a symbol absent at HEAD goes
"red" by failing to compile, and the test that later goes green is then not the test that was red.
Step 1 therefore lands the accessor with HEAD BEHAVIOUR (the counter exists and nothing increments it),
Step 2 writes the test, Step 3 observes `0 != 1` - a real assertion failure - and Step 4 adds the one
line that fixes it. The test body is byte-identical either side of the fix.

- [ ] **Step 1: add the counter field and accessor, with NO increment anywhere**

In `internal/worker/handler.go`, immediately after the `ingestDrops` field (`:181`), inside the
`Handler` struct:

```go
	// taskLogFenceRejects counts chunks handleTaskLog dropped because
	// AppendTaskLog's fence returned pgx.ErrNoRows. A VALUE, not a pointer, for
	// the same reason ingestDrops is: the zero value works, so a bare &Handler{}
	// in a test has a working counter and there is no nil case anywhere. Read
	// through TaskLogFenceRejections; wired to GET /v1/server/counters by
	// cmd/relay-server's buildHTTPServer under its OWN section and its OWN
	// CounterSources field.
	//
	// A DIFFERENT NOUN FROM ingestDrops, and neither number covers any part of
	// the other. ingestDrops counts LOG LINES THE BUDGET DROPPED; this counts
	// CHUNKS THE FENCE REJECTED, on an arm that never consults the budget at all.
	// No input moves both. Do not sum them and do not merge the sections.
	//
	// NOT IN metrics.Store, AND THE REASON IS MECHANICAL RATHER THAN STYLISTIC.
	// That type's Append is a no-op for an untracked worker, and its Clear
	// DELETES the whole entry when a worker goes offline (internal/metrics, called
	// from this file's teardown), so a cumulative rejection counter parked there
	// is destroyed by the very disconnect that produced the rejections: a worker
	// that floods and then drops leaves zero behind. The Metrics WIRING pattern -
	// an exported thing main sets, nil-checked at every use - is the right
	// precedent and is what api.CounterSources uses. metrics.Store is the wrong
	// HOME and must not gain a counter method.
	taskLogFenceRejects atomic.Uint64
```

And immediately after `IngestLogDropCounts` (`:190`):

```go
// TaskLogFenceRejections reports how many task-log chunks this server's
// AppendTaskLog fence has rejected since process start, across every worker and
// all three rejection reasons.
//
// It satisfies api.TaskLogFenceSource. ONE NUMBER, AND THE REASON IS NOT
// AVAILABLE - see the pgx.ErrNoRows arm in handleTaskLog for why, and do not add
// a second query to find out. Per PROCESS, monotonic, zeroed by a restart, and
// never returned to an agent: the only read path is the admin-authenticated
// GET /v1/server/counters.
func (h *Handler) TaskLogFenceRejections() uint64 { return h.taskLogFenceRejects.Load() }
```

`sync/atomic` is already imported in this file (`taskLogPublishes atomic.Int64`, `:1038`). Add nothing
else. **Do not touch the arm yet.**

- [ ] **Step 2: write the failing test**

Create `internal/worker/tasklog_fence_counter_test.go`:

```go
package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubFenceDB is the narrowest store.DBTX that makes AppendTaskLog return a
// chosen result WITHOUT Postgres, which is what puts this proof in the lane CI
// actually runs (go-ci: `go test -race ./...`, no tag, no container).
//
// IT WORKS BECAUSE AppendTaskLog IS ONE QueryRow PLUS ONE Scan and returns the
// raw error (internal/store/tasks.sql.go). The fence arm under test needs a REAL
// AppendTaskLog call - unlike handleTaskLog's bad-id arm, which returns before
// h.q is touched - and "a real call" is not the same requirement as "a real
// database". Nobody had checked which one it was.
//
// Exec and Query panic rather than returning a plausible zero value: handleTaskLog
// is documented as exactly one statement, so a second one must fail loudly here.
type stubFenceDB struct {
	err   error
	calls atomic.Int64
}

func (d *stubFenceDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("stubFenceDB: handleTaskLog performs no Exec - it is one statement, AppendTaskLog")
}

func (d *stubFenceDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("stubFenceDB: handleTaskLog performs no Query - it is one statement, AppendTaskLog")
}

func (d *stubFenceDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	d.calls.Add(1)
	return stubFenceRow{err: d.err}
}

type stubFenceRow struct{ err error }

// Scan fills AppendTaskLogRow BY DESTINATION TYPE rather than by position, so a
// reordered column list cannot make a success look like a failure or vice versa.
func (r stubFenceRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for _, d := range dest {
		switch v := d.(type) {
		case *int64:
			*v = 1
		case *pgtype.Timestamptz:
			*v = pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true}
		case *pgtype.UUID:
			*v = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
		}
	}
	return nil
}

// A well-formed uuid: the id must PARSE, or handleTaskLog returns on the bad-id
// arm and never reaches the fence.
const fenceTaskID = "3f1c0a2e-7b64-4d8a-9c31-0e5b6a7d8c90"

func newFenceHandler(err error) (*Handler, *stubFenceDB) {
	db := &stubFenceDB{err: err}
	return &Handler{q: store.New(db), broker: events.NewBroker()}, db
}

func fenceWorkerID() pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{9}, Valid: true} }

func fenceChunk() *relayv1.TaskLogChunk {
	return &relayv1.TaskLogChunk{TaskId: fenceTaskID, Content: []byte("x"), Epoch: 7}
}

// TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot is the item's own
// Done-When: read the counter across a rejection AND across a success.
//
// EACH LEG IS ASSERTED IMMEDIATELY AFTER IT RUNS, deliberately: a battery that
// only checks the total at the end cannot tell "the success incremented" from
// "the second rejection did not", and a poisoned input observed only at the end
// cannot detect an early-exit mutation.
func TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot(t *testing.T) {
	ctx := context.Background()
	h, db := newFenceHandler(pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	require.Zero(t, h.TaskLogFenceRejections(), "fixture: nothing has been rejected yet")

	h.handleTaskLog(ctx, fenceWorkerID(), lim, fenceChunk())
	require.Equal(t, uint64(1), h.TaskLogFenceRejections(),
		"a chunk the fence rejected must be COUNTED. Until this number existed an operator who set "+
			"RELAY_TASKLOG_TRAILING_WINDOW too small got silently truncated task output with no runtime "+
			"signal of any kind.")

	h.handleTaskLog(ctx, fenceWorkerID(), lim, fenceChunk())
	require.Equal(t, uint64(2), h.TaskLogFenceRejections(),
		"the counter ACCUMULATES: an Add, never a Store, and never a boolean flag")

	assert.Equal(t, "", logged(),
		"the arm must stay side-effect-free apart from the count: NO log line of any wording, including "+
			"a budgeted one. Its integration twin is TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll; "+
			"this is the half CI can see.")
	drops := h.IngestLogDropCounts()
	require.Zero(t, drops.Deduped.TaskLogPersist,
		"DIFFERENT NOUNS: this arm never consults the log budget, so a fence rejection must not appear "+
			"in ingest_log_budget's numbers")
	require.Zero(t, drops.Suppressed.TaskLogPersist)

	// SUCCESS MUST NOT COUNT, on the SAME handler whose counter has already moved:
	// a counter that increments unconditionally passes a fresh-handler check.
	db.err = nil
	h.handleTaskLog(ctx, fenceWorkerID(), lim, fenceChunk())
	require.Equal(t, uint64(2), h.TaskLogFenceRejections(),
		"an ACCEPTED chunk must not be counted as a rejection. This number is what an operator reads as "+
			"'output is being dropped'; incrementing it on the happy path makes it noise.")

	require.Equal(t, int64(3), db.calls.Load(),
		"exactly one DB round trip per chunk, unchanged. The standing constraint on this handler is one "+
			"statement and nothing else; a counter that needed a lookup would break it.")
}

// TestHandleTaskLog_ARealPersistFailureIsNotAFenceRejection is the poisoned input
// placed FIRST in its own test, and it is what kills an increment written above
// the errors.Is check.
func TestHandleTaskLog_ARealPersistFailureIsNotAFenceRejection(t *testing.T) {
	h, _ := newFenceHandler(errors.New(`ERROR: invalid byte sequence for encoding "UTF8" (SQLSTATE 22021)`))
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	h.handleTaskLog(context.Background(), fenceWorkerID(), lim, fenceChunk())

	require.Zero(t, h.TaskLogFenceRejections(),
		"a REAL persist failure is a different arm with a different meaning. An increment placed above "+
			"the errors.Is check counts every database error and makes the number unreadable: the whole "+
			"value of task_log_fence is that it means the FENCE refused something.")
	require.Contains(t, logged(), "handleTaskLog AppendTaskLog",
		"fixture: the other arm still logs, so this test is exercising it rather than falling through")

	drops := h.IngestLogDropCounts()
	require.Zero(t, drops.Deduped.TaskLogPersist,
		"fixture: a fresh limiter has tokens, so that line was ALLOWED - an allowed line is not a drop. "+
			"Asserted so that a change routing fence rejections through the budget is visible here.")
	require.Zero(t, drops.Suppressed.TaskLogPersist)
}

// TestTaskLogFenceRejections_TwoHandlersDoNotShareCounts pins the HOME against
// the item's own "package-level in internal/worker" wording. A package-level var
// would make every exact-count assertion in this package order-dependent on every
// other test in the binary; production has exactly one Handler, so a value field
// IS process-wide there and is a property the wiring guard can check.
func TestTaskLogFenceRejections_TwoHandlersDoNotShareCounts(t *testing.T) {
	a, _ := newFenceHandler(pgx.ErrNoRows)
	b, _ := newFenceHandler(pgx.ErrNoRows)
	lim := newIngestLogLimiter(&a.ingestDrops)

	a.handleTaskLog(context.Background(), fenceWorkerID(), lim, fenceChunk())

	require.Equal(t, uint64(1), a.TaskLogFenceRejections())
	require.Zero(t, b.TaskLogFenceRejections(),
		"counters are per Handler, not per package")
}

// TestTaskLogFenceRejections_ConcurrentRejectionsAreExact is what makes
// "atomic, not a plain uint64" a checked decision rather than a comment: in
// production every connection has its own recv goroutine and they all write this
// one word.
//
// MEASURE BOTH HALVES BEFORE TRUSTING EITHER, and write the numbers into this
// comment rather than copying slice 2's. Against the mutation this test exists
// for - taskLogFenceRejects changed to a plain uint64 with `++`, WITH the .Load()
// in TaskLogFenceRejections dropped to match, or the "kill" is a compile error
// and measures nothing - record the -race kill rate and the exactness kill rate
// at -cpu=1 and -cpu=2 in the golang:1.26 Linux container, plus the unmutated
// green baseline. CI is ubuntu-latest with -race and 2-4 vCPUs, so both halves
// are live there.
func TestTaskLogFenceRejections_ConcurrentRejectionsAreExact(t *testing.T) {
	h, _ := newFenceHandler(pgx.ErrNoRows)
	const goroutines, each = 8, 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One limiter per goroutine, exactly as there is one per connection.
			lim := newIngestLogLimiter(&h.ingestDrops)
			for i := 0; i < each; i++ {
				h.handleTaskLog(context.Background(), fenceWorkerID(), lim, fenceChunk())
			}
		}()
	}
	wg.Wait()

	require.Equal(t, uint64(goroutines*each), h.TaskLogFenceRejections(),
		"every rejection from every connection must land. A plain uint64 here loses counts silently and "+
			"is a data race -race can see.")
}
```

Note: no test in `internal/worker`'s default lane calls `t.Parallel`, which is what makes
`captureUnitLog`'s process-global logger redirect safe (`ingest_log_counters_test.go:441-453`). **Do
not add `t.Parallel` to any test in this file**, and note that the concurrency test above deliberately
does not capture the log.

- [ ] **Step 3: run the tests and verify they FAIL on an assertion, not on a compile error**

Run:
```
go test ./internal/worker/ -run 'TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot|TestTaskLogFenceRejections' -v -timeout 60s
```
Expected: `TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot` FAILS with
`Error: Not equal: expected: 0x1 actual: 0x0` on the first `require.Equal`. If it fails to compile,
Step 1 was not completed - fix that before proceeding; a compile failure is not the RED this plan
claims. `TestHandleTaskLog_ARealPersistFailureIsNotAFenceRejection` and
`TestTaskLogFenceRejections_TwoHandlersDoNotShareCounts` PASS already (they assert zero) - that is
expected and is why they are mutation guards rather than the headline.

- [ ] **Step 4: add the increment, and amend the arm's comment**

In `internal/worker/handler.go`, inside the `if errors.Is(err, pgx.ErrNoRows)` arm, replace the final
two lines of the existing comment block plus the bare `return` (currently `:1130-1138`) with:

```go
			// THIS ARM IS DELIBERATELY SIDE-EFFECT-FREE APART FROM THE COUNT, AND
			// MUST STAY SILENT. A log line here would be caller-driven volume on
			// the recv goroutine, and it would fire on the legitimate late-flush
			// case as well as on forged chunks; a BUDGETED line is no better,
			// because it would spend a token from a six-per-minute bucket that a
			// genuine infra failure needs. Nothing here may publish. Pinned by
			// TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll, which asserts
			// the whole captured log is empty, so any wording reddens it, and by
			// its default-lane twin in tasklog_fence_counter_test.go.
			//
			// THE COUNTER IS THE OBSERVABILITY, and it is one atomic add: no
			// allocation, no lock, no map, no round trip, sitting next to a
			// Postgres round trip that already happened. The one-statement
			// constraint at the top of this function is respected in substance and
			// not merely in letter. Without this number an operator who set
			// RELAY_TASKLOG_TRAILING_WINDOW too small gets silently truncated task
			// output with NO runtime signal of any kind. Served as
			// task_log_fence.counts.rejected_total on the admin-only
			// GET /v1/server/counters. Never returned to an agent.
			//
			// IT IS ONE NUMBER AND IT CANNOT BE THREE, AND THE REASON IS PRECISE
			// RATHER THAN A SLOGAN. The three cases are not recoverable from this
			// statement's RESULT: the fence is a CTE that yields no row at all when
			// any predicate fails, so there is nothing to carry a reason column on
			// (see AppendTaskLog's comment). Getting the reason needs either a
			// SECOND query, which the top of this function forbids, or a rewrite of
			// AppendTaskLog to return a row on the rejection path - a LEFT JOIN over
			// the task row exposing the three predicates as booleans - which would
			// delete the pgx.ErrNoRows signal that every caller, comment and test of
			// this fence is written against and make the success path's three
			// columns nullable. That is a bigger and riskier slice than one number
			// is worth, and it is not "impossible": it is declined, here, with the
			// price written down. Do not spend an afternoon rediscovering either
			// half.
			h.taskLogFenceRejects.Add(1)
			return
```

Keep the existing three-meanings paragraph above it (`:1111-1129`) unchanged.

- [ ] **Step 5: run the tests and verify they pass**

Run:
```
go test ./internal/worker/ -run 'TestHandleTaskLog|TestTaskLogFenceRejections|TestIngestLog' -v -timeout 120s
go test -race ./internal/worker/ -timeout 180s
```
Expected: PASS. On Windows, `-race` needs the MSYS2 mingw64 toolchain
(`CC=/c/msys64/mingw64/bin/gcc.exe`) or the `golang:1.26` Linux container; the container is the
established route and is required anyway for the mutation battery.

- [ ] **Step 6: commit**

```bash
git add internal/worker/handler.go internal/worker/tasklog_fence_counter_test.go
git commit -m "feat(worker): count task-log chunks the AppendTaskLog fence rejects

One atomic.Uint64 on Handler, incremented in the existing pgx.ErrNoRows arm
before its return. No log line, no publish, no second round trip. The counter
is NOT in metrics.Store: Append no-ops for an untracked worker and Clear deletes
the entry at teardown, so a counter there is destroyed by the disconnect that
produced the rejections.

Proven in the DEFAULT lane, which slice 2's counter could not be: AppendTaskLog
is one QueryRow plus one Scan over a store.DBTX, so a stub DBTX returning
pgx.ErrNoRows drives the real fence arm with no container.

Refs idea-2026-08-14-tasklog-fence-rejection-is-unobservable"
```

---

## Task 2: publish the section (do NOT commit at the end of this task)

**Files:**
- Modify: `internal/api/server_counters.go:113-267`
- Test: `internal/api/server_counters_test.go:517-534`, `:644-657`, plus appended tests

- [ ] **Step 1: write the first failing assertion, with no new symbol**

Add one line to `counterPayloadLeaves` (`internal/api/server_counters_test.go:534`, after the last
ingest entry):

```go
	"task_log_fence.counts.rejected_total",
```

- [ ] **Step 2: run the payload guards and verify they fail behaviourally**

Run:
```
go test ./internal/api/ -run TestCounterPayload -v -timeout 60s
```
Expected: BOTH `TestCounterPayloadCarriesNoIdentifiers` and `TestCounterPayloadBytesCarryNoIdentifiers`
FAIL with an `ElementsMatch` diff naming `task_log_fence.counts.rejected_total` as missing from the
actual set. This is a real behavioural RED against shipped tests with no new symbol in the tree - the
payload contract list is load-bearing, and this is the proof.

- [ ] **Step 3: write the rest of the failing tests**

Append to `internal/api/server_counters_test.go`:

```go
// fakeTaskLogFenceSource returns a fixed count.
type fakeTaskLogFenceSource struct{ n uint64 }

func (f fakeTaskLogFenceSource) TaskLogFenceRejections() uint64 { return f.n }

// TestTaskLogFenceSourceReturnsAScalar is a forcing function on an ANTECEDENT,
// not on today's code.
//
// The rule this package learned in the ingest slice is that any section whose
// payload struct RESTATES fields owned by another package needs a NumField
// assertion between the two types, written here because this is the only place
// both are visible (TestIngestLogKindCountsPublishesEveryWorkerSideField).
// task_log_fence restates NOTHING: its source returns a bare uint64, there is no
// hand-written field-by-field mapper, and so that rule does not apply - which is
// only true while the return type stays a scalar. Widening it to a struct is a
// one-line edit that would silently move this section into the class that needs
// the arity check.
func TestTaskLogFenceSourceReturnsAScalar(t *testing.T) {
	iface := reflect.TypeOf((*TaskLogFenceSource)(nil)).Elem()
	m, ok := iface.MethodByName("TaskLogFenceRejections")
	require.True(t, ok, "TaskLogFenceSource must declare TaskLogFenceRejections")
	require.Equal(t, 1, m.Type.NumOut(), "one return value")
	require.Equal(t, reflect.Uint64, m.Type.Out(0).Kind(),
		"TaskLogFenceRejections returns %s. While it returns a SCALAR this section restates no "+
			"worker-side field and needs no cardinality check. If it now returns a struct, an arity "+
			"assertion between the worker-side type and the api-side type must ship IN THIS COMMIT - see "+
			"TestIngestLogKindCountsPublishesEveryWorkerSideField, which exists because a fully correct "+
			"sixth kind was counted on the recv path and published under no JSON key with all three "+
			"packages green.", m.Type.Out(0).Kind())
}

func TestServerCounters_ReportsTheTaskLogFenceSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{TaskLogFence: fakeTaskLogFenceSource{n: 77}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		TaskLogFence *struct {
			Counts map[string]any `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"task_log_fence"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.TaskLogFence, "a wired section must be present")
	require.Nil(t, body.TaskLogFence.Levels,
		"COUNTS ONLY. There is no current 'level' of fence rejections to report - the number is a "+
			"monotonic total since started_at.")

	// Key-set equality, not a per-key assertion alone: a renamed key would decode
	// as a missing value and read as zero.
	assert.ElementsMatch(t, []string{"rejected_total"}, counterMapKeys(body.TaskLogFence.Counts))
	assert.Equal(t, float64(77), body.TaskLogFence.Counts["rejected_total"])
}

// TestServerCounters_WiredButZeroTaskLogFenceSectionIsStillPresent. A server
// whose fence has rejected nothing is the COMMON case and must still emit its
// section: zeros mean "this control ran and stopped nothing", absence means "not
// wired on this build". This section's counts half is SCALARS, so unlike
// ingest_log_budget the shipped one-level loop is the right shape here.
func TestServerCounters_WiredButZeroTaskLogFenceSectionIsStillPresent(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{TaskLogFence: fakeTaskLogFenceSource{}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.ElementsMatch(t, []string{"started_at", "task_log_fence"}, counterKeys(top),
		"a WIRED source whose counter is zero must still emit its section, and no OTHER section may "+
			"appear: each source is nil-able on its own")

	var section map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["task_log_fence"], &section))
	require.ElementsMatch(t, []string{"counts"}, counterKeys(section), "counts only; no levels half")

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(section["counts"], &fields))
	require.Len(t, fields, 1, "counts must be an object with the one key, not an empty object")
	assert.Equal(t, "0", string(fields["rejected_total"]),
		"rejected_total must serialise as an explicit zero, never be elided by omitempty")
}
```

Also extend the every-section fixture in `TestCounterPayloadBytesCarryNoIdentifiers` (`:647-654`) so
the new leaf is actually produced:

```go
			IngestLogBudget: fakeIngestLogSource{d: tenDistinctDrops()},
			TaskLogFence:    fakeTaskLogFenceSource{n: 123},
```

`reflect` is already imported by this file. Add no other import.

- [ ] **Step 4: run and verify the new tests fail to compile against a missing field**

Run:
```
go test ./internal/api/ -run 'TestServerCounters_ReportsTheTaskLogFenceSnapshot' -v -timeout 60s
```
Expected: a compile error naming `TaskLogFence` as an unknown field of `CounterSources`. That is
expected for THIS step (the behavioural RED for the section is Step 2's, which is already recorded);
Step 5 makes it compile and green.

- [ ] **Step 5: write the implementation**

In `internal/api/server_counters.go`, after `IngestLogBudgetSource` (`:129`):

```go
// TaskLogFenceSource is whatever can report how many task-log chunks the
// AppendTaskLog fence has rejected - in production, *worker.Handler.
//
// ITS OWN SOURCE FIELD, NOT A WIDENED IngestLogBudgetSource, exactly as that
// interface's comment demands. The two counters live on the same *worker.Handler
// and are wired together today, but they are independent CONTROLS counting
// different nouns, and an interface carrying both would make them appear and
// disappear together as a matter of TYPE rather than as a matter of wiring.
//
// A SCALAR, NOT A STRUCT, and that is load-bearing rather than minimal: a
// section whose payload struct restates fields owned by another package needs a
// cardinality check between the two types (see
// TestIngestLogKindCountsPublishesEveryWorkerSideField). This section restates
// nothing, so it needs no such check - a property pinned by
// TestTaskLogFenceSourceReturnsAScalar, which reddens if this return type is ever
// widened.
type TaskLogFenceSource interface {
	TaskLogFenceRejections() uint64
}
```

Add the field to `CounterSources` (`:154-157`):

```go
type CounterSources struct {
	GRPCAdmission   GRPCAdmissionSource
	IngestLogBudget IngestLogBudgetSource
	TaskLogFence    TaskLogFenceSource
}
```

Add the response field (`:159-163`):

```go
type serverCountersResponse struct {
	StartedAt       time.Time               `json:"started_at"`
	GRPCAdmission   *grpcAdmissionSection   `json:"grpc_admission,omitempty"`
	IngestLogBudget *ingestLogBudgetSection `json:"ingest_log_budget,omitempty"`
	TaskLogFence    *taskLogFenceSection    `json:"task_log_fence,omitempty"`
}
```

Add the section types after `ingestLogKindCountsFrom` (`:238`):

```go
// task_log_fence is COUNTS ONLY and it is ONE NUMBER, both by decision.
//
// rejected_total counts task-log chunks that AppendTaskLog's three-predicate
// fence refused: the sender is not the task's assignee, or its generation is
// stale, or the task finished longer ago than RELAY_TASKLOG_TRAILING_WINDOW.
// THE THIRD IS LEGITIMATE and is the one an operator who set that knob too small
// hits constantly, which is why this number exists at all - before it there was
// no runtime signal of any kind that task output was being dropped.
//
// WHY THE THREE ARE NOT SPLIT: the fence yields no row when any predicate fails,
// so there is nothing to carry a reason on. Recovering it needs a second round
// trip (forbidden on the recv goroutine) or a rewrite of AppendTaskLog's result
// contract. Declined with the price written down; see the pgx.ErrNoRows arm in
// internal/worker/handler.go. Do not "improve" this into three fields without
// reading that first.
//
// AND WHAT IT DOES NOT COVER: it is not a subset or a superset of
// ingest_log_budget. That arm never consults the log budget, so no input moves
// both numbers and neither explains any part of the other.
type taskLogFenceSection struct {
	Counts taskLogFenceCounts `json:"counts"`
}

type taskLogFenceCounts struct {
	RejectedTotal uint64 `json:"rejected_total"`
}
```

Add the handler branch after the `IngestLogBudget` branch (`:265`):

```go
	if src := s.Counters.TaskLogFence; src != nil {
		resp.TaskLogFence = &taskLogFenceSection{
			Counts: taskLogFenceCounts{RejectedTotal: src.TaskLogFenceRejections()},
		}
	}
```

Finally, amend the "AND WHAT THEY DO NOT COUNT" paragraph of `ingestLogBudgetSection`'s comment
(`:198-202`), whose last clause currently reads "handleTaskLog's fence-rejection arm never consults it
at all (that one is its own item and its own section)". Change the parenthetical to "(that one is
counted in task_log_fence, and the two numbers are disjoint)". Leave the rest of the paragraph exactly
as it is.

- [ ] **Step 6: run the api tests and verify they pass**

Run:
```
go test ./internal/api/ -run 'TestServerCounters|TestCounterPayload|TestTaskLogFenceSource|TestIngestLogKindCounts' -v -timeout 120s
```
Expected: PASS, including the two payload walks that were RED at Step 2.

- [ ] **Step 7: run the whole api package**

Run: `go test ./internal/api/ -timeout 300s`
Expected: PASS.

- [ ] **Step 8: observe the PREDICTED wiring-guard red, and DO NOT COMMIT**

Run:
```
go test ./cmd/relay-server/ -run TestServerCountersIsWiredByMain -v -timeout 60s
```
Expected: FAIL, with the shipped message "api.CounterSources has 3 source fields and this table names 2
DISTINCT httpServerDeps fields (in 2 rows)". **Record that output.** This is the guard whose own
comment predicted this exact moment, firing exactly as predicted. **Do not commit here** - the tree is
red in `cmd/relay-server` by design, and Task 3 closes it in the same commit. **Do not make it green
by duplicating a row**: that is the evasion the check was rewritten to stop, and Task 3's rewrite makes
it impossible rather than merely forbidden.

---

## Task 3: wire it, and fix the cardinality relation deliberately

**Files:**
- Modify: `cmd/relay-server/http_server.go:50-80`, `:131-136`
- Modify: `cmd/relay-server/counters_wiring_test.go:319-363`, `:578-599`, `:601-609`

**The decision, for the commit message and for the record.** The third `api.CounterSources` field is
fed by the `*worker.Handler` that already feeds `IngestLogBudget`, so no new `httpServerDeps` field is
added. A second deps field for the same object would have made the shipped `NumField` arithmetic
accidentally right while letting the two sections be fed two DIFFERENT handlers - only `agentHandler`
is compared against `RegisterAgentServiceServer` - which is the "confident zero" that guard exists to
prevent. Instead the relation is re-expressed as **N sections over M deps fields**.

- [ ] **Step 1: write the failing guard**

In `cmd/relay-server/counters_wiring_test.go`, replace the `wiredDep` type (`:601-609`) with:

```go
// wiredDep names one httpServerDeps field whose source must be a plain,
// unconditionally-bound local in main's body, the constructor that local has to
// derive from, and the api.CounterSources fields it feeds.
//
// ONE ROW PER DEPS FIELD, AND EACH ROW NAMES ITS SECTIONS. It used to be one row
// per SECTION, on the assumption that the two were the same thing. They are not:
// agentHandler feeds BOTH IngestLogBudget and TaskLogFence, because both controls
// live on the same *worker.Handler and neither exists without it. The completeness
// relation below therefore counts SECTIONS, which is the claim being made ("every
// section is wired through a checked field"), and counts deps fields only to
// reject a duplicated row.
type wiredDep struct {
	field     string
	sections  []string
	mustReach string
	what      string
}
```

Replace the table and the cardinality block (`:319-363`) with:

```go
	deps := []wiredDep{
		{"grpcAdmission", []string{"GRPCAdmission"}, "Wrap", "the netlimit listener bound in main's body"},
		{"agentHandler", []string{"IngestLogBudget", "TaskLogFence"}, "NewHandlerWithGrace", "the worker.Handler bound in main's body"},
	}

	// COUNT THE SECTIONS AGAINST api.CounterSources, because "every wired source
	// has a row" is a completeness claim and a completeness claim cannot be checked
	// by reading the rows.
	//
	// THE DENOMINATOR CHANGED, AND THE REASON IS WORTH THE PARAGRAPH. This block
	// used to compare the number of DISTINCT httpServerDeps fields to
	// NumField(api.CounterSources), which held only while the two were in bijection.
	// The task-log fence counter broke that: it is a third CounterSources field fed
	// by the SAME *worker.Handler, so distinct deps fields stayed at 2 while NumField
	// went to 3. The two available repairs were a second deps field for the same
	// object, or this. The second deps field was REJECTED on merit rather than on
	// taste: only agentHandler is compared against the identifier passed to
	// RegisterAgentServiceServer below, so a sibling field could legitimately be fed
	// a DIFFERENT worker.Handler with every check here green, and the endpoint would
	// then report a permanently zero fence count while the real one climbed - the
	// confident zero this whole file exists to prevent, created in order to satisfy
	// an arithmetic check.
	//
	// DISTINCT FIELDS, NOT ROWS, is kept and strengthened into an equality: counting
	// rows was PROVED evadable (replacing the agentHandler row with a second
	// grpcAdmission row made the package green while dropping agentHandler out of the
	// plain-identifier check, the derives-from check and the assigned-exactly-once
	// check at a stroke). A duplicated row is now RED on its own, before any section
	// arithmetic is reached.
	depsType := reflect.TypeOf(httpServerDeps{})
	sourcesType := reflect.TypeOf(api.CounterSources{})
	distinct := map[string]bool{}
	sections := map[string]bool{}
	for _, d := range deps {
		_, ok := depsType.FieldByName(d.field)
		require.True(t, ok,
			"this table has a row for httpServerDeps.%s, which does not exist. A row naming no field "+
				"guards nothing and makes the counts below pass on a table that is short one section.",
			d.field)
		distinct[d.field] = true

		require.NotEmpty(t, d.sections,
			"the row for httpServerDeps.%s names no api.CounterSources field. A row that feeds no "+
				"section is a dependency nothing reads; a section fed by no row is guarded by nothing.",
			d.field)
		for _, s := range d.sections {
			_, ok := sourcesType.FieldByName(s)
			require.True(t, ok,
				"this table's %s row names api.CounterSources.%s, which does not exist. Fix the typo: a "+
					"row naming a phantom section satisfies the count below while a real section goes "+
					"unguarded.", d.field, s)
			require.False(t, sections[s],
				"api.CounterSources.%s is named by two rows. Each section is fed by exactly one deps "+
					"field, and two rows naming the same one lets the count below pass on a table that is "+
					"short a different section.", s)
			sections[s] = true
		}
	}
	require.Len(t, distinct, len(deps),
		"this table has %d rows naming %d DISTINCT httpServerDeps fields. Do not resolve a cardinality "+
			"failure by repeating a row: that was proved to drop the displaced field out of every check "+
			"below while every count still passed.", len(deps), len(distinct))
	require.Len(t, sections, sourcesType.NumField(),
		"api.CounterSources has %d source fields and this table names %d of them. EVERY SECTION needs to "+
			"be named by exactly one row, or it is wired by code nothing checks and can be silently "+
			"unwired on some deployments with this guard green. A deps field may feed more than one "+
			"section (agentHandler feeds two); a section may not be fed by more than one row.",
		sourcesType.NumField(), len(sections))
```

The rest of the test - `depArg`, the plain-identifier check, the derives-from walk, the
`RegisterAgentServiceServer` comparison and the assignments-per-identifier count - is unchanged and
needs no edit.

Extend the shipped typed-nil test (`:578-599`) with one assertion and a sentence of comment:

```go
	require.NotContains(t, top, "task_log_fence",
		"the same nil filter covers BOTH sections fed by this handler. One deps field, one `if`, two "+
			"sections: a separate typed-nil test would copy this fixture to assert the same branch.")
```

Add the new presence test after `TestBuildHTTPServer_ServesTheWiredHandlersIngestSection` (`:576`):

```go
// TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection is EXECUTED, and
// it says what it does not buy.
//
// It proves the section is PRESENT with the right shape whenever a non-nil
// Handler is passed, which is what kills a dropped assignment inside
// buildHTTPServer. It does NOT prove the section is served from THAT Handler: a
// fresh worker.NewHandler substituted there produces an identical zero section
// and leaves this green. That question is executable only past Connect's message
// loop, so it lives in
// TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers,
// which is INTEGRATION-tagged - go-ci runs `go test -race ./...` with no tag, so
// CI compiles it and never runs it.
func TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection(t *testing.T) {
	h := worker.NewHandler(nil, nil, worker.NewRegistry(), events.NewBroker(), func() {})
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: h,
	})

	top := countersAsAdmin(t, srv)
	require.Contains(t, top, "task_log_fence",
		"a wired Handler must produce the section from the moment the server is built. An absent "+
			"section reads as 'this build has no task-log fence', which is false.")

	var section struct {
		Counts struct {
			RejectedTotal uint64 `json:"rejected_total"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(top["task_log_fence"], &section))
	require.Zero(t, section.Counts.RejectedTotal, "nothing has been rejected on a fresh handler")
}
```

- [ ] **Step 2: run and verify the guard is still red for the RIGHT reason**

Run:
```
go test ./cmd/relay-server/ -run 'TestServerCountersIsWiredByMain|TestBuildHTTPServer' -v -timeout 60s
```
Expected: `TestServerCountersIsWiredByMain` now PASSES (the table names three sections over two deps
fields, and `NumField` is three), while
`TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection` FAILS with "the task_log_fence section
is absent" - because nothing assigns it yet. If the guard is still red, the table edit is wrong; fix
that before touching `buildHTTPServer`.

- [ ] **Step 3: write the wiring**

In `cmd/relay-server/http_server.go`, replace the `agentHandler` block (`:134-136`) with:

```go
	// TWO SECTIONS, ONE OBJECT, AND THAT IS NOT THE WIDENED INTERFACE
	// IngestLogBudgetSource's comment forbids. api.CounterSources keeps a separate
	// nil-able field per section, so each is a per-SECTION fact in the payload and
	// a future source could satisfy one and not the other. What is shared here is
	// the WIRING: both controls live on this one *worker.Handler and neither exists
	// without it, so one nil filter is the honest shape and two identical `if`s
	// would imply an independence this deployment does not have.
	if d.agentHandler != nil {
		s.Counters.IngestLogBudget = d.agentHandler
		s.Counters.TaskLogFence = d.agentHandler
	}
```

And extend the `agentHandler` field comment (`:50-80`) - insert after the "IT MUST BE THE SAME HANDLER"
paragraph:

```go
	// IT FEEDS TWO SECTIONS, ingest_log_budget and task_log_fence, which are
	// different nouns counted on different branches of the same `if` in
	// handleTaskLog. counters_wiring_test.go's table names both against this one
	// field; that is why its cardinality relation counts SECTIONS rather than deps
	// fields.
```

- [ ] **Step 4: run and verify everything passes**

Run:
```
go test ./cmd/relay-server/ -timeout 180s
go test ./internal/api/ ./internal/worker/ -timeout 300s
```
Expected: PASS everywhere.

- [ ] **Step 5: commit Tasks 2 and 3 together**

```bash
git add internal/api/server_counters.go internal/api/server_counters_test.go \
        cmd/relay-server/http_server.go cmd/relay-server/counters_wiring_test.go
git commit -m "feat(api): serve task_log_fence.counts.rejected_total on /v1/server/counters

Its own CounterSources field and its own section, never a widened
IngestLogBudgetSource. The source returns a SCALAR, so this section restates no
worker-side field and needs no arity check - pinned by
TestTaskLogFenceSourceReturnsAScalar so that widening it to a struct reddens.

The wiredDep cardinality relation is re-expressed as N sections over M deps
fields rather than padded with a second deps field. Reusing agentHandler is
deliberate: only agentHandler is compared against RegisterAgentServiceServer, so
a sibling field for the same object could have been fed a different Handler with
every check green. A duplicated row is now RED on its own.

Refs idea-2026-08-14-tasklog-fence-rejection-is-unobservable"
```

---

## Task 4: prove `buildHTTPServer` forwards what it was given (integration lane)

**Files:**
- Modify: `cmd/relay-server/grpc_admission_e2e_integration_test.go` (append one test and one helper)

- [ ] **Step 1: write the failing test**

Append to `cmd/relay-server/grpc_admission_e2e_integration_test.go`:

```go
// TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers is
// the FORWARDING guard for the third section, and it is the top rung of the
// ladder: a real registered gRPC stream, real fence rejections on the recv
// goroutine, read back through the real admin-gated route on a server built by
// buildHTTPServer.
//
// The gap it closes is the one slice 2 shipped and had to be told about.
// Substituting a freshly constructed worker.NewHandler inside buildHTTPServer -
//
//	s.Counters.TaskLogFence = worker.NewHandler(d.q, d.pool, d.registry, d.broker, func() {})
//
// compiles, vets clean under both tag sets and leaves every package green,
// including TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection, which
// asserts the section exists with the right shape - and a fresh Handler's section
// does. In production that serves a permanently zero rejection count while real
// output is being dropped: a CONFIDENT ZERO, which is worse than no endpoint.
//
// NO TASK NEEDS SEEDING. A well-formed uuid naming no task parses (so the bad-id
// arm is not involved), matches no fence row, and returns pgx.ErrNoRows - which
// is the arm under test.
//
// WHY THE INTEGRATION LANE: reaching the fence arm requires Connect's message
// loop, which is past authenticateAndRegister, which is a Postgres round trip. Note
// what is NOT here for that reason - the counter's own behaviour is proved in the
// DEFAULT lane by internal/worker/tasklog_fence_counter_test.go, which drives the
// real AppendTaskLog call through a stub store.DBTX. Only the wiring needs a
// container.
func TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers(t *testing.T) {
	pool, q := newTestPoolAndQueries(t)
	ctx := context.Background()
	rawToken := seedE2EWorker(t, ctx, q, "e2e-fence-counters")

	addr, handler := startProductionGRPCServerWithHandler(t, pool, q,
		netlimit.Config{MaxTotal: 10, MaxPerIP: 10},
		grpcBounds{maxConns: 10, maxConnsPerIP: 10, maxConnIdle: 0})

	// Built exactly as main builds it, from the handler already serving gRPC above.
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: handler,
	})

	require.Zero(t, taskLogFenceRejectedTotal(t, srv),
		"fixture: nothing rejected yet, so the section is present and zero")

	cc := dialE2E(t, addr)
	stream, _ := registerOverRealConnection(t, cc, "e2e-fence-counters", rawToken)

	const unknownTask = "3f1c0a2e-7b64-4d8a-9c31-0e5b6a7d8c90"
	const chunks = 5
	for i := 0; i < chunks; i++ {
		require.NoError(t, stream.Send(&relayv1.AgentMessage{
			Payload: &relayv1.AgentMessage_TaskLog{TaskLog: &relayv1.TaskLogChunk{
				TaskId: unknownTask, Content: []byte("x"), Epoch: 1,
			}},
		}))
	}

	// The recv loop is asynchronous, so poll rather than sleep, and target an EXACT
	// number: "non-zero" would pass on a partially drained stream while proving less
	// than it claims.
	deadline := time.Now().Add(30 * time.Second)
	var got uint64
	for time.Now().Before(deadline) {
		got = taskLogFenceRejectedTotal(t, srv)
		if got == uint64(chunks) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, uint64(chunks), got,
		"GET /v1/server/counters must report the rejections of the Handler that ran this connection's "+
			"recv loop. Zero here means buildHTTPServer served some OTHER source - a second Handler, a "+
			"stub, or nothing.")

	// THE SIBLING SECTION MUST NOT MOVE. The fence arm never consults the log
	// budget, so these are different nouns and neither covers any part of the other.
	require.Zero(t, ingestDedupedCount(t, srv, "task_log_persist"),
		"a fence rejection is not a dropped log line: it never reaches the budget at all")
	require.Zero(t, ingestSuppressedCount(t, srv, "task_log_persist"))

	// AND THE NUMBER MUST SURVIVE THE DISCONNECT THAT PRODUCED IT. This is the
	// metrics.Store refutation made executable: that type's Clear DELETES a worker's
	// entry at teardown, so a counter parked there would read zero here.
	require.NoError(t, stream.CloseSend())
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, uint64(chunks), taskLogFenceRejectedTotal(t, srv),
		"the count is process-lifetime, not per connection: a worker that floods and then drops must "+
			"still be visible to the operator who goes looking afterwards")
}

// taskLogFenceRejectedTotal reads the section back through the real admin-gated
// route, the way an operator's poller would, rather than reaching into the Handler.
func taskLogFenceRejectedTotal(t *testing.T, srv *http.Server) uint64 {
	t.Helper()
	top := countersAsAdmin(t, srv)
	raw, ok := top["task_log_fence"]
	require.True(t, ok, "the task_log_fence section is ABSENT, so the handler was never wired at all")

	var section struct {
		Counts struct {
			RejectedTotal uint64 `json:"rejected_total"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(raw, &section))
	return section.Counts.RejectedTotal
}
```

- [ ] **Step 2: verify it compiles in the lane CI can see**

Run: `make vet-integration`
Expected: clean. CI runs exactly this and nothing more for tagged files.

- [ ] **Step 3: run it (requires Docker Desktop)**

Run:
```
go test -tags integration -p 1 ./cmd/relay-server/ -run TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers -v -timeout 300s
```
Expected: PASS.

- [ ] **Step 4: prove the guard is load-bearing before committing it**

Temporarily replace `s.Counters.TaskLogFence = d.agentHandler` in `buildHTTPServer` with
`s.Counters.TaskLogFence = worker.NewHandler(d.q, d.pool, d.registry, d.broker, func() {})`, re-run
Step 3, and confirm it FAILS with `0 != 5` on the exact-count assertion (not on an absent section, and
not on the sibling assertions - **a mutation that reddens something is not yet evidence for the claim
you are making**). Then revert the mutation, run Step 3 again, and confirm PASS.

- [ ] **Step 5: commit**

```bash
git add cmd/relay-server/grpc_admission_e2e_integration_test.go
git commit -m "test(cmd): prove buildHTTPServer forwards the serving handler's fence counts

Real registered stream, real fence rejections, read back through the real
admin-gated route. Integration-tagged: reaching the fence arm needs Connect's
message loop and therefore Postgres, and go-ci runs `go test -race ./...` with no
tag, so CI compiles this and does not run it. The counter's own behaviour is
covered in the default lane.

Also asserts the count survives the disconnect that produced it, which is the
metrics.Store refutation made executable."
```

---

## Task 5: document it (independent of Tasks 1-4 after Task 2)

**Files:**
- Modify: `README.md:277`, `:1260-1276`, `:1286`, plus one new bullet

- [ ] **Step 1: add the section to the payload example**

In the JSON block at `README.md:1261-1275`, after the `ingest_log_budget` object, add:

```json
  "task_log_fence": {
    "counts": { "rejected_total": 0 }
  }
```
(with the comma placement fixed so the object stays valid JSON).

- [ ] **Step 2: add the reading bullet**

Insert after the `ingest_log_budget` bullets (after `:1286`):

```markdown
- **Reading `task_log_fence`.** `rejected_total` counts log chunks the coordinator refused to store since `started_at`. A chunk is refused for one of three reasons and **the payload cannot say which**: the sender is not the task's current assignee, or its generation is stale (both are a zombie or forged sender, and both are the system working), or the task finished longer ago than `RELAY_TASKLOG_TRAILING_WINDOW` - **which is legitimate, and is the case to suspect first when task output is missing rather than spurious.** The number is one number on purpose: the fence returns no row at all when it refuses, so there is nothing to carry a reason, and recovering one would need a second query on a path budgeted for exactly one. **What it is for:** a count that climbs steadily on a fleet whose jobs look healthy is the signature of a trailing window set too small - a units mistake such as `15s` for `15m` is the likely one - and before this number there was no runtime signal of any kind for that, only silently truncated output. A count that moves in bursts around requeues, cancellations and worker reconnects is the stale-generation case and is expected. **What it is not:** it does not overlap `ingest_log_budget` in either direction, because the rejection path never consults the log budget; and it never reaches an agent, which still learns nothing about why its chunk was dropped.
```

- [ ] **Step 3: correct the two sentences this slice makes wrong**

At `:1286`, in the "What `ingest_log_budget` does NOT count" bullet, change "a log chunk the fence
rejected" to "a log chunk the fence rejected (counted separately under `task_log_fence`)".

At `:277`, in the `RELAY_TASKLOG_TRAILING_WINDOW` row, change "a rejected chunk is dropped with no
error to the agent and no line in the server log, exactly like a stale-epoch chunk" to "a rejected
chunk is dropped with no error to the agent and no line in the server log, exactly like a stale-epoch
chunk - **the one runtime signal is `task_log_fence.counts.rejected_total` on
`GET /v1/server/counters`**, which climbs steadily when this window is too small".

- [ ] **Step 4: verify the JSON example parses**

Copy the fenced `json` block at `README.md:1260` into any JSON validator, or run it through
`python -c "import json,sys; json.load(sys.stdin)"`. Expected: no error. A trailing comma after the
`ingest_log_budget` object is the classic break here.

- [ ] **Step 5: commit**

```bash
git add README.md
git commit -m "docs: document task_log_fence and correct two sentences it invalidates

The trailing-window row said a rejected chunk has no signal of any kind; it now
has exactly one. The ingest_log_budget bullet said a fence-rejected chunk
contributes nothing to those numbers, which stays true and is now incomplete."
```

---

## Mutation battery

Run in an **isolated detached worktree**, never the shared tree (sibling agents read it). **Establish a
green baseline first**: run every named test unmutated and record PASS. Uniform results across a
battery mean the harness is broken, not that coverage is good. **A kill counts only if the failure
message matches the mechanism being asserted** - isolating slice 2's arity gap took four attempts, and
three of them were real reds for the wrong reason.

| # | Mutation (must compile) | Must go RED in | Lane |
| --- | --- | --- | --- |
| M1 | delete `h.taskLogFenceRejects.Add(1)` from the arm | `TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot` (first `require.Equal`) | default |
| M2 | move the `Add(1)` above `if errors.Is(err, pgx.ErrNoRows)` | `TestHandleTaskLog_ARealPersistFailureIsNotAFenceRejection` | default |
| M3 | add a second `Add(1)` after `AppendTaskLog` returns nil | same test, third leg ("an ACCEPTED chunk must not be counted") | default |
| M4 | `Add(1)` -> `Store(1)` | same test, second leg (`2 != 1`) | default |
| M5 | `TaskLogFenceRejections()` returns a literal `0` | same test, first leg | default |
| M6 | make `taskLogFenceRejects` a plain `uint64` with `++`, dropping `.Load()` to match | `TestTaskLogFenceRejections_ConcurrentRejectionsAreExact` | default, **needs `-race`** (and >1 CPU for the exactness half) |
| M7 | make the counter a package-level `var` shared by all handlers | `TestTaskLogFenceRejections_TwoHandlersDoNotShareCounts` | default |
| M8 | delete the `TaskLogFence` branch from `handleServerCounters` | `TestServerCounters_ReportsTheTaskLogFenceSnapshot`, both payload walks | default |
| M9 | emit the section only when the count is non-zero (`if n == 0 { skip }`) | `TestServerCounters_WiredButZeroTaskLogFenceSectionIsStillPresent` | default |
| M10 | rename the JSON tag `rejected_total` -> `rejected` | `TestServerCounters_ReportsTheTaskLogFenceSnapshot` (key-set equality) and both payload walks | default |
| M11 | delete `"task_log_fence.counts.rejected_total"` from `counterPayloadLeaves` | `TestCounterPayloadCarriesNoIdentifiers` and `TestCounterPayloadBytesCarryNoIdentifiers` | default |
| M12 | widen the source to return a one-field struct and adjust the handler | `TestTaskLogFenceSourceReturnsAScalar` | default |
| M13 | delete `s.Counters.TaskLogFence = d.agentHandler` from `buildHTTPServer` | `TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection` | default |
| M14 | move both `s.Counters.*` assignments outside the `if d.agentHandler != nil` | `TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent` (its new assertion; expect a nil-receiver panic, which is itself the finding) | default |
| M15 | replace `d.agentHandler` with a fresh `worker.NewHandler(...)` for `TaskLogFence` only | `TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers` **only** | **integration** - green in the default lane, and that is the disclosed limit |
| M16 | duplicate the `agentHandler` row in `wiredDep` (the historical evasion) | `TestServerCountersIsWiredByMain` (`distinct` vs `len(deps)`) | default |
| M17 | drop `"TaskLogFence"` from the `agentHandler` row's `sections` | `TestServerCountersIsWiredByMain` (sections vs `NumField`) | default |
| M18 | typo a section name (`"TaskLogFenc"`) | `TestServerCountersIsWiredByMain` (field-exists check, failing on the typo rather than four assertions later) | default |
| M19 | `agentHandler = nil` inside an `if` in `main` | `TestServerCountersIsWiredByMain` (assigned-exactly-once) | default |

**Parallelism dependence:** only **M6**. Its `-race` half kills through happens-before analysis and
does not need true parallelism; its EXACTNESS half does. CI is `ubuntu-latest`, `go test -race ./...`,
no `-cpu` flag, 2-4 vCPUs, so both halves are live there. `-race` cannot run on the authoring Windows
host without the MSYS2 mingw64 toolchain; the established route is a `golang:1.26` Linux container.
**Measure M6 at `-cpu=1` and `-cpu=2` in that container and write the observed numbers into the test's
comment.** Do not copy slice 2's figures: its equivalent comment shipped BACKWARDS and had to be
re-measured by a review lens, and its original "kills" turned out to be compile errors rather than
behavioural ones because the `.Load()` calls had not been removed.

**No other mutation depends on scheduling.** M14 is expected to panic rather than fail an assertion;
record that as the kill and note it in the finding, since a panic in a handler is a worse outcome than
a red assertion and is exactly what the typed-nil filter exists to prevent.

---

## Existing tests: what may change and what may not

- **May change:** `counterPayloadLeaves` (one added entry), `TestCounterPayloadBytesCarryNoIdentifiers`
  (one added fixture field), `TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent` (one
  ADDED assertion), `TestServerCountersIsWiredByMain`'s table and cardinality block, `wiredDep`'s type.
- **Must not change, and any change of result is a finding to REPORT rather than to fix:**
  `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll`,
  `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch`,
  `TestHandleTaskLog_TheLoggedTaskIdIsCanonicalNotTheWireString`,
  `TestIngestLogKindCountsPublishesEveryWorkerSideField`,
  `TestServerCounters_OneWiredSectionDoesNotDragInTheOther`,
  `TestServerCounters_ReportsTheIngestLogSnapshot`,
  `TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers`, and every
  `internal/netlimit` test.

## Constraint checks (CLAUDE.md Invariants)

- **Epoch fence.** No SQL, no migration, no generated file, and no write to `tasks.status` or
  `task_logs`. The counter increments only where the fence did NOT match, so no side effect is gated on
  an unmatched fence. `AppendTaskLog`'s status allow-list (the one that reads backwards) is untouched,
  as is `ListOverdueAssignedTasks`, and `TestTasksStatusVocabularyIsExactly` is unaffected.
- **One bounded sender per gRPC stream.** No send is added anywhere and no number reaches an agent.
- **Identity-checked teardown.** No teardown is added. The counter has no lifecycle at all, which is
  the point of not putting it in `metrics.Store`.
- **No interior pointers across locks.** No lock, and the accessor returns a `uint64` by value.
- **Single JSON entry point.** The endpoint is a `GET` with no body; the response goes through
  `writeJSON`, unchanged.
- **End the generation before releasing the resource.** No generation, no async continuation.
- **No new attacker-driven log site.** Nothing logs. Guarded in both lanes.

## Slice 4 is not foreclosed

The watchdog section adds a fourth `api.CounterSources` field fed by a NEW `httpServerDeps` field
(`*scheduler.Watchdog`), so it adds one `wiredDep` row naming one section: `distinct` = 3 = `len(deps)`,
`sections` = 4 = `NumField`. The rewritten relation absorbs it with no further change. Its snapshot type
must be declared IN `internal/api` (`internal/scheduler` imports `internal/api`), its typed nil must be
filtered at the wiring boundary because the watchdog is legitimately disable-able, and its
`swept_by_worker` map needs a `counterPayloadExemption` with descending `typeOK`/`jsonOK` predicates -
all unchanged by this slice, all recorded in `server_counters.go`'s doc comment.

## Self-review

- **Spec coverage.** Joint spec section 10.3 lists three contents: one `atomic.Uint64` incremented in
  the existing arm (Task 1), the arm's comment extended rather than duplicated (Task 1, Step 4), and
  the `task_log_fence` section (Task 2). Section 9's payload shape is adopted verbatim. Section 7.3's
  "one number, with the comment saying why there will never be three" is met in amended wording (R1).
  Section 13's constraint list is checked above. Section 6.1's absolute exclusion of a log line on this
  arm is met and newly guarded in the default lane.
- **Item Done-When coverage.** All thirteen bullets map to a task; two are met in amended form and the
  amendments are named in "Backlog effects" below.
- **Placeholder scan.** No TBD, no "add error handling", no "similar to Task N". Every code step shows
  the code it lands.
- **Type consistency.** Field `taskLogFenceRejects` -> method `TaskLogFenceRejections()` -> interface
  `TaskLogFenceSource` -> `CounterSources.TaskLogFence` -> `taskLogFenceSection` /
  `taskLogFenceCounts` -> JSON `task_log_fence.counts.rejected_total` -> `wiredDep.sections` entry
  `"TaskLogFence"` -> deps field `agentHandler`. Checked end to end.
- **Plan-supplied tests are untrusted.** The bodies above were written against the code as read, not
  run. Check every fixture assumption before believing a green: in particular that a fresh
  `ingestLogLimiter` has tokens (so the persist arm's line is ALLOWED and drops nothing), that
  `events.NewBroker()` needs no start, and that `AppendTaskLogRow`'s three destinations are
  `*int64`, `*pgtype.Timestamptz`, `*pgtype.UUID`.

## Backlog effects - proposed, not filed

The conductor files these; this plan does not.

- **Closes `idea-2026-08-14-tasklog-fence-rejection-is-unobservable`** via `/backlog close` (which does
  the `git mv` into `docs/backlog/closed/`). The resolution note should record the two amendments:
  (1) the code comment says per-reason splitting is DECLINED with its price written down, not
  "structurally impossible" (R1); (2) the typed-nil property is guarded by an added assertion on the
  shipped test rather than by a duplicated one, because one deps field means one nil filter (R6).
- **Candidate new item, conductor's call:** "the fence rejection counter cannot tell a misconfigured
  trailing window from a forged sender". R1 shows the split is expressible in one round trip by
  rewriting `AppendTaskLog` to return a row on the rejection path, and priced it. That is a real
  operator payoff (the counter would say *which* knob is wrong) behind a real risk (the `ErrNoRows`
  contract), and it is now a decision somebody can take with the numbers in front of them rather than a
  question nobody may ask.
- **No amendment is needed to the two sibling items** by this slice.

## Residual risk

1. **The forwarding proof is integration-only.** CI compiles it and never runs it (C9). A change that
   fed `TaskLogFence` a different `*worker.Handler` inside `buildHTTPServer` is green in the lane that
   decides whether a branch merges. Same limit slice 2 disclosed; the mitigation is that the presence
   test, the typed-nil test and the `main.go` identifier check are all in the default lane, and each
   answers a different question.
2. **One number cannot separate the legitimate late flush from a forged chunk.** Disclosed in the code
   comment, in the payload's doc and in README, and proposed as a follow-up above rather than hidden.
3. **M6's exactness half is likely inert at one CPU.** Measure rather than assume, and write the real
   numbers into the test comment. A test can be robust and inert on the same machine.
4. **`stubFenceDB` asserts a shape it does not own.** If `AppendTaskLog` ever grows a second statement,
   the stub panics rather than passing quietly - that is the intended failure, and it is why `Exec` and
   `Query` panic instead of returning zero values.
5. **Two sections now appear and disappear together in practice.** That is a property of this wiring
   (one object) rather than of the payload contract, and `api.CounterSources` keeps them independent so
   a future source can satisfy one and not the other. Stated in `buildHTTPServer`'s comment so nobody
   "simplifies" the two api fields into one interface.
