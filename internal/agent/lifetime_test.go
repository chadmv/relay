package agent

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
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

	// Wait for the runner to start.
	time.Sleep(100 * time.Millisecond)
	a.mu.Lock()
	r, ok := a.runners["long-task"]
	a.mu.Unlock()
	if !ok {
		t.Fatal("runner was not registered")
	}

	// Cancel connCtx (simulates stream drop). Runner should NOT exit.
	cancelConn()
	time.Sleep(150 * time.Millisecond)

	// Check that cancelled flag is NOT set (runner is still running).
	assert.False(t, r.cancelled.Load(), "runner should still be alive after conn drop")

	// Clean shutdown.
	cancelRun()
	a.runnerWG.Wait()
}
