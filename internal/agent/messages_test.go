package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEnrollmentIgnoredWarning_DoesNotPrescribeDeletingTheTokenFileAlone. The
// warning used to say "delete that file to re-enroll", which stopped being true
// when enrollAndRegister started refusing a hostname whose worker holds a live
// agent_token_hash: deleting the local token file leaves the SERVER-side hash
// live, so the re-enroll is refused and the agent has destroyed the only
// credential that still worked.
func TestEnrollmentIgnoredWarning_DoesNotPrescribeDeletingTheTokenFileAlone(t *testing.T) {
	got := EnrollmentIgnoredWarning(true, true, "/var/lib/relay/token")
	if !strings.Contains(got, "revoke") {
		t.Errorf("warning %q must say the server-side credential has to be revoked first", got)
	}
}

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
		// The cause substring is asserted, not just the leading clause. This row
		// used to check only "enrollment token was rejected" and "exiting", so it
		// exercised the arm while proving NOTHING about what it says the cause
		// was - and errCredentialLive made all three of its stated causes false.
		{"enrollment token rejected", false, true, []string{
			"enrollment token was rejected", "hostname already has a worker", "revoke", "exiting"}},
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

		// THE TWO ROUTES ARE SEQUENTIAL, NOT ALTERNATIVE, and asserting only
		// "relay workers revoke" locked in a remedy that PROVABLY DOES NOT WORK:
		// ClearWorkerAgentToken nulls the hash and sets status='revoked', it does
		// NOT delete the row, and InsertWorkerForAutoEnroll conflicts on
		// (hostname) whatever the status - which
		// TestConnect_AutoEnrollRefusesRevokedWorker asserts. An operator who
		// revoked and restarted a token-less agent got a byte-identical message
		// and no server-side line naming the host, so the loop terminated only by
		// reading source.
		"revoke it and then", // remedy 1: revoke THEN enroll with a token
		"enrollment token",   // ... which the NULL-hash predicate now admits
		"rename",             // remedy 2: the other escape hatch that actually exists
		"workers delete",     // remedy 3: free the hostname outright - a REAL command since 2026-08-26
	} {
		assert.Contains(t, msg, want)
	}
	assert.Contains(t, msg, "exiting", "the agent exits rather than reconnect-looping on Unauthenticated")

	// EVERY COMMAND THIS MESSAGE NAMES MUST EXIST. A 2026-06 revision prescribed
	// `relay workers delete` as "the only way to free the hostname" and pinned it
	// here. AT THAT TIME NO SUCH COMMAND EXISTED at any layer: internal/cli's
	// workers switch HAD no delete arm (it returned "unknown workers subcommand"),
	// there WAS no DELETE FROM workers in internal/store/query, and the only
	// DELETE route on the resource WAS /v1/workers/{id}/token, which is revoke.
	// A terminal exit message is the worst possible place for that - it is the
	// last thing an operator reads and there is nothing after it to correct the
	// record. All three of those facts stopped being true on 2026-08-26; see the
	// ghost list below, where "workers delete" graduated.
	//
	// This is a NEGATIVE assertion because the positive ones cannot catch it: a
	// substring check is satisfied by any plausible-looking string, so "the
	// remedy is named" and "the remedy exists" are different properties and only
	// this one tests the second.
	//
	// IT IS A DENY-LIST AND IT IS NOT WHAT HOLDS THE PROPERTY. It names three
	// spellings; `relay worker delete` (singular) walks straight past it, and so
	// do "destroy" and "purge". PLAUSIBILITY IS THE GENERATOR of this defect, so a
	// list of the ghosts somebody already caught can never be the guard.
	// TestOperatorMessages_OnlyPrescribeCommandsThatExist is: it parses the real
	// command set out of internal/cli and requires every `relay ...` in every
	// message to resolve, whatever its spelling. This stays only because it gives
	// a more pointed message for the three spellings that actually shipped.
	//
	// "workers delete" GRADUATED FROM GHOST TO REAL COMMAND on 2026-08-26
	// (docs/superpowers/plans/2026-08-26-worker-delete.md): internal/cli's workers
	// switch now has a delete arm, DeleteWorker is a real statement, and
	// DELETE /v1/workers/{id} is a real admin-only route. Forbidding it here would
	// forbid the true remedy, so it moved to the want-list above. The other two
	// spellings are still ghosts.
	for _, ghost := range []string{"relay workers rm", "workers remove"} {
		assert.NotContains(t, msg, ghost,
			"this message must not prescribe a command that does not exist; add the subcommand to "+
				"internal/cli/workers.go first, then say so here")
	}
}
