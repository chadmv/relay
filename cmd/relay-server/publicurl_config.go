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
// NO BRANCH MAY RENDER A VALUE DERIVED FROM THE INPUT: it can carry a password,
// and log.Fatalf writes to a stderr that is usually shipped somewhere with
// broader read access than the environment variable had. The operator already
// has the value, so a rejection only has to name the variable and the rule it
// broke. TestParsePublicURL_Rejects and
// TestParsePublicURL_RejectionDoesNotLeakAPassword hold every rejection to a
// fixed set of messages, so a branch that starts interpolating goes red whether
// or not a row happens to carry a sentinel into it.
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
		// The inner error is NOT input-free either: net/url builds "invalid port
		// %q after host" from a slice of the input, and a secret containing '#',
		// '?' or '/' terminates the authority before the '@' is ever found, so
		// that slice is the secret. Classify the failure instead of printing it.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			switch uerr.Err.(type) {
			case url.EscapeError:
				return "", fmt.Errorf("%s is not a URL: it contains an invalid percent-escape", name)
			case url.InvalidHostError:
				return "", fmt.Errorf("%s is not a URL: it contains a character that is not allowed in a host", name)
			}
		}
		return "", fmt.Errorf("%s is not a URL", name)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s must use the http or https scheme", name)
	}
	// Hostname(), not Host: Host keeps the port, so "https://:8080" has a
	// non-empty Host and no host at all.
	if u.Hostname() == "" {
		return "", fmt.Errorf("%s is missing a host", name)
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
	// url.Parse checks that a port is digits, not that it is a port. A bare
	// trailing colon is its own row: u.Port() is "" for it, so the range check
	// cannot see it, and the dangling colon would flow into every link.
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("%s must not end in a bare colon; give a port or leave it off", name)
	}
	if p := u.Port(); p != "" {
		n, convErr := strconv.Atoi(p)
		if convErr != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("%s has a port outside 1-65535", name)
		}
	}
	if u.User != nil {
		// A base URL carrying userinfo is both a credential in an environment
		// variable and a phishing shape (https://relay.example.com@evil.example/)
		// that relay would render into every link it publishes.
		return "", fmt.Errorf("%s must not carry userinfo", name)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%s must not carry a query or fragment; relay appends a path to it", name)
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
