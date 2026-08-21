package main

import (
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
