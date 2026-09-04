package perforce

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"
)

// tHelper is the subset of testing.TB that fakeRunner needs to report fixture
// misses. Using an interface lets regression tests pass a spy instead of the
// real *testing.T.
type tHelper interface {
	Helper()
	Errorf(format string, args ...any)
}

// fakeRunner returns the same error TYPE execRunner does. A fake that returned
// bare errors could not exercise classifyP4Error at all once classification is
// restricted to what a p4 invocation produced, and could not model the
// args-exclusion that restriction exists for.
type fakeRunner struct {
	t         tHelper
	calls     []runCall
	out       map[string]string
	err       map[string]error
	block     map[string]bool
	streamOut map[string]string
	streamErr map[string]error
	// streamStderr is what StreamWithStderr returns for a key, INDEPENDENT of
	// whether Stream errored: p4 exits zero and still writes "no such file(s)"
	// there, and that pairing is the whole reason SyncPreempt exists.
	streamStderr map[string]string

	streamBlock map[string]<-chan struct{}

	// streamDone is incremented as Stream's LAST statement on every return path,
	// so a test may read it from another goroutine. fakeRunner.calls is NOT
	// synchronised: no test may read it or call argHistory() while Prepare is
	// still running.
	streamDone atomic.Int32
}

type runCall struct {
	cwd   string
	args  []string
	stdin string
}

func newFakeP4Fixture(t tHelper) *fakeRunner {
	return &fakeRunner{
		t:         t,
		out:       map[string]string{},
		err:       map[string]error{},
		block:     map[string]bool{},
		streamOut: map[string]string{},
		streamErr: map[string]error{},

		streamStderr: map[string]string{},

		streamBlock: map[string]<-chan struct{}{},
	}
}

func (f *fakeRunner) set(key, out string) {
	f.out[key] = out
}

func (f *fakeRunner) setErr(key string, err error) {
	f.err[key] = err
}

// setBlock makes Run block on the given args key until ctx is cancelled, then
// return ctx.Err(). Models a wedged p4 subprocess that exec.CommandContext kills
// on deadline.
func (f *fakeRunner) setBlock(key string) {
	f.block[key] = true
}

func (f *fakeRunner) setStream(key, out string) {
	f.streamOut[key] = out
}

// setStreamErr makes Stream return err for the given args key without invoking
// onLine, which is how a test drives a p4 sync FAILURE.
func (f *fakeRunner) setStreamErr(key string, err error) {
	f.streamErr[key] = err
}

// setStreamBlock makes Stream park on the given args key until release is
// closed or ctx is cancelled, modelling a long-running p4 sync. The cancel path
// sleeps before it increments streamDone, modelling a p4 child that takes a
// moment to die: a guard asserting Prepare waited for the sync goroutine is
// decorative unless both the after-the-block increment and that delay hold.
// TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished.
func (f *fakeRunner) setStreamBlock(key string, release <-chan struct{}) {
	f.streamBlock[key] = release
}

func (f *fakeRunner) setStreamStderr(key, out string) { f.streamStderr[key] = out }

func (f *fakeRunner) StreamWithStderr(ctx context.Context, cwd string, args []string, onLine func(string)) (string, error) {
	err := f.Stream(ctx, cwd, args, onLine)
	return f.streamStderr[strings.Join(args, " ")], err
}

func (f *fakeRunner) argHistory() [][]string {
	result := make([][]string, len(f.calls))
	for i, c := range f.calls {
		result[i] = c.args
	}
	return result
}

func (f *fakeRunner) Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error) {
	key := strings.Join(args, " ")
	if f.block[key] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if e, ok := f.err[key]; ok && e != nil {
		return nil, newP4CommandError(args, e, "")
	}
	if _, ok := f.out[key]; !ok {
		f.t.Helper()
		f.t.Errorf("fakeRunner.Run: no fixture for args %q (cwd=%q)", key, cwd)
		return nil, fmt.Errorf("fakeRunner: no fixture for %q", key)
	}
	var sb strings.Builder
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		sb.Write(b)
	}
	f.calls = append(f.calls, runCall{cwd: cwd, args: append([]string{}, args...), stdin: sb.String()})
	return []byte(f.out[key]), nil
}

func (f *fakeRunner) Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error {
	key := strings.Join(args, " ")
	if rel, ok := f.streamBlock[key]; ok {
		select {
		case <-rel:
		case <-ctx.Done():
			time.Sleep(50 * time.Millisecond)
			f.streamDone.Add(1)
			return ctx.Err()
		}
		f.streamDone.Add(1)
		return nil
	}
	if e, ok := f.streamErr[key]; ok && e != nil {
		f.streamDone.Add(1)
		return newP4CommandError(args, e, "")
	}
	if _, ok := f.streamOut[key]; !ok {
		f.t.Helper()
		f.t.Errorf("fakeRunner.Stream: no fixture for args %q (cwd=%q)", key, cwd)
		f.streamDone.Add(1)
		return fmt.Errorf("fakeRunner: no fixture for %q", key)
	}
	for _, line := range strings.Split(f.streamOut[key], "\n") {
		if line != "" {
			onLine(line)
		}
	}
	f.calls = append(f.calls, runCall{cwd: cwd, args: append([]string{}, args...)})
	f.streamDone.Add(1)
	return nil
}

// expectedClientName predicts the stream-bound client name that
// Provider.Prepare creates. Calls allocateShortID directly with an empty
// registry so the helper tracks any future change to the production shortID
// derivation (including the collision-resolution loop, if it ever fires).
func expectedClientName(hostname, sourceKey string) string {
	return fmt.Sprintf("relay_%s_%s", hostname, allocateShortID(sourceKey, &Registry{}))
}
