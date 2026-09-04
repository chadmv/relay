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
// A FINER SPLIT - WHICH SQL PREDICATE ACTUALLY FIRED - IS
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
//     TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters pins it.
//   - ATTRIBUTABLE IS NOT HONEST, AND THE ASSIGNEE CAN FORGE ITS OWN. The gate
//     proves who sent the report, never that the report is true. A terminal
//     transition bumps neither the epoch nor worker_id (the terminality
//     paragraph in internal/store/query/tasks.sql states the same reachable
//     state in its own words), so an assignee that reports done at epoch N and
//     then failed at epoch N passes both gates legitimately every time, and
//     every message adds one to conflicting_total. Nothing rate-limits status
//     messages and this path spends no log-budget token, so it is unbounded,
//     silent and free. Measured: 10,000 forged messages produce
//     {Raced:0 Duplicate:0 Conflicting:10000} with every other counter in the
//     process flat.
//     THE CONSEQUENCE IS NOT THE NUMBER, IT IS WHAT THE NUMBER IS READ AS. The
//     shape produced is exactly the watchdog-margin signature, whose documented
//     remedy is to RAISE RELAY_TASK_WATCHDOG_MARGIN - widening the unbounded-
//     assignment window the watchdog exists to close, which is where a wedged
//     agent's incentive already points. The README's task_status_fence bullets
//     carry the operator-facing form: cross-check watchdog.counts.swept_by_worker
//     before raising anything. Not closed here, and closing it would need a
//     second round trip on the recv goroutine.
//   - THEY COUNT REPORTS, NOT TASKS. One task and one assignee can account for
//     the whole section.
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
// the cheaper failure. This branch is UNREACHABLE while TWO tests are green,
// AND IT TAKES BOTH - an earlier version of this comment named only the first
// and was measurably wrong about the likelier mistake.
// TestTaskStatusFenceReasonsAreADenseRunFromZero pins the three names to 0, 1, 2
// and the sentinel to 3, which is the renumbering case.
// TestEveryTaskStatusFenceReasonIsDeclaredInsideTheSentinel is the one that sees
// a fourth reason declared AFTER the sentinel or in a separate block: measured,
// that edit leaves the dense-run test green (its run list still holds three
// entries and the sentinel is still 3), leaves the publish test green (it
// iterates r < fenceReasonCount), and lands every one of its increments here.
//
// THE CHECK IS AN UPPER BOUND ONLY, AND THAT IS THE WHOLE GUARD. Do not restore
// a `< 0` arm to match ingestLogCounters.record's shape: taskStatusFenceReason
// is uint8, so int(r) cannot be negative and such an arm is dead code wearing
// the costume of a control. The shape WAS copied from there, and it does not
// transfer - that method's `i <= 0` is live because logKind starts at 1, which
// its own comment records as a measured mutation survivor. Reasons here start at
// 0 (see the const block), so slot 0 is a real cell and excluding it would lose
// every `raced`. len(c.n) rather than fenceReasonCount because the array is what
// is being indexed; the two are the same number by construction and the array is
// the one that panics. Measured: deleting this `if` fails
// TestTaskStatusFenceCounters_AnOutOfRangeReasonIsDroppedNotPanicked with
// "index out of range [3] with length 3", so the arm that remains is
// load-bearing.
func (c *statusFenceCounters) record(r taskStatusFenceReason) {
	i := int(r)
	if i >= len(c.n) {
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

// taskStatusIsWritable mirrors the status allow-list carried by BOTH statements
// handleTaskStatus writes through - UpdateTaskStatus and IncrementTaskRetryCount
// (internal/store/query/tasks.sql; the rule is stated once, at UpdateTaskStatus,
// and the two must change together).
//
// READ THE STAKE BEFORE COPYING THIS SOMEWHERE ELSE. Every other status
// allow-list in this tree is a CONTROL: it decides whether a write happens, and
// CLAUDE.md's allow-list-never-deny-list rule exists because the wrong shape
// fails OPEN on the next status added. This one decides nothing. It LABELS A
// COUNTER, so drift mislabels a number and cannot admit a write. It is written
// as the allow-list anyway so that its shape matches the SQL it mirrors, and
// TestTaskStatusWritableSetMatchesTheSQLAllowList exploits that: the case list
// below is read straight out of this source with go/ast and required to EQUAL
// the parsed SQL allow-list, as a set. That is a genuine two-way comparison and
// not a complement check, so a status added here that appears in no other source
// in the tree - not the SQL, not tasks_status_check, not the proto - is still
// caught. An earlier version of this sentence claimed the comparison before the
// comparison existed; the guard iterated a candidate set instead, and `cancelled`
// added here went through it green.
//
// A NEW NON-TERMINAL STATUS (`preparing` is the live candidate: TASK_STATUS_
// PREPARING is already in the proto) MUST BE ADDED HERE at the same time it is
// added to those two SQL allow-lists, or a rejection for such a row is labelled
// `duplicate`/`conflicting` when it is in fact a race. Adding it here first
// fails the set comparison; adding it to the SQL first fails the containment
// loop. Either order goes RED until both sides move.
//
// KEEP THE STATUSES SPELLED INLINE. Moving them to a var, a const or another
// function is not a refactor here - it is what the guard's own fail-closed arm
// exists for, and it fails with a message saying so.
func taskStatusIsWritable(status string) bool {
	switch status {
	case "pending", "dispatched", "preparing", "running":
		return true
	}
	return false
}

// classifyStatusFenceRejection labels a rejection from the row THIS HANDLER READ
// AT T0 and the status the agent reported, both of which are already in hand at
// the rejection site. No round trip, no allocation.
//
// SAY WHAT IT KNOWS AND WHAT IT DOES NOT. It does not know which SQL predicate
// fired at T1 - the statement yields no row, so nothing can carry that. It knows
// whether the row was ALREADY unwritable when this handler read it, which is a
// sufficient condition for the rejection (a terminal row is one-way: the only
// statement that reopens one, RetryJobTasks, bumps the epoch, so the agent's
// next report is rejected by the currency gate instead).
//
// THAT CLAIM HAS A SILENT DEPENDENCY, named here because "the only statement" is
// a claim about the complement and cannot be checked by opening its subject.
// tasks.sql carries a second epoch-only status write - the variant that fences
// on assignment_epoch alone, with neither a status predicate nor a bump - and it
// could move a row terminal -> terminal or terminal -> writable at a FIXED
// epoch, which is precisely the shape this classifier assumes away. The claim
// holds only because that statement is test-only, and only because that is
// enforced rather than asserted: internal/store/updatetaskstatusepoch_guard_test.go
// fails if its identifier appears in any non-test Go file under internal/. (It
// is named here by its file and not by its identifier for exactly that reason -
// spelling the identifier in this comment turns that guard RED, which is
// measured, not assumed.) If that guard is ever relaxed, re-derive this
// paragraph before trusting `duplicate` and `conflicting`.
//
// A row that was still
// writable at T0 and rejected at T1 therefore had its generation ended INSIDE
// this handler's own window, which is what `raced` names.
//
// The consequence, stated so the key names are not over-read: a `done` report on
// a `running` row that the watchdog sweeps inside the window is labelled
// `raced`, not `conflicting`. That is honest - a concurrent writer ended it -
// and it is why `raced` is documented as the floor rather than as a measurement.
func classifyStatusFenceRejection(rowStatus, reported string) taskStatusFenceReason {
	if taskStatusIsWritable(rowStatus) {
		return fenceReasonRaced
	}
	if rowStatus == reported {
		return fenceReasonDuplicate
	}
	return fenceReasonConflicting
}
