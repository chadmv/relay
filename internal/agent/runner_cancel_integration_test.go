//go:build integration

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// TestRunner_TreeKill_RealSubprocesses spawns a parent script that forks a
// long-running grandchild and writes the grandchild's PID to a file.
// After cancel, we verify the grandchild process is gone within 2s.
func TestRunner_TreeKill_RealSubprocesses(t *testing.T) {
	dir := t.TempDir()
	pidFile := dir + "/grandchild.pid"

	var argv []string
	switch runtime.GOOS {
	case "windows":
		// PowerShell: spawn a detached ping and write its PID to a file.
		script := "$p = Start-Process -PassThru -WindowStyle Hidden ping.exe -ArgumentList '127.0.0.1','-n','60'; " +
			"$p.Id | Out-File -Encoding ASCII '" + pidFile + "'; " +
			"Wait-Process -Id $p.Id"
		argv = []string{"powershell", "-NoProfile", "-Command", script}
	default:
		// Bash: fork sleep, record PID, then wait.
		script := "sleep 60 & echo $! > " + pidFile + "; wait"
		argv = []string{"sh", "-c", script}
	}

	sendCh := make(chan *relayv1.AgentMessage, 256)
	task := &relayv1.DispatchTask{
		TaskId:   "t-tree",
		JobId:    "j-tree",
		Commands: singleCmd(argv),
	}

	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)

	done := make(chan struct{})
	go func() { defer close(done); r.Run(runCtx, task) }()

	// Wait for the grandchild PID to appear in the file (up to 5s).
	var grandPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pidStr := strings.TrimSpace(string(data))
			if pidStr != "" {
				if p, perr := strconv.Atoi(pidStr); perr == nil && p > 0 {
					grandPID = p
					break
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotZero(t, grandPID, "grandchild PID file never appeared")
	t.Logf("grandchild PID: %d", grandPID)

	// Default cancel — exercises tree-kill.
	r.Cancel(false)

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("runner did not exit within 8s after cancel")
	}

	// Poll for grandchild to be gone within 2s.
	gone := false
	pollDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(pollDeadline) {
		if !pidAlive(grandPID) {
			gone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		_ = killPID(grandPID) // best-effort cleanup
		t.Fatalf("grandchild PID %d still alive 2s after cancel — tree-kill did not propagate", grandPID)
	}
}

// pidAlive returns true if a process with the given PID exists and is alive.
func pidAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	// Unix: use kill -0 as an external command to avoid importing syscall
	err := exec.Command("kill", "-0", strconv.Itoa(pid)).Run()
	return err == nil
}

func killPID(pid int) error {
	if runtime.GOOS == "windows" {
		return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
