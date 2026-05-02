package perforce

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type fakeRunner struct {
	calls     []runCall
	out       map[string]string
	err       map[string]error
	streamOut map[string]string
	streamErr map[string]error
}

type runCall struct {
	args  []string
	stdin string
}

func newFakeP4Fixture() *fakeRunner {
	return &fakeRunner{
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

func (f *fakeRunner) Run(ctx context.Context, args []string, stdin io.Reader) ([]byte, error) {
	key := strings.Join(args, " ")
	if e, ok := f.err[key]; ok && e != nil {
		return nil, e
	}
	var sb strings.Builder
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		sb.Write(b)
	}
	f.calls = append(f.calls, runCall{args: append([]string{}, args...), stdin: sb.String()})
	return []byte(f.out[key]), nil
}

func (f *fakeRunner) Stream(ctx context.Context, args []string, onLine func(string)) error {
	key := strings.Join(args, " ")
	if e, ok := f.streamErr[key]; ok && e != nil {
		return e
	}
	for _, line := range strings.Split(f.streamOut[key], "\n") {
		if line != "" {
			onLine(line)
		}
	}
	f.calls = append(f.calls, runCall{args: append([]string{}, args...)})
	return nil
}

// expectedClientName predicts the stream-bound client name that
// Provider.Prepare creates. Calls allocateShortID directly with an empty
// registry so the helper tracks any future change to the production shortID
// derivation (including the collision-resolution loop, if it ever fires).
func expectedClientName(hostname, sourceKey string) string {
	return fmt.Sprintf("relay_%s_%s", hostname, allocateShortID(sourceKey, &Registry{}))
}
