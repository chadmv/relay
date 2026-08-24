# finishRegister Strand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A registration that fails after `RegisterWorkerConnection` has acquired the worker's generation must release that generation - mark the worker offline and re-arm the grace timer `grace.Cancel` discarded - instead of leaving the worker `online` with tasks assigned to a connection that does not exist.

**Architecture:** `finishRegister` arms a deferred release the instant `RegisterWorkerConnection` returns, guarded by a `handedOff` flag that is set at exactly one point: immediately after `registry.Register` transfers ownership of the release to `Connect`'s existing `defer h.teardownConnection`. The release body is extracted out of `teardownConnection` into one shared `releaseWorkerGeneration(workerID, epoch)` so the failed-registration path and the stream-ended path are provably the same code. The ownership check on the new path is the epoch fence inside `MarkWorkerOfflineIfEpoch`, and nothing else - a failed registration has no sender in the registry to compare against.

**Tech Stack:** Go, pgx v5 / sqlc (`internal/store`), gRPC bidi streams, `testify`. No SQL changes, no `make generate`, no proto changes, no migration.

---

## Slice independence declaration

**There is no frontend work in this slice.** Zero files under `web/` are touched. The `online` -> `offline` transition this plan fixes already renders through the existing `worker` SSE event and `GET /v1/workers`; the SPA needs no change to display the corrected state.

**Everything here is one backend slice, and its tasks are sequential, not parallel.** Task 2 (the fix) must land before Task 3 can be proven RED-then-GREEN, and Task 4 (prose) describes the behaviour Task 2 introduces. Do not fan Phase 3 out.

**No `make generate` step exists in this plan** and none is needed: no `.sql` and no `.proto` file is edited. If you find yourself editing `internal/store/query/*.sql`, stop - the plan is wrong, not the invariant.

---

## Verification log: what was confirmed and what was refuted

The item was filed at `medium` on 2026-08-23 by a read-only review lens and had **never been reproduced**. Every line number in it predates the current tree. All citations below were re-derived against the worktree at `668ca78`.

### Confirmed

| Claim | Evidence at HEAD |
| --- | --- |
| The teardown defer is armed only after `authenticateAndRegister` returns | `internal/worker/handler.go:253-261`. The `err != nil` return is `:254-256`; `defer h.teardownConnection(workerID, sender)` is `:261`. It captures `workerID string` and `sender *workerSender`, **both of which only exist on the success path** - which is exactly why it cannot be hoisted to cover `finishRegister`'s interior. |
| `finishRegister` acquires state before it can fail | `:565-624`. `RegisterWorkerConnection` at `:566-573`; `workerID := uuidStr(updated.ID)` at `:575`; `h.grace.Cancel(workerID)` at `:578-580`; `reconcileRunningTasks` at `:583-586`; `applyInventory` at `:589-591`; `stream.Send(RegisterResponse)` at `:595-605`; `NewWorkerSender` + `registry.Register` at `:608-610`. Item's ordering claim: correct. |
| `RegisterWorkerConnection` really acquires all three pieces of state | `internal/store/query/workers.sql` (generated: `internal/store/workers.sql.go:1105-1114`): `SET status = 'online', last_seen_at = $2, disconnected_at = NULL, connection_epoch = connection_epoch + 1`. |
| Two later statements can return without any teardown | `:583-586` returns `fmt.Errorf("reconcile: %w", err)`; `:595-605` returns `fmt.Errorf("send register response: %w", err)`. Both return to `authenticateAndRegister` -> `Connect:253-256`, which returns before `:261`. No sender was registered, so nothing unregisters; `markWorkerOffline` is never called; no grace timer is re-armed. **The strand is real.** |
| Nothing in the gRPC layer cleans up after `Connect` returns an error | `cmd/relay-server/main.go:190-204` registers the handler with `relayv1.RegisterAgentServiceServer(grpcSrv, agentHandler)` and serves on a `netlimit`-wrapped listener. `grpcServerOptions` (`cmd/relay-server/grpc_config.go:70`) installs `MaxConcurrentStreams`, keepalive policy and idle bounds - **no unary or stream interceptor exists anywhere in the tree.** A `Connect` error closes the stream and that is all. |
| `h.grace.Cancel` throws away the previous disconnect's pending requeue irrecoverably | `internal/worker/grace.go:96-103`: `Cancel` stops the timer and `delete`s the map entry. There is no "restore". |
| The item's backstop claim is directionally right | `internal/scheduler/watchdog.go:48` - `DefaultMaxAssignment = 24 * time.Hour`, overridable by `RELAY_TASK_MAX_ASSIGNMENT`. |

### Refuted or materially corrected

1. **The 24h watchdog does not requeue the stranded tasks - it fails them.** The item says "no requeue happens until the assignment watchdog fires". The watchdog writes `Status: "timed_out"` (`internal/scheduler/watchdog.go:208-216`), a terminal status, and then `finalizeTerminalTask` + `NotifyTaskCompleted`. So the 24h backstop does not eventually re-run the work; it marks it failed a day later. The damage is worse than the item claims.

2. **The 24h watchdog never touches `workers.status`.** It only writes `tasks`. Grep for `MarkWorkerOffline` returns exactly one production call site: `internal/worker/handler.go:1337`, inside `markWorkerOffline`, reachable only from `teardownConnection`. So for the **worker row** there is no 24h backstop and no backstop at all at runtime - it stays `online` until the next successful registration or a process restart.

3. **The one runtime mechanism that would have caught this is disabled by the same missing line.** `internal/metrics/sweep.go:59-82` is a liveness sweeper that flips a connected-looking worker `online` -> `stale` after `staleAfter`. It skips any worker for which `LastSampleAt` reports `tracked == false` (`sweep.go:67-72`), and `LastSampleAt` returns `false` for a worker never passed to `Activate` (`internal/metrics/store.go:104-110`). `h.Metrics.Activate` is called at `handler.go:612-614` - **below both failure points**. And the previous disconnect's `markWorkerOffline` called `h.Metrics.Clear` (`:1350-1352`), deleting the entry. So the stranded worker is untracked and the sweeper walks past it. This is the single most important finding in the review: the mechanism that exists to notice a worker that stopped reporting cannot see this one.

4. **Partial self-healing exists, but only on two paths, and neither is a runtime backstop.**
   - *The agent reconnects.* `reconnectAndRegister` -> `finishRegister` runs again, bumping the epoch and re-reconciling. But the failure this plan fixes is caused by the peer vanishing, which is precisely the case where the agent may never come back (host died, agent killed). Self-healing exists exactly where the bug does not matter.
   - *The server restarts.* `seedGraceTimersFromActiveTasks` (`cmd/relay-server/main.go:341-361`) enumerates `ListGraceCandidates` and, because the stranded row has `disconnected_at IS NULL`, arms a full-window timer. That requeues the tasks. It does not fix `workers.status`.

5. **The two failure paths are NOT unequally reachable, and the item's implied "reconcile needs a database fault" is wrong.** `ctx` in `finishRegister` is `stream.Context()` (`Connect:242`). A peer that vanishes between `RegisterWorkerConnection` and `GetActiveTasksForWorker` cancels that context, and pgx returns the context error from `Query`. So the reconcile arm has **the same production trigger as the send arm** - a peer that went away - it just lands a few lines earlier. Both are reachable without any database being unhealthy.

6. **The reconcile arm, and only the reconcile arm, is provable without Docker.** `applyInventory` (`:589`, `:1387-1411`) calls `pgx.BeginTxFunc(ctx, h.pool, ...)` **unconditionally**, including for an empty inventory, and `h.pool` is a concrete `*pgxpool.Pool` with no interface seam. A `Handler` built with a nil pool therefore cannot reach the `stream.Send` at `:595` at all. The reconcile arm returns at `:585`, four lines above that call, and never touches `h.pool`. This is what decides the lane split below; it was not obvious and had to be traced.

7. **The item's cited line numbers are all stale.** `Connect`'s defer: item says `:254-256`, actually `:261`. `grace.Cancel`: `:579` (correct by luck). `reconcileRunningTasks`: `:583` (correct). The send: item says `:594`, the `if err := stream.Send(` line is `:595`. The wake-gate comment: item says `:757-764`, actually `:753-767` with the load-bearing sentence at `:759-764`. `teardownConnection`: item says `:1309-1324`, the doc comment starts at `:1304`.

### Verdict

**The bug reproduces, by both named paths, and nothing already covers it.** The reproduction below drives the reconcile path in the default lane and the send path in the integration lane. This is not a refutation - but three of the item's supporting claims were wrong in ways that change the fix's justification, and the strongest of them (finding 3) is the reason a fix is worth shipping rather than deferring to an existing sweeper.

---

## Design decisions, with their arguments

### What the release does: BOTH offline and a fresh grace timer

Not "either". `RegisterWorkerConnection` acquired two things and both must be given back:

- `status='online'`, `disconnected_at=NULL` - undone by `markWorkerOffline`, which is already epoch-fenced and already publishes the `offline` SSE event and clears metrics.
- The previous disconnect's pending requeue, destroyed by `grace.Cancel` - undone by arming a fresh timer.

**The discarded timer cannot be restored, and that is a load-bearing detail, not a convenience.** The old timer carried the *old* `connection_epoch`. `RequeueWorkerTasksIfEpoch` fences on `workers.connection_epoch` (`internal/store/query/tasks.sql:721-722`), and the row has since moved on, so re-arming at the old epoch would move zero rows - a silent no-op that looks like a fix. The new timer must carry the epoch **this registration created**.

### What an operator sees afterwards

`GET /v1/workers` shows the worker `offline` within milliseconds of the failed registration instead of `online` forever, and one `worker` SSE frame with `{"status":"offline"}` is published. Its running tasks return to `pending` one `RELAY_WORKER_GRACE_WINDOW` (default `2m`, README:276) after the failed registration, and are re-dispatched - rather than being marked `timed_out` 24 hours later by the watchdog. The grace clock restarts from the failed registration rather than from the original disconnect, which is strictly later than "as if the registration never happened" and strictly, enormously earlier than the status quo. That trade is the price of `grace.Cancel` being irreversible and is accepted deliberately.

For a *first enrollment* that fails this way, the release arms a timer for a worker with no tasks. It fires once, requeues nothing and deletes its own map entry (`grace.go:65-72`). Harmless and bounded.

### The ownership check, stated exactly

On this path the ownership check is **one predicate: `workers.connection_epoch = $4` inside `MarkWorkerOfflineIfEpoch`** (`internal/store/query/workers.sql:46-54`), compared against `updated.ConnectionEpoch` - the value **this** call's `RegisterWorkerConnection` returned. If a fresher connection has registered in the meantime the row holds a higher epoch, the UPDATE matches zero rows, `markWorkerOffline` returns `0`, and the release returns having touched nothing. The `== 0` early return is therefore a correctness control, not an optimisation.

`teardownConnection`'s `registry.UnregisterIf(workerID, sender)` pointer-identity gate is a *second, earlier* check that this path deliberately does not have and must not fake: **a failed registration has no sender in the registry** - that is what makes it a failed registration - so there is nothing to compare, and calling `UnregisterIf` with any other sender would be the clobber the invariant forbids. Do not add a registry call to the new path.

Does arming the release earlier widen the window in which a stale cleanup fires against a fresh registration? Yes, and the fence already covers it, in both interleavings:

- *Our release runs first, the fresh connection registers second.* We mark offline at epoch N and arm a timer at N. The fresh connection's `RegisterWorkerConnection` bumps to N+1 and its `grace.Cancel` deletes our timer. Correct.
- *The fresh connection registers first, our release runs second.* The row holds N+1, our `MarkWorkerOfflineIfEpoch(N)` matches zero rows, we return before touching grace. Correct, and this is exactly the interleaving `TestTeardownConnection_GapPath_SqlFenceBlocksOfflineAndRequeue` (`handler_teardown_test.go:199-274`) already pins for the other caller.

One consequence worth naming rather than discovering later: if connection A is live and registered, and connection B for the same worker registers and then fails, B's release **does** win - it holds the newer epoch. A's tasks are requeued and A's metrics are cleared. That is correct and is not new: A was superseded the moment B's `RegisterWorkerConnection` bumped the epoch, and the same thing happens today if B succeeds and then disconnects a second later. A's own later teardown finds `UnregisterIf` true but `MarkWorkerOfflineIfEpoch(A's epoch)` zero, and does nothing.

### Why a `defer` + flag rather than a call at each error return

Two error returns exist today. Writing `h.releaseWorkerGeneration(...)` at each is more visible and is exactly the shape that rotted into this bug: the next early return added below will not have it. The `defer` makes the release unconditional at the point the state is acquired, which is what CLAUDE.md's first invariant asks for read in the acquire direction. The flag is flipped at **one** place so the two release paths are mutually exclusive by construction.

The flag is named `handedOff`, not `registered`: it names the ownership transfer to `Connect`'s defer, and "registered" is already three other things in this file.

---

## File structure

| File | Change | Responsibility |
| --- | --- | --- |
| `internal/worker/handler.go` | Modify `:575-580` (arm the release), `:610` (hand off), `:1304-1324` (extract `releaseWorkerGeneration`) | The whole fix. ~45 lines of which ~35 are comment. |
| `internal/worker/handler.go` | Modify comments at `:258-260`, `:577`, `:753-767`, `:1326-1329`, `:1356-1359` | Prose that describes the registration window and goes stale under this change. |
| `internal/worker/handler_register_strand_test.go` | **Create** (`package worker`, NO build tag) | Default-lane reproduction of the reconcile arm + the superseded-epoch ownership guard. |
| `internal/worker/handler_register_strand_integration_test.go` | **Create** (`//go:build integration`, `package worker_test`) | Real-Postgres reproduction of the `RegisterResponse`-send arm, end to end through the grace timer to a requeued task. |
| `docs/backlog/bug-2026-08-23-failed-finishregister-strands-worker-online.md` | `git mv` to `docs/backlog/closed/` via `/backlog close` | Required scope, not cleanup. |

**Critical file:** `internal/worker/handler.go`. Everything else is tests and prose.

**Files that must NOT change:** any `*.sql`, any `*.sql.go`, `models.go`, `internal/worker/grace.go`, `internal/worker/registry.go`, `internal/worker/sender.go`, and every existing `_test.go` file. `git diff --stat` at the end must list exactly the five paths above.

---

## Test lane decision, stated plainly

**The headline reproduction lives in the DEFAULT lane** (`go test ./internal/worker/...`, which is what `go-ci` runs as `go test -race ./...` with no tag and no container). This is worth the effort: `idea-2026-08-23-integration-only-guards-ci-never-runs` is open precisely because this package's guards keep landing behind `//go:build integration`, and `internal/worker` has an established DB-free precedent - `tasklog_fence_counter_test.go` drives real `store.Queries` calls through a hand-written `store.DBTX`, and `handler_registration_deadline_test.go` drives real `Connect` calls through a hand-written `scriptedStream`. This slice needs both at once and nothing more.

**The `RegisterResponse`-send arm cannot join it,** for the mechanical reason in finding 6: `applyInventory` at `:589` unconditionally opens a transaction on the concrete `*pgxpool.Pool` between the reconcile and the send, so a pool-less fixture panics before reaching `stream.Send`. Its proof is integration-tagged. Do not "fix" this by making `h.pool` an interface - that is a different slice and would be unrelated refactoring here.

---

## Task 1: Reproduce the strand in the default lane (RED)

**Files:**
- Create: `internal/worker/handler_register_strand_test.go`
- Read for context: `internal/worker/handler.go:241-315` (`Connect`), `:565-624` (`finishRegister`), `internal/worker/handler_registration_deadline_test.go:26-58` (`scriptedStream`, reused), `internal/worker/tasklog_fence_counter_test.go:34-85` (the `store.DBTX` stub precedent)

- [ ] **Step 1: Write the failing test file**

Create `internal/worker/handler_register_strand_test.go` with exactly this content:

```go
package worker

import (
	"context"
	"errors"
	"sync"
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

// strandDB is the narrowest store.DBTX that drives an entire reconnect
// registration - GetWorkerByAgentTokenHash, RegisterWorkerConnection,
// GetActiveTasksForWorker, MarkWorkerOfflineIfEpoch - WITHOUT Postgres, which is
// what puts this proof in the lane CI actually runs (go-ci: `go test -race ./...`,
// no tag, no container). It is the same seam tasklog_fence_counter_test.go uses,
// one layer up.
//
// IT WORKS BECAUSE THE FAILING PATH NEVER TOUCHES h.pool, and that is a fact
// about line numbers rather than a design choice. finishRegister returns on
// reconcileRunningTasks' error at handler.go:583-586, four lines ABOVE the
// applyInventory call that opens a transaction on the concrete *pgxpool.Pool -
// unconditionally, even for an empty inventory. A nil pool is therefore a
// complete fixture for THIS arm and would panic one statement later. That is
// also why the RegisterResponse-send arm cannot live in this lane; its proof is
// TestRegisterWorker_SendFailureReleasesTheGeneration, //go:build integration.
type strandDB struct {
	mu       sync.Mutex
	queryErr error // returned by Query, i.e. by GetActiveTasksForWorker
	execTag  string
	execs    []strandExec
}

type strandExec struct {
	sql  string
	args []any
}

func (d *strandDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execs = append(d.execs, strandExec{sql: sql, args: args})
	return pgconn.NewCommandTag(d.execTag), nil
}

func (d *strandDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return nil, d.queryErr
}

func (d *strandDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return strandWorkerRow{}
}

func (d *strandDB) execsSeen() []strandExec {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]strandExec, len(d.execs))
	copy(out, d.execs)
	return out
}

// strandWorkerID is the id every worker row this stub returns carries. Both
// QueryRow statements on the reconnect path scan the same store.Worker, so one
// row serves both - and they MUST agree on the id, because finishRegister passes
// GetWorkerByAgentTokenHash's id into RegisterWorkerConnection and then renders
// RegisterWorkerConnection's back out as the registry key and the grace key.
var strandWorkerID = pgtype.UUID{
	Bytes: [16]byte{0x5a, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	Valid: true,
}

// strandEpoch is what EVERY int32 column of the stub row scans as, including
// connection_epoch - the only one finishRegister reads. Filling by DESTINATION
// TYPE rather than by position means a reordered column list cannot turn a
// success into a failure; the cost is that cpu_cores, ram_gb, gpu_count and
// max_slots come back as 7 too, which nothing on this path looks at.
const strandEpoch int32 = 7

type strandWorkerRow struct{}

func (strandWorkerRow) Scan(dest ...any) error {
	for _, d := range dest {
		switch v := d.(type) {
		case *pgtype.UUID:
			*v = strandWorkerID
		case *int32:
			*v = strandEpoch
		case *string:
			*v = "strand-host"
		case **string:
			*v = nil
		case *[]byte:
			*v = []byte("{}")
		case *bool:
			*v = false
		case *pgtype.Timestamptz:
			*v = pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true}
		}
	}
	return nil
}

// graceFire records one grace-timer expiry: which worker, at which epoch.
type graceFire struct {
	workerID string
	epoch    int32
}

// newStrandHandler wires a Handler whose registration fails inside
// reconcileRunningTasks, plus a grace registry whose expiries are observable.
//
// onExpire RECORDS rather than requeueing: this lane has no database, and the
// question is whether a timer was armed and at WHICH epoch. The 20ms window
// makes an armed timer observable in milliseconds and a missing one observable
// as a timeout.
func newStrandHandler(t *testing.T, queryErr error, execTag string) (*Handler, *strandDB, <-chan graceFire) {
	t.Helper()
	db := &strandDB{queryErr: queryErr, execTag: execTag}
	fired := make(chan graceFire, 4)
	grace := NewGraceRegistry(20*time.Millisecond, func(workerID string, epoch int32) {
		fired <- graceFire{workerID: workerID, epoch: epoch}
	})
	t.Cleanup(grace.Stop)
	h := &Handler{
		q:                   store.New(db),
		registry:            NewRegistry(),
		broker:              events.NewBroker(),
		grace:               grace,
		triggerDispatch:     func() {},
		RegistrationTimeout: 5 * time.Second,
	}
	return h, db, fired
}

// strandStream is a reconnect RegisterRequest and nothing else. The agent token
// is never validated against anything: strandDB answers every QueryRow with a
// worker row, which is what makes GetWorkerByAgentTokenHash succeed.
func strandStream(t *testing.T) *scriptedStream {
	t.Helper()
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
	t.Cleanup(func() { close(s.release) })
	return s
}

// TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration
// is the item's own first acceptance step, and it had never been run.
//
// By the time reconcile fails, RegisterWorkerConnection has flipped the row to
// 'online', bumped connection_epoch to 7 and cleared disconnected_at, and
// grace.Cancel has thrown away the requeue timer the PREVIOUS disconnect armed.
// At HEAD nothing happens next: Connect's `defer h.teardownConnection` is armed
// only AFTER finishRegister returns (handler.go:261) and needs the sender only
// the success path creates, so on this path it is never armed at all.
//
// NOTHING ELSE CATCHES IT, which is what makes this worth a test rather than a
// comment. The metrics liveness sweeper (internal/metrics/sweep.go:59-82) is the
// one runtime mechanism that flips a connected-looking worker to 'stale', and it
// skips any worker LastSampleAt reports as untracked - which this one is,
// because Metrics.Activate is at handler.go:612, four lines BELOW the failure,
// and the previous disconnect's markWorkerOffline already called Metrics.Clear.
// The stale-task watchdog is not a substitute either: it marks tasks timed_out
// at RELAY_TASK_MAX_ASSIGNMENT (24h) rather than requeueing them, and it never
// writes workers.status at all.
func TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration(t *testing.T) {
	h, db, fired := newStrandHandler(t, errors.New("connection reset by peer"), "UPDATE 1")

	err := h.Connect(strandStream(t))

	require.Error(t, err)
	require.Contains(t, err.Error(), "reconcile",
		"fixture: the registration must fail inside reconcileRunningTasks, AFTER "+
			"RegisterWorkerConnection acquired the generation. Any other error means this test is "+
			"driving the wrong arm and its assertions below prove nothing.")

	execs := db.execsSeen()
	require.Len(t, execs, 1,
		"a registration that failed after RegisterWorkerConnection must release the generation it "+
			"acquired. Nothing else will: the worker row is status='online' at connection_epoch 7 "+
			"with no connection behind it, the previous disconnect's requeue timer was destroyed by "+
			"grace.Cancel, and the liveness sweeper walks past this worker because Metrics.Activate "+
			"is never reached.")
	assert.Contains(t, execs[0].sql, "status = 'offline'",
		"the one statement must be MarkWorkerOfflineIfEpoch")
	assert.Contains(t, execs[0].sql, "connection_epoch = $4",
		"and it must carry its epoch fence")
	assert.Contains(t, execs[0].args, strandEpoch,
		"it must be fenced on the epoch THIS registration created (7). That fence is the WHOLE of "+
			"the ownership check on this path: a failed registration has no sender in the registry "+
			"to compare against, so there is no second gate behind it.")

	select {
	case f := <-fired:
		assert.Equal(t, uuidStr(strandWorkerID), f.workerID)
		assert.Equal(t, strandEpoch, f.epoch,
			"the timer must be armed at the epoch this registration created. Re-arming at the "+
				"PREVIOUS epoch - trying to restore the timer grace.Cancel discarded - is a silent "+
				"no-op: RequeueWorkerTasksIfEpoch fences on workers.connection_epoch and the row now "+
				"holds 7.")
	case <-time.After(3 * time.Second):
		t.Fatal("no grace timer was armed after the failed registration. grace.Cancel destroyed the " +
			"previous disconnect's pending requeue, so without a fresh timer these tasks are not " +
			"requeued at all - the only thing left is the 24h stale-task watchdog, which marks them " +
			"timed_out rather than re-running them.")
	}

	require.Error(t, h.registry.Send(uuidStr(strandWorkerID), &relayv1.CoordinatorMessage{}),
		"a failed registration must leave no sender in the registry")
}

// TestConnect_ASupersededFailedRegistrationReleasesNothing is the
// Identity-checked-teardown half, and it is why the release goes through
// markWorkerOffline's RETURN VALUE rather than arming grace directly.
//
// Arming a release earlier widens the window in which a DEAD registration's
// cleanup can run against a LIVE one. The scenario: our RegisterWorkerConnection
// made epoch 7 and our reconcile then failed, but by the time our deferred
// release runs a fresher connection has registered and the row holds 8. Postgres
// answers that with zero rows affected - which is what "UPDATE 0" below stands
// for - and the release must stop there. Arming a grace timer anyway would leave
// a live agent's running tasks one fence away from being requeued out from under
// it.
//
// IT IS RED AT HEAD for the same reason as the test above (no release is
// attempted at all), so it is not the discriminating test for the fix's
// EXISTENCE. Its discriminating power is against the fix's SHAPE: delete the
// `== 0` early return from releaseWorkerGeneration and this test fails while the
// strand test above still passes.
func TestConnect_ASupersededFailedRegistrationReleasesNothing(t *testing.T) {
	h, db, fired := newStrandHandler(t, errors.New("connection reset by peer"), "UPDATE 0")

	err := h.Connect(strandStream(t))
	require.Error(t, err)
	require.Contains(t, err.Error(), "reconcile", "fixture: same arm as the strand test")

	require.Len(t, db.execsSeen(), 1,
		"the release must still be ATTEMPTED. The epoch fence is what decides ownership here, and "+
			"Postgres evaluates it - the caller cannot know in advance whether it still owns the "+
			"generation, so it must ask.")

	select {
	case f := <-fired:
		t.Fatalf("a superseded registration armed a grace timer for %s at epoch %d. The zero rows "+
			"MarkWorkerOfflineIfEpoch returned mean a FRESHER connection already owns this worker; "+
			"the only thing then standing between this timer and a requeue of a healthy agent's "+
			"running tasks is RequeueWorkerTasksIfEpoch's own fence.", f.workerID, f.epoch)
	case <-time.After(500 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail for the STATED reason**

Run:

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && go test ./internal/worker/... -run 'TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration|TestConnect_ASupersededFailedRegistrationReleasesNothing' -v -timeout 60s
```

Expected: **both FAIL**, and the first failure in each must be the `require.Len(t, execs, 1)` assertion, reading approximately:

```
--- FAIL: TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration
    handler_register_strand_test.go:NNN:
        Error:  "[]" should have 1 item(s), but has 0
        Test:   TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration
        Messages: a registration that failed after RegisterWorkerConnection must release the
                  generation it acquired. ...
```

**This is the gate on the RED being real.** Two `require`s run *before* it - `require.Error` and `require.Contains(err, "reconcile")` - and they must both PASS. If either fails instead, the fixture is broken (the stub row did not satisfy `GetWorkerByAgentTokenHash`, or the credential did not route to `reconnectAndRegister`) and the test is not yet evidence of anything. Fix the fixture before proceeding. If the run panics with a nil-pointer dereference inside `pgxpool`, the test reached `applyInventory`, meaning `Query` did not return an error - check `queryErr` is non-nil.

- [ ] **Step 3: Do NOT commit yet**

The tree has failing tests. Task 2 makes them pass and the two commit together.

---

## Task 2: Release the generation the moment it is acquired (GREEN)

**Files:**
- Modify: `internal/worker/handler.go:1304-1324` (extract the release), `:575-580` (arm it), `:610` (hand off)

- [ ] **Step 1: Extract the release out of `teardownConnection`**

Replace `internal/worker/handler.go:1304-1324` in full - that is the doc comment plus the whole body of `teardownConnection` - with:

```go
// teardownConnection runs when a Connect stream ends. It always stops this
// connection's own send goroutine, but only tears down shared worker state
// (DB offline flag, grace timer / requeue) when this connection still owns the
// worker's registry slot. A newer connection for the same worker must not be
// clobbered (Identity-checked teardown invariant).
func (h *Handler) teardownConnection(workerID string, sender *workerSender) {
	owned := h.registry.UnregisterIf(workerID, sender)
	sender.Close() // always stop our own send goroutine
	if !owned {
		return // a newer connection owns this worker; leave shared state alone
	}
	h.releaseWorkerGeneration(workerID, sender.connEpoch)
}

// releaseWorkerGeneration ends the worker generation identified by epoch: it
// marks the worker offline and then either arms the grace timer or requeues the
// worker's tasks directly. It is the ONE place shared worker state is released,
// and it has exactly two callers - teardownConnection above, when a registered
// stream ends, and finishRegister's deferred release, when a registration failed
// after RegisterWorkerConnection had already acquired the generation. Keeping
// one body is the point: those two paths must not be able to drift apart.
//
// THE EPOCH ARGUMENT IS THE OWNERSHIP CHECK, AND ON THE SECOND CALLER IT IS THE
// ONLY ONE. It is compared, inside MarkWorkerOfflineIfEpoch's WHERE clause,
// against workers.connection_epoch as it stands right now; a caller whose
// generation has been superseded by a fresher RegisterWorkerConnection matches
// zero rows, gets 0 back, and returns having touched nothing. That early return
// is load-bearing rather than an optimisation: without it a superseded caller
// arms a grace timer against a LIVE worker, and the timer's own
// RequeueWorkerTasksIfEpoch fence becomes the only thing between it and a
// requeue of a healthy agent's running tasks.
// TestConnect_ASupersededFailedRegistrationReleasesNothing is what holds it, in
// the default lane.
//
// teardownConnection's registry.UnregisterIf gate is a SECOND, EARLIER check
// that this function deliberately does not duplicate and the failed-registration
// caller deliberately does not have: that caller has no sender in the registry
// to compare against - which is precisely what makes it a failed registration -
// so sender identity is unavailable there and the epoch is the whole of the
// question. Do NOT add a registry call here to "make the two paths symmetric";
// unregistering a sender this caller never registered is the clobber the
// invariant forbids.
func (h *Handler) releaseWorkerGeneration(workerID string, epoch int32) {
	if h.markWorkerOffline(workerID, epoch) == 0 {
		return // a fresher connection holds the epoch; leave grace/requeue alone
	}
	if h.grace != nil {
		h.grace.Start(workerID, epoch)
	} else {
		h.requeueWorkerTasks(workerID, epoch)
	}
}
```

- [ ] **Step 2: Arm the release inside `finishRegister`**

In `internal/worker/handler.go`, replace lines `:575-580` - from `workerID := uuidStr(updated.ID)` through the closing brace of the `if h.grace != nil` block - with:

```go
	workerID := uuidStr(updated.ID)

	// THE GENERATION IS ACQUIRED, SO ITS RELEASE IS ARMED HERE AND NOT ONE
	// STATEMENT LATER. RegisterWorkerConnection above has already flipped the row
	// to 'online', bumped connection_epoch and cleared disconnected_at, and the
	// grace.Cancel below is about to throw away the pending requeue from the
	// PREVIOUS disconnect. Everything after this point can still fail -
	// reconcileRunningTasks returns an error, and so does the RegisterResponse
	// send - and until this defer existed those two returns left the worker
	// 'online' at a live epoch with no connection behind it and no timer to clean
	// up after it.
	//
	// CONNECT'S OWN DEFER CANNOT COVER THIS AND NEVER COULD. It is armed only
	// after this function RETURNS, and it takes the *workerSender that only the
	// success path below creates - so on a failed registration there is nothing
	// to arm it with. The two defers partition the window rather than overlapping
	// it.
	//
	// NOTHING ELSE CATCHES THE GAP EITHER, which is why this is a defer and not a
	// backlog note. The metrics liveness sweeper skips any worker Metrics has not
	// been told to track, and Metrics.Activate is below the failure points; the
	// stale-task watchdog marks tasks timed_out at RELAY_TASK_MAX_ASSIGNMENT
	// (24h) rather than requeueing them, and never writes workers.status at all.
	//
	// This is CLAUDE.md's "End the generation before releasing the resource" read
	// in the acquire direction: take the state and arm its release in the same
	// breath, so no future early return can be added that forgets to. handedOff
	// is flipped at exactly ONE place, so the two releases are mutually exclusive
	// by construction and neither can be skipped.
	handedOff := false
	defer func() {
		if !handedOff {
			h.releaseWorkerGeneration(workerID, updated.ConnectionEpoch)
		}
	}()

	// Agent reconnected within its grace window - stop the requeue timer. THIS IS
	// THE SECOND HALF OF THE ACQUISITION, and is why the release above is armed
	// before it rather than after: the cancelled timer is not recoverable
	// (GraceRegistry.Cancel stops it and deletes the entry), so a failure below
	// has to arm a FRESH one at the epoch RegisterWorkerConnection just created.
	// Restoring the OLD epoch's timer would be a silent no-op -
	// RequeueWorkerTasksIfEpoch fences on workers.connection_epoch and the row has
	// moved on.
	if h.grace != nil {
		h.grace.Cancel(workerID)
	}
```

- [ ] **Step 3: Hand ownership over at the registry**

In `internal/worker/handler.go`, replace the single line `h.registry.Register(workerID, sender)` (at `:610`) with:

```go
	h.registry.Register(workerID, sender)

	// OWNERSHIP HANDOFF. From this instant the release belongs to Connect's
	// `defer h.teardownConnection(workerID, sender)`, which Connect arms the
	// moment this function returns a nil error. Setting this flag any EARLIER
	// reopens the strand for the RegisterResponse send; setting it any LATER
	// makes a successful registration release its own generation.
	//
	// EVERYTHING BELOW THIS LINE MUST STAY INFALLIBLE. Connect arms its defer only
	// on a nil error, so a future statement here that returns an error would be
	// covered by neither release and would additionally strand a live sender in
	// the registry. If such a statement is ever needed, it must log and continue
	// (as applyInventory does), not return.
	handedOff = true
```

- [ ] **Step 4: Run the two new tests and confirm GREEN**

Run:

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && go test ./internal/worker/... -run 'TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration|TestConnect_ASupersededFailedRegistrationReleasesNothing' -v -timeout 60s
```

Expected: `--- PASS` for both, `ok relay/internal/worker`.

- [ ] **Step 5: Prove the ownership guard is load-bearing, then revert the mutation**

Temporarily delete the two-line early return from `releaseWorkerGeneration`, so the body reads:

```go
func (h *Handler) releaseWorkerGeneration(workerID string, epoch int32) {
	h.markWorkerOffline(workerID, epoch)
	if h.grace != nil {
		h.grace.Start(workerID, epoch)
	} else {
		h.requeueWorkerTasks(workerID, epoch)
	}
}
```

Run the same command. Expected: `TestConnect_ASupersededFailedRegistrationReleasesNothing` **FAILS** with "a superseded registration armed a grace timer for 5a010203-... at epoch 7", and `TestConnect_ARegistrationFailingAfterRegisterWorkerConnectionReleasesTheGeneration` still **PASSES**. That asymmetry is the proof: the guard test discriminates something the strand test cannot see.

Then restore the early return exactly as written in Step 1 and re-run to confirm both PASS again. The discriminating input (`"UPDATE 0"`) stays in the tree permanently, in a test - the mutation is thrown away, the coverage is not.

- [ ] **Step 6: Run the whole default suite**

Run:

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && make test
```

Expected: all packages `ok` or `no test files`. Nothing in `internal/worker`'s default lane regresses; no existing test file was touched.

- [ ] **Step 7: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && git add internal/worker/handler.go internal/worker/handler_register_strand_test.go && git commit -m "fix(worker): release the worker generation when registration fails after acquiring it

finishRegister acquired the worker's generation - status 'online', a bumped
connection_epoch, a cancelled grace timer - and then had two statements that
could still fail. Connect's teardown defer is armed only after finishRegister
returns and needs the sender only the success path creates, so on either failure
the worker sat 'online' with its tasks assigned to a connection that did not
exist.

Nothing else covered it: the metrics liveness sweeper skips workers Metrics was
never told to track, and Metrics.Activate is below both failure points; the
stale-task watchdog marks tasks timed_out at 24h rather than requeueing them and
never writes workers.status.

finishRegister now arms a deferred releaseWorkerGeneration the instant
RegisterWorkerConnection returns, handed off to Connect's defer at
registry.Register. The release body is shared with teardownConnection so the two
paths cannot drift. Ownership on the new path is MarkWorkerOfflineIfEpoch's epoch
fence and nothing else - a failed registration has no sender to identity-check
against - and the zero-rows early return is what stops a superseded registration
arming a timer against a live worker."
```

---

## Task 3: Prove the RegisterResponse-send arm against real Postgres

**Files:**
- Create: `internal/worker/handler_register_strand_integration_test.go`
- Read for context: `internal/worker/handler_test.go:32-112` (`seedWorkerWithAgentToken`, `fakeStream`, `newTestStore`), `internal/worker/handler_teardown_test.go:281-378` (the `onExpire`-wired-like-main.go pattern)

- [ ] **Step 1: Write the integration test**

Create `internal/worker/handler_register_strand_integration_test.go`:

```go
//go:build integration

package worker_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// sendFailStream is handler_test.go's fakeStream with one difference: Send
// fails. It is a separate type rather than a flag on fakeStream so that every
// existing test file in this package stays byte-identical.
type sendFailStream struct {
	msgs []*relayv1.AgentMessage
	pos  int
	ctx  context.Context
}

func (s *sendFailStream) Recv() (*relayv1.AgentMessage, error) {
	if s.pos >= len(s.msgs) {
		return nil, io.EOF
	}
	m := s.msgs[s.pos]
	s.pos++
	return m, nil
}

// Send is what a peer that vanished between RegisterWorkerConnection and the
// RegisterResponse looks like from the server's side. It is the plausible half
// of this bug: the reconcile arm needs the same peer to vanish a few lines
// earlier (its ctx is stream.Context()), this one needs nothing but a closed
// socket.
func (s *sendFailStream) Send(*relayv1.CoordinatorMessage) error {
	return errors.New("rpc error: code = Unavailable desc = transport is closing")
}

func (s *sendFailStream) Context() context.Context     { return s.ctx }
func (s *sendFailStream) RecvMsg(any) error            { return io.EOF }
func (s *sendFailStream) SendMsg(any) error            { return nil }
func (s *sendFailStream) SetHeader(metadata.MD) error  { return nil }
func (s *sendFailStream) SendHeader(metadata.MD) error { return nil }
func (s *sendFailStream) SetTrailer(metadata.MD)       {}

// TestRegisterWorker_SendFailureReleasesTheGeneration is the second of the two
// arms the strand can take, and the one this lane exists for: it needs a real
// pool, because applyInventory opens a transaction on *pgxpool.Pool
// unconditionally between the reconcile and the send, so a pool-less fixture
// cannot reach stream.Send at all.
//
// It carries what the default-lane proof cannot: the actual worker ROW and the
// actual TASK, through a real grace timer, to a real requeue.
func TestRegisterWorker_SendFailureReleasesTheGeneration(t *testing.T) {
	ctx := context.Background()
	q, pool := newTestStore(t)
	registry := worker.NewRegistry()
	broker := events.NewBroker()

	// onExpire wired exactly as cmd/relay-server/main.go:123-133 wires it, so
	// what this test proves is what production does.
	grace := worker.NewGraceRegistry(100*time.Millisecond, func(workerID string, epoch int32) {
		var id pgtype.UUID
		if err := id.Scan(workerID); err != nil {
			return
		}
		_, _ = q.RequeueWorkerTasksIfEpoch(context.Background(), store.RequeueWorkerTasksIfEpochParams{
			WorkerID: id, ConnectionEpoch: epoch,
		})
	})
	defer grace.Stop()

	h := worker.NewHandlerWithGrace(q, pool, registry, broker, func() {}, grace)

	wkID, rawToken := seedWorkerWithAgentToken(t, ctx, q, "strand-01")

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "strand-user", Email: "strand-user@test.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "strand-job", Priority: "normal", SubmittedBy: user.ID,
		Labels: []byte("{}"), ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "strand-task",
		Commands: []byte(`[["echo","hi"]]`), Env: []byte("{}"), Requires: []byte("[]"), Retries: 0,
	})
	require.NoError(t, err)
	claimed, err := q.ClaimTaskForWorker(ctx, store.ClaimTaskForWorkerParams{
		ID: task.ID, WorkerID: wkID,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), claimed.AssignmentEpoch)
	require.Equal(t, "dispatched", claimed.Status)

	// THE AGENT MUST REPORT THE RUNNING TASK. reconcileRunningTasks requeues any
	// task the coordinator has assigned that the agent did NOT report, so an
	// empty RunningTasks makes the task go pending before the send is even
	// attempted - and this test would then pass at HEAD for entirely the wrong
	// reason. Reporting it at the matching epoch makes reconcile a no-op and
	// leaves the requeue to be caused by the fix, or by nothing.
	stream := &sendFailStream{
		ctx: ctx,
		msgs: []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname:   "strand-01",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken},
				RunningTasks: []*relayv1.RunningTask{{
					TaskId: h.UUIDStringForTest(claimed.ID),
					Epoch:  int64(claimed.AssignmentEpoch),
				}},
			},
		}}},
	}

	err = h.Connect(stream)
	require.Error(t, err)
	require.Contains(t, err.Error(), "send register response",
		"fixture: the registration must fail on the RegisterResponse send, i.e. AFTER "+
			"RegisterWorkerConnection and grace.Cancel and AFTER a reconcile that moved nothing")

	wAfter, err := q.GetWorker(ctx, wkID)
	require.NoError(t, err)
	assert.Equal(t, "offline", wAfter.Status,
		"a registration that failed on the RegisterResponse send must not leave the worker 'online'. "+
			"GET /v1/workers is where an operator reads this, and at HEAD it says 'online' for as "+
			"long as the process lives - the liveness sweeper cannot correct it, because "+
			"Metrics.Activate is never reached on this path.")
	assert.True(t, wAfter.DisconnectedAt.Valid,
		"disconnected_at is what a server restart reads to decide how much grace remains "+
			"(seedGraceTimersFromActiveTasks); RegisterWorkerConnection cleared it a moment ago and "+
			"the release has to put it back")
	assert.Equal(t, int32(1), wAfter.ConnectionEpoch,
		"marking offline must NOT bump the epoch: the grace timer armed alongside it is fenced on "+
			"exactly this value, and bumping here would make its requeue a silent no-op")

	require.Eventually(t, func() bool {
		after, err := q.GetTask(ctx, task.ID)
		return err == nil && after.Status == "pending"
	}, 5*time.Second, 50*time.Millisecond,
		"the grace timer grace.Cancel discarded must be re-armed at the new epoch, or this task is "+
			"stranded on a connection that does not exist until the 24h stale-task watchdog marks it "+
			"timed_out - which fails the work rather than re-running it")

	after, err := q.GetTask(ctx, task.ID)
	require.NoError(t, err)
	assert.False(t, after.WorkerID.Valid, "a requeued task must be unassigned")
	assert.Equal(t, int32(2), after.AssignmentEpoch,
		"the requeue must bump assignment_epoch: returning a task to pending without ending its "+
			"generation is the epoch-fence invariant's own named counter-example")
}
```

- [ ] **Step 2: Run it and confirm GREEN with the fix in place**

Requires Docker Desktop running.

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_SendFailureReleasesTheGeneration -v -timeout 300s
```

Expected: `--- PASS`.

- [ ] **Step 3: Prove it is RED at HEAD by removing only the fix**

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && git stash push internal/worker/handler.go && go test -tags integration -p 1 ./internal/worker/... -run TestRegisterWorker_SendFailureReleasesTheGeneration -v -timeout 300s ; git stash pop
```

Expected while stashed: **FAIL**, first at

```
    Error: Not equal:
           expected: "offline"
           actual  : "online"
    Messages: a registration that failed on the RegisterResponse send must not leave the worker
              'online'. ...
```

`require.Contains(err, "send register response")` must still PASS while stashed - that is what proves the test is driving the intended arm rather than failing earlier. If the run fails on that line instead, the fixture is wrong (most likely the token or hostname does not match the seeded worker).

Confirm `git stash pop` succeeded and `git status` shows `internal/worker/handler.go` modified again.

- [ ] **Step 4: Run the full integration lane for this package**

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && go test -tags integration -p 1 ./internal/worker/... -timeout 900s
```

Expected: `ok relay/internal/worker`. In particular `TestHandler_RegisterNewWorker` and the four tests in `handler_teardown_test.go` must still pass - between them they are the standing proof that a SUCCESSFUL registration does not release its own generation (a `handedOff` flag never set would mark every freshly-registered worker offline immediately, reddening `TestHandler_RegisterNewWorker`'s `assert.Equal(t, "online", wk.Status)` at `handler_test.go:170`).

- [ ] **Step 5: Commit**

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && git add internal/worker/handler_register_strand_integration_test.go && git commit -m "test(worker): prove the RegisterResponse-send strand end to end

The second arm of the strand cannot be proven without Postgres: applyInventory
opens a transaction on the concrete *pgxpool.Pool between the reconcile and the
send, so a pool-less fixture panics before reaching stream.Send. This drives a
real reconnect whose RegisterResponse send fails, and asserts the worker row goes
offline at an unbumped epoch and its running task is requeued by the re-armed
grace timer.

The agent reports its running task deliberately: with an empty RunningTasks,
reconcile requeues the task before the send is attempted and the test passes at
HEAD for the wrong reason."
```

---

## Task 4: Correct every comment that describes the registration window

**Wrong prose about correct code is this repository's dominant defect class.** These five sites all describe behaviour Task 2 changed. Each edit below is exact; apply all five.

**Files:**
- Modify: `internal/worker/handler.go` at the five sites named below

- [ ] **Step 1: `Connect`'s teardown-defer comment (was `:258-260`)**

Find:

```go
	// Registered above, so the teardown defer must be armed BEFORE any path that
	// can return early below, or a failed connection leaves its sender in the
	// registry (identity-checked teardown).
	defer h.teardownConnection(workerID, sender)
```

Replace with:

```go
	// Registered above, so the teardown defer must be armed BEFORE any path that
	// can return early below, or a failed connection leaves its sender in the
	// registry (identity-checked teardown).
	//
	// IT COVERS EVERYTHING BELOW AND NOTHING ABOVE, which is the half this
	// comment used to leave unsaid and which cost a HIGH-severity strand.
	// finishRegister acquires the worker's generation - status 'online', a bumped
	// connection_epoch, a cancelled grace timer - several statements before it
	// returns the sender this defer needs, and two of its own statements can
	// still fail after that acquisition. Those are released by finishRegister's
	// OWN deferred release (see its handedOff block), not here, because this
	// defer cannot be armed without a sender that a failed registration never
	// creates. Between the two, every path that acquires the generation releases
	// it exactly once.
	defer h.teardownConnection(workerID, sender)
```

- [ ] **Step 2: The `grace.Cancel` comment**

Already rewritten as part of Task 2 Step 2. Verify it now reads "THIS IS THE SECOND HALF OF THE ACQUISITION" and no longer stands alone as a bare one-liner.

- [ ] **Step 3: The wake-gate comment inside `reconcileRunningTasks` (was `:753-767`)**

This is the item's own third acceptance criterion. The existing sentence "The gate is load-bearing on exactly one path: the RegisterResponse send fails and finishRegister returns early, never reaching its own trigger" is **still literally true** after the fix - `go h.triggerDispatch()` at the end of `finishRegister` is still not reached on that path. What changed is the surrounding implication that the early return is a dead end. Append a second paragraph; do not delete the first.

Find the line `	if requeued > 0 {` inside `reconcileRunningTasks` and insert the following immediately above it, after the existing comment block:

```go
	// THAT PATH IS NO LONGER A DEAD END, and the gate's job on it narrowed
	// accordingly. finishRegister now releases the generation it acquired when it
	// returns early (see its handedOff defer), and that release ends in either
	// grace.Start - whose expiry calls dispatcher.Trigger in cmd/relay-server -
	// or requeueWorkerTasks, which triggers dispatch itself. So a wake DOES
	// arrive on that path now even with this gate deleted. What the gate still
	// buys is PROMPTNESS for the rows this loop moved to pending here and now,
	// which would otherwise wait out RELAY_WORKER_GRACE_WINDOW (2m by default).
	// Keep it, and keep it counting matches rather than attempts: the paragraph
	// above is still the reason that switch is safe.
```

- [ ] **Step 4: `markWorkerOffline`'s doc comment (was `:1326-1329`)**

Find:

```go
// markWorkerOffline is called in a defer after the stream ends. It is fenced on
```

Replace that first sentence so the comment opens:

```go
// markWorkerOffline is called only from releaseWorkerGeneration, which reaches
// it from two places: a defer after a registered stream ends, and a registration
// that failed after RegisterWorkerConnection had already acquired the generation
// - in which case no stream ever carried traffic at all. It is fenced on
```

Leave the rest of that comment (`connection_epoch: if a fresher connection has bumped...`) exactly as it is.

- [ ] **Step 5: `requeueWorkerTasks`'s doc comment (was `:1356-1359`)**

Find:

```go
// requeueWorkerTasks requeues dispatched/running tasks for a disconnected
// worker, fenced on connection_epoch: if a fresher connection has bumped the
```

Replace the first clause so it opens:

```go
// requeueWorkerTasks requeues dispatched/running tasks for a worker whose
// generation has ended - a disconnect, or a registration that acquired the
// generation and then failed - fenced on connection_epoch: if a fresher
// connection has bumped the
```

Leave the rest unchanged.

- [ ] **Step 6: Re-read the neighbours that were checked and left alone**

Do not edit these; confirm by reading that they are still true, because a reviewer will ask:

- `internal/worker/sender.go:32-35` - "connEpoch is ... set once in finishRegister and never mutated. Teardown fences shared-state writes on it". Still exactly true; the new release path fences on the same number without going through a sender.
- `internal/worker/handler.go:593-594` - "At this point the worker is not yet in the registry, so no other goroutine can race us on stream.Send". Unchanged: the fix adds no send and no goroutine.
- `internal/worker/handler.go:678-685` and `:740-744` - both say the reconcile log sites run "BEFORE Connect allocates this connection's ingestLogLimiter". Still true; the fix adds no log line anywhere. **Do not add one** - `releaseWorkerGeneration` runs inside `finishRegister` on the failed path and has no budget, exactly as those two comments explain.
- `CLAUDE.md:41` - the `internal/worker/` code-map bullet. Still accurate; no new exported symbol.

- [ ] **Step 7: Verify nothing regressed and commit**

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && make test && git diff --stat
```

Expected: `make test` fully green, and `git diff --stat` lists `internal/worker/handler.go` only.

```bash
cd /d/dev/relay/.claude/worktrees/pr-merge-session-d3977d && git add internal/worker/handler.go && git commit -m "docs(worker): correct the prose describing the registration window

Five comments described a registration window in which a failure left nothing
behind. Connect's teardown-defer comment claimed to cover 'any path that can
return early' without saying it covers nothing ABOVE it; the wake-gate comment
treated finishRegister's early return as a dead end; markWorkerOffline and
requeueWorkerTasks both described themselves as disconnect-only. Each is now
stated at its true scope."
```

---

## Task 5: Close the backlog item

- [ ] **Step 1: Run the close command**

```
/backlog close failed-finishregister-strands-worker-online
```

This `git mv`s the file into `docs/backlog/closed/`, stamps the frontmatter, appends a `## Resolution` note and commits. Do not hand-edit `status:`.

- [ ] **Step 2: In the resolution note, record the three corrections**

The Resolution must say, in addition to what shipped:

- the 24h watchdog marks stranded tasks `timed_out` rather than requeueing them, and never touches `workers.status`, so for the worker row there was no backstop at all;
- the metrics liveness sweeper would have caught a stranded worker except that `Metrics.Activate` sits below both failure points, so the strand is invisible to it by construction;
- both named failure paths are equally reachable in production - `finishRegister`'s ctx is `stream.Context()`, so the reconcile arm needs a vanished peer, not a database fault.

---

## Phase 4 verification lanes

Run `/code-review` first, then fan out these four in one message:

1. **`relay-code-reviewer`, invariants lens.** Focus: does `releaseWorkerGeneration`'s epoch argument constitute a sufficient ownership check on the failed-registration path, given there is no sender to identity-check? Is there any interleaving in which the new defer fires against a live registration and the fence lets it through? Does `handedOff` have exactly one write site, and is anything between it and `finishRegister`'s return fallible?
2. **`relay-code-reviewer`, correctness lens.** Focus: the enrollment and auto-enroll paths also route through `finishRegister` - what does the new release do to a worker that has just been enrolled for the first time and has no tasks and no prior grace timer? Does the release's `broker.Publish` of `offline` ever produce an `offline` frame for a worker that never published an `online` one, and does the SPA handle that?
3. **`relay-code-reviewer`, security lens.** Focus: the release runs on a path an unauthenticated-until-a-moment-ago peer can drive by closing its socket at the right instant. Can a peer drive repeated register-then-vanish cycles to force unbounded `grace` map growth or unbounded `MarkWorkerOfflineIfEpoch` writes? (Bound the answer against `RELAY_GRPC_REGISTRATION_TIMEOUT` and the `netlimit` caps, and check `GraceRegistry`'s map deletes on fire.) Confirm no new log line was added on this path.
4. **`relay-integration-tester`.** Re-run both lanes, then add adversarial coverage: two concurrent `Connect` calls for the same worker where the second fails after `RegisterWorkerConnection`, asserting the first stays online and keeps its tasks.

---

## Phase 6 proposals

- The mutation in Task 2 Step 5 showed that `TestConnect_ASupersededFailedRegistrationReleasesNothing` is the only default-lane guard on the `== 0` early return in `releaseWorkerGeneration`. `teardownConnection`'s equivalent coverage (`TestTeardownConnection_GapPath_SqlFenceBlocksOfflineAndRequeue`) is integration-only. Worth noting on `idea-2026-08-23-integration-only-guards-ci-never-runs` as a second concrete instance, not a new item.
- `h.pool` being a concrete `*pgxpool.Pool` with no interface seam is what forces the `RegisterResponse`-send proof into the integration lane, and it will force the next one there too. If a future slice needs to unit-test anything below `applyInventory`, file an item then - do not pre-emptively refactor here.

---

## Self-review

- **Item acceptance criterion 1** (a test that is RED at HEAD and GREEN after): Task 1 Step 2 (default lane, reconcile arm) and Task 3 Step 3 (integration lane, send arm). Both have a stated expected failure message and both name the `require` that must PASS ahead of the failing one, so a broken fixture cannot masquerade as a RED.
- **Item acceptance criterion 2** (teardown stays identity-checked, `connEpoch` fencing still holds): Task 2 Step 1's comment states the check exactly; `TestConnect_ASupersededFailedRegistrationReleasesNothing` pins it; Task 2 Step 5 proves it discriminates by mutation and leaves the discriminating input behind; the four existing `handler_teardown_test.go` tests are unchanged and re-run in Task 3 Step 4.
- **Item acceptance criterion 3** (the wake-gate comment re-read and corrected): Task 4 Step 3, plus four neighbouring comments the item did not name and one deliberate no-change list in Step 6.
- **Placeholder scan:** every code step contains complete, compilable content; no "add error handling", no "similar to Task N", no TBD.
- **Type consistency:** `releaseWorkerGeneration(workerID string, epoch int32)` is spelled identically in Task 2 Steps 1, 2 and 5 and in the Task 4 Step 3 comment. `handedOff` is spelled identically in Task 2 Steps 2 and 3, Task 4 Step 1 and the Phase 4 brief. `strandEpoch`, `strandWorkerID`, `strandDB`, `graceFire`, `newStrandHandler`, `strandStream` are each defined once and used consistently. `store.DBTX` is satisfied with the exact three-method signature at `internal/store/db.go:14-18`.
- **Single-slice check:** one PR, one session. No `## Stage N` headings, so nothing to hand to `/backlog phases`.
