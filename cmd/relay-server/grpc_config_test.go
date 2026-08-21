package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"relay/internal/netlimit"
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
// existing Time/Timeout liveness probe, and this pins the VALUES that struct
// carries.
//
// IT DOES NOT CATCH THE SECOND-OPTION REGRESSION, contrary to what the plan for
// this slice claimed. Appending a second grpc.KeepaliveParams(...) with an
// inline literal compiles, silently discards Time and Timeout (grpc-go
// overwrites o.keepaliveParams wholesale, grpc@v1.80.0/server.go:330-332), and
// leaves this test perfectly green - because this test calls
// grpcKeepaliveParams directly and never looks at the option list. A mutation
// run proved exactly that. The option list is guarded by
// TestGRPCServerOptionsHasExactlyOneKeepaliveParams; the two are complements.
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

// TestGRPCBoundsLine mirrors TestWatchdogBoundsLine. A mechanism that can REFUSE
// a user's agent must state its limits at every boot: the ordinary path is
// otherwise completely silent, so an operator cannot tell from the log whether
// the caps are on, what they are, or that somebody switched them off.
func TestGRPCBoundsLine(t *testing.T) {
	all := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 64, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, all, "1024")
	assert.Contains(t, all, "64")
	assert.Contains(t, all, "15m")
	// The plan asked for `Contains(all, "1")` here with the message below. That
	// assertion is VACUOUS - "1024" already contains "1", so it holds under any
	// implementation that never mentions the stream cap at all. Asserting the
	// rendered phrase is what actually pins it.
	assert.Contains(t, all, "1 stream(s) per connection",
		"the stream cap is part of the admission story and must be named")
	assert.NotContains(t, all, "DISABLED")

	noTotal := grpcBoundsLine(grpcBounds{maxConns: 0, maxConnsPerIP: 64, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, noTotal, "DISABLED")
	assert.Contains(t, noTotal, "64", "one cap off is not both caps off; saying so sends an operator hunting the wrong thing")

	noPerIP := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 0, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, noPerIP, "DISABLED")
	assert.Contains(t, noPerIP, "1024")

	noIdle := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 64, maxConnIdle: 0})
	assert.Contains(t, noIdle, "DISABLED")
	assert.Contains(t, noIdle, "1024")

	off := grpcBoundsLine(grpcBounds{})
	assert.Equal(t, 3, strings.Count(off, "DISABLED"),
		"all three knobs off is the single most important thing this line can say")
}

// TestRefusalSummaryLogsOnlyWhenCountersMove.
//
// A log.Printf per refused connection would be a new, unbounded,
// ATTACKER-DRIVEN log site on the exact path this slice exists to bound - the
// 2026-08-15 lesson one layer down. The reporter is bounded at one line per
// interval BY CONSTRUCTION, and it carries counts only.
//
// This test is necessary and NOT sufficient: adding a per-refusal line inside
// netlimit leaves this reporter perfectly correct. That half is pinned by
// TestLimitListener_RefusalWritesNothingToTheLog, in the package where the
// refusal happens.
func TestRefusalSummaryLogsOnlyWhenCountersMove(t *testing.T) {
	type line struct {
		format string
		args   []any
	}
	var lines []line
	r := &refusalReporter{logf: func(f string, a ...any) { lines = append(lines, line{f, a}) }}

	r.tick(netlimit.Stats{})
	assert.Empty(t, lines, "a quiet interval must produce no line at all")

	r.tick(netlimit.Stats{RefusedPerIP: 3})
	require.Len(t, lines, 1, "the first movement must be reported")

	r.tick(netlimit.Stats{RefusedPerIP: 3})
	assert.Len(t, lines, 1,
		"an unchanged counter must not re-log: a sustained attack must cost ONE line per interval, not one per tick")

	r.tick(netlimit.Stats{RefusedTotal: 2, RefusedPerIP: 3})
	require.Len(t, lines, 2, "a movement on the OTHER counter must also be reported")

	for i, l := range lines {
		assert.Contains(t, l.format, "%d", "line %d must be a counts template", i)
		require.NotEmpty(t, l.args)
		for _, a := range l.args {
			assert.IsType(t, uint64(0), a,
				"the summary must carry COUNTS ONLY. A caller-supplied byte here (a peer address) would "+
					"make this an attacker-writable log site inside the slice that bounds attacker-driven "+
					"log volume.")
		}
	}
}

// TestGRPCAdmissionIsWiredByMain is a structural guard in the same spirit as
// TestWatchdogIsStartedByMain (watchdog_config_test.go:129). Deleting the
// netlimit.Wrap call from main.go compiles and leaves `go test ./...` fully
// green - netlimit keeps its own passing unit tests, grpc_config keeps its own,
// and the agent port silently has no connection bound again, which is the entire
// bug. The end-to-end test next door builds its OWN listener, so it cannot see
// this.
//
// go/ast, NOT a regex: a source-scanning regex guard in this repo was proven
// breakable by a single stray comment.
//
// WHAT IT CANNOT REACH, stated rather than overclaimed: it cannot tell that
// `grpcBnds = grpcBounds{}` inserted just above the Wrap call disables both caps,
// and it cannot tell that runRefusalReporter was handed a pre-cancelled context.
// Both compile and both leave every package green.
func TestGRPCAdmissionIsWiredByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name assigned -> identifiers its RHS mentions, so the walk can follow
	// `x := netlimit.Wrap(...)` and then srv.Serve(x).
	from := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		var rhs []string
		for _, e := range as.Rhs {
			ast.Inspect(e, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					rhs = append(rhs, id.Name)
				}
				return true
			})
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				from[id.Name] = append(from[id.Name], rhs...)
			}
		}
		return true
	})
	reaches := func(seed, target string) bool {
		seen := map[string]bool{}
		queue := []string{seed}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			if name == target {
				return true
			}
			queue = append(queue, from[name]...)
		}
		return false
	}
	mentions := func(n ast.Node, want string) bool {
		found := false
		ast.Inspect(n, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == want {
				found = true
			}
			return !found
		})
		return found
	}

	// 1. The listener handed to Serve must derive from netlimit.Wrap.
	var serveArg string
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Serve" || len(ce.Args) != 1 {
			return true
		}
		if id, ok := ce.Args[0].(*ast.Ident); ok {
			serveArg = id.Name
		}
		return true
	})
	require.NotEmpty(t, serveArg, "main.go has no `<server>.Serve(<listener>)` call with a single identifier argument")
	require.True(t, reaches(serveArg, "Wrap"),
		"the listener passed to grpcSrv.Serve(%s) does not derive from netlimit.Wrap: the gRPC port has NO "+
			"connection cap, in total or per source IP, and nothing else fails", serveArg)

	// 2. grpc.NewServer must be built from grpcServerOptions, or the stream cap,
	//    the keepalive policy and MaxConnectionIdle are all absent.
	var newServer *ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := ce.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewServer" {
			newServer = ce
		}
		return true
	})
	require.NotNil(t, newServer, "main.go no longer calls grpc.NewServer")
	require.True(t, mentions(newServer, "grpcServerOptions"),
		"grpc.NewServer must be built from grpcServerOptions(...): otherwise MaxConcurrentStreams, the "+
			"keepalive enforcement policy and MaxConnectionIdle are all silently absent")

	// 3. The refusal reporter must be started, and NOT from inside a conditional.
	//    A `go` statement nested in an if-body would leave refusals unreported
	//    while an ast.Inspect walk happily found it.
	started := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.List {
			gs, ok := stmt.(*ast.GoStmt)
			if ok && mentions(gs.Call, "runRefusalReporter") {
				started = true
			}
		}
	}
	require.True(t, started,
		"main.go has no `go runRefusalReporter(...)` statement directly in a function body: refused "+
			"connections are counted and never surfaced, so an operator sees agents fail to appear with "+
			"nothing in the log")
}

// TestGRPCServerOptionsHasExactlyOneKeepaliveParams.
//
// NOT IN THE PLAN, AND IT EXISTS BECAUSE THE PLAN WAS WRONG HERE. The plan
// asserted that appending a second grpc.KeepaliveParams(...) to the option list
// would be caught by TestGRPCKeepaliveParamsKeepsTheLivenessProbe. It is not,
// and the mutation battery proved it in both directions:
//
//   - Appending grpc.KeepaliveParams(grpcKeepaliveParams(b.maxConnIdle)) a
//     SECOND time is idempotent - the same struct overwrites itself - so it is
//     not a defect at all and killing it would prove nothing.
//   - Appending an INLINE keepalive.ServerParameters{MaxConnectionIdle: ...} IS
//     the defect: grpc-go stores keepaliveParams wholesale
//     (grpc@v1.80.0/server.go:330-332), so the later option silently discards
//     Time and Timeout and the 30s/10s liveness probe becomes grpc-go's 2h
//     default. Every test in this package stayed green under it, because they
//     all call grpcKeepaliveParams directly and never look at the option LIST.
//
// So this guard reads the option list itself. go/ast, not a regex, matching
// TestGRPCAdmissionIsWiredByMain: a source-scanning regex guard in this repo was
// proven breakable by a single stray comment.
//
// WHAT IT CANNOT REACH, stated rather than overclaimed: it is structural, so it
// cannot tell that grpcKeepaliveParams itself returned the wrong numbers. That
// half is TestGRPCKeepaliveParamsKeepsTheLivenessProbe's job. The two are
// complements, and neither alone covers the option.
func TestGRPCServerOptionsHasExactlyOneKeepaliveParams(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "grpc_config.go", nil, 0)
	require.NoError(t, err)

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "grpcServerOptions" {
			fn = d
		}
	}
	require.NotNil(t, fn, "grpc_config.go no longer declares grpcServerOptions")

	var args []ast.Expr
	ast.Inspect(fn, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "KeepaliveParams" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "grpc" {
			args = append(args, ce.Args...)
		}
		return true
	})

	require.Len(t, args, 1,
		"grpcServerOptions must contain EXACTLY ONE grpc.KeepaliveParams option. grpc-go stores "+
			"keepaliveParams wholesale (server.go:330-332), so a second one silently discards Time and "+
			"Timeout and the 30s/10s liveness probe becomes grpc-go's 2h default. Put every keepalive "+
			"decision inside grpcKeepaliveParams instead.")

	call, ok := args[0].(*ast.CallExpr)
	require.True(t, ok, "the grpc.KeepaliveParams argument must be a call to grpcKeepaliveParams, not a literal")
	id, ok := call.Fun.(*ast.Ident)
	require.True(t, ok && id.Name == "grpcKeepaliveParams",
		"grpc.KeepaliveParams must be handed grpcKeepaliveParams(...). An inline "+
			"keepalive.ServerParameters literal here is the exact shape that drops Time and Timeout, and "+
			"it is invisible to every test that calls grpcKeepaliveParams directly.")
}
