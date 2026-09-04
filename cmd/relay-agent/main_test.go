package main

import (
	"bytes"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDurationEnv(t *testing.T) {
	require.Equal(t, 14*24*time.Hour, parseDurationEnv("SOME_VAR", "14d", 0))
	require.Equal(t, 5*time.Minute, parseDurationEnv("SOME_VAR", "5m", 0))
	require.Equal(t, 30*time.Second, parseDurationEnv("SOME_VAR", "30s", 0))
	require.Equal(t, time.Hour, parseDurationEnv("SOME_VAR", "garbage", time.Hour))
	require.Equal(t, time.Duration(0), parseDurationEnv("SOME_VAR", "", 0))
}

func TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	result := parseDurationEnv("RELAY_WORKSPACE_MAX_AGE", "7days", 0)

	require.Equal(t, time.Duration(0), result, "should return fallback on invalid input")
	require.Contains(t, buf.String(), "RELAY_WORKSPACE_MAX_AGE", "warning should name the env var")
	require.Contains(t, buf.String(), "7days", "warning should echo the bad value")
}

// The property: a value inside the regex but outside int64 nanoseconds takes the
// fallback and warns. The rows discriminate because each wraps DIFFERENTLY - one
// to a negative, one to a plausible-looking positive - so no check on the
// returned product could separate them from a deliberate setting.
func TestParseDurationEnv_AnOverflowingValueIsRefusedRatherThanWrappedNegative(t *testing.T) {
	rows := []struct{ name, in string }{
		// 1e10 seconds is ~1.0e19 ns against an int64 ceiling of ~9.22e18.
		{"seconds_overflow", "10000000000s"},
		{"days_overflow", "1000000000000d"},
		// Past Atoi's own range, so the discarded error is the first thing wrong.
		{"beyond_int64_digits", "99999999999999999999999s"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			got := parseDurationEnv("RELAY_WORKSPACE_MAX_AGE", row.in, time.Hour)

			require.Equal(t, time.Hour, got, "an unrepresentable value must take the fallback")
			require.Contains(t, buf.String(), "RELAY_WORKSPACE_MAX_AGE")
			require.Contains(t, buf.String(), row.in, "the warning must echo the bad value")
		})
	}
}

// The boundary the check above must not swallow: a legitimate zero is how every
// caller spells "disabled", and it is also what a wrapped multiply can look
// like, so the two are separated by the operand rather than by the product.
func TestParseDurationEnv_AnExplicitZeroIsStillZero(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	for _, in := range []string{"0s", "0m", "0h", "0d", "00s"} {
		require.Equal(t, time.Duration(0), parseDurationEnv("SOME_VAR", in, time.Hour), "input %q", in)
	}
	require.Empty(t, buf.String(), "a representable value must not warn")
}

func TestParseDurationEnv_NoWarningOnEmptyInput(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	result := parseDurationEnv("RELAY_WORKSPACE_MAX_AGE", "", 0)

	require.Equal(t, time.Duration(0), result)
	require.Empty(t, buf.String(), "empty env var should not produce a warning")
}

// Covers the PARSING only. The assignment of the result into the
// perforce.Config literal in main() is not covered here
// (docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md).
func TestResolveSyncHeartbeatInterval(t *testing.T) {
	rows := []struct {
		name, in string
		want     time.Duration
		warn     []string
	}{
		{name: "unset", in: "", want: 30 * time.Second},
		// A zero WITH a unit disables the timer; the shared regex has no
		// unit-less form, so the bare "0" below is unparseable rather than zero.
		// TestParseDurationEnv_AnExplicitZeroIsStillZero covers the other units.
		{name: "explicit_zero_disables", in: "0s", want: 0},
		{name: "seconds", in: "45s", want: 45 * time.Second},
		{name: "minutes", in: "2m", want: 2 * time.Minute},
		{name: "bare_zero_is_not_a_duration", in: "0", want: 30 * time.Second,
			warn: []string{"RELAY_SYNC_HEARTBEAT_INTERVAL", `"0"`}},
		{name: "negative_is_unrepresentable", in: "-30s", want: 30 * time.Second,
			warn: []string{"RELAY_SYNC_HEARTBEAT_INTERVAL"}},
		{name: "below_the_floor", in: "1s", want: 30 * time.Second,
			warn: []string{"RELAY_SYNC_HEARTBEAT_INTERVAL", "5s"}},
		{name: "garbage", in: "garbage", want: 30 * time.Second,
			warn: []string{"RELAY_SYNC_HEARTBEAT_INTERVAL"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			require.Equal(t, row.want, resolveSyncHeartbeatInterval(row.in))
			if len(row.warn) == 0 {
				require.Empty(t, buf.String(), "a valid value must not warn")
				return
			}
			for _, w := range row.warn {
				require.Contains(t, buf.String(), w)
			}
		})
	}
}
