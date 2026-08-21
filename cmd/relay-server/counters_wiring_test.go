package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/netlimit"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// Scan fills GetTokenWithUserRow BY DESTINATION TYPE rather than by column
// index, so a reordered or added column cannot silently authenticate a
// zero-valued (non-admin) user and turn every assertion below into a 403.
func (stubAdminRow) Scan(dest ...any) error {
	bools := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = true
			bools++
		case *string:
			*v = "counters-wiring"
		}
	}
	if bools != 1 {
		return fmt.Errorf("stubAdminDB: GetTokenWithUserRow has %d bool destinations, want exactly 1 "+
			"(user_is_admin); the row shape changed and this stub no longer authenticates an admin", bools)
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

// TestServerCountersIsWiredByMain is what remains of the syntactic guard, and it
// is deliberately THIN: three questions execution cannot answer from inside
// buildHTTPServer.
//
// The old version asked eight, six of them re-derived from individual evasions,
// and four evasions still got past it. Everything those six covered - a second
// literal, an empty literal, an explicit nil field, a field assignment on the
// following line, a named intermediate mutated before assignment, a mutation
// after the fact - is now impossible to write rather than checked for, because
// main does not hold the api.Server.
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

	// The grpcAdmission argument must derive from netlimit.Wrap through
	// unconditional assignments in main's body.
	var admissionArg ast.Expr
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
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "grpcAdmission" {
					admissionArg = kv.Value
				}
			}
		}
	}
	require.NotNil(t, admissionArg,
		"buildHTTPServer is called with no grpcAdmission field, so the only wired section is absent and "+
			"the endpoint reports started_at and nothing else - which reads exactly like an admission "+
			"control that has never refused anything")
	argIdent, ok := admissionArg.(*ast.Ident)
	require.True(t, ok,
		"grpcAdmission must be fed a plain identifier - the netlimit listener bound in main's body - "+
			"not %T", admissionArg)

	seen := map[string]bool{}
	queue := []string{argIdent.Name}
	reachesWrap := false
	for len(queue) > 0 && !reachesWrap {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if name == "Wrap" {
			reachesWrap = true
			break
		}
		queue = append(queue, from[name]...)
	}
	require.True(t, reachesWrap,
		"grpcAdmission is fed %q, which does not derive from netlimit.Wrap through an UNCONDITIONAL "+
			"assignment in main's body. `var grpcLis *netlimit.Listener` assigned inside an if - the "+
			"natural shape for wrapping only when a cap is set - reaches the endpoint as a typed nil and "+
			"the section vanishes on every deployment that does not take the branch.", argIdent.Name)
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
