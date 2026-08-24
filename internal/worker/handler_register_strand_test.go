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
// reconcileRunningTasks' error four lines ABOVE the applyInventory call that
// opens a transaction on the concrete *pgxpool.Pool - unconditionally, even for
// an empty inventory. A nil pool is therefore a complete fixture for THIS arm
// and would panic one statement later. That is also why the RegisterResponse-send
// arm cannot live in this lane; its proof is
// TestRegisterWorker_SendFailureReleasesTheGeneration, //go:build integration.
type strandDB struct {
	mu       sync.Mutex
	queryErr error // returned by Query, i.e. by GetActiveTasksForWorker
	execErr  error // returned by Exec, i.e. by MarkWorkerOfflineIfEpoch
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
	if d.execErr != nil {
		return pgconn.CommandTag{}, d.execErr
	}
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
// only AFTER finishRegister returns and needs the sender only the success path
// creates, so on this path it is never armed at all.
//
// NOTHING ELSE CATCHES IT, which is what makes this worth a test rather than a
// comment. The metrics liveness sweeper (internal/metrics/sweep.go) is the one
// runtime mechanism that flips a connected-looking worker to 'stale', and it
// skips any worker LastSampleAt reports as untracked - which this one is,
// because Metrics.Activate sits BELOW the failure, and the previous disconnect's
// markWorkerOffline already called Metrics.Clear. The stale-task watchdog is not
// a substitute either: it marks tasks timed_out at RELAY_TASK_MAX_ASSIGNMENT
// (24h) rather than requeueing them, and it never writes workers.status at all.
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

// TestConnect_AFailedRegistrationStillReleasesWhenTheOfflineWriteERRORS is the
// correlated-failure case, and it is why markWorkerOffline reports "I could not
// tell" separately from "the fence said no".
//
// THE TWO FAILURES ARE CORRELATED, which is what makes this sharp rather than
// theoretical. The reconcile arm this fixture drives fails for exactly two
// reasons - a cancelled peer context, or a database fault - and in the second
// case the release's OWN write goes to the same unhealthy pool. Reading a query
// error as "a fresher connection holds the epoch" therefore re-creates the exact
// strand this whole change exists to close, silently, in one of its two trigger
// scenarios.
//
// PROCEEDING IS SAFE BECAUSE BOTH CONTINUATIONS CARRY THEIR OWN FENCE.
// grace.Start's expiry runs RequeueWorkerTasksIfEpoch, which has its own
// workers.connection_epoch EXISTS guard, and requeueWorkerTasks calls that
// statement directly. If we really had been superseded, the worst case is a
// fenced no-op; today's worst case is a permanent strand.
func TestConnect_AFailedRegistrationStillReleasesWhenTheOfflineWriteERRORS(t *testing.T) {
	h, db, fired := newStrandHandler(t, errors.New("connection reset by peer"), "UPDATE 1")
	db.execErr = errors.New("failed to connect to `host=localhost`: dial error")

	offline, unsubscribe := h.broker.Subscribe(events.Filter{})
	defer unsubscribe()

	err := h.Connect(strandStream(t))
	require.Error(t, err)
	require.Contains(t, err.Error(), "reconcile", "fixture: same arm as the strand test")

	require.Len(t, db.execsSeen(), 1, "the release must still be attempted")

	select {
	case f := <-fired:
		assert.Equal(t, uuidStr(strandWorkerID), f.workerID)
		assert.Equal(t, strandEpoch, f.epoch,
			"the timer must be armed at the epoch THIS registration created")
	case <-time.After(3 * time.Second):
		t.Fatal("the offline write ERRORED and the release gave up. A query error is not evidence " +
			"that a fresher connection holds the epoch - it is evidence of nothing, and it is the " +
			"correlated symptom of the very database fault that failed the registration. grace.Start " +
			"is independently fenced, so proceeding costs at worst a no-op; stopping costs a worker " +
			"stranded 'online' with its running tasks requeued by nobody.")
	}

	select {
	case ev := <-offline:
		t.Fatalf("an offline worker event was published even though the offline write never landed: "+
			"%s. The broker publish and Metrics.Clear are UNFENCED side effects - they must stay "+
			"gated on the write actually having applied, or a UI shows a worker offline that Postgres "+
			"still calls online.", ev.Data)
	case <-time.After(100 * time.Millisecond):
	}
}
