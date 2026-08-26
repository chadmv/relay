package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// drainByStream joins the Content of every TaskLogChunk currently on ch, in FIFO
// order, keyed by stream. It stops when the channel is empty.
//
// THE JOIN IS THE ASSERTION SURFACE ON PURPOSE. The contract chunkWriter has is
// about the CONCATENATION of what one (step, stream) writer emits - "the
// subprocess's bytes with each \r\n OF THE ORIGINAL replaced by \n" - and it is
// deliberately NOT a per-chunk property. A payload of "\r\r" legitimately emits
// a chunk that ends in '\r', and the emitted bytes can contain a \r\n at a
// position that did not have one. Asserting "no chunk contains CRLF" would fail
// on legitimate input; asserting it only on inputs where it happens to hold
// would pass vacuously and pin the wrong contract (spec R5/D6).
func drainByStream(t *testing.T, ch chan *relayv1.AgentMessage) map[relayv1.LogStream]string {
	t.Helper()
	bufs := map[relayv1.LogStream]*strings.Builder{}
	for {
		select {
		case msg := <-ch:
			l := msg.GetTaskLog()
			if l == nil {
				continue
			}
			if bufs[l.Stream] == nil {
				bufs[l.Stream] = &strings.Builder{}
			}
			bufs[l.Stream].Write(l.Content)
		default:
			out := map[relayv1.LogStream]string{}
			for k, v := range bufs {
				out[k] = v.String()
			}
			return out
		}
	}
}

func newCRLFWriter(t *testing.T, stream relayv1.LogStream, capacity int) (*chunkWriter, *Runner, chan *relayv1.AgentMessage) {
	t.Helper()
	sendCh := make(chan *relayv1.AgentMessage, capacity)
	r, _ := newRunner("t-crlf", 0, sendCh, context.Background(), 0)
	return &chunkWriter{r: r, stream: stream, stepIndex: 1, stepTotal: 1}, r, sendCh
}

// TestChunkWriter_StraddledCRLFCollapsesAcrossWriteBoundary is THE
// discriminating test for the whole of Part 2. An entry is not a line:
// chunkWriter copies whatever os/exec hands it, so a CRLF can straddle a Write
// boundary - chunk N ends "\r", chunk N+1 begins "\n" - and a stateless
// bytes.ReplaceAll cannot see that pair.
//
// THE STRADDLED PAIR IS FIRST, and that is load-bearing here in a way it is not
// in the web table: these two Writes share ONE writer and one piece of held
// state, so a discriminating input placed after a benign one cannot detect an
// early-exit mutation.
func TestChunkWriter_StraddledCRLFCollapsesAcrossWriteBoundary(t *testing.T) {
	w, _, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 64)

	for _, p := range [][]byte{[]byte("alpha\r"), []byte("\nbravo\r\ncharlie")} {
		n, err := w.Write(p)
		require.NoError(t, err)
		require.Equal(t, len(p), n, "io.Copy stops with ErrShortWrite on a short write that carries a nil error")
	}

	got := drainByStream(t, sendCh)
	require.Equal(t, "alpha\nbravo\ncharlie", got[relayv1.LogStream_LOG_STREAM_STDOUT],
		"the straddled pair must collapse; a per-Write ReplaceAll leaves the CR that ended the first chunk")
}

// TestChunkWriter_LoneCarriageReturnIsConsumedWithoutEnqueueing is spec T2-B.
// The write of a lone '\r' consumes its byte into the writer's own state and
// enqueues NOTHING - the same thing bufio.Writer does, and not an io.Writer
// violation: io.Writer requires n < len(p) only when a non-nil error is
// returned. Emitting no chunk STRENGTHENS the never-emit-an-empty-chunk
// invariant at runner.go's len(p) == 0 guard rather than breaking it.
func TestChunkWriter_LoneCarriageReturnIsConsumedWithoutEnqueueing(t *testing.T) {
	w, _, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 64)

	n, err := w.Write([]byte("\r"))
	require.NoError(t, err)
	require.Equal(t, 1, n, "reporting fewer than len(p) with a nil error stalls io.Copy")
	require.Len(t, sendCh, 0, "a held byte must not be enqueued as a chunk of its own")

	n, err = w.Write([]byte("\n"))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := drainByStream(t, sendCh)
	require.Equal(t, "\n", got[relayv1.LogStream_LOG_STREAM_STDOUT],
		"the held CR and the following LF are one CRLF pair and collapse to a single LF")
}

// TestChunkWriter_AbandonedWriteDropsTheHeldByte is spec T2-E and D10. A Write
// either emits the held byte or drops it WITH ITS OWN CHUNK - never both, never
// neither-with-a-successor. Arming the new held byte before the enqueue instead
// of after would leave a '\r' owned by a writer whose abort path has already
// decided to stop sending.
//
// THE FIRST WRITE IS THE FIXTURE, NOT SETUP NOISE. held must already be armed
// when the abandoned write runs, or the assertion is vacuous in both directions:
// with nothing held, held is nil before the write and nil after it whether or
// not Write clears it, so the test stays green against a chunkWriter that never
// clears held at all. Pre-arming makes it kill both mutants - the missing clear
// (held survives the abort as "\r") and arming the new byte before the enqueue
// instead of after.
//
// Deterministic, not timing-based: the pre-arming write also fills the one-slot
// sendCh, and forcedCh is closed before the second write, so sendOrAbort has
// exactly one ready case.
func TestChunkWriter_AbandonedWriteDropsTheHeldByte(t *testing.T) {
	w, r, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 1)

	n, err := w.Write([]byte("abc\r"))
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Len(t, w.held, 1, "the pre-arming write must leave a trailing CR held")
	require.Len(t, sendCh, 1, "the pre-arming write enqueues its own chunk and wedges the channel full")

	r.Cancel(true)

	n, err = w.Write([]byte("def\r"))
	require.ErrorIs(t, err, errForcedAbort)
	require.Equal(t, 0, n, "an abandoned Write reports zero bytes consumed alongside its error")
	require.Empty(t, w.held,
		"the held byte is discarded with the abandoned chunk; arming it before the enqueue re-emits it after the abort")
	require.Len(t, sendCh, 1, "nothing may be enqueued once the chunk is abandoned")
}

// TestChunkWriter_StdoutAndStderrHoldIndependently is spec T2-F. os/exec drives
// each of the two writers from its own copy goroutine and they are distinct
// pointers, so hold-back state is PER WRITER. This is the test that catches a
// "hoist held onto the Runner" refactor, which would prepend one stream's held
// carriage return to the other stream's next chunk.
func TestChunkWriter_StdoutAndStderrHoldIndependently(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	r, _ := newRunner("t-two-streams", 0, sendCh, context.Background(), 0)
	out := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDOUT, stepIndex: 1, stepTotal: 1}
	errw := &chunkWriter{r: r, stream: relayv1.LogStream_LOG_STREAM_STDERR, stepIndex: 1, stepTotal: 1}

	// The stderr write lands BETWEEN stdout's hold and its release. That is the
	// whole fixture: with shared state, stdout's held '\r' arrives on stderr.
	for _, step := range []struct {
		w *chunkWriter
		p string
	}{
		{out, "out\r"},
		{errw, "err\r\nline\n"},
		{out, "put\r\n"},
	} {
		n, err := step.w.Write([]byte(step.p))
		require.NoError(t, err)
		require.Equal(t, len(step.p), n)
	}

	got := drainByStream(t, sendCh)
	require.Equal(t, "err\nline\n", got[relayv1.LogStream_LOG_STREAM_STDERR],
		"stderr must never see stdout's held byte")
	// The interior '\r' SURVIVES: its successor is 'p', not '\n', so it is not
	// part of a CRLF pair and this transform does not touch it. Collapsing it
	// would be a judgement about visible content, which stays in the client.
	require.Equal(t, "out\rput\n", got[relayv1.LogStream_LOG_STREAM_STDOUT])
}

// TestCRLFHelperProcess IS NOT A TEST. It is the SUBPROCESS the two wiring tests
// exec: the test binary re-executes itself with -test.run pointed here, so the
// producer is Go code writing exact bytes and the same bytes reach the runner on
// Windows and on Linux. Go performs no newline translation.
//
// A SHELL PRODUCER CANNOT CARRY THIS ASSERTION. `cmd /c echo` emits CRLF
// natively on Windows and never on Linux, so a CRLF assertion behind a
// runtime.GOOS switch is vacuously green on CI and meaningful only on a
// developer's machine - the platform-gated-verification trap, inverted. That is
// why this file carries no build tag and no GOOS switch (spec D15).
//
// os.Exit(0) IS NOT OPTIONAL: without it the testing framework appends "PASS\nok
// ..." to the very stdout the parent asserts on. Nothing is written before the
// test body runs in non-verbose mode, and a raw test binary does not read
// GOFLAGS (that is the go command's variable), so the child's stdout is exactly
// what this function writes.
func TestCRLFHelperProcess(t *testing.T) {
	mode := os.Getenv("RELAY_CRLF_HELPER")
	if mode == "" {
		return // an ordinary test run; this process is not the helper
	}
	switch mode {
	case "crlf":
		// Two CRLF pairs and a bare trailing CR. The middle CR-CR-LF is the
		// shape the whole slice turns on.
		_, _ = os.Stdout.Write([]byte("a\r\nb\r\r\nc\r"))
		// STDERR CARRIES ITS OWN TRAILING CR, and writing to two streams is the
		// whole reason this mode exists in its current form. outW.flush() and
		// errW.flush() are two independent call sites; with stdout as the only
		// assertion subject, deleting errW.flush() left every package green.
		_, _ = os.Stderr.Write([]byte("d\r\ne\r"))
	case "trailing-cr":
		_, _ = os.Stdout.Write([]byte("step-one\r"))
	}
	os.Exit(0)
}

// crlfHelperCmd returns the argv and env that re-exec this test binary as the
// helper above. os.Args[0] under `go test` is the built test binary, an absolute
// path. The sentinel travels through DispatchTask.Env, which Run merges into the
// child's environment (runner.go:155-162), so the parent's own environment is
// never mutated.
func crlfHelperCmd(mode string) ([]string, map[string]string) {
	return []string{os.Args[0], "-test.run=^TestCRLFHelperProcess$"},
		map[string]string{"RELAY_CRLF_HELPER": mode}
}

// TestRunner_CRLFFlushIsWiredAndPrecedesTheTerminalStatus is spec T2-C, and it
// is what makes M6 and M7 killable: unit-testing flush() directly, or asserting
// that the method exists, proves nothing about the CALL SITE.
//
// IT ASSERTS ON BOTH STREAMS BECAUSE THERE ARE TWO CALL SITES. Run builds a
// stdout writer and a stderr writer per step and flushes each one; a wiring test
// that reads only LOG_STREAM_STDOUT pins outW.flush() and says nothing about
// errW.flush().
func TestRunner_CRLFFlushIsWiredAndPrecedesTheTerminalStatus(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	argv, env := crlfHelperCmd("crlf")
	r, runCtx := newRunner("t-crlf-wire", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId:   "t-crlf-wire",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	})

	msgs := collectMessages(sendCh, 1500*time.Millisecond)
	require.NotEmpty(t, msgs)

	// Everything the step emitted BEFORE the terminal status, in FIFO order.
	// internal/worker/handler.go:173-183 bounds AppendTaskLog's trailing window
	// on exactly this ordering: a chunk enqueued before sendFinalStatus cannot
	// outlive the terminal status. A flush hoisted past the loop makes that
	// sentence false and pushes a one-byte chunk into the trailing-window
	// carve-out instead of the status allow-list.
	term := -1
	for i, m := range msgs {
		if ts := m.GetTaskStatus(); ts != nil {
			switch ts.Status {
			case relayv1.TaskStatus_TASK_STATUS_DONE,
				relayv1.TaskStatus_TASK_STATUS_FAILED,
				relayv1.TaskStatus_TASK_STATUS_TIMED_OUT:
				term = i
			}
		}
	}
	require.GreaterOrEqual(t, term, 0, "the task must reach a terminal status")

	var beforeOut, beforeErr strings.Builder
	for _, m := range msgs[:term] {
		l := m.GetTaskLog()
		if l == nil {
			continue
		}
		switch l.Stream {
		case relayv1.LogStream_LOG_STREAM_STDOUT:
			beforeOut.Write(l.Content)
		case relayv1.LogStream_LOG_STREAM_STDERR:
			beforeErr.Write(l.Content)
		}
	}
	// Drop the synthetic step marker, which is always the first stdout content
	// and is terminated by the first '\n' in the joined stream. stderr carries no
	// marker, so its join is the subprocess's bytes exactly.
	_, payload, ok := strings.Cut(beforeOut.String(), "\n")
	require.True(t, ok, "expected the step marker line then the subprocess bytes; got %q", beforeOut.String())

	// THE EXPECTED VALUE CONTAINS A CR-LF AND THAT IS NOT A TYPO. The transform is
	// defined on the ORIGINAL byte positions. The input "a\r\nb\r\r\nc\r" has two
	// CRLF pairs; removing both leaves "a\nb" + "\r" + "\nc" - a CRLF at a
	// position that did not have one. "Correcting" this to "a\nb\nc\r" silently
	// changes the design to \r+\n -> \n, which the spec rejects on purpose (6.2):
	// that is a judgement about visible content, and visible-content judgements
	// stay in the client that holds the opinion. The residue is removed by
	// web/src/jobs/logBuffer.ts, which strips ALL trailing carriage returns.
	//
	// The trailing "c\r" is the FLUSHED held byte. Swallowing it would be silent
	// loss - Write would have reported a byte consumed that appears nowhere - and
	// it would break the concatenation invariant.
	require.Equal(t, "a\nb\r\nc\r", payload)

	// THE SECOND CALL SITE, WHICH IS NOT THE FIRST ONE. Run creates two writers
	// per step and flushes each; asserting only on stdout leaves errW.flush()
	// unpinned, and deleting that one line kept all 21 packages green. The
	// assertion has to come through Run's per-step loop for the same reason the
	// comment above gives: calling flush() on a writer the test built itself
	// proves the method works, not that anything calls it.
	require.Equal(t, "d\ne\r", beforeErr.String(),
		"stderr's held trailing CR was never flushed; errW.flush() is a call site of its own")
}

// TestRunner_HeldCarriageReturnIsFlushedBeforeTheNextStepMarker is spec T2-D and
// it pins the flush call site as PER STEP. The writers are constructed fresh
// inside the command loop and become garbage at the end of each iteration, so a
// flush hoisted to the end of Run finds step 1's writer already replaced and
// loses its byte outright.
func TestRunner_HeldCarriageReturnIsFlushedBeforeTheNextStepMarker(t *testing.T) {
	sendCh := make(chan *relayv1.AgentMessage, 64)
	argv, env := crlfHelperCmd("trailing-cr")
	r, runCtx := newRunner("t-crlf-steps", 0, sendCh, context.Background(), 0)
	r.Run(runCtx, &relayv1.DispatchTask{
		TaskId: "t-crlf-steps",
		Commands: []*relayv1.CommandLine{
			{Argv: argv},
			{Argv: echoArgv("second")},
		},
		Env: env,
	})

	joined := collectStdoutLogs(collectMessages(sendCh, 2500*time.Millisecond))

	held := strings.Index(joined, "step-one\r")
	require.GreaterOrEqual(t, held, 0,
		"step 1's held trailing CR was never flushed; logs:\n%q", joined)
	marker2 := strings.Index(joined, "=== relay step 2/2")
	require.GreaterOrEqual(t, marker2, 0, "step 2 must have run; logs:\n%q", joined)
	require.Less(t, held, marker2,
		"the held byte must be enqueued before the next step's marker")
}

// TestChunkWriter_FlushIsBoundedByTheCancelChannels is spec T2-G / D8. flush()
// runs after cmd.Wait() on the CANCEL path too, and r.send is bounded only by
// the AGENT context - not the run context - so a per-task cancel with a wedged
// sendCh would park Run until agent shutdown. That is precisely the wedge
// sendFinalStatus's cancelled branch and sendInventory were both written to
// avoid, and it would delay the terminal status indefinitely.
//
// The 2s bound is not a timing assertion in disguise: correct code returns in
// microseconds and the mutant parks FOREVER, so there is no margin to tune.
func TestChunkWriter_FlushIsBoundedByTheCancelChannels(t *testing.T) {
	w, r, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 2)

	n, err := w.Write([]byte("abc\r"))
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Len(t, w.held, 1, "the trailing CR must be held for the next write")

	for len(sendCh) < cap(sendCh) {
		sendCh <- &relayv1.AgentMessage{} // wedge it full
	}
	r.Cancel(false) // closes cancelledCh; r.ctx is Background and stays live

	done := make(chan struct{})
	go func() { defer close(done); w.flush() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush parked on a wedged sendCh: it must use sendOrAbort, not the agent-context-only r.send")
	}
	require.Len(t, sendCh, cap(sendCh), "an abandoned flush must enqueue nothing")
}
