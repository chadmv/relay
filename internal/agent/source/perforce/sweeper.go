package perforce

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Sweeper evicts stale workspaces by age and/or disk pressure.
type Sweeper struct {
	Root          string
	MaxAge        time.Duration // 0 = disabled
	MinFreeGB     int64         // 0 = disabled
	SweepInterval time.Duration // 0 = 15m default

	Client      *Client
	ListLocked  func() map[string]bool           // returns short_ids of currently-held workspaces
	FreeDiskGB  func(root string) (int64, error) // injectable for tests
	OnEvictedCB func(shortID string)
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
	reg, err := LoadRegistry(filepath.Join(s.Root, ".relay-registry.json"))
	if err != nil {
		return nil, err
	}

	locked := map[string]bool{}
	if s.ListLocked != nil {
		locked = s.ListLocked()
	}

	// Build candidate list: unlocked workspaces, sorted oldest-first.
	candidates := make([]WorkspaceEntry, 0, len(reg.Workspaces))
	for _, w := range reg.Workspaces {
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
					return evicted, err
				}
				evicted = append(evicted, w.ShortID)
			}
		}
	}

	// Pressure pass: evict oldest until free disk meets MinFreeGB.
	if s.MinFreeGB > 0 && s.FreeDiskGB != nil {
		for _, w := range candidates {
			// Skip if already evicted above.
			if reg.Get(w.ShortID) == nil {
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
				return evicted, err
			}
			evicted = append(evicted, w.ShortID)
		}
	}
	return evicted, nil
}

func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
	if err := s.Client.DeleteClient(ctx, w.ClientName); err != nil {
		return err
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
