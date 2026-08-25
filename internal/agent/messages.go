package agent

import "fmt"

// EnrollmentIgnoredWarning returns a warning when an enrollment token is set but
// will be ignored because a stored agent token already exists. "" = no warning.
func EnrollmentIgnoredWarning(hasAgentToken, enrollmentTokenSet bool, tokenPath string) string {
	if hasAgentToken && enrollmentTokenSet {
		return fmt.Sprintf("relay-agent: RELAY_AGENT_ENROLLMENT_TOKEN is set but ignored because a stored agent token already exists at %s; delete that file to re-enroll", tokenPath)
	}
	return ""
}

// authFailureMessage returns the exit log for an Unauthenticated registration
// failure, tailored to which credential was in use.
func authFailureMessage(hasAgentToken, hasEnrollmentToken bool, tokenPath string) string {
	switch {
	case hasAgentToken:
		return fmt.Sprintf("agent: authentication failed - stored agent token at %s was rejected; if this agent was re-provisioned, delete that file and set RELAY_AGENT_ENROLLMENT_TOKEN to re-enroll; exiting", tokenPath)
	case hasEnrollmentToken:
		return "agent: authentication failed - enrollment token was rejected (invalid, expired, or already used); exiting"
	default:
		return "agent: authentication failed - token-less auto-enroll was rejected. The server returns " +
			"one opaque refusal for all three causes, so check them in order: (1) the server may not have " +
			"RELAY_ALLOW_AUTO_ENROLL enabled; (2) this hostname already has a worker row, and auto-enroll " +
			"creates workers but never claims them - run `relay workers revoke <id>` for it, or enroll with " +
			"an admin-issued enrollment token, which does revive a revoked worker; (3) the fleet may be at " +
			"the auto-enroll worker ceiling (RELAY_AUTO_ENROLL_WORKER_CEILING), which enrollment tokens " +
			"are never refused by. exiting"
	}
}
