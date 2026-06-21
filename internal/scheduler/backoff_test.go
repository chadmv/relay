package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextReconnectBackoff(t *testing.T) {
	// Healthy session (LISTEN succeeded) resets to 1s regardless of prior value.
	assert.Equal(t, time.Second, nextReconnectBackoff(32*time.Second, true))
	assert.Equal(t, time.Second, nextReconnectBackoff(60*time.Second, true))

	// Unhealthy session doubles, capped at 60s.
	assert.Equal(t, 2*time.Second, nextReconnectBackoff(time.Second, false))
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(40*time.Second, false))
	assert.Equal(t, 60*time.Second, nextReconnectBackoff(60*time.Second, false))
}

// TestRun_ResetsBackoffBeforeSleepingAfterHealthyDrop is the ordering test for
// the reconnect-backoff-never-resets fix in the notify listener. It seeds the
// loop with an accumulated (capped) backoff, then lets a healthy session (both
// LISTENs succeeded) register and immediately drop. The reset must be applied
// BEFORE the loop sleeps, so the FIRST wait after the drop is the short reset
// value (~1s), not the stale accumulated value (60s).
func TestRun_ResetsBackoffBeforeSleepingAfterHealthyDrop(t *testing.T) {
	initialReconnectBackoff = 60 * time.Second
	defer func() { initialReconnectBackoff = time.Second }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var firstWait time.Duration
	got := make(chan struct{})
	reconnectSleep = func(_ context.Context, d time.Duration) bool {
		firstWait = d
		close(got)
		cancel()
		return false
	}
	defer func() { reconnectSleep = nil }()

	n := &NotifyListener{trigger: func() {}}
	// Healthy session: both LISTENs succeeded, then the connection dropped.
	n.sessionFn = func(context.Context) (bool, error) {
		return true, assert.AnError
	}

	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()

	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("loop never reached the reconnect sleep")
	}
	<-done

	assert.Equal(t, time.Second, firstWait,
		"first wait after a healthy session drop must be the reset value, not the stale accumulated backoff")
}

// TestRun_FirstUnhealthyFailureWaitsOneSecond guards requirement #3 for the
// notify listener: a first unhealthy failure from a fresh start (LISTEN never
// succeeds) still waits ~1s before retrying, not 2s.
func TestRun_FirstUnhealthyFailureWaitsOneSecond(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var firstWait time.Duration
	got := make(chan struct{})
	reconnectSleep = func(_ context.Context, d time.Duration) bool {
		firstWait = d
		close(got)
		cancel()
		return false
	}
	defer func() { reconnectSleep = nil }()

	n := &NotifyListener{trigger: func() {}}
	n.sessionFn = func(context.Context) (bool, error) {
		return false, assert.AnError
	}

	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()

	select {
	case <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("loop never reached the reconnect sleep")
	}
	<-done

	assert.Equal(t, time.Second, firstWait,
		"first unhealthy failure from a fresh start must wait ~1s, not double to 2s")
}
