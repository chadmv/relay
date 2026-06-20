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
	setupProcTree(cmd)
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

	// Spawn something that produces a steady stream of stdout. On a forced
	// cancel the whole process group is SIGKILLed, so exec's copy goroutine
	// reaches EOF promptly and cmd.Wait returns well under WaitDelay (5s).
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

// TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang reproduces the
// go.dev/issue/23019 pattern: the foreground command exits 0 while a background
// grandchild keeps the inherited stdout pipe open. With the old
// StdoutPipe/pipeLog/wg.Wait() machinery the runner blocked forever because
// WaitDelay never engaged. After the fix exec owns the copy goroutines, so
// cmd.Wait() force-closes the descriptors within WaitDelay (5s) and the runner
// returns with a terminal status.
//
// The leaked child sleeps 30s - far longer than WaitDelay (5s) and the in-test
// 9s hang-timeout - so the test distinguishes red from green: the fixed code
// returns at ~5s (WaitDelay), while the old code blocks until the child dies at
// ~30s and trips the timeout. After the fixed runner returns, the leaked
// sleep 30 lingers harmlessly in its own process group (a normal exit does not
// kill the tree); it self-reaps and does not affect the assertion.
//
// Unix-only: relies on a POSIX shell backgrounding a child that inherits the
// stdout pipe. The file's //go:build !windows tag skips it on Windows.
func TestRunner_NormalExit_LeakedChildHoldingStdout_DoesNotHang(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 4096)

	// `sleep 30 &` forks a child that inherits stdout and holds the write end
	// open for 30s; the parent shell exits 0 immediately. The runner must not
	// wait on the leaked child past WaitDelay (5s).
	task := &relayv1.DispatchTask{
		TaskId:   "t-leak",
		JobId:    "j-leak",
		Commands: singleCmd([]string{"sh", "-c", "sleep 30 & echo done"}),
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	// The shell exits immediately; the leaked child holds the pipe for 30s.
	// WaitDelay (5s) plus slack must bound the runner. The old code would hang
	// here until the leaked child dies (~30s), tripping this 9s timeout.
	select {
	case <-done:
	case <-time.After(9 * time.Second):
		t.Fatal("runner hung: did not return within WaitDelay bound after normal exit with leaked child")
	}

	// Drain sendCh and assert a terminal status was reported.
	var sawTerminal bool
	for {
		select {
		case msg := <-sendCh:
			if ts := msg.GetTaskStatus(); ts != nil {
				switch ts.Status {
				case relayv1.TaskStatus_TASK_STATUS_DONE,
					relayv1.TaskStatus_TASK_STATUS_FAILED,
					relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
					sawTerminal = true
				}
			}
		default:
			require.True(t, sawTerminal, "runner must report a terminal status")
			return
		}
	}
}
