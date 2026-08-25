package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnrollmentIgnoredWarning(t *testing.T) {
	const path = "/var/lib/relay-agent/token"
	tests := []struct {
		name             string
		hasAgentToken    bool
		enrollmentSet    bool
		wantEmpty        bool
		wantContainsPath bool
	}{
		{"stored token and enrollment set", true, true, false, true},
		{"stored token, no enrollment", true, false, true, false},
		{"no stored token, enrollment set", false, true, true, false},
		{"neither", false, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnrollmentIgnoredWarning(tt.hasAgentToken, tt.enrollmentSet, path)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("want empty, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("want non-empty warning, got empty")
			}
			if tt.wantContainsPath && !strings.Contains(got, path) {
				t.Errorf("warning %q does not name token path %q", got, path)
			}
			if !strings.Contains(got, "ignored") {
				t.Errorf("warning %q should explain the token is ignored", got)
			}
			if strings.ContainsRune(got, '—') {
				t.Errorf("warning %q contains an em dash", got)
			}
		})
	}
}

func TestAuthFailureMessage(t *testing.T) {
	const path = "/var/lib/relay-agent/token"
	tests := []struct {
		name           string
		hasAgentToken  bool
		hasEnrollment  bool
		wantSubstrings []string
	}{
		{"stored token rejected", true, false, []string{path, "delete that file", "RELAY_AGENT_ENROLLMENT_TOKEN", "exiting"}},
		{"enrollment token rejected", false, true, []string{"enrollment token was rejected", "exiting"}},
		{"token-less auto-enroll rejected", false, false, []string{"auto-enroll was rejected", "RELAY_ALLOW_AUTO_ENROLL", "exiting"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := authFailureMessage(tt.hasAgentToken, tt.hasEnrollment, path)
			for _, sub := range tt.wantSubstrings {
				if !strings.Contains(got, sub) {
					t.Errorf("message %q missing substring %q", got, sub)
				}
			}
			if strings.ContainsRune(got, '—') {
				t.Errorf("message %q contains an em dash", got)
			}
		})
	}
}

// TestAuthFailureMessage_TokenlessArmNamesAllThreeCausesAndBothRemedies. The
// server refuses a token-less registration for three reasons and returns the
// SAME opaque string for all of them, deliberately, so the agent's own exit log
// is the only place an operator can learn what to try. It used to name one cause
// and prescribe enabling a flag that is already enabled whenever the other two
// fire.
func TestAuthFailureMessage_TokenlessArmNamesAllThreeCausesAndBothRemedies(t *testing.T) {
	msg := authFailureMessage(false, false, "/var/lib/relay/token")

	for _, want := range []string{
		"RELAY_ALLOW_AUTO_ENROLL", // cause 1: the flag is off
		"already has a worker",    // cause 2: the hostname is claimed
		"ceiling",                 // cause 3: the fleet is at the ceiling
		"relay workers revoke",    // remedy 1
		"enrollment token",        // remedy 2
	} {
		assert.Contains(t, msg, want)
	}
	assert.Contains(t, msg, "exiting", "the agent exits rather than reconnect-looping on Unauthenticated")
}
