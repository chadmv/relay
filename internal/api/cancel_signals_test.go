package api

import (
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/worker"
)

// blockingSender is a worker.Sender whose Send always blocks for the duration of
// the configured worker send timeout, simulating a wedged stream whose 64-deep
// queue is full so every Send hits ErrSendTimeout. It lets the test measure how
// the cancel-signal fan-out behaves: sequential sends take N x timeout, while a
// concurrent fan-out takes ~one timeout.
type blockingSender struct {
	d time.Duration
}

func (b blockingSender) Send(*relayv1.CoordinatorMessage) error {
	time.Sleep(b.d)
	return nil
}

// TestSendCancelSignals_FanOutIsConcurrent asserts the best-effort agent cancel
// signals are dispatched concurrently, bounding the caller to roughly one send
// timeout rather than N x it. With N senders each blocking for `block`, a
// sequential loop would take ~N*block; a concurrent fan-out takes ~block.
func TestSendCancelSignals_FanOutIsConcurrent(t *testing.T) {
	const block = 200 * time.Millisecond
	const n = 5

	reg := worker.NewRegistry()
	cancels := make([]cancelSignal, 0, n)
	for i := 0; i < n; i++ {
		// Distinct worker IDs so each Send resolves to its own blocking sender;
		// the property holds regardless, but this mirrors the worst case of one
		// wedged worker the least and keeps the registry lookups independent.
		wid := string(rune('a' + i))
		reg.Register(wid, blockingSender{d: block})
		cancels = append(cancels, cancelSignal{
			workerID: wid,
			taskID:   "task-" + wid,
			force:    false,
		})
	}

	s := &Server{registry: reg}

	start := time.Now()
	s.sendCancelSignals(cancels)
	elapsed := time.Since(start)

	// Concurrent fan-out completes in roughly one block. A sequential loop would
	// take n*block (~1s here). Bound well below the sequential time so the test
	// is RED against a sequential implementation and GREEN against a concurrent
	// one, while leaving slack for scheduling jitter.
	if elapsed >= (n-1)*block {
		t.Fatalf("sendCancelSignals took %v; expected concurrent fan-out near %v, not sequential ~%v",
			elapsed, block, n*block)
	}
}
