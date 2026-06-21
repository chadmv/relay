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
//
// Returns two functions. assign must be called by the caller immediately after
// cmd.Start() returns successfully; it assigns the started process to the Job
// Object. Calling it after Start guarantees cmd.Process is set and avoids racing
// the Start write to cmd.Process. cleanup must be called after cmd.Wait returns
// to close the Job Object handle when the process completes without cancellation.
func setupProcTree(cmd *exec.Cmd) (assign func(), cleanup func()) {
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

	// assign eagerly attaches the started process to the job. The caller invokes
	// it synchronously right after cmd.Start() returns, so cmd.Process is set and
	// this read is sequenced-after the Start write (no goroutine, no race).
	assign = func() {
		ensureAssigned()
	}

	// cleanup closes the Job Object handle when the process completes without
	// cancellation. cmd.Cancel zeros job before closing, so this is a no-op
	// if cancel already ran.
	cleanup = func() {
		jobMu.Lock()
		h := job
		job = 0
		jobMu.Unlock()
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}

	return assign, cleanup
}
