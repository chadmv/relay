package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire is the
// background-sweeper analogue of
// TestEvictWorkspace_PrepareBacksOutWhenEvictReservesDuringAcquire. The
// dominant eviction path is the background Sweeper, not the manual
// EvictWorkspace API. With the Claim hook wired (Sweeper.Claim =
// Provider.ReserveForEvict), a sweeper eviction reserves p.evicting[shortID]
// under p.mu after an inline holder re-check; a concurrent Prepare that loses
// the race must observe the reservation in its post-Acquire re-check and back
// out (release the handle, return "being evicted") rather than sync into a
// workspace being deleted.
//
// Deterministic, no timing: prepareAcquireHook drives the concurrent SweepOnce
// into the gap, and a gatingRunner pauses the sweeper inside its `client -d`
// call so the reservation is provably held when Prepare's re-check runs.
func TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	gate := &gatingRunner{
		inner:   fr,
		entered: make(chan struct{}),
		proceed: make(chan struct{}),
	}
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: gate}})

	reg, err := p.Registry()
	require.NoError(t, err)
	shortID := allocateShortID("//depot/main", reg)
	clientName := "relay_host_" + shortID
	fr.set("client -d "+clientName, "Client deleted.\n")
	gate.gateKey = "client -d " + clientName
	// Seed the entry as STALE so the sweeper's age pass selects it.
	reg.Upsert(WorkspaceEntry{
		ShortID:    shortID,
		SourceKey:  "//depot/main",
		ClientName: clientName,
		LastUsedAt: time.Now().Add(-30 * 24 * time.Hour),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	// The background sweeper, with Claim wired exactly as cmd/relay-agent does.
	sw := &Sweeper{
		Root:        root,
		Reg:         reg,
		MaxAge:      14 * 24 * time.Hour,
		Client:      p.Client(),
		ListLocked:  p.LockedShortIDs,
		Claim:       p.ReserveForEvict,
		OnEvictedCB: p.InvalidateWorkspace,
	}

	// The hook fires on Prepare's goroutine, after the pre-check and before
	// Acquire. It launches one SweepOnce concurrently and waits until that
	// sweep has reserved the short ID and reached its (gated) `client -d` call,
	// i.e. the reservation is set but the slow evict is not yet done.
	type sweepResult struct {
		evicted []string
		err     error
	}
	sweepDone := make(chan sweepResult, 1)
	var once bool
	prepareAcquireHook = func(string) {
		if once {
			return
		}
		once = true
		go func() {
			ev, err := sw.SweepOnce(context.Background())
			sweepDone <- sweepResult{ev, err}
		}()
		<-gate.entered // sweeper has reserved and is paused in client -d
	}
	t.Cleanup(func() { prepareAcquireHook = nil })

	spec := &relayv1.SourceSpec{
		Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{
				Stream: "//depot/main",
				Sync:   []*relayv1.SyncEntry{{Path: "//depot/main/...", Rev: "@1"}},
			},
		},
	}

	_, prepErr := p.Prepare(context.Background(), "task-1", spec, func(string) {})

	// Let the gated sweep finish so we can join it cleanly.
	close(gate.proceed)
	res := <-sweepDone
	require.NoError(t, res.err, "SweepOnce itself must not error")

	// The hook forces the sweep to reserve (ReserveForEvict succeeds, then it
	// reaches the gated `client -d`) BEFORE Prepare's ws.Acquire runs. So the
	// sweep WINS the race and completes the eviction, exactly as EvictWorkspace
	// does in the manual-path test; the live Prepare is the loser and must back
	// out. The safety property is mutual exclusion: the sweep evicts AND Prepare
	// never synced into the workspace (it returns "being evicted" and holds
	// nothing).
	require.Error(t, prepErr, "Prepare must not succeed when it loses the sweep race")
	require.ErrorContains(t, prepErr, "being evicted")

	// The winning sweep must have evicted the workspace it reserved first.
	require.Equal(t, []string{shortID}, res.evicted, "the winning sweep must complete the eviction")

	// And the losing Prepare must not leave a holder dangling.
	p.mu.Lock()
	ws := p.workspaces[shortID]
	p.mu.Unlock()
	if ws != nil {
		ws.mu.Lock()
		n := len(ws.holders)
		ws.mu.Unlock()
		require.Zero(t, n, "losing Prepare must release the workspace handle it acquired")
	}
}
