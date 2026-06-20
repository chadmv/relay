package worker

import (
	"sync"
	"time"
)

// graceEntry pairs a pending timer with the connection_epoch that was live when
// the worker disconnected. The epoch is passed to onExpire at fire time so the
// requeue can be fenced (RequeueWorkerTasksIfEpoch), no-opping if the worker has
// since reconnected at a newer epoch.
type graceEntry struct {
	timer *time.Timer
	epoch int32
}

// GraceRegistry tracks per-worker grace timers. When a worker disconnects,
// Start schedules its onExpire callback to fire after window. If the worker
// reconnects before expiry, Cancel stops the timer. Stop cancels all pending
// timers without firing any of them (used on server shutdown).
//
// GraceRegistry is safe for concurrent use.
type GraceRegistry struct {
	mu       sync.Mutex
	timers   map[string]*graceEntry
	window   time.Duration
	onExpire func(workerID string, epoch int32)
	stopped  bool
}

// NewGraceRegistry returns a registry configured with the given grace window
// and expiry callback.
func NewGraceRegistry(window time.Duration, onExpire func(workerID string, epoch int32)) *GraceRegistry {
	return &GraceRegistry{
		timers:   make(map[string]*graceEntry),
		window:   window,
		onExpire: onExpire,
	}
}

// Start schedules onExpire(workerID, epoch) to fire after g.window. If a timer
// already exists for workerID, it is reset to the full window (idempotent).
func (g *GraceRegistry) Start(workerID string, epoch int32) {
	g.StartWithDuration(workerID, epoch, g.window)
}

// StartWithDuration schedules onExpire(workerID, epoch) to fire after d. If a
// timer already exists for workerID, it is replaced. Used by startup
// reconciliation to honor remaining grace from a persisted disconnect time.
func (g *GraceRegistry) StartWithDuration(workerID string, epoch int32, d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	if old, ok := g.timers[workerID]; ok {
		old.timer.Stop()
	}
	entry := &graceEntry{epoch: epoch}
	entry.timer = time.AfterFunc(d, func() {
		g.mu.Lock()
		// Guard against ABA: only fire if this specific entry is still the
		// active one. A concurrent Start may have replaced it between timer
		// expiry and lock acquisition.
		if g.timers[workerID] != entry {
			g.mu.Unlock()
			return
		}
		delete(g.timers, workerID)
		g.mu.Unlock()
		g.onExpire(workerID, entry.epoch)
	})
	g.timers[workerID] = entry
}

// ExpireNow invokes onExpire(workerID, epoch) synchronously without scheduling a
// timer. If a timer was already pending for workerID, it is cancelled to
// preserve the ABA-safety invariant. No-op if the registry has been Stopped.
// Used by startup reconciliation when persisted grace has already expired
// during downtime.
func (g *GraceRegistry) ExpireNow(workerID string, epoch int32) {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	if old, ok := g.timers[workerID]; ok {
		old.timer.Stop()
		delete(g.timers, workerID)
	}
	g.mu.Unlock()
	g.onExpire(workerID, epoch)
}

// Cancel stops any pending timer for workerID. Safe to call if no timer exists.
func (g *GraceRegistry) Cancel(workerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.timers[workerID]; ok {
		e.timer.Stop()
		delete(g.timers, workerID)
	}
}

// Stop cancels all pending timers without firing any of them. After Stop,
// subsequent Start calls are no-ops.
func (g *GraceRegistry) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	for id, e := range g.timers {
		e.timer.Stop()
		delete(g.timers, id)
	}
}
