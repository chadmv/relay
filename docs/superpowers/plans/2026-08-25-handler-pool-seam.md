# Handler.pool Test Seam Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Narrow `Handler.pool` from the concrete `*pgxpool.Pool` to a one-method interface so that `internal/worker` gains its first default-lane route to a **successful** worker registration - and then spend that seam on behavioural tests for the success path, which CI has never executed.

**Architecture:** One production edit: `pool *pgxpool.Pool` becomes `pool txBeginner`, a single-method interface (`BeginTx`) copied from the `terminalTailStore` / `failClaimedStore` precedent in `internal/scheduler`. `*pgxpool.Pool` satisfies it, so `cmd/relay-server`'s wiring is unchanged and the three `pgx.BeginTxFunc(ctx, h.pool, ...)` call sites are unchanged source text. Everything else in this slice is test fixtures (a fake pool, a fake `pgx.Tx`, an empty `pgx.Rows`), four new default-lane tests, a reduction of the `go/parser` guard those tests make redundant, and six prose corrections.

**Tech Stack:** Go, pgx v5 (`pgx.BeginTxFunc`, `pgx.Tx`, `pgx.Rows`), sqlc-generated `internal/store`, gRPC bidi streams, `testify`. No SQL, no proto, no migration, no `make generate`, no frontend.

---

## Slice independence declaration

**This is a backend-only slice and its tasks are STRICTLY SEQUENTIAL. Do not fan Phase 3 out.**

- **Zero files under `web/` are touched.** Nothing operator-visible or API-visible changes: no HTTP handler, no REST payload, no SSE event shape, no proto message. The `worker` SSE frame this slice asserts on (`{"id":...,"status":"online"}`) is the frame the SPA already consumes today, unchanged. There is no frontend slice to run in parallel with, so the Phase 3 question does not arise.
- **The backend work cannot be parallelised either**, and the ordering is load-bearing rather than a preference:
  - Task 2's RED depends on Task 1's empty-`pgx.Rows` arm existing, or the panic lands four lines earlier in sqlc-generated code and the RED is a misdiagnosis rather than a proof.
  - Task 3's GREEN depends on Task 2's test existing and being red.
  - Tasks 5 and 6 depend on the seam Task 3 introduces.
  - **Task 7 (the guard reduction) must come last of the code tasks.** The backlog item says so in its own acceptance criteria - "Do not delete it before the behavioural test exists" - and spec section 7.2 restates it: both behavioural tests green in the default lane FIRST, then the clause deletions, then re-run, then the mutations that justify them.
- **No `make generate` step exists in this plan** and none is needed: no `internal/store/query/*.sql` and no `.proto` file is edited. If you find yourself editing a `.sql` file, stop - the plan is wrong, not the invariant. Never edit `*.sql.go` or `models.go`.

---

## What the RED actually is, task by task

This project's hard-won rule is that **a test seam must not destroy the RED**: reject any sequencing in which the headline test's failure at HEAD is "the symbol does not compile". Spec section 10 asserts both new tests panic at HEAD inside `applyInventory`. That is true only if the test can be BUILT at HEAD, and a test that assigns a fake to `Handler.pool` cannot be, because at HEAD that field is `*pgxpool.Pool`.

The sequencing below resolves that, and every task states which kind of RED it has:

| Task | Test | RED at that point | What proves each assertion is load-bearing |
| --- | --- | --- | --- |
| 2 -> 3 | `TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration` | **Genuine runtime RED.** The test is written with a **pool-less** handler - exactly today's `newStrandHandler` shape - and panics with a nil-pointer dereference inside `pgxpool.(*Pool).BeginTx`, reached from `handler.go:1743`. The **test function body does not change** between RED and GREEN; the only delta is one field in the fixture helper. | Mutations M1, M3, M4, M5, M6, M7 (Task 3) |
| 5 | `TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration` | **No RED is available and none is faked.** The behaviour it covers already works (PR #146 shipped it); at HEAD it is unbuildable for the same reason as above, and after Task 3 it passes on the first run. Its discriminating power comes from mutation M8 instead, run in the same task. | M8 |
| 6 | `TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone`, `TestFinishRegister_SucceedsWhenTheInventoryTransactionFails` | Same: these pin a non-goal (behaviour that must NOT change), so they are green on arrival by construction. | M9, M10 |
| 7 | the reduced guard | n/a - a deletion. | M13, M14, plus M2 and M8 re-run to prove what was KEPT still holds |

**The honest statement, which the plan makes rather than hides:** the panic RED proves the lane was structurally closed - which is the defect the backlog item names. It does not prove any individual assertion is worth anything, because no assertion is reached. That is what the mutation battery is for, and it is why the battery is distributed across the tasks that own each test rather than run once at the end.

---

## File inventory

| File | New / edit kind | What changes |
| --- | --- | --- |
| `internal/worker/handler.go` | **edit - additive + type change + one deletion** | Add the `txBeginner` interface near the `Handler` struct; change `pool`'s type at `:143` and the two constructor parameters at `:255` and `:261`; **delete the now-unused `pgxpool` import at `:23`**; correct the prose at `:753`. |
| `internal/worker/handler_register_success_test.go` | **NEW** (`package worker`, no build tag) | The whole default-lane success fixture and all four new tests. |
| `internal/worker/handler_register_strand_test.go` | **edit - additive** | `strandDB.Query` gains an empty-rows arm; `emptyRows` fake added; `newStrandHandler` gains the fake pool; header prose at `:30-37` corrected. No existing assertion changes. |
| `internal/worker/handler_registration_deadline_test.go` | **edit - additive** | `scriptedStream` gains a mutex-guarded send recorder (Task 1) and a `sendErr` field (Task 5). No existing assertion changes. |
| `internal/worker/handler_handoff_guard_test.go` | **edit - DELETION (~70 lines) + prose** | Clauses G3, G6, G7, G12, G15 and the `paramNamedByType` helper are removed, with the `strings` import and the `aliases` counter that only they used; header and two worked examples rewritten. |
| `internal/worker/handler_register_strand_integration_test.go` | **edit - prose only** | The justification at `:57-64` is falsified by this slice. The test itself is untouched and stays in the integration lane. |
| `docs/backlog/idea-2026-08-24-handler-pool-has-no-seam.md` | **`git mv` to `docs/backlog/closed/`** via `/backlog close` | Required scope, not cleanup. |
| `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` | **edit - additive note** | One named instance removed; the item stays open. |
| `docs/backlog/bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory.md` | **edit - additive note** | Its regression test is now a default-lane test; its line citations have drifted. Stays open. |

**Critical file:** `internal/worker/handler.go` - and the production diff in it is four lines of code plus one interface declaration. Everything else in this slice is tests and prose.

**Files that must NOT change:** any `*.sql`, any `*.sql.go`, `models.go`, `cmd/relay-server/main.go`, `internal/worker/registry.go`, `internal/worker/sender.go`, `internal/worker/grace.go`, and every `//go:build integration` test file except the one prose edit named above. `git diff --stat` at the end must list exactly the nine paths in the table.

---

## Two things in the spec that are wrong or under-specified

Called out rather than smoothed over.

1. **Spec 5.1 asks for an observable that is not reachable.** It says to assert "the returned `*workerSender`'s `connEpoch == strandEpoch`". A test driving `Connect` never sees that pointer: `finishRegister` returns it to `Connect`, which keeps it as a local, and `Registry` has no getter that hands a `Sender` back (`internal/worker/registry.go` exposes `Register`, `UnregisterIf`, `Send`, `SendEvictCommand`, `SendCancel` and nothing else). **The plan uses the teardown fence argument instead** - `teardownConnection` passes `sender.connEpoch` into `releaseWorkerGeneration`, which lands as `MarkWorkerOfflineIfEpoch`'s `$4`, and `strandDB` records it. That observable kills M4 exactly as well (binding `updated.MaxSlots` at `handler.go:713` makes the teardown fence carry the wrong int32, and `TestStrandFixture_EveryInt32ColumnScansDistinct` guarantees the two values differ). Do **not** add a registry getter to make the spec's wording literally true; that would be new production surface for a test's convenience.

2. **Spec 4.2 names the empty-`pgx.Rows` fake but never says where it plugs in, and the answer is a THIRD edit to an existing default-lane file** that the spec's own regression scope (section 10) does not list among the permitted ones. `reconcileRunningTasks` reaches the rows through `strandDB.Query`, which today returns `(nil, d.queryErr)`; with a nil `queryErr` that is `(nil, nil)`, and sqlc's `:many` body calls `defer rows.Close()` on a nil interface. So `strandDB.Query` must gain an arm. Task 1 does it, proves it inert (every one of the five existing construction sites sets `queryErr` non-nil), and the spec's regression scope should be read as four permitted edits, not three.

Two smaller notes, not defects: the spec's `handler.go:1743` and `:693` citations were re-derived against this worktree and are correct at `1097211`; and the `newTestPoolAndQueries` comment in `cmd/relay-server/grpc_admission_e2e_integration_test.go:47-51`, which also talks about `pgx.BeginTxFunc(ctx, h.pool, ...)`, was checked and stays **true** - it explains why an integration harness needs the real pool, which this slice does not change. Task 8 re-reads it and leaves it alone.

---

## Task 1: Give the two existing default-lane fixtures what a SUCCESS path needs

The success path needs two things today's fixtures do not supply: a `pgx.Rows` that is genuinely empty rather than nil, and a stream that remembers what it was sent. Both are additive edits to existing files with existing users, so both are proven inert before anything else is built on them.

**Files:**
- Modify: `internal/worker/handler_register_strand_test.go:62-70` (the `Query` method) and add `emptyRows` below it
- Modify: `internal/worker/handler_registration_deadline_test.go:1-15` (imports), `:26-33` (the struct), `:52` (`Send`)

- [ ] **Step 1: Add the empty-rows arm to `strandDB.Query`**

In `internal/worker/handler_register_strand_test.go`, replace the whole `Query` method (currently `:62-70`, comment included) with:

```go
// Query records what it was asked as well as answering it. RequeueWorkerTasksIfEpoch
// is a :many, so the else arm of releaseWorkerGeneration lands here rather than
// in Exec, and the statement it issues is the only evidence that arm ran.
//
// A NIL pgx.Rows WITH A NIL ERROR IS NOT AN EMPTY RESULT, and it used to be what
// this returned. sqlc's :many body does `rows, err := q.db.Query(...)` and then
// `defer rows.Close()` on the very next line (internal/store/tasks.sql.go), so
// (nil, nil) panics on a nil interface receiver inside generated code - one frame
// short of applyInventory, and easy to misread as the pool panic it sits next to.
// A fixture that means "this worker has no active tasks" has to say so with a
// Rows that exists.
func (d *strandDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = append(d.queries, strandExec{sql: sql, args: args})
	if d.queryErr != nil {
		return nil, d.queryErr
	}
	return emptyRows{}, nil
}

// emptyRows is a pgx.Rows carrying no rows at all: Next says there are none,
// Close is a no-op and Err reports success. That is the whole of what a
// generated :many body calls when a result set is empty.
//
// THE EMBEDDED NIL INTERFACE SUPPLIES THE OTHER SIX METHODS AS A PANIC, which is
// the fail-loud choice fenceStore makes in internal/scheduler and the right
// report if a query on this path ever grows a Scan or a Values: a nil
// dereference naming the method beats a plausible zero value that silently makes
// a test prove less.
type emptyRows struct{ pgx.Rows }

func (emptyRows) Close()     {}
func (emptyRows) Next() bool { return false }
func (emptyRows) Err() error { return nil }
```

- [ ] **Step 2: Prove the new arm is unreachable from every existing test**

Run:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && grep -rn "strandDB{\|newStrandHandler(" internal/worker/*_test.go
```

Expected: five construction sites - four `newStrandHandler(t, errors.New(...), ...)` calls and one `&strandDB{execTag: "UPDATE 1", queryErr: errors.New("no rows fixture")}` in `TestReleaseWorkerGeneration_WithoutAGraceRegistryRequeuesImmediately`. **Every one passes a non-nil `queryErr`,** so no existing test can take the new branch. If you find a site with a nil `queryErr`, stop: that test's behaviour is about to change and this step is the place to notice.

- [ ] **Step 3: Give `scriptedStream` a send recorder**

In `internal/worker/handler_registration_deadline_test.go`, add `"sync"` to the import block (after `"context"`), then replace the struct at `:26-33` with:

```go
type scriptedStream struct {
	msgs     []*relayv1.AgentMessage
	pos      int
	ctx      context.Context
	release  chan struct{}
	recvDone chan struct{} // closed by the Recv that returns after release
	delay    time.Duration // sleep before handing back the first message

	// mu guards sent, and it is load-bearing rather than defensive. The
	// successful-registration tests next door run Connect on their own goroutine
	// and read this slice from the test goroutine, and the sends arrive from TWO
	// goroutines even within Connect: finishRegister writes the RegisterResponse
	// directly, and every send after that goes through the workerSender's send
	// loop. CI runs `go test -race ./...`, which reports an unguarded slice as a
	// failure rather than as a flake.
	mu   sync.Mutex
	sent []*relayv1.CoordinatorMessage
}
```

and replace the `Send` one-liner at `:52` with:

```go
// Send records what it was asked to deliver. It used to discard its argument,
// which is what made "the RegisterResponse was actually sent" unobservable in
// this lane - the message never left the stream fake, so no default-lane test
// could tell a sent response from a deleted send.
func (s *scriptedStream) Send(m *relayv1.CoordinatorMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	return nil
}

// sentMsgs returns a copy of what has been sent so far, so callers never read
// the slice the send goroutine is appending to.
func (s *scriptedStream) sentMsgs() []*relayv1.CoordinatorMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*relayv1.CoordinatorMessage, len(s.sent))
	copy(out, s.sent)
	return out
}
```

- [ ] **Step 4: Re-run every existing user of both fixtures and confirm the edits were inert**

Run:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -v -timeout 120s -run 'TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration|TestConnect_ASupersededFailedRegistrationReleasesNothing|TestConnect_AFailedRegistrationStillReleasesWhenTheOfflineWriteERRORS|TestConnect_AFailedRegistrationReplacesThePreviousDisconnectsTimer|TestStrandFixture_EveryInt32ColumnScansDistinct|TestReleaseWorkerGeneration_WithoutAGraceRegistryRequeuesImmediately|TestConnect_SilentPeerIsDisconnectedAtTheRegistrationDeadline|TestConnect_RegistrationArrivingInTimeIsNotDisconnected|TestConnect_RegistrationSlowerThanTheDeadlineIsCutOff|TestConnect_FirstMessageMustStillBeARegisterRequest|TestRegistrationTimeout_ZeroMeansTheDefault'
```

Expected: **eleven `--- PASS` lines**, `ok relay/internal/worker`.

**What proves the edits were inert, stated as evidence rather than as a green run:** (a) Step 2 established that no existing test reaches the new `Query` branch, so `strandDB`'s behaviour for all of them is byte-for-byte what it was; (b) `Send` still returns `nil` unconditionally and no existing test reads `sent` or calls `sentMsgs`, so the recorder is write-only from every existing caller's point of view; (c) the four strand tests each carry a `require.Contains(err.Error(), "reconcile")` **before** their real assertions, which is precisely the check that would fail if `Query` had stopped erroring for them.

- [ ] **Step 5: Run the whole default lane, then commit**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go build ./... && go test ./internal/worker/... -timeout 300s
```

Expected: `ok relay/internal/worker`.

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && git add internal/worker/handler_register_strand_test.go internal/worker/handler_registration_deadline_test.go && git commit -m "test(worker): teach the default-lane fixtures to answer a SUCCESSFUL registration

strandDB.Query returned (nil, nil) for a nil queryErr, and sqlc's :many body
calls rows.Close() on the next line - so a fixture that meant 'this worker has
no active tasks' panicked on a nil interface four lines short of the pool.
emptyRows is the smallest thing that is genuinely a result set with no rows.

scriptedStream.Send discarded its argument, which made 'the RegisterResponse was
actually sent' unobservable in the lane CI runs. It now records under a mutex,
because the tests that read it drive Connect on another goroutine and CI runs
-race.

Both edits are additive and unreachable from every existing user: all five
strandDB construction sites set queryErr non-nil, and nothing reads sent."
```

---

## Task 2: The success path, written against a pool-less handler (RED)

**Files:**
- Create: `internal/worker/handler_register_success_test.go`
- Read for context: `internal/worker/handler.go:604-803` (`finishRegister`), `:266-354` (`Connect`), `internal/worker/handler_register_strand_test.go:38-230` (the fakes being reused)

- [ ] **Step 1: Write the new test file**

Create `internal/worker/handler_register_success_test.go` with exactly this content:

```go
package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/metrics"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// successFixture is a Handler whose reconnect registration SUCCEEDS, plus one
// observable for every effect finishRegister has below applyInventory. It reuses
// strandDB, strandWorkerRow, strandWorkerID, strandEpoch, graceFire and
// scriptedStream from the two files next door rather than re-declaring them -
// same package, one fixture family.
type successFixture struct {
	h          *Handler
	db         *strandDB
	stream     *scriptedStream
	metrics    *metrics.Store
	events     <-chan events.Event
	dispatched chan struct{}
	fired      <-chan graceFire
	release    func()
}

// newSuccessFixture builds the handler.
//
// THE ONE THING THAT MAKES IT DIFFERENT from newStrandHandler is the absent
// queryErr: with none, strandDB.Query hands back an empty pgx.Rows,
// GetActiveTasksForWorker returns no rows, and reconcileRunningTasks becomes a
// no-op instead of the failure the four strand tests drive. Empty rather than
// populated is deliberate - reconcile's CONTENT is covered by
// handler_reconcile_canonical_test.go in the integration lane, and a populated
// fixture here would add failure modes without adding coverage of what is under
// test, which is everything BELOW reconcile.
//
// Metrics IS set here and is nil in newStrandHandler, also deliberately: the
// Metrics.Activate call near the bottom of finishRegister is one of the effects
// under test, and LastSampleAt is how it is read back.
func newSuccessFixture(t *testing.T) *successFixture {
	t.Helper()

	db := &strandDB{execTag: "UPDATE 1"}

	fired := make(chan graceFire, 4)
	grace := NewGraceRegistry(20*time.Millisecond, func(workerID string, epoch int32) {
		fired <- graceFire{workerID: workerID, epoch: epoch}
	})
	t.Cleanup(grace.Stop)

	// A BUFFERED SEND, NOT A CLOSE. triggerDispatch is called once by
	// finishRegister and again by requeueWorkerTasks on the no-grace release arm,
	// so closing a channel here would turn a second call into a panic that reports
	// the wrong thing entirely. Capacity 4 keeps every call non-blocking, which
	// matters because one of them runs on a goroutine nothing joins.
	dispatched := make(chan struct{}, 4)

	broker := events.NewBroker()
	evs, unsubscribe := broker.Subscribe(events.Filter{})
	t.Cleanup(unsubscribe)

	ms := metrics.NewStore(8)

	h := &Handler{
		q:        store.New(db),
		registry: NewRegistry(),
		broker:   broker,
		grace:    grace,
		triggerDispatch: func() {
			select {
			case dispatched <- struct{}{}:
			default:
			}
		},
		Metrics:             ms,
		RegistrationTimeout: 5 * time.Second,
	}

	s := &scriptedStream{
		ctx:     context.Background(),
		release: make(chan struct{}),
		msgs: []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname:   "strand-host",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: "strand-agent-token"},
			},
		}}},
	}
	// OnceFunc because the release is closed by the test that wants to observe
	// teardown AND by the cleanup that covers the tests that do not; a second
	// close of the same channel would panic.
	release := sync.OnceFunc(func() { close(s.release) })
	t.Cleanup(release)

	return &successFixture{
		h:          h,
		db:         db,
		stream:     s,
		metrics:    ms,
		events:     evs,
		dispatched: dispatched,
		fired:      fired,
		release:    release,
	}
}

// startConnect runs Connect on its own goroutine and blocks until the worker
// event finishRegister publishes arrives. That event is the barrier every
// assertion rests on: it is published second-to-last on the success path, so by
// the time it is observed the registry entry, the metrics activation and the
// RegisterResponse are all already in place and will not move. scriptedStream.Recv
// then parks in the message loop, which is what keeps them stable while the test
// reads them.
//
// Returns that event and a func that tears the stream down and waits for Connect
// to return.
//
// THE TIMEOUT IS THE FAILURE REPORT FOR HALF THE MUTATION BATTERY. Deleting the
// publish, or turning applyInventory's swallowed error into a return, both end
// with no event arriving - and that has to be one bounded, explanatory failure
// rather than a hung package.
func (f *successFixture) startConnect(t *testing.T) (events.Event, func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.h.Connect(f.stream) }()

	finish := func() error {
		f.release()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Error("Connect did not return after the stream was torn down, so the teardown half of " +
				"this test observed nothing")
			return nil
		}
	}

	select {
	case ev := <-f.events:
		return ev, finish
	case <-time.After(5 * time.Second):
		select {
		case err := <-done:
			t.Fatalf("the registration ended without publishing a worker event: %v. finishRegister "+
				"publishes 'online' as its second-to-last statement, so no event means the success path "+
				"was never reached and every assertion below would be about a registration that did not "+
				"happen.", err)
		default:
			t.Fatal("no worker event was published within 5s and Connect is still running. " +
				"finishRegister publishes 'online' immediately before `go h.triggerDispatch()`; without " +
				"it the SPA and GET /v1/workers never learn the agent connected.")
		}
		return events.Event{}, finish
	}
}

// TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration is
// the first default-lane test in this repository to reach a SUCCESSFUL worker
// registration, and it exists because everything it asserts could previously
// only be pinned by reading source text.
//
// IT IS DRIVEN THROUGH Connect, NOT finishRegister, and that is the whole design.
// The handedOff flag partitions a window between TWO releases -
// finishRegister's own deferred releaseWorkerGeneration and Connect's
// `defer h.teardownConnection` - and calling finishRegister directly sees only
// one of them. The property worth pinning is that across the connection's whole
// life the generation is released EXACTLY ONCE, and that the one release is
// teardown's. Delete `handedOff = true` and the count becomes two, with the
// first landing before the mid-connection assertions below: both halves redden
// independently, which is what makes the assertion hard to satisfy by accident.
func TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration(t *testing.T) {
	f := newSuccessFixture(t)
	workerID := uuidStr(strandWorkerID)

	online, finish := f.startConnect(t)

	require.Equal(t, "worker", online.Type)
	assert.JSONEq(t, `{"id":"`+workerID+`","status":"online"}`, string(online.Data),
		"a successful registration publishes exactly one worker event and it says online; this frame "+
			"is what GET /v1/workers subscribers and the SPA render from")

	select {
	case <-f.dispatched:
	case <-time.After(5 * time.Second):
		t.Fatal("the registration did not trigger dispatch. A worker that comes online while tasks " +
			"sit pending then waits for the scheduler's next poll instead of being fed immediately.")
	}

	// THE GENERATION WAS NOT RELEASED. This wait comes FIRST because it is the
	// slowest of the three signals: the release does markWorkerOffline and only
	// then grace.Start, so 300ms of silence on a 20ms grace window means no
	// release ran at all, and the two cheaper assertions after it cannot then be
	// racing a release that is still in flight.
	select {
	case fire := <-f.fired:
		t.Fatalf("a grace timer was armed for %s at epoch %d by a SUCCESSFUL registration. That timer "+
			"requeues a healthy agent's running tasks one grace window from now, out from under a "+
			"connection that is up and serving.", fire.workerID, fire.epoch)
	case <-time.After(300 * time.Millisecond):
	}

	assert.Empty(t, f.db.execsSeen(),
		"a successful registration must issue no statement at all before teardown. The only Exec on "+
			"this path is MarkWorkerOfflineIfEpoch, so anything here means the generation this "+
			"registration just acquired was released by the registration itself - the live agent "+
			"published 'offline' and its metrics entry wiped the instant it came online.")

	select {
	case ev := <-f.events:
		t.Fatalf("a second worker event was published mid-connection: %s. One registration publishes "+
			"one event; a second means something below the publish released the generation.", ev.Data)
	default:
	}

	// Read the stream BEFORE probing the registry: the probe below goes through
	// the workerSender's queue and lands in this same slice a moment later.
	sent := f.stream.sentMsgs()
	require.Len(t, sent, 1,
		"exactly one message must have reached the raw stream: the RegisterResponse finishRegister "+
			"sends before anything else can race it")
	rr := sent[0].GetRegisterResponse()
	require.NotNil(t, rr,
		"the one message must be the RegisterResponse; the agent blocks on it and cannot run a task "+
			"until it arrives")
	assert.Equal(t, workerID, rr.WorkerId,
		"the response carries the id the agent uses for every later message on this stream")
	assert.Empty(t, rr.CancelTaskIds,
		"the agent reported no running tasks and the coordinator has none assigned, so reconcile has "+
			"nothing to cancel")

	require.NoError(t, f.h.registry.Send(workerID, &relayv1.CoordinatorMessage{}),
		"the sender must be in the registry. Registry.Send is the ONLY route the dispatcher has to a "+
			"connected agent, and it answers a missing entry with `worker is not connected` - so an "+
			"unregistered sender is an agent that is online, idle and unreachable.")

	at, tracked := f.metrics.LastSampleAt(workerID)
	require.True(t, tracked,
		"Metrics.Activate must have run. An untracked worker is skipped by the liveness sweeper "+
			"(internal/metrics/sweep.go), which is the one runtime mechanism that flips a "+
			"connected-looking worker to 'stale' - so an agent that stops reporting is never noticed.")
	assert.False(t, at.IsZero(),
		"Activate seeds activatedAt, and LastSampleAt returns it until the first telemetry sample "+
			"arrives; a zero time means the activation carried no clock")

	require.Error(t, finish(),
		"tearing the stream down ends Connect with the Recv error, which is how a real disconnect "+
			"arrives")

	execs := f.db.execsSeen()
	require.Len(t, execs, 1,
		"across the connection's whole life the generation must be released EXACTLY ONCE, and this is "+
			"it: teardown's. Two means finishRegister released its own generation as well, which is the "+
			"failure the handedOff flag exists to prevent; zero means a disconnected agent's worker row "+
			"stays 'online' forever with its tasks assigned to a stream that has closed.")
	assert.Contains(t, execs[0].sql, "status = 'offline'",
		"the one statement must be MarkWorkerOfflineIfEpoch")
	assert.Contains(t, execs[0].sql, "connection_epoch = $4",
		"and it must carry its epoch fence")
	assert.Contains(t, execs[0].args, strandEpoch,
		"the fence must carry THIS connection's epoch, which is the value RegisterWorkerConnection "+
			"returned and workerSender.connEpoch was set from. Every int32 column of the stub row "+
			"scans distinct (TestStrandFixture_EveryInt32ColumnScansDistinct), so binding any other "+
			"one at the assignment is visible right here.")

	select {
	case fire := <-f.fired:
		assert.Equal(t, workerID, fire.workerID)
		assert.Equal(t, strandEpoch, fire.epoch,
			"the grace timer must be armed at the epoch whose generation just ended; "+
				"RequeueWorkerTasksIfEpoch fences on workers.connection_epoch, so a timer at any other "+
				"epoch requeues nothing and the disconnected agent's tasks are stranded")
	case <-time.After(3 * time.Second):
		t.Fatal("teardown armed no grace timer, so the disconnected worker's running tasks have no " +
			"requeue scheduled at all - the only thing left is the 24h stale-task watchdog, which " +
			"marks them timed_out rather than re-running them")
	}

	select {
	case fire := <-f.fired:
		t.Fatalf("a second grace timer fired, at epoch %d. One ended generation schedules one requeue.",
			fire.epoch)
	case <-time.After(200 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run it and confirm the RED is the STATED one**

Run:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration -v -timeout 60s
```

Expected: the test binary **panics** (it does not fail an assertion - it never reaches one), with a stack whose top frames read approximately:

```
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=... addr=0x0 pc=...]

goroutine NN [running]:
github.com/jackc/pgx/v5/pgxpool.(*Pool).BeginTx(...)
github.com/jackc/pgx/v5.BeginTxFunc(...)
relay/internal/worker.(*Handler).applyInventory(...)
	.../internal/worker/handler.go:1743 +0x...
relay/internal/worker.(*Handler).finishRegister(...)
	.../internal/worker/handler.go:693 +0x...
FAIL	relay/internal/worker
```

**This is the gate on the RED being real, and it has two halves.**

1. The panic frame must be **`applyInventory` at `handler.go:1743`**, reached through `pgxpool.(*Pool).BeginTx`. That is the defect: a nil pool, a transaction opened on it unconditionally, and the entire success path below it unreachable.
2. It must **not** be inside `internal/store/tasks.sql.go` at `rows.Close()`. If it is, Task 1's `emptyRows` arm is not being taken - re-check that `newSuccessFixture` leaves `queryErr` unset - and the panic you are looking at is finding F1, not the pool.

If instead the test fails with `no worker event was published within 5s`, the registration failed for some third reason before applyInventory; read the error `startConnect` printed and fix the fixture before proceeding. A fixture that fails early is not evidence of anything.

- [ ] **Step 3: Do NOT commit**

The tree has a panicking test. Task 3 makes it pass and the two commit together.

---

## Task 3: Narrow `Handler.pool` to `txBeginner` (GREEN), and mutate

**Files:**
- Modify: `internal/worker/handler.go:23` (delete the `pgxpool` import), `:139-143` (the interface + the field), `:254-263` (both constructors)
- Modify: `internal/worker/handler_register_success_test.go` (add `fakePool`, `fakeTx`, and three fixture lines)

- [ ] **Step 1: Declare the interface and change the field**

In `internal/worker/handler.go`, insert the following immediately **above** the `// Handler implements relayv1.AgentServiceServer.` comment at `:139`:

```go
// txBeginner is the subset of *pgxpool.Pool this package uses: the single method
// pgx.BeginTxFunc requires of its second argument, which is itself declared as an
// anonymous interface (pgx/tx.go). Handler.pool is typed as this rather than as
// the concrete pool for the same reason internal/scheduler narrowed
// failClaimedStore - it is what makes finishRegister's SUCCESS path drivable by a
// fake, without Postgres, and therefore in the lane CI actually runs.
//
// THREE CALL SITES SHARE IT, not one: enrollAndRegister, autoEnrollAndRegister
// and applyInventory all open their transaction with the identical expression
// pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, ...) and differ only in the
// closure they pass.
//
// *pgxpool.Pool satisfies it, so cmd/relay-server's wiring is unchanged and
// production behaviour is identical. The field keeps the name `pool` because in
// production it still is one; this comment carries the type's real meaning.
type txBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}
```

Then change the field at what is now `Handler`'s fourth line:

```go
	pool            txBeginner
```

- [ ] **Step 2: Change both constructor signatures and delete the dead import**

Replace `internal/worker/handler.go:254-263` with:

```go
// NewHandler returns a Handler wired to the given dependencies. pool is a
// txBeginner, which *pgxpool.Pool satisfies; see that type for why.
func NewHandler(q *store.Queries, pool txBeginner, r *Registry, b *events.Broker, triggerDispatch func()) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch}
}

// NewHandlerWithGrace is like NewHandler but also wires in a GraceRegistry so
// that agent disconnects start a grace timer instead of immediately requeueing.
func NewHandlerWithGrace(q *store.Queries, pool txBeginner, r *Registry, b *events.Broker, triggerDispatch func(), g *GraceRegistry) *Handler {
	return &Handler{q: q, pool: pool, registry: r, broker: b, triggerDispatch: triggerDispatch, grace: g}
}
```

Then **delete line 23 of `internal/worker/handler.go`**:

```go
	"github.com/jackc/pgx/v5/pgxpool"
```

Those three sites were its only uses in this file; leaving it is `imported and not used`, which is a compile error. Confirm with:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && grep -n "pgxpool" internal/worker/handler.go
```

Expected: **no output.**

- [ ] **Step 3: Add the pool and transaction fakes to the test file**

In `internal/worker/handler_register_success_test.go`, add `"github.com/jackc/pgx/v5"` and `"github.com/jackc/pgx/v5/pgconn"` to the third import group, and add these types immediately below the `successFixture` struct:

```go
// fakePool is the txBeginner a default-lane Handler gets instead of a
// *pgxpool.Pool. It hands out one fakeTx, so a test can read back what the
// transaction was asked to do.
type fakePool struct {
	tx       *fakeTx
	beginErr error
}

func (p *fakePool) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
}

// fakeTx is a pgx.Tx that records the statements applyInventory issues.
//
// THE EMBEDDED NIL pgx.Tx SUPPLIES THE EIGHT METHODS THIS PATH NEVER CALLS, and
// supplies them as a panic rather than as a plausible zero value - the same
// fail-loud choice fenceStore makes in internal/scheduler, and the right report
// if applyInventory ever grows a Query or a SendBatch. The idiom is what makes
// an eleven-method interface cost four lines instead of forty.
//
// Commit and Rollback both return nil, which is correct rather than merely
// convenient: pgx's beginFuncExec defers a Rollback AFTER the Commit and
// propagates a rollback error only when it is not ErrTxClosed, so a nil from
// both is the quiet, successful shape.
type fakeTx struct {
	pgx.Tx
	mu      sync.Mutex
	execErr error
	execs   []strandExec
}

func (tx *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.execs = append(tx.execs, strandExec{sql: sql, args: args})
	if tx.execErr != nil {
		return pgconn.CommandTag{}, tx.execErr
	}
	return pgconn.NewCommandTag("DELETE 0"), nil
}

func (tx *fakeTx) Commit(context.Context) error   { return nil }
func (tx *fakeTx) Rollback(context.Context) error { return nil }

// execsSeen is a SEPARATE list from strandDB's, and keeping them apart is what
// lets "the deferred release did not fire" stay an exact count of zero. Let
// ReplaceWorkerInventory land in the same list as MarkWorkerOfflineIfEpoch and
// that assertion weakens from a count to a substring match.
func (tx *fakeTx) execsSeen() []strandExec {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	out := make([]strandExec, len(tx.execs))
	copy(out, tx.execs)
	return out
}
```

- [ ] **Step 4: Wire the fake pool into the fixture - three lines, and no change to any test body**

In `successFixture`, add one field below `db`:

```go
	tx         *fakeTx
```

In `newSuccessFixture`, add one line below `db := &strandDB{execTag: "UPDATE 1"}`:

```go
	tx := &fakeTx{}
```

add one line to the `&Handler{...}` literal, immediately below `q: store.New(db),`:

```go
		pool:     &fakePool{tx: tx},
```

and one line to the returned struct literal, below `db: db,`:

```go
		tx:         tx,
```

**Nothing inside `func TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration` changes.** Verify that before running anything:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && git diff -U0 internal/worker/handler_register_success_test.go | grep -c "^[+-]"
```

The test function is untracked at this point, so instead read the diff of your own edit against Task 2's version by eye: the four added lines above and the fake types, and no line inside the `func Test...` block. **This is the check that the test which goes green is the test that was red.**

- [ ] **Step 5: Run and confirm GREEN**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration -v -timeout 60s
```

Expected: `--- PASS`, `ok relay/internal/worker`.

- [ ] **Step 6: Confirm the whole tree still builds, including the integration lane**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go build ./... && go vet ./... && go vet -tags integration ./...
```

Expected: all three silent. The integration lane and `cmd/relay-server` compile unchanged because every existing caller passes either a real `*pgxpool.Pool` (which satisfies `txBeginner`) or an untyped `nil` (assignable to any interface) - `cmd/relay-server/main.go:142`, `cmd/relay-server/counters_wiring_test.go:235,631,668,1033`, and the `worker.NewHandler(q, pool, ...)` calls throughout `internal/worker/*_integration_test.go`.

- [ ] **Step 7: The mutation battery for this test - M1, M3, M4, M5, M6, M7**

Six mutations, each in `internal/worker/handler.go`. **The procedure is the same for every one and every step of it is mandatory**, because CRLF in this tree has silently defeated four mutations in a row: a mutation that did not apply reports "survived" and reads exactly like good news.

For each: (a) make the edit, (b) run the **applied-check** command and confirm its stated output, (c) run the test, (d) confirm the stated failure, (e) revert with `git checkout -- internal/worker/handler.go` and re-run to confirm PASS again.

The test command throughout is:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration -v -timeout 60s
```

| # | Edit | Applied-check | Must FAIL at |
| --- | --- | --- | --- |
| **M1** | Delete the line `	handedOff = true` (`:789`) | `grep -c "handedOff = true" internal/worker/handler.go` -> `0` | the grace-fire `t.Fatalf` ("a grace timer was armed ... by a SUCCESSFUL registration"), and if that is fixed, three more: `execsSeen` non-empty, a second worker event, and a teardown count of 2. **This is the primary target of the whole slice** - the line the 2026-08-24 slice paid 669 guard lines to pin. |
| **M3** | Delete `	h.registry.Register(workerID, sender)` (`:714`) | `grep -c "h.registry.Register(" internal/worker/handler.go` -> `0` | `require.NoError(t, f.h.registry.Send(...))` with `worker "..." is not connected` |
| **M4** | `sender.connEpoch = updated.ConnectionEpoch` -> `sender.connEpoch = updated.MaxSlots` (`:713`) | `grep -n "sender.connEpoch = updated.MaxSlots" internal/worker/handler.go` -> one hit | `assert.Contains(t, execs[0].args, strandEpoch)` and the grace-fire epoch assertion. Distinctness is guaranteed by `TestStrandFixture_EveryInt32ColumnScansDistinct`. |
| **M5** | Delete the three-line `if h.Metrics != nil { h.Metrics.Activate(...) }` (`:791-793`) | `grep -c "h.Metrics.Activate(" internal/worker/handler.go` -> `0` | `require.True(t, tracked, "Metrics.Activate must have run...")` |
| **M6** | Delete the four-line `h.broker.Publish(...)` with `"status":"online"` (`:795-798`) | `grep -c 'status\\":\\"online' internal/worker/handler.go` -> `0` | `startConnect`'s bounded 5s timeout, with "no worker event was published within 5s and Connect is still running". **Must be a timeout with that message, never a hang.** |
| **M7** | Delete `	go h.triggerDispatch()` (`:800`) | `grep -c "go h.triggerDispatch()" internal/worker/handler.go` -> `1` (the surviving one is in `requeueWorkerTasks`); confirm with `grep -n` that the remaining hit is inside `requeueWorkerTasks` | the dispatch wait's `t.Fatal("the registration did not trigger dispatch...")`, bounded at 5s |

**M11, run once and with a different expectation:** revert `Handler.pool` to `*pgxpool.Pool` (restoring the import) and run `go build ./...` plus the test command. Expected: a **compile error** in `handler_register_success_test.go` - `cannot use &fakePool{...} as *pgxpool.Pool`. That is the point: the seam cannot silently regress. A compile error is not a behavioural kill and is not counted as one; it is a type-level guard, recorded as such. Revert.

**M2 is a DELIBERATE KNOWN SURVIVOR. Do not "fix" it.** Change `handedOff = true` to `if h.Metrics != nil { handedOff = true }`, applied-check `grep -c "if h.Metrics != nil { handedOff = true }" internal/worker/handler.go` -> `1`, and run:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run 'TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration|TestFinishRegisterHandsOffOwnershipInsideTheWindow' -v -timeout 60s
```

Expected: the behavioural test **PASSES** (the new fixture sets `Metrics`, so the condition is true and the flip happens) and `TestFinishRegisterHandsOffOwnershipInsideTheWindow` **FAILS** on guard clause G14 with "is set to true at ..., but that assignment is nested inside another statement". **This is the measurement that proves G14 must survive Task 7's reduction**, and the pair PASS/FAIL is also the battery's health check: a uniform result across a battery means the harness is broken, not that coverage is good. Record it, revert it.

- [ ] **Step 8: Run the full default lane with the race detector, then commit**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && CC=/c/msys64/mingw64/bin/gcc.exe go test ./internal/worker/... -race -timeout 600s
```

Expected: `ok relay/internal/worker`. (`-race` on this machine needs MSYS2 mingw64 gcc; the default Strawberry Perl gcc fails every package with exit `0xc0000139`.)

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && make test
```

Expected: every package `ok` or `no test files`.

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && git add internal/worker/handler.go internal/worker/handler_register_success_test.go && git commit -m "feat(worker): narrow Handler.pool to a txBeginner, and drive a successful registration without Postgres

applyInventory opens a transaction on the concrete *pgxpool.Pool
unconditionally, between reconcileRunningTasks and the RegisterResponse send, so
a default-lane fixture panicked there and everything below it - registry.Register,
the ownership handoff, Metrics.Activate, the online broker event, the dispatch
trigger - could only be exercised behind //go:build integration, which CI does
not run.

Handler.pool is now txBeginner, the single method pgx.BeginTxFunc requires. It is
the same narrowing internal/scheduler applied to failClaimedStore and for the
same stated reason. *pgxpool.Pool satisfies it, so cmd/relay-server's wiring and
every integration fixture compile and behave unchanged; the three
pgx.BeginTxFunc(ctx, h.pool, ...) call sites are unchanged source text.

The first behavioural test of the success path asserts, through Connect: the
RegisterResponse was sent, the sender is reachable via Registry.Send, metrics are
activated, the worker publishes online exactly once, dispatch is triggered, and
the generation is released EXACTLY ONCE across the connection's life - by
teardown, fenced on this connection's epoch.

Mutations M1 and M3-M7 each redden it; M2 (wrapping the flip in an h.Metrics nil
check) survives it and is killed by the structural guard's G14 clause, which is
why that clause stays."
```

---

## Task 4: Give `newStrandHandler` the fake pool

Two lines, and they close an evasion class rather than fixing a bug. The handoff guard was evaded twice in the 2026-08-24 slice, both times by a construct that is **nil in every default-lane fixture and real under `main.go`** - first `h.Metrics != nil`, then `h.pool != nil`. Filling the second one in the fixture is what turns that specific evasion from invisible into caught.

**Files:**
- Modify: `internal/worker/handler_register_strand_test.go:194-211` (`newStrandHandler`)

- [ ] **Step 1: Add the pool**

In `newStrandHandler`, add one line below `db := &strandDB{queryErr: queryErr, execTag: execTag}`:

```go
	pool := &fakePool{tx: &fakeTx{}}
```

and one line to the `&Handler{...}` literal, below `q: store.New(db),`:

```go
		pool:                pool,
```

Then extend that helper's doc comment with:

```go
// THE POOL IS NON-NIL EVEN THOUGH NO TEST HERE REACHES IT, and that is the
// point. All four tests below fail inside reconcileRunningTasks, four lines
// above applyInventory, so the pool is never touched and this changes none of
// their behaviour. What it changes is what a MUTATION can hide behind: a
// construct that is nil in every default-lane fixture and real under main.go is
// exactly how this package's handoff guard was evaded twice, and
// `if h.pool != nil { return }` ahead of the deferred release was the second of
// those two. With a pool here, that edit makes these tests red instead of green.
```

- [ ] **Step 2: Re-run the four existing strand tests and confirm nothing moved**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -v -timeout 120s -run 'TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration|TestConnect_ASupersededFailedRegistrationReleasesNothing|TestConnect_AFailedRegistrationStillReleasesWhenTheOfflineWriteERRORS|TestConnect_AFailedRegistrationReplacesThePreviousDisconnectsTimer'
```

Expected: four `--- PASS`. **What proves the edit was inert for them:** each of the four carries `require.Contains(err.Error(), "reconcile")` as its second statement, and that assertion is upstream of `applyInventory` - so if the pool had come into play at all, the arm being driven would have changed and that require would be the first thing to notice.

- [ ] **Step 3: M12 - the second deliberate known survivor. Do not "fix" it.**

First run a **control** so the harness is known good: apply M1 again (delete `handedOff = true`) and confirm `TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration` FAILS. Revert.

Then remove the two lines you just added (the `pool := ...` and the `pool: pool,`) and run the four strand tests plus the success test:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -timeout 300s
```

Expected: **everything still passes.** M12 kills nothing **by design** - the fixture pool has no behavioural consumer, and it is not supposed to have one. Its whole value is measured by M13 in Task 7, which is `if h.pool != nil { return }` in the deferred closure: M13 alone is now caught, M12 alone changes nothing, and **M12 composed with M13 is precisely the 2026-08-24 evasion**. Record M12 as a known survivor in the task notes so nobody later deletes the fixture pool as dead weight. Restore the two lines.

- [ ] **Step 4: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -timeout 300s && git add internal/worker/handler_register_strand_test.go && git commit -m "test(worker): give newStrandHandler a non-nil pool

The handoff guard was evaded twice by constructs that are nil in every
default-lane fixture and real under main.go - h.Metrics != nil, then
h.pool != nil. The second of those is now false in this fixture too, so
\`if h.pool != nil { return }\` ahead of the deferred release reddens these four
tests instead of passing them.

Inert for all four: each fails inside reconcileRunningTasks, four lines above the
applyInventory call that would touch the pool."
```

---

## Task 5: The RegisterResponse-send arm, in the default lane

**Files:**
- Modify: `internal/worker/handler_registration_deadline_test.go` (`scriptedStream` gains `sendErr`)
- Modify: `internal/worker/handler_register_success_test.go` (one new test)

- [ ] **Step 1: Let `scriptedStream.Send` fail on request**

Add one field to the struct, immediately below `sent []*relayv1.CoordinatorMessage`:

```go
	// sendErr, when set, is what Send returns after recording. A peer that
	// vanished between RegisterWorkerConnection and the RegisterResponse looks
	// exactly like this from the server's side, and it is the second of the two
	// arms a failed registration can take.
	sendErr error
```

and change `Send`'s last line from `return nil` to:

```go
	return s.sendErr
```

- [ ] **Step 2: Write the test**

Append to `internal/worker/handler_register_success_test.go`:

```go
// TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration
// is the arm that existed only behind //go:build integration
// (TestRegisterWorker_SendFailureReleasesTheGeneration), which CI compiles and
// never runs. That test STAYS: it carries the durable half this one cannot - a
// real workers row, a real task, a real grace timer, a real requeue. This one
// carries the half CI executes on every push.
//
// IT IS NOT RED AT HEAD AND IS NOT PRESENTED AS IF IT WERE. The behaviour it
// covers shipped in the finishregister-strand slice; what this test adds is a
// witness in the lane that runs. Its discriminating power is established by
// mutation M8 - moving `handedOff = true` above the send - which is also the
// replacement proof for deleting the structural guard's ordering clause in the
// next task.
func TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration(t *testing.T) {
	f := newSuccessFixture(t)
	workerID := uuidStr(strandWorkerID)
	f.stream.sendErr = errors.New("rpc error: code = Unavailable desc = transport is closing")

	err := f.h.Connect(f.stream)

	require.Error(t, err)
	require.Contains(t, err.Error(), "send register response",
		"fixture: the registration must fail on the RegisterResponse send - AFTER "+
			"RegisterWorkerConnection acquired the generation, AFTER grace.Cancel discarded the "+
			"previous disconnect's requeue, and AFTER a reconcile that moved nothing. Any other error "+
			"means this test is driving a different arm and its assertions below prove nothing.")

	execs := f.db.execsSeen()
	require.Len(t, execs, 1,
		"a registration that failed on the send must release the generation it acquired, exactly "+
			"once. Nothing else will: no sender was ever registered, so Connect's teardown defer is "+
			"never armed, and the liveness sweeper walks past this worker because Metrics.Activate is "+
			"below the failure.")
	assert.Contains(t, execs[0].sql, "status = 'offline'",
		"the one statement must be MarkWorkerOfflineIfEpoch")
	assert.Contains(t, execs[0].args, strandEpoch,
		"fenced on the epoch THIS registration created")

	select {
	case fire := <-f.fired:
		assert.Equal(t, workerID, fire.workerID)
		assert.Equal(t, strandEpoch, fire.epoch,
			"the timer must be armed at the epoch this registration created. Re-arming at the previous "+
				"epoch is a silent no-op - RequeueWorkerTasksIfEpoch fences on "+
				"workers.connection_epoch and the row has moved past it.")
	case <-time.After(3 * time.Second):
		t.Fatal("no grace timer was armed after the failed send. grace.Cancel destroyed the previous " +
			"disconnect's pending requeue, so without a fresh one these tasks have no requeue " +
			"scheduled at all.")
	}

	require.Error(t, f.h.registry.Send(workerID, &relayv1.CoordinatorMessage{}),
		"a registration that failed on the send must leave no sender in the registry: "+
			"h.registry.Register sits BELOW the send, so nothing was ever published")

	require.Len(t, f.stream.sentMsgs(), 1,
		"the send was attempted exactly once and failed; a retry here would be a second "+
			"RegisterResponse on a stream the peer has already dropped")
}
```

Add `"errors"` to the first import group of the file.

- [ ] **Step 3: Run it - expect PASS on the first run**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration -v -timeout 60s
```

Expected: `--- PASS`. If it fails on `require.Contains(err.Error(), "send register response")`, the fixture is failing earlier than intended - most likely `sendErr` was set after `Connect` started, or the register message did not route to `reconnectAndRegister`.

- [ ] **Step 4: M8 - prove the test discriminates**

Move `handedOff = true` from `internal/worker/handler.go:789` to immediately **above** the `if err := stream.Send(&relayv1.CoordinatorMessage{` at `:699`.

Applied-check:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && grep -n "handedOff = true" internal/worker/handler.go
```

Expected: exactly one hit, at a line number **below 690 and above 700**. If the line number is still ~789, the edit did not apply - re-open the file rather than trusting the diff.

Then run:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run 'TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration|TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration' -v -timeout 60s
```

Expected: the send-failure test **FAILS** at `require.Len(t, execs, 1)` with `"[]" should have 1 item(s), but has 0`, and the success test still **PASSES**. That asymmetry is the proof: this test sees something the success test cannot, and it is exactly what guard clause G15 (`handoff < sendPos`) was checking structurally.

Revert with `git checkout -- internal/worker/handler.go` and re-run both to confirm PASS.

- [ ] **Step 5: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -timeout 300s && git add internal/worker/handler_registration_deadline_test.go internal/worker/handler_register_success_test.go && git commit -m "test(worker): cover the RegisterResponse-send strand in the default lane

The send arm had exactly one witness, TestRegisterWorker_SendFailureReleasesThe
Generation, behind //go:build integration - compiled by CI and never run. The
integration test stays; it carries the real worker row, the real task and the
real requeue. This adds the half CI executes: a stream whose Send fails, and the
assertions that the generation is released exactly once at this connection's
epoch, a grace timer is armed at that epoch, and no sender reaches the registry.

Not red at HEAD and not presented as if it were. Mutation M8 - moving
handedOff = true above the send - reddens it while leaving the success test
green, which is the behavioural replacement for the structural guard's ordering
clause."
```

---

## Task 6: Pin the two `applyInventory` properties this slice must NOT change

Spec section 8 makes "no early return on an empty inventory" a non-goal, and the backlog item forbids that fix by name. A prohibition nothing checks is advisory. These two tests make it enforceable.

**Files:**
- Modify: `internal/worker/handler_register_success_test.go` (two new tests)

- [ ] **Step 1: Write both tests**

Append to `internal/worker/handler_register_success_test.go`:

```go
// TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone is the check
// that makes a prohibition enforceable. `if len(inv) == 0 { return nil }` at the
// top of applyInventory is a green change today and a plausible one - it looks
// like an optimisation and it removes the transaction this slice's seam had to
// fake. It is also a silent data bug: an agent that legitimately reports zero
// workspaces stops clearing its stale worker_workspaces rows, and the dispatcher
// scores warm-workspace affinity off exactly those rows
// (internal/scheduler/dispatch.go), so it keeps preferring a worker for a
// workspace that is no longer there.
//
// The RegisterRequest here carries no Inventory at all - the nil case, which is
// what a reconnecting agent with an empty registry sends.
func TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone(t *testing.T) {
	f := newSuccessFixture(t)
	require.Nil(t, f.stream.msgs[0].GetRegister().Inventory,
		"fixture: this test is about the EMPTY inventory case")

	_, finish := f.startConnect(t)
	defer func() { require.Error(t, finish()) }()

	txExecs := f.tx.execsSeen()
	require.Len(t, txExecs, 1,
		"an empty inventory must still run the full-replace transaction. Returning early leaves the "+
			"worker's previous worker_workspaces rows in place forever, and the dispatcher keeps "+
			"scoring warm-workspace affinity off them.")
	assert.Contains(t, txExecs[0].sql, "DELETE FROM worker_workspaces",
		"the one statement must be ReplaceWorkerInventory, which is what clears the stale rows")
}

// TestFinishRegister_SucceedsWhenTheInventoryTransactionFails pins the
// log-and-continue shape at handler.go:693-695. applyInventory's error is
// deliberately swallowed there, and the "EVERYTHING BELOW THIS LINE MUST STAY
// INFALLIBLE" rule a few lines down names that shape as the required one.
// Turning that log.Printf into a return is a plausible edit - it looks like
// error handling - and it would make a workspace-inventory hiccup refuse an
// agent's registration outright.
//
// The injected error is the one from the open
// bug-2026-08-23-applyinventory-null-timestamp item, because this seam is what
// makes that bug cheaply reproducible for the first time. Fixing it is NOT this
// slice's job; the item stays open.
func TestFinishRegister_SucceedsWhenTheInventoryTransactionFails(t *testing.T) {
	f := newSuccessFixture(t)
	workerID := uuidStr(strandWorkerID)
	f.tx.execErr = errors.New(
		`ERROR: null value in column "last_used_at" violates not-null constraint (SQLSTATE 23502)`)

	online, finish := f.startConnect(t)

	assert.JSONEq(t, `{"id":"`+workerID+`","status":"online"}`, string(online.Data),
		"a failed inventory replace must not stop the worker coming online. applyInventory's error is "+
			"logged and swallowed on purpose: a registration that returns here would be covered by the "+
			"deferred release but would refuse a healthy agent over a workspace bookkeeping fault.")
	require.NoError(t, f.h.registry.Send(workerID, &relayv1.CoordinatorMessage{}),
		"and the sender must still reach the registry, or the agent is online and undispatchable")
	assert.Empty(t, f.db.execsSeen(),
		"the generation must not be released: this registration succeeded")

	require.Error(t, finish())
}
```

- [ ] **Step 2: Run both - expect PASS**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run 'TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone|TestFinishRegister_SucceedsWhenTheInventoryTransactionFails' -v -timeout 60s
```

Expected: two `--- PASS`.

- [ ] **Step 3: M9 - the forbidden early return**

Insert at the top of `applyInventory`'s body (`internal/worker/handler.go:1743`, above the `return pgx.BeginTxFunc(`):

```go
	if len(inv) == 0 {
		return nil
	}
```

Applied-check: `grep -n "if len(inv) == 0" internal/worker/handler.go` -> one hit at ~1743.

Run the two tests. Expected: `TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone` **FAILS** at `require.Len(t, txExecs, 1)` with `"[]" should have 1 item(s), but has 0`. Revert and re-run.

- [ ] **Step 4: M10 - swallow becomes return**

Replace `internal/worker/handler.go:693-695` with:

```go
	if err := h.applyInventory(ctx, updated.ID, reg.Inventory); err != nil {
		return "", nil, err
	}
```

Applied-check: `grep -n "register inventory replace failed" internal/worker/handler.go` -> **no output**.

Run the two tests. Expected: `TestFinishRegister_SucceedsWhenTheInventoryTransactionFails` **FAILS** inside `startConnect`, bounded at 5s, with "the registration ended without publishing a worker event: ..." naming the injected SQLSTATE 23502 error. Revert and re-run.

Note in passing what M10 also demonstrates: with the guard's G19 clause still in place, this mutation is **additionally** rejected structurally (an error return below the flip) - except that it sits *above* the flip, so it is not. G19 covers returns below the flip only; this test is the only thing covering this one.

- [ ] **Step 5: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -timeout 300s && git add internal/worker/handler_register_success_test.go && git commit -m "test(worker): pin applyInventory's two load-bearing properties

The backlog item forbids 'return early on an empty inventory' by name, and until
now nothing checked it: the change is green, looks like an optimisation, and
silently freezes a worker's stale worker_workspaces rows - which the dispatcher
scores warm-workspace affinity off. It now reddens a test.

The second test pins the log-and-continue shape at the applyInventory call site.
Turning that log.Printf into a return would refuse a healthy agent's
registration over a workspace bookkeeping fault; handler.go states that as a
rule and this is what makes it a check."
```

---

## Task 7: Reduce the structural guard to what a behavioural test cannot see

**Only now.** Both behavioural tests are green in the default lane, and the mutations that justify each deletion have either already run (M8) or run in this task (M13, M14).

**Reduce, do not retire.** The guard's own comment states why: what a source position pins is a point, what the code needs is a range, and every position in that range behaves identically today. Fourteen clauses were reviewed; **five are deleted and nine stay.** Two of the five (G6, G7) are bookkeeping for a third, one (G3) is redundancy with a sibling clause, and only G12 and G15 are genuine structure-replaced-by-behaviour.

**Files:**
- Modify: `internal/worker/handler_handoff_guard_test.go` (deletions only in this task; its prose is Task 8)

- [ ] **Step 1: Delete G6 and G7 - the stream anchor and its bookkeeping**

Delete lines `:84-95` in full - that is `streamParam := paramNamedByType(...)` with its `if streamParam == ""` fatal, the two-line comment `// The lower bounds of the window: ...`, and the `sendPos := onlyCallOnReceiver(...)` block. Replace the deleted comment with one that describes what survives:

```go
	// The lower bound of the window: the moment the sender becomes reachable by
	// other goroutines. The RegisterResponse send used to be a second anchor here;
	// ordering against it is now covered behaviourally by
	// TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration.
	registerPos := onlyCallOnReceiver(t, fn, "the worker registry", func(sel *ast.SelectorExpr) bool {
```

- [ ] **Step 2: Delete G15 - the flip-not-before-the-send ordering clause**

Delete lines `:247-253` in full: the whole `if handoff < sendPos { t.Fatalf(...) }` block. Its behavioural replacement is the test named above, proven by M8 in Task 5.

- [ ] **Step 3: Delete G12 - the address-of clause, and the counter that only it used**

Three coupled deletions; do all three or the file will not compile:

1. Delete the `if aliases != 0 { ... }` block at `:210-219`.
2. Delete `	var aliases int` at `:154`.
3. Delete the whole `case *ast.UnaryExpr:` arm of the `ast.Inspect` switch at `:157-163`.

**Do not leave the counter behind "in case".** An `aliases++` with no reader is at best dead weight and at worst a compile error; and half-deleting the clause would leave a counter nothing reads, which is the shape a future reader repairs in the wrong direction.

Keep the `ast.Unparen` normalisation everywhere it appears - that is what catches `(handedOff) = false`, which is `otherWrites`, a clause that stays.

- [ ] **Step 4: Delete G3 - the redundant "closure calls the release exactly once" clause**

In `handoffFlagIdent`, delete lines `:490-497`: the `if n := countCallsNamed(lit.Body, releaseMethod); n != 1 { ... }` block.

**Keep `countCallsNamed`** - `handoffFlagIdent` still uses it at `:467` to *select* the candidate closure, which is a different job. Confirm after the edit:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && grep -c "countCallsNamed" internal/worker/handler_handoff_guard_test.go
```

Expected: `2` (the definition and the one remaining call).

This is the one deletion justified by **redundancy** rather than by behaviour, and it is the one clause safe to keep if a reviewer disagrees: the switch below already pins the closure body to exactly one or two statements with the release at a fixed place, so a second release cannot hide inside an accepted body, and both new tests assert exact release counts.

- [ ] **Step 5: Delete `paramNamedByType` and the `strings` import it was the only user of**

Delete the whole `paramNamedByType` function (`:645-658`, doc comment included), then delete `	"strings"` from the import block at `:8`.

Confirm:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && grep -n "strings\.\|paramNamedByType" internal/worker/handler_handoff_guard_test.go
```

Expected: **no output.** A leftover `strings` import is `imported and not used`, a compile error.

- [ ] **Step 6: Confirm the guard still runs and still passes**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run TestFinishRegisterHandsOffOwnershipInsideTheWindow -v -timeout 60s
```

Expected: `--- PASS`. Then confirm the size of the reduction:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && git diff --stat internal/worker/handler_handoff_guard_test.go
```

Expected: roughly 70 deletions, a handful of insertions.

- [ ] **Step 7: M14 - the replacement proof for G12, run AFTER the deletion**

This ordering is not optional. Run before the deletion, M14 proves nothing about the behavioural test, because the clause you are about to remove would kill it first.

Insert immediately **below** `handedOff = true` at `internal/worker/handler.go:789`:

```go
	p := &handedOff
	*p = false
```

Applied-check:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && grep -n "p := &handedOff" internal/worker/handler.go
```

Expected: one hit immediately after the `handedOff = true` line.

Run:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -run 'TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration|TestFinishRegisterHandsOffOwnershipInsideTheWindow' -v -timeout 60s
```

Expected: the **guard PASSES** (it no longer counts aliases - that is the deletion you just made) and the **success test FAILS**, first at the grace-fire `t.Fatalf`, then on `execsSeen` and the teardown count of 2 if that one is silenced. That is the whole argument for the deletion: G12's own failure message described the harm as "the live agent published 'offline', its metrics entry wiped, a grace timer requeueing its running tasks", and those are now three separate assertions in a test that runs.

Revert with `git checkout -- internal/worker/handler.go` and re-run.

- [ ] **Step 8: M13 - the evasion Task 4's fixture pool closed, run AFTER the deletions**

Change `finishRegister`'s deferred closure at `internal/worker/handler.go:653-657` to:

```go
	defer func() {
		if h.pool != nil {
			return
		}
		if !handedOff {
			h.releaseWorkerGeneration(workerID, updated.ConnectionEpoch)
		}
	}()
```

Applied-check:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && grep -n "if h.pool != nil" internal/worker/handler.go
```

Expected: one hit in the 650s.

Run:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -timeout 300s
```

Expected: **red on two fronts.** `TestFinishRegisterHandsOffOwnershipInsideTheWindow` fails on clause G4 (the closure body is no longer exactly the guard construct - it now has three statements, so `handoffFlagIdent`'s `default:` arm rejects it), **and** the strand tests plus `TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration` fail behaviourally, because Task 4 gave those fixtures a non-nil pool.

**If the behavioural half does NOT redden, the fake pool did not reach `newStrandHandler` / `newSuccessFixture`** - go back to Task 4 Step 1 rather than accepting the guard-only kill. Two independent kills is the whole point of this measurement: this exact edit passed the guard and the whole package in the 2026-08-24 slice.

Revert and re-run.

- [ ] **Step 9: Re-run M2 and M8 to prove what was KEPT still holds**

Re-apply M2 (`if h.Metrics != nil { handedOff = true }`) and confirm the reduced guard still fails on G14, exactly as in Task 3 Step 7. Revert.

Re-apply M8 (`handedOff = true` moved above the send) and confirm that with G15 now deleted, the **guard passes** and `TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration` **fails**. Revert.

That pair is the ledger entry for the whole reduction: the structural check that went away has a behavioural successor that reddens on the same input, and the structural check that stayed still catches what no behavioural test can.

- [ ] **Step 10: Full lane, race, and commit**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -timeout 300s && CC=/c/msys64/mingw64/bin/gcc.exe go test ./internal/worker/... -race -timeout 600s && go vet -tags integration ./...
```

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && git add internal/worker/handler_handoff_guard_test.go && git commit -m "test(worker): reduce the handoff guard to what a behavioural test cannot see

Five clauses go, nine stay. G12 (no address-of the flag) and G15 (the flip is not
before the RegisterResponse send) are now covered behaviourally - by
TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration and
TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration,
proven by mutations M14 and M8 run after the deletions. G6 and G7 were
bookkeeping for G15 and go with it, taking paramNamedByType and the strings
import. G3 was redundant with the closure-shape switch that stays.

What stays is the half no runtime test can reach: source positions. Adjacency to
registry.Register, the flip being a direct body statement rather than nested on a
condition true in a fixture and false in production (G14, which mutation M2
survives behaviourally and only this guard kills), and the absence of an error
return that does not exist yet.

The guard stops being the ONLY witness, which is what made it grow three rounds
of evasions."
```

---

## Task 8: Correct every claim this slice falsified

**Wrong prose about correct code is this repository's dominant defect class - nine iterations running.** These are acceptance criteria, not cleanup. Six sites, each with the exact claim being falsified. One of them is production-file prose.

**Files:**
- Modify: `internal/worker/handler.go:752-753`
- Modify: `internal/worker/handler_handoff_guard_test.go:16-22`, `:130-150`, `:437-443`, `:603-607`, `:51-56`
- Modify: `internal/worker/handler_register_strand_test.go:30-37`
- Modify: `internal/worker/handler_register_strand_integration_test.go:57-64`
- Modify: `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`
- Modify: `docs/backlog/bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory.md`

| # | Site | The claim, and why it is now false |
| --- | --- | --- |
| 1 | `internal/worker/handler.go:752-753` | "The guard lives in the default lane because no default-lane test can drive a successful registration at all." **A default-lane test now does.** This is the one PRODUCTION-file prose site. |
| 2 | `handler_handoff_guard_test.go:16-22` | "WHY A GUARD AND NOT A BEHAVIOURAL TEST ... the default-lane fixture structurally cannot drive one: applyInventory opens a transaction on the concrete `*pgxpool.Pool`." Both halves false: the field is an interface and the fixture drives it. |
| 3a | `handler_handoff_guard_test.go:437-443` | `if h.pool != nil { return }` is offered as an evasion "the default lane cannot notice ... newStrandHandler leaves pool nil deliberately". Task 4 made that premise false and mutation M13 measured it. |
| 3b | `handler_handoff_guard_test.go:603-607` | `isCallTo`'s doc comment uses the same `if h.pool != nil { return }` example for containment-vs-identity. The example is still VALID for that point but its stated reason is gone; keep the shape, drop the pool. |
| 4 | `handler_register_strand_test.go:30-37` | "IT WORKS BECAUSE THE FAILING PATH NEVER TOUCHES `h.pool` ... That is also why the RegisterResponse-send arm cannot live in this lane." The first sentence stays true and still earns its place; **the second is now false.** |
| 5 | `handler_register_strand_integration_test.go:57-64` | "it needs a real pool, because applyInventory opens a transaction on `*pgxpool.Pool` unconditionally ... so a pool-less fixture cannot reach `stream.Send` at all." False. The test STAYS; the justification that survives is already written in its next paragraph. |
| 6 | `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` | This slice removes one of that item's named instances. **It is not closed by this slice** - note the reduction on it. |

- [ ] **Step 1: Site 1 - the production comment**

In `internal/worker/handler.go`, find the sentence inside the `OWNERSHIP HANDOFF` comment block:

```go
	// The guard lives in the default lane because no default-lane test can drive a
	// successful registration at all.
```

Replace with:

```go
	// The guard lives in the default lane, and what it covers there has narrowed.
	// It is now the SOURCE-POSITION half only: adjacency to registry.Register, the
	// flip being a direct body statement rather than nested on a condition that is
	// true in a fixture and false under main.go, and the absence of an error return
	// below the flip - none of which a runtime test can observe. The behavioural
	// half is TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration,
	// which drives this whole function to a successful return without Postgres and
	// asserts that the generation is released exactly once across the connection's
	// life. It exists because Handler.pool is a txBeginner rather than a
	// *pgxpool.Pool; before that seam no default-lane test could get past
	// applyInventory.
```

- [ ] **Step 2: Site 2 - the guard's header**

In `handler_handoff_guard_test.go`, replace the paragraph beginning `// WHY A GUARD AND NOT A BEHAVIOURAL TEST.` through the two bullets that end `//     which only the integration lane covers.` with:

```go
// WHY A GUARD AS WELL AS A BEHAVIOURAL TEST. There is now a behavioural witness:
// TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration
// drives a SUCCESSFUL registration in this lane and asserts the generation is
// released exactly once across the connection's life, and
// TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration
// covers the send arm. Both were impossible until Handler.pool became a
// txBeginner. Deleting the flag, or flipping it too early, is caught by those
// two and this test's clauses for either were removed.
//
// WHAT NO RUNTIME TEST CAN SEE IS SOURCE POSITION, and that is the whole of what
// survives here. A flip wrapped in `if h.Metrics != nil { ... }` sits at a
// perfectly legal position and leaves the success path unflipped in production
// while the fixture - which must set Metrics, to assert Activate - flips it
// happily. Measured: that mutation passes the behavioural test and fails this
// one. So does an error return added below the flip, which is a claim about
// statements that do not exist yet and which no runtime test can ever assert.
```

- [ ] **Step 3: Site 3a - the worked example in `handoffFlagIdent`'s doc comment**

Replace the third bullet (`//   - THE CLOSURE BODY IS EXACTLY THE GUARD CONSTRUCT ...` through `// ... while this whole package stayed green.`) with:

```go
//   - THE CLOSURE BODY IS EXACTLY THE GUARD CONSTRUCT, with the release at a
//     fixed place inside it. Anything looser admits a skip on a condition this
//     test cannot evaluate: `if h.AllowAutoEnroll { return }` ahead of the
//     release is false in every fixture in this package and true wherever an
//     operator set RELAY_ALLOW_AUTO_ENROLL, so every failed registration on such
//     a server would release nothing while the whole package stayed green.
//     `if h.pool != nil { return }` used to be the example here, on the grounds
//     that newStrandHandler left pool nil; that is no longer true - both
//     default-lane fixtures now carry a fake pool, and mutation M13 confirmed
//     the edit reddens them as well as this clause. The general shape is what
//     this clause is for, not that one instance.
```

- [ ] **Step 4: Site 3b - `isCallTo`'s doc comment**

Replace:

```go
// distinction is the whole point: `if h.pool != nil { return }` followed by the
// release contains the call, and containment is what a recursive check accepts.
```

with:

```go
// distinction is the whole point: `if h.AllowAutoEnroll { return }` followed by
// the release contains the call, and containment is what a recursive check
// accepts.
```

- [ ] **Step 5: Site 2 continued - the header's anchor paragraph**

The header at `:51-56` says the stream parameter "and the success return are derived the same way - by type". After Task 7 the stream parameter is not derived at all. Replace the sentence

```go
// instead of defeating it, and the stream parameter and the success return are
// derived the same way - by type, and by the final result being the predeclared
// nil. The remaining anchors ARE source text: the file name, "finishRegister",
// the "AgentService_ConnectServer" type suffix, "releaseWorkerGeneration", and
// at the registry anchor the receiver "h" and the field "registry".
```

with:

```go
// instead of defeating it, and the success return is derived the same way - by
// its final result being the predeclared nil. The remaining anchors ARE source
// text: the file name, "finishRegister", "releaseWorkerGeneration", and at the
// registry anchor the receiver "h" and the field "registry".
```

- [ ] **Step 6: Site 2 continued - the alias paragraph at `:130-150`**

That block argues at length that address-of is a chokepoint and counts it. The counting is gone. Replace the paragraph beginning `// WRITES BY NAME ARE NOT THE WHOLE WRITE SET, AND THIS COMMENT USED TO CLAIM` through `// ... the identifier there is the pointer's name, not the flag's.` with:

```go
	// WRITES BY NAME ARE NOT THE WHOLE WRITE SET, and this comment used to claim
	// they were plus exactly one more. It said a local bool has "exactly one other
	// way to be written: through a pointer to it", counted address-of, and was
	// wrong: `(handedOff) = false` after the flip needs no pointer and no
	// indirection, it simply wraps the name in parens, which an *ast.Ident type
	// assertion does not see through. It was measured releasing the generation on
	// every SUCCESSFUL registration with `go vet` clean and the whole repo green.
	// `gofmt` does not normalise it away, and this tree has no fmt gate (CRLF makes
	// `gofmt -l` flag every file at baseline).
	//
	// So every expression site below is normalised with ast.Unparen first, and the
	// honest statement of what is checked is: writes are counted BY NAME after
	// dropping parens. That covers a parenthesised write and a closure writing the
	// flag by name - `defer func(){ handedOff = false }()` is already otherWrites,
	// because ast.Inspect descends into function literals. Shadowing is caught by
	// the initFalse count.
	//
	// AN ALIAS THROUGH A POINTER IS NO LONGER COUNTED HERE, deliberately.
	// `p := &handedOff` plus `*p = false` below the flip is now caught by
	// TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration,
	// in four places at once - a grace fire, a non-empty statement log, a second
	// worker event and a teardown release count of two - which is what the deleted
	// clause's failure message described in words. Mutation M14 confirmed it after
	// the deletion, not before.
```

- [ ] **Step 7: Site 4 - the strand fixture's header**

In `handler_register_strand_test.go`, replace the block at `:30-37` (`// IT WORKS BECAUSE THE FAILING PATH NEVER TOUCHES h.pool` through `// TestRegisterWorker_SendFailureReleasesTheGeneration, //go:build integration.`) with:

```go
// IT WORKS BECAUSE THE FAILING PATH NEVER TOUCHES h.pool, and that stays worth
// saying: finishRegister returns on reconcileRunningTasks' error four lines
// ABOVE the applyInventory call that opens a transaction, so the four tests below
// need no inventory fixture at all and the fake pool newStrandHandler carries is
// there for mutation coverage rather than for them.
//
// THE SEND ARM IS NO LONGER EXCLUDED FROM THIS LANE. It used to be - h.pool was a
// concrete *pgxpool.Pool and a pool-less fixture could not reach stream.Send at
// all - and that sentence was here. Handler.pool is now a txBeginner, and
// TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration in
// handler_register_success_test.go drives that arm right here in the default
// lane. TestRegisterWorker_SendFailureReleasesTheGeneration stays in the
// integration lane for what it carries that this cannot: a real worker row, a
// real task, a real requeue.
```

- [ ] **Step 8: Site 5 - the integration test's justification**

In `handler_register_strand_integration_test.go`, replace `:57-64` with:

```go
// TestRegisterWorker_SendFailureReleasesTheGeneration is the second of the two
// arms the strand can take, proven end to end against real Postgres.
//
// IT IS NO LONGER THE ONLY WITNESS FOR THIS ARM, and its justification changed
// when that stopped being true. It used to say it needed a real pool because
// applyInventory opened a transaction on *pgxpool.Pool unconditionally, so a
// pool-less fixture could not reach stream.Send at all. Handler.pool is a
// txBeginner now and
// TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration
// covers the same arm in the lane CI runs.
//
// What keeps this test is the half the default-lane proof cannot carry: the
// actual worker ROW and the actual TASK, through a real grace timer, to a real
// requeue. The duplication is deliberate and the two are at different layers.
```

- [ ] **Step 9: Site 6 - note the reduction on the lane item**

Append to the `## Related` or a new `## Progress` section of `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`:

```markdown
## Progress

- 2026-08-25: one named instance removed. `internal/worker`'s successful-registration
  path had every witness behind `//go:build integration`; narrowing `Handler.pool` to a
  one-method `txBeginner` interface put four behavioural tests in the default lane
  (`internal/worker/handler_register_success_test.go`) and let
  `handler_handoff_guard_test.go` shed five clauses. The item stays open - the remaining
  instances are untouched.
```

- [ ] **Step 10: The `applyInventory` bug item's amendment**

Append to `docs/backlog/bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory.md`:

```markdown
## Amendment 2026-08-25

- Its regression test is now cheap and default-lane. `Handler.pool` is a `txBeginner`
  interface, so a `fakeTx.Exec` returning a NOT NULL violation reproduces this without
  Postgres - `TestFinishRegister_SucceedsWhenTheInventoryTransactionFails` in
  `internal/worker/handler_register_success_test.go` already injects exactly that error
  for a different purpose. **This item is not fixed and stays open.**
- Its line citations have drifted: `applyInventory` is at `internal/worker/handler.go:1742-1766`,
  not `:1387-1411`.
```

- [ ] **Step 11: Re-read the two neighbours that were checked and deliberately left alone**

Do not edit these; confirm by reading that they are still true, because a reviewer will ask:

- `cmd/relay-server/grpc_admission_e2e_integration_test.go:47-51` - "worker.Handler needs the pool itself, not just the `*store.Queries` wrapping it: applyInventory opens its own transaction via `pgx.BeginTxFunc(ctx, h.pool, ...)` even for an empty inventory update". **Still true.** That harness runs against real Postgres and genuinely needs the real pool; `applyInventory` still opens its transaction unconditionally, which Task 6's first test now pins.
- `internal/worker/sender.go:32-35` - "connEpoch is ... set once in finishRegister and never mutated. Teardown fences shared-state writes on it." **Still true**, and now observed: the success test reads that epoch back off `MarkWorkerOfflineIfEpoch`'s fence argument after teardown.
- `CLAUDE.md`'s `internal/worker/` code-map bullet and the Invariants section: **no change needed.** No invariant's wording is affected, no new exported symbol exists (`txBeginner` is unexported), and nothing operator-visible moved, so README is untouched too.

- [ ] **Step 12: Verify and commit**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test ./internal/worker/... -timeout 300s && go vet -tags integration ./... && git diff --stat
```

Expected: green, and `git diff --stat` lists only the files this task edits.

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && git add internal/worker/handler.go internal/worker/handler_handoff_guard_test.go internal/worker/handler_register_strand_test.go internal/worker/handler_register_strand_integration_test.go docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md docs/backlog/bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory.md && git commit -m "docs(worker): correct the six claims the pool seam falsified

Six written claims said the default lane structurally cannot drive a successful
worker registration, and one of them is in handler.go itself. It can now.

- handler.go's ownership-handoff comment claimed the guard is default-lane
  because no behavioural test is possible; it now states what the guard actually
  covers, which is source position, and names the behavioural half.
- The guard's header said the same thing twice and used
  \`if h.pool != nil { return }\` as an evasion the default lane cannot notice;
  both fixtures now carry a fake pool, so that instance is caught. The general
  shape is what the clause is for and the example moved to AllowAutoEnroll.
- The strand fixture's header said the send arm cannot live in this lane. It does.
- The integration test's header said it needs a real pool to reach stream.Send.
  It does not; it stays for the real row, the real task and the real requeue,
  which is the justification already written in its next paragraph.
- Two backlog items get amendments rather than closures."
```

---

## Task 9: Close the backlog item

- [ ] **Step 1: Run the close command**

```
/backlog close handler-pool-has-no-seam
```

This `git mv`s `docs/backlog/idea-2026-08-24-handler-pool-has-no-seam.md` into `docs/backlog/closed/`, stamps `status: closed` plus `closed:`/`resolution:` frontmatter, appends a `## Resolution` note and commits. **Do not hand-edit `status:`** - flipping it alone leaves the file in the open directory and `/backlog list` then reports it as malformed.

- [ ] **Step 2: The Resolution must record what the item did not say**

In addition to what shipped, the Resolution note must state:

- **The pool seam alone was not enough.** `reconcileRunningTasks` sits above `applyInventory` and its `GetActiveTasksForWorker` is a sqlc `:many`; the existing fixture's `Query` returned `(nil, nil)`, which panics on `rows.Close()` one frame short of the pool. An empty-`pgx.Rows` fake was required and none existed in the tree. A slice scoped to "narrow the pool and stop" would not have met this item's own acceptance criterion.
- **Three call sites share the one interface, not one.** `enrollAndRegister` and `autoEnrollAndRegister` open the identical transaction; they are now fakeable too, and testing them is deliberately out of scope.
- **The guard was reduced, not retired,** and the split is on a real line: five clauses had behavioural or redundant cover, nine cover source positions no runtime test can see. The measurable win is not the deleted lines - it is that the next slice touching `finishRegister` has a behavioural witness and does not have to grow the guard a fourth time.

- [ ] **Step 3: Confirm the whole slice one last time**

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go build ./... && go vet ./... && go vet -tags integration ./... && make test && CC=/c/msys64/mingw64/bin/gcc.exe go test ./internal/worker/... -race -timeout 600s
```

Expected: all green. If Docker Desktop is available, also:

```bash
cd /d/dev/relay/.claude/worktrees/reverent-solomon-87f44d && go test -tags integration -p 1 ./internal/worker/... -timeout 900s
```

Expected: `ok relay/internal/worker`. In particular `TestHandler_RegisterNewWorker` and the four tests in `handler_teardown_test.go` must still pass - they are the standing end-to-end proof that a successful registration does not release its own generation.

---

## Verify commands for the whole slice

| Command | What it establishes |
| --- | --- |
| `go build ./...` | The type change compiles everywhere, including `cmd/relay-server`. |
| `go vet ./...` | Default lane is clean. |
| `go vet -tags integration ./...` | **The integration lane still COMPILES.** This is the one that catches a `*pgxpool.Pool` call site the type change broke, and no default-lane command will. |
| `make test` | Every package's default lane, which is what CI runs. |
| `CC=/c/msys64/mingw64/bin/gcc.exe go test ./internal/worker/... -race -timeout 600s` | The race detector over the new fixtures. **The `CC` is mandatory on this machine** - MSYS2 mingw64 gcc; the default Strawberry Perl gcc fails every package with exit `0xc0000139`. |
| `go test -tags integration -p 1 ./internal/worker/... -timeout 900s` | Docker-gated; the existing success-path coverage is unchanged. |
| `git diff --stat` against `main` | Exactly the nine paths in the file inventory, and no `*.sql`, `*.sql.go` or `models.go`. |

---

## Mutation ledger - where each mutation lives

Assigned to the task that owns the test it must kill, so the battery runs incrementally rather than as a closing ritual. **Every mutation step includes an applied-check command** (CRLF has silently defeated four mutations in a row in this tree, and a mutation that did not apply reports "survived") **and every known-survivor step is preceded by a control that must die.**

| # | Mutation | Task | Expected |
| --- | --- | --- | --- |
| M1 | Delete `handedOff = true` | 3 | Kills 5.1 four ways |
| M2 | `if h.Metrics != nil { handedOff = true }` | 3 (recorded), 7 (re-run) | **KNOWN SURVIVOR behaviourally.** Killed only by guard G14. Do not "fix" it - it is the argument for keeping G14. |
| M3 | Delete `h.registry.Register` | 3 | Kills 5.1's `registry.Send` |
| M4 | `connEpoch = updated.MaxSlots` | 3 | Kills 5.1's fence-arg and grace-epoch assertions |
| M5 | Delete `Metrics.Activate` | 3 | Kills 5.1's `LastSampleAt` |
| M6 | Delete the online publish | 3 | Kills 5.1 as a bounded timeout, never a hang |
| M7 | Delete `go h.triggerDispatch()` | 3 | Kills 5.1's dispatch wait, bounded |
| M11 | Revert `pool` to `*pgxpool.Pool` | 3 | **Compile error**, not a behavioural kill. Recorded as a type-level guard. |
| M12 | Remove the fake pool from `newStrandHandler` | 4 | **KNOWN SURVIVOR by design.** Its value is measured by M13, and M12∘M13 is the 2026-08-24 evasion. Do not delete the fixture pool as dead weight. |
| M8 | Move the flip above the send | 5, re-run in 7 | Kills 5.2 - the replacement proof for deleting G15 |
| M9 | `if len(inv) == 0 { return nil }` | 6 | Kills the empty-inventory test |
| M10 | `log.Printf` -> `return "", nil, err` | 6 | Kills the inventory-failure test, bounded |
| M13 | `if h.pool != nil { return }` in the deferred closure | **7, after the deletions** | Kills G4 **and** the strand + 5.2 tests. If the behavioural half does not redden, the fixture pool did not land. |
| M14 | `p := &handedOff` plus `*p = false` | **7, after the deletions** | Kills 5.1 - the replacement proof for deleting G12. Running it before the deletion proves nothing. |

---

## Phase 4 verification lanes

Run `/code-review` first, then fan out these four in one message:

1. **`relay-code-reviewer`, invariants lens.** Focus: does narrowing `pool` to an interface weaken any invariant? Specifically - "Identity-checked teardown" and "End the generation before releasing the resource" are now asserted by a fake-driven test; is any assertion satisfiable by the fake rather than by the code? Check that `emptyRows` and `fakeTx` fail loud (panic) rather than returning plausible zero values for anything on the path, and that no test asserts something only the fake produces.
2. **`relay-code-reviewer`, correctness lens.** Focus: the two enrollment transactions (`enrollAndRegister:471`, `autoEnrollAndRegister:546`) now go through the same interface and are NOT covered by this slice. Does any of their behaviour change when `h.pool` is a nil interface rather than a typed-nil `*pgxpool.Pool` - in particular at `cmd/relay-server/counters_wiring_test.go`'s four `worker.NewHandler(nil, nil, ...)` sites? Both panic; confirm neither reaches a transaction, and confirm the panic message change does not break any assertion.
3. **`relay-code-reviewer`, security lens.** Focus: `txBeginner` is unexported and takes a value, so no external package can substitute a pool - confirm that, and confirm no new exported surface exists. Then: the success test now drives `Metrics.Activate` and the registry in the default lane; does anything in the new fixtures escape a test binary (a goroutine leak from `NewGraceRegistry`, an unclosed broker subscription, a `workerSender` loop left running)? `t.Cleanup` coverage for each.
4. **`relay-integration-tester`.** Re-run both lanes with Docker up. Then add adversarial coverage the plan deliberately did not: two `Connect` calls for the same worker where the first succeeds and the second also succeeds, asserting the first connection's teardown does not release the second's generation - the `UnregisterIf` gate, in the default lane, which this slice made reachable for the first time.

---

## Phase 6 proposals

Proposed, **not filed** - the conductor decides:

1. **`idea`: extend the default-lane registration fixture to the two enrollment transactions.** `enrollAndRegister` (`handler.go:448-516`) and `autoEnrollAndRegister` (`:539-600`) became fakeable the moment this slice landed, and both contain branch logic no default-lane test reaches: `errEnrollmentNotConsumable` (`:496-498`, `:509-511`), `errWorkerRevoked` (`:553-555`, `:580-582`), and the auto-enroll audit log line at `:598` whose own comment argues at length about its forgeability. High confidence it is now cheap; medium confidence it deserves its own slice.
2. Amendments to `bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory` and `idea-2026-08-23-integration-only-guards-ci-never-runs` are written by **Task 8**, not deferred.

---

## Single-slice check

**One PR, one session.** There are no `## Stage N` headings in this plan and nothing here is meant to span more than one session, so there is nothing to hand to `/backlog phases`. The one scope line the plan draws - the two enrollment transactions - is a Phase 6 proposal above, not a deferred stage of this work.

---

## Self-review against the spec

- **Spec 3 (the seam's shape):** Task 3 Steps 1-2, including the unexported interface, the retained field name, and the deleted `pgxpool` import the spec does not mention but the compiler requires.
- **Spec 3.2 (production wiring byte-identical):** Task 3 Step 6, with the five untyped-`nil` and real-pool call sites named.
- **Spec 4.1 / 4.2 (the `pgx.Tx` and `pgx.Rows` fakes):** Task 3 Step 3 and Task 1 Step 1. F1's missing rows fake is Task 1, sequenced FIRST because otherwise Task 2's RED lands in the wrong frame.
- **Spec 4.3 (three separate recorders):** `strandDB.Exec` (existing), `fakeTx.Exec` (Task 3), `scriptedStream.sent` (Task 1). The plan states why they must not be merged.
- **Spec 4.4 (fixture location, `newStrandHandler` gains the pool):** Task 2 creates the file; Task 4 does the fixture edit with its own inertness proof and M12.
- **Spec 5.1 / 5.2 (what the tests assert):** Tasks 2-3 and 5. The one observable the spec asks for and the code cannot provide (`sender.connEpoch` read directly) is flagged above and substituted with the teardown fence argument, which kills the same mutation.
- **Spec 6 (the non-goal gets a test):** Task 6, both tests, with M9 and M10.
- **Spec 7.1 (clause-by-clause):** Task 7 Steps 1-5 delete exactly G3, G6, G7, G12, G15 plus `paramNamedByType`, the `strings` import and the `aliases` counter. The nine kept clauses are untouched.
- **Spec 7.2 (ordering):** enforced structurally - Task 7 is after Tasks 2, 3, 5 and 6, and its two justifying mutations run after its own deletions.
- **Spec 8 (non-goals):** no `applyInventory` behaviour change (pinned by Task 6), no integration test deleted (Task 8 changes one comment and nothing else in that file), `internal/api` and `cmd/relay-server` untouched.
- **Spec 9 (prose):** Task 8, six sites plus three deliberate no-change confirmations.
- **Spec 10 (RED-first and the battery):** the RED analysis table above; the battery distributed across Tasks 3-7 with applied-checks and controls.
- **Placeholder scan:** every code step contains complete, compilable content. No TBD, no "similar to Task N", no "add appropriate error handling".
- **Type consistency:** `txBeginner`, `fakePool`, `fakeTx`, `emptyRows`, `successFixture`, `newSuccessFixture`, `startConnect`, `sentMsgs`, `execsSeen`, `sendErr` are each defined once and spelled identically at every later use. `strandDB`, `strandExec`, `strandWorkerRow`, `strandWorkerID`, `strandEpoch`, `graceFire`, `newStrandHandler`, `scriptedStream` keep the spellings they already have in the tree.
