package main

import (
	"fmt"
	"strconv"

	"relay/internal/worker"
)

// parseAutoEnrollCeiling resolves RELAY_AUTO_ENROLL_WORKER_CEILING. Same
// three-outcome contract as parseConnLimit and parseWatchdogDuration, and
// deliberately not a log.Fatalf: a bad limit must not stop a server booting when
// a safe default exists.
//
//   - Unset, or a valid positive integer: used as-is, silently.
//   - Exactly zero: ACCEPTED, and the ceiling is disabled. Because disabling a
//     bound must never be silent, this returns an informational line naming what
//     is now unbounded.
//   - Negative or unparseable: the default is used and the message says so. A
//     silently-ignored typo would leave an operator believing they had tightened
//     a bound they had not.
func parseAutoEnrollCeiling(name, raw string) (int, string) {
	if raw == "" {
		return worker.DefaultAutoEnrollWorkerCeiling, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return worker.DefaultAutoEnrollWorkerCeiling, fmt.Sprintf(
			"%s=%q is not a non-negative integer; using %d", name, raw, worker.DefaultAutoEnrollWorkerCeiling)
	}
	if n == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: the token-less auto-enroll worker ceiling is disabled. A caller that can reach the "+
				"gRPC port creates one permanent workers row per distinct hostname, with no bound.", name, raw)
	}
	return n, ""
}

// autoEnrollCeilingLine renders the unconditional startup line, in the shape of
// watchdogBoundsLine and grpcBoundsLine.
//
// IT TAKES maxConns BECAUSE THIS IS THE ONE PLACE BOTH NUMBERS ARE IN HAND. The
// published overshoot is ceiling + RELAY_GRPC_MAX_CONNS - two concurrent
// auto-enrolls at the boundary both pass under read-committed - and that
// expression HAS NO FINITE VALUE when the connection cap is disabled, which is a
// supported configuration for an operator fronting :9090 with a proxy that caps
// connections itself. Printing the formula there would state a bound that does
// not exist, so the line says so instead. README cannot: it has no access to the
// deployment's actual cap.
func autoEnrollCeilingLine(ceiling int, allowAutoEnroll bool, maxConns int) string {
	if !allowAutoEnroll {
		return "auto-enroll: disabled (RELAY_ALLOW_AUTO_ENROLL is not set), so the worker ceiling is moot"
	}
	if ceiling <= 0 {
		return "auto-enroll: ENABLED with no bound on worker-row creation. Every distinct hostname a " +
			"caller presents creates one permanent row."
	}
	overshoot := fmt.Sprintf("at most %d in flight, so the honest bound is %d", maxConns, ceiling+maxConns)
	if maxConns <= 0 {
		overshoot = "RELAY_GRPC_MAX_CONNS=0, so the number of concurrent enrolls is not bounded by relay " +
			"and neither is the overshoot"
	}
	return fmt.Sprintf(
		"auto-enroll: ENABLED, refusing token-less enrollment at %d non-revoked workers (approximate: two "+
			"concurrent enrolls at the boundary can both pass; %s). "+
			"Revoke unused workers to free budget - note that revoking frees BUDGET but not the HOSTNAME. "+
			"Enrollment tokens are never refused by this ceiling.",
		ceiling, overshoot)
}
