//go:build !windows

package agent

import (
	"context"
	"os/exec"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// TestSetupProcTree_Unix_SetsPgid verifies that setupProcTree configures the
// child to start a new process group via Setpgid.
func TestSetupProcTree_Unix_SetsPgid(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sleep", "30")
	r := &Runner{}
	setupProcTree(cmd, r)
	require.NotNil(t, cmd.SysProcAttr, "SysProcAttr should be set")
	require.True(t, cmd.SysProcAttr.Setpgid, "Setpgid should be true")
	require.NotNil(t, cmd.Cancel, "cmd.Cancel should be set")

	// Smoke-test the cancel path against a real process.
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	_ = cmd.Cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit within 2s after cancel")
	}
}

func TestRunner_ForceCancel_SkipsWorkspaceFinalize(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 256)
	fh := &fakeHandle{dir: t.TempDir()}
	prov := &fakeProvider{handle: fh}

	// A long-running command so we have time to cancel mid-flight.
	task := &relayv1.DispatchTask{
		TaskId:   "t-force",
		JobId:    "j-force",
		Commands: singleCmd([]string{"sleep", "30"}),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	r.SetProviderForTest(prov)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	// Wait for the subprocess to actually start before we cancel.
	time.Sleep(200 * time.Millisecond)
	r.Cancel(true)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not exit within 5s after force cancel")
	}

	require.False(t, fh.finalized, "Finalize must not be called on forced cancel")
}

func TestRunner_DefaultCancel_RunsWorkspaceFinalize(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 256)
	fh := &fakeHandle{dir: t.TempDir()}
	prov := &fakeProvider{handle: fh}

	task := &relayv1.DispatchTask{
		TaskId:   "t-default",
		JobId:    "j-default",
		Commands: singleCmd([]string{"sleep", "30"}),
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	r.SetProviderForTest(prov)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	time.Sleep(200 * time.Millisecond)
	r.Cancel(false)

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runner did not exit within 8s after default cancel")
	}

	require.True(t, fh.finalized, "Finalize must be called on default cancel")
}

func TestRunner_ForceCancel_ReturnsQuickly(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4096)

	// Spawn something that produces a steady stream of stdout, so the kernel
	// pipe buffer always has bytes the agent's pipeLog goroutine could be
	// reading. On a non-forced cancel, draining this would burn the
	// 5s WaitDelay; on a forced cancel, closeStepPipesForForce should
	// short-circuit it.
	task := &relayv1.DispatchTask{
		TaskId:   "t-quick",
		JobId:    "j-quick",
		Commands: singleCmd([]string{"sh", "-c", "while true; do echo line; done"}),
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	time.Sleep(300 * time.Millisecond) // let it start producing output
	start := time.Now()
	r.Cancel(true)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("forced cancel did not return within 3s")
	}
	elapsed := time.Since(start)
	require.Less(t, elapsed, 2*time.Second,
		"forced cancel should return well under WaitDelay (5s); took %s", elapsed)
}
