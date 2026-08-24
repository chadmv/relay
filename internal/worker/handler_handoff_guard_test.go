package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestFinishRegisterHandsOffOwnershipInsideTheWindow is a STRUCTURAL guard over
// the one load-bearing line of the ownership handoff, and it is deliberately
// untagged so it runs in the lane CI actually executes: .github/workflows/go-ci.yml
// runs `go test -race ./...` with no -tags integration.
//
// WHY A GUARD AND NOT A BEHAVIOURAL TEST. Every test in this package that drives
// a SUCCESSFUL registration is //go:build integration, and the default-lane
// fixture structurally cannot drive one: applyInventory opens a transaction on
// the concrete *pgxpool.Pool unconditionally, so a pool-less stub panics one
// statement past the reconcile. The flag's two failure modes therefore have no
// default-lane behavioural witness at all, and both are fleet-scale:
//
//   - Never set, and every SUCCESSFUL registration releases the generation it
//     just took. The worker flips 'offline' the instant it comes online,
//     Metrics.Clear wipes the entry Activate just created, and a grace timer is
//     armed against a live agent that requeues all of its running tasks a grace
//     window later.
//   - Set too early, and the RegisterResponse-send arm of the strand reopens -
//     which only the integration lane covers.
//
// WHAT IS ACTUALLY PINNED IS A RANGE, because that is what the code needs and
// claiming more would be false. The flag must be flipped after the send has
// succeeded and after the sender is in the registry, and before the function
// returns nil. Anywhere in that range is semantically identical - the deferred
// closure reads the flag after the return value is evaluated, so "too late" is
// not reachable by moving the statement. The value of pinning the range is that
// a fallible statement inserted anywhere inside it is caught by the release.
//
// NOTHING HERE MATCHES SOURCE TEXT. The flag is not named by this test: it is
// whatever identifier the deferred release closure guards on, so a rename moves
// the guard with the code instead of defeating it. The anchors are likewise
// derived - the stream parameter is found by its type, the registry call by its
// receiver, and the success return by its final result being the predeclared
// nil.
func TestFinishRegisterHandsOffOwnershipInsideTheWindow(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "handler.go", nil, 0)
	if err != nil {
		t.Fatalf("parse handler.go: %v", err)
	}

	fn := findFuncDecl(file, "finishRegister")
	if fn == nil {
		t.Fatal("finishRegister is gone from handler.go; the ownership handoff this guard covers " +
			"cannot be located, so nothing is holding it")
	}

	flag := handoffFlagIdent(t, fn)
	streamParam := paramNamedByType(fn, "AgentService_ConnectServer")
	if streamParam == "" {
		t.Fatal("finishRegister no longer takes the gRPC stream, so the send this guard orders " +
			"against cannot be located")
	}

	// The lower bounds of the window: the RegisterResponse send, and the moment
	// the sender becomes reachable by other goroutines.
	sendPos := onlyCallOnReceiver(t, fn, "the gRPC stream", func(sel *ast.SelectorExpr) bool {
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == streamParam
	})
	registerPos := onlyCallOnReceiver(t, fn, "the worker registry", func(sel *ast.SelectorExpr) bool {
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		recv, ok := inner.X.(*ast.Ident)
		return ok && recv.Name == "h" && inner.Sel.Name == "registry"
	})

	// The upper bound: the one return that reports success.
	var successReturn token.Pos
	var successReturns int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			return true
		}
		if id, ok := ret.Results[len(ret.Results)-1].(*ast.Ident); ok && id.Name == "nil" {
			successReturns++
			successReturn = ret.Pos()
		}
		return true
	})
	if successReturns != 1 {
		t.Fatalf("finishRegister has %d returns whose last result is nil; the handoff window closes "+
			"at the success return, and with more than one there is no single position to order "+
			"against - each would need its own handoff", successReturns)
	}

	// The flag itself: initialised false, flipped true exactly once.
	var setTrue []token.Pos
	var initFalse int
	var otherWrites int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name != flag {
				continue
			}
			var rhs string
			if i < len(as.Rhs) {
				if r, ok := as.Rhs[i].(*ast.Ident); ok {
					rhs = r.Name
				}
			}
			switch {
			case as.Tok == token.DEFINE && rhs == "false":
				initFalse++
			case as.Tok == token.ASSIGN && rhs == "true":
				setTrue = append(setTrue, as.Pos())
			default:
				otherWrites++
			}
		}
		return true
	})

	if initFalse != 1 {
		t.Fatalf("%s is initialised to false %d times; the deferred release runs on every exit and "+
			"reads this flag, so an unset or re-set initialiser decides the outcome of paths this "+
			"guard cannot see", flag, initFalse)
	}
	if otherWrites != 0 {
		t.Fatalf("%s is written %d times with something other than the literal true; the deferred "+
			"release is a two-valued decision and a computed value makes the outcome depend on state "+
			"this guard cannot order", flag, otherWrites)
	}
	if len(setTrue) != 1 {
		t.Fatalf("%s is set to true %d times, not once. Every one of the %d exits from this function "+
			"runs the deferred release, and it releases the generation unless this flag says a live "+
			"connection has taken ownership of it. With none, a SUCCESSFUL registration marks its own "+
			"worker offline, wipes its metrics entry and arms a grace timer that requeues the running "+
			"tasks of a healthy agent. With several, which one decided the outcome depends on the "+
			"path taken.", flag, len(setTrue), successReturns)
	}
	handoff := setTrue[0]

	if handoff < sendPos {
		t.Fatalf("%s is set before the RegisterResponse is sent (%s vs %s). A send failure returns an "+
			"error from a generation that RegisterWorkerConnection has already acquired - the worker "+
			"row is 'online' at a live epoch, the previous disconnect's requeue timer was discarded, "+
			"and the deferred release is the only thing that ends it.",
			flag, fset.Position(handoff), fset.Position(sendPos))
	}
	if handoff < registerPos {
		t.Fatalf("%s is set before the sender is registered (%s vs %s). Ownership passes to Connect's "+
			"teardown defer, and that defer identifies what it owns by the registry entry - so until "+
			"the entry exists there is nothing for it to claim.",
			flag, fset.Position(handoff), fset.Position(registerPos))
	}
	if handoff > successReturn {
		t.Fatalf("%s is set after the success return at %s, so on the success path it is never set at "+
			"all and the deferred release runs against a live connection",
			flag, fset.Position(successReturn))
	}
}

// handoffFlagIdent returns the identifier the deferred release closure guards
// on. Deriving it from the defer rather than naming it is what keeps this guard
// attached to the mechanism instead of to a spelling.
func handoffFlagIdent(t *testing.T, fn *ast.FuncDecl) string {
	t.Helper()
	var found []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		def, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		lit, ok := def.Call.Fun.(*ast.FuncLit)
		if !ok {
			return true
		}
		ast.Inspect(lit.Body, func(inner ast.Node) bool {
			ifStmt, ok := inner.(*ast.IfStmt)
			if !ok || !callsMethodNamed(ifStmt.Body, "releaseWorkerGeneration") {
				return true
			}
			unary, ok := ifStmt.Cond.(*ast.UnaryExpr)
			if !ok || unary.Op != token.NOT {
				return true
			}
			if id, ok := unary.X.(*ast.Ident); ok {
				found = append(found, id.Name)
			}
			return true
		})
		return true
	})
	if len(found) != 1 {
		t.Fatalf("finishRegister has %d deferred releases guarded by a single negated flag, not one. "+
			"The acquisition made by RegisterWorkerConnection is ended by exactly this construct on "+
			"every failing exit; without it the worker stays 'online' at a live epoch with no "+
			"connection behind it, and with more than one the release is no longer a single decision.",
			len(found))
	}
	return found[0]
}

func callsMethodNamed(n ast.Node, name string) bool {
	var hit bool
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			hit = true
		}
		return true
	})
	return hit
}

// onlyCallOnReceiver returns the position of the single call in fn matching
// match. More than one is a failure rather than a "take the first": the guard
// orders against this position, and with several the order it enforces would be
// silently partial.
func onlyCallOnReceiver(t *testing.T, fn *ast.FuncDecl, describe string, match func(*ast.SelectorExpr) bool) token.Pos {
	t.Helper()
	var hits []token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && match(sel) {
			hits = append(hits, call.Pos())
		}
		return true
	})
	if len(hits) != 1 {
		t.Fatalf("finishRegister makes %d calls on %s; the ownership handoff is ordered against that "+
			"call, and any count but one leaves the order this guard enforces undefined",
			len(hits), describe)
	}
	return hits[0]
}

// paramNamedByType returns the name of fn's parameter whose type selector ends
// in suffix, or "" if there is no such parameter.
func paramNamedByType(fn *ast.FuncDecl, suffix string) string {
	for _, field := range fn.Type.Params.List {
		sel, ok := field.Type.(*ast.SelectorExpr)
		if !ok || !strings.HasSuffix(sel.Sel.Name, suffix) {
			continue
		}
		if len(field.Names) == 1 {
			return field.Names[0].Name
		}
	}
	return ""
}

// findFuncDecl returns the top-level function declaration named name.
func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
