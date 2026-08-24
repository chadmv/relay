package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
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
