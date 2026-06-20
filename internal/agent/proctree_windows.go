//go:build windows

package agent

import (
	"log"
	"os/exec"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setupProcTree configures cmd so that:
//   - the child is assigned to a Windows Job Object with
//     JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE set, so any descendant the child
//     spawns is also tied to the job and dies with it.
//   - cmd.Cancel calls TerminateJobObject, which kills every process in the
//     job atomically — direct child and any descendants.
//
// Returns a cleanup function that must be called after cmd.Wait returns to
// close the Job Object handle when the process completes without cancellation.
func setupProcTree(cmd *exec.Cmd) func() {
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
		job = 0 // claim ownership so cleanup won't double-close
		jobMu.Unlock()
		if h != 0 {
			_ = windows.TerminateJobObject(h, 1)
			_ = windows.CloseHandle(h)
		} else if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil
	}

	// Eagerly assign the process to the job as soon as cmd.Start sets
	// cmd.Process. Start is synchronous so the loop exits almost immediately.
	// The deadline prevents a goroutine leak if Start fails and Process is
	// never set.
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if cmd.Process != nil {
				ensureAssigned()
				return
			}
			runtime.Gosched()
		}
	}()

	// cleanup closes the Job Object handle when the process completes without
	// cancellation. cmd.Cancel zeros job before closing, so this is a no-op
	// if cancel already ran.
	return func() {
		jobMu.Lock()
		h := job
		job = 0
		jobMu.Unlock()
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}
}
