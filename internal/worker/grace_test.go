package worker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraceRegistry_StartPropagatesEpochToOnExpire(t *testing.T) {
	var gotEpoch atomic.Int32
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string, epoch int32) {
		if workerID == "w1" {
			gotEpoch.Store(epoch)
			fired.Add(1)
		}
	})
	defer g.Stop()

	g.Start("w1", 7)
	require.Eventually(t, func() bool {
		return fired.Load() == 1
	}, 200*time.Millisecond, 5*time.Millisecond)
	assert.Equal(t, int32(7), gotEpoch.Load(), "onExpire must receive the epoch Start was called with")
}

func TestGraceRegistry_StartFiresAfterWindow(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string, epoch int32) {
		if workerID == "w1" {
			fired.Add(1)
		}
	})
	defer g.Stop()

	g.Start("w1", 1)
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(1), fired.Load())
}

func TestGraceRegistry_CancelPreventsFire(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(50*time.Millisecond, func(workerID string, epoch int32) {
		fired.Add(1)
	})
	defer g.Stop()

	g.Start("w1", 1)
	time.Sleep(10 * time.Millisecond)
	g.Cancel("w1")
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load())
}

func TestGraceRegistry_StartIsIdempotent(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(40*time.Millisecond, func(workerID string, epoch int32) {
		fired.Add(1)
	})
	defer g.Stop()

	// Rapid re-starts: timer should reset each time and ultimately fire once.
	g.Start("w1", 1)
	time.Sleep(15 * time.Millisecond)
	g.Start("w1", 1)
	time.Sleep(15 * time.Millisecond)
	g.Start("w1", 1)
	time.Sleep(90 * time.Millisecond)
	assert.Equal(t, int32(1), fired.Load())
}

func TestGraceRegistry_StopPreventsAllFires(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string, epoch int32) {
		fired.Add(1)
	})

	g.Start("w1", 1)
	g.Start("w2", 1)
	g.Stop()
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load())
}

func TestGraceRegistry_CancelNonexistentIsSafe(t *testing.T) {
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string, epoch int32) {})
	defer g.Stop()

	// Should not panic.
	g.Cancel("never-started")
}

func TestGraceRegistry_ConcurrentStartCancelStop(t *testing.T) {
	g := NewGraceRegistry(5*time.Millisecond, func(workerID string, epoch int32) {})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); g.Start("w1", 1) }()
		go func() { defer wg.Done(); g.Cancel("w1") }()
		go func() { defer wg.Done(); g.Start("w2", 1) }()
	}
	wg.Wait()
	g.Stop()
}

func TestGraceRegistry_StartWithDurationFiresAfterCustomWindow(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(1*time.Second, func(workerID string, epoch int32) {
		if workerID == "w-custom" {
			fired.Add(1)
		}
	})

	g.StartWithDuration("w-custom", 1, 30*time.Millisecond)

	require.Eventually(t, func() bool {
		return fired.Load() == 1
	}, 200*time.Millisecond, 5*time.Millisecond)
}

func TestGraceRegistry_ExpireNowFiresSynchronously(t *testing.T) {
	var fired atomic.Int32
	var firedID string
	g := NewGraceRegistry(1*time.Hour, func(workerID string, epoch int32) {
		firedID = workerID
		fired.Add(1)
	})

	g.ExpireNow("w-expired", 1)

	require.Equal(t, int32(1), fired.Load(), "ExpireNow must invoke onExpire synchronously")
	require.Equal(t, "w-expired", firedID)
}

func TestGraceRegistry_ExpireNowAfterStopIsNoOp(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(1*time.Hour, func(string, int32) { fired.Add(1) })
	g.Stop()
	g.ExpireNow("w-late", 1)
	require.Equal(t, int32(0), fired.Load(), "ExpireNow must not fire after Stop")
}

func TestGraceRegistry_ExpireNowReplacesPendingTimer(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(1*time.Hour, func(string, int32) { fired.Add(1) })
	g.Start("w-x", 1)
	g.ExpireNow("w-x", 1)
	require.Equal(t, int32(1), fired.Load())
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(1), fired.Load())
}

// TestGraceRegistry_AStaleEpochDoesNotDisplaceALiveTimer pins the registry as
// epoch-MONOTONIC, which is a property releaseWorkerGeneration's caller cannot
// supply for it.
//
// The scenario is a delayed superseded release. finishRegister's deferred
// release checks the fence inside markWorkerOffline and then calls grace.Start
// on the result, and those two steps are a DATABASE ROUND TRIP apart with no
// lock held across them - so a fresher connection can register, disconnect and
// arm its own timer in between. Without this rule the stale caller's Start
// evicts the LIVE generation's entry and installs its own, whose epoch fence
// then matches zero rows when it fires: that worker's tasks are never requeued
// at all and sit until the 24h stale-task watchdog marks them timed_out.
//
// EQUAL EPOCHS MUST STILL REPLACE, or TestGraceRegistry_StartIsIdempotent's
// reset-the-window behaviour is lost.
func TestGraceRegistry_AStaleEpochDoesNotDisplaceALiveTimer(t *testing.T) {
	fired := make(chan int32, 4)
	g := NewGraceRegistry(40*time.Millisecond, func(workerID string, epoch int32) {
		fired <- epoch
	})
	defer g.Stop()

	g.Start("w1", 8)
	g.Start("w1", 7) // a superseded release, arriving late

	select {
	case epoch := <-fired:
		assert.Equal(t, int32(8), epoch,
			"the live generation's timer must survive a stale one. Firing at 7 means the entry for "+
				"8 was evicted, and RequeueWorkerTasksIfEpoch will match zero rows at 7 - so the "+
				"live worker's tasks are requeued by nobody.")
	case <-time.After(2 * time.Second):
		t.Fatal("no timer fired at all; the stale Start must leave the existing entry running")
	}
}

// TestGraceRegistry_StartAtTheSameEpochStillResetsTheWindow is the other half of
// the monotonicity rule: only a STRICTLY older epoch is refused.
func TestGraceRegistry_StartAtTheSameEpochStillResetsTheWindow(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(40*time.Millisecond, func(workerID string, epoch int32) {
		fired.Add(1)
	})
	defer g.Stop()

	g.Start("w1", 3)
	time.Sleep(25 * time.Millisecond)
	g.Start("w1", 3)
	time.Sleep(25 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load(),
		"the second Start at the same epoch must have reset the 40ms window; if it were refused "+
			"like a stale one, the first timer would have fired by now")
}
