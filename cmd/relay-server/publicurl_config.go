package main

import (
	"fmt"
	"net/url"
	"strings"
)

// parsePublicURL resolves RELAY_PUBLIC_URL into the browser-facing base the
// coordinator renders task links from, or "" when the feature is off.
//
// FAIL-CLOSED, unlike parseConnLimit / parseAutoEnrollCeiling / parseWatchdogDuration,
// which warn and fall back. A warn-and-disable typo produces exactly "the URL
// variables are absent", which is also what an operator who never set the
// variable sees, so the degraded mode would be indistinguishable from the
// unconfigured mode. There is also no defensible default origin to fall back
// to, and two of the rejections below are security rejections. The
// error-returning shape is api.ParseCORSOrigins's.
//
// The cost is real: a deployment whose value is edited badly will not come back
// up on its next restart. publicURLLine's unconditional startup line is the
// mitigation, and it is also the only defence against the failure no validator
// can catch - a value that parses perfectly and names the wrong host.
//
// Every rejection that has a structured URL in hand renders it through
// (*url.URL).Redacted(), so a base carrying a password cannot reach the log.
func parsePublicURL(name, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	// Ahead of url.Parse on purpose: url.Parse rejects some control bytes and
	// accepts a space, so leaving this to it would make the refusal depend on
	// which byte was typed. A shell step interpolating this value unquoted is
	// the realistic footgun.
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] == 0x7f {
			return "", fmt.Errorf("%s must not contain whitespace or control characters", name)
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		// The one branch that echoes the raw value: there is no structured URL
		// to redact yet.
		return "", fmt.Errorf("%s=%q is not a URL: %w", name, s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s=%q must use the http or https scheme", name, u.Redacted())
	}
	if u.Host == "" {
		return "", fmt.Errorf("%s=%q is missing a host", name, u.Redacted())
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

// publicURLLine renders the unconditional startup line, in the shape of
// grpcBoundsLine, autoEnrollCeilingLine and watchdogBoundsLine. A fail-closed
// parser plus a silent success is half a control: nothing else tells an operator
// that the value relay believes is the one they meant.
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
