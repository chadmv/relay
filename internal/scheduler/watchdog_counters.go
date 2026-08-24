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
// the WHOLE map on one non-uuid key. Where that costs a RED is
// cmd/relay-server's
// TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap, the
// only place keys THIS function produced are read back out through the real
// route; internal/api's own guard cannot see this function at all, since it
// drives a fake source whose keys are literals in its test file.
// ListOverdueAssignedTasks requires worker_id IS NOT NULL, so this branch is
// unreachable today - and it is what lets the payload guard be written as a
// shape check rather than as a promise.
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
// aggregate sweep line. It returns ("", 0, overflow) when nothing has been
// swept.
//
// IT RETURNS SweptOverflow AS A THIRD VALUE, and that is the point rather than
// a convenience. TRACKED is load-bearing: at capacity a never-before-seen
// worker's sweeps go to SweptOverflow however many there are, so the worst
// TRACKED worker may be an innocent machine with 1 while the real offender is
// unattributed. A caller that could not see the overflow had no way to know
// that, and the caller cannot read it separately either - two calls are two
// critical sections, and the pair could then disagree. Both come out of the ONE
// lock acquisition below, and SweepOnce qualifies its line when the third value
// is non-zero.
func (w *watchdogCounters) worst() (string, uint64, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var id string
	var n uint64
	for k, v := range w.c.SweptByWorker {
		// Ties broken by the lexically smaller id, so the line is deterministic
		// across map iteration order rather than flapping between equals.
		// TestWatchdogCounters_WorstBreaksTiesDeterministically is what keeps
		// the second disjunct alive: dropping it survived -count=3 until that
		// test existed.
		if v > n || (v == n && id != "" && k < id) {
			id, n = k, v
		}
	}
	return id, n, w.c.SweptOverflow
}

// canonicalWorkerKey reports whether s is the lowercase 8-4-4-4-12 form uuidStr
// emits. Anchored and hex-only, so nothing that is not a server-rendered uuid
// can become a key in a document an operator reads.
//
// IT HAS RESTATEMENTS IT CANNOT SHARE, AND THIS SENTENCE MUST NOT COUNT THEM.
// It read "IT HAS A TWIN" when there was one; a third copy landed two commits
// later and nobody came back here. That is the uniqueness-claim shape CLAUDE.md
// warns about - a claim about the COMPLEMENT, which cannot be checked by opening
// its subject - so what is stated instead is the PROPERTY: every restatement is
// this same rule spelled as an anchored regexp, and every one of them lives in a
// test file. At the time of writing they are internal/api's canonicalUUIDRe
// (server_counters_test.go), which is what the payload guard checks these keys
// against, and cmd/relay-server's canonicalWorkerKeyRe
// (counters_wiring_test.go), which restates it AGAIN because internal/api's is
// unexported. Read the current set off the code, not off that list:
//
//	grep -rn 'canonicalWorkerKey\|canonicalUUIDRe' --include=*.go .
//
// Neither package can import the other (internal/scheduler imports internal/api,
// which is also why WatchdogSweptWorkerMax had to be hoisted over there), so the
// constant is shared and this predicate is not. Change this one and change all
// of them: a producer stricter than the guard is harmless, a producer looser
// than it ships a key the guard will reject.
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
