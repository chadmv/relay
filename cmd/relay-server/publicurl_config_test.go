package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParsePublicURL_AcceptsAndNormalizes covers every accepted shape and the
// normalization contract jobURL and taskURL depend on: the returned base NEVER
// ends in a slash, which is why those two joiners can concatenate with no
// separator logic at all.
func TestParsePublicURL_AcceptsAndNormalizes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"unset disables the feature", "", ""},
		{"whitespace-only is the same as unset", "   ", ""},
		{"bare origin", "https://relay.example.com", "https://relay.example.com"},
		{"one trailing slash", "https://relay.example.com/", "https://relay.example.com"},
		{"several trailing slashes", "https://relay.example.com///", "https://relay.example.com"},
		{"scheme is lower-cased, host case is left alone", "HTTPS://Relay.Example.com", "https://Relay.Example.com"},
		{"http and an explicit port", "http://10.0.0.5:8080", "http://10.0.0.5:8080"},
		{"path prefix, trailing slash trimmed", "https://ops.example.com/relay/", "https://ops.example.com/relay"},
		{"surrounding whitespace is trimmed", "  https://relay.example.com  ", "https://relay.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePublicURL("RELAY_PUBLIC_URL", tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.False(t, strings.HasSuffix(got, "/"),
				"jobURL and taskURL concatenate with no separator logic; a trailing slash here "+
					"produces a double slash in every link relay publishes")
		})
	}
}

// TestParsePublicURL_Rejects is the fail-closed half. Each row is a value an
// operator could plausibly type, and each is refused at boot rather than
// warned about and disabled - a warn-and-disable typo is indistinguishable
// from never having set the variable at all.
//
// The tab and newline rows are written as Go escapes deliberately: a raw
// control byte in a source literal is unverifiable by eye.
func TestParsePublicURL_Rejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"userinfo is a phishing shape in a value relay publishes", "https://relay.example.com@evil.example/"},
		{"non-http scheme", "ftp://relay.example.com"},
		{"no scheme at all leaves the host empty", "relay.example.com"},
		{"query string cannot have a path appended", "https://relay.example.com/?x=1"},
		{"fragment cannot have a path appended", "https://relay.example.com/#frag"},
		// A space in the HOST and a space in the PATH are two different rows,
		// and only the second one discriminates: url.Parse rejects the first
		// itself, so the host row stays green with the pre-parse check moved
		// below url.Parse. url.Parse ACCEPTS the second and percent-encodes it
		// to /re%20lay, so nothing but the pre-parse check refuses it.
		{"space in the host", "https://relay example.com"},
		{"space in the path, which url.Parse would silently percent-encode", "https://ops.example.com/re lay"},
		{"embedded tab", "https://relay.example.com\tx"},
		{"embedded newline", "https://relay.example.com\nX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePublicURL("RELAY_PUBLIC_URL", tc.raw)
			require.Error(t, err)
			require.Empty(t, got, "a rejected value must not also return a usable base")
			require.Contains(t, err.Error(), "RELAY_PUBLIC_URL",
				"the message must name the variable, or an operator cannot tell which setting "+
					"stopped the boot")
		})
	}
}

// TestParsePublicURL_RejectionDoesNotLeakAPassword is the only test that pins
// the redaction rule. The message goes to a server log an operator reads and
// ships; the value it is refusing may carry a credential.
func TestParsePublicURL_RejectionDoesNotLeakAPassword(t *testing.T) {
	_, err := parsePublicURL("RELAY_PUBLIC_URL", "https://user:hunter2@relay.example.com")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hunter2")
}

// TestPublicURLLine_SaysWhichVariablesAreInjected asserts on the variable NAMES,
// not on the sentence: a rewording must stay green, a variable silently dropping
// out of the feature must not.
func TestPublicURLLine_SaysWhichVariablesAreInjected(t *testing.T) {
	off := publicURLLine("")
	require.Contains(t, off, "RELAY_PUBLIC_URL")
	require.Contains(t, off, "RELAY_JOB_URL")
	require.Contains(t, off, "RELAY_TASK_URL")
	require.Contains(t, off, "RELAY_JOB_ID")
	require.Contains(t, off, "RELAY_TASK_ID")

	on := publicURLLine("https://ops.example.com/relay")
	require.Contains(t, on, "https://ops.example.com/relay")
	require.Contains(t, on, "https://ops.example.com/relay/jobs/<job-id>")
	require.Contains(t, on, "https://ops.example.com/relay/jobs/<job-id>/tasks/<task-id>")
}
