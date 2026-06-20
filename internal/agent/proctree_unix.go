//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// setupProcTree configures cmd so that:
//   - the child starts in its own process group (Setpgid), so all descendants
//     inherit the same PGID by default.
//   - cmd.Cancel (invoked when the context tied to exec.CommandContext is
//     cancelled) sends SIGKILL to the entire process group, killing every
//     descendant in one shot.
func setupProcTree(cmd *exec.Cmd) func() {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		pid := 0
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		// Negative PID targets the process group whose PGID == |pid|.
		// We set Setpgid above so PGID == child PID.
		if pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		return nil
	}
	return func() {} // no kernel handle to release on Unix
}
