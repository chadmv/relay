package worker

import "sync/atomic"

// The two suppression arms of ingestLogLimiter.allow, and only those two. They
// mean different things to an operator and must never be summed into one
// number: DEDUPED is a healthy repeating failure being collapsed, SUPPRESSED is
// either an attack or a misconfiguration.
//
// THERE IS NO THIRD ARM HERE ON PURPOSE. allow returns false in three places;
// the third is its `l == nil` fail-closed guard, where NO EVENT WAS SUPPRESSED
// because there was no limiter. Counting it would count a phantom. It is also
// unwritable from here: these counters are reached through the limiter, so a nil
// receiver has nothing to increment, and adding a package-level fallback to
// count it would be a visible diff rather than an accident.
const (
	ingestDropDeduped = iota
	ingestDropSuppressed
	ingestDropArms
)

// IngestLogDrops is a snapshot of what one server's ingest log budget has
// dropped since process start.
//
// WHAT THESE NUMBERS ARE. They count LOG LINES THE BUDGET DROPPED, not
// diagnostics lost. A handler that decides not to log without consulting the
// budget contributes nothing here - handleTaskStatus's pgx.ErrNoRows GetTask
// (which short-circuits before allow) and handleTaskLog's fence-rejection arm
// (whose counter is a separate item) are both invisible to these fields, by
// design.
//
// MONOTONIC, per process, zeroed by a restart, and never returned to an agent:
// the only read path is the admin-authenticated GET /v1/server/counters.
type IngestLogDrops struct {
	// Deduped is the healthy arm: the key was logged inside
	// ingestLogDedupeWindow, so this occurrence was folded into an earlier line.
	// A large number next to a small line count is a repeating failure being
	// collapsed exactly as intended.
	Deduped IngestLogDropsByKind

	// Suppressed is the loud arm: the key was new or re-armed and the
	// connection's token bucket was empty, so the line was dropped entirely.
	// Non-zero means some connection is producing distinct failures faster than
	// 6 lines per minute, which is either an attack or a misconfiguration.
	Suppressed IngestLogDropsByKind
}

// IngestLogDropsByKind splits an arm by which log site was dropped.
//
// A STRUCT, NOT A MAP, and the choice is load-bearing rather than stylistic. The
// kind set is closed at compile time, so a fixed set of named fields makes
// unbounded key cardinality structurally impossible, and it keeps
// internal/api's two payload walks at full reach: an entry on
// counterPayloadAllowList is shape-checked but NON-DESCENDING, so a map here
// would have to re-implement key, value and cardinality checking inside its own
// exemption predicates. See counterPayloadExemption's comment.
//
// THESE FIELD NAMES ARE PART OF A RESPONSE CONTRACT. Each maps to one JSON key
// under ingest_log_budget.counts; see the logKind block in
// ingest_log_limiter.go.
type IngestLogDropsByKind struct {
	TaskLogPersist  uint64
	BadTaskIDLog    uint64
	BadTaskIDStatus uint64
	StatusGetTask   uint64
	Inventory       uint64
}

// ingestLogCounters is the process-lifetime home for what the per-connection log
// budgets dropped. It is a VALUE FIELD on Handler, not a package-level var:
// there is exactly one Handler per server process (main registers one with
// RegisterAgentServiceServer), so per-Handler IS process-wide in production,
// while every test gets its own and no count leaks between them.
//
// ATOMICS, NOT A MUTEX, AND netlimit DELIBERATELY DID THE OPPOSITE. netlimit's
// refusal counters are plain uint64 under the listener's existing mutex, because
// its snapshot carries a cross-field invariant (max_per_source <= live_total <=
// the configured cap) that only one critical section can hold, and plain fields
// make an unsynchronised access a data race -race can see. NEITHER APPLIES HERE.
// These ten numbers have no relation to each other - each is an independent
// monotonic total - so a snapshot that reads them microseconds apart is not
// inconsistent, merely unsynchronised in a way nothing can observe. And the
// increment site is the gRPC recv goroutine, whose standing constraint is no new
// lock, queue, goroutine or round trip: an atomic add is one locked
// exchange-add, no allocation and no scheduling, which is what lets
// ingestLogLimiter keep its documented no-mutex property VERBATIM.
//
// COUNTERS, NEVER LOG LINES. The next person to "improve" one of these into a
// log.Printf hands back the exact vector
// bug-2026-08-12-tasklog-err-limiter-attacker-keyed closed. Do not.
type ingestLogCounters struct {
	n [kindCount][ingestDropArms]atomic.Uint64
}

// record adds one drop. Out of range fails CLOSED - see the comment on the
// bounds check - and the kind guards in ingest_log_counters_test.go are what
// keep that branch unreachable.
func (c *ingestLogCounters) record(k logKind, arm int) {
	// A NIL COUNTER SET IS ITS OWN CASE, and the bounds check below does not
	// cover it however much it looks like it does: len(c.n) has an ARRAY-typed
	// operand, so it is a compile-time constant that never dereferences c. An
	// out-of-range kind on a nil receiver therefore returns harmlessly while an
	// IN-RANGE one - the shape production takes - panics on the recv goroutine.
	//
	// Unreachable today (Connect, newIngestLogLimiter and shimLimiterFor all pass
	// &h.ingestDrops, and Handler's field is a value so its zero value works), but
	// newIngestLogLimiterAt(now, nil) and a bare &ingestLogLimiter{...} both
	// compile, and allow already guards `l == nil` without guarding
	// `l.drops == nil`. One register compare on a path whose standing constraint
	// is no new lock, queue, goroutine or round trip.
	// TestIngestLogCounters_ANilCounterSetIsDroppedNotPanicked pins it.
	if c == nil {
		return
	}

	// FAIL CLOSED, DO NOT PANIC. An out-of-range index here would panic on the
	// gRPC recv goroutine, which Connect does not recover and grpc-go does not
	// recover either, so it would kill the whole server process. Losing a count
	// is the cheaper failure. This branch is UNREACHABLE while
	// TestIngestLogKindsAreADenseRunFromOne and
	// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished are green, and
	// THOSE TWO TESTS ARE THE ONLY THING KEEPING IT SO - a reader who believes
	// the branch is dead has no reason to preserve them, which is precisely how
	// the property gets lost.
	//
	// WHAT THE BRANCH ITSELF DOES is pinned by
	// TestIngestLogCounters_AnOutOfRangeKindIsDroppedNotPanicked, and that test
	// asserts on the WHOLE array rather than on the snapshot for a measured
	// reason: kind 0 would land in c.n[0], which no published field reads, so
	// relaxing `i <= 0` to `i < 0` is invisible to any assertion made through
	// snapshot(). It was a live mutation survivor, not a hypothetical.
	i := int(k)
	if i <= 0 || i >= len(c.n) || arm < 0 || arm >= ingestDropArms {
		return
	}
	c.n[i][arm].Add(1)
}

func (c *ingestLogCounters) snapshot() IngestLogDrops {
	return IngestLogDrops{
		Deduped:    c.byKind(ingestDropDeduped),
		Suppressed: c.byKind(ingestDropSuppressed),
	}
}

// byKind reads one arm. Every field here is one JSON key of the endpoint's
// ingest_log_budget section; adding a kind without adding a line here counts it
// into a cell nobody reads, which
// TestIngestLogCounters_EveryKindIsPublishedDistinctly turns RED.
func (c *ingestLogCounters) byKind(arm int) IngestLogDropsByKind {
	return IngestLogDropsByKind{
		TaskLogPersist:  c.n[kindTaskLogPersist][arm].Load(),
		BadTaskIDLog:    c.n[kindBadTaskIDLog][arm].Load(),
		BadTaskIDStatus: c.n[kindBadTaskIDStatus][arm].Load(),
		StatusGetTask:   c.n[kindStatusGetTask][arm].Load(),
		Inventory:       c.n[kindInventory][arm].Load(),
	}
}
