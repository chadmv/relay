package main

import (
	"context"
	"fmt"
	"log"

	"relay/internal/netlimit"
	"relay/internal/worker"

	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// grpcMaxConcurrentStreams is 1 because AgentService has exactly ONE RPC and
// zero unary methods (internal/proto/relayv1/relay_grpc.pb.go:102-115), and an
// agent opens exactly one stream per connection and dials a fresh ClientConn per
// reconnect attempt (internal/agent/agent.go:202-209). "One stream per
// connection" is therefore a property of the wire contract, not a convention.
//
// THERE IS DELIBERATELY NO ENV KNOB. There are exactly TWO legitimate reasons for
// this to move, and each has its own guard, in a different file.
//
//   - A PROTO CHANGE giving AgentService a second RPC.
//     TestAgentServiceHasExactlyOneStreamPerConnection catches that with a
//     message naming this constant.
//   - REGISTERING A SECOND SERVICE on the same grpc.Server - health, reflection,
//     channelz. MaxConcurrentStreams is a per-CONNECTION limit across every
//     service registered on that server, not a per-service one, so a second
//     service registration would make one stream too few with the proto
//     untouched. The test above inspects AgentService_ServiceDesc alone and
//     cannot see what else was registered. That half is guarded by parsing
//     main.go: TestGRPCAdmissionIsWiredByMain check 6 requires the gRPC server
//     value to be passed to exactly one call. This sentence used to claim the
//     guard was "asserting the server hosts exactly one service", which was
//     true of a server the TEST built and registered one service on - vacuous by
//     construction, and proved so by adding reflection.Register(grpcSrv) to
//     main.go with the package still green.
//
// An operator knob here could only LOOSEN a security control: the value multiplies worker.ingestLogLimiter's
// per-connection budget one-for-one, because that limiter is allocated once per
// Connect call, i.e. once per STREAM (internal/worker/handler.go:172).
//
// Cost to a legitimate agent: zero. Cost to an attacker: the per-connection
// multiplication now needs a connection, which netlimit bounds.
const grpcMaxConcurrentStreams = 1

// grpcBounds is the resolved, already-parsed admission configuration. It is a
// plain struct so tests can construct a server from the PRODUCTION option list
// with a 200ms idle timeout and no env var, no global and no build tag.
type grpcBounds struct {
	maxConns      int           // total live connections; 0 disables
	maxConnsPerIP int           // live connections per source PREFIX; 0 disables
	maxConnIdle   time.Duration // reap a transport with no stream; 0 disables
	// registrationTimeout bounds the first stream.Recv in worker.Handler.Connect.
	// It is admission configuration and belongs in the startup line with the
	// rest, even though grpcServerOptions has no use for it: it is the only one
	// of the four that cannot be disabled, and an operator debugging agents that
	// are dropped before they register needs to see it.
	registrationTimeout time.Duration
}

// grpcServerOptions is the complete option list for the agent gRPC server.
// EXACTLY ONE grpc.KeepaliveParams may appear here - see grpcKeepaliveParams.
func grpcServerOptions(b grpcBounds) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
		grpc.KeepaliveParams(grpcKeepaliveParams(b.maxConnIdle)),
		grpc.KeepaliveEnforcementPolicy(grpcEnforcementPolicy()),
	}
}

// grpcKeepaliveParams is ONE struct carrying three decisions, and it is a
// separate function because they must not be split across two options: grpc-go
// stores keepaliveParams wholesale (server.go:330-332), so a second
// grpc.KeepaliveParams(...) silently discards Time and Timeout.
//
//   - Time/Timeout are the pre-existing liveness probe. Unchanged.
//   - MaxConnectionIdle reaps a transport that completed the HTTP/2 preface and
//     then never opened a stream. It can NEVER terminate a connection that is
//     doing its job: t.idle is zeroed when the first stream opens and re-stamped
//     only when the last one closes (http2_server.go:582-585, :1299-1306), and a
//     zero t.idle reschedules rather than reaping (:1204-1220). That is exactly
//     why it is here and MaxConnectionAge is not.
//   - A zero idle value is passed straight through: grpc-go maps it to infinity
//     (:219-221), so "disabled" needs no branch here.
//
// NOTE WHAT MaxConnectionIdle DOES NOT COVER. Two cases, and the second is the
// strictly cheaper one, so leaving it out of this list was the more dangerous
// half of an otherwise accurate sentence:
//
//   - A peer that completes TCP and then says nothing at all never reaches
//     transport construction, so it is bounded instead by grpc-go's
//     connectionTimeout, 120s by default (server.go:193).
//   - A peer that completes the preface AND OPENS A STREAM, then sends nothing
//     on it. This is NOT reaped, for the same reason the option is safe for real
//     agents: opening a stream zeroes t.idle, and a zero t.idle reschedules
//     rather than reaps. The keepalive liveness probe does not reach it either,
//     because any frame the peer reads re-stamps t.lastRead. Such a peer parks
//     its connection slot for free and forever. That case is bounded on relay's
//     side of the wire instead, by worker.DefaultRegistrationTimeout, which ends
//     the STREAM and so hands the connection back to this option. It does not
//     end the CONNECTION - MaxConnectionAge is the arm that would, and it is out
//     of this slice - so a peer willing to open a fresh stream once per idle
//     window still holds its slot, now at the cost of a periodic round trip
//     rather than free.
func grpcKeepaliveParams(idle time.Duration) keepalive.ServerParameters {
	return keepalive.ServerParameters{
		Time:              30 * time.Second, // ping after 30s of transport inactivity
		Timeout:           10 * time.Second, // close the transport if no ack within 10s
		MaxConnectionIdle: idle,
	}
}

// grpcKeepaliveMinTime is grpc-go's OWN defaultKeepalivePolicyMinTime
// (internal/transport/defaults.go:40), restated here on purpose.
//
// This value is not picked; it is the unique non-regressive one. grpc-go already
// enforces a 5m floor whether you set a policy or not (http2_server.go:241-244),
// so anything smaller is a LOOSENING. Anything larger would start refusing pings
// grpc-go accepts today, and no principal sends them: the agent configures no
// client keepalive at all (internal/agent/agent.go:196-202), so
// defaultClientKeepaliveTime is infinity and it sends none, ever.
//
// LOWERING THIS IS THE ONLY WAY TO MAKE IT MATTER, AND IS A REGRESSION. The
// realistic failure mode this line exists to prevent is somebody "adding a
// keepalive policy" with MinTime: 10*time.Second because that is what the
// internet suggests, silently loosening a control by a factor of 30.
const grpcKeepaliveMinTime = 5 * time.Minute

func grpcEnforcementPolicy() keepalive.EnforcementPolicy {
	return keepalive.EnforcementPolicy{
		MinTime:             grpcKeepaliveMinTime,
		PermitWithoutStream: false,
	}
}

// parseConnLimit resolves one of the two gRPC connection caps into the value
// handed to netlimit.Config, plus a startup message to log, empty when there is
// nothing to say. It follows parseWatchdogDuration's three-outcome shape
// (cmd/relay-server/watchdog_config.go:41), with its own prose:
//
//   - Unset, or a valid integer above 1: used as-is, silently.
//   - Exactly zero: ACCEPTED, and the cap is disabled. Because disabling an
//     admission control must never be silent, this returns an informational line
//     naming what is now unbounded.
//   - Exactly one: KEPT, and warned about. See below.
//   - Negative or unparseable: the default is used and the message says so. A
//     silently-ignored typo would leave an operator believing they had tightened
//     a security-relevant knob they had not.
//
// There is no FLOOR outcome in parseWatchdogDuration's sense - a floor catches
// units confusion, and a bare connection count has no units. But there is
// exactly ONE positive value that is not a legitimate statement about fleet size
// or NAT topology, and it is 1. An agent closes its old connection and dials the
// new one from the client side (internal/agent/agent.go:206), while this server
// releases the slot only on OBSERVING the close, so with a cap of 1 an agent can
// be refused its own reconnect and back off. README says in bold not to set it
// and this parser used to accept it in silence; one of the two had to give. The
// value is KEPT, because narrowing a cap is the operator's prerogative, matching
// parseGRPCConnIdle's sub-floor arm.
//
// Not a log.Fatalf, following parseTrailingLogWindow and parseWatchdogDuration:
// a bad limit must not stop a server booting when a safe default exists.
func parseConnLimit(name, raw string, def int) (int, string) {
	if raw == "" {
		return def, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def, fmt.Sprintf("%s=%q is not a non-negative integer; using %d", name, raw, def)
	}
	if n == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: this gRPC connection cap is disabled. Admission on the agent port is bounded only by "+
				"the process file-descriptor limit, and every per-connection control (including "+
				"worker.ingestLogLimiter's log budget) multiplies without a ceiling.", name, raw)
	}
	if n == 1 {
		return n, fmt.Sprintf(
			"%s=%q leaves no headroom for a reconnect. An agent closes its old connection and dials the "+
				"new one itself, but this server releases the slot only when it observes the close, so an "+
				"agent can be refused its own reconnect and back off. Using it anyway; 2 or more is "+
				"strongly preferred.", name, raw)
	}
	return n, ""
}

// minGRPCConnIdleDur is the floor for RELAY_GRPC_MAX_CONN_IDLE. A legitimate
// agent's idle window is the gap between grpc.NewClient dialing and
// client.Connect opening its stream (internal/agent/agent.go:202-209), which is
// sub-millisecond on a LAN. The floor is not that number - it is the point below
// which a scheduling stall on a loaded host could plausibly exceed the window,
// GOAWAYing agents before they ever open a stream and leaving them
// reconnect-looping. One second is three orders above the real window and still
// well inside "obviously a mistake" territory.
const minGRPCConnIdleDur = time.Second

// parseGRPCConnIdle resolves RELAY_GRPC_MAX_CONN_IDLE. Same contract as
// parseWatchdogDuration, floor included: this knob DOES have a fail-aggressive
// direction, so a sub-floor value is KEPT and warned about rather than rejected.
func parseGRPCConnIdle(name, raw string, def time.Duration) (time.Duration, string) {
	if raw == "" {
		return def, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return def, fmt.Sprintf("%s=%q is not a Go duration (or is negative); using %s", name, raw, def)
	}
	if d == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: idle gRPC transport reaping is disabled. A peer that completes the HTTP/2 handshake "+
				"and never opens a stream now holds its connection slot forever. Note that ENABLING it "+
				"does not close connection parking either: a peer that opens a stream and then stays "+
				"silent is not idle by this option's definition, and is bounded instead by "+
				"RELAY_GRPC_REGISTRATION_TIMEOUT, which ends the stream and hands the connection back "+
				"here.", name, raw)
	}
	if d < minGRPCConnIdleDur {
		return d, fmt.Sprintf(
			"%s=%q resolves to %s, below the %s floor. Using it anyway, but a legitimate agent may be "+
				"disconnected between dialing and opening its stream and will reconnect-loop. Check the "+
				"units (%s, not %s?).", name, raw, d, minGRPCConnIdleDur, def, d)
	}
	return d, ""
}

// Defaults for the three knobs.
//
//   - defaultGRPCMaxConns: far above any plausible relay fleet and far below
//     where file descriptors or goroutines hurt. Anchor: RELAY_DB_MAX_CONNS
//     defaults to 25 and an agent's recv loop holds at most one in-flight
//     statement, so more than ~25 simultaneously busy agents already queue on
//     the pool.
//   - defaultGRPCMaxConnsPerIP: NOT DERIVABLE from this repo, and said so out
//     loud. One agent process is strictly one connection at a time
//     (internal/agent/agent.go:184-212), so the legitimate maximum is "how many
//     agent processes share a source address", which depends on NAT topology
//     nothing here can see. 64 is chosen generously and is reversible.
//   - defaultGRPCMaxConnIdle: THIS VALUE IS THE ATTACKER'S DUTY CYCLE, not a
//     tolerance for slow middleboxes, and the direction that matters is the one
//     the previous derivation did not multiply. A peer parking connection slots
//     re-establishes each one once per window and pays nothing else: no
//     credential, no stream, no database work. At 15m, holding all 1024 slots
//     cost 1024/900 = 1.14 new TCP connections per second, sustained - so the
//     option that exists to close parking was itself the cheapest parking route
//     on the port, and a peer that completed the handshake parked a slot 7.5x
//     LONGER than one that said nothing at all (grpcConnectionTimeout). A
//     legitimate agent's honest window - dial to first stream - is
//     sub-millisecond and the warned floor is 1s, so 60s still leaves about four
//     orders of magnitude of headroom and costs a stream-holding connection
//     exactly nothing.
//   - defaultGRPCRegistrationTimeout: worker.DefaultRegistrationTimeout owns the
//     reasoning; it is restated here only as this knob's default.
//
// THE LAST TWO NUMBERS ARE NOT INDEPENDENT, and pricing the idle window alone
// understated the cheapest hold available on this port. A peer that opens a
// stream and never registers is cut off at defaultGRPCRegistrationTimeout, which
// ends the STREAM and hands the connection back to the idle reaper, and only
// then reaped at defaultGRPCMaxConnIdle: it holds its slot for the SUM, 90s,
// measured at 701ms for 300ms + 400ms in
// TestGRPCServer_RegistrationDeadlineAndIdleWindowCompose. Holding all 1024
// slots therefore costs 1024/90 = 11.4 new TCP connections per second, not the
// ~17/s the idle window alone advertised - so the composite is the CHEAPER of
// the two routes, and the earlier comparison to grpc-go's 120s pointed the wrong
// way. 90s is still under that 120s, which is the property that matters, and
// resolveGRPCBounds warns when an operator's values stop satisfying it.
const (
	defaultGRPCMaxConns            = 1024
	defaultGRPCMaxConnsPerIP       = 64
	defaultGRPCMaxConnIdle         = 60 * time.Second
	defaultGRPCRegistrationTimeout = worker.DefaultRegistrationTimeout
)

// grpcConnectionTimeout is grpc-go's OWN defaultServerOptions.connectionTimeout
// (grpc@v1.80.0/server.go:193), restated here because it is the benchmark every
// parking bound on this port is measured against: it is how long a peer that
// completes TCP and then says NOTHING AT ALL holds its slot, for free. Any
// relay-side bound whose hold exceeds it has made saying something cheaper than
// saying nothing, which inverts the control.
const grpcConnectionTimeout = 120 * time.Second

// minRegistrationTimeout is the point below which parseRegistrationTimeout says
// out loud what the value costs. A legitimate agent sends its RegisterRequest
// immediately after opening the stream, so the honest window is one network
// round trip; a sub-second bound would start cutting off agents on a loaded host
// or a slow link, and they would reconnect-loop with nothing to diagnose from.
const minRegistrationTimeout = time.Second

// parseRegistrationTimeout resolves RELAY_GRPC_REGISTRATION_TIMEOUT.
//
// ONE DELIBERATE DIVERGENCE FROM parseGRPCConnIdle, WHICH IT OTHERWISE MIRRORS:
// zero does NOT disable. Every other knob here can be switched off because an
// operator may have bounded the same thing elsewhere - a proxy can cap
// connections. No proxy can enforce "send a RegisterRequest within N seconds",
// because that is an application-layer property of relay's own protocol, so a
// disable here would have no substitute and would restore a free, permanent,
// fleet-wide denial. An operator who genuinely wants the old behaviour writes a
// very large duration and can see in the startup line that they did.
func parseRegistrationTimeout(name, raw string, def time.Duration) (time.Duration, string) {
	if raw == "" {
		return def, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return def, fmt.Sprintf(
			"%s=%q is not a positive Go duration; using %s. This bound cannot be disabled: an unbounded "+
				"wait for the first RegisterRequest lets any peer park a connection slot for free.",
			name, raw, def)
	}
	if d < minRegistrationTimeout {
		return d, fmt.Sprintf(
			"%s=%q resolves to %s, below the %s floor. Using it anyway, but an agent on a slow link or a "+
				"loaded host may be disconnected before its RegisterRequest arrives and will "+
				"reconnect-loop. Check the units (%s, not %s?).", name, raw, d, minRegistrationTimeout, def, d)
	}
	return d, ""
}

// resolveGRPCBounds parses all four gRPC admission knobs out of getenv and
// returns the resolved bounds plus every startup message to log, in order.
//
// IT IS A FUNCTION RATHER THAN INLINE CODE IN main() FOR TWO REASONS, and only
// the second is tidiness. First, a structural guard over main.go could see that
// netlimit.Wrap was called but not that `grpcBnds = grpcBounds{}` had been
// inserted just above it - a disclosed blind spot that disabled the entire
// control while every package stayed green. Building the value in one place that
// main cannot shadow removes that by construction. Second, the warning emissions
// had no direct test; here they are ordinary return values.
func resolveGRPCBounds(getenv func(string) string) (grpcBounds, []string) {
	var msgs []string
	add := func(m string) {
		if m != "" {
			msgs = append(msgs, m)
		}
	}

	maxConns, m := parseConnLimit(
		"RELAY_GRPC_MAX_CONNS", getenv("RELAY_GRPC_MAX_CONNS"), defaultGRPCMaxConns)
	add(m)
	maxConnsPerIP, m := parseConnLimit(
		"RELAY_GRPC_MAX_CONNS_PER_IP", getenv("RELAY_GRPC_MAX_CONNS_PER_IP"), defaultGRPCMaxConnsPerIP)
	add(m)
	maxConnIdle, m := parseGRPCConnIdle(
		"RELAY_GRPC_MAX_CONN_IDLE", getenv("RELAY_GRPC_MAX_CONN_IDLE"), defaultGRPCMaxConnIdle)
	add(m)
	registrationTimeout, m := parseRegistrationTimeout(
		"RELAY_GRPC_REGISTRATION_TIMEOUT", getenv("RELAY_GRPC_REGISTRATION_TIMEOUT"),
		defaultGRPCRegistrationTimeout)
	add(m)

	// THE LAST TWO BOUNDS COMPOSE, AND THIS IS THE ONLY PLACE THAT SEES BOTH.
	// Every parse function above can only price its own knob, and each of these
	// two is individually reasonable at values whose SUM is not. A peer that
	// opens a stream and never registers is disconnected at registrationTimeout -
	// which ends the STREAM, handing the connection back to the idle window - and
	// only then reaped at maxConnIdle. It holds its slot for the sum. Measured,
	// not assumed: 300ms + 400ms held a slot for 704ms
	// (TestGRPCServer_RegistrationDeadlineAndIdleWindowCompose).
	//
	// grpcConnectionTimeout is what the same slot costs a peer that says NOTHING
	// at all. Once the sum exceeds it, completing the handshake and opening a
	// stream is the CHEAPER way to park - so the two controls that exist to close
	// parking become the cheapest parking route on the port, with nothing red and
	// nothing logged. That is reachable from documented settings: README's
	// RELAY_GRPC_REGISTRATION_TIMEOUT row sanctions a large value for a slow
	// fleet, and 2m at the default 60s idle window is 180s.
	//
	// Guarded on maxConnIdle > 0 because a DISABLED idle window makes the hold
	// unbounded rather than merely long, and parseGRPCConnIdle already says
	// exactly that. A second line quoting a finite sum would understate it.
	if maxConnIdle > 0 && registrationTimeout+maxConnIdle > grpcConnectionTimeout {
		add(fmt.Sprintf(
			"RELAY_GRPC_REGISTRATION_TIMEOUT=%s and RELAY_GRPC_MAX_CONN_IDLE=%s COMPOSE to %s. A peer "+
				"that opens a stream and never registers holds its connection slot for that whole time: "+
				"the registration deadline ends the STREAM and hands the connection back to the idle "+
				"reaper. That is longer than the %s grpc-go allows a peer that says nothing at all, so "+
				"opening a stream is now the CHEAPEST way to park a slot on this port - which is the "+
				"denial these two bounds exist to close. Lower either value until the sum is under %s.",
			registrationTimeout, maxConnIdle, registrationTimeout+maxConnIdle,
			grpcConnectionTimeout, grpcConnectionTimeout))
	}

	return grpcBounds{
		maxConns:            maxConns,
		maxConnsPerIP:       maxConnsPerIP,
		maxConnIdle:         maxConnIdle,
		registrationTimeout: registrationTimeout,
	}, msgs
}

// grpcBoundsLine renders the unconditional startup line naming every effective
// admission bound, in the shape of watchdogBoundsLine
// (cmd/relay-server/watchdog_config.go:88). It must say DISABLED explicitly for
// each knob that is off: a disabled safety bound is never allowed to be silent.
func grpcBoundsLine(b grpcBounds) string {
	total := fmt.Sprintf("%d total", b.maxConns)
	if b.maxConns <= 0 {
		total = "total DISABLED"
	}
	perIP := fmt.Sprintf("%d per source IP", b.maxConnsPerIP)
	if b.maxConnsPerIP <= 0 {
		perIP = "per-source-IP DISABLED"
	}
	idle := fmt.Sprintf("idle transports reaped after %s", b.maxConnIdle)
	if b.maxConnIdle <= 0 {
		idle = "idle reaping DISABLED"
	}
	// No DISABLED arm for the last clause: registrationTimeout cannot be switched
	// off (see parseRegistrationTimeout), so there is no off state to report.
	return fmt.Sprintf(
		"gRPC admission: %d stream(s) per connection; connections %s, %s; %s; register within %s",
		grpcMaxConcurrentStreams, total, perIP, idle, b.registrationTimeout)
}

// grpcRefusalReportInterval is how often the refusal summary may speak. One line
// per minute, and only when something moved.
const grpcRefusalReportInterval = time.Minute

// refusalReporter turns netlimit's counters into at most one log line per
// interval. A line per refusal is deliberately NOT an option: it would be a new
// unbounded attacker-driven log site inside the control that bounds
// attacker-driven log volume. The line names counts and never addresses, so no
// caller-supplied byte can reach the log through it.
//
// tick is separate from runRefusalReporter so the "only when counters move"
// property can be driven directly, with no timer and no sleeping.
type refusalReporter struct {
	last netlimit.Stats
	logf func(format string, args ...any)
}

func (r *refusalReporter) tick(s netlimit.Stats) {
	if s == r.last {
		return
	}
	r.logf("gRPC admission: %d connection(s) refused over the total cap and %d over the per-source-IP cap since startup",
		s.RefusedTotal, s.RefusedPerIP)
	r.last = s
}

// runRefusalReporter logs a refusal summary at most once per interval, in the
// shape of runEnrollmentJanitor (cmd/relay-server/main.go:269).
func runRefusalReporter(ctx context.Context, l *netlimit.Listener, interval time.Duration) {
	r := &refusalReporter{logf: log.Printf}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(l.Stats())
		}
	}
}
