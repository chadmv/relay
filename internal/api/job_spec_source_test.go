package api

import (
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
		// value. Today p4's own parser rejects that with a usage error, which
		// means the safety is p4's and not relay's - and this branch moved that
		// argument onto a call relay makes on the cold path of every stream.
		{"client_template leading hyphen", func(s *JobSpec) {
			tmpl := "-x"
			s.Tasks[0].Source.ClientTemplate = &tmpl
		}, "invalid client_template"},
		{"client_template with an interior hyphen", func(s *JobSpec) {
			tmpl := "base-template"
			s.Tasks[0].Source.ClientTemplate = &tmpl
		}, ""},
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
