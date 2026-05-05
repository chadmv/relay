//go:build !windows

package agent

import (
	"context"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSetupProcTree_Unix_SetsPgid verifies that setupProcTree configures the
// child to start a new process group via Setpgid.
func TestSetupProcTree_Unix_SetsPgid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	cmd := exec.Command("sleep", "30")
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
	_ = syscall.SIGKILL
	_ = context.Background
}
