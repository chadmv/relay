package agent

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)

// Runner manages the execution of a single dispatched task as a subprocess.
type Runner struct {
	taskID         string
	epoch          int64
	sendCh         chan *relayv1.AgentMessage
	ctx            context.Context // parent (agent) context — lives for the agent lifetime, not the connection
	cancel         context.CancelFunc
	cancelled      atomic.Bool
	forced         atomic.Bool
	abandoned      atomic.Bool
	forcedCh       chan struct{} // closed exactly once by Cancel(force=true); signals in-flight log writes to abandon
	cancelledCh    chan struct{} // closed exactly once by Cancel(false) or Abandon(); signals in-flight log writes to abandon on a per-task cancel
	cancelledClose sync.Once     // guards the single close of cancelledCh across mixed/repeated cancels
	provider       source.Provider
}

// newRunner creates a Runner and its execution context.
// If timeoutSec > 0, the context carries a deadline; otherwise it inherits
// only the parent's cancellation.
func newRunner(taskID string, epoch int64, sendCh chan *relayv1.AgentMessage, parent context.Context, timeoutSec int32) (*Runner, context.Context) {
	var runCtx context.Context
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		runCtx, cancel = context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
	} else {
		runCtx, cancel = context.WithCancel(parent)
	}
	return &Runner{
		taskID:      taskID,
		epoch:       epoch,
		sendCh:      sendCh,
		ctx:         parent,
		cancel:      cancel,
		forcedCh:    make(chan struct{}),
		cancelledCh: make(chan struct{}),
	}, runCtx
}

// Cancel signals the subprocess to stop. The task is reported as FAILED.
// If force is true, the runner skips workspace finalize, bypasses pipe drain
// when killing the subprocess, and closes forcedCh so in-flight log writes
// abandon instead of parking on a full sendCh. A non-forced (default) cancel
// closes cancelledCh, which gives in-flight log writes the same per-task escape
// without skipping workspace finalize.
func (r *Runner) Cancel(force bool) {
	if force {
		// CompareAndSwap guarantees exactly one forced caller closes forcedCh,
		// even under concurrent or repeated Cancel(true) / mixed forced and
		// non-forced cancels. Closing a channel twice panics; this gate prevents it.
		if r.forced.CompareAndSwap(false, true) {
			close(r.forcedCh)
		}
	}
	// Both cancel kinds free a parked log send via cancelledCh. The sync.Once
	// makes this safe under repeated, concurrent, or mixed forced/default/abandon
	// calls on the same runner.
	r.cancelledClose.Do(func() { close(r.cancelledCh) })
	r.cancelled.Store(true)
	r.cancel()
}

// Abandon is like Cancel but suppresses the final status message. Used when
// the coordinator's RegisterResponse.cancel_task_ids indicates this task was
// reassigned to another worker during a grace-expiry requeue.
func (r *Runner) Abandon() {
	r.abandoned.Store(true)
	r.cancelledClose.Do(func() { close(r.cancelledCh) })
	r.cancel()
}

// Run executes the task and sends status/log messages to sendCh. Blocks until done.
func (r *Runner) Run(ctx context.Context, task *relayv1.DispatchTask) {
	defer r.cancel() // always release context resources, even on normal exit

	// 1) Prepare phase — acquire and sync workspace if a source spec is present.
	var workDir string
	var extraEnv map[string]string
	// A source-bearing task requires a workspace provider. If the agent has
	// none (p4 preflight failed, or RELAY_WORKSPACE_ROOT is unset), reject the
	// task loudly instead of silently running its commands without a synced
	// workspace. Dispatch does not filter on provider capability, so this is the
	// agent's last line of defense.
	if task.Source != nil && r.provider == nil {
		r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId:       r.taskID,
				Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
				ErrorMessage: "task has a source spec but this worker has no workspace provider (check p4 preflight / RELAY_WORKSPACE_ROOT)",
				Epoch:        r.epoch,
			},
		}})
		return
	}
	if task.Source != nil && r.provider != nil {
		r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId: r.taskID,
				Status: relayv1.TaskStatus_TASK_STATUS_PREPARING,
				Epoch:  r.epoch,
			},
		}})
		progress, flushProgress := r.makePrepareProgressFn()
		handle, err := r.provider.Prepare(ctx, r.taskID, task.Source, progress)
		flushProgress() // drain any buffered tail lines whether Prepare succeeded or failed
		if err != nil {
			r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskStatus{
				TaskStatus: &relayv1.TaskStatusUpdate{
					TaskId:       r.taskID,
					Status:       relayv1.TaskStatus_TASK_STATUS_PREPARE_FAILED,
					ErrorMessage: err.Error(),
					Epoch:        r.epoch,
				},
			}})
			return
		}
		defer func() {
			if r.forced.Load() {
				log.Printf("runner: skipping workspace finalize for %s (forced cancel)", r.taskID)
				return
			}
			if finalErr := handle.Finalize(r.ctx); finalErr != nil {
				log.Printf("runner: finalize failed for %s: %v", r.taskID, finalErr)
			}
			r.sendInventory(handle.Inventory())
		}()
		workDir = handle.WorkingDir()
		extraEnv = handle.Env()
	}

	// 2) Command execution.
	if len(task.Commands) == 0 {
		r.sendFinalStatus(relayv1.TaskStatus_TASK_STATUS_FAILED, nil)
		return
	}

	// Merge env: current process env first, task env overrides, then workspace env.
	env := os.Environ()
	for k, v := range task.Env {
		env = append(env, k+"="+v)
	}
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}

	// Send a single RUNNING status before the first step. Subsequent steps
	// reuse the same RUNNING phase; the synthetic per-step marker lines in the
	// log stream delineate progress.
	r.send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId: r.taskID,
				Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
				Epoch:  r.epoch,
			},
		},
	})

	total := len(task.Commands)
	var lastExitCode *int32
	finalStatus := relayv1.TaskStatus_TASK_STATUS_DONE
	for i, cl := range task.Commands {
		if cl == nil || len(cl.Argv) == 0 {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}
		argv := cl.Argv
		step := int32(i + 1)
		stepTotal := int32(total)
		r.sendStepMarker(step, stepTotal, argv)

		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.WaitDelay = 5 * time.Second // bound pipe draining after process exit/kill
		assignProcTree, cleanupProcTree := setupProcTree(cmd)
		cmd.Env = env
		if workDir != "" {
			cmd.Dir = workDir
		}

		// Hand exec custom writers instead of taking the pipes ourselves. This
		// makes exec own the OS pipes AND the copy goroutines, so cmd.Wait()
		// enforces WaitDelay: if a leaked child still holds the write end after
		// the process exits, Wait force-closes the descriptors within 5s instead
		// of blocking forever (go.dev/issue/23019).
		cmd.Stdout = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, stepIndex: step, stepTotal: stepTotal}
		cmd.Stderr = &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, stepIndex: step, stepTotal: stepTotal}

		if err := cmd.Start(); err != nil {
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
			break
		}
		// Assign the started process to the proctree (Windows Job Object) now
		// that cmd.Start has populated cmd.Process. Calling this synchronously
		// after Start - rather than from a goroutine that polls cmd.Process -
		// avoids racing the Start write to cmd.Process. No-op on Unix.
		assignProcTree()

		waitErr := cmd.Wait()
		cleanupProcTree()

		lastExitCode = nil
		if cmd.ProcessState != nil {
			if code := cmd.ProcessState.ExitCode(); code >= 0 {
				c := int32(code)
				lastExitCode = &c
			}
		}

		if waitErr == nil {
			continue
		}
		switch {
		case r.cancelled.Load():
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
		case ctx.Err() == context.DeadlineExceeded:
			finalStatus = relayv1.TaskStatus_TASK_STATUS_TIMED_OUT
		default:
			finalStatus = relayv1.TaskStatus_TASK_STATUS_FAILED
		}
		break
	}

	r.sendFinalStatus(finalStatus, lastExitCode)
}

// sendStepMarker writes a synthetic delimiter line into the stdout stream so
// the consolidated log can be split per step. step_index and step_total are
// also stamped onto the chunk for structured consumers; the text marker is
// retained for log-tailing tools that don't (yet) read the structured fields.
func (r *Runner) sendStepMarker(step, total int32, argv []string) {
	line := []byte("=== relay step " + strconv.Itoa(int(step)) + "/" + strconv.Itoa(int(total)) + " === " + strings.Join(argv, " ") + "\n")
	r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskLog{
		TaskLog: &relayv1.TaskLogChunk{
			TaskId:    r.taskID,
			Stream:    relayv1.LogStream_LOG_STREAM_STDOUT,
			Content:   line,
			Epoch:     r.epoch,
			StepIndex: step,
			StepTotal: total,
		},
	}})
}

// errForcedAbort is returned by chunkWriter.Write when a forced cancel closes
// r.forcedCh while a log send is in flight. A non-nil Write error makes exec's
// io.Copy stop copying so cmd.Wait() returns promptly instead of waiting out
// WaitDelay. It is consumed only by exec's copy loop; the runner's terminal
// status is decided independently in Run (the cancelled branch yields FAILED),
// so this sentinel never leaks as an extra task failure.
var errForcedAbort = errors.New("relay: forced cancel aborted in-flight log write")

// chunkWriter is the io.Writer exec copies subprocess stdout/stderr into. Each
// Write copies its slice (exec reuses the buffer between calls), wraps it in a
// TaskLogChunk stamped with the runner's stream/step/epoch, and pushes it
// through r.sendOrAbort. On a successful enqueue Write returns (len(p), nil) so
// exec keeps copying until EOF (unchanged slow-consumer behavior). If a forced
// cancel has closed r.forcedCh (or the agent context is done), the enqueue is
// abandoned and Write returns errForcedAbort so exec's io.Copy stops and
// cmd.Wait() returns promptly instead of waiting out WaitDelay.
type chunkWriter struct {
	r         *Runner
	stream    relayv1.LogStream
	stepIndex int32
	stepTotal int32
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil // match the old pipeLog n>0 guard: never emit an empty chunk
	}
	chunk := make([]byte, len(p))
	copy(chunk, p)
	if !w.r.sendOrAbort(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId:    w.r.taskID,
				Stream:    w.stream,
				Content:   chunk,
				Epoch:     w.r.epoch,
				StepIndex: w.stepIndex,
				StepTotal: w.stepTotal,
			},
		},
	}) {
		// Abandoned. On a forced cancel this stops io.Copy so cmd.Wait returns.
		// On agent shutdown (ctx.Done) returning the sentinel is equally fine:
		// the runner is tearing down regardless.
		return 0, errForcedAbort
	}
	return len(p), nil
}

func (r *Runner) sendFinalStatus(status relayv1.TaskStatus, exitCode *int32) {
	if r.abandoned.Load() {
		return // coordinator reassigned this task; suppress final status
	}
	msg := &relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{TaskStatus: &relayv1.TaskStatusUpdate{
			TaskId:   r.taskID,
			Status:   status,
			ExitCode: exitCode,
			Epoch:    r.epoch,
		}},
	}
	if r.forced.Load() {
		// Forced cancel: best-effort, bounded enqueue so Run returns even when
		// sendCh is wedged full. Try the enqueue first; only abandon when sendCh
		// is genuinely full. forcedCh is already closed (Cancel closed it), so a
		// plain two-case select would race the always-ready closed channel
		// against the send even when there is headroom; the non-blocking try-send
		// prefers delivery and falls back to abandon only on a full channel.
		// Dropping the message is safe: the server's CancelJobTasks already set
		// the task failed and bumped assignment_epoch, so this terminal message
		// (carrying the old r.epoch) is epoch-fenced out.
		select {
		case r.sendCh <- msg:
		default:
			// sendCh full and wedged; abandon best-effort. Server is authoritative.
		}
		return
	}
	r.send(msg)
}

func (r *Runner) send(msg *relayv1.AgentMessage) {
	select {
	case r.sendCh <- msg:
	case <-r.ctx.Done():
		// Connection lost; will be redelivered when agent reconnects.
	}
}

// sendOrAbort enqueues a log chunk like send, but additionally abandons the
// enqueue if a forced cancel has closed r.forcedCh. It returns true on a
// successful enqueue and false if it abandoned (agent shutdown or forced abort).
// Only chunkWriter.Write uses this; all other callers use send so their
// blocking discipline is unchanged.
func (r *Runner) sendOrAbort(msg *relayv1.AgentMessage) bool {
	select {
	case r.sendCh <- msg:
		return true
	case <-r.ctx.Done():
		// Agent shutdown; will be redelivered when the agent reconnects.
		return false
	case <-r.forcedCh:
		// Forced cancel in progress; abandon this chunk so cmd.Wait can return.
		return false
	}
}

// makePrepareProgressFn returns a progress callback and a flush function. The
// callback batches log lines and sends them as LOG_STREAM_PREPARE chunks,
// rate-limited to one send per 500 ms or 50 lines. The flush function drains
// any remaining buffered lines and must be called after provider.Prepare
// returns so tail-end progress lines are not silently dropped.
func (r *Runner) makePrepareProgressFn() (progress func(line string), flush func()) {
	var mu sync.Mutex
	var buf []string
	var lastFlush time.Time
	doFlush := func() {
		if len(buf) == 0 {
			return
		}
		content := []byte(strings.Join(buf, "\n") + "\n")
		buf = nil
		lastFlush = time.Now()
		r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId:  r.taskID,
				Stream:  relayv1.LogStream_LOG_STREAM_PREPARE,
				Content: content,
				Epoch:   r.epoch,
			},
		}})
	}
	progress = func(line string) {
		mu.Lock()
		defer mu.Unlock()
		buf = append(buf, line)
		if time.Since(lastFlush) >= 500*time.Millisecond || len(buf) >= 50 {
			doFlush()
		}
	}
	flush = func() {
		mu.Lock()
		defer mu.Unlock()
		doFlush()
	}
	return
}

// sendInventory reports a workspace inventory entry to the coordinator.
func (r *Runner) sendInventory(e source.InventoryEntry) {
	r.send(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_WorkspaceInventory{
		WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
			SourceType:   e.SourceType,
			SourceKey:    e.SourceKey,
			ShortId:      e.ShortID,
			BaselineHash: e.BaselineHash,
			LastUsedAt:   e.LastUsedAt.Format("2006-01-02T15:04:05Z07:00"),
			Deleted:      e.Deleted,
		},
	}})
}
