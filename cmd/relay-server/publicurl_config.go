package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parsePublicURL resolves RELAY_PUBLIC_URL into the browser-facing base the
// coordinator renders task links from, or "" when the feature is off.
//
// FAIL-CLOSED. A warn-and-disable typo produces exactly "the URL variables are
// absent", which is also what an operator who never set the variable sees, so
// the degraded mode would be indistinguishable from the unconfigured mode, and
// there is no defensible default origin to fall back to.
//
// NOTHING HERE MAY ECHO THE RAW VALUE: it can carry a password, and log.Fatalf
// writes to a stderr that is usually shipped somewhere with broader read access
// than the environment variable had. Rejections holding a structured URL render
// it through (*url.URL).Redacted(); the url.Parse branch has no structured URL
// and must not use %w either, because *url.Error quotes the whole input.
// TestParsePublicURL_RejectionDoesNotLeakAPassword's parse-failure rows pin
// both halves.
func parsePublicURL(name, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	// Ahead of url.Parse on purpose, and the reachable case is narrower than it
	// looks: url.Parse refuses every control byte anywhere and a space in the
	// HOST, but a space in the PATH it accepts and percent-encodes. Without this
	// loop, "https://ops.example.com/re lay" is accepted silently and the %20
	// is rendered into every link relay publishes. A shell step interpolating
	// this value unquoted is the realistic footgun.
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] == 0x7f {
			return "", fmt.Errorf("%s must not contain whitespace or control characters", name)
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		// Neither the raw value nor the *url.Error wrapping it may be rendered:
		// url.Error.Error() quotes the whole input, so a %w here leaks
		// independently of a %q. Only the inner error is safe to show.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return "", fmt.Errorf("%s is not a URL: %v", name, uerr.Err)
		}
		return "", fmt.Errorf("%s is not a URL", name)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s=%q must use the http or https scheme", name, u.Redacted())
	}
	// Hostname(), not Host: Host keeps the port, so "https://:8080" has a
	// non-empty Host and no host at all.
	if u.Hostname() == "" {
		return "", fmt.Errorf("%s=%q is missing a host", name, u.Redacted())
	}
	// A host byte >= 0x80 is refused rather than rendered. Browsers map U+3002,
	// U+FF0E and U+FF61 to '.' before resolving a name, so
	// "https://relay.example.com<U+3002>evil.com" reads as relay's host and
	// resolves under evil.com - and U+3002 is what a CJK IME emits for a typed
	// period, so it is a typo shape and not only a paste. Homographs are the
	// same problem one layer up. An operator with a genuine IDN host supplies
	// the punycode form, which is ASCII and unaffected. The value is
	// deliberately not echoed: a log line is exactly where the substitution is
	// invisible again.
	for i := 0; i < len(u.Host); i++ {
		if u.Host[i] >= 0x80 {
			return "", fmt.Errorf("%s must use an ASCII host; supply the punycode (xn--) form of an IDN", name)
		}
	}
	// url.Parse checks that a port is digits, not that it is a port.
	if p := u.Port(); p != "" {
		n, convErr := strconv.Atoi(p)
		if convErr != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("%s=%q has a port outside 1-65535", name, u.Redacted())
		}
	}
	if u.User != nil {
		// A base URL carrying userinfo is both a credential in an environment
		// variable and a phishing shape (https://relay.example.com@evil.example/)
		// that relay would render into every link it publishes.
		return "", fmt.Errorf("%s=%q must not carry userinfo", name, u.Redacted())
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%s=%q must not carry a query or fragment; relay appends a path to it",
			name, u.Redacted())
	}
	// EscapedPath rather than Path so an operator's percent-encoding survives.
	// Assembled explicitly rather than through u.String(): the two agree only
	// because every other component has been rejected above, and explicit
	// assembly cannot resurrect a component if a later edit stops rejecting one.
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// publicURLLine renders the unconditional startup line. A fail-closed parser
// plus a silent success is half a control: no validator can catch a value that
// parses perfectly and names the wrong host, so an operator has to be shown the
// value relay believes they meant.
func publicURLLine(base string) string {
	if base == "" {
		return "public URL: not configured (RELAY_PUBLIC_URL is unset), so RELAY_JOB_URL and " +
			"RELAY_TASK_URL are not injected into task subprocesses. RELAY_JOB_ID and RELAY_TASK_ID " +
			"still are - they need no configuration."
	}
	return fmt.Sprintf(
		"public URL: %s - task subprocesses receive RELAY_JOB_URL=%s/jobs/<job-id>, "+
			"RELAY_TASK_URL=%s/jobs/<job-id>/tasks/<task-id>, RELAY_JOB_ID and RELAY_TASK_ID.",
		base, base, base)
}
