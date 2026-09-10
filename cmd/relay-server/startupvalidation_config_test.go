package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"relay/internal/schedrunner"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultStartupValidationDeadlineIsAboveTheStatementTimeoutDefault pins the
// constant's VALUE in its own test, separately from the parser table below.
//
// THE TWO ASSERTIONS ARE SPLIT ON PURPOSE. A parser test that compares against
// defaultStartupValidationDeadline derives its expectation from part of its own
// subject, so the constant could move to 1ms with that table still green. This
// test is the half that pins the number; the table is the half that pins the
// behaviour.
func TestDefaultStartupValidationDeadlineIsAboveTheStatementTimeoutDefault(t *testing.T) {
	require.Equal(t, 45*time.Second, defaultStartupValidationDeadline)
	require.Greater(t, defaultStartupValidationDeadline, 30*time.Second,
		"the budget must sit above RELAY_DB_STATEMENT_TIMEOUT's 30s default: at or below it one slow "+
			"statement can consume the whole pass, and two knobs sharing a number read as coupled")
}

// TestParseStartupValidationDeadline pins the two-outcome contract as BEHAVIOUR.
// Whatever README says about what this parser refuses must be phrased as what
// this table pins, never written from memory.
//
// THE ZERO AND NEGATIVE ROWS ARE THE ONES THAT DISCRIMINATE. 0 is the value an
// operator reaches for as an off switch, and a parser that accepted it would
// restore the unbounded boot this whole slice exists to close while every other
// row here stayed green. The unset row is the second discriminator: a parser that
// warned on the ordinary path would put a line in every boot forever.
func TestParseStartupValidationDeadline(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    time.Duration
		msgPart string
	}{
		{"unset uses the default and says nothing", "", defaultStartupValidationDeadline, ""},
		{"a valid duration is used as-is, silently", "90s", 90 * time.Second, ""},
		{"the documented escape hatch is honoured", "24h", 24 * time.Hour, ""},
		{"zero is NOT an off switch: it folds to the default and warns", "0s", defaultStartupValidationDeadline, "is not a positive Go duration"},
		{"negative folds to the default and warns", "-5m", defaultStartupValidationDeadline, "is not a positive Go duration"},
		{"unparseable folds to the default and warns", "forty-five seconds", defaultStartupValidationDeadline, "is not a positive Go duration"},
		{"a bare integer is not a Go duration", "45", defaultStartupValidationDeadline, "is not a positive Go duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseStartupValidationDeadline("RELAY_STARTUP_VALIDATION_DEADLINE", tc.raw)
			require.Equal(t, tc.want, got)
			if tc.msgPart == "" {
				require.Empty(t, msg,
					"warning on the ordinary unset or sensible case is as wrong as staying silent on a typo")
				return
			}
			assert.Contains(t, msg, tc.msgPart)
			assert.Contains(t, msg, tc.raw,
				"a warning that does not name the ignored value leaves an operator believing they "+
					"tightened a bound they did not")
			assert.NotContains(t, msg, "disable",
				"there is no off token to offer, so no message may imply one")
		})
	}
}

// TestStartupValidationDeadlineLineNamesTheBoundAndItsConsequence.
func TestStartupValidationDeadlineLineNamesTheBoundAndItsConsequence(t *testing.T) {
	line := startupValidationDeadlineLine(45 * time.Second)
	assert.True(t, strings.HasPrefix(line, startupValidationPrefix),
		"every line of this pass carries one prefix, so one grep retrieves the whole block")
	assert.Contains(t, line, "45s")
	assert.Contains(t, line, "RELAY_STARTUP_VALIDATION_DEADLINE",
		"the bound is useless to an operator who cannot find the knob")
	assert.Contains(t, line, "only part of the enabled set",
		"an operator must learn from the bounds line that the bound costs coverage, before the pass runs")
}

// truncatedFixture is a result no other path in these tests can produce, so an
// assertion below cannot pass because the value it matched came from somewhere
// else. 901 and 3 are neither 0 nor 1 nor any digit of a duration used here.
func truncatedFixture() schedrunner.SweepResult {
	return schedrunner.SweepResult{Checked: 901, Invalid: 3, Truncated: true}
}

// TestStartupValidationLines_ATruncatedPassNeverReadsAsATotal is the guard for
// the item's headline defect: a partial count presented as a total.
//
// THE MUTATION IT EXISTS TO KILL is a formatter that falls through to the
// completed string for a truncated result - a single change that re-creates the
// defect. It asserts on words that DIFFER between the shapes rather than on the
// whole line, so rewording either one does not produce a false alarm.
//
// BOTH DURATIONS ARE ASSERTED WITH THE CLAUSE AROUND THEM, WHICH IS WHAT MAKES
// THE CHECK POSITIONAL. budget and elapsed are adjacent time.Duration parameters,
// so swapping them at the call site COMPILES, and a bare Contains on each value
// is green under the swap because BOTH values still appear in the line. The
// surrounding words are the only thing that says which slot each landed in; 45s
// against 47.123s then makes the swap visible. The two int counts are asserted
// the same way and for the same reason.
func TestStartupValidationLines_ATruncatedPassNeverReadsAsATotal(t *testing.T) {
	lines := startupValidationLines(truncatedFixture(), context.DeadlineExceeded,
		45*time.Second, 47123*time.Millisecond)

	require.Len(t, lines, 3,
		"the truncated outcome is three consecutive lines. One 500-character line is unreadable in a "+
			"boot log, and dropping a line drops a disclosure")
	for i, line := range lines {
		assert.True(t, strings.HasPrefix(line, startupValidationPrefix),
			"line %d does not carry the shared prefix, so an operator's grep finds the headline and "+
				"misses the caveat: %q", i, line)
	}
	joined := strings.Join(lines, "\n")

	// THE FIRST LINE MUST BE HONEST ON ITS OWN. This is what makes the split safe:
	// the count and the word that qualifies it are never on different lines.
	assert.Contains(t, lines[0], "checking 901 enabled schedules",
		"the checked count must appear, and in its own clause: without it an operator cannot tell "+
			"whether the budget is nearly enough or nowhere near")
	assert.Contains(t, lines[0], "3 of which no longer validate",
		"the invalid count must appear in its own clause; swapping the two counts must be visible")
	assert.Contains(t, lines[0], "FLOORS, NOT TOTALS",
		"THE SPLIT IS ONLY SAFE BECAUSE THIS IS ON THE SAME LINE AS THE COUNT. Move it to line 2 and "+
			"a reader who sees one line reads a floor as a total")
	assert.Contains(t, lines[0], "STOPPED AT ITS 45s DEADLINE",
		"the bound must be named so the operator can see how close the budget was, and it must be the "+
			"BUDGET in this clause: a bare Contains on 45s survives a swap with elapsed")
	assert.Contains(t, lines[0], "enabled schedules in 47.123s,",
		"the ELAPSED time must be named, in its own clause. A swap with the budget compiles and leaves "+
			"both values present in the line, so only the surrounding words can see it")
	assert.Contains(t, lines[0], "STOPPED AT ITS",
		"the word the completed shape does not contain")

	assert.NotContains(t, joined, "completed:",
		"THE MUTATION THIS KILLS: a formatter that falls through to the completed string for a "+
			"truncated result re-creates the defect the whole slice exists to close")
	assert.NotContains(t, joined, "DID NOT COMPLETE",
		"a fired deadline must not print the shutdown shape: testing err before Truncated does exactly "+
			"that, and then the remedy is never named")
	assert.Contains(t, joined, "size is unknown",
		"the remainder's SIZE must be stated as unknown - not zero, not estimated")
	assert.Contains(t, joined, "only ever adds them, never clears them",
		"the absence-versus-presence asymmetry is the only thing that keeps last_error readable after "+
			"a truncated boot")
	assert.Contains(t, joined, "RELAY_STARTUP_VALIDATION_DEADLINE")
	assert.Contains(t, joined, "RELAY_MAX_SCHEDULES_PER_OWNER")
	assert.Less(t, strings.Index(joined, "RELAY_MAX_SCHEDULES_PER_OWNER"),
		strings.Index(joined, "RELAY_STARTUP_VALIDATION_DEADLINE also works"),
		"the ladder is ordered tightening-first: the loosening remedy widens a boot delay the "+
			"population that caused the truncation can drive, so it must not come first")
	assert.Contains(t, joined, "from the beginning of the set",
		"where the remainder went (nowhere) and what the next boot does")
	for _, forbidden := range []string{"disable", "turn it off", "set it to 0"} {
		assert.NotContains(t, strings.ToLower(joined), forbidden,
			"there is no off token for this bound, so no line may offer one as a remedy")
	}
}

// TestStartupValidationLines_AShutdownDoesNotAdvertiseTheDeadline is the
// formatter half of the deadline-versus-shutdown distinction;
// TestValidateStoredSpecsOnStartup_AShutdownIsNotATruncation is the library half.
//
// THE DISCRIMINATING INPUT IS Truncated: false WITH A NON-NIL ERROR. That is
// exactly what a mid-boot SIGTERM produces, and the wrong implementation -
// Truncated derived from err != nil - cannot produce it at all.
func TestStartupValidationLines_AShutdownDoesNotAdvertiseTheDeadline(t *testing.T) {
	lines := startupValidationLines(
		schedrunner.SweepResult{Checked: 901, Invalid: 3, Truncated: false},
		context.Canceled, 45*time.Second, 2500*time.Millisecond)

	require.Len(t, lines, 1)
	line := lines[0]
	assert.Contains(t, line, startupValidationPrefix,
		"the shared phrase must be on this line too, or an operator's grep for the block misses the "+
			"one shape that says the pass did not finish")
	assert.Contains(t, line, "DID NOT COMPLETE")
	assert.Contains(t, line, "901")
	assert.Contains(t, line, "3 of which no longer validate")
	assert.Contains(t, line, "floors, not totals")
	assert.Contains(t, line, "context canceled",
		"the cause must be printed: this shape also covers a page-query fault, where the process "+
			"continues with partial coverage and the operator must know why")
	assert.NotContains(t, line, "DEADLINE",
		"THE MUTANT THIS KILLS advertises a deadline that did not fire and prescribes a knob that is "+
			"not the problem")
	assert.NotContains(t, line, "RELAY_STARTUP_VALIDATION_DEADLINE",
		"same: a shutdown must not send the operator to this knob")
	assert.NotContains(t, line, "completed:")
	assert.NotContains(t, line, "45s",
		"the budget is not why it stopped, so naming it would be a false attribution")
}

// TestStartupValidationLines_TheCompleteShapes covers the two non-error outcomes.
//
// THE ZERO CASE HAS ITS OWN SHAPE so an empty table does not read as a broken
// instrument: "0 enabled schedules checked" is what a sweep that never ran also
// prints.
func TestStartupValidationLines_TheCompleteShapes(t *testing.T) {
	full := startupValidationLines(
		schedrunner.SweepResult{Checked: 1432, Invalid: 3}, nil, 45*time.Second, 4100*time.Millisecond)
	require.Len(t, full, 1)
	assert.Contains(t, full[0], "completed: 1432 enabled schedules checked in 4.1s")
	assert.Contains(t, full[0], "3 of which no longer validate")
	assert.NotContains(t, full[0], "FLOORS",
		"a complete pass must not hedge: a caveat on every boot is a caveat nobody reads")
	assert.NotContains(t, full[0], "all ",
		"'completed', not 'all': the pass reads a moving table one page at a time")

	empty := startupValidationLines(schedrunner.SweepResult{}, nil, 45*time.Second, 12*time.Millisecond)
	require.Len(t, empty, 1)
	assert.Contains(t, empty[0], "no enabled schedules to check")
	assert.NotContains(t, empty[0], "0 enabled schedules checked",
		"the zero case must not read as a broken instrument")
}
