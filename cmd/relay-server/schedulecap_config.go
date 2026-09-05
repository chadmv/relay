package main

import (
	"fmt"
	"strconv"

	"relay/internal/api"
)

// parseScheduleCap resolves RELAY_MAX_SCHEDULES_PER_OWNER. Same three-outcome
// shape as parseAutoEnrollCeiling, parseConnLimit and parseWatchdogDuration, and
// deliberately not a log.Fatalf: a typo must not stop a farm booting when a safe
// default exists. The rate-limit family fatals; this is not in that family.
//
//   - Unset, or a valid integer >= 1: used as-is, silently.
//   - 0, negative or unparseable: the default is used and the message names the
//     ignored value.
//
// THE ZERO ARM IS WHERE THIS DIVERGES FROM parseAutoEnrollCeiling, and the
// divergence has a reason. That ceiling gates a path with a non-refused
// alternative - enrollment tokens are never refused by it - so an operator on a
// trusted network can legitimately turn it off. This gates the only route that
// creates a scheduled job. There is no off token and no value that disables the
// check; a very large number is the spelling for effectively-unbounded, which
// differs from an off switch in the way that matters, because it stays visible
// as a number in the environment and in the startup line. TestParseScheduleCap
// pins every arm.
func parseScheduleCap(name, raw string) (int, string) {
	if raw == "" {
		return api.DefaultMaxSchedulesPerOwner, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return api.DefaultMaxSchedulesPerOwner, fmt.Sprintf(
			"%s=%q is not a positive integer; using %d", name, raw, api.DefaultMaxSchedulesPerOwner)
	}
	return n, ""
}

// scheduleCapLine renders the unconditional startup line, in the shape of
// autoEnrollCeilingLine and watchdogBoundsLine.
//
// IT NAMES GRANDFATHERING because that is the half an operator cannot discover
// from the number: the cap does not shrink an existing table by one row, so on
// the deploy that lands it the boot sweep's work set is exactly what it was the
// day before.
func scheduleCapLine(n int) string {
	return fmt.Sprintf(
		"scheduled jobs: refusing creation at %d schedules per owner. Owners already over it keep "+
			"every schedule and are refused only new ones. The bound is per owner and not a fleet "+
			"ceiling, so M accounts hold M x %d.", n, n)
}
