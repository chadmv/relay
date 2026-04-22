package agent

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRunnerSurvivesConnectionContextCancellation(t *testing.T) {
	a := NewAgent("nowhere:0", Capabilities{
		Hostname: "test", CPUCores: 1, RAMGB: 1, OS: "linux",
	}, "", func(string) error { return nil })

	// Simulate entering Run: set the long-lived runCtx.
	runCtx, cancelRun := context.WithCancel(context.Background())
	a.runCtx = runCtx
	defer cancelRun()

	// Simulate the recv loop handling a dispatch with a connection-scoped ctx.
	connCtx, cancelConn := context.WithCancel(runCtx)
	a.handleDispatch(connCtx, &relayv1.DispatchTask{
		TaskId:  "long-task",
		Command: sleepCmd(), // cross-platform long-running command (~10s)
		Epoch:   1,
	})

	// Wait for the runner to be registered (subprocess startup can be slow on CI).
	var r *Runner
	require.Eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		var ok bool
		r, ok = a.runners["long-task"]
		return ok
	}, time.Second, 10*time.Millisecond, "runner was not registered within 1s")

	// Cancel connCtx (simulates stream drop). Runner should NOT exit.
	cancelConn()
	time.Sleep(150 * time.Millisecond)

	// Check that cancelled flag is NOT set (runner is still running).
	assert.False(t, r.cancelled.Load(), "runner should still be alive after conn drop")

	// Clean shutdown.
	cancelRun()
	a.runnerWG.Wait()
}
