package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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

	// THE PROTO IS ONLY HALF OF IT. MaxConcurrentStreams is a per-CONNECTION
	// limit across every service registered on the server, not a per-service one,
	// so registering a SECOND service - health, reflection, channelz - makes 1
	// too few with relay.proto untouched. The assertions above inspect
	// AgentService_ServiceDesc and are structurally unable to see that.
	srv := grpc.NewServer(grpcServerOptions(grpcBounds{})...)
	t.Cleanup(srv.Stop)
	relayv1.RegisterAgentServiceServer(srv, &blockingAgentService{})
	assert.Len(t, srv.GetServiceInfo(), 1,
		"a SECOND service is registered on the agent gRPC server. grpcMaxConcurrentStreams is 1 because "+
			"AgentService needs one stream per connection, but that budget is shared across every service "+
			"on the server - so a client using both will block on stream quota until its deadline with no "+
			"error to explain why. Raise grpcMaxConcurrentStreams to the new total.")
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
		// README says in bold "Do not set it to 1", and the parser used to accept
		// it in silence. Both cannot be right. The race README names is real -
		// internal/agent/agent.go:206 defers conn.Close, but the server releases
		// the slot only on OBSERVING the FIN - so a reconnecting agent's new
		// connection can arrive while its own previous one still holds the only
		// slot, and it then backs off. The value is KEPT (narrowing a cap is the
		// operator's prerogative) and warned about, matching parseGRPCConnIdle's
		// sub-floor arm, which is the same fail-aggressive shape.
		{"1 is KEPT but warns about the reconnect race", "1", 1, "reconnect"},
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
	// THE PHRASE, NOT THE NUMBER. Contains(all, "1024") and Contains(all, "64")
	// hold just as well when the two caps are rendered into each other's slots,
	// labels included - a mutation that swapped them left this test green while
	// the startup line said "connections 64 total, 1024 per source IP" and named
	// the WRONG knob as the disabled one in every single-off case below. Same
	// substring-vacuity family as the Contains(all, "1") case already caught
	// here.
	assert.Contains(t, all, "1024 total")
	assert.Contains(t, all, "64 per source IP")
	assert.Contains(t, all, "15m")
	// The plan asked for `Contains(all, "1")` here with the message below. That
	// assertion is VACUOUS - "1024" already contains "1", so it holds under any
	// implementation that never mentions the stream cap at all. Asserting the
	// rendered phrase is what actually pins it.
	assert.Contains(t, all, "1 stream(s) per connection",
		"the stream cap is part of the admission story and must be named")
	assert.NotContains(t, all, "DISABLED")

	// Each single-off case must name the knob that is off AND leave the other
	// two alone. Asserting only Contains(x, "DISABLED") cannot tell "the total
	// cap is off" from "the per-source cap is off", which is the entire content
	// of the line for an operator debugging refused agents.
	noTotal := grpcBoundsLine(grpcBounds{maxConns: 0, maxConnsPerIP: 64, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, noTotal, "total DISABLED")
	assert.NotContains(t, noTotal, "per-source-IP DISABLED",
		"one cap off is not both caps off; saying so sends an operator hunting the wrong thing")
	assert.Contains(t, noTotal, "64 per source IP")

	noPerIP := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 0, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, noPerIP, "per-source-IP DISABLED")
	assert.NotContains(t, noPerIP, "total DISABLED")
	assert.Contains(t, noPerIP, "1024 total")

	noIdle := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 64, maxConnIdle: 0})
	assert.Contains(t, noIdle, "idle reaping DISABLED")
	assert.NotContains(t, noIdle, "total DISABLED")
	assert.Contains(t, noIdle, "1024 total")

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
// CHECK 1 USED TO BE THE WHOLE OF IT, AND CHECK 1 ALONE IS NEARLY VACUOUS. It
// asserts only that the identifier `Wrap` appears somewhere in the assignment
// chain behind Serve's argument. It says nothing about the netlimit.Config that
// Wrap was handed, and two mutations proved it: `MaxTotal: 0, MaxPerIP: 0`
// disables the entire admission control and passed, and swapping the two fields
// (total 64, per-source 1024 - capping the fleet at 64 AND letting one source
// take every slot) also passed. Check 2 reads the literal.
//
// WHAT IT STILL CANNOT REACH, stated rather than overclaimed: it cannot tell that
// runRefusalReporter was handed a pre-cancelled context. The
// `grpcBnds = grpcBounds{}` blind spot named in the previous version of this
// comment is closed twice over - the bounds now come from resolveGRPCBounds
// rather than a literal main can shadow, and check 2 requires the identifier
// feeding netlimit.Config to be assigned EXACTLY ONCE in this file.
func TestGRPCAdmissionIsWiredByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name assigned -> identifiers its RHS mentions, so the walk can follow
	// `x := netlimit.Wrap(...)` and then srv.Serve(x). intoField is the same for
	// `h.Field = x`, which is how check 5 follows the handler wiring.
	from := map[string][]string{}
	intoField := map[string][]string{}
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
			switch lhs := l.(type) {
			case *ast.Ident:
				from[lhs.Name] = append(from[lhs.Name], rhs...)
			case *ast.SelectorExpr:
				intoField[lhs.Sel.Name] = append(intoField[lhs.Sel.Name], rhs...)
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

	// 2. The netlimit.Config handed to Wrap must carry the RESOLVED caps, in the
	//    right fields. `reaches(serveArg, "Wrap")` is true for
	//    netlimit.Config{MaxTotal: 0, MaxPerIP: 0} and for a config with the two
	//    fields swapped; both were run and both passed before this check existed.
	var cfg *ast.CompositeLit
	ast.Inspect(file, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if sel, ok := cl.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Config" {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "netlimit" {
				cfg = cl
			}
		}
		return true
	})
	require.NotNil(t, cfg, "main.go builds no netlimit.Config literal")

	// field name -> the selector it is assigned from, e.g. MaxTotal -> maxConns.
	wantField := map[string]string{"MaxTotal": "maxConns", "MaxPerIP": "maxConnsPerIP"}
	gotField := map[string]string{}
	var bases []string
	for _, e := range cfg.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		sel, ok := kv.Value.(*ast.SelectorExpr)
		require.True(t, ok,
			"netlimit.Config.%s is not read out of the resolved bounds. A constant here (0 disables the "+
				"cap entirely) compiles and leaves every package in this repo green.", key.Name)
		gotField[key.Name] = sel.Sel.Name
		if id, ok := sel.X.(*ast.Ident); ok {
			bases = append(bases, id.Name)
		}
	}
	for field, want := range wantField {
		require.Equal(t, want, gotField[field],
			"netlimit.Config.%s must come from grpcBounds.%s. Swapping the two caps compiles, is invisible "+
				"to every other test, and produces a server that caps the whole fleet at the per-source "+
				"number while letting a single source take every remaining slot.", field, want)
	}

	// The value they are read out of must derive from resolveGRPCBounds AND be
	// assigned exactly once, or a later `x = grpcBounds{}` silently zeroes both
	// caps while the selectors above still read the right field names.
	require.NotEmpty(t, bases)
	for _, base := range bases {
		require.True(t, reaches(base, "resolveGRPCBounds"),
			"netlimit.Config is built from %q, which does not derive from resolveGRPCBounds, so the "+
				"RELAY_GRPC_MAX_CONNS* environment variables are no longer what reaches the listener", base)
		assigned := 0
		ast.Inspect(file, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, l := range as.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == base {
					assigned++
				}
			}
			return true
		})
		require.Equal(t, 1, assigned,
			"%q is assigned %d times in main.go. The admission bounds must be built once and not "+
				"reassigned: a second assignment can zero both caps while every selector above still "+
				"reads the correct field.", base, assigned)
	}

	// 3. grpc.NewServer must be built from grpcServerOptions, or the stream cap,
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

	// 4. The refusal reporter must be started, and NOT from inside a conditional.
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

	// 5. The registration deadline must reach the handler. Deleting the one-line
	//    assignment compiles, leaves every package green, and restores an
	//    unbounded first Recv - the handler would keep falling back to
	//    worker.DefaultRegistrationTimeout, so this guards the ENV VAR rather
	//    than the bound. Same shape as TestTrailingLogWindowIsWiredIntoTheHandler.
	seeds, ok := intoField["RegistrationTimeout"]
	require.True(t, ok,
		"main.go assigns nothing to a .RegistrationTimeout field: RELAY_GRPC_REGISTRATION_TIMEOUT is dead "+
			"and nothing else fails")
	found := false
	for _, seed := range seeds {
		if reaches(seed, "resolveGRPCBounds") {
			found = true
		}
	}
	require.True(t, found,
		"main.go assigns to .RegistrationTimeout but the value does not derive from resolveGRPCBounds")
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

// TestDefaultGRPCMaxConnIdleIsTheAttackersDutyCycle pins a DECISION, in the
// spirit of TestGRPCEnforcementPolicyMatchesGRPCsOwnDefault, and the arithmetic
// is the point rather than the number.
//
// This value is not "how long an idle connection is tolerated". It is the
// attacker's DUTY CYCLE: a peer holding parked connections has to re-establish
// each one once per window, and nothing else. At 15m, holding all 1024 slots
// cost roughly 1024/900 = 1.14 new TCP connections per second, sustained, with
// no credential, no stream and no database work - cheap enough that the option
// meant to close parking was the cheapest parking route on the port. Worse, at
// 15m a peer that completed the handshake parked a slot 7.5x LONGER than one
// that said nothing at all, which grpc-go bounds at its 120s connectionTimeout.
//
// The floor is 1s and a legitimate agent's honest window - dial to first stream
// - is sub-millisecond, so 60s leaves roughly four orders of magnitude of
// headroom and costs a stream-holding connection exactly nothing (pinned by
// TestGRPCServer_ConnectionHoldingAStreamIsNotIdle).
func TestDefaultGRPCMaxConnIdleIsTheAttackersDutyCycle(t *testing.T) {
	assert.LessOrEqual(t, defaultGRPCMaxConnIdle, time.Minute,
		"this default IS the attacker's re-establishment period for a parked connection slot, not a "+
			"tolerance for slow middleboxes. Raising it multiplies how long each parked slot is held for "+
			"one TCP handshake. Do not raise it without redoing the arithmetic in README's row.")
	assert.GreaterOrEqual(t, defaultGRPCMaxConnIdle, minGRPCConnIdleDur,
		"the default must not sit below its own warned floor")
	assert.Less(t, defaultGRPCMaxConnIdle, 120*time.Second,
		"a peer that completes the handshake and opens no stream must not park a slot LONGER than one "+
			"that says nothing at all, which grpc-go bounds at its 120s connectionTimeout - that would "+
			"make the anti-parking control the cheapest parking route on the port")
}

// TestGRPCServerOptionNamesAreUniqueAcrossThePackage generalizes
// TestGRPCServerOptionsHasExactlyOneKeepaliveParams, which was written from the
// ONE instance a mutation run happened to produce and was therefore scoped to
// one function and one option name. That is the recorded "a uniqueness claim is
// a claim about the complement" mistake: the claim was about every way a second
// option could reach grpc.NewServer, and it was checked by opening one function.
// Two one-line escapes survived it, both proved by running them:
//
//   - CALL-SITE ESCAPE. `grpc.NewServer(append(grpcServerOptions(b),
//     grpc.KeepaliveParams(...))...)` in main.go. The old guard parsed
//     grpc_config.go only, and TestGRPCAdmissionIsWiredByMain needs NewServer
//     merely to MENTION grpcServerOptions, which append still does. The 30s/10s
//     liveness probe silently became grpc-go's 2h default.
//   - SIBLING-OPTION ESCAPE. A second grpc.KeepaliveEnforcementPolicy. grpc-go
//     stores that one wholesale too (server.go:336-340), and it is LITERALLY the
//     regression grpcKeepaliveMinTime's own comment names - somebody adding a
//     policy with MinTime: 10*time.Second "because that is what the internet
//     suggests".
//
// So the rule is stated on the shape rather than on the instance: every
// grpc.<Option> constructor appears AT MOST ONCE across the package's
// non-test files. MaxConcurrentStreams and KeepaliveEnforcementPolicy are then
// covered by construction, as is any option added later.
//
// NewServer is excluded because it is the constructor, not an option.
func TestGRPCServerOptionNamesAreUniqueAcrossThePackage(t *testing.T) {
	notAnOption := map[string]bool{"NewServer": true}

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	counts := map[string]int{}
	where := map[string][]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue // tests build their own servers on purpose
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "grpc" || notAnOption[sel.Sel.Name] {
				return true
			}
			counts[sel.Sel.Name]++
			where[sel.Sel.Name] = append(where[sel.Sel.Name], name)
			return true
		})
	}

	require.NotEmpty(t, counts,
		"no grpc.<Option> calls were found at all, so this guard is watching nothing - the package layout "+
			"or the import alias must have changed")
	for name, n := range counts {
		assert.Equal(t, 1, n,
			"grpc.%s is applied %d times, in %v. grpc-go stores each of these options WHOLESALE, so the "+
				"last one silently discards every field the earlier one set - there is no merge and no "+
				"error. Put every decision for an option inside the single call that builds it.",
			name, n, where[name])
	}

	// The guard must reject what it exists to catch, or it is a green test that
	// checks nothing: the failure above is driven by counts, so a synthetic
	// double-count must be the failing case.
	assert.Equal(t, 1, counts["KeepaliveParams"],
		"grpc.KeepaliveParams specifically: this is the option whose duplication drops Time and Timeout")
	assert.Equal(t, 1, counts["MaxConcurrentStreams"])
	assert.Equal(t, 1, counts["KeepaliveEnforcementPolicy"])
}

// TestParseRegistrationTimeout mirrors TestParseGRPCConnIdle, WITH ONE
// DELIBERATE DIVERGENCE that is the whole reason it is a separate function: zero
// does not disable, it is rejected like any other non-positive value.
//
// Every other knob in this file can be switched off, because an operator may
// have bounded the same thing somewhere else - a proxy can cap connections. No
// proxy can enforce "send a RegisterRequest within N seconds", because that is
// an application-layer property of relay's own protocol. A disable here would
// have no substitute and would restore a free, permanent, fleet-wide denial: a
// peer parks a connection slot by opening a stream and saying nothing, which
// costs it no credential and no database round trip and which MaxConnectionIdle
// structurally cannot reap.
func TestParseRegistrationTimeout(t *testing.T) {
	def := 30 * time.Second
	cases := []struct {
		name    string
		raw     string
		want    time.Duration
		wantMsg string
	}{
		{"unset keeps the default and is silent", "", def, ""},
		{"a valid value is used as-is", "10s", 10 * time.Second, ""},
		{"the escape hatch for a very slow fleet is honoured", "1h", time.Hour, ""},
		{"zero does NOT disable: it keeps the default and warns", "0s", def, "cannot be disabled"},
		{"negative keeps the default and warns", "-5s", def, "not a positive Go duration"},
		{"unparseable keeps the default and warns", "thirty", def, "not a positive Go duration"},
		{"below the floor KEEPS the value and warns", "200ms", 200 * time.Millisecond, "below"},
		{"exactly the floor is silent", "1s", time.Second, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseRegistrationTimeout("RELAY_GRPC_REGISTRATION_TIMEOUT", tc.raw, def)
			assert.Equal(t, tc.want, got)
			if tc.wantMsg == "" {
				assert.Empty(t, msg, "a valid value must not produce startup noise")
				return
			}
			require.Contains(t, msg, tc.wantMsg)
			assert.Contains(t, msg, "RELAY_GRPC_REGISTRATION_TIMEOUT",
				"the message must name the variable it is about")
		})
	}
}

// TestResolveGRPCBounds covers the seam that used to be inline in main() and so
// had no test at all: which env var reaches which field, and that every warning
// is RETURNED rather than swallowed. main() only logs what it is handed, so a
// knob wired to the wrong field or a warning dropped on the floor was invisible.
func TestResolveGRPCBounds(t *testing.T) {
	t.Run("an empty environment yields exactly the documented defaults, silently", func(t *testing.T) {
		b, msgs := resolveGRPCBounds(func(string) string { return "" })
		assert.Empty(t, msgs, "the ordinary boot must produce no warnings at all")
		assert.Equal(t, defaultGRPCMaxConns, b.maxConns)
		assert.Equal(t, defaultGRPCMaxConnsPerIP, b.maxConnsPerIP)
		assert.Equal(t, defaultGRPCMaxConnIdle, b.maxConnIdle)
		assert.Equal(t, defaultGRPCRegistrationTimeout, b.registrationTimeout)
	})

	t.Run("each variable reaches its own field", func(t *testing.T) {
		env := map[string]string{
			"RELAY_GRPC_MAX_CONNS":            "500",
			"RELAY_GRPC_MAX_CONNS_PER_IP":     "7",
			"RELAY_GRPC_MAX_CONN_IDLE":        "90s",
			"RELAY_GRPC_REGISTRATION_TIMEOUT": "12s",
		}
		b, msgs := resolveGRPCBounds(func(k string) string { return env[k] })
		assert.Empty(t, msgs)
		// The two connection caps are the pair that can be crossed silently, and
		// distinct values are what makes a crossing visible.
		assert.Equal(t, 500, b.maxConns)
		assert.Equal(t, 7, b.maxConnsPerIP)
		assert.Equal(t, 90*time.Second, b.maxConnIdle)
		assert.Equal(t, 12*time.Second, b.registrationTimeout)
	})

	t.Run("every knob can speak, and all four messages come back", func(t *testing.T) {
		env := map[string]string{
			"RELAY_GRPC_MAX_CONNS":            "0",
			"RELAY_GRPC_MAX_CONNS_PER_IP":     "nonsense",
			"RELAY_GRPC_MAX_CONN_IDLE":        "0s",
			"RELAY_GRPC_REGISTRATION_TIMEOUT": "0s",
		}
		b, msgs := resolveGRPCBounds(func(k string) string { return env[k] })
		require.Len(t, msgs, 4,
			"a warning dropped here is a security-relevant knob silently doing something other than what "+
				"the operator asked for, and main() cannot notice because it only prints what it is given")
		assert.Contains(t, msgs[0], "RELAY_GRPC_MAX_CONNS=")
		assert.Contains(t, msgs[1], "RELAY_GRPC_MAX_CONNS_PER_IP=")
		assert.Contains(t, msgs[2], "RELAY_GRPC_MAX_CONN_IDLE=")
		assert.Contains(t, msgs[3], "RELAY_GRPC_REGISTRATION_TIMEOUT=")

		assert.Zero(t, b.maxConns, "0 disables the total cap")
		assert.Equal(t, defaultGRPCMaxConnsPerIP, b.maxConnsPerIP, "an unparseable value falls back")
		assert.Zero(t, b.maxConnIdle, "0 disables idle reaping")
		assert.Equal(t, defaultGRPCRegistrationTimeout, b.registrationTimeout,
			"0 must NOT disable the registration deadline - it is the one bound with no substitute")
	})
}
