package api

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateJobSpec_Source_Perforce(t *testing.T) {
	valid := func() JobSpec {
		return JobSpec{
			Name: "j", Priority: "normal",
			Tasks: []TaskSpec{{
				Name: "t", Command: []string{"true"},
				Source: &SourceSpec{
					Type:   "perforce",
					Stream: "//streams/X/main",
					Sync: []SyncEntry{
						{Path: "//streams/X/main/...", Rev: "#head"},
					},
				},
			}},
		}
	}

	cases := []struct {
		name    string
		mutate  func(*JobSpec)
		wantErr string
	}{
		{"happy path", func(s *JobSpec) {}, ""},
		{"unsupported type", func(s *JobSpec) { s.Tasks[0].Source.Type = "git" }, "unsupported source type"},
		{"missing stream", func(s *JobSpec) { s.Tasks[0].Source.Stream = "" }, "stream is required"},
		{"stream not depot path", func(s *JobSpec) { s.Tasks[0].Source.Stream = "GameX" }, "stream must start with //"},
		{"empty sync", func(s *JobSpec) { s.Tasks[0].Source.Sync = nil }, "at least one sync entry"},
		{"sync path outside stream", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{{Path: "//other/depot/...", Rev: "#head"}}
		}, "must be under stream"},
		{"sync path not depot", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{{Path: "relative/path", Rev: "#head"}}
		}, "must start with //"},
		{"bad rev", func(s *JobSpec) {
			s.Tasks[0].Source.Sync[0].Rev = "garbage"
		}, "invalid rev"},
		{"good rev #head", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "#head" }, ""},
		{"good rev @cl", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "@12345" }, ""},
		{"good rev @label", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "@label-stable" }, ""},
		{"good rev #N", func(s *JobSpec) { s.Tasks[0].Source.Sync[0].Rev = "#42" }, ""},
		{"negative unshelve", func(s *JobSpec) { s.Tasks[0].Source.Unshelves = []int64{-1} }, "unshelve must be positive"},
		{"bad client_template", func(s *JobSpec) {
			tmpl := "has space"
			s.Tasks[0].Source.ClientTemplate = &tmpl
		}, "invalid client_template"},
		// CreateStreamClient places this value immediately after -t, so a
		// leading hyphen makes it read as a flag rather than as the flag's
		// value. relay owns that argument shape, so relay is what refuses it.
		{"client_template leading hyphen", func(s *JobSpec) {
			tmpl := "-x"
			s.Tasks[0].Source.ClientTemplate = &tmpl
		}, "invalid client_template"},
		{"client_template with an interior hyphen", func(s *JobSpec) {
			tmpl := "base-template"
			s.Tasks[0].Source.ClientTemplate = &tmpl
		}, ""},
		// --- exclusions ---
		{"exclusion happy path", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, ""},
		// The preempt's revision comes from the covering include and from
		// nothing else; a second revision here would name a different one, and a
		// preempt at the wrong revision fetches the excluded subtree BACKWARDS
		// rather than merely failing to exclude.
		{"exclusion carrying a revision", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Rev: "#head", Exclude: true},
			}
		}, "an excluded path carries no revision"},
		{"uncovered exclusion", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/Code/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 0"},
		{"exclusion covered twice at different revs", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "@100"},
				{Path: "//streams/X/main/Content/...", Rev: "@200"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 2"},
		// Two IDENTICAL literal revs, and still ambiguous: #head resolves per
		// path on the agent, so these two can land on different changelists and
		// the validator cannot see it.
		{"exclusion covered twice at the same literal rev", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 2"},
		{"exclusion equal to its include", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/Content/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/...", Exclude: true},
			}
		}, "leaves included path"},
		// The exclusion is BROADER than the second include, so that include has
		// nothing left. It is not covered BY that include, which is why the
		// swallow check runs against every include rather than the covering one.
		{"exclusion swallowing a narrower include", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/Movies/...", Rev: "#head"},
				{Path: "//streams/X/main/Content/...", Exclude: true},
			}
		}, "leaves included path"},
		{"sixteen exclusions is allowed", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = manySyncExclusions(16)
		}, ""},
		{"seventeen exclusions", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = manySyncExclusions(17)
		}, "at most 16 excluded sync paths are allowed, got 17"},
		// An exclusion is still a path under the stream. No new code enforces
		// this - the existing per-entry containment check already runs for every
		// entry - so the case pins that the exclusion branch did not skip past
		// it.
		{"exclusion outside the stream", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/...", Rev: "#head"},
				{Path: "//other/depot/...", Exclude: true},
			}
		}, "must be under stream"},
		// A sibling that shares a textual prefix but is not under the include.
		// TestToClientPath's sharesATextualPrefixButIsNotUnder row is the same
		// hazard one layer down; this is the discriminator for DepotPathCovers.
		{"exclusion under a sibling sharing a textual prefix", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = []SyncEntry{
				{Path: "//streams/X/main/Content/...", Rev: "#head"},
				{Path: "//streams/X/main/ContentExtra/Movies/...", Exclude: true},
			}
		}, "covered by exactly one included path, found 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := valid()
			tc.mutate(&spec)
			err := ValidateJobSpec(spec)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

// manySyncExclusions returns one include covering n distinct exclusions, so the
// only rule an over-count case can trip is the count itself: each exclusion is
// covered exactly once and swallows nothing.
func manySyncExclusions(n int) []SyncEntry {
	out := []SyncEntry{{Path: "//streams/X/main/...", Rev: "#head"}}
	for i := 0; i < n; i++ {
		out = append(out, SyncEntry{Path: fmt.Sprintf("//streams/X/main/d%02d/...", i), Exclude: true})
	}
	return out
}
