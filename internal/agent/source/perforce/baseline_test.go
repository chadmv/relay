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

// THE DISCRIMINATING INPUT: two specs whose entries agree on path AND rev and
// differ ONLY in the exclusion flag. With the rev held equal the flag is the
// only bit that moves, so a BaselineHash that does not read it returns one
// value for both. Any pair that also varied the rev would pass against the
// broken build, because the rev is already hashed.
//
// The pair is synthetic on purpose and could not come from two valid specs:
// validateSourceSpec refuses a rev on an excluded entry, so a real exclusion
// carries rev "". The property under test is the hash function's own contract,
// and this is the input that isolates it.
func TestBaselineHash_TheExcludeFlagIsHashed(t *testing.T) {
	mk := func(exclude bool) *relayv1.PerforceSource {
		return &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync: []*relayv1.SyncEntry{
				{Path: "//s/x/...", Rev: "@100"},
				{Path: "//s/x/heavy/...", Rev: "", Exclude: exclude},
			},
		}
	}
	require.NotEqual(t, BaselineHash(mk(false), nil), BaselineHash(mk(true), nil))
}

// Which ENTRY carries the flag has to matter, not merely how many do. A marker
// written once for the whole entry block, or a sort that ignores the flag,
// passes the test above and fails this one.
func TestBaselineHash_WhichEntryIsExcludedChangesTheHash(t *testing.T) {
	a := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100", Exclude: true},
		{Path: "//s/x/b/...", Rev: "@100"},
	}}
	b := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100"},
		{Path: "//s/x/b/...", Rev: "@100", Exclude: true},
	}}
	require.NotEqual(t, BaselineHash(a, nil), BaselineHash(b, nil))
}

// Two entries sharing a path and a rev sort unstably against each other
// without the flag in the sort key, so the digest would depend on the order the
// two arrived in.
func TestBaselineHash_StableWhenAPathAppearsBothIncludedAndExcluded(t *testing.T) {
	a := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100"},
		{Path: "//s/x/a/...", Rev: "@100", Exclude: true},
	}}
	b := &relayv1.PerforceSource{Stream: "//s/x", Sync: []*relayv1.SyncEntry{
		{Path: "//s/x/a/...", Rev: "@100", Exclude: true},
		{Path: "//s/x/a/...", Rev: "@100"},
	}}
	require.Equal(t, BaselineHash(a, nil), BaselineHash(b, nil))
}
