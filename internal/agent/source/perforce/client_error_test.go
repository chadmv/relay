package perforce

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

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
