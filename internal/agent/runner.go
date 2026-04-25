package agent

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)

// Runner manages the execution of a single dispatched task as a subprocess.
type Runner struct {
	taskID    string
	epoch     int64
	sendCh    chan *relayv1.AgentMessage
	ctx       context.Context // parent (agent) context — lives for the agent lifetime, not the connection
	cancel    context.CancelFunc
	cancelled atomic.Bool
	abandoned atomic.Bool
	provider  source.Provider
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
	return &Runner{taskID: taskID, epoch: epoch, sendCh: sendCh, ctx: parent, cancel: cancel}, runCtx
}

// Cancel signals the subprocess to stop. The task is reported as FAILED.
func (r *Runner) Cancel() {
	r.cancelled.Store(true)
	r.cancel()
}

// Abandon is like Cancel but suppresses the final status message. Used when
// the coordinator's RegisterResponse.cancel_task_ids indicates this task was
// reassigned to another worker during a grace-expiry requeue.
func (r *Runner) Abandon() {
	r.abandoned.Store(true)
	r.cancel()
}

// Run executes the task and sends status/log messages to sendCh. Blocks until done.
func (r *Runner) Run(ctx context.Context, task *relayv1.DispatchTask) {
	defer r.cancel() // always release context resources, even on normal exit

	// 1) Prepare phase — acquire and sync workspace if a source spec is present.
	var workDir string
	var extraEnv map[string]string
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
			if finalErr := handle.Finalize(r.ctx); finalErr != nil {
				log.Printf("runner: finalize failed for %s: %v", r.taskID, finalErr)
			}
			r.sendInventory(handle.Inventory())
		}()
		workDir = handle.WorkingDir()
		extraEnv = handle.Env()
	}

	// 2) Command execution.
	if len(task.Command) == 0 {
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

	cmd := exec.CommandContext(ctx, task.Command[0], task.Command[1:]...)
	cmd.WaitDelay = 5 * time.Second // bound pipe draining after process kill
	cmd.Env = env
	if workDir != "" {
		cmd.Dir = workDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.sendFinalStatus(relayv1.TaskStatus_TASK_STATUS_FAILED, nil)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.sendFinalStatus(relayv1.TaskStatus_TASK_STATUS_FAILED, nil)
		return
	}

	if err := cmd.Start(); err != nil {
		r.sendFinalStatus(relayv1.TaskStatus_TASK_STATUS_FAILED, nil)
		return
	}

	r.send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{
			TaskStatus: &relayv1.TaskStatusUpdate{
				TaskId: r.taskID,
				Status: relayv1.TaskStatus_TASK_STATUS_RUNNING,
				Epoch:  r.epoch,
			},
		},
	})

	// Drain stdout and stderr concurrently.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.pipeLog(stdout, relayv1.LogStream_LOG_STREAM_STDOUT) }()
	go func() { defer wg.Done(); r.pipeLog(stderr, relayv1.LogStream_LOG_STREAM_STDERR) }()
	wg.Wait()

	waitErr := cmd.Wait()

	var exitCode *int32
	if cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			c := int32(code)
			exitCode = &c
		}
	}

	var status relayv1.TaskStatus
	switch {
	case waitErr == nil:
		status = relayv1.TaskStatus_TASK_STATUS_DONE
	case r.cancelled.Load():
		status = relayv1.TaskStatus_TASK_STATUS_FAILED
	case ctx.Err() == context.DeadlineExceeded:
		status = relayv1.TaskStatus_TASK_STATUS_TIMED_OUT
	default:
		status = relayv1.TaskStatus_TASK_STATUS_FAILED
	}

	r.sendFinalStatus(status, exitCode)
}

func (r *Runner) pipeLog(pipe io.Reader, stream relayv1.LogStream) {
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			r.send(&relayv1.AgentMessage{
				Payload: &relayv1.AgentMessage_TaskLog{
					TaskLog: &relayv1.TaskLogChunk{
						TaskId:  r.taskID,
						Stream:  stream,
						Content: chunk,
						Epoch:   r.epoch,
					},
				},
			})
		}
		if err != nil {
			return
		}
	}
}

func (r *Runner) sendFinalStatus(status relayv1.TaskStatus, exitCode *int32) {
	if r.abandoned.Load() {
		return // coordinator reassigned this task; suppress final status
	}
	upd := &relayv1.TaskStatusUpdate{
		TaskId:   r.taskID,
		Status:   status,
		ExitCode: exitCode,
		Epoch:    r.epoch,
	}
	r.send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskStatus{TaskStatus: upd},
	})
}

func (r *Runner) send(msg *relayv1.AgentMessage) {
	select {
	case r.sendCh <- msg:
	case <-r.ctx.Done():
		// Connection lost; will be redelivered when agent reconnects.
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
