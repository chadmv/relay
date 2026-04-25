package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDurationEnv(t *testing.T) {
	require.Equal(t, 14*24*time.Hour, parseDurationEnv("14d", 0))
	require.Equal(t, 5*time.Minute, parseDurationEnv("5m", 0))
	require.Equal(t, 30*time.Second, parseDurationEnv("30s", 0))
	require.Equal(t, time.Hour, parseDurationEnv("garbage", time.Hour))
	require.Equal(t, time.Duration(0), parseDurationEnv("", 0))
}
