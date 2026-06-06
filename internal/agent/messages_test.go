package agent

import (
	"strings"
	"testing"
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
			got := authFailureMessage(tt.hasAgentToken, path, tt.hasEnrollment)
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
