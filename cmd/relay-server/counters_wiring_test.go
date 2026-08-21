package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestServerCountersIsWiredByMain is a structural guard in the same spirit as
// TestGRPCAdmissionIsWiredByMain. Deleting `httpServer.Counters = ...` from
// main.go compiles and leaves `go test ./...` entirely green: internal/api keeps
// its own passing tests with a fake source, internal/netlimit keeps its own, and
// the endpoint permanently answers 200 with started_at and nothing else - which
// reads exactly like a server whose admission control has never refused
// anything. That is the whole bug.
//
// HEED THE RECORDED LESSON THAT A GUARD BUILT FROM A MUTATION KILL INHERITS THE
// MUTATION'S SHAPE, NOT THE DEFECT'S. The admission slice's three structural
// guards were each evaded on the first attempt - by an append, by an import
// alias, and by moving a field assignment to the next line. This one is written
// from the property "the value that reaches the SERVED api.Server's Counters
// field is a CounterSources literal whose GRPCAdmission derives from
// netlimit.Wrap", and it was attacked with five evasions before it was called
// done: deletion, an empty literal, an explicit nil field, a field assignment on
// the following line, and a named intermediate variable mutated before the
// assignment. Searching past those five for the SHAPE found three more, and one
// of them SURVIVED the first version of this guard: a conditional wrapper (see
// the unconditional check below). The other two were already covered - wiring a
// second api.Server and serving the original, and feeding a typed-nil
// *netlimit.Listener declared with var. One further evasion is blocked by the
// TYPE SYSTEM rather than by this guard and so is not checked here:
// GRPCAdmission: grpcRawLis does not compile, because a raw net.Listener has no
// Stats method.
func TestServerCountersIsWiredByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name -> identifiers its RHS mentions, so the walk can follow
	// `grpcLis := netlimit.Wrap(...)`.
	from := map[string][]string{}
	type selAssign struct {
		lhs  string
		rhs  ast.Expr
		stmt *ast.AssignStmt
	}
	var sels []selAssign

	// Statements that run UNCONDITIONALLY, once, in main's own body. Everything
	// else - an if, a switch, a for, a closure, a goroutine - is a place the
	// assignment can sit while never executing, or executing twice.
	unconditional := map[*ast.AssignStmt]bool{}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "main" || fd.Recv != nil || fd.Body == nil {
			continue
		}
		for _, st := range fd.Body.List {
			if as, ok := st.(*ast.AssignStmt); ok {
				unconditional[as] = true
			}
		}
	}
	require.NotEmpty(t, unconditional, "main.go no longer declares func main with a body of statements")

	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, l := range as.Lhs {
			var rhs ast.Expr
			if len(as.Lhs) == len(as.Rhs) {
				rhs = as.Rhs[i]
			}
			if id, ok := l.(*ast.Ident); ok {
				if rhs != nil {
					ast.Inspect(rhs, func(m ast.Node) bool {
						if x, ok := m.(*ast.Ident); ok {
							from[id.Name] = append(from[id.Name], x.Name)
						}
						return true
					})
				}
				continue
			}
			// A SELECTOR on the left. Keyed by the rendered path, so that
			// `httpServer.Counters.GRPCAdmission = nil` on the next line is
			// visible as a mutation rather than invisible as "not an *ast.Ident".
			sels = append(sels, selAssign{lhs: exprString(l), rhs: rhs, stmt: as})
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

	var target *selAssign
	var mutations []string
	for i := range sels {
		switch {
		case strings.HasSuffix(sels[i].lhs, ".Counters"):
			require.Nil(t, target,
				"main.go assigns to .Counters more than once. The last write decides and this guard cannot "+
					"say which one it is.")
			target = &sels[i]
		case strings.Contains(sels[i].lhs, ".Counters."):
			mutations = append(mutations, sels[i].lhs)
		}
	}
	require.NotNil(t, target,
		"main.go never assigns <httpServer>.Counters = api.CounterSources{...}. Deleting that one line "+
			"compiles, leaves every package green, and makes GET /v1/server/counters report an empty "+
			"payload forever - indistinguishable from a control that has stopped nothing.")
	require.Empty(t, mutations,
		"main.go mutates the counter sources after building them (%v). `httpServer.Counters.GRPCAdmission "+
			"= nil` on the following line compiles and silently unwires the section: this is the exact "+
			"evasion that beat the netlimit.Config guard one slice ago.", mutations)

	// NOT IN THE PLAN, AND IT IS AN EVASION THE PLAN'S FIVE MISSED. Wrapping the
	// assignment in `if os.Getenv("RELAY_ENABLE_COUNTERS") != "" { ... }` -
	// exactly the shape somebody reaches for to make a section opt-in - compiles,
	// leaves this whole guard green, and unwires the section on every deployment
	// that does not set the variable. Proved by running it. The assignment must
	// therefore be an unconditional statement in main's own body.
	require.True(t, unconditional[target.stmt],
		"the assignment to %s is nested inside a conditional, a loop, a closure or a goroutine rather "+
			"than sitting unconditionally in main's body. Every check below still passes in that shape, "+
			"and the section is silently absent on any run that does not take the branch.", target.lhs)

	base := strings.TrimSuffix(target.lhs, ".Counters")
	cl, ok := target.rhs.(*ast.CompositeLit)
	require.True(t, ok,
		"the value assigned to %s must be an api.CounterSources composite literal AT THE ASSIGNMENT SITE. "+
			"Building it into a named variable first re-opens the hole: `cs := api.CounterSources{...}; "+
			"cs.GRPCAdmission = nil; %s = cs` compiles and every other check here still passes.",
		target.lhs, target.lhs)
	require.Equal(t, "api.CounterSources", exprString(cl.Type))

	var src ast.Expr
	for _, e := range cl.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "GRPCAdmission" {
			src = kv.Value
		}
	}
	require.NotNil(t, src,
		"api.CounterSources is built with no GRPCAdmission field, so the only wired section is absent and "+
			"the endpoint reports an empty payload")
	srcIdent, ok := src.(*ast.Ident)
	require.True(t, ok,
		"GRPCAdmission must be fed a plain identifier - the netlimit listener - not %s. A nil or a stub "+
			"defined in this file compiles and reports zeros forever.", exprString(src))
	require.True(t, reaches(srcIdent.Name, "Wrap"),
		"GRPCAdmission is fed %q, which does not derive from netlimit.Wrap: the endpoint would report a "+
			"listener that is not the one serving the agent port.", srcIdent.Name)

	// The api.Server that received the sources must be the one whose Handler()
	// is served. Wiring a DIFFERENT api.Server value compiles and satisfies
	// every check above.
	handlerBase := ""
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		k, ok := kv.Key.(*ast.Ident)
		if !ok || k.Name != "Handler" {
			return true
		}
		if ce, ok := kv.Value.(*ast.CallExpr); ok {
			if s, ok := ce.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Handler" {
				handlerBase = exprString(s.X)
			}
		}
		return true
	})
	require.NotEmpty(t, handlerBase, "main.go no longer builds the http.Server from <server>.Handler()")
	require.Equal(t, handlerBase, base,
		"main.go wires the counter sources into %q but serves %q.Handler(). The endpoint would report an "+
			"empty payload with every check above satisfied.", base, handlerBase)
}
