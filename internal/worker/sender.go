package worker

import (
	"errors"
	"sync"

	relayv1 "relay/internal/proto/relayv1"
)

// ErrWorkerDisconnected is returned when a send is attempted on a closed sender.
var ErrWorkerDisconnected = errors.New("worker disconnected")

// workerSender serializes all writes to a gRPC stream through a single
// send goroutine. gRPC bidirectional streams are not concurrent-send-safe.
type workerSender struct {
	stream Sender
	ch     chan *relayv1.CoordinatorMessage
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

// NewWorkerSender wraps a raw stream in a send goroutine and returns a Sender
// that is safe for concurrent use. Call Close when the underlying stream ends.
func NewWorkerSender(stream Sender) *workerSender {
	ws := &workerSender{
		stream: stream,
		ch:     make(chan *relayv1.CoordinatorMessage, 64),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go ws.loop()
	return ws
}

func (ws *workerSender) loop() {
	defer close(ws.done)
	for {
		select {
		case msg := <-ws.ch:
			if err := ws.stream.Send(msg); err != nil {
				return
			}
		case <-ws.stop:
			return
		}
	}
}

// Send enqueues msg for delivery. Blocks if the internal buffer is full until
// either space is available or the sender is closed.
func (ws *workerSender) Send(msg *relayv1.CoordinatorMessage) error {
	// Check done first (non-blocking) to give a deterministic closed signal.
	select {
	case <-ws.done:
		return ErrWorkerDisconnected
	default:
	}
	select {
	case ws.ch <- msg:
		return nil
	case <-ws.done:
		return ErrWorkerDisconnected
	}
}

// Close stops the send loop. Safe to call multiple times.
func (ws *workerSender) Close() {
	ws.once.Do(func() { close(ws.stop) })
	<-ws.done
}
