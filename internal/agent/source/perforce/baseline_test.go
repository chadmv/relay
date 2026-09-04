package perforce

import (
	"testing"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

func TestBaselineHash_StableUnderReorder(t *testing.T) {
	a := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/a/...", Rev: "@100"},
			{Path: "//s/x/b/...", Rev: "@200"},
		},
		Unshelves: []int64{2, 1, 3},
	}
	b := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/b/...", Rev: "@200"},
			{Path: "//s/x/a/...", Rev: "@100"},
		},
		Unshelves: []int64{3, 1, 2},
	}
	require.Equal(t, BaselineHash(a, nil), BaselineHash(b, nil))
}

func TestBaselineHash_HeadResolvedVsLiteral(t *testing.T) {
	a := &relayv1.PerforceSource{
		Sync: []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
	}
	resolved := map[string]string{"//s/x/...": "@12345"}
	h1 := BaselineHash(a, nil)      // #head as sentinel
	h2 := BaselineHash(a, resolved) // resolved to @12345
	require.NotEqual(t, h1, h2, "estimated and resolved must differ")
}

func TestPathOverlap(t *testing.T) {
	require.True(t, PathPrefixOverlap("//a/b/...", "//a/b/c/..."))
	require.True(t, PathPrefixOverlap("//a/b/c/...", "//a/b/..."))
	require.False(t, PathPrefixOverlap("//a/b/...", "//a/c/..."))
	require.True(t, PathPrefixOverlap("//a/b/...", "//a/b/..."))
	require.False(t, PathPrefixOverlap("//a/b/x.ma", "//a/b/y.ma"))
}

// The no-exclusion encoding is a cross-process contract:
// scheduler.BaselineHashFromAPISpec computes it server-side for warm scoring,
// so a moved encoding re-syncs every warm workspace in the fleet once. The
// literal is captured from the binary rather than derived from the
// implementation, which is what makes an encoding change a deliberate RED here
// instead of a silent fleet event.
//
// The fixture is built to see a shifted separator: two entries whose revs are
// both non-empty and different, and two unshelves, so dropping any one NUL or
// the section terminator re-associates content across a boundary and moves the
// digest.
const goldenNoExclusionBaseline = "0bdf75118a365d31"

func TestBaselineHash_NoExclusionsIsUnchanged(t *testing.T) {
	p := &relayv1.PerforceSource{
		Stream: "//s/x",
		Sync: []*relayv1.SyncEntry{
			{Path: "//s/x/a/...", Rev: "@100"},
			{Path: "//s/x/b/...", Rev: "@200"},
		},
		Unshelves: []int64{2, 1},
	}
	require.Equal(t, goldenNoExclusionBaseline, BaselineHash(p, nil))
}
