package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// schedrunnerPkgPath is matched as an IMPORT PATH, never as an identifier
// spelling. See parseMainBodyAndPkgName for why that distinction is the point.
const schedrunnerPkgPath = "relay/internal/schedrunner"

// TestSchedrunnerStartupSweepIsWiredInOrderByMain pins the THREE properties that
// make ValidateStoredSpecsOnStartup's placement correct, because the ORDERING is
// the entire correctness argument for that placement and none of it is
// observable from the schedrunner package's own tests.
//
// Deleting the call from main leaves all 22 packages green. That was measured,
// not assumed, and it is the defect this guard exists to catch:
//
//  1. main calls schedrunner.ValidateStoredSpecsOnStartup at all.
//  2. It calls it AFTER schedrunner.ReconcileOnStartup. They commute for
//     correctness - the sweep never touches next_run_at and reconcile never
//     touches the failure columns - but reconcile is what the RUNNER needs to be
//     correct, so a purely diagnostic pass must not delay it.
//  3. It calls it BEFORE the "go schedrunner.NewRunner(...).Run(ctx)" goroutine
//     starts. THIS IS THE LOAD-BEARING ONE. The sweep and TickOnce's fire path
//     write the same two columns on the same rows, and the sweep takes no lock.
//     Its safety argument is entirely "nothing else is running yet": with the
//     runner already started, a row whose fire succeeds between the sweep's LIST
//     and its UPDATE gets its fresh clear stamped back over with a stale
//     failure.
//
// "THE CALL APPEARS SOMEWHERE IN MAIN" IS THE WEAK VERSION OF THIS GUARD and it
// would pass while property 3 was violated, so the statement INDEX within main's
// body is what is compared, not mere presence.
//
// AND INDEX ORDER ALONE IS ITSELF TOO WEAK, which is the second trap. A call
// moved into a "go func() { ... }()" at an earlier index keeps its index and
// destroys the no-lock argument completely, because it then runs concurrently
// with the runner it textually precedes. So each synchronous call is
// additionally required NOT to sit inside a func literal, a go or a defer. That
// check is written against the shape of the DEFECT (concurrency) rather than the
// shape of the mutation that first motivated the guard (a moved line).
func TestSchedrunnerStartupSweepIsWiredInOrderByMain(t *testing.T) {
	body, pkg := parseMainBodyAndPkgName(t, schedrunnerPkgPath)

	reconcile := findPkgCalls(body, pkg, "ReconcileOnStartup")
	sweep := findPkgCalls(body, pkg, "ValidateStoredSpecsOnStartup")
	runner := findPkgCalls(body, pkg, "NewRunner")

	// EXACTLY ONE OF EACH. With two calls the last one decides and "before" and
	// "after" stop being well defined, so the guard would be comparing an index
	// it cannot justify.
	require.Len(t, sweep, 1,
		"main's body must call %s.ValidateStoredSpecsOnStartup exactly once. Zero means a schedule broken "+
			"by a retroactive validation change stays invisible until its next fire, which for @monthly is "+
			"up to a month; the whole sweep exists to close that. Found %d.", pkg, len(sweep))
	require.Len(t, reconcile, 1,
		"main's body must call %s.ReconcileOnStartup exactly once; the sweep's position is stated "+
			"relative to it. Found %d.", pkg, len(reconcile))
	require.Len(t, runner, 1,
		"main's body must construct %s.NewRunner exactly once; the sweep's position is stated relative "+
			"to it. Found %d.", pkg, len(runner))

	// PROPERTY 3, first part: the runner really is started by a go statement. If
	// it stopped being one, this guard's "before the goroutine" phrasing would be
	// describing something that no longer exists, and it must say so loudly
	// rather than keep passing.
	_, isGo := body.List[runner[0].stmt].(*ast.GoStmt)
	require.True(t, isGo,
		"the statement starting %s.NewRunner(...).Run is no longer a go statement, so the concurrency "+
			"boundary this guard orders the sweep against has moved. Re-derive the ordering argument.", pkg)

	// PROPERTY 2.
	require.Greater(t, sweep[0].stmt, reconcile[0].stmt,
		"%s.ValidateStoredSpecsOnStartup must run AFTER ReconcileOnStartup (sweep at statement %d, "+
			"reconcile at %d). Reconcile is what the runner needs to be correct; a purely diagnostic pass "+
			"must not delay it.", pkg, sweep[0].stmt, reconcile[0].stmt)

	// PROPERTY 3, second part.
	require.Less(t, sweep[0].stmt, runner[0].stmt,
		"%s.ValidateStoredSpecsOnStartup must run BEFORE the go ...NewRunner(...).Run(ctx) goroutine "+
			"(sweep at statement %d, runner at %d). The sweep takes NO LOCK; its entire safety argument is "+
			"that nothing else is running yet. With the runner already started, a row whose fire succeeds "+
			"between the sweep's LIST and its UPDATE has its fresh clear stamped back over with a stale "+
			"failure.", pkg, sweep[0].stmt, runner[0].stmt)

	// PROPERTY 3, third part - the one an index comparison cannot see.
	require.False(t, sweep[0].async,
		"%s.ValidateStoredSpecsOnStartup sits inside a func literal, a go or a defer. Its statement "+
			"index still reads as 'before the runner', but it would run CONCURRENTLY with the runner and "+
			"the no-lock argument collapses. The sweep must complete before anything else can write those "+
			"columns.", pkg)
	require.False(t, reconcile[0].async,
		"%s.ReconcileOnStartup sits inside a func literal, a go or a defer, so the never-catch-up "+
			"advance is no longer guaranteed to finish before the runner starts ticking.", pkg)
}

// callHit is one call site, located by the index of the top-level statement of
// main's body that contains it.
type callHit struct {
	stmt  int
	async bool
}

// parseMainBodyAndPkgName returns func main's body from main.go together with
// the local name bound to importPath in that file.
//
// RESOLVING THE PATH IS NOT DECORATION. A guard that hardcodes the identifier
// "schedrunner" is checking a SPELLING, and a spelling is walkable past: any
// same-named symbol reachable in cmd/relay-server - a different package aliased
// to that name, or a local no-op shim - satisfies it while calling nothing real.
// Binding the name from the import path instead means the guard follows the
// package it actually cares about, and keeps working across a rename of the
// import alias, which is a legitimate refactor it must not turn into a false
// alarm.
func parseMainBodyAndPkgName(t *testing.T, importPath string) (*ast.BlockStmt, string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var names []string
	for _, im := range file.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil || p != importPath {
			continue
		}
		if im.Name == nil {
			names = append(names, "schedrunner") // package name == last path element
			continue
		}
		names = append(names, im.Name.Name)
	}
	require.Len(t, names, 1,
		"main.go must import %q exactly once so this guard can bind its local name. Found %v.",
		importPath, names)
	require.NotEqual(t, "_", names[0],
		"%q is imported for side effects only, so main cannot be calling it", importPath)
	require.NotEqual(t, ".", names[0],
		"%q is dot-imported, so its calls carry no package selector and this guard cannot see them",
		importPath)

	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "main" || fd.Recv != nil || fd.Body == nil {
			continue
		}
		return fd.Body, names[0]
	}
	require.FailNow(t, "main.go no longer declares func main with a body")
	return nil, ""
}

// findPkgCalls returns every call to pkg.fn inside body, tagged with the index of
// the top-level statement containing it and whether it is deferred, spawned or
// wrapped in a closure.
//
// Containment is decided by token.Pos range rather than by threading a flag
// through the walk: a node lies inside a func literal exactly when its position
// falls within that literal's extent, which is both simpler and harder to get
// subtly wrong than maintaining a stack across ast.Inspect's entry and exit
// callbacks.
func findPkgCalls(body *ast.BlockStmt, pkg, fn string) []callHit {
	type span struct{ from, to token.Pos }
	var hits []callHit
	for i, st := range body.List {
		var async []span
		ast.Inspect(st, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
				async = append(async, span{n.Pos(), n.End()})
			}
			return true
		})
		ast.Inspect(st, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != fn {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != pkg {
				return true
			}
			h := callHit{stmt: i}
			for _, s := range async {
				if ce.Pos() >= s.from && ce.End() <= s.to {
					h.async = true
					break
				}
			}
			hits = append(hits, h)
			return true
		})
	}
	return hits
}
