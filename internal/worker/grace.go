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
// timer already exists for workerID it is replaced, UNLESS its epoch is newer
// than the incoming one, in which case this call is refused and the existing
// timer left running. Used by startup reconciliation to honor remaining grace
// from a persisted disconnect time.
//
// THE MONOTONICITY RULE IS THE REGISTRY'S OWN, because no caller can supply it.
// releaseWorkerGeneration decides ownership with an epoch fence evaluated inside
// Postgres and then calls Start on the result, so the check and the arming are a
// round trip apart with nothing held across them: a superseded release can win
// that race and evict a LIVE generation's entry. The stale entry it installs
// then fires against RequeueWorkerTasksIfEpoch's own fence, matches zero rows,
// and the live worker's tasks are requeued by nobody - they sit until the 24h
// stale-task watchdog fails them as timed_out.
//
// ONLY A STRICTLY OLDER EPOCH IS REFUSED. An equal epoch still replaces, which
// is what keeps Start idempotent-with-reset for repeated calls within one
// generation.
func (g *GraceRegistry) StartWithDuration(workerID string, epoch int32, d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return
	}
	if old, ok := g.timers[workerID]; ok {
		if old.epoch > epoch {
			return // a newer generation owns this worker's timer
		}
		old.timer.Stop()
	}
	entry := &graceEntry{epoch: epoch}
	entry.timer = time.AfterFunc(d, func() {
		g.mu.Lock()
		// Guard against ABA: only fire if this specific ENTRY is still the active
		// one. Identity, not epoch - an equal epoch still replaces (see above), so
		// a same-epoch replacement is indistinguishable from this entry by epoch
		// and an epoch comparison here would wave the fired timer through.
		//
		// A concurrent Start may have replaced this entry between timer expiry and
		// lock acquisition, and Stop cannot recall a timer that has already fired.
		// THE TWO CONTROLS ARE INDEPENDENT rather than one propping up the other:
		// deleting the refusal above fails only
		// TestGraceRegistry_AStaleEpochDoesNotDisplaceALiveTimer, deleting this
		// fails only the two AFiredTimer tests. What this one covers is the window
		// the refusal cannot reach - once a timer has fired there is no Start left
		// to refuse. Pinned by
		// TestGraceRegistry_AFiredTimerDoesNotEvictTheEntryThatReplacedIt and
		// TestGraceRegistry_AFiredTimerDoesNotEvictItsSameEpochReplacement.
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
//
// IT IS DELIBERATELY EXEMPT FROM StartWithDuration'S MONOTONICITY RULE. It
// cancels and fires whatever epoch it is handed without comparing it against a
// pending entry's, and what makes that safe is WHERE it is called from rather
// than anything about the method.
//
// ITS ONLY NON-TEST CALLER is cmd/relay-server's
// seedGraceTimersFromActiveTasks, which runs on the main goroutine before the
// gRPC server is constructed, so no concurrent Start can race it.
//
// THAT IS ONLY HALF THE ARGUMENT, and the obvious other half - "the registry was
// built moments earlier, so the map is empty" - is true of the FIRST candidate
// only. The same loop calls Start and StartWithDuration for the others, so by
// candidate N the map can hold up to N-1 entries. What actually holds is a
// property of the query: ListGraceCandidates is a SELECT DISTINCT whose only
// non-key columns come from the same single workers row, so distinct rows means
// distinct worker ids and no id can appear twice in that loop. ExpireNow
// therefore never finds an entry under its OWN key, and the cancel it performs
// cancels nothing.
//
// A SECOND CALLER WOULD HAVE TO REPRODUCE THAT PROPERTY OF ITS INPUT, which is
// not something this method can check. Give it the same `old.epoch > epoch`
// refusal before adding one.
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
