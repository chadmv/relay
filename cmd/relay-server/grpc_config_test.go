package main

import (
	"fmt"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// oneStreamPerConnection is the predicate behind the structural guard below. It
// is a named function so the guard can be exercised against SYNTHETIC service
// descriptors: mutating the real one would mean editing relay.proto and running
// buf generate, which this slice forbids, and a mutation you cannot run is a
// claim rather than a kill.
func oneStreamPerConnection(d grpc.ServiceDesc) error {
	if len(d.Methods) != 0 {
		return fmt.Errorf("%s has %d unary method(s); expected 0", d.ServiceName, len(d.Methods))
	}
	if len(d.Streams) != 1 {
		return fmt.Errorf("%s has %d stream(s); expected 1", d.ServiceName, len(d.Streams))
	}
	return nil
}

// TestAgentServiceHasExactlyOneStreamPerConnection is a tripwire, not a
// behaviour test: it is GREEN at HEAD by design and its failure message is the
// deliverable. grpcMaxConcurrentStreams is 1 because "one stream per connection"
// is a property of the wire contract. If AgentService ever gains a second RPC, a
// compliant client BLOCKS on stream quota rather than erroring - a miserable
// thing to debug in production. This turns that into a red test naming the fix.
func TestAgentServiceHasExactlyOneStreamPerConnection(t *testing.T) {
	require.NoError(t, oneStreamPerConnection(relayv1.AgentService_ServiceDesc),
		"AgentService no longer has exactly one streaming RPC and zero unary methods. RAISE "+
			"grpcMaxConcurrentStreams in cmd/relay-server/grpc_config.go to the new number of concurrent "+
			"streams an agent needs, or a client opening the second one will block until its deadline "+
			"with no error to explain why.")

	// The predicate must reject the shapes it exists to catch, or this guard is a
	// green test that checks nothing.
	assert.Error(t, oneStreamPerConnection(grpc.ServiceDesc{
		ServiceName: "synthetic", Streams: []grpc.StreamDesc{{}, {}}}),
		"two streams must be rejected")
	assert.Error(t, oneStreamPerConnection(grpc.ServiceDesc{
		ServiceName: "synthetic", Methods: []grpc.MethodDesc{{}}, Streams: []grpc.StreamDesc{{}}}),
		"a unary method must be rejected")
}

// TestGRPCKeepaliveParamsKeepsTheLivenessProbe.
//
// MaxConnectionIdle lives in the SAME keepalive.ServerParameters struct as the
// existing Time/Timeout liveness probe. Appending a second
// grpc.KeepaliveParams(...) option compiles, is the obvious way to write this
// diff, and silently discards Time and Timeout because the later option
// overwrites o.keepaliveParams wholesale (grpc@v1.80.0/server.go:330-332). This
// is the test that makes that regression red.
func TestGRPCKeepaliveParamsKeepsTheLivenessProbe(t *testing.T) {
	kp := grpcKeepaliveParams(15 * time.Minute)
	assert.Equal(t, 30*time.Second, kp.Time, "the 30s inactivity ping must survive")
	assert.Equal(t, 10*time.Second, kp.Timeout, "the 10s ping-ack deadline must survive")
	assert.Equal(t, 15*time.Minute, kp.MaxConnectionIdle)
	assert.Zero(t, kp.MaxConnectionAge,
		"MaxConnectionAge is deliberately OUT of this slice: it terminates connections that are doing "+
			"their job and costs a log chunk per forced reconnect. It has its own backlog item.")

	assert.Zero(t, grpcKeepaliveParams(0).MaxConnectionIdle,
		"0 must pass straight through: grpc-go maps a zero MaxConnectionIdle to infinity "+
			"(http2_server.go:219-221), so 'disabled' needs no relay-side branch")
}

// TestGRPCEnforcementPolicyMatchesGRPCsOwnDefault is a CONSTANT LOCKSTEP CHECK
// AND NOTHING MORE, and that is stated rather than implied.
//
// The policy we ship is behaviourally identical to grpc-go's default
// (defaultKeepalivePolicyMinTime = 5m, defaults.go:40; PermitWithoutStream's zero
// value is false), so no test can be RED at HEAD for it. A behavioural test is
// also unavailable: grpc.WithKeepaliveParams CLAMPS the client ping interval to
// internal.KeepaliveMinPingTime = 10s (dialoptions.go:561-565), and that knob is
// in a package relay cannot import - so the fastest honest abusive-pinger test
// would take ~40s and would straddle the exact value a realistic regression
// would use. What this test buys is that LOWERING MinTime - the only change that
// makes the option matter, and a regression - shows up as a red test in the diff
// that does it.
func TestGRPCEnforcementPolicyMatchesGRPCsOwnDefault(t *testing.T) {
	ep := grpcEnforcementPolicy()
	assert.Equal(t, 5*time.Minute, ep.MinTime,
		"5m is not picked, it IS grpc-go's defaultKeepalivePolicyMinTime. Anything smaller LOOSENS a "+
			"control; anything larger refuses pings grpc-go accepts today, for a principal that sends none "+
			"(the agent sets no client keepalive at all - internal/agent/agent.go:196-202).")
	assert.False(t, ep.PermitWithoutStream,
		"PermitWithoutStream=false is not independently testable in reasonable time - distinguishing it "+
			"from true needs a client pinging slower than MinTime with no stream, i.e. over five minutes. "+
			"This assertion pins the decision, not the behaviour.")
}

// TestParseConnLimit mirrors TestParseWatchdogDuration's table. Three outcomes,
// not two, which is why the second return is a message and not an ok bool.
//
// ONE DELIBERATE DEVIATION FROM parseWatchdogDuration: there is no `floor`
// outcome. A floor exists to catch units confusion (`24m` for `24h`), and a bare
// connection count has no units to confuse. Any positive value is a legitimate
// operator choice about fleet size or NAT topology.
func TestParseConnLimit(t *testing.T) {
	const def = 1024
	cases := []struct {
		name    string
		raw     string
		want    int
		wantMsg string
	}{
		{"unset keeps the default and is silent", "", def, ""},
		{"a valid value is used as-is", "64", 64, ""},
		{"1 is accepted without comment", "1", 1, ""},
		{"zero is ACCEPTED and disables the cap, with an informational line", "0", 0, "disabled"},
		{"negative keeps the default and warns", "-5", def, "not a non-negative integer"},
		{"unparseable keeps the default and warns", "lots", def, "not a non-negative integer"},
		{"a float keeps the default and warns", "64.5", def, "not a non-negative integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseConnLimit("RELAY_GRPC_MAX_CONNS", tc.raw, def)
			assert.Equal(t, tc.want, got)
			if tc.wantMsg == "" {
				assert.Empty(t, msg, "a valid value must not produce startup noise")
				return
			}
			require.Contains(t, msg, tc.wantMsg,
				"the message is the only signal an operator gets; it must name the consequence")
			assert.Contains(t, msg, "RELAY_GRPC_MAX_CONNS", "the message must name the variable it is about")
		})
	}
}

// TestParseGRPCConnIdle keeps parseWatchdogDuration's four-outcome shape,
// INCLUDING the floor - unlike the integer knob, this one has a fail-aggressive
// direction (a sub-second value reaps a legitimate agent between its dial and its
// first stream, so it reconnect-loops forever).
func TestParseGRPCConnIdle(t *testing.T) {
	def := 15 * time.Minute
	cases := []struct {
		name    string
		raw     string
		want    time.Duration
		wantMsg string
	}{
		{"unset keeps the default and is silent", "", def, ""},
		{"a valid value is used as-is", "5m", 5 * time.Minute, ""},
		{"zero is ACCEPTED and disables reaping, with an informational line", "0s", 0, "disabled"},
		{"negative keeps the default and warns", "-5m", def, "not a Go duration"},
		{"unparseable keeps the default and warns", "fifteen", def, "not a Go duration"},
		{"below the floor KEEPS the value and warns", "200ms", 200 * time.Millisecond, "below"},
		{"exactly the floor is silent", "1s", time.Second, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseGRPCConnIdle("RELAY_GRPC_MAX_CONN_IDLE", tc.raw, def)
			assert.Equal(t, tc.want, got)
			if tc.wantMsg == "" {
				assert.Empty(t, msg)
				return
			}
			require.Contains(t, msg, tc.wantMsg)
			assert.Contains(t, msg, "RELAY_GRPC_MAX_CONN_IDLE")
		})
	}
}
