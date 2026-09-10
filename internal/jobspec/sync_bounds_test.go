package jobspec

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// syncStream is the stream every fixture in this file is rooted at. Short on
// purpose: the fixtures run to 513 entries and every path is built from it.
const syncStream = "//s"

// syncIncludes returns n include entries under syncStream, each a DISTINCT,
// NON-NESTING path at #head.
//
// The distinctness is the point. No entry sits under another, so neither the
// coverage rule nor the swallow rule can fire and a refusal in an over-count
// case can only have come from the count. #head is the rev because it is the
// axis the bound is argued from: one p4 round trip per entry inside the task's
// own prepare phase, repeated on every attempt.
func syncIncludes(n int) []SyncEntry {
	out := make([]SyncEntry, n)
	for i := range out {
		out[i] = SyncEntry{Path: fmt.Sprintf("%s/d%04d/...", syncStream, i), Rev: "#head"}
	}
	return out
}

// syncSpecOf wraps entries in a one-task job spec rooted at syncStream, so the
// only rule any case in this file can trip is a source rule.
func syncSpecOf(entries []SyncEntry) *JobSpec {
	return &JobSpec{
		Name: "sync-bounds",
		Tasks: []TaskSpec{{
			Name:    "t",
			Command: []string{"true"},
			Source: &SourceSpec{
				Type:   "perforce",
				Stream: syncStream,
				Sync:   entries,
			},
		}},
	}
}

// syncOverMsg is the refusal, as Validate's per-task wrapper prefixes it.
//
// THE NUMBERS ARE LITERALS, NOT maxSyncEntries, ON PURPOSE. A test that spells
// the constant agrees with the implementation by construction and cannot detect
// the constant moving, which is the single most likely defect in a change that
// is one constant and one comparison.
const syncOverMsg = "task t: at most 512 sync entries are allowed, got 513"

// TestValidate_TheSyncEntryCountIsBoundedAtBothEnds pins the range whose lower
// end - "source.sync must have at least one sync entry" - already existed, and
// whose upper end this bound adds.
func TestValidate_TheSyncEntryCountIsBoundedAtBothEnds(t *testing.T) {
	t.Run("an empty sync list is refused", func(t *testing.T) {
		require.EqualError(t, Validate(syncSpecOf(nil)),
			"task t: source.sync must have at least one sync entry",
			"the lower end of the range, pinned in the same file as the upper end so an edit that "+
				"deletes either one is caught beside the other")
	})

	t.Run("one over the cap is rejected and the message reports the count", func(t *testing.T) {
		require.EqualError(t, Validate(syncSpecOf(syncIncludes(513))), syncOverMsg,
			"the message must STATE the limit and REPORT what arrived: it is what the boot sweep "+
				"writes into scheduled_jobs.last_error and what run-now answers with, so a caller who "+
				"generated the spec has to be able to read by how much to cut it with no context around it")
	})

	t.Run("exactly at the cap is accepted", func(t *testing.T) {
		require.NoError(t, Validate(syncSpecOf(syncIncludes(512))),
			"a spec AT the boundary must still be accepted - this is the leg an off-by-one written "+
				"as >= breaks, and nothing else in the tree catches it")
	})

	t.Run("an ordinary source spec still validates", func(t *testing.T) {
		// CONTROL. Without it, a validateSourceSpec that had started refusing
		// everything would pass every refusal case in this file. It is in this
		// package deliberately: the sibling control in internal/api cannot see a
		// break confined to this one.
		require.NoError(t, Validate(syncSpecOf([]SyncEntry{
			{Path: "//s/Engine/...", Rev: "#head"},
			{Path: "//s/Content/...", Rev: "@1200"},
			{Path: "//s/Content/Movies/...", Exclude: true},
		})), "control: a three-entry spec that is legal on every rule must stay legal")
	})
}

// TestValidate_TheSyncEntryCountIsCheckedBeforeEveryPerEntryRule pins the
// placement. In each case the spec violates a LATER rule as well, so the
// returned message is the only observable trace of which check ran first - and
// running first is the whole point, since each later rule is work this bound
// exists to avoid doing on an over-count spec.
func TestValidate_TheSyncEntryCountIsCheckedBeforeEveryPerEntryRule(t *testing.T) {
	t.Run("before the per-entry loop", func(t *testing.T) {
		// The FIRST entry carries an invalid rev, so the per-entry loop fails on
		// iteration zero. Below the loop, this returns the rev message. Above it,
		// the spec never pays 513 prefix checks, control-byte scans and regexp
		// matches.
		entries := syncIncludes(513)
		entries[0].Rev = "garbage"
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})

	t.Run("before the coverage loop", func(t *testing.T) {
		// 513 entries: 511 distinct includes, one include broadened to the whole
		// stream, and one exclusion that both the broad include and its own parent
		// include cover. Below the coverage loop this returns "covered by exactly
		// one included path, found 2". That loop is O(exclusions x len(Sync)) and
		// closing its second factor is the placement argument.
		entries := syncIncludes(512)
		entries[0].Path = syncStream + "/..."
		entries = append(entries, SyncEntry{Path: syncStream + "/d0001/y/...", Exclude: true})
		require.Len(t, entries, 513)
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})

	t.Run("before the exclusion count", func(t *testing.T) {
		// Over BOTH source bounds: 513 entries of which 17 are exclusions. The
		// entry count wins, deliberately - it is knowable without traversing
		// anything, and it is the more actionable message for a spec that is over
		// both. Transpose the two checks and this returns "at most 16 excluded
		// sync paths are allowed, got 17".
		entries := syncIncludes(496)
		for i := 0; i < 17; i++ {
			entries = append(entries, SyncEntry{
				Path: fmt.Sprintf("%s/d%04d/x/...", syncStream, i), Exclude: true,
			})
		}
		require.Len(t, entries, 513)
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})
}

// TestValidate_TheSyncEntryCountIsCheckedForEveryTask pins that the bound runs
// against each task's own source rather than one representative task.
//
// The over-count source is the SECOND task and the first is legal, so a loop
// that validates only spec.Tasks[0] returns nil here while every other case in
// this file stays green - every other Source-bearing fixture in the tree is
// single-task. The expected message names t2, so the same case also pins that
// the wrapper interpolates the task the refusal came from.
func TestValidate_TheSyncEntryCountIsCheckedForEveryTask(t *testing.T) {
	sourceTask := func(name string, n int) TaskSpec {
		return TaskSpec{
			Name:    name,
			Command: []string{"true"},
			Source: &SourceSpec{
				Type:   "perforce",
				Stream: syncStream,
				Sync:   syncIncludes(n),
			},
		}
	}
	spec := &JobSpec{
		Name:  "sync-bounds",
		Tasks: []TaskSpec{sourceTask("t1", 1), sourceTask("t2", 513)},
	}
	require.EqualError(t, Validate(spec),
		"task t2: at most 512 sync entries are allowed, got 513",
		"the bound is per source spec, so a job whose first task is legal must still be "+
			"refused on the task that is not, and named by it")
}

// TestValidate_TheSyncEntryCountCountsEveryEntry pins the AXIS: the bound counts
// entries, not includes and not #head entries. Both cases below are legal on
// every other rule, so an implementation that counts the wrong subset accepts
// them while every other case in this file stays green.
func TestValidate_TheSyncEntryCountCountsEveryEntry(t *testing.T) {
	t.Run("an exclusion counts toward the total", func(t *testing.T) {
		// 512 includes plus one exclusion covered by exactly one of them and
		// swallowing nothing: 513 entries, legal on every rule but this one. An
		// implementation counting only includes sees 512 and accepts.
		entries := append(syncIncludes(512),
			SyncEntry{Path: syncStream + "/d0000/x/...", Exclude: true})
		require.Len(t, entries, 513)
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})

	t.Run("a spec of pinned revisions is bounded too", func(t *testing.T) {
		// 513 entries, no #head anywhere. An implementation counting only the
		// entries that cost a p4 round trip sees 0 and accepts - which is what the
		// backlog item's own framing would have produced, and it would leave the
		// argv length and both BaselineHash sites unbounded.
		entries := syncIncludes(513)
		for i := range entries {
			entries[i].Rev = "@1200"
		}
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})
}
