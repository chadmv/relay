package perforce

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

func pfWith(entries ...*relayv1.SyncEntry) *relayv1.PerforceSource {
	return &relayv1.PerforceSource{Stream: "//s/x", Sync: entries}
}

// A task with NO exclusions keeps today's key BYTE FOR BYTE. Every existing
// registry row, every worker_workspaces row and every allocated short_id stays
// valid only because of this, which is why the assertion is an equality against
// the literal stream rather than a round trip through anything.
func TestSourceKey_NoExclusionsIsTheBareStream(t *testing.T) {
	require.Equal(t, "//s/x", SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Rev: "@100"},
	)))
	require.Equal(t, "", SourceKey(nil))
}

func TestSourceKey_AnExclusionMakesADistinctCompositeKey(t *testing.T) {
	bare := SourceKey(pfWith(&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"}))
	comp := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/heavy/...", Exclude: true},
	))
	require.NotEqual(t, bare, comp)
	// The composite can never collide with a bare stream, because
	// validateSourceSpec requires a stream to start with // and no legal stream
	// starts with x1|. A collision would put a task into another task's
	// workspace, which is the poisoning hazard by a second route.
	require.True(t, strings.HasPrefix(comp, "x1|"))
	require.True(t, strings.HasSuffix(comp, "|//s/x"))
}

func TestSourceKey_OrderAndDuplicatesCanonicalise(t *testing.T) {
	a := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/b/...", Exclude: true},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true},
	))
	b := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/...", Rev: "#head"},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true},
		&relayv1.SyncEntry{Path: "//s/x/b/...", Exclude: true},
		&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true},
	))
	require.Equal(t, a, b, "order and duplicates must canonicalise to one workspace")
}

func TestSourceKey_DifferentExclusionSetsAreDifferentWorkspaces(t *testing.T) {
	a := SourceKey(pfWith(&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true}))
	b := SourceKey(pfWith(&relayv1.SyncEntry{Path: "//s/x/b/...", Exclude: true}))
	require.NotEqual(t, a, b)
}

// THE DISCRIMINATING INPUT for the separator: one entry whose path is the
// CONCATENATION of the other set's two paths, so the two canonical forms differ
// only by where the boundary falls. Drop the terminator between entries and the
// two sets hash to one value, which puts two different exclusion sets into one
// workspace. The pair is synthetic - neither would survive
// validateSourceSpec's containment rule - because the property under test is
// the key function's own encoding, and this is the input that isolates it.
func TestSourceKey_ASetBoundaryIsPartOfTheEncoding(t *testing.T) {
	two := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/a/...", Exclude: true},
		&relayv1.SyncEntry{Path: "//s/x/b/...", Exclude: true},
	))
	one := SourceKey(pfWith(
		&relayv1.SyncEntry{Path: "//s/x/a/...//s/x/b/...", Exclude: true},
	))
	require.NotEqual(t, two, one)
}

// worker_workspaces.source_key is TEXT inside PRIMARY KEY (worker_id,
// source_type, source_key) and inside worker_workspaces_lookup_idx, and nothing
// on the registration-time bulk ingest bounds its length - an over-long value
// fails the whole inventory transaction rather than one row
// (idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key).
// This design stays clear of that by construction, and "by construction" is a
// property of the function below, not of the schema: a canonicalisation that
// inlined the paths would reintroduce it silently. Sixteen maximum-length depot
// paths would exceed Postgres's btree index-row limit; twenty bytes cannot.
func TestSourceKey_IsBoundedAtTwentyBytesOverTheStream(t *testing.T) {
	long := make([]*relayv1.SyncEntry, 0, 16)
	for i := 0; i < 16; i++ {
		long = append(long, &relayv1.SyncEntry{
			Path:    "//s/x/" + strings.Repeat("d", 200) + string(rune('a'+i)) + "/...",
			Exclude: true,
		})
	}
	require.Len(t, SourceKey(pfWith(long...)), len("//s/x")+20)
}
