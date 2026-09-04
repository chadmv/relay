package perforce

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The framing of a failed p4 invocation is depended on by every classification
// fixture, so it is pinned against the REAL producer rather than a hand-written
// copy. The underlying text is left to the platform (Go spells the not-found
// error with $PATH or %PATH%); what is pinned here is the framing bytes and the
// fact that the wrapped error survives errors.Is.
func TestExecRunner_RunFramesArgsUnderlyingAndStderr(t *testing.T) {
	e := &execRunner{binary: "relay-no-such-p4-binary"}
	_, err := e.Run(context.Background(), "", []string{"sync", "//depot/x/..."}, nil)
	require.Error(t, err)

	got := err.Error()
	assert.True(t, strings.HasPrefix(got, "p4 sync //depot/x/...: "),
		"args are joined with a space behind a `p4 ` prefix and a `: ` separator; got %q", got)
	assert.True(t, strings.HasSuffix(got, " (stderr: )"),
		"stderr is rendered in a trailing parenthesised segment; got %q", got)
	assert.ErrorIs(t, err, exec.ErrNotFound,
		"the underlying error must stay reachable through errors.Is")
}

// The rendered string is a contract: every classification fixture and every
// operator-facing p4 failure message depends on it. Pinned against the exact
// format the inline fmt.Errorf used before the fields were split apart.
func TestP4CommandError_RendersTheSameStringTheInlineWrapDid(t *testing.T) {
	args := []string{"-c", "relay_h_ab12", "sync", "//s/x/...@12345"}
	underlying := errors.New("exit status 1")
	stderr := "Perforce client error: Connect to server failed."

	got := newP4CommandError(args, underlying, stderr)
	want := fmt.Errorf("p4 %s: %w (stderr: %s)", strings.Join(args, " "), underlying, stderr)

	assert.Equal(t, want.Error(), got.Error())
	assert.ErrorIs(t, got, underlying, "Unwrap must keep errors.Is working for callers")
}

// Error() renders the underlying error with %v and so tolerates a nil one;
// anything reading .Error() off that field directly does not. The two must not
// disagree, or a classification turns an agent-side error path into a panic.
func TestClassifyP4Error_ToleratesANilUnderlyingErrorJustLikeErrorDoes(t *testing.T) {
	e := newP4CommandError([]string{"sync", "//s/x"}, nil, "no space left on device")

	require.NotPanics(t, func() { _ = e.Error() })
	require.NotPanics(t, func() { _ = classifyP4Error(e) })
	assert.Contains(t, classifyP4Error(e).Error(), "out of disk space",
		"stderr still classifies when there is no underlying error")
}

// ResolveHead has returns that are NOT p4CommandError - a parse failure and a
// strconv error - and Provider.Prepare wraps them with the job's own depot path.
// Driven through Prepare rather than a hand-built error: an assertion built from
// a p4CommandError cannot see this, because having one in the chain is the very
// condition that triggers the exclusion.
func TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified(t *testing.T) {
	fr := newFakeP4Fixture(t)
	// Output that does not match changeFirstLine, so ResolveHead returns
	// fmt.Errorf("could not parse %q", line).
	fr.set("changes -m1 //depot/disk full/...#head", "no changes.\n")
	client := expectedClientName("h", "//depot/disk full")
	fr.set("client -o -S //depot/disk full "+client, "")
	fr.set("client -i", "Client saved.\n")

	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//depot/disk full",
			Sync:   []*relayv1.SyncEntry{{Path: "//depot/disk full/...", Rev: "#head"}},
		},
	}}

	_, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "out of disk space",
		"a spec path is not evidence about the disk; got %v", err)
	assert.Contains(t, err.Error(), "could not parse", "the real cause must survive")
}

// The other half of the same change. execRunner.Stream's early returns are the
// only route by which a missing p4 binary reaches the sync path, and excluding
// non-p4CommandError errors from classification removes their guidance unless
// they carry the structured type.
func TestClassifyP4Error_AMissingBinaryOnTheSyncPathStillClassifies(t *testing.T) {
	e := &execRunner{binary: "relay-no-such-p4-binary"}
	err := e.Stream(context.Background(), "", []string{"-c", "c1", "sync", "-q", "//s/x/..."}, func(string) {})
	require.Error(t, err)

	// Exactly how Provider.Prepare wraps a SyncStream failure.
	got := classifyP4Error(fmt.Errorf("p4 sync: %w", err))
	assert.Contains(t, got.Error(), "p4 binary not found on PATH",
		"a missing binary on the sync path must keep its operator guidance; got %v", got)
}
