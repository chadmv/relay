package perforce

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// tHelper is the subset of testing.TB that fakeRunner needs to report fixture
// misses. Using an interface lets regression tests pass a spy instead of the
// real *testing.T.
type tHelper interface {
	Helper()
	Errorf(format string, args ...any)
}

type fakeRunner struct {
	t         tHelper
	calls     []runCall
	out       map[string]string
	err       map[string]error
	streamOut map[string]string
	streamErr map[string]error
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
		streamOut: map[string]string{},
		streamErr: map[string]error{},
	}
}

func (f *fakeRunner) set(key, out string) {
	f.out[key] = out
}

func (f *fakeRunner) setErr(key string, err error) {
	f.err[key] = err
}

func (f *fakeRunner) setStream(key, out string) {
	f.streamOut[key] = out
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
	if e, ok := f.err[key]; ok && e != nil {
		return nil, e
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
	if e, ok := f.streamErr[key]; ok && e != nil {
		return e
	}
	if _, ok := f.streamOut[key]; !ok {
		f.t.Helper()
		f.t.Errorf("fakeRunner.Stream: no fixture for args %q (cwd=%q)", key, cwd)
		return fmt.Errorf("fakeRunner: no fixture for %q", key)
	}
	for _, line := range strings.Split(f.streamOut[key], "\n") {
		if line != "" {
			onLine(line)
		}
	}
	f.calls = append(f.calls, runCall{cwd: cwd, args: append([]string{}, args...)})
	return nil
}

// expectedClientName predicts the stream-bound client name that
// Provider.Prepare creates. Calls allocateShortID directly with an empty
// registry so the helper tracks any future change to the production shortID
// derivation (including the collision-resolution loop, if it ever fires).
func expectedClientName(hostname, sourceKey string) string {
	return fmt.Sprintf("relay_%s_%s", hostname, allocateShortID(sourceKey, &Registry{}))
}
