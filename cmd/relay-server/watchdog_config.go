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
//
// Deliberately not a log.Fatalf, following parseTrailingLogWindow: a bad
// duration must not stop a server booting when a safe default exists.
func parseWatchdogDuration(name, raw string, def time.Duration) (time.Duration, string) {
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
	return d, ""
}
