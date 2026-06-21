//go:build windows

package agent

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSetupProcTree_Windows_AssignsJobObject(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd", "/c", "ping", "127.0.0.1", "-n", "30")
	assign, cleanup := setupProcTree(cmd)
	require.NotNil(t, cmd.Cancel, "cmd.Cancel should be set")

	require.NoError(t, cmd.Start())
	assign() // synchronously assigns the started process to the Job Object
	t.Cleanup(func() { cleanup(); _ = cmd.Process.Kill() })

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
