package main

import (
	"fmt"
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
// THERE IS DELIBERATELY NO ENV KNOB. The only legitimate reason for this to move
// is a proto change, and TestAgentServiceHasExactlyOneStreamPerConnection catches
// that with a message naming this constant. An operator knob here could only
// LOOSEN a security control: the value multiplies worker.ingestLogLimiter's
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
	maxConnsPerIP int           // live connections per source IP; 0 disables
	maxConnIdle   time.Duration // reap a transport with no stream; 0 disables
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
// Note what MaxConnectionIdle does NOT cover: a peer that completes TCP and then
// says nothing at all never reaches transport construction, so it is bounded
// instead by grpc-go's connectionTimeout, 120s by default (server.go:193).
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
//   - Unset, or a valid non-negative integer: used as-is, silently.
//   - Exactly zero: ACCEPTED, and the cap is disabled. Because disabling an
//     admission control must never be silent, this returns an informational line
//     naming what is now unbounded.
//   - Negative or unparseable: the default is used and the message says so. A
//     silently-ignored typo would leave an operator believing they had tightened
//     a security-relevant knob they had not.
//
// There is deliberately NO floor outcome, unlike parseWatchdogDuration. A floor
// catches units confusion, and a bare connection count has no units; any positive
// value is a legitimate statement about fleet size or NAT topology.
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
				"and never opens a stream now holds its connection slot forever, which turns the "+
				"connection caps into a parking primitive.", name, raw)
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
//   - defaultGRPCMaxConnIdle: a legitimate agent's idle window is the gap
//     between dialing and opening its stream, sub-millisecond on a LAN, so this
//     is bounded below only by paranoia about slow middleboxes.
const (
	defaultGRPCMaxConns      = 1024
	defaultGRPCMaxConnsPerIP = 64
	defaultGRPCMaxConnIdle   = 15 * time.Minute
)

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
	return fmt.Sprintf("gRPC admission: %d stream(s) per connection; connections %s, %s; %s",
		grpcMaxConcurrentStreams, total, perIP, idle)
}
