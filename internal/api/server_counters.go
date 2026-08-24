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
// THE CONTRACT, fixed for all four sections before the first one shipped so that
// no later slice reshapes a payload that is already in the wild:
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
//   - NO FIELD ANYWHERE CARRIES A CALLER-SUPPLIED BYTE. Two paths are exempt
//     today and each was argued in the commit that added it: started_at, as an
//     RFC 3339 instant; and watchdog.counts.swept_by_worker, as a map from
//     server-assigned worker uuids to counts, bounded by
//     WatchdogSweptWorkerMax. The second was written into slice 1's allow-list
//     against code nobody had written, DE-AUTHORIZED during slice 1's review
//     because pre-blessing it reduced its only forcing function to a one-line
//     edit, and re-added in slice 4 with predicates that DESCEND into the map
//     and enforce the cap - see counterPayloadExemption. Anything else
//     non-integer goes RED and forces the same argument.
//
// WHAT THIS ENDPOINT DOES NOT BUY, stated next to what it does:
//
//   - A zero level is not necessarily an empty control. When BOTH gRPC
//     connection caps are disabled (RELAY_GRPC_MAX_CONNS=0 and
//     RELAY_GRPC_MAX_CONNS_PER_IP=0) netlimit.Listener.Accept returns the
//     connection unwrapped and does no accounting at all, so every field of
//     grpc_admission.levels reads 0 with thousands of live connections. "Not
//     measured" and "nothing there" are the same payload there, which is this
//     endpoint's own subject one layer down. Not fixed in this slice: closing it
//     needs either a boolean (banned by the counts-only rule) or the configured
//     caps as extra fields, and "max_per_source" as an observed maximum next to
//     "max_per_source" as a configured cap is a naming trap. Documented in
//     netlimit.Stats, in README and here.
//   - Serving grpc_admission is not free at every configuration. max_per_source
//     is an O(len(perIP)) walk under the listener's mutex, and len(perIP) is
//     bounded by RELAY_GRPC_MAX_CONNS only while that cap is enabled; with the
//     total cap disabled and the per-source cap live, it is bounded by the
//     process file-descriptor limit instead. What the walk delays is the gRPC
//     ACCEPT PATH, not this request: this route's BearerAuth is paid in a
//     different goroutine and completes before the handler runs, so it never
//     overlaps holding that mutex, and cmd/relay-server's runRefusalReporter
//     takes the same walk once a minute whether or not anybody polls here.
//     Nothing rate-limits this route. Measured and priced in
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
// this package, because this is the only place both are visible. A
// field-by-field mapper is the one link in the chain the compiler says nothing
// about - a field added on the subsystem side and not here compiles, vets and
// tests clean while its number is counted on the hot path and published under no
// JSON key. Every other link in ingest_log_budget's chain already had such a
// check (kinds against the counters array, the array against
// worker.IngestLogDropsByKind, CounterSources' fields against
// cmd/relay-server's wiring table, the response against this handler's
// branches); the mapper's was missed and is now
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
// ONE METHOD, AND A SEPARATE FIELD FROM ANY FUTURE WORKER COUNTER. The task-log
// fence-rejection counter (idea-2026-08-14) will live on the same *worker.Handler
// and must get its OWN source field and its own section, so that "wired" stays a
// per-SECTION fact. Widening this interface to carry both would make two
// independent controls appear and disappear together.
type IngestLogBudgetSource interface {
	IngestLogDropCounts() worker.IngestLogDrops
}

// TaskLogFenceSource is whatever can report how many task-log chunks the
// AppendTaskLog fence has rejected - in production, *worker.Handler.
//
// ITS OWN SOURCE FIELD, NOT A WIDENED IngestLogBudgetSource, exactly as that
// interface's comment demands. The two counters live on the same *worker.Handler
// and are wired together today, but they are independent CONTROLS counting
// different nouns, and an interface carrying both would make them appear and
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
// ever say that THAT fixture is well-formed. The one place the real producer's
// keys are read back through the real route is cmd/relay-server's
// TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap, which
// exists because that package can import both sides and this one structurally
// cannot.
//
// 256 is a policy number, not a measurement: it comfortably exceeds any fleet
// this project has seen, and the design is FIRST-COME rather than top-K because
// top-K needs a comparison on every increment to buy an ordering that
// swept_overflow already discloses the absence of.
const WatchdogSweptWorkerMax = 256

// WatchdogCounts is what the coordinator stale-task watchdog has ended since
// started_at. It is declared HERE rather than in internal/scheduler because
// internal/scheduler imports this package (scheduler/dispatch.go), so the
// reverse import is impossible - which INVERTS the shape ingest_log_budget uses,
// where the producing package owned the type.
//
// THAT INVERSION IS WHY THERE IS NO MAPPER ANYWHERE. This type is the response
// type: serverCountersResponse carries *WatchdogCounters directly and
// handleServerCounters assigns it whole, and scheduler.Watchdog stores a
// WatchdogCounts as its OWN counter state and returns a struct copy. A field
// added here is published by both sides for free. That matters because slice 2
// shipped a fully correct sixth log kind that was counted on one side and
// published under no JSON key on the other, with all three packages green; the
// remedy there was an arity assertion between two restated types, and the better
// remedy is not to restate. TestWatchdogSectionRestatesNothing guards the
// antecedent, and TestWatchdogCountersLiveOnlyInThePublishedStruct guards the
// only remaining way a counter can go unpublished.
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
// agent-written one needs either a new terminal status (threaded through every
// status allow-list, including the two that must be read BACKWARDS -
// AppendTaskLog's first arm and ListOverdueAssignedTasks - plus
// TestTasksStatusVocabularyIsExactly) or a nullable writer column plus a
// migration on a write path that sits under the epoch fence. That is a larger
// and riskier slice than the observability it buys. IF SUCH A COLUMN IS EVER
// ADDED FOR ANOTHER REASON, REVISIT THIS: the query route is genuinely better.
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
// levels half is a decision: the only candidates were len(SweptByWorker), which
// is the map itself restated and can only ever agree or be a bug, and the 256
// cap, which is a CONFIGURED CONSTANT rather than a level. A constant in a
// levels half would have to MOVE when a limits classification is added
// (idea-2026-08-21-counters-payload-cannot-say-not-measured), and that is a
// breaking change to a published payload. It is documented in README instead,
// and swept_overflow is the runtime signal that it bound.
type WatchdogCounters struct {
	Counts WatchdogCounts `json:"counts"`
}

// WatchdogSource is whatever can report the coordinator watchdog's sweep
// counters - in production, *scheduler.Watchdog.
//
// ITS OWN SOURCE FIELD, like every other section. And note the direction: this
// interface is declared here and SATISFIED over there, because internal/api can
// never name internal/scheduler.
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
// This is not hypothetical for the next section to land. The watchdog is
// legitimately disable-able (RELAY_TASK_WATCHDOG_MARGIN=0 and
// RELAY_TASK_MAX_ASSIGNMENT=0), so `var wd *scheduler.Watchdog; if enabled
// { wd = ... }; CounterSources{Watchdog: wd}` is the natural shape and it
// panics. Filter the typed nil where the CONCRETE type is still visible, at the
// wiring boundary: cmd/relay-server's buildHTTPServer is the live example, and
// TestBuildHTTPServer_TypedNilListenerLeavesTheSectionAbsent plus
// TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent are its
// guards - one per wired source, because the filter is per httpServerDeps
// FIELD. Not per CounterSources field: one deps field may feed several sections
// (agentHandler feeds two), and they are covered by that field's single `if` -
// see the comment on buildHTTPServer's nil filter. Do
// not instead make the source's snapshot method nil-tolerant - returning a zero
// snapshot turns an unwired control into a section of zeros, which is the one
// distinction this payload exists to preserve.
type CounterSources struct {
	GRPCAdmission   GRPCAdmissionSource
	IngestLogBudget IngestLogBudgetSource
	TaskLogFence    TaskLogFenceSource
	Watchdog        WatchdogSource
}

type serverCountersResponse struct {
	StartedAt       time.Time               `json:"started_at"`
	GRPCAdmission   *grpcAdmissionSection   `json:"grpc_admission,omitempty"`
	IngestLogBudget *ingestLogBudgetSection `json:"ingest_log_budget,omitempty"`
	TaskLogFence    *taskLogFenceSection    `json:"task_log_fence,omitempty"`

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
// short-circuits before the budget, and handleTaskLog's fence-rejection arm
// never consults it at all (that one is counted in task_log_fence, and the two
// numbers are disjoint).
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
// but NON-DESCENDING - slice 1 demonstrated a map[string]string with a
// newline-injected key passing both guards.
//
// THESE KEYS ARE A RESPONSE CONTRACT tied to worker's logKind names; see that
// type's comment before renaming anything here.
type ingestLogKindCounts struct {
	TaskLogPersist  uint64 `json:"task_log_persist"`
	BadTaskIDLog    uint64 `json:"bad_task_id_log"`
	BadTaskIDStatus uint64 `json:"bad_task_id_status"`
	StatusGetTask   uint64 `json:"status_get_task"`
	Inventory       uint64 `json:"inventory"`
}

func ingestLogKindCountsFrom(k worker.IngestLogDropsByKind) ingestLogKindCounts {
	return ingestLogKindCounts{
		TaskLogPersist:  k.TaskLogPersist,
		BadTaskIDLog:    k.BadTaskIDLog,
		BadTaskIDStatus: k.BadTaskIDStatus,
		StatusGetTask:   k.StatusGetTask,
		Inventory:       k.Inventory,
	}
}

// task_log_fence is COUNTS ONLY and it is ONE NUMBER, both by decision.
//
// rejected_total counts task-log chunks that AppendTaskLog's FOUR-predicate
// fence refused: the sender is not the task's assignee, or its generation is
// stale, or the task finished longer ago than RELAY_TASKLOG_TRAILING_WINDOW, or
// the task id matches no row at all. THE THIRD IS LEGITIMATE and is the one an
// operator who set that knob too small hits constantly, which is why this number
// exists at all - before it there was no runtime signal of any kind that task
// output was being dropped. THE FOURTH is `t.id = task_id`, and it is easy to
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
	if src := s.Counters.Watchdog; src != nil {
		// ONE ASSIGNMENT, NOT A FIELD-BY-FIELD COPY, and that is the whole
		// point: the source's type is the response type, so a counter added on
		// the scheduler side reaches a JSON key with no edit here. Slice 2's
		// mapper needed TestIngestLogKindCountsPublishesEveryWorkerSideField
		// precisely because it was five hand-written assignments.
		snap := src.CounterSnapshot()
		resp.Watchdog = &snap
	}
	writeJSON(w, http.StatusOK, resp)
}
