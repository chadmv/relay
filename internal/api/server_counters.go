package api

import (
	"net/http"
	"time"

	"relay/internal/netlimit"
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
//   - NO FIELD ANYWHERE CARRIES A CALLER-SUPPLIED BYTE. The only non-integer
//     values in the whole payload are started_at and, when slice 4 lands, the
//     server-resolved worker UUIDs keying watchdog.counts.swept_by_worker.
//     TestCounterPayloadCarriesNoIdentifiers enforces that as an ALLOW-LIST, so
//     any third one goes RED and forces an argument. Worker UUIDs are admissible
//     HERE and remain inadmissible in any log line reachable from the gRPC recv
//     path, and those are two different arguments: this route is
//     admin-authenticated, so it is not an attacker-writable site.
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
//     process file-descriptor limit instead. Priced and accepted in
//     netlimit.Listener.Stats: this route's own BearerAuth costs a Postgres
//     round trip, which dominates the walk by orders of magnitude.
//
// HOW A FUTURE SECTION ATTACHES ITSELF, because the answer is NOT the same for
// every package and getting it wrong shows up as an import cycle:
//
//   - internal/netlimit is a stdlib-only leaf, so this package imports it and
//     the source interface can return netlimit.Stats directly.
//   - internal/worker is already imported by this package (server.go), so a
//     worker-side counters type works the same way.
//   - internal/scheduler IMPORTS THIS PACKAGE (scheduler/dispatch.go), so this
//     package can never import it. The watchdog section must therefore declare
//     its snapshot type HERE, next to the response types, and scheduler.Watchdog
//     returns that type. CounterSources is a struct of independent fields
//     precisely so each section can make that choice separately.

// GRPCAdmissionSource is whatever can report the agent-port admission
// counters - in production, *netlimit.Listener.
type GRPCAdmissionSource interface {
	Stats() netlimit.Stats
}

// CounterSources is the set of subsystem counter sources the endpoint
// assembles. Every field is nil-able and nil means the section is ABSENT from
// the payload, not zero. cmd/relay-server sets this after construction, in the
// established shape of Server.Metrics.
type CounterSources struct {
	GRPCAdmission GRPCAdmissionSource
}

type serverCountersResponse struct {
	StartedAt     time.Time             `json:"started_at"`
	GRPCAdmission *grpcAdmissionSection `json:"grpc_admission,omitempty"`
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
	writeJSON(w, http.StatusOK, resp)
}
