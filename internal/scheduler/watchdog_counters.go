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
