package agent

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

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

// Run executes the task and sends status/log messages to sendCh. Blocks until done.
func (r *Runner) Run(ctx context.Context, task *relayv1.DispatchTask) {
	defer r.cancel() // always release context resources, even on normal exit

	if len(task.Command) == 0 {
		r.sendFinalStatus(relayv1.TaskStatus_TASK_STATUS_FAILED, nil)
		return
	}

	// Merge env: current process env first, task env overrides.
	env := os.Environ()
	for k, v := range task.Env {
		env = append(env, k+"="+v)
	}

	cmd := exec.CommandContext(ctx, task.Command[0], task.Command[1:]...)
	cmd.WaitDelay = 5 * time.Second // bound pipe draining after process kill
	cmd.Env = env

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
