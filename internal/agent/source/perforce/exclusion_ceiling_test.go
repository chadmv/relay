package perforce

import (
	"fmt"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// envOnly returns a getenv that answers only for the ceiling variable, so a
// table row cannot pass because of something else in the real environment.
func envOnly(value string) func(string) string {
	return func(k string) string {
		if k == maxExclusionSetsEnv {
			return value
		}
		return ""
	}
}

// THE ZERO ROW IS WHY THIS TABLE EXISTS. Its two neighbours in README's agent
// table mean "disabled" at zero, so the text is asserted, not just the number:
// a resolver that returned the default silently would leave an operator
// believing the control is off.
func TestResolveMaxExclusionSets(t *testing.T) {
	cases := []struct {
		name        string
		value       string
		want        int
		wantWarning bool
		wantText    string
	}{
		{"unset yields the default and says nothing", "", defaultMaxExclusionSets, false, ""},
		{"a value inside the range is used verbatim", "8", 8, false, ""},
		{"the maximum itself is used verbatim", "64", 64, false, ""},
		{"zero does not disable it", "0", defaultMaxExclusionSets, true, "0 does not disable this ceiling"},
		{"a negative value does not disable it", "-1", defaultMaxExclusionSets, true, "0 does not disable this ceiling"},
		{"an unparseable value falls back", "abc", defaultMaxExclusionSets, true, "cannot be switched off"},
		{"above the maximum clamps", "1000", maxMaxExclusionSets, true, "clamped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings := resolveMaxExclusionSets(envOnly(tc.value))
			require.Equal(t, tc.want, got)
			if !tc.wantWarning {
				require.Empty(t, warnings, "a value used verbatim must not warn")
				return
			}
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0], tc.wantText)
			require.Contains(t, warnings[0], maxExclusionSetsEnv,
				"a warning an operator cannot trace to a variable is noise")
		})
	}
}

// The ceiling is resolved in New, inside the package. A Config field would
// default to zero, and a zero here must never mean unlimited, so an unwired
// main.go has to fail CLOSED.
func TestNew_ResolvesTheCeilingInsideThePackage(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "7")
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
	require.Equal(t, 7, p.maxExclusionSets)
}

// THE CONTROL, and the one that matters. With nothing wired anywhere, the
// ceiling is still in force at its default.
func TestNew_AnUnsetEnvironmentStillCarriesTheCeiling(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "")
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
	require.Equal(t, defaultMaxExclusionSets, p.maxExclusionSets)
}

// A Provider whose field is zero must use the DEFAULT, never "unlimited" and
// never "refuse everything": a bare `count >= 0` comparison refuses every cold
// exclusion prepare, which is fail-closed and still a catastrophe.
func TestExclusionCeiling_AZeroFieldMeansTheDefault(t *testing.T) {
	require.Equal(t, defaultMaxExclusionSets, (&Provider{}).exclusionCeiling())
	require.Equal(t, 9, (&Provider{maxExclusionSets: 9}).exclusionCeiling())
}

// entryFor builds a registry entry for one distinct exclusion set on stream,
// so each call yields a distinct composite source key.
func entryFor(t *testing.T, stream, tag, baseline string, lastUsed time.Time) WorkspaceEntry {
	t.Helper()
	key := SourceKey(&relayv1.PerforceSource{
		Stream: stream,
		Sync: []*relayv1.SyncEntry{
			{Path: stream + "/...", Rev: "@100"},
			{Path: fmt.Sprintf("%s/%s/...", stream, tag), Exclude: true},
		},
	})
	return WorkspaceEntry{
		ShortID:      "id-" + tag,
		SourceKey:    key,
		ClientName:   "relay_h_id-" + tag,
		BaselineHash: baseline,
		LastUsedAt:   lastUsed,
	}
}

// AN UNSYNCED ENTRY IS THE VICTIM EVEN WHEN IT IS THE NEWER ONE. The
// discriminating input is therefore an empty-baseline entry with the NEWER
// timestamp against a synced entry with the older one: under pure LRU the synced
// one is picked, which reclaims a workspace an operator paid to fill in order to
// protect a failed prepare's residue.
func TestExclusionEvictionCandidates_AnUnsyncedEntryOutranksAnOlderSyncedOne(t *testing.T) {
	now := time.Now()
	synced := entryFor(t, "//s/x", "synced", "bh-real", now.Add(-10*time.Hour))
	unsynced := entryFor(t, "//s/x", "unsynced", "", now.Add(-1*time.Hour))

	got := exclusionEvictionCandidates([]WorkspaceEntry{synced, unsynced}, "//s/x")

	require.Len(t, got, 2)
	require.Equal(t, unsynced.ShortID, got[0].ShortID,
		"the residue of a failed prepare is reclaimed before any warm workspace")
}

// Within one baseline class the order is LRU.
func TestExclusionEvictionCandidates_OrdersBySyncedThenLeastRecentlyUsed(t *testing.T) {
	now := time.Now()
	newest := entryFor(t, "//s/x", "c", "bh-real", now.Add(-1*time.Hour))
	oldest := entryFor(t, "//s/x", "a", "bh-real", now.Add(-9*time.Hour))
	middle := entryFor(t, "//s/x", "b", "bh-real", now.Add(-5*time.Hour))

	got := exclusionEvictionCandidates([]WorkspaceEntry{newest, oldest, middle}, "//s/x")

	require.Equal(t,
		[]string{oldest.ShortID, middle.ShortID, newest.ShortID},
		[]string{got[0].ShortID, got[1].ShortID, got[2].ShortID})
}

// THE DECOY GOES FIRST. The base entry is made the OLDEST and given a non-empty
// baseline, so it sorts ahead of every composite under either ordering arm. A
// list that can hold it hands a job author the outcome the ceiling exists to
// deny: destroying the workspace every non-exclusion task on that stream shares.
func TestExclusionEvictionCandidates_ExcludesTheBaseWorkspaceAndOtherStreams(t *testing.T) {
	now := time.Now()
	base := WorkspaceEntry{
		ShortID: "id-base", SourceKey: "//s/x", ClientName: "relay_h_id-base",
		BaselineHash: "bh-base", LastUsedAt: now.Add(-100 * time.Hour),
	}
	otherStream := entryFor(t, "//s/other", "z", "bh-real", now.Add(-99*time.Hour))
	mine := entryFor(t, "//s/x", "a", "bh-real", now)

	got := exclusionEvictionCandidates([]WorkspaceEntry{base, otherStream, mine}, "//s/x")

	require.Len(t, got, 1, "only this stream's exclusion-derived entries are candidates")
	require.Equal(t, mine.ShortID, got[0].ShortID)
}
