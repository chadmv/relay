package worker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGraceRegistry_StartFiresAfterWindow(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string) {
		if workerID == "w1" {
			fired.Add(1)
		}
	})
	defer g.Stop()

	g.Start("w1")
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(1), fired.Load())
}

func TestGraceRegistry_CancelPreventsFire(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(50*time.Millisecond, func(workerID string) {
		fired.Add(1)
	})
	defer g.Stop()

	g.Start("w1")
	time.Sleep(10 * time.Millisecond)
	g.Cancel("w1")
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load())
}

func TestGraceRegistry_StartIsIdempotent(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(40*time.Millisecond, func(workerID string) {
		fired.Add(1)
	})
	defer g.Stop()

	// Rapid re-starts: timer should reset each time and ultimately fire once.
	g.Start("w1")
	time.Sleep(15 * time.Millisecond)
	g.Start("w1")
	time.Sleep(15 * time.Millisecond)
	g.Start("w1")
	time.Sleep(90 * time.Millisecond)
	assert.Equal(t, int32(1), fired.Load())
}

func TestGraceRegistry_StopPreventsAllFires(t *testing.T) {
	var fired atomic.Int32
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string) {
		fired.Add(1)
	})

	g.Start("w1")
	g.Start("w2")
	g.Stop()
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(0), fired.Load())
}

func TestGraceRegistry_CancelNonexistentIsSafe(t *testing.T) {
	g := NewGraceRegistry(30*time.Millisecond, func(workerID string) {})
	defer g.Stop()

	// Should not panic.
	g.Cancel("never-started")
}

func TestGraceRegistry_ConcurrentStartCancelStop(t *testing.T) {
	g := NewGraceRegistry(5*time.Millisecond, func(workerID string) {})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); g.Start("w1") }()
		go func() { defer wg.Done(); g.Cancel("w1") }()
		go func() { defer wg.Done(); g.Start("w2") }()
	}
	wg.Wait()
	g.Stop()
}
