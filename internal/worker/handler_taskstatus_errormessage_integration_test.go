//go:build integration

package worker_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three storability properties the coordinator must give an agent-supplied
// message before it can become a task_logs row, asserted at the layer that
// produces it. They are also exercised through the handler below, but a Postgres
// Bind failure is indistinguishable at the handler from any other dropped write,
// so only a test at this layer can say WHICH property broke.
func TestSanitizeAgentErrorMessage_BoundsAndValidity(t *testing.T) {
	t.Run("a short ascii message is unchanged", func(t *testing.T) {
		require.Equal(t, "boom", worker.SanitizeAgentErrorMessageForTest("boom"))
	})

	t.Run("a NUL byte is removed", func(t *testing.T) {
		got := worker.SanitizeAgentErrorMessageForTest("boom\x00boom")
		require.Equal(t, "boomboom", got)
		require.NotContains(t, got, "\x00")
	})

	t.Run("an oversized message is cut at the bound and stays valid UTF-8", func(t *testing.T) {
		// A three-byte rune, so the bound does NOT fall on a rune boundary: with
		// MaxAgentErrorMessageBytes not a multiple of 3, a naive msg[:N] cut lands
		// mid-rune and produces invalid UTF-8. That is the discriminating property;
		// a one-byte input, or a two-byte rune with an even bound, is green under
		// the naive cut and proves nothing. The rune is written as an escape
		// deliberately - a raw non-ASCII byte in this file is unverifiable by eye.
		in := strings.Repeat("\u20ac", worker.MaxAgentErrorMessageBytes)
		got := worker.SanitizeAgentErrorMessageForTest(in)
		assert.True(t, utf8.ValidString(got), "the truncated message must be valid UTF-8")
		assert.LessOrEqual(t, len(got), worker.MaxAgentErrorMessageBytes)
		assert.Greater(t, len(got), worker.MaxAgentErrorMessageBytes-4,
			"the cut must be AT the bound, not far below it")
		assert.True(t, strings.HasPrefix(in, got), "truncation must keep a prefix of the input")
	})

	t.Run("invalid UTF-8 on the wire is made valid", func(t *testing.T) {
		got := worker.SanitizeAgentErrorMessageForTest("ok\xff\xfe tail")
		require.True(t, utf8.ValidString(got))
	})
}
