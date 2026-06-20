package perforce

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRegistry_ConcurrentSweepAndMutate(t *testing.T) {
	root := t.TempDir()
	reg := &Registry{path: root + "/.relay-registry.json"}

	// Seed entries the workers will mutate and the sweeper will scan.
	const n = 24
	for i := 0; i < n; i++ {
		reg.Upsert(WorkspaceEntry{
			ShortID:    fmt.Sprintf("ws%d", i),
			SourceKey:  fmt.Sprintf("//s/%d", i),
			LastUsedAt: time.Now(),
		})
	}

	// Sweeper with stubbed I/O: never actually evicts (MaxAge huge), just scans.
	// Match the Client/stub construction used in sweeper_test.go.
	sw := &Sweeper{
		Root:       root,
		MaxAge:     time.Hour,
		Reg:        reg,
		Client:     &Client{r: newFakeP4Fixture(t)},
		ListLocked: func() map[string]bool { return map[string]bool{} },
	}

	ctx := context.Background()
	var writers sync.WaitGroup
	var sweeper sync.WaitGroup
	stop := make(chan struct{})

	// Sweeper goroutine: repeated Snapshot-based scans.
	sweeper.Add(1)
	go func() {
		defer sweeper.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = sw.SweepOnce(ctx)
			}
		}
	}()

	// Writer goroutines: Upsert / Mutate / AddPendingCL / Remove / Get / Snapshot.
	for g := 0; g < 8; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("ws%d", (g+i)%n)
				reg.Upsert(WorkspaceEntry{ShortID: id, SourceKey: "//s/" + id, LastUsedAt: time.Now()})
				_ = reg.Mutate(id, func(e *WorkspaceEntry) { e.LastUsedAt = time.Now() })
				_ = reg.AddPendingCL(id, fmt.Sprintf("t%d-%d", g, i), int64(i))
				_, _ = reg.Get(id)
				_ = reg.Snapshot()
				_ = reg.RemovePendingCL(id, fmt.Sprintf("t%d-%d", g, i))
			}
		}(g)
	}

	writers.Wait() // writers finish
	close(stop)
	sweeper.Wait() // sweeper observes stop and exits
}
