package api

import (
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"relay/internal/netlimit"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdmissionSource returns a fixed snapshot. FIVE DISTINCT VALUES: equal
// values would hide a crossed field assignment in the mapping, which is the
// mutation TestServerCounters_ReportsTheNetlimitSnapshot exists to kill.
type fakeAdmissionSource struct{ s netlimit.Stats }

func (f fakeAdmissionSource) Stats() netlimit.Stats { return f.s }

func testStartedAt() time.Time { return time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC) }

// counterKeys returns the key set of a decoded JSON object.
func counterKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func counterMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestServerCounters_RequiresAuth(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/v1/server/counters", nil) // no Authorization header
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"the counters route must sit behind BearerAuth. These numbers describe adversary activity and "+
			"internal control state; /v1/config and /v1/health are the public routes and this is not one.")
}

func TestServerCounters_OmitsUnwiredSections(t *testing.T) {
	s := &Server{startedAt: testStartedAt()}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	// The discriminating assertion is on KEY ABSENCE in the raw JSON. A decoded
	// zero value cannot tell "this control ran and stopped nothing" from "this
	// control is not wired on this build", and those need opposite responses -
	// which is this endpoint's own subject, one layer up.
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	assert.ElementsMatch(t, []string{"started_at"}, counterKeys(top),
		"an unwired section must be ABSENT from the payload, never present-and-zero")
	assert.Contains(t, rec.Body.String(), "\"started_at\":\"2026-08-21T09:00:00Z\"",
		"started_at is present even when every section is absent: a restart zeroes every counter, so a "+
			"stalled counter and a restart are otherwise the same payload")
}

func TestServerCounters_ReportsTheNetlimitSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters: CounterSources{GRPCAdmission: fakeAdmissionSource{s: netlimit.Stats{
			Counts: netlimit.RefusalCounts{RefusedTotal: 11, RefusedPerIP: 22},
			Levels: netlimit.Occupancy{LiveTotal: 33, DistinctSources: 44, MaxPerSource: 55},
		}}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		StartedAt     string `json:"started_at"`
		GRPCAdmission *struct {
			Counts map[string]any `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"grpc_admission"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.GRPCAdmission, "a wired section must be present")

	// Key-set equality, not per-key assertions alone: a RENAMED key would
	// otherwise decode as a missing value and a per-key check would report zero.
	assert.ElementsMatch(t, []string{"refused_total", "refused_per_source"}, counterMapKeys(body.GRPCAdmission.Counts))
	assert.ElementsMatch(t, []string{"live_total", "distinct_sources", "max_per_source"}, counterMapKeys(body.GRPCAdmission.Levels))

	assert.Equal(t, float64(11), body.GRPCAdmission.Counts["refused_total"])
	assert.Equal(t, float64(22), body.GRPCAdmission.Counts["refused_per_source"])
	assert.Equal(t, float64(33), body.GRPCAdmission.Levels["live_total"])
	assert.Equal(t, float64(44), body.GRPCAdmission.Levels["distinct_sources"])
	assert.Equal(t, float64(55), body.GRPCAdmission.Levels["max_per_source"])
	assert.Equal(t, "2026-08-21T09:00:00Z", body.StartedAt)
}

// stubTokenDB is the narrowest store.DBTX that lets BearerAuth resolve a token
// WITHOUT Postgres, so the admin gate on this route can be proved BEHAVIOURALLY
// in the default (non-integration) lane. That matters because CI runs
// `go test -race ./...` with no tag and no container
// (.github/workflows/go-ci.yml): before this existed, the only behavioural 403
// lived behind //go:build integration and the default gate was a source scan
// alone - which a block-scoped `admin := func(h http.Handler) http.Handler
// { return h }` walked straight past, leaving every authenticated NON-ADMIN
// able to read the payload with the whole repo green.
//
// It is a stub for ONE query, GetTokenWithUser, and it is deliberately not
// general: any other statement panics rather than returning a plausible zero
// value, so a handler that grew a second query cannot pass here by accident.
type stubTokenDB struct{ isAdmin bool }

func (d stubTokenDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("stubTokenDB: Exec is not part of the bearer-auth path")
}

func (d stubTokenDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("stubTokenDB: Query is not part of the bearer-auth path")
}

func (d stubTokenDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return stubTokenRow{isAdmin: d.isAdmin}
}

type stubTokenRow struct{ isAdmin bool }

// Scan fills GetTokenWithUserRow BY DESTINATION TYPE rather than by column
// index, so adding or reordering a column in the query cannot silently make this
// stub authenticate a zero-valued user. The single *bool is user_is_admin, and
// it is what the gate under test reads; the strings are filled so the resulting
// AuthUser is a plausible one rather than empty.
func (r stubTokenRow) Scan(dest ...any) error {
	bools := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = r.isAdmin
			bools++
		case *string:
			*v = "counters-guard"
		}
	}
	if bools != 1 {
		return fmt.Errorf("stubTokenDB: GetTokenWithUserRow has %d bool destinations, want exactly 1 "+
			"(user_is_admin). The row shape changed and this stub no longer decides the admin gate", bools)
	}
	return nil
}

// serverWithStubAuth builds a Server whose bearer-auth resolves any token to a
// user with the given admin flag. Nothing else is wired: Handler() only
// registers routes.
func serverWithStubAuth(isAdmin bool) *Server {
	return &Server{q: store.New(stubTokenDB{isAdmin: isAdmin}), startedAt: testStartedAt()}
}

func countersRequest() *http.Request {
	req := httptest.NewRequest("GET", "/v1/server/counters", nil)
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	return req
}

// TestServerCounters_NonAdminIsForbidden is the BEHAVIOURAL half of the admin
// gate, run through srv.Handler() so that the route pattern, the middleware
// order and the handler are all exercised as one. A guard that compares
// middleware IDENTIFIER NAMES at the registration site cannot see a locally
// rebound `admin`, and a Handler() split into
// `serverRoutes(mux, auth, admin func(http.Handler) http.Handler)` would pass
// every name check while the caller supplied whatever it liked. Execution has no
// such blind spot.
func TestServerCounters_NonAdminIsForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	serverWithStubAuth(false).Handler().ServeHTTP(rec, countersRequest())

	require.Equal(t, http.StatusForbidden, rec.Code,
		"an AUTHENTICATED NON-ADMIN must not read GET /v1/server/counters. These numbers describe "+
			"adversary activity and internal control state, and this route is the one place they are "+
			"served on demand.")
	assert.NotContains(t, rec.Body.String(), "started_at",
		"a 403 must carry no part of the payload")
}

// TestServerCounters_AdminReadsThePayload is the other side of the same gate.
// Without it, `admin := func(http.Handler) http.Handler { return nil }` - or
// admin(auth(...)) in the wrong order, which 403s everybody including admins -
// would satisfy the test above and lock the endpoint shut instead of open.
func TestServerCounters_AdminReadsThePayload(t *testing.T) {
	s := serverWithStubAuth(true)
	s.Counters = CounterSources{GRPCAdmission: fakeAdmissionSource{s: netlimit.Stats{
		Counts: netlimit.RefusalCounts{RefusedTotal: 11, RefusedPerIP: 22},
		Levels: netlimit.Occupancy{LiveTotal: 33, DistinctSources: 44, MaxPerSource: 55},
	}}}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, countersRequest())

	require.Equal(t, http.StatusOK, rec.Code, "an admin must reach the handler through the real route")
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	assert.ElementsMatch(t, []string{"started_at", "grpc_admission"}, counterKeys(top))
}

// TestServerCountersRouteIsAdminGated is the SECOND layer, and it is SCOPED
// rather than file-wide.
//
// The behavioural pair above is what actually proves 403-then-200. This exists
// for the two shapes one call cannot see: a SECOND registration of the same
// pattern elsewhere in the file, and the registration migrating out of Handler()
// into a helper whose middleware arrive as parameters.
//
// EVERY LOOKUP HERE IS ANCHORED TO func (s *Server) Handler()'s OWN BODY, which
// is precisely what the first version got wrong: it resolved `auth` and `admin`
// from assignments ANYWHERE in server.go and then compared identifiers by NAME
// at the registration site, so
//
//	{
//	    admin := func(h http.Handler) http.Handler { return h }
//	    mux.Handle("GET /v1/server/counters", auth(admin(...)))
//	}
//
// passed with `go test ./...` entirely green while every authenticated non-admin
// could read the payload. Names are not bindings. Requiring the registration to
// be a DIRECT STATEMENT of Handler's body, and each middleware name to be bound
// EXACTLY ONCE in that same body, is what makes the name comparison mean what it
// says.
func TestServerCountersRouteIsAdminGated(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	require.NoError(t, err)

	var body *ast.BlockStmt
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Handler" || fd.Recv == nil || fd.Body == nil {
			continue
		}
		body = fd.Body
	}
	require.NotNil(t, body, "server.go no longer declares func (s *Server) Handler() with a body")

	// Resolve the two middleware locals from assignments that are DIRECT
	// statements of Handler's body, and from their RIGHT-HAND SIDES. Name-only
	// resolution is satisfied by any `admin := ...`; file-wide resolution is
	// satisfied by the real binding while a shadow does the work.
	authName, adminName := "", ""
	authBinds, adminBinds := 0, 0
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			continue
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			continue
		}
		switch rhs := as.Rhs[0].(type) {
		case *ast.CallExpr:
			if fn, ok := rhs.Fun.(*ast.Ident); ok && fn.Name == "BearerAuth" {
				authName, authBinds = id.Name, authBinds+1
			}
		case *ast.Ident:
			if rhs.Name == "AdminOnly" {
				adminName, adminBinds = id.Name, adminBinds+1
			}
		}
	}
	require.Equal(t, 1, authBinds,
		"Handler()'s body must bind BearerAuth(...) to a local exactly once; found %d such bindings", authBinds)
	require.Equal(t, 1, adminBinds,
		"Handler()'s body must bind AdminOnly to a local exactly once; found %d such bindings", adminBinds)

	const route = "GET /v1/server/counters"
	asReg := func(n ast.Node) *ast.CallExpr {
		ce, ok := n.(*ast.CallExpr)
		if !ok || len(ce.Args) == 0 {
			return nil
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
			return nil
		}
		lit, ok := ce.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return nil
		}
		if v, err := strconv.Unquote(lit.Value); err == nil && v == route {
			return ce
		}
		return nil
	}

	// File-wide first: a second registration anywhere is a second answer.
	var all []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if ce := asReg(n); ce != nil {
			all = append(all, ce)
		}
		return true
	})
	require.Len(t, all, 1,
		"%q must be registered exactly once in server.go, with the pattern spelled as a string literal at "+
			"the registration site. A pattern built from a constant or a variable leaves this guard unable "+
			"to see the route at all.", route)

	// Then scoped: the one registration must be a DIRECT statement of Handler's
	// body, so the middleware identifiers it names are the ones bound above and
	// not a block-scoped, closure-scoped or parameter-supplied rebinding.
	var regs []*ast.CallExpr
	for _, st := range body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		if ce := asReg(es.X); ce != nil {
			regs = append(regs, ce)
		}
	}
	require.Len(t, regs, 1,
		"%q is registered in server.go but NOT as a direct statement of func (s *Server) Handler()'s own "+
			"body. Wrapping it in a block, an if or a closure, or moving it into a routes helper that "+
			"receives `auth, admin func(http.Handler) http.Handler` as PARAMETERS, all put a different "+
			"binding behind the same two identifiers - which is exactly what a by-name check cannot see. "+
			"If the route table is being split, the split is the decision: make it here.", route)

	sel := regs[0].Fun.(*ast.SelectorExpr)
	require.Equal(t, "Handle", sel.Sel.Name,
		"the counters route must be registered with mux.Handle(pattern, auth(admin(...))). mux.HandleFunc "+
			"takes a bare handler, which is this route with no authentication whatsoever.")
	require.Len(t, regs[0].Args, 2)

	outer, ok := regs[0].Args[1].(*ast.CallExpr)
	require.True(t, ok, "the counters handler is registered unwrapped: no auth, no admin")
	outerName := routeIdentName(outer.Fun)
	require.Equal(t, authName, outerName,
		"the OUTERMOST wrapper on the counters route must be the bearer-auth middleware bound in "+
			"Handler()'s body (%q), got %q. AdminOnly reads the user out of the request context and "+
			"BearerAuth is what puts it there, so admin(auth(...)) returns 403 to every caller including "+
			"admins.", authName, outerName)
	require.Len(t, outer.Args, 1)

	inner, ok := outer.Args[0].(*ast.CallExpr)
	require.True(t, ok,
		"the counters route is wrapped in %s(...) alone. Every authenticated user would then be able to "+
			"read numbers that describe adversary activity and internal control state.", outerName)
	innerName := routeIdentName(inner.Fun)
	require.Equal(t, adminName, innerName,
		"the counters route must be wrapped in the AdminOnly local bound in Handler()'s body (%q), inside "+
			"the bearer-auth middleware; got %q", adminName, innerName)
}

func routeIdentName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

// counterPayloadAllowList maps a payload path to the argument for why a
// non-integer is admissible there.
//
// WRITTEN AS AN ALLOW-LIST, NEVER AS A DENY-LIST. The two are interchangeable
// against today's payload, but a deny-list fails OPEN on the next field somebody
// adds and an allow-list fails closed. The list already names slice 4's
// swept_by_worker map, so slice 4 adds a field rather than an exception, and any
// OTHER new non-integer field goes RED and forces the argument.
//
// Entries need not exist yet, which means an entry can go stale. That is a cost
// accepted in exchange for the contract being decided once.
var counterPayloadAllowList = map[string]string{
	"started_at": "a timestamp, server-generated, and the one field that makes a zeroed counter " +
		"distinguishable from a restart",
	"watchdog.counts.swept_by_worker": "server-resolved worker UUIDs from a row the coordinator's own " +
		"scan returned, never a caller-supplied byte. Admissible HERE because this route is " +
		"admin-authenticated; still inadmissible in any log line reachable from the gRPC recv path.",
}

// counterPayloadLeaves is the response CONTRACT, asserted by both halves of the
// guard below. Update it in the same commit as the field, so that adding a
// section is a decision rather than a diff nobody read.
var counterPayloadLeaves = []string{
	"started_at",
	"grpc_admission.counts.refused_total",
	"grpc_admission.counts.refused_per_source",
	"grpc_admission.levels.live_total",
	"grpc_admission.levels.distinct_sources",
	"grpc_admission.levels.max_per_source",
}

// TestCounterPayloadCarriesNoIdentifiers is the cardinality rule of the
// silent-drop spec, shipped in slice 1 as a payload guard so that slices 2, 3
// and 4 inherit it rather than re-argue it.
//
// The walk is written from the PROPERTY - every leaf of the response tree is an
// unsigned integer - and searched against the shape: pointers are dereferenced,
// structs are recursed into, and maps, slices, strings, signed integers, floats,
// bools and interfaces all fail. The exact leaf-path assertion is what makes it
// non-vacuous: a walk that visited nothing would satisfy the type rule
// trivially.
//
// TWO SHAPES THE TYPE WALK ALONE CANNOT SEE, both closed here rather than left
// to the next reader to discover, because a guard built from one mutation
// inherits that mutation's shape:
//
//   - A CUSTOM MARSHALLER. reflect reports the Go kind, and a named uint64 (or
//     any struct) carrying its own MarshalJSON or MarshalText emits whatever it
//     likes - a label, a prefix, an address - while passing an unsigned-integer
//     check. So a type reached by this walk must not implement either
//     interface; time.Time does implement both and is reached only through the
//     allow-list, which short-circuits before the type is ever inspected.
//   - THE HANDLER NOT USING THIS TYPE AT ALL. Walking serverCountersResponse
//     says nothing about the bytes handleServerCounters writes. The second half
//     below decodes a REAL response with every section wired and walks the
//     decoded JSON, so a handler that assembled a map[string]any, or a field
//     whose marshaller emitted a string, is caught on the wire rather than in
//     the type system.
func TestCounterPayloadCarriesNoIdentifiers(t *testing.T) {
	jsonMarshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshaler := reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()

	var leaves []string
	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		require.Equal(t, reflect.Struct, typ.Kind(), "%s is not a struct", path)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := f.Tag.Get("json")
			if c := strings.Index(name, ","); c >= 0 {
				name = name[:c]
			}
			if name == "" {
				name = f.Name
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			// The allow-list is consulted BEFORE the kind switch, and that
			// ordering is load-bearing: time.Time is a struct whose own fields
			// would otherwise be walked into.
			if _, ok := counterPayloadAllowList[p]; ok {
				leaves = append(leaves, p)
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			for _, iface := range []struct {
				t    reflect.Type
				name string
			}{{jsonMarshaler, "json.Marshaler"}, {textMarshaler, "encoding.TextMarshaler"}} {
				if ft.Implements(iface.t) || reflect.PointerTo(ft).Implements(iface.t) {
					t.Fatalf("counter payload field %q has type %s, which implements %s. This walk reports "+
						"the GO KIND, so a custom marshaller can emit an address, a prefix or a label from "+
						"a field that passes an unsigned-integer check. If the marshaller is genuinely "+
						"necessary, add the path to counterPayloadAllowList WITH the argument.",
						p, ft.String(), iface.name)
				}
			}
			switch ft.Kind() {
			case reflect.Struct:
				walk(ft, p)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				leaves = append(leaves, p)
			default:
				t.Fatalf("counter payload field %q is a %s. Every field in this payload must be an "+
					"UNSIGNED INTEGER unless it is on the allow-list at the top of this file. A string, a "+
					"map key, a slice element or a signed value is where a caller-supplied byte or an "+
					"unbounded cardinality gets in - this repo has already shipped an attacker-keyed "+
					"counter once. If the field is genuinely necessary, add it to the allow-list WITH the "+
					"argument, in the same commit.", p, ft.Kind())
			}
		}
	}
	walk(reflect.TypeOf(serverCountersResponse{}), "")

	assert.ElementsMatch(t, counterPayloadLeaves, leaves,
		"the counter payload's field set changed. counterPayloadLeaves is the response CONTRACT: update "+
			"it in the same commit as the field, so that adding a section is a decision rather than a diff "+
			"nobody read.")
}

// TestCounterPayloadBytesCarryNoIdentifiers is the same rule applied to the
// bytes the handler actually writes, with EVERY section wired so that no section
// is omitted and therefore unexamined. The type walk next door proves the shape
// of serverCountersResponse; this proves handleServerCounters serialises that
// shape and nothing else.
func TestCounterPayloadBytesCarryNoIdentifiers(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters: CounterSources{GRPCAdmission: fakeAdmissionSource{s: netlimit.Stats{
			Counts: netlimit.RefusalCounts{RefusedTotal: 11, RefusedPerIP: 22},
			Levels: netlimit.Occupancy{LiveTotal: 33, DistinctSources: 44, MaxPerSource: 55},
		}}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	dec := json.NewDecoder(rec.Body)
	dec.UseNumber()
	var doc any
	require.NoError(t, dec.Decode(&doc))

	var leaves []string
	var walk func(v any, path string)
	walk = func(v any, path string) {
		if _, ok := counterPayloadAllowList[path]; ok && path != "" {
			leaves = append(leaves, path)
			return
		}
		switch tv := v.(type) {
		case map[string]any:
			require.NotEmpty(t, tv, "counter payload object %q is empty, so this walk examined nothing in it", path)
			for k, child := range tv {
				p := k
				if path != "" {
					p = path + "." + k
				}
				walk(child, p)
			}
		case json.Number:
			require.NotContains(t, tv.String(), "-",
				"counter payload value %q is negative (%s). Every number here is a count or a level.", path, tv)
			leaves = append(leaves, path)
		default:
			t.Fatalf("counter payload value %q serialises as %T. On the wire this payload contains numbers "+
				"and objects only, unless the path is on counterPayloadAllowList. A string, an array or a "+
				"boolean here is where a caller-supplied byte or an unbounded cardinality reaches an "+
				"operator-facing document.", path, v)
		}
	}
	walk(doc, "")

	assert.ElementsMatch(t, counterPayloadLeaves, leaves,
		"the SERIALISED counter payload's leaf set is not the contract. The type-level guard next door "+
			"cannot see this: a handler that assembles its own map, or a field with a custom marshaller, "+
			"passes there and fails here.")
}
