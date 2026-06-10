package worker

import (
	"errors"
	"sync"
	"time"

	relayv1 "relay/internal/proto/relayv1"
)

// ErrWorkerDisconnected is returned when a send is attempted on a closed sender.
var ErrWorkerDisconnected = errors.New("worker disconnected")

// ErrSendTimeout is returned when a send cannot be enqueued before sendTimeout
// elapses, which happens when the buffer is full because the send goroutine is
// wedged inside stream.Send on a dead peer.
var ErrSendTimeout = errors.New("worker send timed out")

// sendTimeout bounds how long Send waits for buffer space. It is a var (not a
// const) so tests can lower it via SetSendTimeoutForTest.
var sendTimeout = 5 * time.Second

// workerSender serializes all writes to a gRPC stream through a single
// send goroutine. gRPC bidirectional streams are not concurrent-send-safe.
type workerSender struct {
	stream  Sender
	queue   chan *relayv1.CoordinatorMessage
	stopReq chan struct{}
	closed  chan struct{}
	once    sync.Once
}

// NewWorkerSender wraps a raw stream in a send goroutine and returns a Sender
// that is safe for concurrent use. Call Close when the underlying stream ends.
func NewWorkerSender(stream Sender) *workerSender {
	sender := &workerSender{
		stream:  stream,
		queue:   make(chan *relayv1.CoordinatorMessage, 64),
		stopReq: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	go sender.loop()
	return sender
}

func (sender *workerSender) loop() {
	defer close(sender.closed)
	for {
		select {
		case msg := <-sender.queue:
			if err := sender.stream.Send(msg); err != nil {
				return
			}
		case <-sender.stopReq:
			return
		}
	}
}

// Send enqueues message for delivery. Blocks if the internal buffer is full
// until space is available, the sender is closed, or sendTimeout elapses.
func (sender *workerSender) Send(message *relayv1.CoordinatorMessage) error {
	// Check closed first (non-blocking) to give a deterministic closed signal.
	select {
	case <-sender.closed:
		return ErrWorkerDisconnected
	default:
	}
	timeout := time.NewTimer(sendTimeout)
	defer timeout.Stop()
	select {
	case sender.queue <- message:
		return nil
	case <-sender.closed:
		return ErrWorkerDisconnected
	case <-timeout.C:
		return ErrSendTimeout
	}
}

// Close stops the send loop. Safe to call multiple times.
func (sender *workerSender) Close() {
	sender.once.Do(func() { close(sender.stopReq) })
	<-sender.closed
}
