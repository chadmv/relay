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

func TestParseDurationEnv_NoWarningOnEmptyInput(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	result := parseDurationEnv("RELAY_WORKSPACE_MAX_AGE", "", 0)

	require.Equal(t, time.Duration(0), result)
	require.Empty(t, buf.String(), "empty env var should not produce a warning")
}

// Covers the PARSING only. The assignment of the result into the
// perforce.Config literal in main() is not covered by this or any other test:
// cmd/relay-agent has no env-to-field wiring guard of any kind, so deleting
// that line compiles and leaves every package green, exactly like the existing
// unguarded assignments beside it
// (docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md).
func TestResolveSyncHeartbeatInterval(t *testing.T) {
	rows := []struct {
		name, in string
		want     time.Duration
		warn     []string
	}{
		{name: "unset", in: "", want: 30 * time.Second},
		// "0s" is the ONLY spelling that disables the timer: the shared regex
		// has no unit-less form, so a bare "0" is unparseable rather than zero.
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
