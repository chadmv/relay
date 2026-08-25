package agent

import "fmt"

// EnrollmentIgnoredWarning returns a warning when an enrollment token is set but
// will be ignored because a stored agent token already exists. "" = no warning.
func EnrollmentIgnoredWarning(hasAgentToken, enrollmentTokenSet bool, tokenPath string) string {
	if hasAgentToken && enrollmentTokenSet {
		return fmt.Sprintf("relay-agent: RELAY_AGENT_ENROLLMENT_TOKEN is set but ignored because a stored agent token already exists at %s. "+
			"Deleting that file is NOT sufficient on its own: the server refuses an enrollment token naming a hostname whose worker "+
			"still holds a live credential, so an admin must run `relay workers revoke <id>` for this worker first - after which the "+
			"enrollment token is accepted and revives it", tokenPath)
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
		return "agent: authentication failed - enrollment token was rejected. The cause is NOT necessarily the token: " +
			"as well as invalid, expired or already used, the server refuses a VALID, UNCONSUMED token when the " +
			"hostname already has a worker holding a live credential. Reissuing in that case mints another one-shot " +
			"admin credential that lives until it expires and hits the identical refusal, so check the worker first: " +
			"`relay workers revoke <id>` for this hostname, then retry with the SAME token if it has not expired - a " +
			"refused token is not consumed. exiting"
	default:
		return "agent: authentication failed - token-less auto-enroll was rejected. The server returns " +
			"one opaque refusal for all three causes, so check them in order: (1) the server may not have " +
			"RELAY_ALLOW_AUTO_ENROLL enabled; (2) this hostname already has a worker row, and auto-enroll " +
			"creates workers but never claims them. REVOKING ALONE DOES NOT FIX THIS: `relay workers revoke` " +
			"nulls the credential but keeps the row, so the hostname stays claimed and this message repeats " +
			"identically. Either revoke it and then enroll this agent with an admin-issued enrollment token " +
			"(which a revoked worker accepts, and which reuses the existing worker with its history intact), " +
			"or run `relay workers delete <id>` to free the hostname for token-less enrollment - that also " +
			"destroys the worker's assignments and reservations; (3) the fleet may be at " +
			"the auto-enroll worker ceiling (RELAY_AUTO_ENROLL_WORKER_CEILING), which enrollment tokens " +
			"are never refused by. exiting"
	}
}
