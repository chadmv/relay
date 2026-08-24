package worker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	queries  []strandExec
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

// Query records what it was asked as well as refusing it. RequeueWorkerTasksIfEpoch
// is a :many, so the else arm of releaseWorkerGeneration lands here rather than
// in Exec, and the statement it issues is the only evidence that arm ran.
func (d *strandDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queries = append(d.queries, strandExec{sql: sql, args: args})
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

func (d *strandDB) queriesSeen() []strandExec {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]strandExec, len(d.queries))
	copy(out, d.queries)
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

// strandInt32Base is the value the FIRST int32 column of the stub row scans as;
// each subsequent one gets the next integer, so every int32 column of
// store.Worker comes back DISTINCT.
//
// IT USED TO BE ONE CONSTANT FOR ALL OF THEM, and that made the epoch
// assertions below vacuous rather than merely loose. store.Worker has several
// int32 columns, so a single fill meant `releaseWorkerGeneration(workerID,
// updated.MaxSlots)` produced byte-identical behaviour to the real
// `updated.ConnectionEpoch` and the mutation survived the whole lane. What is
// under test on this path is not "some epoch was used" but "the epoch THIS
// registration created was used", and only distinct values can tell those apart.
const strandInt32Base int32 = 101

// strandEpoch is what connection_epoch - the one int32 column finishRegister
// reads - scans as.
//
// IT IS DERIVED, NOT WRITTEN DOWN, because the fill is positional and the
// position is sqlc's to choose. sqlc emits row.Scan(&i.F1, &i.F2, ...) in
// store.Worker's own field order, so counting int32 fields up to
// ConnectionEpoch reproduces exactly what Scan will hand it - and a column added
// or reordered in the migration moves both together instead of silently
// re-pointing this constant at max_slots.
var strandEpoch = strandInt32ColumnValue("ConnectionEpoch")

// strandInt32ColumnValue returns the value strandWorkerRow.Scan gives the named
// int32 field of store.Worker. It panics rather than returning an error: it runs
// at package-var init, and a miss means the fixture no longer models the struct
// it is standing in for.
func strandInt32ColumnValue(field string) int32 {
	rt := reflect.TypeOf(store.Worker{})
	var seen int32
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Type.Kind() != reflect.Int32 {
			continue
		}
		if f.Name == field {
			return strandInt32Base + seen
		}
		seen++
	}
	panic("strandInt32ColumnValue: store.Worker has no int32 field named " + field)
}

type strandWorkerRow struct{}

func (strandWorkerRow) Scan(dest ...any) error {
	var int32s int32
	for _, d := range dest {
		switch v := d.(type) {
		case *pgtype.UUID:
			*v = strandWorkerID
		case *int32:
			*v = strandInt32Base + int32s
			int32s++
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
		default:
			// A HAND-WRITTEN MAPPER NEEDS AN ARITY CHECK. Without this arm an
			// unhandled destination type is left at its zero value and the scan
			// reports success, so a column whose Go type changes - or a new column of
			// a type this switch has never seen - silently feeds every test in this
			// file a zero where it expects a fixture value. That failure mode is
			// invisible: nothing errors, the assertions just start proving less.
			return fmt.Errorf("strandWorkerRow: no fixture value for scan destination of type %T; "+
				"store.Worker gained a column this stub does not model", d)
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
// 'online', bumped connection_epoch past the value the previous generation held
// and cleared disconnected_at, and grace.Cancel has thrown away the requeue
// timer the PREVIOUS disconnect armed. At HEAD nothing happens next: Connect's `defer h.teardownConnection` is armed
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
			"acquired. Nothing else will: the worker row is status='online' at the epoch this "+
			"registration created, with no connection behind it, the previous disconnect's requeue "+
			"timer destroyed by grace.Cancel, and the liveness sweeper walking past it because "+
			"Metrics.Activate is never reached.")
	assert.Contains(t, execs[0].sql, "status = 'offline'",
		"the one statement must be MarkWorkerOfflineIfEpoch")
	assert.Contains(t, execs[0].sql, "connection_epoch = $4",
		"and it must carry its epoch fence")
	assert.Contains(t, execs[0].args, strandEpoch,
		"it must be fenced on the epoch THIS registration created. That fence is the WHOLE of "+
			"the ownership check on this path: a failed registration has no sender in the registry "+
			"to compare against, so there is no second gate behind it.")

	select {
	case f := <-fired:
		assert.Equal(t, uuidStr(strandWorkerID), f.workerID)
		assert.Equal(t, strandEpoch, f.epoch,
			"the timer must be armed at the epoch this registration created. Re-arming at the "+
				"PREVIOUS epoch - trying to restore the timer grace.Cancel discarded - is a silent "+
				"no-op: RequeueWorkerTasksIfEpoch fences on workers.connection_epoch and the row has "+
				"already moved past it.")
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
// acquired an epoch and our reconcile then failed, but by the time our deferred
// release runs a fresher connection has registered and the row has moved past
// it. Postgres answers that with zero rows affected - which is what "UPDATE 0"
// below stands for - and the release must stop there. Arming a grace timer anyway would leave
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

	offline, unsubscribe := h.broker.Subscribe(events.Filter{})
	defer unsubscribe()

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

	// The other half of "releases nothing", and the half no test held: the two
	// UNFENCED side effects inside markWorkerOffline. Deleting its `rows == 0`
	// early return leaves everything above this green, so without this assertion
	// a superseded release publishes offline and wipes the metrics ring of a
	// worker a FRESHER connection owns.
	select {
	case ev := <-offline:
		t.Fatalf("a superseded release published a worker event: %s. Zero rows means a fresher "+
			"connection holds this worker's epoch, so the broker publish and Metrics.Clear that sit "+
			"below the row count are acting on a LIVE generation - the UI shows a connected agent as "+
			"offline and its metrics ring is cleared out from under it.", ev.Data)
	case <-time.After(100 * time.Millisecond):
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

// TestStrandFixture_EveryInt32ColumnScansDistinct pins the property the two
// epoch assertions in this file rest on, in the one place a future edit to
// strandWorkerRow.Scan would break it.
//
// Without it, collapsing the fill back to a single constant is a green change
// that quietly turns `assert.Equal(t, strandEpoch, f.epoch)` into an assertion
// about nothing: with every int32 column carrying the same value, passing
// max_slots, cpu_cores, ram_gb or gpu_count where connection_epoch belongs is
// indistinguishable from correct.
//
// HOW MANY THERE ARE IS DELIBERATELY NOT STATED, here or at strandInt32Base.
// What this test asserts is DISTINCTNESS, not arity - it walks whatever the
// scan produced - so a column added to store.Worker would falsify a number
// written in this comment without failing anything that would catch it.
func TestStrandFixture_EveryInt32ColumnScansDistinct(t *testing.T) {
	var w store.Worker
	require.NoError(t, strandWorkerRow{}.Scan(
		&w.ID, &w.Name, &w.Hostname, &w.CpuCores, &w.RamGb, &w.GpuCount, &w.GpuModel,
		&w.Os, &w.MaxSlots, &w.Labels, &w.Status, &w.LastSeenAt, &w.CreatedAt,
		&w.AgentTokenHash, &w.DisconnectedAt, &w.DisabledAt, &w.RevokedAt,
		&w.ConnectionEpoch, &w.SupportsWorkspaces,
	), "the destination list mirrors store.Worker; a scan error here means the stub no longer "+
		"models the struct sqlc generates")

	seen := map[int32]string{}
	for name, got := range map[string]int32{
		"CpuCores":        w.CpuCores,
		"RamGb":           w.RamGb,
		"GpuCount":        w.GpuCount,
		"MaxSlots":        w.MaxSlots,
		"ConnectionEpoch": w.ConnectionEpoch,
	} {
		if prev, dup := seen[got]; dup {
			t.Fatalf("%s and %s both scan as %d. Every int32 column must be distinct, or a release "+
				"fenced on the WRONG column is indistinguishable from one fenced on "+
				"connection_epoch.", prev, name, got)
		}
		seen[got] = name
	}

	assert.Equal(t, strandEpoch, w.ConnectionEpoch,
		"strandEpoch is derived by counting int32 fields of store.Worker; if it no longer matches "+
			"what a positional Scan produces, the derivation and sqlc's column order have diverged")
}

// TestConnect_AFailedRegistrationReplacesThePreviousDisconnectsTimer is the
// story both tests above narrate in their headers and neither exercised: the
// worker already had a pending requeue from an EARLIER disconnect, this
// registration threw it away, and the release has to put a fresh one in its
// place at the epoch RegisterWorkerConnection just created.
//
// The pre-armed timer is given an hour so it cannot fire on its own during the
// test - anything that fires here fires because the release armed it, and it
// fires at the NEW epoch or the assertion catches it. Re-arming at the previous
// epoch would be a silent no-op: RequeueWorkerTasksIfEpoch fences on
// workers.connection_epoch and the row has moved on.
//
// DELETING grace.Cancel DOES NOT FAIL THIS TEST, and that is a fact about
// grace.Cancel rather than a gap here. Two independent things make a surviving
// stale entry harmless: RequeueWorkerTasksIfEpoch fences the stale fire to zero
// rows, and GraceRegistry is epoch-monotonic in the direction that matters here
// - the incoming epoch is strictly newer, so Start replaces the old entry
// whether or not Cancel already removed it. Cancel buys promptness and a tidy
// map, not correctness, and no test in this package should be written to claim
// otherwise.
func TestConnect_AFailedRegistrationReplacesThePreviousDisconnectsTimer(t *testing.T) {
	h, _, fired := newStrandHandler(t, errors.New("connection reset by peer"), "UPDATE 1")

	previousEpoch := strandEpoch - 1
	h.grace.StartWithDuration(uuidStr(strandWorkerID), previousEpoch, time.Hour)

	require.Error(t, h.Connect(strandStream(t)))

	select {
	case f := <-fired:
		assert.Equal(t, strandEpoch, f.epoch,
			"the pending requeue from the previous disconnect must not be what fires. It is fenced "+
				"on the epoch that was live when THAT connection ended, and RegisterWorkerConnection "+
				"has moved the row past it - so a fire at the old epoch requeues nothing and the "+
				"tasks this registration stranded stay stranded.")
	case <-time.After(3 * time.Second):
		t.Fatal("no timer fired within the window. The registration discarded the previous " +
			"disconnect's pending requeue and then failed, so unless the release armed a fresh one " +
			"these tasks have no requeue scheduled at all.")
	}

	select {
	case f := <-fired:
		t.Fatalf("a second timer fired, at epoch %d. One ended generation must schedule one requeue; "+
			"two means an entry survived that should have been replaced.", f.epoch)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestReleaseWorkerGeneration_WithoutAGraceRegistryRequeuesImmediately covers
// the else arm, which no test reached: deleting it outright left the whole
// default package green.
//
// IT IS NOT REACHABLE IN PRODUCTION TODAY - cmd/relay-server/main.go always
// builds the handler through NewHandlerWithGrace - and it is covered anyway
// rather than deleted, because it is the arm that decides what happens when the
// grace window is configured away, and an untested branch that ends a worker's
// generation is exactly the kind that comes back wrong. This calls
// releaseWorkerGeneration directly: the arm is one level below the registration
// path, and driving it through Connect would only re-test the level above.
func TestReleaseWorkerGeneration_WithoutAGraceRegistryRequeuesImmediately(t *testing.T) {
	// queryErr is set because the requeue is a :many and this stub returns no
	// pgx.Rows. The handler discards that statement's result either way
	// (`_, _ = h.q.RequeueWorkerTasksIfEpoch(...)`), so refusing it changes
	// nothing about what this test observes: that the statement was issued at all,
	// and with which fence.
	db := &strandDB{execTag: "UPDATE 1", queryErr: errors.New("no rows fixture")}
	h := &Handler{
		q:               store.New(db),
		registry:        NewRegistry(),
		broker:          events.NewBroker(),
		triggerDispatch: func() {},
	}
	require.Nil(t, h.grace, "fixture: this test is about the arm taken when there is no grace registry")

	h.releaseWorkerGeneration(uuidStr(strandWorkerID), strandEpoch)

	execs := db.execsSeen()
	require.Len(t, execs, 1, "the offline mark")
	assert.Contains(t, execs[0].sql, "status = 'offline'")

	queries := db.queriesSeen()
	require.Len(t, queries, 1,
		"with no grace registry the release must requeue the worker's tasks itself. Marking the "+
			"worker offline and stopping there leaves its dispatched and running tasks assigned to a "+
			"connection that no longer exists, with nothing scheduled to free them.")
	assert.Contains(t, queries[0].sql, "connection_epoch = $2",
		"the requeue must carry the connection_epoch fence")
	assert.Contains(t, queries[0].args, strandEpoch,
		"and it must be fenced on the epoch whose generation is being ended")
}
