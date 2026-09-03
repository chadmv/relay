package agent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)

// reservedIdentityNames are the environment names the coordinator owns in a task
// subprocess. Nothing else may supply them; see the merge in Run.
var reservedIdentityNames = [...]string{"RELAY_TASK_ID", "RELAY_JOB_ID", "RELAY_JOB_URL", "RELAY_TASK_URL"}

// isReservedIdentityName reports whether k is a name the coordinator owns for
// the platform this agent is running on. Windows resolves environment names
// case-insensitively, every other platform does not: folding everywhere would
// delete a spec key that is a genuinely distinct variable on Unix, folding
// nowhere would let a Windows spec key differing only in case supply the name
// relay owns.
// TestRunner_TheReservedNamesAreCaseFoldedExactlyWhereOsExecFoldsThem pins both
// directions on the platform it runs on.
func isReservedIdentityName(k string) bool {
	return isReservedIdentityNameFor(runtime.GOOS, k)
}

// isReservedIdentityNameFor takes goos as a parameter so both halves of the case
// rule are exercised wherever the tests run.
//
// The fold is strings.ToLower and NOT strings.EqualFold, because os/exec's
// duplicate-key rule lower-cases the key. The two disagree on U+017F, which
// EqualFold treats as an 's' - stripping a key os/exec would have carried
// through as a distinct variable.
// TestIsReservedIdentityNameFor_FoldsExactlyWhereOsExecFolds pins both.
func isReservedIdentityNameFor(goos, k string) bool {
	if goos == "windows" {
		k = strings.ToLower(k)
		for _, r := range reservedIdentityNames {
			if k == strings.ToLower(r) {
				return true
			}
		}
		return false
	}
	for _, r := range reservedIdentityNames {
		if k == r {
			return true
		}
	}
	return false
}

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

	// Merge env: current process env first, task env, then workspace env. THE
	// RESERVED NAMES ARE STRIPPED FROM BOTH MAPS, NOT MERELY OUTRANKED BY THE
	// APPEND BELOW; TestRunner_UnconfiguredCoordinatorStillRefusesASpecEnvURL and
	// its workspace twin are the legs that redden.
	//
	// A key CONTAINING "=" is refused outright rather than parsed: an entry is
	// split at a "=", so the name the child resolves need not be the string the
	// reserved-name predicate was shown - the key "RELAY_JOB_URL=x" reaches the
	// child as RELAY_JOB_URL.
	// TestRunner_ASpecEnvKeyContainingAnEqualsCannotSupplyAReservedName and its
	// workspace twin pin it.
	//
	// The append has to come after os.Environ(), which is inherited unfiltered,
	// or relay's own value loses to whatever the agent operator exported;
	// TestRunner_ACoordinatorValueBeatsAnInheritedOne is that leg. Each name is
	// appended only when its value is non-empty, so relay never sets one of them
	// to the empty string and a consumer needs one check rather than a second for
	// "set but blank".
	env := os.Environ()
	for k, v := range task.Env {
		if strings.Contains(k, "=") || isReservedIdentityName(k) {
			continue
		}
		env = append(env, k+"="+v)
	}
	for k, v := range extraEnv {
		if strings.Contains(k, "=") || isReservedIdentityName(k) {
			continue
		}
		env = append(env, k+"="+v)
	}
	if r.taskID != "" {
		env = append(env, "RELAY_TASK_ID="+r.taskID)
	}
	if task.JobId != "" {
		env = append(env, "RELAY_JOB_ID="+task.JobId)
	}
	if task.JobUrl != "" {
		env = append(env, "RELAY_JOB_URL="+task.JobUrl)
	}
	if task.TaskUrl != "" {
		env = append(env, "RELAY_TASK_URL="+task.TaskUrl)
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

		// argv[0] ONLY. Nothing in relay sanitises command arguments, so a token
		// passed as one would land here verbatim.
		// TestRunner_AStepLineNamesTheProgramAndNotItsArguments is the guard. It
		// bounds THIS surface and closes nothing: sendStepMarker above already
		// writes the whole vector into task_logs.
		log.Printf("runner: exec step %d/%d for %s: %s", step, stepTotal, r.taskID, argv[0])

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
		outW := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, stepIndex: step, stepTotal: stepTotal}
		errW := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, stepIndex: step, stepTotal: stepTotal}
		cmd.Stdout = outW
		cmd.Stderr = errW

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
		// Flush each writer's held trailing '\r' HERE. Four constraints bound a
		// REGION, not a point: anywhere between cmd.Wait returning and the end of
		// this iteration satisfies all four.
		//   - the writers are per STEP and become garbage at the end of this
		//     iteration, and both the `continue` and the `break` below come after
		//     this line, so every path flushes;
		//   - a held byte must be enqueued before the NEXT step's sendStepMarker,
		//     which is at the top of the next iteration;
		//   - a log chunk must be enqueued before sendFinalStatus (after the
		//     loop), or internal/worker/handler.go:173-183's FIFO argument
		//     becomes false and a one-byte chunk lands in AppendTaskLog's
		//     trailing-window carve-out instead of its status allow-list;
		//   - no copy goroutine may still be running: Cmd.Wait waits for the
		//     command to exit AND for the copying from stdout and stderr to
		//     complete, and WaitDelay force-closes the pipes so those copies do
		//     complete.
		// cleanupProcTree participates in NONE of the four, so where it sits in
		// the region is a free choice - AND IT GOES FIRST ON PURPOSE. flush parks
		// in sendOrAbort whenever a byte is held and sendCh is full, and on a
		// healthy-but-disconnected agent none of forcedCh, cancelledCh or
		// r.ctx.Done() ever fires, so that park lasts the whole partition.
		// cleanupProcTree closes the Job Object handle carrying
		// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE (proctree_windows.go), which is what
		// kills this step's leaked grandchildren; putting it after a flush that
		// can park leaves them running for the length of the outage. Do not
		// reorder these two back.
		// The flushes are a no-op when nothing is held, so the two earlier breaks
		// (nil argv, cmd.Start failure) need no reasoning about.
		outW.flush()
		errW.flush()

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

// errForcedAbort is returned by chunkWriter.Write when a per-task cancel
// (forced via forcedCh, or default/abandon via cancelledCh) signals while a log
// send is in flight, or the agent context is done. A non-nil Write error makes
// exec's io.Copy stop copying so cmd.Wait() returns promptly instead of waiting
// out WaitDelay. It is consumed only by exec's copy loop; the runner's terminal
// status is decided independently in Run (the cancelled branch yields FAILED),
// so this sentinel never leaks as an extra task failure.
var errForcedAbort = errors.New("relay: forced cancel aborted in-flight log write")

// chunkWriter is the io.Writer exec copies subprocess stdout/stderr into. Each
// Write builds its own buffer (exec reuses the slice between calls), collapses
// CRLF to LF in it, wraps it in a TaskLogChunk stamped with the runner's
// stream/step/epoch, and pushes it through r.sendOrAbort.
//
// THE CRLF COLLAPSE IS EXACTLY \r\n -> \n, NOT \r+\n -> \n. The wider rule would
// also remove the residue a CR-run before a newline leaves behind, but that is a
// judgement about what is VISIBLE, which is a rendering decision, and rendering
// decisions stay in the client that holds the opinion - the SPA already collapses
// carriage returns (web/src/jobs/logBuffer.ts) and the CLI deliberately wants the
// raw bytes. The narrow rule has one definition every consumer agrees on and one
// statable cost: precisely the CR of each CRLF is removed, nothing else.
//
// THE INVARIANT IS OVER THE CONCATENATION, AGAINST THE ORIGINAL BYTE POSITIONS:
// for one (step, stream) writer, the concatenation of every payload it emits
// equals the subprocess's bytes with each \r\n OF THE ORIGINAL replaced by \n.
// It is NOT the per-chunk property "no emitted chunk contains a CRLF", which is
// false on purpose: a payload of "\r\r" emits a chunk ending in '\r' (correct -
// that CR's successor is known to be another CR, not a LF), and a CR-run before
// a newline emits a CRLF at a position that did not have one. A second pass would
// be a different transform; see above.
//
// THE HOLD-BACK IS ONE BIT, NOT A BUFFER. The only byte that is ever held is a
// trailing '\r' whose successor has not been read yet, so heldCR records whether
// one is outstanding and the byte itself is a literal at the two places it is
// re-emitted. A CRLF can straddle a Write boundary - exec hands over whatever the
// pipe had - so without the hold-back a per-Write replace silently misses every
// pair split across two reads. The held CR is folded into the next Write's buffer
// BEFORE the scan, so no pair straddles by the time the scan runs. mu guards
// heldCR; see flush for why the lock is insurance rather than correctness.
//
// Write has THREE outcomes, not two. On a successful enqueue it returns
// (len(p), nil) so exec keeps copying until EOF (unchanged slow-consumer
// behavior). On a payload consumed entirely into the hold-back - which happens on
// exactly one input, a lone "\r" with nothing already held - it returns (len(p), nil)
// having enqueued NOTHING; that is what bufio.Writer does, io.Writer requires
// n < len(p) only alongside a non-nil error, and emitting no chunk strengthens
// the never-emit-an-empty-chunk guard below rather than breaking it. If a
// per-task cancel has closed r.forcedCh or r.cancelledCh (or the agent context
// is done), the enqueue is abandoned and Write returns errForcedAbort so exec's
// io.Copy stops and cmd.Wait() returns promptly instead of waiting out
// WaitDelay; the held CR goes with the discarded chunk and is never re-armed.
//
// flush() MUST be called after cmd.Wait() returns for the step that created this
// writer, for BOTH writers, or a trailing '\r' at the end of a step is silently
// dropped. exec will not call it; see flush's own comment.
type chunkWriter struct {
	r         *Runner
	stream    relayv1.LogStream
	stepIndex int32
	stepTotal int32

	mu     sync.Mutex
	heldCR bool // a trailing '\r' whose successor is not yet known is held back
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil // match the old pipeLog n>0 guard: never emit an empty chunk
	}

	// Fold the held CR in and CLEAR the flag in the same breath. Its lifetime is
	// exactly one Write, and newHeldCR below is armed only after a successful
	// enqueue, so no early return - including the errForcedAbort one the abort
	// path takes - can leave a byte owned by a writer nobody will call again.
	w.mu.Lock()
	chunk := make([]byte, 0, 1+len(p))
	if w.heldCR {
		chunk = append(chunk, '\r')
	}
	chunk = append(chunk, p...)
	w.heldCR = false
	w.mu.Unlock()

	newHeldCR := false
	if chunk[len(chunk)-1] == '\r' {
		newHeldCR = true
		chunk = chunk[:len(chunk)-1]
	}
	chunk = collapseCRLFInPlace(chunk)

	if len(chunk) == 0 {
		// Reachable on exactly one input: nothing held and p == "\r".
		// collapseCRLFInPlace keeps a byte for every pair it collapses, so only
		// the hold-back above can empty the buffer.
		w.mu.Lock()
		w.heldCR = newHeldCR
		w.mu.Unlock()
		return len(p), nil
	}

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
		// the runner is tearing down regardless. newHeldCR is deliberately NOT
		// armed - the writer must not send after the abort path decided to stop.
		return 0, errForcedAbort
	}
	w.mu.Lock()
	w.heldCR = newHeldCR
	w.mu.Unlock()
	return len(p), nil
}

// flush enqueues a held trailing '\r' as its own chunk and clears the hold-back.
//
// IT MUST BE CALLED EXPLICITLY, IMMEDIATELY AFTER cmd.Wait() RETURNS, FOR BOTH
// WRITERS, INSIDE THE PER-STEP LOOP. os/exec closes only the pipes it created
// and never calls Close on a caller-supplied Stdout/Stderr, so there is no close
// hook and naming this Close() would imply a call that does not happen. The
// writers are per STEP, so a byte not flushed before the iteration ends is
// silently lost - no error, no log line - and a flush deferred to the end of Run
// would find the step's writer already replaced. See makePrepareProgressFn's
// flush, which is the same shape and the same hazard.
//
// SENDS THROUGH sendOrAbort, NEVER r.send. flush runs after cmd.Wait on the
// cancel path too, and r.send is bounded only by the AGENT context, so a
// per-task forced cancel with a wedged sendCh would park Run until agent
// shutdown - the exact wedge sendFinalStatus's cancelled branch and sendInventory
// were both written to avoid. After an abandoned Write there is nothing held, so
// this sends nothing: the writer must not send after the abort path decided to
// stop sending.
//
// The lock is released before the send, and what leaves the critical section is
// a bool, not a slice header - so nothing the outgoing chunk points at was ever
// reachable from the writer. Holding the lock across a bounded-but-slow enqueue
// would be the first step toward the lock-scope problems the Invariants exist to
// prevent, and handing a shared backing array out from under it would be the
// second.
func (w *chunkWriter) flush() {
	w.mu.Lock()
	held := w.heldCR
	w.heldCR = false
	w.mu.Unlock()
	if !held {
		return
	}
	w.r.sendOrAbort(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{
				TaskId:    w.r.taskID,
				Stream:    w.stream,
				Content:   []byte{'\r'},
				Epoch:     w.r.epoch,
				StepIndex: w.stepIndex,
				StepTotal: w.stepTotal,
			},
		},
	})
}

// collapseCRLFInPlace removes the CR of every CRLF pair in b IN PLACE and returns
// the shortened prefix, which ALIASES b. The name carries the precondition
// because prose does not: b must be a buffer the caller owns, and the caller must
// stop using b afterwards. chunkWriter.Write already has to copy exec's slice
// (exec reuses it between calls), so no second allocation is needed. The
// compaction only ever skips bytes, so the write index never overtakes the read
// index.
//
// The IndexByte fast path is not a micro-optimisation for its own sake: it is
// every write on every Linux agent, where a full forward scan would buy nothing.
func collapseCRLFInPlace(b []byte) []byte {
	i := bytes.IndexByte(b, '\r')
	if i < 0 {
		return b
	}
	out := b[:i]
	for ; i < len(b); i++ {
		if b[i] == '\r' && i+1 < len(b) && b[i+1] == '\n' {
			continue
		}
		out = append(out, b[i])
	}
	return out
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
	// Per-task cancel (forced OR default): best-effort, bounded enqueue so Run
	// returns even when sendCh is wedged full. Cancel(true) sets r.forced AND
	// r.cancelled; Cancel(false) sets r.cancelled; Abandon set r.abandoned and
	// already returned above. So r.cancelled covers both cancel kinds. Try the
	// enqueue first and only abandon when sendCh is genuinely full; dropping the
	// message is safe because the server's CancelJobTasks already set the task
	// failed and bumped assignment_epoch, so this terminal message (carrying the
	// old r.epoch) is epoch-fenced out.
	if r.cancelled.Load() {
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
// enqueue if a forced cancel (forcedCh) or a per-task default cancel / abandon
// (cancelledCh) has signalled, or the agent context is done. It returns true on a
// successful enqueue and false if it abandoned. Both of chunkWriter's enqueue
// paths use this - Write and flush; all other callers use send, so their
// blocking discipline is unchanged. flush is the second caller BECAUSE it runs
// after cmd.Wait on the cancel path too, where r.send (bounded only by the agent
// context) would park Run until agent shutdown.
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
	case <-r.cancelledCh:
		// Default cancel or abandon in progress; abandon this chunk so cmd.Wait
		// can return instead of parking unbounded on a wedged sendCh.
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

// sendInventory reports a workspace inventory entry to the coordinator. On a
// per-task cancel or abandon the cleanup runs through the deferred path while
// sendCh may still be wedged full and r.ctx (the parent/agent context) is not
// done, so a blocking send would park until agent shutdown. Mirror
// sendFinalStatus's cancelled branch: a room-first, bounded try-send that
// abandons the entry best-effort when sendCh is full. Dropping it is safe -
// Finalize already reconciled the workspace locally, the server is
// authoritative, and the entry is recomputed on next workspace use. Normal
// completion (none of cancelled/abandoned set) keeps the blocking send so
// inventory is still delivered under a merely-slow-but-live consumer.
func (r *Runner) sendInventory(e source.InventoryEntry) {
	msg := &relayv1.AgentMessage{Payload: &relayv1.AgentMessage_WorkspaceInventory{
		WorkspaceInventory: &relayv1.WorkspaceInventoryUpdate{
			SourceType:   e.SourceType,
			SourceKey:    e.SourceKey,
			ShortId:      e.ShortID,
			BaselineHash: e.BaselineHash,
			LastUsedAt:   e.LastUsedAt.Format("2006-01-02T15:04:05Z07:00"),
			Deleted:      e.Deleted,
		},
	}}
	if r.cancelled.Load() || r.abandoned.Load() {
		select {
		case r.sendCh <- msg:
		default:
			// sendCh full and wedged; abandon best-effort. Cleanup path only;
			// server is authoritative and Finalize already reconciled the workspace.
		}
		return
	}
	r.send(msg)
}
