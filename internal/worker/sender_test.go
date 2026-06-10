package worker_test

import (
	"errors"
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

// wedgedStream simulates an agent whose stream blocks: the send goroutine parks
// inside Send until release is closed, then Send returns an error (mimicking the
// transport dying). entered is closed the first time Send is reached.
type wedgedStream struct {
	entered   chan struct{}
	release   chan struct{}
	onceEnter sync.Once
}

func (s *wedgedStream) Send(*relayv1.CoordinatorMessage) error {
	s.onceEnter.Do(func() { close(s.entered) })
	<-s.release
	return errors.New("stream closed")
}

func newWedgedStream() *wedgedStream {
	return &wedgedStream{entered: make(chan struct{}), release: make(chan struct{})}
}

// fillBuffer parks the send goroutine inside Send and fills the 64-slot buffer,
// so the next Send call has nowhere to go.
func fillBuffer(t *testing.T, ws worker.Sender, stream *wedgedStream) {
	t.Helper()
	require.NoError(t, ws.Send(&relayv1.CoordinatorMessage{})) // pulled by loop, parks in Send
	<-stream.entered                                           // loop goroutine now blocked inside Send
	for i := 0; i < 64; i++ {
		require.NoError(t, ws.Send(&relayv1.CoordinatorMessage{})) // fills the buffer
	}
}

func TestWorkerSender_TimesOutWhenBufferFull(t *testing.T) {
	worker.SetSendTimeoutForTest(t, 50*time.Millisecond)
	stream := newWedgedStream()
	ws := worker.NewWorkerSender(stream)
	t.Cleanup(func() { close(stream.release); ws.Close() })

	fillBuffer(t, ws, stream)

	// Run the blocking send in a goroutine so the test fails fast (rather than
	// hanging) against the unfixed code.
	errc := make(chan error, 1)
	go func() { errc <- ws.Send(&relayv1.CoordinatorMessage{}) }()
	select {
	case err := <-errc:
		require.ErrorIs(t, err, worker.ErrSendTimeout)
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return; it blocked forever on a full buffer")
	}
}

func TestWorkerSender_ReturnsDisconnectedWhenStreamDiesMidWait(t *testing.T) {
	worker.SetSendTimeoutForTest(t, 5*time.Second) // long; the stream dies first
	stream := newWedgedStream()
	ws := worker.NewWorkerSender(stream)
	t.Cleanup(func() { ws.Close() })

	fillBuffer(t, ws, stream)

	// A send blocked on the full buffer...
	errc := make(chan error, 1)
	go func() { errc <- ws.Send(&relayv1.CoordinatorMessage{}) }()

	// ...then the stream dies: the parked Send returns an error, the loop exits
	// and closes the sender. With no consumer draining the full buffer, the
	// blocked Send must observe the closed sender, not free space.
	time.Sleep(50 * time.Millisecond)
	close(stream.release)

	select {
	case err := <-errc:
		require.ErrorIs(t, err, worker.ErrWorkerDisconnected)
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after the stream died")
	}
}
