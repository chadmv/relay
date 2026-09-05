package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/api"
)

// TestParseScheduleCap pins the three-outcome contract as BEHAVIOUR. Whatever
// README says about what the parser refuses must be phrased as what this table
// pins, never written from memory.
//
// THE ZERO ROW IS FIRST. A poisoned input placed after its target is read by
// neither the code nor the mutant: with 0 last, a mutant that returns early on
// the first row never reaches it.
func TestParseScheduleCap(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		msgPart string
	}{
		{"zero is NOT an off switch: it folds to the default and warns", "0", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"negative folds to the default and warns", "-1", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"unparseable folds to the default and warns", "abc", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"unset uses the default and says nothing", "", api.DefaultMaxSchedulesPerOwner, ""},
		{"a positive value is used as-is, silently", "7", 7, ""},
		{"a very large value is used as-is: that is the spelling for effectively-unbounded", "9999999999", 9999999999, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseScheduleCap("RELAY_MAX_SCHEDULES_PER_OWNER", tc.raw)
			require.Equal(t, tc.want, got)
			if tc.msgPart == "" {
				assert.Empty(t, msg)
				return
			}
			assert.Contains(t, msg, tc.msgPart)
			assert.Contains(t, msg, tc.raw,
				"a warning that does not name the ignored value leaves an operator believing they "+
					"tightened a bound they did not")
		})
	}
}

// TestScheduleCapLineIsUnconditionalAndNamesGrandfathering. An operator
// upgrading into a new refusal needs to see the number and the retroactivity
// without reading release notes.
func TestScheduleCapLineIsUnconditionalAndNamesGrandfathering(t *testing.T) {
	line := scheduleCapLine(100)
	assert.Contains(t, line, "100")
	assert.Contains(t, line, "keep",
		"the line must say existing owners over the cap KEEP their schedules; an operator who reads "+
			"this as a deletion has no way to find out otherwise before the deploy")
	assert.Contains(t, line, "per owner",
		"the line must not let an operator read the number as a fleet ceiling")
}
