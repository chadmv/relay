package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/metrics"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	pool       *fakePool
	tx         *fakeTx
	stream     *scriptedStream
	metrics    *metrics.Store
	events     <-chan events.Event
	dispatched chan struct{}
	fired      <-chan graceFire
	release    func()
}

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

// fakeTx is a pgx.Tx that records the statements a transaction on this handler
// issues - applyInventory's, and, since it grew a QueryRow, both enrollment
// transactions' as well.
//
// THE EMBEDDED NIL pgx.Tx SUPPLIES THE METHODS THIS PATH NEVER CALLS, and
// supplies them as a panic rather than as a plausible zero value. That COUNT is
// deliberately not written down: it was "the eight methods", and the number went
// stale the moment QueryRow was overridden. Say which methods are live, not how
// many are not. The idiom is
// what makes an eleven-method interface cost four lines instead of forty.
//
// IT SHARES fenceStore's POLICY AND NOT ITS DIAGNOSTICS, which is worth being
// exact about. fenceStore (internal/scheduler/dispatch_fence_test.go) panics
// explicitly, with a message that says which contract was broken. A call on a
// nil embedded interface panics with a bare `invalid memory address or nil
// pointer dereference` and names nothing at all - the method is recoverable only
// from the stack trace. Failing loudly is still the right default if
// applyInventory ever grows a Query or a SendBatch, but whoever reads that
// failure gets a worse report than fenceStore's, so override the method
// explicitly if this path ever starts calling it.
//
// Commit and Rollback both return nil, which is correct rather than merely
// convenient: pgx's beginFuncExec defers a Rollback AFTER the Commit and
// propagates a rollback error only when it is not ErrTxClosed, so a nil from
// both is the quiet, successful shape.
//
// THEY ALSO COUNT, AND WITHOUT THAT THE INVENTORY TESTS ARE VACUOUS. "the
// DELETE was issued" is satisfied just as well by a transaction that issued it
// and then rolled back, which leaves the stale worker_workspaces rows exactly
// where an early return would have. Measured (mutation M15): making
// applyInventory's closure return an error instead of nil rolls back every
// inventory replace, and all 21 Go packages stayed green until these counters
// existed.
//
// COMMITS IS THE DISCRIMINATOR, NOT ROLLBACKS, because of the shape of pgx's
// beginFuncExec: it defers a Rollback unconditionally and then returns
// tx.Commit(ctx), so a SUCCESSFUL transaction records one commit and one
// rollback, while a failed one records no commit and two rollbacks (the error
// branch's and the defer's). Asserting rollbacks == 0 on the success path would
// therefore fail against correct code.
type fakeTx struct {
	pgx.Tx
	mu        sync.Mutex
	execErr   error
	script    *rowScript
	execTag   string
	execs     []strandExec
	commits   int
	rollbacks int
}

func (tx *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.execs = append(tx.execs, strandExec{sql: sql, args: args})
	if tx.execErr != nil {
		return pgconn.CommandTag{}, tx.execErr
	}
	tag := tx.execTag
	if tag == "" {
		// The historical value: zero rows affected, which is what every test
		// predating this field was written against.
		tag = "DELETE 0"
	}
	return pgconn.NewCommandTag(tag), nil
}

// QueryRow delegates to the shared rowScript, or - with none - keeps the
// historical strandWorkerRow. EVERY statement in both enrollment transactions is
// a QueryRow on the tx, so without this method those paths are unreachable in
// this lane: it fell through to the embedded nil pgx.Tx and panicked with a bare
// nil dereference one frame inside generated code.
func (tx *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if tx.script != nil {
		return tx.script.answer(sql, args)
	}
	return strandWorkerRow{}
}

func (tx *fakeTx) Commit(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.commits++
	return nil
}

func (tx *fakeTx) Rollback(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.rollbacks++
	return nil
}

// outcome reports how the transaction ENDED, which is the half "what statements
// were issued" cannot see.
func (tx *fakeTx) outcome() (commits, rollbacks int) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.commits, tx.rollbacks
}

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
	tx := &fakeTx{}
	pool := &fakePool{tx: tx}

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
		pool:     pool,
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
		pool:       pool,
		tx:         tx,
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

	commits, _ := f.tx.outcome()
	assert.Equal(t, 1, commits,
		"and the transaction must have COMMITTED. Issuing the DELETE is not the property that "+
			"matters - a replace that runs and then rolls back leaves exactly the stale rows an early "+
			"return would have, and the dispatcher goes on scoring warm-workspace affinity off them. "+
			"Mutation M15 (applyInventory's closure returning an error instead of nil) is green against "+
			"every other assertion in this package and dies here.")
}

// TestFinishRegister_SucceedsWhenTheInventoryTransactionFails pins the
// log-and-continue shape at finishRegister's applyInventory call - cited by name
// rather than by line, because the line number in this comment had already
// drifted once. applyInventory's error is deliberately swallowed there, and the
// "EVERYTHING BELOW THIS LINE MUST STAY INFALLIBLE" rule below it names that
// shape as the required one.
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

	commits, rollbacks := f.tx.outcome()
	require.Len(t, f.tx.execsSeen(), 1,
		"fixture: the inventory transaction must actually have been REACHED and the injected error "+
			"actually returned. Without this the test passes just as well against a build where "+
			"applyInventory never ran, and every assertion above would be about a registration that "+
			"had nothing to swallow.")
	assert.Equal(t, 0, commits,
		"the failed replace must not have committed")
	assert.GreaterOrEqual(t, rollbacks, 1,
		"and it must have rolled back, so the worker's previous rows survive a failed replace "+
			"intact rather than being half-cleared")

	require.Error(t, finish())
}

// TestFinishRegister_SucceedsWhenTheInventoryTransactionCannotBegin is the other
// half of the log-and-continue rule, and it is a DIFFERENT failure mode from its
// sibling above rather than a restatement of it. That one fails inside the
// transaction, where pgx rolls back and applyInventory returns the exec error.
// This one never opens a transaction at all - a pool with no free connection, a
// server that is up but refusing them - so BeginTxFunc returns before the closure
// is ever called and no statement is issued.
//
// The distinction is worth a test because the two arms exercise different code:
// the first proves a returned error is swallowed, the second proves the same of
// an error raised before there is anything to roll back. A registration refused
// because the pool was momentarily exhausted would be an outage caused entirely
// by bookkeeping.
//
// LIKE ITS SIBLING THIS IS NOT RED AT HEAD; it pins a non-goal. Its
// discriminating power is mutation M10 - turning applyInventory's log.Printf into
// a return - which reddens it exactly as it reddens the sibling.
func TestFinishRegister_SucceedsWhenTheInventoryTransactionCannotBegin(t *testing.T) {
	f := newSuccessFixture(t)
	workerID := uuidStr(strandWorkerID)
	f.pool.beginErr = errors.New("timeout: context deadline exceeded acquiring connection from pool")

	online, finish := f.startConnect(t)

	assert.JSONEq(t, `{"id":"`+workerID+`","status":"online"}`, string(online.Data),
		"a pool that cannot hand out a connection must not refuse the agent's registration. The "+
			"inventory replace is bookkeeping; the connection is the product.")
	require.NoError(t, f.h.registry.Send(workerID, &relayv1.CoordinatorMessage{}),
		"and the sender must still reach the registry")

	require.Empty(t, f.tx.execsSeen(),
		"fixture: no statement can have been issued, because the transaction was never opened - "+
			"that is what makes this a different arm from the exec-failure test above")
	commits, rollbacks := f.tx.outcome()
	assert.Equal(t, 0, commits,
		"nothing to commit")
	assert.Equal(t, 0, rollbacks,
		"and nothing to roll back either: pgx.BeginTxFunc returns the begin error before it defers "+
			"anything, so unlike the exec-failure arm this one leaves no transaction lifecycle at all")

	assert.Empty(t, f.db.execsSeen(),
		"the generation must not be released: this registration succeeded")

	require.Error(t, finish())
}
