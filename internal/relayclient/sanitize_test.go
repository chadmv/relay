package relayclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// A SERVER ERROR MESSAGE IS UNTRUSTED TEXT AND ITS ESCAPES ARRIVE INTACT.
//
// handleRunScheduledJobNow answers a stored-spec validation failure with the raw
// jobspec.Validate message, which interpolates a task name the schedule's owner
// chose - "task %s: ..." - with no bound and no character stripping. Nothing
// between there and a terminal repairs it: an ESC is carried across the wire as
// the JSON escape \u001b and json.Decode turns it back into a real ESC byte
// right here, in Do. internal/cli then prints it, which is how the same
// attacker-chosen prose gets two routes to an admin's terminal - one sanitized
// (schedules show) and one not.
//
// It is cross-tenant on purpose in the case that matters: an admin running
// run-now against someone else's schedule is exactly when the text is not their
// own.
//
// THE ASSERTIONS ARE ON THREE INDEPENDENT AXES, because a sanitizer can satisfy
// any two and be wrong: the escapes are gone, the forged line is not a line, and
// every legible rune survived.
func TestDo_ServerErrorMessageCannotCarryEscapesOrForgeLines(t *testing.T) {
	const poisoned = "invalid job_spec: task \x1b[31mevil\x1b[0m: bad\nerror: nothing is wrong\u009b2K"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// Hand-written so the body is not marshalled through this package's own
		// types. json.Marshal of the string above would produce the same escapes;
		// writing them out says which spelling travels on the wire.
		_, _ = io.WriteString(w, `{"error":"invalid job_spec: task \u001b[31mevil\u001b[0m: bad\nerror: nothing is wrong\u009b2K"}`)
	}))
	defer srv.Close()

	err := NewClient(srv.URL, "t").Do(context.Background(), "POST", "/v1/scheduled-jobs/x/run-now", nil, nil)
	require.Error(t, err)
	var re *ResponseError
	require.ErrorAs(t, err, &re)
	require.Equal(t, http.StatusBadRequest, re.StatusCode)

	require.NotContains(t, re.Message, "\x1b",
		"an ESC reaching a terminal is ANSI injection: it can repaint or hide relay's own output")
	require.NotContains(t, re.Message, "\u009b",
		"U+009B is the single-character CSI; stripping ESC alone does not strip escape sequences")
	require.NotContains(t, re.Message, "\n",
		"one line: the CLI prints this after an \"error: \" prefix, so a newline forges a second line under no prefix at all")

	// AND IT IS NOT TRUNCATED. README tells an operator that run-now returns the
	// message in full, against a stored value capped at 1 KB, so length and
	// control characters have to be independent. Rune count is the instrument
	// because the mapping is one rune to one rune.
	require.Equal(t, utf8.RuneCountInString(poisoned), utf8.RuneCountInString(re.Message),
		"the fix strips characters, it does not shorten the message")

	// CONTROL: the content an operator actually needs survived. A sanitizer that
	// returned "" would pass all three assertions above.
	require.Contains(t, re.Message, "invalid job_spec: task ")
	require.Contains(t, re.Message, "evil")
	require.Contains(t, re.Message, "bad")
}

// THE LOCALLY COMPOSED FALLBACKS ARE NOT THE SUBJECT. Do writes its own message
// when the body carries no usable "error" field, and those strings are relay's
// own. This pins that the sanitizer is applied to the SERVER's text rather than
// blanket-applied to whatever Do happens to return, so a later reader can see
// which half is untrusted.
func TestDo_LocallyComposedErrorMessagesAreUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `not json`)
	}))
	defer srv.Close()

	err := NewClient(srv.URL, "t").Do(context.Background(), "GET", "/v1/jobs", nil, nil)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "server error (500)"))
}

func TestSanitizeServerText_CoversC0C1AndBidiButNotPrintableText(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"ESC", "a\x1bb", "a b"},
		{"newline", "a\nb", "a b"},
		{"tab", "a\tb", "a b"},
		{"DEL", "a\u007fb", "a b"},
		{"C1 low", "a\u0080b", "a b"},
		{"C1 CSI", "a\u009bb", "a b"},
		{"C1 high", "a\u009fb", "a b"},
		{"LRM", "a\u200eb", "a b"},
		{"RLM", "a\u200fb", "a b"},
		{"LRE", "a\u202ab", "a b"},
		{"RLO", "a\u202eb", "a b"},
		{"LRI", "a\u2066b", "a b"},
		{"PDI", "a\u2069b", "a b"},
	} {
		require.Equal(t, tc.want, sanitizeServerText(tc.in), tc.name)
	}

	// CONTROL: printable non-ASCII is not a control character. A predicate that
	// mapped everything above U+007F would satisfy every case above and mangle
	// every error message for every operator who does not write in English.
	require.Equal(t, "café 日本語 ✓ \u00a0", sanitizeServerText("café 日本語 ✓ \u00a0"))
}
