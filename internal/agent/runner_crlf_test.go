package agent

import (
	"context"
	"strings"
	"testing"

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
// Deterministic, not timing-based: sendCh is wedged full BEFORE the write and
// forcedCh is closed, so sendOrAbort has exactly one ready case.
func TestChunkWriter_AbandonedWriteDropsTheHeldByte(t *testing.T) {
	w, r, sendCh := newCRLFWriter(t, relayv1.LogStream_LOG_STREAM_STDOUT, 1)
	sendCh <- &relayv1.AgentMessage{} // wedge it full
	r.Cancel(true)

	n, err := w.Write([]byte("abc\r"))
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
