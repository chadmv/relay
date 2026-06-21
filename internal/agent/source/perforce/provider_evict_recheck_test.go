package perforce

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// gatingRunner wraps a Runner and blocks the first Run whose joined args match
// gateKey until proceed is closed, signaling entered when the gated call is
// reached. It lets a test pause EvictWorkspace inside its destructive phase
// (the `client -d` call) while its p.evicting reservation is held.
type gatingRunner struct {
	inner    Runner
	gateKey  string
	entered  chan struct{}
	proceed  chan struct{}
	gatedOne bool
}

func (g *gatingRunner) Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error) {
	key := joinArgs(args)
	if key == g.gateKey && !g.gatedOne {
		g.gatedOne = true
		close(g.entered)
		<-g.proceed
	}
	return g.inner.Run(ctx, cwd, args, stdin)
}

func (g *gatingRunner) Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error {
	return g.inner.Stream(ctx, cwd, args, onLine)
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

// TestEvictWorkspace_PrepareBacksOutWhenEvictReservesDuringAcquire closes the
// residual TOCTOU: EvictWorkspace can reserve the short ID (observing zero
// holders) in the window AFTER Prepare passes its pre-Acquire evicting check but
// BEFORE Prepare's ws.Acquire registers a holder. A Prepare that then acquires
// the workspace must re-check the reservation and back out (release the handle,
// return the "being evicted" error) rather than sync into a workspace being
// deleted out from under it.
//
// Deterministic, no timing: a package-var hook drives the concurrent
// EvictWorkspace into the gap, and a gating runner pauses EvictWorkspace inside
// its `client -d` call so the reservation is provably held when Prepare's
// re-check runs.
func TestEvictWorkspace_PrepareBacksOutWhenEvictReservesDuringAcquire(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	gate := &gatingRunner{
		inner:   fr,
		entered: make(chan struct{}),
		proceed: make(chan struct{}),
	}
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: gate}})

	// Resolve the short ID Prepare will allocate for this stream and seed the
	// registry + on-disk dir so EvictWorkspace finds something to delete.
	reg, err := p.Registry()
	require.NoError(t, err)
	shortID := allocateShortID("//depot/main", reg)
	clientName := "relay_host_" + shortID
	fr.set("client -d "+clientName, "Client deleted.\n")
	gate.gateKey = "client -d " + clientName
	reg.Upsert(WorkspaceEntry{
		ShortID:    shortID,
		SourceKey:  "//depot/main",
		ClientName: clientName,
		LastUsedAt: time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	// The hook fires on Prepare's goroutine, after the pre-check and before
	// Acquire. It launches EvictWorkspace concurrently and waits until that
	// eviction has reserved the short ID and reached its (gated) `client -d`
	// call, i.e. the reservation is set but the slow evict is not yet done.
	evictErr := make(chan error, 1)
	var once bool
	prepareAcquireHook = func(string) {
		if once {
			return
		}
		once = true
		go func() { evictErr <- p.EvictWorkspace(context.Background(), shortID) }()
		<-gate.entered // EvictWorkspace has reserved and is paused in client -d
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

	// Let the gated EvictWorkspace finish so we can join it cleanly.
	close(gate.proceed)
	require.NoError(t, <-evictErr)

	// The losing Prepare must back out: it must return the "being evicted"
	// error and must NOT have synced into the workspace being deleted.
	require.Error(t, prepErr, "Prepare must not succeed when it loses the eviction race")
	require.ErrorContains(t, prepErr, "being evicted")

	// And it must not leave a holder dangling on the workspace.
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
