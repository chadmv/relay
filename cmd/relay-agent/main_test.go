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
