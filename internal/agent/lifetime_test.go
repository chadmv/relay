package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	perforceProvider "relay/internal/agent/source/perforce"
	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRunnerSurvivesConnectionContextCancellation(t *testing.T) {
	creds, _ := LoadCredentials(t.TempDir())
	creds.SetEnrollmentToken("test-enrollment")
	a := NewAgent("nowhere:0", Capabilities{
		Hostname: "test", CPUCores: 1, RAMGB: 1, OS: "linux",
	}, "", creds, func(string) error { return nil }, nil)

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

func TestAgent_BuildRegisterRequest_IncludesRunningTasks(t *testing.T) {
	creds, _ := LoadCredentials(t.TempDir())
	creds.SetEnrollmentToken("test-enrollment")
	a := NewAgent("nowhere:0", Capabilities{
		Hostname: "test", CPUCores: 1, RAMGB: 1, OS: "linux",
	}, "worker-xyz", creds, func(string) error { return nil }, nil)

	// Simulate two active runners (hold mutex to match production invariant).
	a.mu.Lock()
	a.runners["task-1"] = &Runner{taskID: "task-1", epoch: 3}
	a.runners["task-2"] = &Runner{taskID: "task-2", epoch: 7}
	a.mu.Unlock()

	req, err := a.buildRegisterRequest()
	require.NoError(t, err)
	assert.Equal(t, "worker-xyz", req.WorkerId)
	assert.NotNil(t, req.Credential, "credential must be populated")
	assert.Equal(t, "test-enrollment", req.GetEnrollmentToken())
	assert.Len(t, req.RunningTasks, 2)

	byID := map[string]int64{}
	for _, rt := range req.RunningTasks {
		byID[rt.TaskId] = rt.Epoch
	}
	assert.Equal(t, int64(3), byID["task-1"])
	assert.Equal(t, int64(7), byID["task-2"])
}

func TestAgent_BuildRegisterRequest_IncludesInventory(t *testing.T) {
	root := t.TempDir()
	// Pre-seed a registry file.
	reg, err := perforceProvider.LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	reg.Upsert(perforceProvider.WorkspaceEntry{
		ShortID: "abcdef", SourceKey: "//s/x", ClientName: "relay_h_abcdef",
		BaselineHash: "deadbeef", LastUsedAt: time.Now(),
	})
	require.NoError(t, reg.Save())

	p := perforceProvider.New(perforceProvider.Config{Root: root, Hostname: "h"})
	creds, _ := LoadCredentials(t.TempDir())
	creds.SetEnrollmentToken("test-enrollment")
	a := NewAgent("addr", Capabilities{Hostname: "h"}, "", creds, func(string) error { return nil }, p)
	req, err := a.buildRegisterRequest()
	require.NoError(t, err)
	require.Len(t, req.Inventory, 1)
	require.Equal(t, "//s/x", req.Inventory[0].SourceKey)
	require.Equal(t, "abcdef", req.Inventory[0].ShortId)
}
