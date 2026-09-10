package main

import (
	"fmt"
	"time"

	"relay/internal/schedrunner"
)

// defaultStartupValidationDeadline bounds schedrunner.ValidateStoredSpecsOnStartup's
// pass over the enabled set.
//
// IT SITS DELIBERATELY ABOVE RELAY_DB_STATEMENT_TIMEOUT's default, and that is
// the load-bearing constraint rather than a round number. At or below the
// per-statement bound, one slow statement can consume the whole pass - and two
// knobs sharing a number read as coupled and get maintained as if they were.
//
// The other two constraints pull in opposite directions and are the operator's
// to resolve with the knob: short enough that an orchestrator's startup probe
// does not restart the process mid-pass, because a boot slow enough to be killed
// turns an incomplete diagnostic into a crash loop; and long enough that a
// healthy fleet is never truncated, so the truncation line means something when
// it appears.
const defaultStartupValidationDeadline = 45 * time.Second

// parseStartupValidationDeadline resolves RELAY_STARTUP_VALIDATION_DEADLINE into
// the budget handed to schedrunner.ValidateStoredSpecsOnStartup, plus a startup
// message to log, empty when there is nothing to say. Two outcomes:
//
//   - Unset, or a valid positive Go duration: used as-is, silently.
//   - Zero, negative or unparseable: the default is used and the message names
//     the ignored value. A silently-ignored typo would leave an operator
//     believing they had tightened a bound they had not.
//
// NO ZERO-DISABLES ARM, and this diverges from parseWatchdogDuration next door.
// That one accepts 0 because disabling a control that TERMINATES A USER'S WORK is
// a legitimate operational choice with real consequences either way. This control
// terminates nothing; the only thing an off token buys is the unbounded boot the
// bound exists to close, and an operator who genuinely wants a complete pass at
// any cost writes 24h, which stays visible as a number in the environment and in
// the startup line.
//
// AND NO FLOOR ARM, which diverges from parseTrailingLogWindow as well. THE
// DISTINGUISHING QUESTION IS WHETHER THE FAIL-AGGRESSIVE DIRECTION IS SILENT,
// not whether the value is small: a rejected log chunk produces no error and no
// line, and an aggressive watchdog destroys work, so both of those warn below a
// floor. A one-second budget here announces itself on every boot in a line naming
// the budget and the counts, so a floor would be a constant nobody can justify
// guarding a failure mode that is already loud.
//
// Deliberately not a log.Fatalf, following parseScheduleCap: a bad duration must
// not stop a server booting when a safe default exists.
func parseStartupValidationDeadline(name, raw string) (time.Duration, string) {
	if raw == "" {
		return defaultStartupValidationDeadline, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultStartupValidationDeadline, fmt.Sprintf(
			"%s=%q is not a positive Go duration; using %s", name, raw, defaultStartupValidationDeadline)
	}
	return d, ""
}

// startupValidationPrefix appears on every line this pass emits, including the
// warning shape, so one grep retrieves the whole block rather than its headline.
const startupValidationPrefix = "schedrunner: startup validation"

// startupValidationDeadlineLine renders the unconditional bounds line, in the
// shape of watchdogBoundsLine and scheduleCapLine.
//
// ITS VALUE IS ITS ADJACENCY to the pass it bounds. The boot log between the
// dispatcher and "HTTP listening on" is otherwise silent - the reconcile prints
// nothing - so an operator watching a slow boot sees nothing at all during the
// window this knob governs.
func startupValidationDeadlineLine(budget time.Duration) string {
	return fmt.Sprintf(
		"%s bounded at %s (RELAY_STARTUP_VALIDATION_DEADLINE). A pass that runs out of budget checks "+
			"only part of the enabled set; the line after it says which.", startupValidationPrefix, budget)
}

// startupValidationLines renders the sweep's outcome. ONE FORMATTER OWNS ALL FOUR
// SHAPES, which is what makes it impossible for a caller to print a count without
// the sentence saying whether it is a total.
//
// THE ORDER OF THE CASES IS PART OF THE CONTRACT. A truncated pass ALSO returns a
// non-nil error, so testing err first would print the shutdown shape for a fired
// deadline and the remedy would never be named.
//
// THE COUNT AND THE WORDS "FLOORS, NOT TOTALS" SHARE THE FIRST LINE, and the
// split into three is allowed only because they do: a reader who sees only the
// first line still gets the caveat that makes the number honest, and lines two
// and three add detail rather than the caveat.
//
// THE REMEDY LADDER IS ORDERED TIGHTENING-FIRST, not cheapest-first. The quantity
// that drives truncation is the enabled schedule count, which any authenticated
// user grows, so "raise the deadline" is a remedy that WIDENS a boot delay a
// careless or hostile population can drive; it goes second with its cost stated
// inline. There is no disabling option anywhere in the ladder because there is no
// off token to offer.
func startupValidationLines(res schedrunner.SweepResult, err error, budget, elapsed time.Duration) []string {
	switch {
	case res.Truncated:
		return []string{
			fmt.Sprintf("%s STOPPED AT ITS %s DEADLINE after checking %d enabled schedules in %s, "+
				"%d of which no longer validate. THESE ARE FLOORS, NOT TOTALS.",
				startupValidationPrefix, budget, res.Checked, elapsed.Round(time.Millisecond), res.Invalid),
			fmt.Sprintf("%s: the rest of the enabled set was not checked on this boot and its size is "+
				"unknown, so a schedule carrying no recorded failure may simply never have been looked "+
				"at. Recorded failures are still trustworthy - this pass only ever adds them, never "+
				"clears them.", startupValidationPrefix),
			fmt.Sprintf("%s: to get a complete pass, first reduce the enabled set "+
				"(RELAY_MAX_SCHEDULES_PER_OWNER bounds it per owner, not per fleet); raising "+
				"RELAY_STARTUP_VALIDATION_DEADLINE also works and costs exactly that much more boot time "+
				"before the HTTP API answers. The next boot starts again from the beginning of the set, "+
				"not from here.", startupValidationPrefix),
		}
	case err != nil:
		// SHUTDOWN AND A PAGE-QUERY FAULT SHARE ONE SHAPE rather than getting a
		// third and fourth vocabulary. A mid-boot SIGTERM means nobody will read
		// that boot's last_error; a page-query fault means the process IS
		// continuing with partial coverage. The shared shape costs nothing on the
		// first and is required by the second. It does not mention the deadline,
		// because the deadline is not why it stopped.
		return []string{fmt.Sprintf(
			"warn: %s DID NOT COMPLETE after checking %d enabled schedules, %d of which no longer "+
				"validate (floors, not totals - the rest of the enabled set was not checked): %v",
			startupValidationPrefix, res.Checked, res.Invalid, err)}
	case res.Checked == 0:
		// So an empty table does not read as a broken instrument.
		return []string{fmt.Sprintf("%s completed: no enabled schedules to check.", startupValidationPrefix)}
	default:
		// "completed", not "all": the pass reads a moving table one page at a time
		// and ListEnabledScheduledJobsPage's comment enumerates which concurrent
		// writes it can miss.
		return []string{fmt.Sprintf("%s completed: %d enabled schedules checked in %s, %d of which no "+
			"longer validate.", startupValidationPrefix, res.Checked, elapsed.Round(time.Millisecond),
			res.Invalid)}
	}
}
