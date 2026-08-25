package worker

import "sync/atomic"

// enrollmentRefusalReason partitions the refusals the two enrollment guards produce.
//
// THE VALUES ARE ARRAY INDICES AND THEY START AT 0, exactly as
// taskStatusFenceReason does and deliberately unlike logKind, which starts at 1.
// enrollmentRefusalCounters is a [enrollmentRefusalReasonCount]atomic.Uint64 indexed by
// these constants, so they must stay a DENSE RUN from 0 with the sentinel
// immediately after the last one. record fails CLOSED rather than panicking - it
// runs on the gRPC recv goroutine, which neither Connect nor grpc-go recovers -
// so a gap is a SILENT loss of that reason's counts.
type enrollmentRefusalReason uint8

const (
	// enrollmentRefusalHostnameClaimed: token-less auto-enroll, and a workers row
	// for that hostname already exists. Caller-driven and unboundedly repeatable
	// with the same hostname, which is precisely why this is a counter and not a
	// log line.
	enrollmentRefusalHostnameClaimed enrollmentRefusalReason = iota

	// enrollmentRefusalFleetAtCeiling: token-less auto-enroll, and CountWorkers is
	// at or above RELAY_AUTO_ENROLL_WORKER_CEILING. THE ACTIONABLE ONE: a climbing
	// value means either an attacker filling the budget or a fleet that has
	// genuinely outgrown a default derived from a different quantity. The remedy
	// order is read the auto-enrolled audit lines (the only signal carrying a
	// hostname AND a remote address), then revoke unused workers, then use
	// enrollment tokens, which this ceiling never refuses.
	//
	// IT ALIASES THE OTHER TWO AT CAPACITY, AND THAT IS DISCLOSED HERE BECAUSE
	// HERE IS WHERE THE SIGNAL IS READ. The ceiling check runs BEFORE the insert -
	// deliberately, so that a refused auto-enroll writes nothing, which a test
	// pins - so once the fleet is at capacity EVERY token-less refusal is recorded
	// under this reason, including a claimed-hostname retry that would otherwise
	// have been hostname_claimed. So hostname_claimed_total goes flat at exactly
	// the moment an operator starts triaging, and the split stops partitioning.
	// Do not "fix" this by checking the insert first: that would make the refusal
	// no longer free of side effects, which is the property the ordering exists
	// for. Read the two numbers as "at capacity, cause unknown" instead.
	enrollmentRefusalFleetAtCeiling

	// enrollmentRefusalCredentialLive: an ADMIN-ISSUED enrollment token naming a
	// hostname whose worker still holds a live agent_token_hash. Not
	// attacker-reachable without an admin credential, so a non-zero value here is
	// far likelier to be an operator rotating a live agent in place - whose remedy
	// is to revoke first - than an attack.
	enrollmentRefusalCredentialLive

	// enrollmentRefusalReasonCount MUST STAY LAST and is NOT a reason. It is the LENGTH
	// of the counter array.
	enrollmentRefusalReasonCount
)

// EnrollmentRefusalCounts is what the two enrollment guards have refused since
// process start, split by cause.
//
// NO TOTAL, AND THAT IS A DECISION, following TaskStatusFenceCounts: three
// fields that partition the refusals exhaustively make a published total the sum
// of its own siblings, where it can only agree or be a bug. Derive it.
//
// THESE ARE THE ONLY RECORD OF A REFUSAL ANYWHERE. No log site is added on either
// path, deliberately: a log.Printf on an attacker-reachable refusal would be a
// fresh instance of the flood class
// bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget
// describes, on a path whose limiter is not even allocated yet. THE COST, NAMED
// RATHER THAN HIDDEN: a legitimately refused agent - the lost-state-directory
// case - produces no server-side line naming it. The server deliberately cannot
// name an attacker-chosen hostname on an unboundedly repeatable refusal, so an
// operator debugging a refused agent reads the AGENT's exit log, which names its
// own hostname. README says so.
//
// PER REPLICA, monotonic, zeroed by a restart, and never returned to an agent.
// Read through Handler.EnrollmentRefusals.
type EnrollmentRefusalCounts struct {
	HostnameClaimed uint64 `json:"hostname_claimed_total"`
	FleetAtCeiling  uint64 `json:"fleet_at_ceiling_total"`
	CredentialLive  uint64 `json:"credential_live_total"`
}

// enrollmentRefusalCounters is the process-lifetime home. A VALUE field on
// Handler, so the zero value works and every test gets its own. Atomics rather
// than a mutex, for statusFenceCounters' reasons: no container, no cross-field
// invariant (because no total is published), and the increment site is the gRPC
// recv goroutine, whose standing constraint is no new lock, queue, goroutine or
// round trip.
type enrollmentRefusalCounters struct {
	n [enrollmentRefusalReasonCount]atomic.Uint64
}

// record adds one refusal. Out of range fails CLOSED: losing a count is cheaper
// than a panic on the recv goroutine, which would kill the server process. The
// check is an UPPER BOUND ONLY - enrollmentRefusalReason is uint8, so int(r) cannot be
// negative and a `< 0` arm would be dead code wearing the costume of a control.
func (c *enrollmentRefusalCounters) record(r enrollmentRefusalReason) {
	i := int(r)
	if i >= len(c.n) {
		return
	}
	c.n[i].Add(1)
}

// snapshot reads the three cells. Adding a reason without adding a line here
// counts it into a cell nobody reads, which
// TestEnrollmentRefusalCounters_EveryReasonIsPublishedDistinctly turns RED.
func (c *enrollmentRefusalCounters) snapshot() EnrollmentRefusalCounts {
	return EnrollmentRefusalCounts{
		HostnameClaimed: c.n[enrollmentRefusalHostnameClaimed].Load(),
		FleetAtCeiling:  c.n[enrollmentRefusalFleetAtCeiling].Load(),
		CredentialLive:  c.n[enrollmentRefusalCredentialLive].Load(),
	}
}
