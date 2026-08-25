package worker

import "sync/atomic"

// taskStatusFenceReason partitions the rejections handleTaskStatus's two
// epoch-fenced writes produce, by WHAT THE ROW SAID WHEN THIS HANDLER READ IT -
// which is the only thing available without a second round trip.
//
// THE VALUES ARE ARRAY INDICES AND THEY START AT 0. statusFenceCounters is a
// [fenceReasonCount]atomic.Uint64 indexed by these constants, so they must stay
// a DENSE RUN from 0 with fenceReasonCount immediately after the last one. Note
// this differs deliberately from logKind, which starts at 1 because its array is
// sized [kindCount] and wastes slot 0; a run starting at 1 with an array sized
// by the sentinel puts the last constant one past the end. record fails CLOSED
// rather than panicking, so a gap or a renumbering is a SILENT loss of that
// reason's counts. Pinned by TestTaskStatusFenceReasonsAreADenseRunFromZero.
//
// THE NAMES ARE A RESPONSE CONTRACT: each maps to one JSON key under
// task_status_fence.counts on GET /v1/server/counters. Renaming one renames an
// operator-visible key.
type taskStatusFenceReason uint8

const (
	// fenceReasonRaced: the row was still WRITABLE when GetTask read it, so
	// something else ended the generation inside this handler's own
	// read-to-write window - a cancel, a grace requeue, a sibling replica, the
	// coordinator watchdog. THIS IS THE ONE KEY THAT IS A FLOOR: the Go-side
	// identity and currency gates reject stale and forged messages a round trip
	// earlier, so only the narrow TOCTOU window reaches the SQL fence's
	// worker_id and assignment_epoch predicates.
	fenceReasonRaced taskStatusFenceReason = iota

	// fenceReasonDuplicate: the row was already unwritable and its status is
	// the one being reported. THE EXPECTED HEALTHY FLOOR. UpdateTaskStatus
	// deliberately refuses to write a terminal row (see the terminality
	// paragraph in internal/store/query/tasks.sql), and a terminal transition
	// neither bumps the epoch nor clears worker_id, so a duplicate terminal
	// message from a perfectly healthy assignee lands here. A non-zero value is
	// not an incident; its height depends on agent retry behaviour.
	fenceReasonDuplicate

	// fenceReasonConflicting: the row was already unwritable and its status
	// DISAGREES with what the agent is reporting. THE ACTIONABLE ONE. The
	// coordinator recorded one outcome and the agent is reporting another: a
	// task the stale-task watchdog stamped `timed_out` whose agent then reports
	// `done` lands here, which is the "a successful task recorded as a timeout"
	// case RELAY_TASK_WATCHDOG_MARGIN set too small produces, and before this
	// number there was no runtime signal of any kind for it.
	fenceReasonConflicting

	// fenceReasonCount MUST STAY LAST and is NOT a reason. It is the LENGTH of
	// the counter array.
	fenceReasonCount
)

// TaskStatusFenceCounts is what handleTaskStatus's epoch-fenced writes have
// refused since process start, split by what the row said at T0.
//
// DECLARED HERE, IN internal/worker, AND THAT DIRECTION IS FORCED: internal/api
// imports this package (server.go), so the watchdog's shape - declare the type
// in the consumer - is a compile error here. internal/api's response section
// carries this type DIRECTLY rather than restating its fields, so there is no
// hand-written mapper on either side and no arity to drift. That is slice 2's
// defect (a fully correct sixth kind counted on one side, published under no
// JSON key, all three packages green) made unreachable rather than guarded.
// TestTaskStatusFenceSectionRestatesNothing holds the antecedent.
//
// THE JSON TAGS LIVE IN THIS PACKAGE, which is already how a response contract
// owned by a producer is spelled here - see the logKind block's second bullet.
//
// THERE IS NO TOTAL, AND THAT IS A DECISION. The three fields partition the
// rejections exhaustively, so a published total would be the sum of its own
// siblings sitting beside them, where it can only agree or be a bug. (The joint
// spec's payload made exactly that mistake with swept_workers_tracked and it was
// refuted before slice 4 shipped.) The total is Raced+Duplicate+Conflicting;
// derive it, do not publish it.
//
// A FINER SPLIT - WHICH OF THE THREE SQL PREDICATES ACTUALLY FIRED - IS
// DECLINED, WITH THE PRICE, NOT IMPOSSIBLE. Both statements are single-row
// UPDATE ... WHERE forms that return no row on any predicate failure, so there
// is nothing to carry a reason. Recovering it needs a second round trip
// (forbidden on the recv goroutine) or a rewrite of both result contracts to
// RETURNING a reason. Read internal/api/server_counters.go's task_log_fence
// paragraph before "improving" this.
//
// WHAT THESE NUMBERS DO NOT COVER, said here because it is what an operator will
// get wrong:
//
//   - THE COORDINATOR'S OWN FENCE REJECTIONS ARE NOT HERE. Dispatcher.
//     failClaimedTask and Watchdog.SweepOnce are fenced by the same statement
//     and are counted nowhere. This section is the AGENT-REPORTED status path
//     only; it is not a census.
//   - THEY ARE NOT COMPARABLE WITH task_log_fence.counts.rejected_total. That
//     arm has no Go-side pre-filter; this one runs behind an identity gate and a
//     currency gate. No input moves both.
//   - THEY ARE ATTRIBUTABLE TO THE TASK'S OWN ASSIGNEE, and that is what the Go
//     identity gate at handleTaskStatus buys now that this number exists. A
//     non-assignee's forged report is dropped a round trip earlier and never
//     reaches a counter, so conflicting_total cannot be inflated by a peer
//     naming tasks it does not own.
//   - PER REPLICA, monotonic, zeroed by a restart, and never returned to an
//     agent: the only read path is the admin-authenticated
//     GET /v1/server/counters.
type TaskStatusFenceCounts struct {
	Raced       uint64 `json:"raced_total"`
	Duplicate   uint64 `json:"duplicate_total"`
	Conflicting uint64 `json:"conflicting_total"`
}

// statusFenceCounters is the process-lifetime home. A VALUE FIELD on Handler,
// not a package-level var: there is exactly one Handler per server process, so
// per-Handler IS process-wide in production while every test gets its own.
//
// ATOMICS, NOT A MUTEX, AND THE REASONS ARE CHECKED RATHER THAN COPIED. netlimit
// and the watchdog both took a mutex, on two grounds: a cross-field invariant
// only one critical section can hold, and a mutable container that cannot be
// updated atomically. NEITHER APPLIES. There is no container, and there is no
// invariant precisely BECAUSE no total is published (see TaskStatusFenceCounts)
// - three independent monotonic counts read microseconds apart are not
// inconsistent, merely unsynchronised in a way nothing can observe. Meanwhile
// the increment site is the gRPC recv goroutine, whose standing constraint is no
// new lock, queue, goroutine or round trip; an atomic add is one locked
// exchange-add with no allocation and no scheduling.
//
// COUNTERS, NEVER LOG LINES. A log.Printf on either arm would be caller-driven
// volume on the recv goroutine, firing on the legitimate duplicate-terminal
// case. Do not.
type statusFenceCounters struct {
	n [fenceReasonCount]atomic.Uint64
}

// record adds one rejection. Out of range fails CLOSED - a panic here runs on
// the gRPC recv goroutine, which Connect does not recover and grpc-go does not
// recover either, so it would kill the whole server process. Losing a count is
// the cheaper failure. This branch is UNREACHABLE while
// TestTaskStatusFenceReasonsAreADenseRunFromZero is green, AND THAT TEST IS THE
// ONLY THING KEEPING IT SO.
func (c *statusFenceCounters) record(r taskStatusFenceReason) {
	i := int(r)
	if i < 0 || i >= len(c.n) {
		return
	}
	c.n[i].Add(1)
}

// snapshot reads the three cells. Every field here is one JSON key of the
// endpoint's task_status_fence section; adding a reason without adding a line
// here counts it into a cell nobody reads, which
// TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly turns RED.
func (c *statusFenceCounters) snapshot() TaskStatusFenceCounts {
	return TaskStatusFenceCounts{
		Raced:       c.n[fenceReasonRaced].Load(),
		Duplicate:   c.n[fenceReasonDuplicate].Load(),
		Conflicting: c.n[fenceReasonConflicting].Load(),
	}
}
