package worker

import (
	"sync"
	"time"
)

// GraceRegistry tracks per-worker grace timers. When a worker disconnects,
// Start schedules its onExpire callback to fire after window. If the worker
// reconnects before expiry, Cancel stops the timer. Stop cancels all pending
// timers without firing any of them (used on server shutdown).
//
// GraceRegistry is safe for concurrent use.
type GraceRegistry struct {
	mu       sync.Mutex
	timers   map[string]*time.Timer
	window   time.Duration
	onExpire func(workerID string)
	stopped  bool
}

// NewGraceRegistry returns a registry configured with the given grace window
// and expiry callback.
func NewGraceRegistry(window time.Duration, onExpire func(workerID string)) *GraceRegistry {
	return &GraceRegistry{
		timers:   make(map[string]*time.Timer),
		window:   window,
		onExpire: onExpire,
	}
}

// Start schedules onExpire(workerID) to fire after window. If a timer already
// exists for workerID, it is reset to the full window (idempotent).
func (g *GraceRegistry) Start(workerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	if old, ok := g.timers[workerID]; ok {
		old.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(g.window, func() {
		g.mu.Lock()
		// Guard against ABA: only fire if this specific timer is still
		// the active one. A concurrent Start may have replaced the entry
		// between timer expiry and lock acquisition.
		if g.timers[workerID] != t {
			g.mu.Unlock()
			return
		}
		delete(g.timers, workerID)
		g.mu.Unlock()
		g.onExpire(workerID)
	})
	g.timers[workerID] = t
}

// Cancel stops any pending timer for workerID. Safe to call if no timer exists.
func (g *GraceRegistry) Cancel(workerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.timers[workerID]; ok {
		t.Stop()
		delete(g.timers, workerID)
	}
}

// Stop cancels all pending timers without firing any of them. After Stop,
// subsequent Start calls are no-ops.
func (g *GraceRegistry) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	for id, t := range g.timers {
		t.Stop()
		delete(g.timers, id)
	}
}
