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
