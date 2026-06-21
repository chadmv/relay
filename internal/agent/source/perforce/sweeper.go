package perforce

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ErrEvictClaimLost is returned by Sweeper.evict when the optional Claim hook
// declines the reservation - the workspace is currently held by a task or is
// already being evicted. It is a benign, expected outcome of losing the
// eviction race, not a failure: SweepOnce skips such entries without logging
// or counting them. Callers distinguish it with errors.Is.
var ErrEvictClaimLost = errors.New("sweeper: evict claim lost (workspace in use or already evicting)")

// Sweeper evicts stale workspaces by age and/or disk pressure.
type Sweeper struct {
	Root          string
	MaxAge        time.Duration // 0 = disabled
	MinFreeGB     int64         // 0 = disabled
	SweepInterval time.Duration // 0 = 15m default

	// Reg is the shared on-disk workspace registry. Required.
	Reg *Registry

	Client      *Client
	ListLocked  func() map[string]bool           // returns short_ids of currently-held workspaces
	FreeDiskGB  func(root string) (int64, error) // injectable for tests
	OnEvictedCB func(shortID string)

	// Claim, when non-nil, is invoked by evict before any destructive work to
	// atomically reserve the short_id against a concurrent Prepare (mirroring
	// Provider.EvictWorkspace's p.evicting reservation). If ok is false the
	// entry is held or already being evicted: evict performs no deletion and
	// returns ErrEvictClaimLost. If ok is true, evict defers release() and
	// proceeds. Left nil where the caller already holds the reservation (the
	// internal Sweeper that EvictWorkspace builds).
	Claim func(shortID string) (release func(), ok bool)
}

// Run starts the background sweep loop. Blocks until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	if s.MaxAge == 0 && s.MinFreeGB == 0 {
		return
	}
	interval := s.SweepInterval
	if interval == 0 {
		interval = 15 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = s.SweepOnce(ctx)
		}
	}
}

// SweepOnce performs one sweep pass. Returns short_ids of evicted workspaces.
func (s *Sweeper) SweepOnce(ctx context.Context) ([]string, error) {
	if s.Reg == nil {
		return nil, errors.New("sweeper: Reg is required")
	}
	reg := s.Reg

	locked := map[string]bool{}
	if s.ListLocked != nil {
		locked = s.ListLocked()
	}

	// Build candidate list: unlocked workspaces, sorted oldest-first.
	snap := reg.Snapshot()
	candidates := make([]WorkspaceEntry, 0, len(snap))
	for _, w := range snap {
		if !locked[w.ShortID] {
			candidates = append(candidates, w)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].LastUsedAt.Before(candidates[j].LastUsedAt)
	})

	var evicted []string
	now := time.Now()

	// Age pass: evict anything older than MaxAge.
	if s.MaxAge > 0 {
		for _, w := range candidates {
			if now.Sub(w.LastUsedAt) > s.MaxAge {
				if err := s.evict(ctx, reg, w); err != nil {
					if errors.Is(err, ErrEvictClaimLost) {
						continue
					}
					log.Printf("sweeper: evict %s: %v", w.ShortID, err)
					continue
				}
				evicted = append(evicted, w.ShortID)
			}
		}
	}

	// Pressure pass: evict oldest until free disk meets MinFreeGB.
	if s.MinFreeGB > 0 && s.FreeDiskGB != nil {
		for _, w := range candidates {
			// Skip if already evicted above.
			if _, ok := reg.Get(w.ShortID); !ok {
				continue
			}
			free, err := s.FreeDiskGB(s.Root)
			if err != nil {
				return evicted, err
			}
			if free >= s.MinFreeGB {
				break
			}
			if err := s.evict(ctx, reg, w); err != nil {
				if errors.Is(err, ErrEvictClaimLost) {
					continue
				}
				log.Printf("sweeper: evict %s: %v", w.ShortID, err)
				continue
			}
			evicted = append(evicted, w.ShortID)
		}
	}
	return evicted, nil
}

func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
	if s.Claim != nil {
		release, ok := s.Claim(w.ShortID)
		if !ok {
			return ErrEvictClaimLost
		}
		// release() must stay the outermost deferred cleanup so the reservation
		// outlives OnEvictedCB (which clears p.workspaces). A Prepare arriving
		// between OnEvictedCB and release() still sees p.evicting set and backs
		// out; only after release() does it find no registry entry and rebuild.
		defer release()
	}
	// When w.DirtyDelete is set, a prior sweep already deleted the p4 client
	// and only the on-disk directory remains. Calling DeleteClient again would
	// fail ("client doesn't exist") and previously wedged the whole sweep.
	if !w.DirtyDelete {
		if err := s.Client.DeleteClient(ctx, w.ClientName); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(s.Root, w.ShortID)); err != nil {
		_ = reg.MarkDirtyDelete(w.ShortID, true)
		_ = reg.Save()
		return err
	}
	reg.Remove(w.ShortID)
	if err := reg.Save(); err != nil {
		return err
	}
	if s.OnEvictedCB != nil {
		s.OnEvictedCB(w.ShortID)
	}
	return nil
}
