package worker

import (
	"fmt"
	"sync"

	relayv1 "relay/internal/proto/relayv1"
)

// Sender is the send side of a connected agent's gRPC stream.
type Sender interface {
	Send(*relayv1.CoordinatorMessage) error
}

// Registry is a thread-safe map of connected worker gRPC streams.
type Registry struct {
	mu      sync.RWMutex
	streams map[string]Sender
}

// NewRegistry returns a ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{streams: make(map[string]Sender)}
}

// Register adds a worker stream. Replaces any existing entry for workerID.
func (r *Registry) Register(workerID string, s Sender) {
	r.mu.Lock()
	r.streams[workerID] = s
	r.mu.Unlock()
}

// Unregister removes a worker stream.
func (r *Registry) Unregister(workerID string) {
	r.mu.Lock()
	delete(r.streams, workerID)
	r.mu.Unlock()
}

// Send delivers msg to the named worker. Returns an error if the worker is
// not currently connected.
func (r *Registry) Send(workerID string, msg *relayv1.CoordinatorMessage) error {
	r.mu.RLock()
	s, ok := r.streams[workerID]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("worker %q is not connected", workerID)
	}
	return s.Send(msg)
}
