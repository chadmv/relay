package api

import (
	"net/http"
	"time"

	"relay/internal/netlimit"
	"relay/internal/worker"
)

// GET /v1/server/counters is relay's ONE process-lifetime counter surface. It
// exists because relay ships several controls that stop something bad quietly -
// a connection cap that refuses, a fence that drops a forged log chunk, a
// limiter that suppresses a repeating line, a watchdog that ends an assignment -
// and the operator-visible signature of an attack against a silent control is
// FEWER log lines than normal, which is indistinguishable from a healthy fleet.
// See docs/superpowers/specs/2026-08-21-silent-drop-observability.md.
//
// THE CONTRACT (the payload is published; reshaping it is a breaking change):
//
//   - "counts" are MONOTONIC since started_at. "levels" are CURRENT. A reporter
//     may consult counts to decide whether to speak and may NEVER consult
//     levels: a level moves constantly, so a reporter that compared one would
//     speak every interval forever.
//   - An unwired section is ABSENT, never zero-valued. A section of zeros means
//     "this control ran and stopped nothing"; an absent section means "this
//     build or this replica does not have that control wired". Collapsing the
//     two reintroduces the very defect this payload exists to fix, inside the
//     payload.
//   - started_at is ALWAYS present, including when every section is absent. A
//     restart zeroes everything, so "the counter stopped moving" and "the
//     process restarted" are otherwise identical.
//   - PER REPLICA, per process, best effort, zeroed by a restart. Counts from
//     two replicas may be added; levels may NOT (max_per_source in particular
//     does not sum into anything meaningful). No persistence, no history, no
//     rates, no alerting - a poller derives rates itself.
//   - NO FIELD ANYWHERE CARRIES A CALLER-SUPPLIED BYTE. Two exemptions:
//     started_at, an RFC 3339 instant; and watchdog.counts.swept_by_worker, a
//     map from server-assigned worker uuids to counts, bounded by
//     WatchdogSweptWorkerMax and admitted by counterPayloadExemption
//     predicates that DESCEND into the map and enforce the cap. Anything else
//     non-integer goes RED and forces the same argument.
//
// WHAT THIS ENDPOINT DOES NOT BUY:
//
//   - A zero level is not necessarily an empty control. When BOTH gRPC
//     connection caps are disabled (RELAY_GRPC_MAX_CONNS=0 and
//     RELAY_GRPC_MAX_CONNS_PER_IP=0) netlimit.Listener.Accept returns the
//     connection unwrapped and does no accounting at all, so every field of
//     grpc_admission.levels reads 0 with thousands of live connections. "Not
//     measured" and "nothing there" are the same payload there, which is this
//     endpoint's own subject one layer down. Closing it needs either a boolean
//     (banned by the counts-only rule) or the configured caps as extra fields,
//     and "max_per_source" as an observed maximum next to "max_per_source" as
//     a configured cap is a naming trap. See netlimit.Stats.
//   - Serving grpc_admission is not free at every configuration. max_per_source
//     is an O(len(perIP)) walk under the listener's mutex, and len(perIP) is
//     bounded by RELAY_GRPC_MAX_CONNS only while that cap is enabled; with the
//     total cap disabled and the per-source cap live, it is bounded by the
//     process file-descriptor limit instead. What the walk delays is the gRPC
//     ACCEPT PATH, not this request, and nothing rate-limits this route. See
//     netlimit.Listener.Stats.
//
// HOW A FUTURE SECTION ATTACHES ITSELF, because the answer is NOT the same for
// every package and getting it wrong shows up as an import cycle:
//
//   - internal/netlimit is a stdlib-only leaf, so this package imports it and
//     the source interface can return netlimit.Stats directly.
//   - internal/worker is already imported by this package (server.go), so a
//     worker-side counters type works the same way. (ingest_log_budget is the
//     live example: *worker.Handler satisfies IngestLogBudgetSource and returns
//     worker.IngestLogDrops directly.)
//   - internal/scheduler IMPORTS THIS PACKAGE (scheduler/dispatch.go), so this
//     package can never import it. The watchdog section must therefore declare
//     its snapshot type HERE, next to the response types, and scheduler.Watchdog
//     returns that type. CounterSources is a struct of independent fields
//     precisely so each section can make that choice separately.
//
// AND THE RULE THAT HOLDS FOR EVERY SECTION, WHICHEVER OF THOSE THREE SHAPES IT
// TAKES: a section that copies a subsystem's snapshot into a response struct
// FIELD BY FIELD needs a CARDINALITY CHECK between the two types, written in
// this package - the one that imports both. A field-by-field mapper is the one
// link in the chain the compiler says nothing about - a field added on the
// subsystem side and not here compiles, vets and tests clean while its number
// is counted on the hot path and published under no JSON key. Pinned by
// TestIngestLogKindCountsPublishesEveryWorkerSideField. Note what does NOT
// substitute for it: counterPayloadLeaves is an ElementsMatch over this
// package's OWN payload, so it reddens on an extra leaf here and is silent on a
// missing one there.

// GRPCAdmissionSource is whatever can report the agent-port admission
// counters - in production, *netlimit.Listener.
type GRPCAdmissionSource interface {
	Stats() netlimit.Stats
}

// IngestLogBudgetSource is whatever can report what the per-connection agent log
// budgets have dropped - in production, *worker.Handler.
//
// ONE METHOD, AND A SEPARATE SOURCE FIELD PER WORKER COUNTER, so that "wired"
// stays a per-SECTION fact. Widening this interface to carry another counter
// would make independent controls appear and disappear together.
type IngestLogBudgetSource interface {
	IngestLogDropCounts() worker.IngestLogDrops
}

// TaskLogFenceSource is whatever can report how many task-log chunks the
// AppendTaskLog fence has rejected - in production, *worker.Handler.
//
// ITS OWN SOURCE FIELD, NOT A WIDENED IngestLogBudgetSource: the counters are
// wired together today, but they are independent CONTROLS counting different
// nouns, and an interface carrying more than one would make them appear and
// disappear together as a matter of TYPE rather than as a matter of wiring.
//
// A SCALAR, NOT A STRUCT, and that is load-bearing rather than minimal: a
// section whose payload struct restates fields owned by another package needs a
// cardinality check between the two types (see
// TestIngestLogKindCountsPublishesEveryWorkerSideField). This section restates
// nothing, so it needs no such check - a property pinned by
// TestTaskLogFenceSourceReturnsAScalar, which reddens if this return type is ever
// widened.
type TaskLogFenceSource interface {
	TaskLogFenceRejections() uint64
}

// TaskStatusFenceSource is whatever can report what handleTaskStatus's two
// epoch-fenced writes have refused - in production, *worker.Handler.
//
// ITS OWN SOURCE FIELD, not a widened sibling interface, for the reason on
// TaskLogFenceSource: independent controls must not appear and disappear
// together as a matter of TYPE.
//
// A PRODUCER-OWNED STRUCT, AND THE DIRECTION IS FORCED. internal/api imports
// internal/worker (server.go), so the watchdog's shape - declare the snapshot
// type in this package - is a compile error here. The section therefore WRAPS
// worker.TaskStatusFenceCounts rather than restating its fields, which means
// there is no hand-written mapper on either side and no arity to drift.
// TestTaskStatusFenceSectionRestatesNothing holds that.
type TaskStatusFenceSource interface {
	TaskStatusFenceRejections() worker.TaskStatusFenceCounts
}

// WatchdogSweptWorkerMax bounds how many distinct workers watchdog.counts.
// swept_by_worker will ever name. IT IS DECLARED HERE, IN internal/api, AND
// THAT IS NOT WHERE IT LOOKS LIKE IT BELONGS.
//
// The producer is internal/scheduler, so the constant "wants" to live beside the
// map. It cannot: this package's own counterPayloadAllowList predicate has to
// read it (an exemption is shape-checked but NON-DESCENDING, so a predicate that
// did not check the cap itself would leave the map's key count unchecked in this
// package entirely), and that test file is package api - importing
// internal/scheduler from it is the same cycle that forces WatchdogCounts to be
// declared here. One constant read by both the producer and the guard means the
// two cannot drift; two constants would.
//
// WHERE THE BOUND IS ACTUALLY ENFORCED IS watchdogCounters.record, in
// internal/scheduler: it refuses a NEW key at capacity and counts the sweep
// against SweptOverflow instead. Nothing in this package enforces anything at
// runtime - counterPayloadAllowList is a test predicate, and it runs against a
// fake source whose keys are literals in server_counters_test.go, so it can only
// ever say that THAT fixture is well-formed. The real producer's keys are read
// back through the real route in cmd/relay-server's
// TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap, which
// lives there because that package can import both sides and this one
// structurally cannot.
//
// 256 is a policy number, not a measurement. The map is FIRST-COME rather than
// top-K: top-K needs a comparison on every increment to buy an ordering that
// swept_overflow already discloses the absence of.
const WatchdogSweptWorkerMax = 256

// WatchdogCounts is what the coordinator stale-task watchdog has ended since
// started_at. It is declared HERE rather than in internal/scheduler because
// internal/scheduler imports this package (scheduler/dispatch.go), so the
// reverse import is impossible.
//
// THERE IS NO MAPPER ANYWHERE. This type is the response type:
// serverCountersResponse carries *WatchdogCounters directly and
// handleServerCounters assigns it whole, and scheduler.Watchdog stores a
// WatchdogCounts as its OWN counter state and returns a struct copy. A field
// added here is published by both sides for free, where a restating mapper
// would need an arity assertion instead.
// TestWatchdogSectionRestatesNothing guards the antecedent, and
// TestWatchdogCountersLiveOnlyInThePublishedStruct guards the only remaining
// way a counter can go unpublished.
//
// WHAT THESE NUMBERS DO NOT COVER, said here because it is the question an
// operator will get wrong: they count assignments THE COORDINATOR ended. An
// agent that honours its own timeout writes the same 'timed_out' status
// (worker/handler.go maps TASK_STATUS_TIMED_OUT straight through) and
// contributes NOTHING here, which is deliberate - the two mean opposite things
// about a worker's health and this counter SIDE-STEPS the ambiguity rather than
// resolving it.
//
// WHY NOT A DATABASE QUERY, which would be better on every other axis - no
// process state, survives restarts, correct across replicas: DECLINED, WITH THE
// PRICE, NOT IMPOSSIBLE. Telling a watchdog-written 'timed_out' from an
// agent-written one needs either a new terminal status threaded through every
// status allow-list, or a nullable writer column plus a migration on a write
// path that sits under the epoch fence - a larger and riskier change than the
// observability it buys. IF SUCH A COLUMN IS EVER ADDED FOR ANOTHER REASON,
// REVISIT THIS: the query route is genuinely better.
//
// PER REPLICA. The watchdog is multi-replica-safe by first-write-wins, so a
// sweep of worker X may be counted on either replica; add the counts across
// replicas, and expect neither replica's swept_by_worker to be the whole story.
type WatchdogCounts struct {
	// SweptTotal counts every assignment this process's watchdog ended,
	// including the ones attributed to SweptOverflow. It is what makes the
	// section reconcile: SweptTotal == sum(SweptByWorker) + SweptOverflow,
	// always, which is also why these three fields are read in ONE critical
	// section rather than as three independent atomics.
	SweptTotal uint64 `json:"swept_total"`

	// SweptOverflow counts sweeps attributable to a worker the map is not
	// tracking, either because the map was already at WatchdogSweptWorkerMax
	// when that worker was first swept, or because the row's worker id was not
	// renderable as a uuid. NON-ZERO MEANS PER-WORKER ATTRIBUTION IS INCOMPLETE
	// and the worst tracked worker may not be the worst worker. This field
	// exists to make a loss visible; a version of it that were counted and
	// unpublished would be the defect eating its own remedy.
	SweptOverflow uint64 `json:"swept_overflow"`

	// SweptByWorker maps a server-assigned worker uuid to how many of its
	// assignments this process's watchdog ended. NEVER nil - it serialises as
	// {} on a watchdog that has swept nothing, because null is not an object
	// and the payload's walks would have nothing to descend into. Capped at
	// WatchdogSweptWorkerMax; see counterPayloadAllowList for the argument that
	// admits it into a payload where every other leaf is an integer.
	SweptByWorker map[string]uint64 `json:"swept_by_worker"`
}

// WatchdogCounters is the watchdog section. COUNTS ONLY, and the absence of a
// levels half is a decision: the cap is a CONFIGURED CONSTANT rather than a
// level, and a constant in a levels half would have to MOVE when a limits
// classification is added
// (idea-2026-08-21-counters-payload-cannot-say-not-measured), which is a
// breaking change to a published payload. The cap is documented in README
// instead, and swept_overflow is the runtime signal that it bound.
type WatchdogCounters struct {
	Counts WatchdogCounts `json:"counts"`
}

// WatchdogSource is whatever can report the coordinator watchdog's sweep
// counters - in production, *scheduler.Watchdog.
//
// ITS OWN SOURCE FIELD, like every other section. And note the direction: this
// interface is declared here and SATISFIED over there, because internal/api can
// never name internal/scheduler.
//
// AN IMPLEMENTATION MUST RETURN A VALUE NOBODY ELSE STILL WRITES TO. This
// snapshot type carries a MUTABLE container (SweptByWorker), so a struct copy
// is not enough: a plausible one-line implementation - `func (x X)
// CounterSnapshot() WatchdogCounters { return x.c }` - hands this handler a map
// the producer's own goroutine keeps writing, and a single admin GET then ends
// the process with `fatal error: concurrent map read and map write` (that one is
// not recoverable, and -race is not needed to see it). It is also CLAUDE.md's
// "no interior pointers across locks" with the lock left implicit.
//
// scheduler.watchdogCounters.snapshot is the reference implementation: it clones
// the map inside the same critical section that reads the scalars, and
// TestWatchdogCounters_SnapshotIsACopy is its guard. TestWatchdogSectionRestatesNothing
// asserts that this section needs no mapper, which makes reusing WatchdogCounts
// as a source's own storage the natural shape - so read that as "share the
// type", never as "share the map".
type WatchdogSource interface {
	CounterSnapshot() WatchdogCounters
}

// CounterSources is the set of subsystem counter sources the endpoint
// assembles. Every field is nil-able and nil means the section is ABSENT from
// the payload, not zero.
//
// "NIL" HERE MEANS A NIL INTERFACE, AND A TYPED NIL IS NOT ONE. Storing a
// (*scheduler.Watchdog)(nil) - or any other typed nil pointer - in one of these
// fields produces an interface that is NOT == nil, so handleServerCounters'
// `src != nil` is true and the method call below it dereferences a nil receiver.
// Per admin request, that is a goroutine stack trace to the log, inside the
// feature whose subject is bounding log volume.
//
// The watchdog is legitimately disable-able (RELAY_TASK_WATCHDOG_MARGIN=0 and
// RELAY_TASK_MAX_ASSIGNMENT=0), so `var wd *scheduler.Watchdog; if enabled
// { wd = ... }; CounterSources{Watchdog: wd}` is the natural shape and it
// panics. Filter the typed nil where the CONCRETE type is still visible, at the
// wiring boundary: cmd/relay-server's buildHTTPServer is the live example, and
// TestBuildHTTPServer_TypedNilListenerLeavesTheSectionAbsent plus
// TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent are its
// guards. The filter is per httpServerDeps FIELD, not per CounterSources
// field: one deps field may feed several sections, covered by that field's
// single `if` - see the comment on buildHTTPServer's nil filter. Do
// not instead make the source's snapshot method nil-tolerant - returning a zero
// snapshot turns an unwired control into a section of zeros, which is the one
// distinction this payload exists to preserve.
type CounterSources struct {
	GRPCAdmission   GRPCAdmissionSource
	IngestLogBudget IngestLogBudgetSource
	TaskLogFence    TaskLogFenceSource
	TaskStatusFence TaskStatusFenceSource
	Watchdog        WatchdogSource
}

type serverCountersResponse struct {
	StartedAt       time.Time               `json:"started_at"`
	GRPCAdmission   *grpcAdmissionSection   `json:"grpc_admission,omitempty"`
	IngestLogBudget *ingestLogBudgetSection `json:"ingest_log_budget,omitempty"`
	TaskLogFence    *taskLogFenceSection    `json:"task_log_fence,omitempty"`

	// *taskStatusFenceSection wrapping worker.TaskStatusFenceCounts DIRECTLY.
	// The producing package owns the counts type (internal/api imports
	// internal/worker, so the reverse is a cycle), and this wrapper adds only
	// the "counts" nesting the payload contract requires. No field name appears
	// twice, so there is no mapper and no arity to drift.
	TaskStatusFence *taskStatusFenceSection `json:"task_status_fence,omitempty"`

	// *WatchdogCounters, not a *watchdogSection restating it. The source's own
	// type IS the response type, so there is no hand-written mapper and no arity
	// to drift. TestWatchdogSectionRestatesNothing keeps it that way.
	Watchdog *WatchdogCounters `json:"watchdog,omitempty"`
}

type grpcAdmissionSection struct {
	Counts grpcAdmissionCounts `json:"counts"`
	Levels grpcAdmissionLevels `json:"levels"`
}

type grpcAdmissionCounts struct {
	RefusedTotal uint64 `json:"refused_total"`
	// refused_per_source, not refused_per_ip: the cap is keyed on a SOURCE,
	// which is an exact IPv4 address but an aggregated /64 for IPv6. It also
	// under-reports whenever the fleet cap is saturated, because the total is
	// checked first - read it as a floor when live_total has reached the
	// configured maximum.
	RefusedPerSource uint64 `json:"refused_per_source"`
}

type grpcAdmissionLevels struct {
	LiveTotal       uint64 `json:"live_total"`
	DistinctSources uint64 `json:"distinct_sources"`
	MaxPerSource    uint64 `json:"max_per_source"`
}

// ingest_log_budget is COUNTS ONLY, and the absence of a levels half is a
// decision rather than an omission: every ingestLogLimiter is a per-connection
// stack local that dies with its frame, so there is no process-wide "current"
// figure to report without building the shared registry that type explicitly
// refuses to have.
//
// WHAT THE TWO ARMS MEAN, because summing them would be uninterpretable:
// "deduped" is a repeating failure being collapsed to one line per five minutes
// (healthy, and the number is how many chunks that one line represents);
// "suppressed" is a line dropped ENTIRELY because the connection's token bucket
// was empty (an attack or a misconfiguration).
//
// AND WHAT THEY DO NOT COUNT: these are LOG LINES THE BUDGET DROPPED, not
// diagnostics lost. A handler that decides not to log without consulting the
// budget contributes nothing - handleTaskStatus's pgx.ErrNoRows GetTask
// short-circuits before the budget and is counted nowhere, and handleTaskLog's
// fence-rejection arm never consults it at all (that one is counted in
// task_log_fence). handleTaskStatus's two epoch-fenced WRITE arms are the same
// shape and ARE counted, in task_status_fence below. All three sections are
// disjoint from this one: no input moves more than one of them.
type ingestLogBudgetSection struct {
	Counts ingestLogBudgetCounts `json:"counts"`
}

type ingestLogBudgetCounts struct {
	Deduped    ingestLogKindCounts `json:"deduped"`
	Suppressed ingestLogKindCounts `json:"suppressed"`
}

// ingestLogKindCounts is a STRUCT rather than a map[string]uint64, and that is
// the cardinality rule made structural: the kind set is closed at compile time,
// so named fields make an unbounded key set impossible AND keep both payload
// walks at full reach. A map would need a counterPayloadExemption whose
// predicates descend into it themselves, because an exemption is shape-checked
// but NON-DESCENDING.
//
// THESE KEYS ARE A RESPONSE CONTRACT tied to worker's logKind names; see that
// type's comment before renaming anything here.
type ingestLogKindCounts struct {
	TaskLogPersist       uint64 `json:"task_log_persist"`
	BadTaskIDLog         uint64 `json:"bad_task_id_log"`
	BadTaskIDStatus      uint64 `json:"bad_task_id_status"`
	StatusGetTask        uint64 `json:"status_get_task"`
	Inventory            uint64 `json:"inventory"`
	StatusRetryWrite     uint64 `json:"status_retry_write"`
	StatusUpdateWrite    uint64 `json:"status_update_write"`
	StatusFailDependents uint64 `json:"status_fail_dependents"`
}

func ingestLogKindCountsFrom(k worker.IngestLogDropsByKind) ingestLogKindCounts {
	return ingestLogKindCounts{
		TaskLogPersist:       k.TaskLogPersist,
		BadTaskIDLog:         k.BadTaskIDLog,
		BadTaskIDStatus:      k.BadTaskIDStatus,
		StatusGetTask:        k.StatusGetTask,
		Inventory:            k.Inventory,
		StatusRetryWrite:     k.StatusRetryWrite,
		StatusUpdateWrite:    k.StatusUpdateWrite,
		StatusFailDependents: k.StatusFailDependents,
	}
}

// task_log_fence is COUNTS ONLY and it is ONE NUMBER, both by decision.
//
// rejected_total counts task-log chunks that AppendTaskLog's FOUR-predicate
// fence refused: the sender is not the task's assignee, or its generation is
// stale, or the task finished longer ago than RELAY_TASKLOG_TRAILING_WINDOW, or
// the task id matches no row at all. THE THIRD IS LEGITIMATE and is the one an
// operator who set that knob too small hits constantly, which is why this number
// exists at all. THE FOURTH is `t.id = task_id`, and it is easy to
// forget because it looks like a lookup rather than a fence: a well-formed uuid
// naming no task yields pgx.ErrNoRows while being none of the other three, which
// TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers
// drives directly. An operator reading this number still concludes correctly,
// since that case is a forged or badly confused sender like the first two.
//
// WHY THEY ARE NOT SPLIT: the fence yields no row when any predicate fails,
// so there is nothing to carry a reason on. Recovering it needs a second round
// trip (forbidden on the recv goroutine) or a rewrite of AppendTaskLog's result
// contract. DECLINED WITH THE PRICE WRITTEN DOWN - not impossible; see the
// pgx.ErrNoRows arm in internal/worker/handler.go. Do not "improve" this into
// three fields without reading that first.
//
// AND WHAT IT DOES NOT COVER: it is not a subset or a superset of
// ingest_log_budget. That arm never consults the log budget, so no input moves
// both numbers and neither explains any part of the other.
type taskLogFenceSection struct {
	Counts taskLogFenceCounts `json:"counts"`
}

type taskLogFenceCounts struct {
	RejectedTotal uint64 `json:"rejected_total"`
}

// task_status_fence is COUNTS ONLY, and it is THREE KEYS THAT PARTITION rather
// than one scalar. It counts status reports handleTaskStatus's epoch-fenced
// writes refused, split by what the row said when the handler read it:
//
//   - raced_total: the row was still writable at T0, so a concurrent writer
//     ended the generation inside the handler's own read-to-write window. THE
//     ONE KEY THAT IS A FLOOR - the Go-side identity and currency gates reject
//     stale and forged reports a round trip earlier, so only that narrow window
//     reaches the SQL fence's worker_id and assignment_epoch predicates.
//   - duplicate_total: the row was already terminal and its status is the one
//     being reported. THE EXPECTED HEALTHY BASELINE, whose height depends on
//     agent retry behaviour. A non-zero value here is not an incident.
//   - conflicting_total: the row was already terminal with a DIFFERENT status.
//     THE ACTIONABLE ONE, and the reason this section exists: a task the
//     coordinator's stale-task watchdog stamped 'timed_out' whose agent then
//     reports 'done' lands here. That is a successful task recorded as a
//     timeout, which is what RELAY_TASK_WATCHDOG_MARGIN set too small produces.
//
// UNLIKE raced_total, THE OTHER TWO ARE EXACT rather than floors, and the
// asymmetry is worth knowing: nothing between GetTask and the write reads the
// row's status, so the terminality predicate has no Go-side pre-filter and every
// T0-terminal report that reaches the write is counted.
//
// THERE IS NO TOTAL, BY DECISION. The three partition the rejections, so a
// published sum would sit beside its own summands where it can only agree or be
// a bug.
//
// WHY THE ARMS ARE NOT SPLIT BY STATEMENT: IncrementTaskRetryCount and
// UpdateTaskStatus carry the IDENTICAL THREE FENCE predicates - epoch, worker
// id, terminality - which of the two runs is decided by the reported status and
// the row's retry budget rather than by anything about the rejection, and both
// mean the same thing to an operator - the agent's report of this task's
// outcome was discarded. Splitting by reason answers which rejection is
// alarming; splitting by statement does not.
//
// IncrementTaskRetryCount carries a FOURTH predicate, `retry_count < retries`,
// and it can never reach these counters. The Go gate in handleTaskStatus refuses
// to enter the retry branch on an exhausted budget, so the statement is not
// called at all. That is deliberate rather than incidental: a budget exhaustion
// is deterministic, single-writer and the normal end of a task's life, and
// classifyStatusFenceRejection would label it `raced` - putting a steady,
// agent-driven, unbudgeted increment on the one key here that is meant to sit
// near zero. See the gate's own comment in internal/worker/handler.go.
//
// A FINER SPLIT - WHICH SQL PREDICATE FIRED - IS DECLINED WITH THE PRICE, NOT
// IMPOSSIBLE. Both statements yield no row on any predicate failure, so nothing
// can carry a reason; recovering one needs a second round trip (forbidden on the
// recv goroutine) or a rewrite of both result contracts, as in task_log_fence
// above.
//
// AND WHAT IT DOES NOT COVER:
//
//   - IT IS NOT A CENSUS OF FENCE REJECTIONS. Dispatcher.failClaimedTask and
//     Watchdog.SweepOnce are fenced by the same statement and are counted
//     nowhere. This is the AGENT-REPORTED status path only.
//   - IT IS NOT COMPARABLE WITH task_log_fence.counts.rejected_total, which has
//     no equivalent Go-side pre-filter. No input moves both, and neither
//     explains any part of the other.
//   - IT DOES NOT RECONCILE WITH watchdog.counts.swept_total, and an operator
//     will try. The two are opposite ends of the same event seen from the
//     coordinator and from the agent, and they will not match: the watchdog also
//     sweeps tasks whose agents are gone and never report at all.
type taskStatusFenceSection struct {
	Counts worker.TaskStatusFenceCounts `json:"counts"`
}

// handleServerCounters assembles whichever sections are wired. It reads no
// request body, so readJSON is not involved; the response goes through
// writeJSON, matching handleGetWorkerMetrics.
func (s *Server) handleServerCounters(w http.ResponseWriter, r *http.Request) {
	resp := serverCountersResponse{StartedAt: s.startedAt}
	if src := s.Counters.GRPCAdmission; src != nil {
		st := src.Stats()
		resp.GRPCAdmission = &grpcAdmissionSection{
			Counts: grpcAdmissionCounts{
				RefusedTotal:     st.Counts.RefusedTotal,
				RefusedPerSource: st.Counts.RefusedPerIP,
			},
			Levels: grpcAdmissionLevels{
				LiveTotal:       st.Levels.LiveTotal,
				DistinctSources: st.Levels.DistinctSources,
				MaxPerSource:    st.Levels.MaxPerSource,
			},
		}
	}
	if src := s.Counters.IngestLogBudget; src != nil {
		d := src.IngestLogDropCounts()
		resp.IngestLogBudget = &ingestLogBudgetSection{Counts: ingestLogBudgetCounts{
			Deduped:    ingestLogKindCountsFrom(d.Deduped),
			Suppressed: ingestLogKindCountsFrom(d.Suppressed),
		}}
	}
	if src := s.Counters.TaskLogFence; src != nil {
		resp.TaskLogFence = &taskLogFenceSection{
			Counts: taskLogFenceCounts{RejectedTotal: src.TaskLogFenceRejections()},
		}
	}
	if src := s.Counters.TaskStatusFence; src != nil {
		// ONE ASSIGNMENT INTO A WRAPPER, NOT A FIELD-BY-FIELD COPY: the
		// producer's type IS the counts half, so a reason added in
		// internal/worker reaches a JSON key with no edit here.
		resp.TaskStatusFence = &taskStatusFenceSection{Counts: src.TaskStatusFenceRejections()}
	}
	if src := s.Counters.Watchdog; src != nil {
		// ONE ASSIGNMENT, NOT A FIELD-BY-FIELD COPY, and that is the whole
		// point: the source's type is the response type, so a counter added on
		// the scheduler side reaches a JSON key with no edit here.
		snap := src.CounterSnapshot()
		resp.Watchdog = &snap
	}
	writeJSON(w, http.StatusOK, resp)
}
