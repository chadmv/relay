package perforce

import (
	"context"
	"sync"
)

// Mode describes how a workspace is currently held.
type Mode int

const (
	ModeShared    Mode = 0
	ModeExclusive Mode = 1
)

// Request describes what a caller wants from a workspace.
type Request struct {
	BaselineHash       string
	SyncPaths          []string
	Unshelves          []int64
	WorkspaceExclusive bool
}

type holder struct {
	req  Request
	mode Mode
}

// Workspace serializes concurrent access to a single on-disk workspace.
//
// syncedPaths tracks the baseline that was last synced for each depot path
// that has been used by a holder. It is updated when a holder is released so
// that future callers can determine whether an exclusive re-sync is needed.
type Workspace struct {
	shortID string
	mu      sync.Mutex
	cond    *sync.Cond
	holders []*holder

	// syncedPaths maps depot-path prefix → last-synced baseline hash.
	// A nil map means the workspace has never been used.
	syncedPaths map[string]string
}

// NewWorkspace creates a new Workspace for the given shortID.
func NewWorkspace(shortID string) *Workspace {
	w := &Workspace{shortID: shortID}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// WorkspaceHandle is returned by Acquire and must be Release()d when done.
type WorkspaceHandle struct {
	ws *Workspace
	h  *holder
}

// Mode returns whether this hold is shared or exclusive.
func (h *WorkspaceHandle) Mode() Mode { return h.h.mode }

// Release returns the workspace hold.
func (h *WorkspaceHandle) Release() {
	h.ws.release(h.h)
}

// Downgrade switches an exclusive hold to shared, unblocking same-baseline
// peers that were waiting. Call after sync completes (before task body runs).
func (h *WorkspaceHandle) Downgrade() {
	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()
	h.h.mode = ModeShared
	h.ws.cond.Broadcast()
}

// Acquire blocks until the workspace can be held under the given Request.
// Cancelling ctx causes Acquire to return ctx.Err().
func (w *Workspace) Acquire(ctx context.Context, req Request) (*WorkspaceHandle, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mode, ok := w.tryAdmit(req)
		if ok {
			h := &holder{req: req, mode: mode}
			w.holders = append(w.holders, h)
			return &WorkspaceHandle{ws: w, h: h}, nil
		}
		// Wait for a broadcast, but also wake when ctx is cancelled.
		wakeOnCancel := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				w.mu.Lock()
				w.cond.Broadcast()
				w.mu.Unlock()
			case <-wakeOnCancel:
			}
		}()
		w.cond.Wait()
		close(wakeOnCancel)
	}
}

// tryAdmit applies the three-rule arbitration. Caller holds w.mu.
// Returns (mode, true) if the request can be admitted immediately.
func (w *Workspace) tryAdmit(req Request) (Mode, bool) {
	needsExclusive := req.WorkspaceExclusive || len(req.Unshelves) > 0

	if len(w.holders) == 0 {
		if needsExclusive {
			return ModeExclusive, true
		}
		// No current holders. Determine mode based on what was previously
		// synced to disk (syncedPaths).
		mode := w.modeForEmptyWorkspace(req)
		return mode, true
	}

	// Any needsExclusive request must wait until the workspace is empty.
	if needsExclusive {
		return 0, false
	}

	// Reject immediately if any current holder is exclusive.
	for _, h := range w.holders {
		if h.mode == ModeExclusive {
			return 0, false
		}
	}

	// Rule 1: identical baseline + no unshelves on either side → share.
	// Rule 2: disjoint sync paths (no overlap with any holder) → share.
	identical := true
	disjoint := true
	for _, h := range w.holders {
		if len(h.req.Unshelves) > 0 {
			return 0, false // holder has unshelves → must wait
		}
		if h.req.BaselineHash != req.BaselineHash {
			identical = false
		}
		for _, hp := range h.req.SyncPaths {
			for _, rp := range req.SyncPaths {
				if PathPrefixOverlap(hp, rp) {
					disjoint = false
				}
			}
		}
	}
	if identical {
		return ModeShared, true
	}
	if disjoint {
		return ModeShared, true
	}
	return 0, false // overlapping paths, different baseline → serialize
}

// modeForEmptyWorkspace determines whether an incoming request needs
// ModeExclusive or ModeShared when there are no current holders.
//
// If any of the request's sync paths overlap with a previously-synced path
// that carried a different baseline, the caller must re-sync exclusively.
// Paths that were never synced before, or that were synced to the same
// baseline, can be treated as shared.
func (w *Workspace) modeForEmptyWorkspace(req Request) Mode {
	for sp, baseline := range w.syncedPaths {
		for _, rp := range req.SyncPaths {
			if PathPrefixOverlap(sp, rp) && baseline != req.BaselineHash {
				return ModeExclusive
			}
		}
	}
	return ModeShared
}

// release removes h from the holder list, updates syncedPaths, and broadcasts.
func (w *Workspace) release(h *holder) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Record what was synced so future callers can determine their mode.
	// Update for ALL holders (including unshelve holds) so that the next
	// task for the same path sees the correct baseline and is not wrongly
	// admitted as ModeShared when the workspace needs a re-sync.
	if w.syncedPaths == nil {
		w.syncedPaths = make(map[string]string)
	}
	for _, p := range h.req.SyncPaths {
		w.syncedPaths[p] = h.req.BaselineHash
	}

	out := w.holders[:0]
	for _, x := range w.holders {
		if x != h {
			out = append(out, x)
		}
	}
	w.holders = out
	w.cond.Broadcast()
}
