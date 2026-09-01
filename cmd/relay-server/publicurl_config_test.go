package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParsePublicURL_AcceptsAndNormalizes pins the normalization contract jobURL
// and taskURL depend on: the returned base NEVER ends in a slash, which is why
// those two joiners can concatenate with no separator logic at all.
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
		// The two authorities the missing-host check must not sweep up with the
		// port-only ones it rejects, and the ASCII spelling of an IDN host that
		// the non-ASCII host check leaves an operator.
		{"a bracketed IPv6 literal with a port", "https://[::1]:8080", "https://[::1]:8080"},
		{"punycode is an ASCII host", "https://xn--80ak6aa92e.com", "https://xn--80ak6aa92e.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePublicURL("RELAY_PUBLIC_URL", tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	// Hoisted out of the loop deliberately: inside it, this can only fire when
	// the Equal above has already failed, because no want ends in a slash. Over
	// the want values it is an assertion about the table.
	for _, tc := range cases {
		require.False(t, strings.HasSuffix(tc.want, "/"),
			"jobURL and taskURL concatenate with no separator logic; a trailing slash in a "+
				"want here would make this table document a double slash in every link relay "+
				"publishes")
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
		{"no scheme at all", "relay.example.com"},
		{"query string cannot have a path appended", "https://relay.example.com/?x=1"},
		{"fragment cannot have a path appended", "https://relay.example.com/#frag"},
		{"a bare ? is a query even with nothing after it", "https://relay.example.com?"},
		// The missing-host rows. An opaque URL is what the row above with no
		// scheme cannot reach - that one is refused by the scheme check first.
		// A port-only authority is what u.Host cannot see, because Host keeps
		// the port: only u.Hostname() is empty for it.
		{"scheme and separator with nothing after them", "https://"},
		{"opaque URL, no authority at all", "https:example.com/x"},
		{"a port-only authority has no host", "https://:8080"},
		{"a port-only authority with a path has no host either", "https://:8080/relay"},
		{"port out of range", "https://relay.example.com:99999"},
		// The three characters browsers map to '.' before resolving a name. The
		// value reads as relay's host and resolves to a label under evil.com,
		// and U+3002 is what a CJK IME emits for a typed period, so this is a
		// typo shape and not only a paste. Written as Go escapes: a raw
		// non-ASCII byte in a source literal is unverifiable by eye.
		{"ideographic full stop in the host", "https://relay.example.com\u3002evil.com"},
		{"fullwidth full stop in the host", "https://relay.example.com\uff0eevil.com"},
		{"halfwidth ideographic full stop in the host", "https://relay.example.com\uff61evil.com"},
		{"a Cyrillic homograph in the host", "https://rel\u0430y.example.com"},
		// A space in the HOST and a space in the PATH are two different rows,
		// and only the second one discriminates: url.Parse rejects the first
		// itself. url.Parse ACCEPTS the second and percent-encodes it to
		// /re%20lay, so nothing but the pre-parse loop refuses it.
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

// TestParsePublicURL_RejectionDoesNotLeakAPassword pins the redaction rule. The
// message goes to a server log an operator reads and ships; the value it is
// refusing may carry a credential.
//
// The rows below the first one are the ones that discriminate: they reach the
// url.Parse failure branch, which never gets a structured URL to redact and
// which url.Parse's own *url.Error would quote verbatim. Two realistic typos put
// a credentialled value there - an unsubstituted :port placeholder and a bare %
// inside a generated secret - and the userinfo check that redacts correctly is
// several lines too late to see either.
func TestParsePublicURL_RejectionDoesNotLeakAPassword(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"userinfo on an otherwise parseable URL", "https://user:hunter2@relay.example.com"},
		{"an unsubstituted port placeholder fails url.Parse first", "https://ops:hunter2@relay.example.com:port"},
		{"a percent in the secret fails url.Parse first", "https://ops:hunter2@relay.example.com/%zz"},
		{"a non-numeric port fails url.Parse first", "https://ops:hunter2@relay.example.com:80a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePublicURL("RELAY_PUBLIC_URL", tc.raw)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "hunter2")
		})
	}
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
