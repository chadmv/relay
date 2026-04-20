package worker_test

import (
	"sync"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serializingStream records the order and concurrency of Send calls.
type serializingStream struct {
	mu          sync.Mutex
	inflight    int
	maxInflight int
	sent        []*relayv1.CoordinatorMessage
}

func (s *serializingStream) Send(msg *relayv1.CoordinatorMessage) error {
	s.mu.Lock()
	s.inflight++
	if s.inflight > s.maxInflight {
		s.maxInflight = s.inflight
	}
	s.mu.Unlock()
	// Simulate a slow write to widen the race window.
	time.Sleep(1 * time.Millisecond)
	s.mu.Lock()
	s.inflight--
	s.sent = append(s.sent, msg)
	s.mu.Unlock()
	return nil
}

func TestWorkerSender_SerializesConcurrentSends(t *testing.T) {
	stream := &serializingStream{}
	ws := worker.NewWorkerSender(stream)
	defer ws.Close()

	const goroutines = 32
	const perGoroutine = 16

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				err := ws.Send(&relayv1.CoordinatorMessage{})
				require.NoError(t, err)
			}
		}()
	}
	wg.Wait()

	// Give the send loop a moment to drain the channel.
	require.Eventually(t, func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return len(stream.sent) == goroutines*perGoroutine
	}, 5*time.Second, 10*time.Millisecond)

	// The goroutine wrapper must serialize: never more than one in-flight send.
	stream.mu.Lock()
	defer stream.mu.Unlock()
	assert.Equal(t, 1, stream.maxInflight, "workerSender failed to serialize sends")
}

func TestWorkerSender_SendAfterClose(t *testing.T) {
	stream := &serializingStream{}
	ws := worker.NewWorkerSender(stream)
	ws.Close()

	err := ws.Send(&relayv1.CoordinatorMessage{})
	assert.Error(t, err)
}
