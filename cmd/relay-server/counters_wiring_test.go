package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/netlimit"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// stubAdminDB is a store.DBTX for exactly one query, GetTokenWithUser, so that
// BearerAuth resolves a token to an ADMIN with no Postgres. That is what lets
// the wiring below be proved by driving a real request through the real
// admin-gated route rather than by reading main.go and hoping.
//
// Every other statement panics rather than returning a plausible zero value: a
// handler that grew a second query must fail here loudly, not pass by accident.
type stubAdminDB struct{}

func (stubAdminDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("stubAdminDB: Exec is not part of the bearer-auth path")
}

func (stubAdminDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("stubAdminDB: Query is not part of the bearer-auth path")
}

func (stubAdminDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row { return stubAdminRow{} }

type stubAdminRow struct{}

// countersStubUUID is a fixed VALID uuid per destination position. A limiter or
// an authz check keyed on uuidStr(AuthUser.ID) buckets every request under ""
// when these are invalid, which is the state this exists to end.
func countersStubUUID(n byte) pgtype.UUID {
	var raw [16]byte
	raw[0] = 0xc0
	raw[15] = n
	return pgtype.UUID{Bytes: raw, Valid: true}
}

// Scan fills GetTokenWithUserRow BY DESTINATION TYPE rather than by column
// index, so a reordered or added column cannot silently authenticate a
// zero-valued (non-admin) user and turn every assertion below into a 403.
//
// The uuid arm fills BY POSITION, so token_id gets 1, token_user_id 2 and
// user_id 3. The arity check catches a field added or removed; it cannot catch
// a reorder, and nothing here depends on which of the three a given field got -
// only that user_id is valid and differs from token_id.
func (stubAdminRow) Scan(dest ...any) error {
	bools := 0
	uuids := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = true
			bools++
		case *string:
			*v = "counters-wiring"
		case *pgtype.UUID:
			uuids++
			*v = countersStubUUID(byte(uuids))
		}
	}
	if bools != 1 {
		return fmt.Errorf("stubAdminDB: GetTokenWithUserRow has %d bool destinations, want exactly 1 "+
			"(user_is_admin); the row shape changed and this stub no longer authenticates an admin", bools)
	}
	// The wanted count is spelled once and rendered from the same constant, so a
	// drift in it reports "has 3, want 2" rather than the self-contradicting
	// "has 3, want exactly 3" a second literal in the message produces.
	const wantUUIDs = 3
	if uuids != wantUUIDs {
		return fmt.Errorf("stubAdminDB: GetTokenWithUserRow has %d pgtype.UUID destinations, want "+
			"exactly %d (token_id, token_user_id, user_id); the row shape changed and the ids this "+
			"stub fills no longer line up with the fields BearerAuth reads", uuids, wantUUIDs)
	}
	return nil
}

func countersAsAdmin(t *testing.T, srv *http.Server) map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/server/counters", nil)
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	return top
}

// TestBuildHTTPServer_ServesTheRealListenersCounters is the wiring guard, and it
// is an EXECUTED one.
//
// The property - "the server main serves reports the counters of the listener
// main serves gRPC on" - is a runtime property, and the previous guard checked
// it syntactically by parsing main.go. That guard was evaded four separate ways,
// every one of them green across the whole repo:
//
//   - a pointer alias on the next line: `cs := &httpServer.Counters;
//     cs.GRPCAdmission = nil` (the LHS path does not contain ".Counters.");
//   - a helper call in a sibling file of the same package:
//     `disableCounters(httpServer)` (a CallExpr is not an AssignStmt, and the
//     guard parsed main.go alone);
//   - a conditionally-built listener reaching the field as a TYPED NIL, which
//     is not == nil in an interface, so `src != nil` was true and Stats()
//     panicked on a nil receiver - a goroutine stack trace to the log per admin
//     request, inside the feature whose subject is bounding log volume;
//   - moving the assignment BELOW the line that starts serving, which main.go
//     named in prose as a constraint and nothing checked.
//
// Patching four more syntactic sub-checks is what produced that pattern. Instead
// buildHTTPServer now returns the *http.Server and main never holds the
// api.Server, so none of the four can be written at all - and this test calls
// buildHTTPServer with a REAL netlimit.Wrap'd socket, opens real connections
// through it, and reads the numbers back out through the real admin-gated route.
// A stub source, a second server, a wrong listener, an empty literal or a
// reordering all produce different numbers here or no section at all.
func TestBuildHTTPServer_ServesTheRealListenersCounters(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer raw.Close()
	lis := netlimit.Wrap(raw, netlimit.Config{MaxTotal: 8, MaxPerIP: 8})

	srv := buildHTTPServer(httpServerDeps{
		addr:          "127.0.0.1:0",
		q:             store.New(stubAdminDB{}),
		grpcAdmission: lis,
	})

	// Nothing has connected yet: a wired section is PRESENT and zero, which the
	// payload's contract reads as "this control ran and stopped nothing".
	before := countersAsAdmin(t, srv)
	require.Contains(t, before, "grpc_admission",
		"the section must be present the moment the server is built, not only once something happens")

	// Two real connections, admitted through the wrapper. Accept is what runs
	// the accounting, so both must be accepted, and both are held open.
	var held []net.Conn
	for i := 0; i < 2; i++ {
		dialed, err := net.Dial("tcp", raw.Addr().String())
		require.NoError(t, err)
		defer dialed.Close()
		accepted, err := lis.Accept()
		require.NoError(t, err)
		defer accepted.Close()
		held = append(held, accepted)
	}
	require.Len(t, held, 2)

	after := countersAsAdmin(t, srv)
	var section struct {
		Levels struct {
			LiveTotal       uint64 `json:"live_total"`
			DistinctSources uint64 `json:"distinct_sources"`
			MaxPerSource    uint64 `json:"max_per_source"`
		} `json:"levels"`
	}
	require.NoError(t, json.Unmarshal(after["grpc_admission"], &section))
	require.Equal(t, uint64(2), section.Levels.LiveTotal,
		"the served endpoint must report THIS listener's occupancy. A stub, a second listener or an "+
			"unwired section cannot produce this number.")
	require.Equal(t, uint64(1), section.Levels.DistinctSources, "both peers dialed from 127.0.0.1")
	require.Equal(t, uint64(2), section.Levels.MaxPerSource)
}

// TestBuildHTTPServer_TypedNilListenerLeavesTheSectionAbsent.
//
// `var grpcLis *netlimit.Listener` conditionally assigned - the natural shape
// for "only wrap when a cap is configured", and the shape slice 4's optional
// watchdog will reach for - stores a TYPED nil in api.GRPCAdmissionSource. That
// is not == nil, so the handler's `src != nil` is true and Stats() panics on a
// nil receiver.
//
// The fix is at the wiring boundary, where the concrete type still makes the
// distinction visible, and NOT a nil-tolerant Stats() returning an empty struct:
// that would turn an unwired control into a section of zeros, and "not wired"
// versus "ran and stopped nothing" is the one distinction this whole payload
// exists to preserve.
func TestBuildHTTPServer_TypedNilListenerLeavesTheSectionAbsent(t *testing.T) {
	var unwired *netlimit.Listener
	srv := buildHTTPServer(httpServerDeps{
		addr:          "127.0.0.1:0",
		q:             store.New(stubAdminDB{}),
		grpcAdmission: unwired,
	})

	top := countersAsAdmin(t, srv)
	require.NotContains(t, top, "grpc_admission",
		"a nil listener must leave the section ABSENT, never present-and-zero: zeros mean the control "+
			"ran and stopped nothing")
	require.Contains(t, top, "started_at",
		"started_at is present even when every section is absent")
}

// TestBuildHTTPServer_EverySourceFieldProducesAServedSection is the completeness
// relation, and it is EXECUTED. It builds a server with every deps source wired
// and counts the sections that come back out through the real route.
//
// IT REPLACES A RELATION THAT WAS PURE BOOKKEEPING. The previous version of that
// claim lived in TestServerCountersIsWiredByMain as a `sections []string` column
// on each wiredDep row, counted against NumField(api.CounterSources). Nothing
// ever read those strings against the code: the AST walk consults only `field`
// and `mustReach`, and never looks at buildHTTPServer's body at all. Two measured
// consequences, both green module-wide:
//
//   - swapping which row claimed which section (grpcAdmission claiming
//     TaskLogFence, agentHandler not) left the package ok;
//   - adding a FOURTH CounterSources field with its response field and its
//     handleServerCounters branch, and satisfying the count by appending one
//     string to the existing agentHandler row - no new deps field, no call-site
//     argument, NO `s.Counters.X = ...` ANYWHERE - left `go test ./...` green.
//
// The second is the live failure: a new section costs one token in a string
// literal and inherits none of the checks a new deps field used to inherit. The
// old message ("EVERY SECTION needs to be named by exactly one row") made it
// worse, because following it literally IS the evasion.
//
// This test cannot be satisfied that way. Passing the source in the fixture
// without assigning it in buildHTTPServer still leaves the section absent, and
// assigning it without a handleServerCounters branch does too, so both halves of
// "wired" have to be real before the count comes out right. It also catches the
// reverse mistake - a source field whose section is rendered but never assigned -
// which is the shape that ships a permanently absent section reading as "not
// wired on this build".
//
// WHAT IT DOES NOT ANSWER is anything about main.go's identifiers, which is what
// TestServerCountersIsWiredByMain below is for.
func TestBuildHTTPServer_EverySourceFieldProducesAServedSection(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer raw.Close()

	srv := buildHTTPServer(httpServerDeps{
		addr:          "127.0.0.1:0",
		q:             store.New(stubAdminDB{}),
		grpcAdmission: netlimit.Wrap(raw, netlimit.Config{MaxTotal: 8, MaxPerIP: 8}),
		agentHandler:  worker.NewHandler(nil, nil, worker.NewRegistry(), events.NewBroker(), func() {}),
		watchdog:      scheduler.NewWatchdog(nil, nil, nil, 0, 0),
	})

	// keysOfRaw rather than the map itself, so the failure prints section NAMES
	// instead of every section's body as a byte slice.
	served := keysOfRaw(countersAsAdmin(t, srv))
	fields := reflect.TypeOf(api.CounterSources{}).NumField()
	require.Len(t, served, 1+fields, // +1 for started_at
		"api.CounterSources has %d source fields, so a build with every source wired must serve %d "+
			"top-level keys (started_at plus one section each). This one served %d: %v. At least one "+
			"source field is not carried end to end - buildHTTPServer does not assign it, or "+
			"handleServerCounters does not render it, or this fixture does not pass it. Fixing the "+
			"fixture alone will NOT make this pass, which is the point: the count comes from a real "+
			"response.",
		fields, 1+fields, len(served), served)
}

// TestServerCountersIsWiredByMain is what remains of the syntactic guard, and it
// is deliberately THIN: four questions execution cannot answer from inside
// buildHTTPServer.
//
// IT IS A TABLE, ONE ROW PER WIRED SOURCE, and the generalization from one row
// to two is the point rather than a tidy-up. Every deps field that feeds a
// section adds a row here and inherits the whole walk: the plain-identifier
// check, the derives-from-an-unconditional-assignment check, and the
// assignments-per-identifier count over main's entire subtree. A deps field that
// feeds a section with NO row is guarded by nothing, which is why the rows are
// checked against buildHTTPServer's OWN ASSIGNMENTS below rather than against a
// hand-maintained list of section names - that list was the finding this
// rewrite is answering, and the completeness half now lives in
// TestBuildHTTPServer_EverySourceFieldProducesAServedSection above. All six
// evasions that beat this guard's predecessors were re-run against the table
// form and all six still fail.
//
// THE FOURTH QUESTION IS NEW WITH agentHandler: the counters must come from the
// Handler main REGISTERS ON. Feeding buildHTTPServer a second, otherwise
// identical worker.Handler compiles and passes every other check, and the
// endpoint then reports a permanently empty log budget while the real one fills
// up.
//
// WHAT IS STILL ONLY SYNTACTIC HERE is the main.go half of that: whether the
// identifier main passes is the identifier main registered. Nothing in the
// DEFAULT lane can answer it, because moving an ingest counter needs Connect's
// message loop and therefore a Postgres round trip. An earlier version of this
// comment stopped there and said "nothing executable can answer that from
// cmd/relay-server", which quietly covered a second question it does not ask:
// whether buildHTTPServer forwards the handler it was handed. That one IS
// executable and was unguarded - substituting a fresh worker.NewHandler inside
// buildHTTPServer left every package green - and is now guarded twice. The crude
// form dies in the DEFAULT lane, on countersAssignmentSources below: a
// s.Counters assignment fed by anything other than `d.<field>` is RED there
// (measured against exactly that substitution). The form that still reads as a
// deps field, and the question of whether the numbers move at all, is
// TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers in the
// integration lane. The numbers' own proof is
// TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections in
// internal/worker. Three guards, three questions; this identifier check is the
// join between them.
//
// The old version asked eight, six of them re-derived from individual evasions,
// and four evasions still got past it. Returning *http.Server took some of those
// shapes out of the LANGUAGE and left others merely CHECKED, and the difference
// has to be stated: a reader who believes a shape is unwritable has no reason to
// preserve the check that is in fact the only thing stopping it.
//
//   - IMPOSSIBLE TO WRITE, because main never holds the *api.Server: a pointer
//     alias on the field (`cs := &httpServer.Counters; cs.GRPCAdmission = nil`),
//     a helper in a sibling file mutating it, an explicit
//     `Counters: api.CounterSources{}`, a `Counters` assignment on any following
//     line, and moving that assignment below the line that starts serving. There
//     is no *api.Server value in main for any of them to reach.
//   - STILL WRITABLE, AND CHECKED BELOW: everything about the LISTENER and the
//     server binding, both of which are ordinary mutable locals. `grpcLis = nil`
//     inside an if - the same defect one variable to the left of the one the
//     return type removed - compiled and left every package green until the
//     assignment count below was added. Also checked here: a second
//     buildHTTPServer call, serving some other server, and a grpcAdmission
//     argument that does not derive from netlimit.Wrap.
//
// The one real defect carried over from the old guard is fixed here: its
// `reaches` walk asked only "was this identifier EVER assigned something
// mentioning Wrap", and applied its `unconditional` filter to the assignment
// TARGET while never applying it to the SEED. So a conditionally-wrapped
// listener passed. The walk below follows only assignments that are direct
// statements of main's own body.
func TestServerCountersIsWiredByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var body *ast.BlockStmt
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "main" || fd.Recv != nil || fd.Body == nil {
			continue
		}
		body = fd.Body
	}
	require.NotNil(t, body, "main.go no longer declares func main with a body")

	// from[name] = identifiers its RHS mentions, populated ONLY from assignments
	// that are direct, unconditional statements of main's body. An assignment
	// inside an if, a loop, a switch or a closure contributes nothing, so a
	// conditionally-built listener does not reach anything.
	from := map[string][]string{}
	var built []string // names assigned from buildHTTPServer(...)
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for i, l := range as.Lhs {
			id, ok := l.(*ast.Ident)
			if !ok || len(as.Lhs) != len(as.Rhs) {
				continue
			}
			ast.Inspect(as.Rhs[i], func(m ast.Node) bool {
				if x, ok := m.(*ast.Ident); ok {
					from[id.Name] = append(from[id.Name], x.Name)
					if x.Name == "buildHTTPServer" {
						built = append(built, id.Name)
					}
				}
				return true
			})
		}
	}
	require.Len(t, built, 1,
		"main's own body must call buildHTTPServer exactly once and bind the result. Nested inside an if, "+
			"a loop or a closure it may never run; called twice, the last one decides and this guard "+
			"cannot say which. Found: %v", built)
	srvName := built[0]

	// The server main SERVES must be the one buildHTTPServer returned.
	served := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ListenAndServe" {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok {
			served[x.Name] = true
		}
		return true
	})
	require.True(t, served[srvName],
		"main builds %q with buildHTTPServer but serves %v. A second http.Server would answer every "+
			"request while the wired one sat idle.", srvName, keysOf(served))

	// EVERY WIRED SOURCE, not just the first one. The walk below is run once per
	// row: a section whose source is fed a conditionally-assigned local reaches
	// the endpoint as a typed nil and vanishes on every deployment that takes the
	// branch, which reads exactly like a control that has never stopped anything.
	deps := []wiredDep{
		{"grpcAdmission", "Wrap", "the netlimit listener bound in main's body"},
		{"agentHandler", "NewHandlerWithGrace", "the worker.Handler bound in main's body"},
		{"watchdog", "NewWatchdog", "the scheduler.Watchdog bound in main's body"},
	}

	depsType := reflect.TypeOf(httpServerDeps{})
	distinct := map[string]bool{}
	for _, d := range deps {
		_, ok := depsType.FieldByName(d.field)
		require.True(t, ok,
			"this table has a row for httpServerDeps.%s, which does not exist. A row naming no field "+
				"guards nothing and makes the counts below pass on a table that is short one field.",
			d.field)
		distinct[d.field] = true
	}
	require.Len(t, distinct, len(deps),
		"this table has %d rows naming %d DISTINCT httpServerDeps fields. Do not resolve a cardinality "+
			"failure by repeating a row: that was proved to drop the displaced field out of every check "+
			"below while every count still passed.", len(deps), len(distinct))

	// EVERY DEPS FIELD buildHTTPServer FEEDS A SECTION FROM MUST HAVE A ROW, read
	// off buildHTTPServer's OWN ASSIGNMENTS rather than off a list of section
	// names maintained by hand.
	//
	// THAT LIST WAS THE DEFECT. Each row used to carry a `sections []string`
	// column counted against NumField(api.CounterSources), and nothing ever read
	// those strings against any code - the walk here consults only `field` and
	// `mustReach`. Measured: swapping which row claimed which section left the
	// package ok, and a fourth CounterSources field wired end-to-end EXCEPT for
	// the assignment went green module-wide once one string was appended to the
	// agentHandler row. The section-completeness half is now executed, in
	// TestBuildHTTPServer_EverySourceFieldProducesAServedSection; what is left
	// here is the direction only this test can answer: a deps field that reaches
	// s.Counters must be one whose identifier in main.go is checked below.
	//
	// IT FAILS CLOSED because it demands the assignment be spelled `d.<field>`
	// exactly. Reaching a section through a local, a helper or a conversion is RED
	// here rather than invisible, which is the failure mode the string list had.
	for depField := range countersAssignmentSources(t) {
		_, wired := lookupWiredDep(deps, depField)
		require.True(t, wired,
			"buildHTTPServer feeds an api.CounterSources field from httpServerDeps.%s, which has no row "+
				"in this table. That section's source therefore gets NONE of main.go's identifier checks "+
				"below: it may be bound conditionally, rebound later, or passed as an expression, and "+
				"this guard would stay green while the section vanished on some deployments. Add the "+
				"row.", depField)
	}

	depArg := map[string]*ast.Ident{}
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for _, r := range as.Rhs {
			ce, ok := r.(*ast.CallExpr)
			if !ok {
				continue
			}
			if fn, ok := ce.Fun.(*ast.Ident); !ok || fn.Name != "buildHTTPServer" {
				continue
			}
			require.Len(t, ce.Args, 1)
			cl, ok := ce.Args[0].(*ast.CompositeLit)
			require.True(t, ok, "buildHTTPServer must be called with an httpServerDeps composite literal "+
				"at the call site, so that every dependency is readable there")
			for _, e := range cl.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				k, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				id, isIdent := kv.Value.(*ast.Ident)
				if d, wired := lookupWiredDep(deps, k.Name); wired {
					require.True(t, isIdent,
						"httpServerDeps.%s must be fed a plain identifier - %s - not %T. A helper call, "+
							"a conversion or a composite literal there hides whether the value main "+
							"actually built is the one this endpoint reports on.", k.Name, d.what, kv.Value)
				}
				if isIdent {
					depArg[k.Name] = id
				}
			}
		}
	}

	chainNames := map[string]bool{srvName: true}
	for _, d := range deps {
		argIdent := depArg[d.field]
		require.NotNil(t, argIdent,
			"buildHTTPServer is called with no %s field, so that section is absent and the endpoint "+
				"reports nothing about a control that IS running - which reads exactly like a control "+
				"that has never stopped anything", d.field)

		seen := map[string]bool{}
		queue := []string{argIdent.Name}
		reached := false
		for len(queue) > 0 && !reached {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			if name == d.mustReach {
				reached = true
				break
			}
			queue = append(queue, from[name]...)
		}
		require.True(t, reached,
			"httpServerDeps.%s is fed %q, which does not derive from %s through an UNCONDITIONAL "+
				"assignment in main's body. A local assigned inside an if - the natural shape for "+
				"'only build it when configured' - reaches the endpoint as a typed nil and the section "+
				"vanishes on every deployment that does not take the branch.",
			d.field, argIdent.Name, d.mustReach)
		for n := range seen {
			chainNames[n] = true
		}
	}

	// THE COUNTERS MUST COME FROM THE HANDLER THAT SERVES gRPC. Feeding
	// buildHTTPServer a second worker.Handler compiles, passes every check above
	// and leaves the endpoint reporting a permanently empty log budget while the
	// real one fills up - which is worse than no endpoint, because it is a
	// confident zero.
	var registered []string
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RegisterAgentServiceServer" || len(ce.Args) != 2 {
			return true
		}
		if id, ok := ce.Args[1].(*ast.Ident); ok {
			registered = append(registered, id.Name)
		}
		return true
	})
	require.Len(t, registered, 1,
		"main must register exactly one AgentService implementation; found %v", registered)
	require.Equal(t, registered[0], depArg["agentHandler"].Name,
		"main serves gRPC on %q but reports ingest log counters from %q. They must be the same "+
			"Handler.", registered[0], depArg["agentHandler"].Name)

	// EVERY NAME ON THOSE CHAINS, PLUS THE SERVER BINDING, MUST BE ASSIGNED
	// EXACTLY ONCE IN THE WHOLE OF MAIN.
	//
	// `from` above is built from main's DIRECT statements only, so a name
	// assigned solely inside an if never reaches its seed and the check above
	// fails. The shape that survives it is a name assigned BOTH ways:
	//
	//	grpcLis := netlimit.Wrap(grpcRawLis, ...)
	//	if grpcBnds.maxConns == 0 && grpcBnds.maxConnsPerIP == 0 {
	//		grpcLis = nil
	//	}
	//
	// The top-level seed still reaches Wrap, so every check above passes, while
	// each deployment taking that branch feeds buildHTTPServer a typed nil and
	// serves an endpoint with no grpc_admission section at all - an admission
	// control that reads as having never refused anything, which is the exact
	// defect this whole guard exists for. It is also the shape a maintainer is
	// most likely to reach for, since "only wrap when a cap is configured" is a
	// reasonable-sounding optimisation. The same shape one variable to the right
	// - `agentHandler = nil` inside an if - silences the ingest log budget.
	//
	// Counting assignments across main's ENTIRE subtree - ifs, loops, switches
	// and closures included - is what separates a single unconditional binding
	// from a binding that is later taken back. srvName is checked the same way:
	// the ListenAndServe check above matches on NAME, so a conditional
	// `srv = &http.Server{...}` would serve an unwired server through an
	// identifier this test already blessed.
	//
	// Field assignments (`agentHandler.Metrics = ...`) have a SelectorExpr on the
	// left rather than an Ident, so they are deliberately not counted: they
	// mutate the object, they do not rebind the name.
	assignedAnywhere := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				assignedAnywhere[id.Name]++
			}
		}
		return true
	})
	for name := range chainNames {
		if len(from[name]) == 0 && assignedAnywhere[name] == 0 {
			// Not a local bound by an assignment in main's body: a package
			// name, a function name, a field name. Nothing to count.
			continue
		}
		require.Equal(t, 1, assignedAnywhere[name],
			"%q is assigned %d times inside main. Exactly one unconditional assignment is the whole "+
				"basis on which this test concludes anything: a second one, in an if or a loop or a "+
				"closure, can take the wiring back on some deployments while every check above still "+
				"passes. If the second assignment is legitimate, this guard can no longer answer the "+
				"question and needs replacing, not relaxing.", name, assignedAnywhere[name])
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestBuildHTTPServer_ServesTheWiredHandlersIngestSection is EXECUTED: it builds
// the server the way main does and reads the section back through the real
// admin-gated route.
//
// SAY WHAT IT DOES NOT BUY, and the first sentence here used to overstate it. It
// proves the section is PRESENT, with the right shape, whenever a non-nil
// Handler is passed. It does NOT prove the section is served from THAT Handler:
// a fresh worker.NewHandler substituted inside buildHTTPServer produces an
// identical five-key-per-arm section of zeros and leaves this test green
// (measured). Nor does it prove the numbers move, which needs the gRPC recv
// goroutine and a registered agent.
//
// Those two live in integration lanes:
// TestGRPCAdmissionEndToEnd_TheServedIngestCountersAreTheServingHandlers drives
// real drops through a real stream and reads them back through this route, and
// TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections in
// internal/worker proves the counts outlive the connection. The main.go half -
// "the Handler serving gRPC is the Handler reporting counts" - is the identifier
// property checked in TestServerCountersIsWiredByMain below.
func TestBuildHTTPServer_ServesTheWiredHandlersIngestSection(t *testing.T) {
	h := worker.NewHandler(nil, nil, worker.NewRegistry(), events.NewBroker(), func() {})
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: h,
	})

	top := countersAsAdmin(t, srv)
	require.Contains(t, top, "ingest_log_budget",
		"a wired Handler must produce the section from the moment the server is built. An absent "+
			"section reads as 'this build has no ingest log budget', which is false and is exactly "+
			"the distinction this payload exists to preserve.")

	var section struct {
		Counts struct {
			Deduped    map[string]uint64 `json:"deduped"`
			Suppressed map[string]uint64 `json:"suppressed"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(top["ingest_log_budget"], &section))
	// DERIVED, never a literal: a hardcoded count here is a census of another
	// package that goes stale the next time a kind is added.
	kinds := reflect.TypeOf(worker.IngestLogDropsByKind{}).NumField()
	require.Len(t, section.Counts.Deduped, kinds, "one key per kind")
	require.Len(t, section.Counts.Suppressed, kinds, "one key per kind")
}

// TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection is EXECUTED, and
// it says what it does not buy.
//
// It proves the section is PRESENT with the right shape whenever a non-nil
// Handler is passed, which is what kills a dropped assignment inside
// buildHTTPServer. It does NOT prove the section is served from THAT Handler: a
// fresh worker.NewHandler substituted there produces an identical zero section
// and leaves this green. That question is executable only past Connect's message
// loop, so it lives in
// TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers,
// which needs Postgres and lives in this package's own integration lane.
func TestBuildHTTPServer_ServesTheWiredHandlersTaskLogFenceSection(t *testing.T) {
	h := worker.NewHandler(nil, nil, worker.NewRegistry(), events.NewBroker(), func() {})
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: h,
	})

	top := countersAsAdmin(t, srv)
	require.Contains(t, top, "task_log_fence",
		"a wired Handler must produce the section from the moment the server is built. An absent "+
			"section reads as 'this build has no task-log fence', which is false.")

	var section struct {
		Counts struct {
			RejectedTotal uint64 `json:"rejected_total"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(top["task_log_fence"], &section))
	require.Zero(t, section.Counts.RejectedTotal, "nothing has been rejected on a fresh handler")
}

// TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent. `var h
// *worker.Handler` conditionally assigned stores a TYPED nil in the interface,
// which is not == nil, so the handler's `src != nil` is true and the method call
// dereferences a nil receiver - a goroutine stack trace per admin request,
// inside the feature whose subject is bounding log volume.
//
// The fix belongs at the wiring boundary, where the concrete type still makes
// the distinction visible, and NOT in a nil-tolerant snapshot method: that would
// turn an unwired control into a section of zeros.
func TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent(t *testing.T) {
	var unwired *worker.Handler
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: unwired,
	})

	top := countersAsAdmin(t, srv)
	require.NotContains(t, top, "ingest_log_budget",
		"a nil handler must leave the section ABSENT, never present-and-zero, and must never panic")
	require.NotContains(t, top, "task_log_fence",
		"the same nil filter covers BOTH sections fed by this handler. One deps field, one `if`, two "+
			"sections: a separate typed-nil test would copy this fixture to assert the same branch.")
	require.Contains(t, top, "started_at")
}

// wiredDep names one httpServerDeps field whose source must be a plain,
// unconditionally-bound local in main's body, and the constructor that local has
// to derive from.
//
// ONE ROW PER DEPS FIELD. It used to be one row per SECTION, on the assumption
// that the two were the same thing; they are not, since agentHandler feeds BOTH
// IngestLogBudget and TaskLogFence. It then carried a `sections []string` column
// to bridge the difference, which was measured to be bookkeeping no check ever
// read - see countersAssignmentSources, which replaced it with the same relation
// taken off buildHTTPServer's real assignments.
type wiredDep struct {
	field     string
	mustReach string
	what      string
}

// countersAssignmentSources returns the httpServerDeps field name behind every
// `s.Counters.<Section> = ...` assignment in buildHTTPServer, as a set.
//
// It parses http_server.go rather than main.go, because that is where the
// deps-field-to-section edge actually lives. It is deliberately STRICT about the
// right-hand side: anything other than a plain `<ident>.<field>` selector fails
// here rather than being skipped, so the set it returns is complete by
// construction and a caller may treat "not in this set" as "does not feed a
// section".
func countersAssignmentSources(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "http_server.go", nil, 0)
	require.NoError(t, err)

	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "buildHTTPServer" && fd.Body != nil {
			body = fd.Body
		}
	}
	require.NotNil(t, body, "http_server.go no longer declares func buildHTTPServer with a body")

	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, l := range as.Lhs {
			sel, ok := l.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			inner, ok := sel.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "Counters" {
				continue
			}
			rhs, ok := as.Rhs[i].(*ast.SelectorExpr)
			require.True(t, ok,
				"buildHTTPServer assigns Counters.%s from a %T. It must be assigned from a plain "+
					"httpServerDeps field - `d.<field>` - so that the section's source is traceable to "+
					"a row in the wiredDep table and thence to an identifier in main. An expression "+
					"here hides which dependency feeds the section.", sel.Sel.Name, as.Rhs[i])
			out[rhs.Sel.Name] = true
		}
		return true
	})
	require.NotEmpty(t, out,
		"buildHTTPServer assigns no api.CounterSources field at all, so every section is absent and the "+
			"endpoint reports nothing about any control that IS running")
	return out
}

// keysOfRaw names the top-level keys of a counters payload, for a failure message
// that says WHICH section is missing rather than only how many there are.
func keysOfRaw(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func lookupWiredDep(deps []wiredDep, name string) (wiredDep, bool) {
	for _, d := range deps {
		if d.field == name {
			return d, true
		}
	}
	return wiredDep{}, false
}

// sweepableStore is a Postgres-free watchdogStore. internal/scheduler's
// watchdogStore is an UNEXPORTED INTERFACE WHOSE METHODS ARE ALL EXPORTED, so
// this package can implement it - which is what puts this proof in the DEFAULT
// lane rather than behind //go:build integration. A sweep needs a store, not a
// gRPC recv goroutine and not a container, which is something neither slice 2's
// nor slice 3's forwarding proof could say.
type sweepableStore struct{ overdue []store.Task }

func (s *sweepableStore) ListOverdueAssignedTasks(context.Context, store.ListOverdueAssignedTasksParams) ([]store.Task, error) {
	return s.overdue, nil
}

func (s *sweepableStore) UpdateTaskStatus(_ context.Context, p store.UpdateTaskStatusParams) (store.Task, error) {
	return store.Task{ID: p.ID, JobID: p.ID, Status: p.Status, WorkerID: p.WorkerID,
		AssignmentEpoch: p.AssignmentEpoch}, nil
}

func (s *sweepableStore) NotifyTaskCompleted(context.Context) error             { return nil }
func (s *sweepableStore) FailDependentTasks(context.Context, pgtype.UUID) error { return nil }
func (s *sweepableStore) RecomputeJobStatus(context.Context, pgtype.UUID) (string, error) {
	return "failed", nil
}

// nopCanceller: the watchdog's cancel fan-out is best-effort and irrelevant here.
type nopCanceller struct{}

func (nopCanceller) SendCancel(string, string, bool) error { return nil }

func countersTestUUID(b byte) pgtype.UUID {
	var raw [16]byte
	raw[15] = b
	return pgtype.UUID{Bytes: raw, Valid: true}
}

// TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters is EXECUTED, and it
// moves a REAL number through the REAL route.
//
// It is the strongest forwarding proof any section in this cluster has: a
// substituted scheduler.NewWatchdog inside buildHTTPServer produces a section of
// zeros here and this test FAILS on the count, with no container. The two
// remaining questions live elsewhere - whether main passes the watchdog it runs
// is TestServerCountersIsWiredByMain (syntactic), and whether the assignment is
// spelled d.<field> is countersAssignmentSources.
func TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters(t *testing.T) {
	wid := countersTestUUID(200)
	q := &sweepableStore{overdue: []store.Task{{
		ID: countersTestUUID(1), JobID: countersTestUUID(99), Status: "running", WorkerID: wid,
		AssignmentEpoch: 7,
		AssignedAt:      pgtype.Timestamptz{Time: time.Now().Add(-48 * time.Hour), Valid: true},
	}}}
	wd := scheduler.NewWatchdog(q, nopCanceller{}, events.NewBroker(), 30*time.Minute, 24*time.Hour)

	srv := buildHTTPServer(httpServerDeps{
		addr:     "127.0.0.1:0",
		q:        store.New(stubAdminDB{}),
		watchdog: wd,
	})

	before := countersAsAdmin(t, srv)
	require.Contains(t, before, "watchdog",
		"a wired watchdog must produce the section from the moment the server is built. An absent "+
			"section reads as 'this build has no watchdog', which is false.")

	require.NoError(t, wd.SweepOnce(context.Background()))

	after := countersAsAdmin(t, srv)
	var section struct {
		Counts struct {
			SweptTotal    uint64            `json:"swept_total"`
			SweptOverflow uint64            `json:"swept_overflow"`
			SweptByWorker map[string]uint64 `json:"swept_by_worker"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(after["watchdog"], &section))
	require.Equal(t, uint64(1), section.Counts.SweptTotal,
		"the served endpoint must report THIS watchdog's sweeps. A stub, a second watchdog or an "+
			"unwired section cannot produce this number.")
	require.Equal(t, uint64(0), section.Counts.SweptOverflow)
	require.Len(t, section.Counts.SweptByWorker, 1,
		"and the sweep must be attributed to the worker the row named")
}

// TestBuildHTTPServer_TypedNilWatchdogLeavesTheSectionAbsent.
//
// SAY WHAT THIS GUARDS, because the item that asked for it overstated the case.
// `var wd *scheduler.Watchdog` conditionally assigned stores a TYPED nil in
// api.WatchdogSource, which is not == nil, so the handler's `src != nil` is true
// and CounterSnapshot dereferences a nil receiver. That shape is NOT what main
// writes and cannot be: TestServerCountersIsWiredByMain requires exactly one
// unconditional assignment on the chain, so the watchdog is constructed
// unconditionally even when both its bounds are zero. This test guards the SHAPE
// against a future caller, not a live panic.
//
// The fix belongs at the wiring boundary where the concrete type is still
// visible, and NOT in a nil-tolerant CounterSnapshot: returning a zero snapshot
// would turn an unwired control into a section of zeros, and "not wired" versus
// "ran and stopped nothing" is the one distinction this payload exists to keep.
func TestBuildHTTPServer_TypedNilWatchdogLeavesTheSectionAbsent(t *testing.T) {
	var unwired *scheduler.Watchdog
	srv := buildHTTPServer(httpServerDeps{
		addr:     "127.0.0.1:0",
		q:        store.New(stubAdminDB{}),
		watchdog: unwired,
	})

	top := countersAsAdmin(t, srv)
	require.NotContains(t, top, "watchdog",
		"a nil watchdog must leave the section ABSENT, never present-and-zero, and must never panic")
	require.Contains(t, top, "started_at")
}

// canonicalWorkerKeyRe is internal/api's canonicalUUIDRe, restated here because
// that one is unexported in a package this test cannot reach into. It is the
// shape internal/scheduler's uuidStr emits and the shape
// counterPayloadAllowList's swept_by_worker predicate demands.
var canonicalWorkerKeyRe = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// distinctWorkerUUID makes more distinct worker uuids than countersTestUUID's
// single byte can express, which this test has to do to cross the cap.
func distinctWorkerUUID(n int) pgtype.UUID {
	var raw [16]byte
	raw[14] = byte(n >> 8)
	raw[15] = byte(n)
	return pgtype.UUID{Bytes: raw, Valid: true}
}

// TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap is the
// only place in the repo where the payload's key rule meets REAL PRODUCER
// BYTES.
//
// internal/api's counterPayloadAllowList is the rule's home, and its jsonOK
// predicate is exercised only against fakeWatchdogSource{c: threeDistinctSweeps()},
// whose keys are string literals in that test file. So the predicate proves
// things about a fixture, not about internal/scheduler - and internal/api
// structurally cannot import internal/scheduler to fix that, because
// internal/scheduler imports internal/api. This package can import both, which
// is why the check lives here.
//
// MEASURED: mutating internal/scheduler's producer so that every key was
// "build-agent-07.corp.example\n10.0.0.7" - a hostname with an injected
// newline, exactly the payload the exemption's own argument cites as what must
// never get in - left BOTH internal/api and cmd/relay-server fully green. The
// only assertion that had ever touched these keys was a require.Len of 1 in
// TestBuildHTTPServer_ServesTheWiredWatchdogsSweepCounters: a count, never a
// shape.
//
// It drives MORE workers than the cap on purpose, so the two halves of the
// exemption's argument - a bounded key count and a server-rendered key shape -
// are both read off one real response.
func TestBuildHTTPServer_TheServedWatchdogKeysAreCanonicalUUIDsUnderTheCap(t *testing.T) {
	// One line per swept task, and there are more than 256 of them.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	const workers = api.WatchdogSweptWorkerMax + 4
	rows := make([]store.Task, 0, workers)
	for i := 0; i < workers; i++ {
		rows = append(rows, store.Task{
			ID: distinctWorkerUUID(1000 + i), JobID: countersTestUUID(99), Status: "running",
			WorkerID:        distinctWorkerUUID(i),
			AssignmentEpoch: 7,
			AssignedAt:      pgtype.Timestamptz{Time: time.Now().Add(-48 * time.Hour), Valid: true},
		})
	}
	require.Less(t, len(rows), scheduler.WatchdogMaxRowsPerSweep,
		"the fixture must fit in ONE sweep, or the cap is never reached here")

	wd := scheduler.NewWatchdog(&sweepableStore{overdue: rows}, nopCanceller{}, events.NewBroker(),
		30*time.Minute, 24*time.Hour)
	srv := buildHTTPServer(httpServerDeps{
		addr:     "127.0.0.1:0",
		q:        store.New(stubAdminDB{}),
		watchdog: wd,
	})
	require.NoError(t, wd.SweepOnce(context.Background()))

	var section struct {
		Counts struct {
			SweptTotal    uint64            `json:"swept_total"`
			SweptOverflow uint64            `json:"swept_overflow"`
			SweptByWorker map[string]uint64 `json:"swept_by_worker"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(countersAsAdmin(t, srv)["watchdog"], &section))

	require.NotEmpty(t, section.Counts.SweptByWorker,
		"the sweep populated no keys at all, so every assertion below would be vacuous")

	// SHAPE FIRST, then the bound. A producer that has stopped rendering uuids
	// usually collapses the key set too, so a cardinality assertion placed above
	// this one would fire first and report the wrong defect.
	for k := range section.Counts.SweptByWorker {
		require.Regexp(t, canonicalWorkerKeyRe, k,
			"the served payload carries the key %q, which is not a server-rendered uuid. This is half "+
				"of counterPayloadAllowList's argument for swept_by_worker, and internal/api can only "+
				"ever check it against a fake source whose keys are literals in its own test file - "+
				"internal/scheduler imports internal/api, so the reverse import that would let it see "+
				"the real producer is impossible. A newline in a key injects a line into every "+
				"operator's log pipeline; a hostname in a key puts a caller-supplied byte in a payload "+
				"whose contract says it carries none.", k)
	}

	require.LessOrEqual(t, len(section.Counts.SweptByWorker), api.WatchdogSweptWorkerMax,
		"the served payload names %d workers, over the %d-key bound that is half of the argument "+
			"admitting this map into a document of integers at all (see counterPayloadAllowList in "+
			"internal/api). Worker ids are server-assigned but their COUNT is not server-limited - "+
			"with RELAY_ALLOW_AUTO_ENROLL on, a reachable host creates one worker row per hostname it "+
			"claims.", len(section.Counts.SweptByWorker), api.WatchdogSweptWorkerMax)
	require.Equal(t, uint64(workers-api.WatchdogSweptWorkerMax), section.Counts.SweptOverflow,
		"and the sweeps the cap refused must be counted, not dropped")
}

// TestBuildHTTPServer_ServesTheWiredHandlersTaskStatusFenceSection is the
// third section fed by the agentHandler deps field, and it is checked the same
// way its two siblings are: through the real route, off a real buildHTTPServer.
//
// NO NEW wiredDep ROW. This section reuses an httpServerDeps field that already
// has one, so it inherits every main.go identifier check that row carries -
// which is exactly what countersAssignmentSources requires and why the
// assignment below must be spelled d.agentHandler.
//
// IT DECODES THE COUNTS HALF AS map[string]json.RawMessage AND REUSES keysOfRaw
// rather than adding a second key-set helper for json.Number. The raw form
// answers both questions this test asks - the key SET and the literal zero - and
// a second helper spelled differently would be a second name for one thing.
func TestBuildHTTPServer_ServesTheWiredHandlersTaskStatusFenceSection(t *testing.T) {
	h := worker.NewHandler(nil, nil, worker.NewRegistry(), events.NewBroker(), func() {})
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: h,
	})

	top := countersAsAdmin(t, srv)
	raw, ok := top["task_status_fence"]
	require.True(t, ok, "a wired agentHandler must serve task_status_fence: %v", keysOfRaw(top))

	var section struct {
		Counts map[string]json.RawMessage `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(raw, &section))
	require.ElementsMatch(t,
		[]string{"raced_total", "duplicate_total", "conflicting_total"},
		keysOfRaw(section.Counts),
		"the three keys are the response contract; a rename here is operator-visible")
	for k, v := range section.Counts {
		require.Equal(t, "0", string(v), "a fresh handler has refused nothing, so %s is an explicit zero", k)
	}

	_, hasIngest := top["ingest_log_budget"]
	_, hasLogFence := top["task_log_fence"]
	require.True(t, hasIngest && hasLogFence,
		"one agentHandler feeds THREE sections under one nil filter, because all three controls live on "+
			"that one object and neither exists without it")
}

// TestStubAdminDB_ResolvesAUserWithARenderableID pins what the stub has to
// produce for any guard that reaches past AdminOnly. Filling only the bool left
// GetTokenWithUserRow's uuid fields invalid, so uuidStr(AuthUser.ID) rendered ""
// - fine for a route that only asks "is this an admin", and fatal for one whose
// behaviour depends on WHICH principal is calling.
//
// The distinctness assertion is the transposition guard: token_id and user_id
// are the same type, and an assertion that passes on either cannot tell a
// per-user control apart from a per-token one.
func TestStubAdminDB_ResolvesAUserWithARenderableID(t *testing.T) {
	row, err := store.New(stubAdminDB{}).GetTokenWithUser(context.Background(), "any")
	require.NoError(t, err)

	require.True(t, row.UserID.Valid, "AuthUser.ID comes from user_id; invalid renders as \"\"")
	require.True(t, row.TokenID.Valid, "AuthUser.TokenID comes from token_id")
	require.True(t, row.TokenUserID.Valid)
	require.NotEqual(t, row.TokenID, row.UserID,
		"token_id and user_id must differ, so an assertion satisfied by one cannot be satisfied by "+
			"the other")
	require.True(t, row.UserIsAdmin, "the admin bool must still be set: every counters test above "+
		"depends on it")
}
