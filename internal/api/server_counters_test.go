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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"relay/internal/netlimit"
	"relay/internal/store"
	"relay/internal/worker"

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

// TestServerCounters_WiredButZeroSectionIsStillPresent is the OTHER half of the
// absent-versus-zero contract, and until now only the absent half had a test.
//
// server_counters.go states that a section of zeros means "this control ran and
// stopped nothing" and an absent section means "not wired on this build", and
// calls collapsing the two the very defect the payload exists to fix. Both
// existing wired-source tests use five NON-ZERO values, so an early return that
// omitted the section whenever Stats() was the zero value - the tidy-looking
// `if st == (netlimit.Stats{}) { return }` - was green across `go test ./...`.
// A healthy server that has refused nothing is exactly the case that mutation
// breaks, and it is the common case.
//
// The assertion is on the RAW JSON key set, twice: a decoded struct cannot tell
// a present-and-zero object from a missing one, which is the whole distinction.
func TestServerCounters_WiredButZeroSectionIsStillPresent(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{GRPCAdmission: fakeAdmissionSource{s: netlimit.Stats{}}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.ElementsMatch(t, []string{"started_at", "grpc_admission"}, counterKeys(top),
		"a WIRED source whose every counter is zero must still emit its section. Omitting it says 'this "+
			"control is not on this build', when the truth is 'this control ran and stopped nothing' - "+
			"and telling those apart is the entire purpose of this payload.")

	var section map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["grpc_admission"], &section))
	require.ElementsMatch(t, []string{"counts", "levels"}, counterKeys(section),
		"both halves must be present as objects, not elided by omitempty")

	for _, half := range []string{"counts", "levels"} {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(section[half], &fields))
		require.NotEmpty(t, fields, "%s must be an object of zeros, not an empty object", half)
		for k, v := range fields {
			assert.Equal(t, "0", string(v), "%s.%s must serialise as an explicit zero", half, k)
		}
	}
}

// TestNewStampsStartedAt.
//
// Every other test in this file hand-builds &Server{startedAt: ...}, and the
// only check of the PRODUCTION path was behind //go:build integration - which CI
// does not run. Deleting `startedAt: time.Now().UTC()` from New builds clean and
// leaves `go test ./internal/api/ ./cmd/relay-server/` green, and the endpoint
// then reports 0001-01-01T00:00:00Z forever.
//
// That is not a cosmetic field. started_at is the ONLY thing separating "the
// counters stopped moving" from "the process restarted and zeroed them", so a
// zero value makes every restart invisible to a poller, silently, while still
// answering 200 - this endpoint's own subject one layer down.
func TestNewStampsStartedAt(t *testing.T) {
	before := time.Now().UTC()
	s := New(nil, nil, nil, nil, nil, 0, 0, 0, 0)
	after := time.Now().UTC()

	require.False(t, s.startedAt.IsZero(),
		"New must stamp startedAt. A zero value serialises as 0001-01-01T00:00:00Z and makes every "+
			"restart invisible to a poller.")
	assert.False(t, s.startedAt.Before(before), "startedAt must be taken during New, not earlier")
	assert.False(t, s.startedAt.After(after), "startedAt must be taken during New, not later")
	assert.Equal(t, time.UTC, s.startedAt.Location(),
		"startedAt is serialised straight into the payload, so it must be UTC rather than whatever zone "+
			"the host happens to be in")
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

// counterPayloadExemption authorizes a SHAPE at a path, never the path itself.
//
// THE PREDICATE IS THE POINT, AND IT REPLACES A PROSE REASON THAT WAS A TOTAL
// EXEMPTION. Both walks below used to consult the allow-list BEFORE the
// marshaller check and before the kind switch, then continue without descending,
// so an allow-listed path was never inspected at all - not its kind, not its key
// type, not its cardinality, and on the wire not its keys or its values. A
// map[string]string sitting at such a path, carrying a hostname-shaped key with
// an embedded newline and an RTL override and an IP address for a value, passed
// BOTH guards with zero failures. Demonstrated, not theorised.
//
// So an exemption now says what shape it argued for and is checked against it:
// started_at is exempt AS A time.Time serialising as an RFC 3339 instant, and
// nothing else at that path is exempt from anything.
//
// AND THE LIST NAMES ONLY PATHS THAT EXIST. "watchdog.counts.swept_by_worker"
// was first written here in slice 1 against code nobody had written,
// pre-authorizing a map of UUID keys whose only remaining forcing function was a
// one-line edit to counterPayloadLeaves with its justification already supplied;
// it was taken out for that reason. It is BACK, in slice 4, in the commit that
// shipped the map - with an argument that can be read against the producer, and
// with predicates that do the descending themselves (key shape, value shape and
// the WatchdogSweptWorkerMax cardinality bound) rather than accepting a
// container and stopping. TestWatchdogSweptByWorkerExemptionRejectsHostileShapes
// is that entry's own test, and it is where the exemption's real content lives.
//
// THE RESIDUAL, STATED SO THE NEXT ENTRY IS WRITTEN KNOWING IT: both walks
// still stop at an exempted path - the JSON walk `return`s and the type walk
// `continue`s once the predicate passes - so an exemption is SHAPE-CHECKED but
// NON-DESCENDING. That is exactly right for started_at, whose shape is a scalar
// and whose predicate therefore examines the whole value. It would not be right
// for a container: a jsonOK that accepted map[string]any would leave every key
// and every value in that map uninspected, which is the total exemption this
// mechanism replaced, re-entered through the predicate. An exemption for a
// container must therefore do the descending ITSELF inside jsonOK/typeOK -
// checking key shape, value shape and cardinality - or the walks must be taught
// to recurse past it first.
type counterPayloadExemption struct {
	// why is the argument for the exemption, quoted back in the failure.
	why string
	// typeOK reports whether the Go type at this path is the exempted shape.
	typeOK func(reflect.Type) bool
	// jsonOK reports whether the decoded JSON value at this path is that shape.
	jsonOK func(any) bool
}

// canonicalUUIDRe is exactly what internal/scheduler's uuidStr emits: lowercase
// hex, 8-4-4-4-12, anchored. Anchored and hex-only is what makes the
// newline-injection and RTL-override class unrepresentable rather than merely
// unlikely.
var canonicalUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var counterPayloadAllowList = map[string]counterPayloadExemption{
	"watchdog.counts.swept_by_worker": {
		why: "a map from SERVER-ASSIGNED worker uuids to how many of that worker's assignments the " +
			"coordinator watchdog ended. THE SECURITY QUESTION SPLITS IN TWO AND ONLY ONE HALF IS " +
			"ANSWERED BY 'server-assigned'. (1) CALLER-SUPPLIED BYTES: no. The keys are uuidStr() of a " +
			"worker_id the coordinator's own scan returned, and this route is admin-authenticated, so a " +
			"uuid admissible HERE is still inadmissible in any log line reachable from the gRPC recv " +
			"path. The predicates below CHECK that shape rather than assert it, because an exemption " +
			"granted to a NAME is an exemption from every question. (2) CARDINALITY: server-assigned is " +
			"NOT server-limited. With RELAY_ALLOW_AUTO_ENROLL on, a reachable host creates one " +
			"persistent workers row per hostname it claims " +
			"(bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded, open), so a peer CAN drive " +
			"the number of distinct keys an admin-facing document serialises on every request. The " +
			"control is therefore the producer's hard cap, WatchdogSweptWorkerMax, which is ENFORCED " +
			"in jsonOK below rather than described beside it. DO NOT cite the workers row count as the " +
			"bound - that is the quantity that is unbounded.",
		typeOK: func(t reflect.Type) bool {
			return t.Kind() == reflect.Map &&
				t.Key().Kind() == reflect.String &&
				t.Elem().Kind() == reflect.Uint64
		},
		jsonOK: func(v any) bool {
			// DESCEND. Both walks stop here, so anything this function does not
			// look at is unexamined for the life of the payload.
			m, ok := v.(map[string]any)
			if !ok {
				// nil lands here: a nil Go map serialises as null, and the
				// producer must always allocate so the section reads {} on a
				// watchdog that has swept nothing.
				return false
			}
			if len(m) > WatchdogSweptWorkerMax {
				return false
			}
			for k, val := range m {
				if !canonicalUUIDRe.MatchString(k) {
					return false
				}
				n, ok := val.(json.Number)
				if !ok {
					return false
				}
				if _, err := strconv.ParseUint(n.String(), 10, 64); err != nil {
					return false
				}
			}
			return true
		},
	},
	"started_at": {
		why: "a timestamp, server-generated, and the one field that makes a zeroed counter " +
			"distinguishable from a restart",
		typeOK: func(t reflect.Type) bool { return t == reflect.TypeOf(time.Time{}) },
		jsonOK: func(v any) bool {
			s, ok := v.(string)
			if !ok {
				return false
			}
			_, err := time.Parse(time.RFC3339, s)
			return err == nil
		},
	},
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
	"ingest_log_budget.counts.deduped.task_log_persist",
	"ingest_log_budget.counts.deduped.bad_task_id_log",
	"ingest_log_budget.counts.deduped.bad_task_id_status",
	"ingest_log_budget.counts.deduped.status_get_task",
	"ingest_log_budget.counts.deduped.inventory",
	"ingest_log_budget.counts.suppressed.task_log_persist",
	"ingest_log_budget.counts.suppressed.bad_task_id_log",
	"ingest_log_budget.counts.suppressed.bad_task_id_status",
	"ingest_log_budget.counts.suppressed.status_get_task",
	"ingest_log_budget.counts.suppressed.inventory",
	"task_log_fence.counts.rejected_total",
	"watchdog.counts.swept_total",
	"watchdog.counts.swept_overflow",
	"watchdog.counts.swept_by_worker",
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
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			// The allow-list is consulted BEFORE the kind switch, and that
			// ordering is load-bearing: time.Time is a struct whose own fields
			// would otherwise be walked into. What it is NOT is a free pass -
			// the exempted SHAPE is checked here, or the entry authorizes
			// whatever a later commit puts at that path.
			if ex, ok := counterPayloadAllowList[p]; ok {
				require.True(t, ex.typeOK(ft),
					"counter payload field %q is exempt from the unsigned-integer rule AS A SHAPE, and "+
						"its type is now %s, which is not that shape. The exemption argued: %s. Re-argue "+
						"it for the new type or take the exemption out.", p, ft.String(), ex.why)
				leaves = append(leaves, p)
				continue
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
		Counters: CounterSources{
			GRPCAdmission: fakeAdmissionSource{s: netlimit.Stats{
				Counts: netlimit.RefusalCounts{RefusedTotal: 11, RefusedPerIP: 22},
				Levels: netlimit.Occupancy{LiveTotal: 33, DistinctSources: 44, MaxPerSource: 55},
			}},
			IngestLogBudget: fakeIngestLogSource{d: tenDistinctDrops()},
			TaskLogFence:    fakeTaskLogFenceSource{n: 123},
			Watchdog:        fakeWatchdogSource{c: threeDistinctSweeps()},
		},
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
		if ex, ok := counterPayloadAllowList[path]; ok && path != "" {
			require.True(t, ex.jsonOK(v),
				"counter payload value %q is exempt AS A SHAPE and serialises as %T (%v), which is not "+
					"that shape. The exemption argued: %s.", path, v, v, ex.why)
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

// TestIngestLogKindCountsPublishesEveryWorkerSideField is the completeness guard
// on the ONE boundary in this chain that had none, and this package is the one
// place both types are visible.
//
// EVERY OTHER LINK IS ALREADY COUNTED: the kinds against the counters array
// (TestIngestLogKindsAreADenseRunFromOne), the array against
// worker.IngestLogDropsByKind
// (TestIngestLogCounters_EveryKindIsPublishedDistinctly's final require.Len),
// api.CounterSources' fields against the wiring table
// (TestServerCountersIsWiredByMain), the response against the handler's
// branches (counterPayloadLeaves). ingestLogKindCountsFrom was the gap: it is
// five hand-written assignments and NOTHING compared its arity to its source's.
// Measured - a fully correct sixth kind (const inside the sentinel, a real
// lim.allow call site, an IngestLogDropsByKind field, a byKind line, the dense-
// run test's list updated) left internal/worker, internal/api AND
// cmd/relay-server green with `go vet -tags integration` clean, while that kind
// was counted on the recv path and published under no JSON key. That is this
// slice's own defect class one layer up: a counter that silently stops counting,
// inside the mechanism built because controls silently stop reporting.
//
// counterPayloadLeaves cannot cover it. It is an ElementsMatch against a fixed
// list, so it reddens on an EXTRA api-side leaf and never on a MISSING
// worker-side one - it is a contract check on this package's own payload, and
// the missing field is not in the payload to be noticed.
//
// CARDINALITY IS ENOUGH, and the reason is worth stating so nobody "improves"
// this into a name comparison it does not need: ingestLogKindCountsFrom assigns
// BY NAME, so a worker-side rename is already a compile error here. Only the
// arity can drift silently.
func TestIngestLogKindCountsPublishesEveryWorkerSideField(t *testing.T) {
	src := reflect.TypeOf(worker.IngestLogDropsByKind{})
	pub := reflect.TypeOf(ingestLogKindCounts{})
	require.Equal(t, src.NumField(), pub.NumField(),
		"worker.IngestLogDropsByKind has %d fields and api.ingestLogKindCounts has %d. Every kind the "+
			"recv path counts must reach a JSON key: ingestLogKindCountsFrom is hand-written, so a "+
			"field added on one side and not the other is counted into a number nobody can read (or, "+
			"the other way, published as a permanent zero). Add the field, its json tag, its line in "+
			"ingestLogKindCountsFrom, and its name to the kinds list in "+
			"TestServerCounters_ReportsTheIngestLogSnapshot.", src.NumField(), pub.NumField())
}

// fakeIngestLogSource returns a fixed snapshot. TEN DISTINCT VALUES: the mapping
// from worker.IngestLogDrops into the response types is ten hand-written
// assignments, and equal values would hide a crossed one.
type fakeIngestLogSource struct{ d worker.IngestLogDrops }

func (f fakeIngestLogSource) IngestLogDropCounts() worker.IngestLogDrops { return f.d }

func tenDistinctDrops() worker.IngestLogDrops {
	return worker.IngestLogDrops{
		Deduped: worker.IngestLogDropsByKind{
			TaskLogPersist: 11, BadTaskIDLog: 22, BadTaskIDStatus: 33,
			StatusGetTask: 44, Inventory: 55,
		},
		Suppressed: worker.IngestLogDropsByKind{
			TaskLogPersist: 66, BadTaskIDLog: 77, BadTaskIDStatus: 88,
			StatusGetTask: 99, Inventory: 110,
		},
	}
}

func TestServerCounters_ReportsTheIngestLogSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{IngestLogBudget: fakeIngestLogSource{d: tenDistinctDrops()}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		IngestLogBudget *struct {
			Counts struct {
				Deduped    map[string]any `json:"deduped"`
				Suppressed map[string]any `json:"suppressed"`
			} `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"ingest_log_budget"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.IngestLogBudget, "a wired section must be present")
	require.Nil(t, body.IngestLogBudget.Levels,
		"this section is COUNTS ONLY. Every limiter is a per-connection stack local, so there is no "+
			"process-wide 'current' figure to report without building the shared registry the limiter "+
			"deliberately refuses.")

	// Key-set equality, not per-key assertions alone: a renamed key would decode
	// as a missing value and a per-key check would report zero. THESE NAMES ARE A
	// RESPONSE CONTRACT - see the logKind block in internal/worker.
	kinds := []string{"task_log_persist", "bad_task_id_log", "bad_task_id_status", "status_get_task", "inventory"}
	assert.ElementsMatch(t, kinds, counterMapKeys(body.IngestLogBudget.Counts.Deduped))
	assert.ElementsMatch(t, kinds, counterMapKeys(body.IngestLogBudget.Counts.Suppressed))

	assert.Equal(t, float64(11), body.IngestLogBudget.Counts.Deduped["task_log_persist"])
	assert.Equal(t, float64(22), body.IngestLogBudget.Counts.Deduped["bad_task_id_log"])
	assert.Equal(t, float64(33), body.IngestLogBudget.Counts.Deduped["bad_task_id_status"])
	assert.Equal(t, float64(44), body.IngestLogBudget.Counts.Deduped["status_get_task"])
	assert.Equal(t, float64(55), body.IngestLogBudget.Counts.Deduped["inventory"])
	assert.Equal(t, float64(66), body.IngestLogBudget.Counts.Suppressed["task_log_persist"])
	assert.Equal(t, float64(77), body.IngestLogBudget.Counts.Suppressed["bad_task_id_log"])
	assert.Equal(t, float64(88), body.IngestLogBudget.Counts.Suppressed["bad_task_id_status"])
	assert.Equal(t, float64(99), body.IngestLogBudget.Counts.Suppressed["status_get_task"])
	assert.Equal(t, float64(110), body.IngestLogBudget.Counts.Suppressed["inventory"])
}

// TestServerCounters_WiredButZeroIngestSectionIsStillPresent is the
// absent-versus-zero contract for this section. A server whose budget has
// dropped nothing is the COMMON case, and it must not read as "this build does
// not have an ingest budget".
//
// It walks two levels rather than one: unlike grpc_admission, this section's
// counts half contains OBJECTS, so the shipped
// TestServerCounters_WiredButZeroSectionIsStillPresent's scalar loop would fail
// here. Do not copy that loop.
func TestServerCounters_WiredButZeroIngestSectionIsStillPresent(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{IngestLogBudget: fakeIngestLogSource{}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.ElementsMatch(t, []string{"started_at", "ingest_log_budget"}, counterKeys(top),
		"a WIRED source whose every counter is zero must still emit its section: zeros mean 'this "+
			"control ran and stopped nothing', absence means 'not wired on this build'")

	var section map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["ingest_log_budget"], &section))
	require.ElementsMatch(t, []string{"counts"}, counterKeys(section),
		"counts only; this section has no levels")

	var counts map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(section["counts"], &counts))
	require.ElementsMatch(t, []string{"deduped", "suppressed"}, counterKeys(counts))
	for _, arm := range []string{"deduped", "suppressed"} {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(counts[arm], &fields))
		require.Len(t, fields, 5, "%s must carry one key per kind, not an empty object", arm)
		for k, v := range fields {
			assert.Equal(t, "0", string(v), "%s.%s must serialise as an explicit zero", arm, k)
		}
	}
}

// TestServerCounters_OneWiredSectionDoesNotDragInTheOther. Each section is its
// own nil-able source, so wiring the ingest budget must not conjure a
// grpc_admission object and vice versa.
func TestServerCounters_OneWiredSectionDoesNotDragInTheOther(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{IngestLogBudget: fakeIngestLogSource{d: tenDistinctDrops()}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	assert.ElementsMatch(t, []string{"started_at", "ingest_log_budget"}, counterKeys(top),
		"an unwired section stays ABSENT even when a sibling section is wired")
}

// fakeTaskLogFenceSource returns a fixed count.
type fakeTaskLogFenceSource struct{ n uint64 }

func (f fakeTaskLogFenceSource) TaskLogFenceRejections() uint64 { return f.n }

// TestTaskLogFenceSourceReturnsAScalar is a forcing function on an ANTECEDENT,
// not on today's code.
//
// The rule this package learned in the ingest slice is that any section whose
// payload struct RESTATES fields owned by another package needs a NumField
// assertion between the two types, written here because this is the only place
// both are visible (TestIngestLogKindCountsPublishesEveryWorkerSideField).
// task_log_fence restates NOTHING: its source returns a bare uint64, there is no
// hand-written field-by-field mapper, and so that rule does not apply - which is
// only true while the return type stays a scalar. Widening it to a struct is a
// one-line edit that would silently move this section into the class that needs
// the arity check.
//
// THE ANTECEDENT IS WHAT IS GUARDED HERE, deliberately: there is nothing to
// count today, so the only honest guard is one that reddens the moment counting
// becomes necessary, and whose message names what must ship alongside.
func TestTaskLogFenceSourceReturnsAScalar(t *testing.T) {
	iface := reflect.TypeOf((*TaskLogFenceSource)(nil)).Elem()
	require.Equal(t, 1, iface.NumMethod(),
		"TaskLogFenceSource must stay a ONE-METHOD interface. A second method is a second thing to "+
			"restate, and the reasoning below only covers the one.")
	m, ok := iface.MethodByName("TaskLogFenceRejections")
	require.True(t, ok, "TaskLogFenceSource must declare TaskLogFenceRejections")
	require.Equal(t, 1, m.Type.NumOut(), "one return value")
	require.Equal(t, reflect.Uint64, m.Type.Out(0).Kind(),
		"TaskLogFenceRejections returns %s. While it returns a SCALAR this section restates no "+
			"worker-side field and needs no cardinality check. If it now returns a struct, an arity "+
			"assertion between the worker-side type and the api-side type must ship IN THIS COMMIT - see "+
			"TestIngestLogKindCountsPublishesEveryWorkerSideField, which exists because a fully correct "+
			"sixth kind was counted on the recv path and published under no JSON key with all three "+
			"packages green.", m.Type.Out(0).Kind())
}

func TestServerCounters_ReportsTheTaskLogFenceSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{TaskLogFence: fakeTaskLogFenceSource{n: 77}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		TaskLogFence *struct {
			Counts map[string]any `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"task_log_fence"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.TaskLogFence, "a wired section must be present")
	require.Nil(t, body.TaskLogFence.Levels,
		"COUNTS ONLY. There is no current 'level' of fence rejections to report - the number is a "+
			"monotonic total since started_at.")

	// Key-set equality, not a per-key assertion alone: a renamed key would decode
	// as a missing value and read as zero.
	assert.ElementsMatch(t, []string{"rejected_total"}, counterMapKeys(body.TaskLogFence.Counts))
	assert.Equal(t, float64(77), body.TaskLogFence.Counts["rejected_total"])
}

// TestServerCounters_WiredButZeroTaskLogFenceSectionIsStillPresent. A server
// whose fence has rejected nothing is the COMMON case and must still emit its
// section: zeros mean "this control ran and stopped nothing", absence means "not
// wired on this build". This section's counts half is SCALARS, so unlike
// ingest_log_budget the shipped one-level loop is the right shape here.
func TestServerCounters_WiredButZeroTaskLogFenceSectionIsStillPresent(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{TaskLogFence: fakeTaskLogFenceSource{}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.ElementsMatch(t, []string{"started_at", "task_log_fence"}, counterKeys(top),
		"a WIRED source whose counter is zero must still emit its section, and no OTHER section may "+
			"appear: each source is nil-able on its own")

	var section map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["task_log_fence"], &section))
	require.ElementsMatch(t, []string{"counts"}, counterKeys(section), "counts only; no levels half")

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(section["counts"], &fields))
	require.Len(t, fields, 1, "counts must be an object with the one key, not an empty object")
	assert.Equal(t, "0", string(fields["rejected_total"]),
		"rejected_total must serialise as an explicit zero, never be elided by omitempty")
}

// fakeWatchdogSource returns a fixed snapshot. THREE DISTINCT VALUES and a
// REAL-SHAPED uuid key: equal values would hide a crossed field, and a key that
// is not a canonical uuid would not exercise the allow-list predicate that
// admits this map into a payload of integers.
type fakeWatchdogSource struct{ c WatchdogCounters }

func (f fakeWatchdogSource) CounterSnapshot() WatchdogCounters { return f.c }

func threeDistinctSweeps() WatchdogCounters {
	return WatchdogCounters{Counts: WatchdogCounts{
		SweptTotal:    37,
		SweptOverflow: 4,
		SweptByWorker: map[string]uint64{
			"00000000-0000-0000-0000-0000000000c8": 33,
		},
	}}
}

func TestServerCounters_ReportsTheWatchdogSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{Watchdog: fakeWatchdogSource{c: threeDistinctSweeps()}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Watchdog *struct {
			Counts map[string]any `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"watchdog"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Watchdog, "a wired section must be present")
	require.Nil(t, body.Watchdog.Levels,
		"COUNTS ONLY. The two candidate levels were len(swept_by_worker), which is the map restated, "+
			"and the 256 cap, which is a configured constant and belongs in a limits half nobody has "+
			"designed yet - putting it in levels now would mean moving it later.")

	// Key-set equality, not per-key assertions alone: a renamed key would decode
	// as a missing value and a per-key check would report zero.
	assert.ElementsMatch(t, []string{"swept_total", "swept_overflow", "swept_by_worker"},
		counterMapKeys(body.Watchdog.Counts))
	assert.Equal(t, float64(37), body.Watchdog.Counts["swept_total"])
	assert.Equal(t, float64(4), body.Watchdog.Counts["swept_overflow"])
	assert.Equal(t, map[string]any{"00000000-0000-0000-0000-0000000000c8": float64(33)},
		body.Watchdog.Counts["swept_by_worker"])
}

// TestWatchdogSweptByWorkerExemptionRejectsHostileShapes is the descent made
// executable.
//
// An exemption is shape-checked but NON-DESCENDING: both walks stop at an
// exempted path once the predicate passes. That is right for started_at, whose
// predicate examines the whole value. It is WRONG for a container - a jsonOK
// that merely accepted map[string]any would leave every key and every value
// uninspected, which is the total exemption the predicate mechanism replaced,
// re-entered through the predicate. Slice 1 proved that is not theoretical: a
// map[string]string at an exempted path, with a newline-injected RTL-override
// key and an IP-address value, passed both guards with zero failures.
//
// So this table is the exemption's real content. Each row is a value that must
// NOT be admitted, and the positive rows prove the predicate is not simply
// `false`.
func TestWatchdogSweptByWorkerExemptionRejectsHostileShapes(t *testing.T) {
	ex, ok := counterPayloadAllowList["watchdog.counts.swept_by_worker"]
	require.True(t, ok, "the watchdog map must be admitted by an ARGUED exemption, not by a walk that "+
		"stopped looking")

	tooMany := map[string]any{}
	for i := 0; i <= WatchdogSweptWorkerMax; i++ {
		tooMany[fmt.Sprintf("00000000-0000-0000-0000-%012x", i)] = json.Number("1")
	}
	require.Len(t, tooMany, WatchdogSweptWorkerMax+1)

	atCap := map[string]any{}
	for i := 0; i < WatchdogSweptWorkerMax; i++ {
		atCap[fmt.Sprintf("00000000-0000-0000-0000-%012x", i)] = json.Number("1")
	}

	cases := []struct {
		name string
		v    any
		want bool
	}{
		// The hostile rows come FIRST. A poisoned input placed last cannot
		// detect an early-exit mutation.
		{"a key that is not a uuid", map[string]any{
			"worker-one": json.Number("1")}, false},
		{"a uuid with an injected newline", map[string]any{
			"00000000-0000-0000-0000-0000000000c8\n10.0.0.7": json.Number("1")}, false},
		{"an uppercase uuid", map[string]any{
			"00000000-0000-0000-0000-0000000000C8": json.Number("1")}, false},
		{"a hostname-shaped key", map[string]any{
			"build-agent-07.corp.example": json.Number("1")}, false},
		{"a string value", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": "33"}, false},
		{"a negative value", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": json.Number("-1")}, false},
		{"a nested object as a value", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": map[string]any{"n": json.Number("1")}}, false},
		{"one over the cap", tooMany, false},
		{"nil (a nil Go map serialises as null)", nil, false},
		{"not an object at all", json.Number("3"), false},

		{"a canonical uuid key with a count", map[string]any{
			"00000000-0000-0000-0000-0000000000c8": json.Number("33")}, true},
		{"empty", map[string]any{}, true},
		{"exactly at the cap", atCap, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ex.jsonOK(c.v))
		})
	}

	// The type half. A cap cannot be read off a type, so typeOK checks the only
	// thing a type carries: that the container is a map of string to unsigned.
	assert.False(t, ex.typeOK(reflect.TypeOf(map[string]string{})),
		"a map of strings at this path is where an address or a hostname gets in")
	assert.False(t, ex.typeOK(reflect.TypeOf(map[string]int64{})),
		"signed values are inadmissible everywhere in this payload")
	assert.False(t, ex.typeOK(reflect.TypeOf("")))
	assert.True(t, ex.typeOK(reflect.TypeOf(map[string]uint64{})))
}
