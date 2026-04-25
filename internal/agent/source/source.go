package source

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	relayv1 "relay/internal/proto/relayv1"
)

// Provider prepares a workspace for a task and returns a Handle.
type Provider interface {
	Type() string
	// Prepare acquires a workspace and prepares it (sync, optional unshelve).
	// taskID identifies the calling task — providers use it to scope side effects
	// (e.g. the per-task pending changelist used for unshelve cleanup).
	Prepare(ctx context.Context, taskID string, spec *relayv1.SourceSpec, progress func(line string)) (Handle, error)
}

// Handle represents an acquired workspace for a single task execution.
type Handle interface {
	WorkingDir() string
	Env() map[string]string
	Finalize(ctx context.Context) error
	Inventory() InventoryEntry
}

// InventoryEntry describes a workspace for reporting to the coordinator.
type InventoryEntry struct {
	SourceType   string
	SourceKey    string
	ShortID      string
	BaselineHash string
	LastUsedAt   time.Time
	Deleted      bool
}

// InventoryLister is an optional extension of Provider that reports the
// current on-disk workspace inventory for inclusion in RegisterRequest.
type InventoryLister interface {
	ListInventory() ([]InventoryEntry, error)
}

// ErrUnknownProvider is returned by Registry.Get when the type is not registered.
var ErrUnknownProvider = errors.New("unknown source provider")

// Registry holds Provider factories keyed by type string.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]func() Provider
	instances map[string]Provider
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: map[string]func() Provider{},
		instances: map[string]Provider{},
	}
}

// Register adds a factory for the given provider type.
func (r *Registry) Register(typ string, factory func() Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[typ] = factory
}

// Get returns the singleton Provider for the given type, creating it on first call.
func (r *Registry) Get(typ string) (Provider, error) {
	r.mu.RLock()
	if p, ok := r.instances[typ]; ok {
		r.mu.RUnlock()
		return p, nil
	}
	factory, ok := r.factories[typ]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, typ)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if p, ok := r.instances[typ]; ok {
		return p, nil
	}
	p := factory()
	r.instances[typ] = p
	return p, nil
}
