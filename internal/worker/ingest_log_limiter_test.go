package worker

import (
	"strings"
	"testing"
	"time"
)

// The integration tests in package worker_test cannot see these constants, so
// they assert literals. This test is the single place the literals and the
// constants are tied together: if you tune a constant, this test tells you which
// literals to move.
//   - ingestLogBurst 16  -> handler_ingest_budget_integration_test.go uses 20 as
//     its upper bound (burst plus headroom for a slow container).
//   - ingestLogIDClip 64 -> the same file asserts a clipped id.
func TestIngestLogLimiter_ConstantsAreWhatTheHandlerTestsAssume(t *testing.T) {
	if ingestLogSeenMax != 128 {
		t.Errorf("ingestLogSeenMax = %d, want 128", ingestLogSeenMax)
	}
	if ingestLogBurst != 16 {
		t.Errorf("ingestLogBurst = %d, want 16", ingestLogBurst)
	}
	if ingestLogRefill != 10*time.Second {
		t.Errorf("ingestLogRefill = %v, want 10s", ingestLogRefill)
	}
	if ingestLogIDClip != 64 {
		t.Errorf("ingestLogIDClip = %d, want 64", ingestLogIDClip)
	}
}

// newFrozen returns a limiter whose clock never advances unless the test moves
// it, so every count below is exact rather than wall-clock dependent. Both `now`
// and `last` must be reset: newIngestLogLimiter stamps last from time.Now.
func newFrozen() (*ingestLogLimiter, *time.Time) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	clock := &now
	l := newIngestLogLimiter()
	l.now = func() time.Time { return *clock }
	l.last = *clock
	return l, clock
}

func key(n int) logKey { return logKey{kind: kindTaskLogPersist, id: "t", epoch: int64(n)} }

// THE BOUND. Distinct keys are limited by tokens, not by the map.
func TestIngestLogLimiter_DistinctKeysAreCappedAtBurst(t *testing.T) {
	l, _ := newFrozen()
	allowed := 0
	for i := 0; i < 1000; i++ {
		if l.allow(key(i)) {
			allowed++
		}
	}
	if allowed != ingestLogBurst {
		t.Errorf("allowed = %d, want %d - the token bucket is the bound", allowed, ingestLogBurst)
	}
}

// THE DIAGNOSTIC. Distinct task ids at the same epoch are distinct keys: a real
// multi-task persist failure must not collapse to one line. This is the only
// test that pins the `id` component of the persist key; the existing
// PersistFailure integration test pins the `epoch` component.
func TestIngestLogLimiter_TaskIdIsPartOfThePersistKey(t *testing.T) {
	l, _ := newFrozen()
	a := l.allow(logKey{kind: kindTaskLogPersist, id: "task-a", epoch: 1})
	b := l.allow(logKey{kind: kindTaskLogPersist, id: "task-b", epoch: 1})
	again := l.allow(logKey{kind: kindTaskLogPersist, id: "task-a", epoch: 1})
	if !a || !b {
		t.Errorf("two different tasks failing at the same epoch must both log: a=%v b=%v", a, b)
	}
	if again {
		t.Error("the same task+epoch must be deduplicated")
	}
}

// DEDUPE HAPPENS BEFORE THE SPEND. An honest repeating failure costs exactly one
// token no matter how many chunks it produces. Spend-before-dedupe would pass
// every flood test while burning a token per repeated chunk.
func TestIngestLogLimiter_DedupeHappensBeforeTheSpend(t *testing.T) {
	l, _ := newFrozen()
	k := logKey{kind: kindTaskLogPersist, id: "hot", epoch: 1}
	for i := 0; i < 500; i++ {
		l.allow(k)
	}
	if l.tokens != ingestLogBurst-1 {
		t.Fatalf("tokens = %d, want %d - 500 repeats of one key must spend exactly one token",
			l.tokens, ingestLogBurst-1)
	}
	fresh := 0
	for i := 0; i < ingestLogBurst-1; i++ {
		if l.allow(key(i)) {
			fresh++
		}
	}
	if fresh != ingestLogBurst-1 {
		t.Errorf("fresh keys allowed = %d, want %d - the repeats consumed budget they should not have",
			fresh, ingestLogBurst-1)
	}
}

// A KEY SUPPRESSED FOR LACK OF A TOKEN IS NOT RECORDED, so the diagnostic
// reappears when tokens refill instead of being swallowed for the connection's
// whole lifetime.
func TestIngestLogLimiter_ASuppressedKeyIsNotRecorded(t *testing.T) {
	l, clock := newFrozen()
	for i := 0; i < ingestLogBurst; i++ {
		l.allow(key(i))
	}
	victim := logKey{kind: kindTaskLogPersist, id: "victim", epoch: 9}
	if l.allow(victim) {
		t.Fatal("fixture: the bucket must be empty here")
	}
	if _, recorded := l.seen[victim]; recorded {
		t.Fatal("a key suppressed for lack of a token must NOT be recorded in seen")
	}
	*clock = clock.Add(ingestLogRefill)
	if !l.allow(victim) {
		t.Error("after a refill the suppressed key must log, not dedupe-drop")
	}
}

// REFILL ADVANCES BY WHOLE CONSUMED INTERVALS AND CAPS AT BURST.
func TestIngestLogLimiter_RefillsWholeIntervalsAndCapsAtBurst(t *testing.T) {
	l, clock := newFrozen()
	for i := 0; i < ingestLogBurst; i++ {
		l.allow(key(i))
	}
	*clock = clock.Add(3 * ingestLogRefill)
	got := 0
	for i := 100; i < 200; i++ {
		if l.allow(key(i)) {
			got++
		}
	}
	if got != 3 {
		t.Errorf("after 3 refill intervals, allowed = %d, want 3", got)
	}

	*clock = clock.Add(1000 * ingestLogRefill)
	got = 0
	for i := 1000; i < 2000; i++ {
		if l.allow(key(i)) {
			got++
		}
	}
	if got != ingestLogBurst {
		t.Errorf("after 1000 refill intervals, allowed = %d, want %d (capped at burst)", got, ingestLogBurst)
	}
}

// THE REFILL MUST NOT STALL UNDER CALLS MORE FREQUENT THAN THE INTERVAL. This is
// the test that catches `l.last = l.now()` on a call that added zero tokens: a
// connection calling more often than the refill interval would then never refill
// at all. 20000 calls at 1ms apart is 20s, exactly two intervals.
func TestIngestLogLimiter_RefillDoesNotStallUnderCallsMoreFrequentThanTheInterval(t *testing.T) {
	l, clock := newFrozen()
	for i := 0; i < ingestLogBurst; i++ {
		l.allow(key(i))
	}
	got := 0
	for i := 0; i < 20000; i++ {
		*clock = clock.Add(time.Millisecond)
		if l.allow(key(1000 + i)) {
			got++
		}
	}
	if got != 2 {
		t.Errorf("allowed = %d over 20s of 1ms-spaced calls, want 2 (one per refill interval)", got)
	}
}

// THE DEDUPE MAP IS BOUNDED, and clearing it at capacity is safe ONLY because
// the bucket is the bound. Re-arming every key can at worst produce another
// burst; permanent suppression would have no time-based recovery.
func TestIngestLogLimiter_SeenMapIsBoundedAndClearsAtCapacity(t *testing.T) {
	l, clock := newFrozen()
	maxSeen := 0
	for i := 0; i < 4*ingestLogSeenMax; i++ {
		*clock = clock.Add(ingestLogRefill)
		l.allow(key(i))
		if len(l.seen) > maxSeen {
			maxSeen = len(l.seen)
		}
	}
	if maxSeen > ingestLogSeenMax {
		t.Errorf("len(seen) reached %d, want at most %d", maxSeen, ingestLogSeenMax)
	}
	if maxSeen < ingestLogSeenMax/2 {
		t.Errorf("len(seen) only reached %d - the fixture never approached capacity, so this test proves nothing", maxSeen)
	}
}

// A NIL LIMITER DROPS THE LINE INSTEAD OF PANICKING. Connect has no recover and
// grpc-go does not recover handler panics, so a nil dereference on the recv
// goroutine would take down the whole server process. Fail closed on volume.
func TestIngestLogLimiter_NilLimiterDropsTheLineInsteadOfPanicking(t *testing.T) {
	var l *ingestLogLimiter
	if l.allow(logKey{kind: kindBadTaskID}) {
		t.Error("a nil limiter must drop the line, not allow it")
	}
}

func TestClipID_BoundsTheLoggedIdentifier(t *testing.T) {
	short := "00000000-0000-0000-0000-000000000001"
	if got := clipID(short); got != short {
		t.Errorf("clipID(%q) = %q, want it unchanged", short, got)
	}
	long := strings.Repeat("A", 100000)
	got := clipID(long)
	if len(got) > ingestLogIDClip+32 {
		t.Errorf("clipID returned %d bytes, want at most %d", len(got), ingestLogIDClip+32)
	}
	if !strings.HasPrefix(got, strings.Repeat("A", ingestLogIDClip)) {
		t.Error("clipID must keep the leading bytes so the operator sees what was sent")
	}
}
