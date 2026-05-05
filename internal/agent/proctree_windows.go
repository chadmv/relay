//go:build windows

package agent

import (
	"log"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setupProcTree configures cmd so that:
//   - the child is assigned to a Windows Job Object with
//     JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE set, so any descendant the child
//     spawns is also tied to the job and dies with it.
//   - cmd.Cancel calls TerminateJobObject, which kills every process in the
//     job atomically — direct child and any descendants.
//   - if r.forced is set at cancel time, stdout/stderr pipe handles are
//     closed immediately, bypassing the pipe-buffer drain bounded by
//     cmd.WaitDelay.
func setupProcTree(cmd *exec.Cmd, r *Runner) {
	var (
		jobMu sync.Mutex
		job   windows.Handle
		setup bool
	)

	ensureAssigned := func() {
		jobMu.Lock()
		defer jobMu.Unlock()
		if setup {
			return
		}
		setup = true
		if cmd.Process == nil {
			return
		}
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			log.Printf("agent: CreateJobObject: %v", err)
			return
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(
			h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			log.Printf("agent: SetInformationJobObject: %v", err)
			_ = windows.CloseHandle(h)
			return
		}
		ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err != nil {
			log.Printf("agent: OpenProcess(%d): %v", cmd.Process.Pid, err)
			_ = windows.CloseHandle(h)
			return
		}
		defer windows.CloseHandle(ph)
		if err := windows.AssignProcessToJobObject(h, ph); err != nil {
			log.Printf("agent: AssignProcessToJobObject: %v", err)
			_ = windows.CloseHandle(h)
			return
		}
		job = h
	}

	cmd.Cancel = func() error {
		ensureAssigned()
		jobMu.Lock()
		h := job
		jobMu.Unlock()
		if h != 0 {
			_ = windows.TerminateJobObject(h, 1)
		} else if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if r.forced.Load() {
			r.closeStepPipesForForce()
		}
		return nil
	}

	// Eagerly assign the process to the job object as soon as cmd.Start
	// sets cmd.Process. cmd.Start sets Process synchronously, so this
	// goroutine should complete almost immediately after Start returns.
	go func() {
		for cmd.Process == nil {
		}
		ensureAssigned()
	}()
}
