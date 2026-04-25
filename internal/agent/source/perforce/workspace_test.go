package perforce

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newReq(baseline string, paths []string, unshelves []int64, exclusive bool) Request {
	return Request{
		BaselineHash:       baseline,
		SyncPaths:          paths,
		Unshelves:          unshelves,
		WorkspaceExclusive: exclusive,
	}
}

func TestWorkspace_IdenticalSharedAdmits(t *testing.T) {
	ws := NewWorkspace("a")
	ctx := context.Background()
	h1, err := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
	require.NoError(t, err)
	require.Equal(t, ModeShared, h1.Mode())

	done := make(chan struct{})
	go func() {
		h2, err := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
		require.NoError(t, err)
		require.Equal(t, ModeShared, h2.Mode())
		h2.Release()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("identical-baseline shared acquire did not admit immediately")
	}
	h1.Release()
}

func TestWorkspace_DifferentBaselineSerializes(t *testing.T) {
	ws := NewWorkspace("a")
	ctx := context.Background()
	h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
	require.Equal(t, ModeShared, h1.Mode())

	var admitted atomic.Bool
	go func() {
		h2, err := ws.Acquire(ctx, newReq("BL2", []string{"//s/x/..."}, nil, false))
		require.NoError(t, err)
		admitted.Store(true)
		require.Equal(t, ModeExclusive, h2.Mode())
		h2.Release()
	}()
	time.Sleep(50 * time.Millisecond)
	require.False(t, admitted.Load(), "must wait while BL1 holder is active")
	h1.Release()
	require.Eventually(t, admitted.Load, time.Second, 10*time.Millisecond)
}

func TestWorkspace_DisjointAdditiveAdmits(t *testing.T) {
	ws := NewWorkspace("a")
	ctx := context.Background()
	h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/A/..."}, nil, false))
	h2, err := ws.Acquire(ctx, newReq("BL2", []string{"//s/x/B/..."}, nil, false))
	require.NoError(t, err)
	require.Equal(t, ModeShared, h2.Mode())
	h2.Release()
	h1.Release()
}

func TestWorkspace_OverlappingDifferentBaselineSerializes(t *testing.T) {
	ws := NewWorkspace("a")
	ctx := context.Background()
	h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/A/..."}, nil, false))

	var admitted atomic.Bool
	go func() {
		h2, _ := ws.Acquire(ctx, newReq("BL2", []string{"//s/x/A/sub/..."}, nil, false))
		admitted.Store(true)
		h2.Release()
	}()
	time.Sleep(50 * time.Millisecond)
	require.False(t, admitted.Load())
	h1.Release()
	require.Eventually(t, admitted.Load, time.Second, 10*time.Millisecond)
}

func TestWorkspace_ExclusiveBlocks(t *testing.T) {
	ws := NewWorkspace("a")
	ctx := context.Background()
	h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, true /*exclusive*/))
	require.Equal(t, ModeExclusive, h1.Mode())

	var admitted atomic.Bool
	go func() {
		h2, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
		admitted.Store(true)
		h2.Release()
	}()
	time.Sleep(50 * time.Millisecond)
	require.False(t, admitted.Load(), "exclusive holder must block any acquire")
	h1.Release()
	require.Eventually(t, admitted.Load, time.Second, 10*time.Millisecond)
}

func TestWorkspace_UnshelvingBlocks(t *testing.T) {
	ws := NewWorkspace("a")
	ctx := context.Background()
	h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, []int64{1234}, false))
	require.Equal(t, ModeExclusive, h1.Mode(),
		"unshelve requires exclusive end-to-end")
	h1.Release()
}

func TestWorkspace_AfterUnshelveRelease_NextTaskGetsExclusive(t *testing.T) {
	ws := NewWorkspace("a")
	ctx := context.Background()

	// Task 1: sync BL1 to path //s/x/...
	h1, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, nil, false))
	h1.Release()

	// Task 2: unshelve on BL1 (exclusive). After release, on-disk state is BL1 + unshelve artifacts.
	h2, _ := ws.Acquire(ctx, newReq("BL1", []string{"//s/x/..."}, []int64{1234}, false))
	require.Equal(t, ModeExclusive, h2.Mode())
	h2.Release()

	// Task 3: sync BL2 (different baseline). Must get ModeExclusive because workspace needs re-sync.
	h3, err := ws.Acquire(ctx, newReq("BL2", []string{"//s/x/..."}, nil, false))
	require.NoError(t, err)
	require.Equal(t, ModeExclusive, h3.Mode(), "must be exclusive: workspace may have unshelve artifacts from BL1")
	h3.Release()
}
