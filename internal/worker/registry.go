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

// UnregisterIf removes the worker's stream only if the currently registered
// sender is s. Returns true if it removed it (this caller still owned the
// slot); false if a newer connection has since replaced it. Pointer identity
// works because the registry stores *workerSender values behind the Sender
// interface; interface comparison falls through to pointer equality.
func (r *Registry) UnregisterIf(workerID string, s Sender) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streams[workerID] != s {
		return false
	}
	delete(r.streams, workerID)
	return true
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

// SendEvictCommand sends an EvictWorkspaceCommand to the named connected worker.
// Returns an error if the worker is not connected.
func (r *Registry) SendEvictCommand(workerID string, cmd *relayv1.EvictWorkspaceCommand) error {
	return r.Send(workerID, &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_EvictWorkspace{EvictWorkspace: cmd},
	})
}
