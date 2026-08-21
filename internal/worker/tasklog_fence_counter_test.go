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

// QueryRow reads err WITHOUT synchronisation, and that is checked rather than
// assumed: the only test that writes it does so on its own goroutine with no
// others running, and the only test with concurrent readers sets it once before
// the first goroutine starts. If a future test flips it while a goroutine is
// reading, -race says so and the fix is a mutex here, not a relaxed assertion.
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

// fenceSubscribe tails fenceTaskID's log events, and SUBSCRIBING IS LOAD-BEARING
// TWICE rather than being fixture noise.
//
// It is the only way "nothing was published" is observable at all: Publish on an
// unsubscribed broker is a map lookup that finds nothing, so an added
// h.broker.Publish on the rejection arm is invisible without a subscriber. And it
// is what defeats handleTaskLog's HasLogSubscriber short-circuit, so the SUCCESS
// leg really reaches the publish rather than returning one line above it - which
// is what makes the count below a positive proof that the chunk was ACCEPTED
// rather than an assertion that could be satisfied by any arm that does nothing.
//
// The drain is non-blocking because it can be: Publish is synchronous, under the
// broker's own mutex, and completes before handleTaskLog returns, so anything
// this call is going to see is already in the 64-slot buffer by the time it runs.
func fenceSubscribe(t *testing.T, h *Handler) func() []events.Event {
	t.Helper()
	ch, cancel := h.broker.Subscribe(events.Filter{TaskID: fenceTaskID})
	t.Cleanup(cancel)
	return func() []events.Event {
		var got []events.Event
		for {
			select {
			case e := <-ch:
				got = append(got, e)
			default:
				return got
			}
		}
	}
}

// TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot is the item's own
// Done-When: read the counter across a rejection AND across a success.
//
// EACH LEG IS ASSERTED IMMEDIATELY AFTER IT RUNS, deliberately: a battery that
// only checks the total at the end cannot tell "the success incremented" from
// "the second rejection did not", and a poisoned input observed only at the end
// cannot detect an early-exit mutation.
//
// IT ALSO CARRIES THE NO-PUBLISH PROPERTY, WHICH USED TO BE GUARDED ONLY BEHIND
// A BUILD TAG. "Gate any side effect on the fence having actually matched"
// (CLAUDE.md, epoch fence) names publishing as the consequence: a rejected chunk
// that reaches the broker puts a zombie or forged sender's output into another
// user's live SSE view, where it then vanishes on refresh because it was
// correctly never stored. The only guard was
// TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished, whose file is
// //go:build integration, and go-ci runs `go test -race ./...` with no tag -
// inserting an h.broker.Publish into the rejection arm left `go test
// ./internal/worker/...` ok (measured). The subscription below closes that.
//
// AND THE SUCCESS LEG NOW ESTABLISHES ACCEPTANCE POSITIVELY. It used to assert a
// negative - the counter did not move - through a projection that cannot see the
// claim: a run in which the third call fell into the PERSIST-FAILURE arm produces
// the same counter and the same db.calls, so the leg would have passed while
// testing the wrong arm entirely. Nothing today can make stubFenceRow.Scan fail
// with err == nil, so that was assertion strength rather than a live defect, but
// the fix is one line: with a subscriber attached, an accepted chunk publishes and
// a chunk that took any other path does not.
func TestHandleTaskLog_AFenceRejectionIsCountedAndASuccessIsNot(t *testing.T) {
	ctx := context.Background()
	h, db := newFenceHandler(pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)
	published := fenceSubscribe(t, h)

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
	require.Empty(t, published(),
		"a REJECTED chunk must never be published. It was correctly not stored, so publishing it puts a "+
			"zombie or forged sender's output into a live SSE view that vanishes on the next refresh - "+
			"the epoch fence's own named consequence. A subscriber is attached here precisely so this "+
			"assertion can fail; without one the broker drops the event on the floor and any Publish "+
			"added to that arm is invisible.")
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

	// THE CHUNK WAS ACCEPTED, and this is what says so. Without it the leg above
	// asserts a negative through a projection shared by every other arm: a call
	// that fell into the persist-failure arm leaves the counter at 2 and db.calls
	// at 3 too.
	accepted := published()
	require.Len(t, accepted, 1,
		"an accepted chunk must be published exactly once, which is what proves this leg exercised the "+
			"SUCCESS arm rather than some other arm that also leaves the rejection counter alone")
	require.Equal(t, events.TypeTaskLog, accepted[0].Type)
	require.Equal(t, fenceTaskID, accepted[0].TaskID)
	assert.Equal(t, "", logged(),
		"the success arm logs nothing either. Re-checked AFTER the success leg on purpose: the earlier "+
			"check cannot see a line this call emitted, and a persist-failure fall-through would leave "+
			"one here.")

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
// MEASURED, NOT ASSUMED, AND THE TWO HALVES ARE NOT EQUALLY STRONG. The
// mutation this test exists for is taskLogFenceRejects changed to a plain uint64
// with `++`, WITH the .Load() in TaskLogFenceRejections dropped to match - leave
// the .Load() in and the "kill" is a compile error, which measures nothing.
// Measured in a golang:1.26 Linux container, CPU-pinned with docker --cpus /
// --cpuset-cpus so the -cpu figures mean something:
//
//   - the -race half kills 10/10 at -cpu=1 AND 10/10 at -cpu=2. It does NOT need
//     true parallelism: TSan's vector clocks see two sibling goroutines writing
//     one word with no happens-before edge between them. Do not dismiss this
//     test on a single-core runner - that half is at full strength there.
//   - the EXACTNESS half is the weak one: 0/20 at -cpu=1, 17/20 at -cpu=2. At one
//     CPU it is inert, and the require.Equal below proves nothing on its own.
//
// Unmutated green baseline, same container: 0/10 races at -cpu=1, 0/20 exactness
// failures at -cpu=1 and 0/20 at -cpu=2. CI is ubuntu-latest running
// `go test -race ./...` with no -cpu flag on 2-4 vCPUs, so both halves are live
// there.
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
