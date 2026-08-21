package main

import (
	"fmt"
	"time"
)

// parseWatchdogDuration resolves one of the stale-task watchdog's two bounds
// into the duration handed to scheduler.NewWatchdog, plus a startup message to
// log, empty when there is nothing to say. Three outcomes, not two, which is why
// the second return is a message and not an ok bool:
//
//   - Unset, or a valid positive duration: used as-is, silently.
//   - Exactly zero: ACCEPTED, and the arm is disabled. `0` means "this arm is
//     off" in BOTH watchdog variables - one rule, no exceptions. An operator who
//     genuinely wants no margin writes `1s`; giving the same literal two meanings
//     across two adjacent knobs would be a footgun. Because disabling a safety
//     bound must never be silent, this returns an informational line naming what
//     is now unbounded.
//   - Unparseable or negative: the default is used and the message says so. A
//     silently-ignored typo would leave an operator believing they had tightened
//     a bound they had not.
//   - Positive but below `floor`: KEPT, and warned about, exactly as
//     parseTrailingLogWindow treats a sub-2m window. This is the
//     fail-AGGRESSIVE direction and it is the one that destroys work rather than
//     failing to protect it: `RELAY_TASK_MAX_ASSIGNMENT=24m` from an operator who
//     meant `24h` parses clean, and within one 60s tick marks EVERY assigned task
//     in the fleet timed_out and cascades each one's transitive dependent closure
//     to failed, with no automatic retry. Units confusion is likelier than a typo
//     and is the only failure mode here that destroys work rather than being a
//     no-op, so the message names the units explicitly.
//
// The floor also removes a second, quieter hazard rather than leaving a separate
// guard for it: the watchdog binds the margin as `int64(margin / time.Second)`,
// so a sub-second value truncates to 0 while `execEnabled := margin > 0` still
// reads true - an execution arm that is enabled and compares against a zero
// margin. Any floor at or above a second makes that unreachable.
//
// Deliberately not a log.Fatalf, following parseTrailingLogWindow: a bad
// duration must not stop a server booting when a safe default exists.
func parseWatchdogDuration(name, raw string, def, floor time.Duration) (time.Duration, string) {
	if raw == "" {
		return def, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return def, fmt.Sprintf("%s=%q is not a Go duration (or is negative); using %s", name, raw, def)
	}
	if d == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: this arm of the stale-task watchdog is disabled. Tasks it would have bounded can now hold "+
				"an assignment indefinitely. Use 1s, not 0s, if you meant `no margin` rather than `no bound`.",
			name, raw)
	}
	if d < floor {
		return d, fmt.Sprintf(
			"%s=%q resolves to %s, below the %s floor below which this bound is far likelier to be a units "+
				"mistake than an intent. Using it anyway, but the watchdog will end assignments aggressively: a "+
				"swept task is marked timed_out, its transitive dependents are cascaded to failed, and NOTHING is "+
				"retried automatically. Check the units (%s, not %s?).",
			name, raw, d, floor, def, d)
	}
	return d, ""
}

// minWatchdogMarginDur is the floor for RELAY_TASK_WATCHDOG_MARGIN. The margin
// must absorb the whole gap between the agent's own deadline firing and the
// coordinator seeing the terminal update - subprocess kill, proctree cleanup,
// final log flush, and a gRPC reconnect if the stream dropped - which README's
// analysis puts at roughly 105s. 2m is the smallest round number above that, and
// it is deliberately the same number as minSaneTrailingLogWindow next door:
// both answer "how late may a legitimate agent still be".
const minWatchdogMarginDur = 2 * time.Minute

// minMaxAssignmentDur is the floor for RELAY_TASK_MAX_ASSIGNMENT, and it is NOT
// the margin's floor - an hour is a perfectly sane margin and a fleet-destroying
// absolute cap. This bound has to exceed the longest LEGITIMATE assignment, which
// is dominated by a P4 sync on a 1 TB+ workspace plus the task's own run; an hour
// is already implausibly tight for that, so anything below it is almost certainly
// `24m` for `24h`.
const minMaxAssignmentDur = time.Hour

// watchdogBoundsLine renders the unconditional startup line naming both effective
// bounds. A mechanism that can terminate a user's work should state its limits at
// every boot - and the ordinary path is otherwise completely silent, so an
// operator cannot tell from the log whether the watchdog is running, what it will
// do, or that somebody switched it off.
func watchdogBoundsLine(margin, maxAssignment time.Duration) string {
	switch {
	case margin <= 0 && maxAssignment <= 0:
		return "stale-task watchdog: BOTH arms disabled - no coordinator-side bound on task duration. " +
			"A connected agent can hold a task, its worker slot and its job indefinitely."
	case margin <= 0:
		return fmt.Sprintf("stale-task watchdog: execution arm disabled (a task's own timeout_sec is enforced only "+
			"by the agent); absolute cap %s from dispatch", maxAssignment)
	case maxAssignment <= 0:
		return fmt.Sprintf("stale-task watchdog: execution arm timeout_sec + %s; absolute cap disabled, so a task "+
			"with timeout_sec=0 or one that never reports running is unbounded", margin)
	default:
		return fmt.Sprintf("stale-task watchdog: execution arm timeout_sec + %s; absolute cap %s from dispatch",
			margin, maxAssignment)
	}
}
