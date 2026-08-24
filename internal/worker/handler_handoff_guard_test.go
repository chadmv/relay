package worker

import (
	"fmt"
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
// WHAT IS PINNED IS A POINT; WHAT THE CODE NEEDS IS A RANGE. The semantic
// requirement is only that the flag be flipped after the send has succeeded and
// before the function returns nil. Every position in that range behaves
// identically today - the deferred closure reads the flag after the return value
// has been evaluated, so "too late" is not reachable by moving the statement -
// and mutation confirms it. This test nevertheless pins ONE position, the
// statement immediately after registry.Register, and the extra strictness is not
// decoration: each statement that comes to sit between registry.Register and the
// flip is a statement whose failure ends the DB generation correctly (the flag
// is still false) and STILL strands the *workerSender in the registry, because
// Connect arms `defer h.teardownConnection` only on a nil error. The
// infallible-below-the-flip check further down looks at returns positioned BELOW
// the flip only, so without adjacency the two checks would leave an unguarded
// region between them instead of meeting.
//
// THE FLAG IS NOT NAMED BY THIS TEST. It is whatever identifier the deferred
// release closure guards on, so renaming it moves the guard with the code
// instead of defeating it, and the stream parameter and the success return are
// derived the same way - by type, and by the final result being the predeclared
// nil. The remaining anchors ARE source text: the file name, "finishRegister",
// the "AgentService_ConnectServer" type suffix, "releaseWorkerGeneration", and
// at the registry anchor the receiver "h" and the field "registry". Renaming any
// of those makes this test fail rather than pass vacuously - it is brittleness,
// not a hole - but the message it fails with will describe a missing anchor
// rather than the rename that moved it.
//
// SPELLING IS NOT AN ANCHOR; SHAPE IS. Both ordinary ways of writing the guard
// are accepted - `if !flag { release }` and `if flag { return }` followed by the
// release - as are all three ordinary ways of declaring the flag false:
// `flag := false`, `var flag bool` and `var flag = false`. Failing any of those
// would mean reporting a structural loss that did not happen, with a message
// ("0 deferred releases", "initialised to false 0 times") that names the wrong
// thing. What IS constrained is that the deferred closure's body be exactly the
// guard construct and nothing more. handoffFlagIdent carries the argument for
// that, and it is the one place here where this test does dictate style on
// purpose: any additional statement in that closure can skip the release on a
// condition no structural test can evaluate, and the default lane cannot drive a
// successful registration to notice at runtime.
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
	rets := returnStmts(fn.Body)
	var successReturn token.Pos
	var successReturns int
	for _, ret := range rets {
		if returnsNil(ret) {
			successReturns++
			successReturn = ret.Pos()
		}
	}
	if successReturns != 1 {
		t.Fatalf("finishRegister has %d returns whose last result is nil; the handoff window closes "+
			"at the success return, and with more than one there is no single position to order "+
			"against - each would need its own handoff", successReturns)
	}

	// The flag itself: initialised false, flipped true exactly once.
	//
	// BOTH SPELLINGS OF "INITIALISED FALSE" COUNT. `flag := false`, `var flag
	// bool` and `var flag = false` are the same declaration, and a guard that
	// accepted only the first would fail an ordinary rewrite with "initialised to
	// false 0 times" - a message asserting a loss that did not happen. What
	// actually has to hold is that the flag is declared exactly once and starts
	// out false; how it is spelled is not this test's business.
	var setTrue []*ast.AssignStmt
	var initFalse int
	var otherWrites int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != flag {
					continue
				}
				var rhs string
				if i < len(v.Rhs) {
					if r, ok := v.Rhs[i].(*ast.Ident); ok {
						rhs = r.Name
					}
				}
				switch {
				case v.Tok == token.DEFINE && rhs == "false":
					initFalse++
				case v.Tok == token.ASSIGN && rhs == "true":
					setTrue = append(setTrue, v)
				default:
					otherWrites++
				}
			}
		case *ast.ValueSpec:
			for i, name := range v.Names {
				if name.Name != flag {
					continue
				}
				if declaresFalseBool(v, i) {
					initFalse++
				} else {
					otherWrites++
				}
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
	// The flip must be a DIRECT element of the function body, not merely
	// somewhere beneath it. Position alone cannot express "happens on every
	// path": a flip wrapped in an `if` sits at a position well inside the window
	// and still leaves the success path unflipped. Requiring a plain body
	// statement rejects that, a closure wrap and a `defer func(){}()` wrap
	// alike, without this test having to reason about what each wrapper does at
	// runtime.
	if !directBodyStmt(fn.Body, setTrue[0]) {
		t.Fatalf("%s is set to true at %s, but that assignment is nested inside another statement "+
			"rather than being a statement of finishRegister's own body. Every position check below "+
			"is about WHERE the flip is; this one is about whether it happens at all. A conditional "+
			"wrap is the live hazard - `if h.Metrics != nil { %s = true }` compiles, and h.Metrics is "+
			"nil for every handler NewHandler and NewHandlerWithGrace build, so a SUCCESSFUL "+
			"registration would take the deferred release: its own worker marked offline, its metrics "+
			"entry wiped, and a grace timer armed that requeues a healthy agent's running tasks.",
			flag, fset.Position(setTrue[0].Pos()), flag)
	}
	handoff := setTrue[0].Pos()

	if handoff < sendPos {
		t.Fatalf("%s is set before the RegisterResponse is sent (%s vs %s). A send failure returns an "+
			"error from a generation that RegisterWorkerConnection has already acquired - the worker "+
			"row is 'online' at a live epoch, the previous disconnect's requeue timer was discarded, "+
			"and the deferred release is the only thing that ends it.",
			flag, fset.Position(handoff), fset.Position(sendPos))
	}
	// The flip must be the statement IMMEDIATELY AFTER registry.Register, not
	// merely somewhere below it.
	//
	// AN ORDERING CHECK WOULD NOT BE ENOUGH, and the gap it leaves is not
	// cosmetic. A `handoff > registerPos` bound admits every position down to the
	// success return, and a fallible statement inserted in that drift is covered
	// by finishRegister's own deferred release (the flag is still false, so the DB
	// generation IS ended correctly) and by nothing at all for the registry:
	// Connect arms `defer h.teardownConnection(workerID, sender)` only on a nil
	// error, so the *workerSender that registry.Register just published stays in
	// the registry forever, reachable by the dispatcher, wrapping a stream whose
	// RPC has returned. The infallible-below-the-flip check further down cannot
	// see that statement either - it only looks at returns positioned BELOW the
	// flip. Adjacency is what makes the two checks meet with no unguarded region
	// between them.
	regIdx := stmtIndexContaining(fn.Body, registerPos)
	if regIdx < 0 {
		t.Fatalf("the registry.Register call at %s is not inside any statement of finishRegister's own "+
			"body, so the handoff cannot be positioned against it", fset.Position(registerPos))
	}
	if regIdx+1 >= len(fn.Body.List) || fn.Body.List[regIdx+1] != setTrue[0] {
		t.Fatalf("%s is set at %s, which is not the statement immediately after registry.Register at "+
			"%s. Moving the flip later is behaviour-preserving TODAY - the deferred closure reads the "+
			"flag after the return value is evaluated, and mutation confirms every position down to "+
			"the success return behaves identically - so this is not a report of a live defect. What "+
			"it stops is the drift: each statement that comes to sit between registry.Register and "+
			"this flip is a statement whose failure ends the DB generation correctly and still leaves "+
			"a live sender stranded in the registry, because Connect arms its teardown only on a nil "+
			"error. Keep the flip adjacent and that region cannot exist.",
			flag, fset.Position(handoff), fset.Position(registerPos))
	}
	if handoff > successReturn {
		t.Fatalf("%s is set after the success return at %s, so on the success path it is never set at "+
			"all and the deferred release runs against a live connection",
			flag, fset.Position(successReturn))
	}

	// Everything below the flip must stay infallible. handler.go states that as a
	// rule; this is what makes it a check.
	var lateErrorReturns []token.Pos
	for _, ret := range rets {
		if ret.Pos() > handoff && !returnsNil(ret) {
			lateErrorReturns = append(lateErrorReturns, ret.Pos())
		}
	}
	if len(lateErrorReturns) > 0 {
		t.Fatalf("finishRegister returns an error at %s, below the %s flip. That exit is covered by "+
			"NEITHER release and leaves worse state than the strand this guard closes: the sender is "+
			"already in the registry, the flag has waived this function's own deferred release, and "+
			"Connect arms `defer h.teardownConnection` only on a nil error - so the generation stays "+
			"unreleased, the worker row stays 'online' at a live epoch, and the send goroutine is "+
			"never Closed. A statement needed here must log and continue, as applyInventory does.",
			fset.Position(lateErrorReturns[0]), flag)
	}
}

// returnStmts returns every return that exits body's own function - nested
// function literals are skipped, because a return inside a closure exits the
// closure and is not part of finishRegister's exit set.
func returnStmts(body *ast.BlockStmt) []*ast.ReturnStmt {
	var out []*ast.ReturnStmt
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			out = append(out, v)
		}
		return true
	})
	return out
}

// returnsNil reports whether ret's last result is the predeclared nil, which is
// how finishRegister's success exit is told apart from its error exits. A bare
// `return` counts as an error exit: it cannot compile here today, and treating
// it as success would be the fail-open reading.
func returnsNil(ret *ast.ReturnStmt) bool {
	if len(ret.Results) == 0 {
		return false
	}
	id, ok := ret.Results[len(ret.Results)-1].(*ast.Ident)
	return ok && id.Name == "nil"
}

// directBodyStmt reports whether stmt is one of body's own statements, as
// opposed to being nested inside one of them. Identity comparison, not position
// comparison: a nested statement's position is inside the parent's range, so
// nothing about an offset can distinguish the two.
func directBodyStmt(body *ast.BlockStmt, stmt ast.Stmt) bool {
	for _, s := range body.List {
		if s == stmt {
			return true
		}
	}
	return false
}

// declaresFalseBool reports whether spec declares its i-th name as a bool that
// starts out false - either `var flag bool` with no initialiser, or an explicit
// `= false`. Anything else (another type, a computed initialiser) is a write
// this guard cannot order, and is counted as one.
func declaresFalseBool(spec *ast.ValueSpec, i int) bool {
	if len(spec.Values) == 0 {
		id, ok := spec.Type.(*ast.Ident)
		return ok && id.Name == "bool"
	}
	if i >= len(spec.Values) {
		return false
	}
	id, ok := spec.Values[i].(*ast.Ident)
	return ok && id.Name == "false"
}

// stmtIndexContaining returns the index of body's own statement whose source
// range contains pos, or -1 if pos falls outside all of them. Position is the
// right tool here and identity is not: the call being located sits INSIDE a
// statement rather than being one.
func stmtIndexContaining(body *ast.BlockStmt, pos token.Pos) int {
	for i, s := range body.List {
		if s.Pos() <= pos && pos < s.End() {
			return i
		}
	}
	return -1
}

// releaseMethod is the method the deferred closure must call to end the
// generation. It is source text: renaming the method makes this test fail
// rather than pass vacuously.
const releaseMethod = "releaseWorkerGeneration"

// handoffFlagIdent returns the identifier the deferred release closure guards
// on, after verifying that the closure IS that decision and nothing else.
// Deriving the name from the defer keeps this guard attached to the mechanism
// rather than to a spelling; verifying the shape is what makes the derivation
// worth anything.
//
// handler.go claims the two releases are "mutually exclusive by construction and
// neither can be skipped". Those are two claims, and each needs its own clause:
//
//   - THE DEFER IS A DIRECT STATEMENT of finishRegister's body, so it is armed on
//     every path. `if h.grace != nil { defer func(){...}() }` leaves the closure
//     verbatim and arms it for nobody.
//   - THE CLOSURE CALLS THE RELEASE EXACTLY ONCE, which is the "mutually
//     exclusive" half. An `else` arm that also releases fires on the SUCCESS
//     path, where the fence matches: a live agent is published 'offline', the
//     metrics entry Activate just created is wiped, and a grace timer requeues
//     that healthy agent's running tasks a window later.
//   - THE CLOSURE BODY IS EXACTLY THE GUARD CONSTRUCT, with the release at a
//     fixed place inside it. Anything looser admits a skip on a condition this
//     test cannot evaluate: `if h.pool != nil { return }` ahead of the release is
//     false in every default-lane fixture (newStrandHandler leaves pool nil
//     deliberately, and applyInventory's unconditional BeginTxFunc is why) and
//     true under main.go, so every failed registration would release nothing in
//     production while this whole package stayed green.
//
// BOTH SPELLINGS OF THE GUARD ARE ACCEPTED. `if !flag { release }` and
// `if flag { return }; release` are the same decision written two ordinary ways.
// Admitting one and failing the other with a message about a missing release
// would be dictating style while reporting a defect.
func handoffFlagIdent(t *testing.T, fn *ast.FuncDecl) string {
	t.Helper()

	// Candidates are selected by REACHING the release anywhere inside the
	// closure, never by matching the shape. A release moved somewhere the shape
	// checks reject has to fail those checks and say what it lost; selecting on
	// shape would drop it from the candidate set instead and report the opposite
	// of what happened - "no release at all".
	var candidates []*ast.DeferStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		def, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		lit, ok := def.Call.Fun.(*ast.FuncLit)
		if !ok {
			return true
		}
		if countCallsNamed(lit.Body, releaseMethod) > 0 {
			candidates = append(candidates, def)
		}
		return true
	})
	if len(candidates) != 1 {
		t.Fatalf("finishRegister defers %d closures that reach %s, not one. The acquisition made by "+
			"RegisterWorkerConnection is ended by exactly this construct on every failing exit; with "+
			"none the worker stays 'online' at a live epoch with no connection behind it and no timer "+
			"to clean up after it, and with several the release is no longer a single decision.",
			len(candidates), releaseMethod)
	}
	def := candidates[0]
	lit := def.Call.Fun.(*ast.FuncLit)

	if !directBodyStmt(fn.Body, def) {
		t.Fatalf("the deferred %s is nested inside another statement rather than being a statement of "+
			"finishRegister's own body, so it is armed only on the paths that reach it. The release "+
			"has to be armed in the same breath as the acquisition RegisterWorkerConnection already "+
			"made - a conditional arm leaves every failed registration it does not cover with the "+
			"worker row 'online' at a live epoch and the previous disconnect's requeue timer already "+
			"cancelled.", releaseMethod)
	}
	if n := countCallsNamed(lit.Body, releaseMethod); n != 1 {
		t.Fatalf("the deferred closure calls %s %d times, not once. The two releases - this one and "+
			"Connect's `defer h.teardownConnection` - are only mutually exclusive if this one fires on "+
			"exactly the not-handed-off branch. A second call reachable on the handed-off branch runs "+
			"against a SUCCESSFUL registration, where the epoch fence matches: the live agent is "+
			"published 'offline', its metrics entry is wiped, and a grace timer requeues its running "+
			"tasks a grace window later.", releaseMethod, n)
	}

	// Rejections below share one message. The two accepted forms are spelled out
	// in full because the useful thing to know at a failure is what the closure
	// must look like, not which clause of this function noticed.
	reject := func(reason string) {
		t.Fatalf("finishRegister's deferred release closure is not a bare handoff guard: %s.\n"+
			"Exactly two forms are accepted, and each must be the closure's WHOLE body:\n"+
			"    defer func() { if !flag { h.%s(...) } }()\n"+
			"    defer func() { if flag { return }; h.%s(...) }()\n"+
			"The body is pinned that tightly because any additional statement can skip the release on "+
			"a condition this test cannot evaluate, and the default lane cannot drive a successful "+
			"registration to notice. Work that genuinely belongs on this path belongs inside %s, "+
			"where the strand tests can see it.",
			reason, releaseMethod, releaseMethod, releaseMethod)
	}

	switch len(lit.Body.List) {
	case 1:
		// defer func() { if !flag { release } }()
		guard, ok := lit.Body.List[0].(*ast.IfStmt)
		if !ok {
			reject("its single statement is not an `if`")
		}
		if guard.Init != nil {
			reject("the guarding `if` carries an init statement, so the branch depends on something " +
				"evaluated inside the closure")
		}
		if guard.Else != nil {
			reject("the guarding `if` has an else arm, which by definition runs on the handed-off " +
				"branch")
		}
		unary, ok := guard.Cond.(*ast.UnaryExpr)
		if !ok || unary.Op != token.NOT {
			reject("the guarding `if` in the one-statement form does not test a negated identifier")
		}
		id, ok := unary.X.(*ast.Ident)
		if !ok {
			reject("the guarding `if` negates an expression rather than a plain flag identifier")
		}
		if len(guard.Body.List) != 1 || !isCallTo(guard.Body.List[0], releaseMethod) {
			reject("the guarded branch is not exactly one call to " + releaseMethod)
		}
		return id.Name

	case 2:
		// defer func() { if flag { return }; release }()
		guard, ok := lit.Body.List[0].(*ast.IfStmt)
		if !ok {
			reject("its first statement is not an `if`")
		}
		if guard.Init != nil {
			reject("the guarding `if` carries an init statement, so the branch depends on something " +
				"evaluated inside the closure")
		}
		if guard.Else != nil {
			reject("the guarding `if` in the early-return form has an else arm")
		}
		id, ok := guard.Cond.(*ast.Ident)
		if !ok {
			reject("the guarding `if` in the early-return form does not test a plain flag identifier")
		}
		ret, isRet := guard.Body.List[0].(*ast.ReturnStmt)
		if len(guard.Body.List) != 1 || !isRet || len(ret.Results) != 0 {
			reject("the early-return branch is not exactly a bare `return`")
		}
		if !isCallTo(lit.Body.List[1], releaseMethod) {
			reject("the statement after the early return is not exactly one call to " + releaseMethod)
		}
		return id.Name

	default:
		reject(fmt.Sprintf("its body has %d statements", len(lit.Body.List)))
	}
	return "" // unreachable: every branch above returns or fatals.
}

// countCallsNamed counts the calls to a method named name anywhere beneath n,
// nested positions included. Counting rather than reporting presence is what
// makes "exactly one release" checkable, and recursing is what makes a release
// hidden one level down visible to the shape checks rather than invisible to
// them.
func countCallsNamed(n ast.Node, name string) int {
	var hits int
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			hits++
		}
		return true
	})
	return hits
}

// isCallTo reports whether stmt IS an expression statement calling a method
// named name, as opposed to a statement that merely contains such a call. That
// distinction is the whole point: `if h.pool != nil { return }` followed by the
// release contains the call, and containment is what a recursive check accepts.
func isCallTo(stmt ast.Stmt, name string) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
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
