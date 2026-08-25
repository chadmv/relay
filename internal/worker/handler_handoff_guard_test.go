package worker

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestFinishRegisterHandsOffOwnershipInsideTheWindow is a STRUCTURAL guard over
// the one load-bearing line of the ownership handoff, and it is deliberately
// untagged so it runs in the lane CI actually executes: .github/workflows/go-ci.yml
// runs `go test -race ./...` with no -tags integration.
//
// WHY A GUARD AS WELL AS A BEHAVIOURAL TEST. There is now a behavioural witness:
// TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration
// drives a SUCCESSFUL registration in this lane and asserts the generation is
// released exactly once across the connection's life, and
// TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration
// covers the send arm. Both were impossible until Handler.pool became a
// txBeginner. Deleting the flag, or flipping it too early, is caught by those
// two and this test's clauses for either were removed.
//
// WHAT NO RUNTIME TEST CAN SEE IS SOURCE POSITION AND SHAPE, and that is what
// survives here. A flip wrapped in `if h.Metrics != nil { ... }` sits at a
// perfectly legal position and is INVISIBLE TO A RUNTIME TEST - which is the
// claim worth making, and is not the same as "broken in production". It is not
// broken in production: cmd/relay-server/main.go:143 sets Metrics
// unconditionally right after construction, so the wrapped flip fires there
// too. What the wrap does is make the flag depend on a field no fixture in
// either lane varies, so the day Metrics becomes optional it is a live defect
// and nothing runtime would have noticed on the way there. Measured: it passes
// the behavioural test and fails this one. So does an error return added below
// the flip, which is a claim about
// statements that do not exist yet and which no runtime test can ever assert.
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
// instead of defeating it, and the success return is derived the same way - by
// its final result being the predeclared nil. The remaining anchors ARE source
// text: the file name, "finishRegister", "releaseWorkerGeneration", and at the
// registry anchor the receiver "h" and the field "registry". Renaming any
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
// condition no structural test can evaluate AND no fixture in either lane
// varies, so no runtime test notices it either. (The default lane CAN now drive
// a successful registration - see the two tests named above - which is why the
// residual claim is about unvaried state rather than about an unreachable path.)
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
	// The lower bound of the window: the moment the sender becomes reachable by
	// other goroutines. The RegisterResponse send used to be a second anchor here;
	// ordering against it is now covered behaviourally by
	// TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration.
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
	//
	// WRITES BY NAME ARE NOT THE WHOLE WRITE SET, and this comment used to claim
	// they were plus exactly one more. It said a local bool has "exactly one other
	// way to be written: through a pointer to it", counted address-of, and was
	// wrong: `(handedOff) = false` after the flip needs no pointer and no
	// indirection, it simply wraps the name in parens, which an *ast.Ident type
	// assertion does not see through. It was measured releasing the generation on
	// every SUCCESSFUL registration with `go vet` clean and the whole repo green.
	// `gofmt` does not normalise it away, and this tree has no fmt gate (CRLF makes
	// `gofmt -l` flag every file at baseline).
	//
	// So every expression site below is normalised with ast.Unparen first, and the
	// honest statement of what is checked is: writes are counted BY NAME after
	// dropping parens. That covers a parenthesised write and a closure writing the
	// flag by name - `defer func(){ handedOff = false }()` is already otherWrites,
	// because ast.Inspect descends into function literals. Shadowing is caught by
	// the initFalse count.
	//
	// AN ALIAS THROUGH A POINTER IS NO LONGER COUNTED HERE, deliberately.
	// `p := &handedOff` plus `*p = false` below the flip is now caught by
	// TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration,
	// in four places at once - a grace fire, a non-empty statement log, a second
	// worker event and a teardown release count of two - which is what the deleted
	// clause's failure message described in words. Mutation M14 confirmed it after
	// the deletion, not before.
	var setTrue []*ast.AssignStmt
	var initFalse int
	var otherWrites int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				id, ok := ast.Unparen(lhs).(*ast.Ident)
				if !ok || id.Name != flag {
					continue
				}
				var rhs string
				if i < len(v.Rhs) {
					if r, ok := ast.Unparen(v.Rhs[i]).(*ast.Ident); ok {
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
			"wrap is the live hazard - `if h.Metrics != nil { %s = true }` compiles, and it makes the "+
			"handoff depend on a field NEITHER LANE VARIES. It is not broken today: NewHandler and "+
			"NewHandlerWithGrace leave Metrics nil, but cmd/relay-server/main.go:143 sets it "+
			"unconditionally, so a real server flips the flag and the default-lane fixture (which must "+
			"set Metrics, to assert Activate) flips it too. The hazard is that the day Metrics becomes "+
			"optional, a SUCCESSFUL registration takes the deferred release - its own worker marked "+
			"offline, its metrics entry wiped, a grace timer armed that requeues a healthy agent's "+
			"running tasks - and no runtime test in either lane would have noticed on the way there.",
			flag, fset.Position(setTrue[0].Pos()), flag)
	}
	handoff := setTrue[0].Pos()

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
	//
	// AND ADJACENCY ONLY MEANS THAT IF THE REGISTER CALL IS ITSELF THE STATEMENT
	// IT IS INDEXED AT. stmtIndexContaining answers "which body statement's source
	// range CONTAINS this call", which is the right question for locating the
	// call and the wrong one for anchoring against it: the moment
	// h.registry.Register stops being a top-level ExprStmt and moves inside a
	// compound body statement, regIdx names the ENCLOSING statement and
	// `List[regIdx+1] == setTrue[0]` still holds - with arbitrarily many fallible
	// statements sitting inside that compound statement, after the Register and
	// before the flip, which is exactly the region this check exists to forbid.
	// Measured: a bare block, an `if`/`switch` with the Register in its init, and
	// the plausible-refactor shape `if h.registry != nil { Register; if err := ...
	// { return } }` all passed this test and `go vet` and the whole package before
	// the identity clause below. So does `if h.grace != nil { Register }` with the
	// flip left unconditional, which strands the other direction: on the skipped
	// branch nothing is registered, the flip waives the deferred release anyway,
	// and Connect arms teardownConnection with a sender UnregisterIf does not own.
	// This is the same containment-vs-identity distinction isCallTo draws for the
	// deferred closure's body; it just has to be drawn at this anchor too.
	regIdx := stmtIndexContaining(fn.Body, registerPos)
	if regIdx < 0 {
		t.Fatalf("the registry.Register call at %s is not inside any statement of finishRegister's own "+
			"body, so the handoff cannot be positioned against it", fset.Position(registerPos))
	}
	if es, ok := fn.Body.List[regIdx].(*ast.ExprStmt); !ok || es.X.Pos() != registerPos {
		t.Fatalf("the registry.Register call at %s is nested inside a compound statement of "+
			"finishRegister's body (%s) rather than being a statement of that body itself. The "+
			"adjacency check below asks whether the flip is the NEXT body statement, and against an "+
			"enclosing statement that question no longer means \"with nothing in between\": every "+
			"statement sharing the wrapper with the Register call sits after the sender is published "+
			"and before the flip, which is the unguarded region this whole check exists to forbid. "+
			"Register on its own line, and let the flip follow it.",
			fset.Position(registerPos), fset.Position(fn.Body.List[regIdx].Pos()))
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
	id, ok := ast.Unparen(spec.Values[i]).(*ast.Ident)
	return ok && id.Name == "false"
}

// stmtIndexContaining returns the index of body's own statement whose source
// range contains pos, or -1 if pos falls outside all of them. Position is the
// right tool for LOCATING the call, and identity is not: the call being located
// sits inside a statement rather than being one.
//
// Containment is not enough for ANCHORING against it, though, and the caller has
// to close that gap itself. If the call is nested in a compound statement this
// returns the enclosing statement's index, so "the next statement" stops meaning
// "with nothing in between" - see the identity clause at the register anchor.
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
//   - THE CLOSURE RELEASES EXACTLY ONCE, which is the "mutually exclusive" half.
//     An `else` arm that also releases fires on the SUCCESS path, where the
//     fence matches: a live agent is published 'offline', the metrics entry
//     Activate just created is wiped, and a grace timer requeues that healthy
//     agent's running tasks a window later. There is no longer a dedicated
//     COUNT clause for this - the case-1/case-2 shape checks below admit only a
//     body with the release at one fixed place, so a second release cannot hide
//     inside an accepted body, and mutation confirms it.
//   - THE CLOSURE BODY IS EXACTLY THE GUARD CONSTRUCT, with the release at a
//     fixed place inside it. Anything looser admits a skip on a condition this
//     test cannot evaluate: `if h.AllowAutoEnroll { return }` ahead of the
//     release is false in every DEFAULT-LANE fixture and true wherever an
//     operator set RELAY_ALLOW_AUTO_ENROLL, so every failed registration on such
//     a server would release nothing while the whole package stayed green.
//     Seven integration-lane fixtures DO set it true - handler_auth_test.go:367,
//     :403, :467, :519, handler_tasklog_integration_test.go:543 and
//     handler_taskstatus_integration_test.go:445, :788 - and the mutation is
//     unreddened there too, which is the stronger statement and the one that
//     earns this clause: six drive a SUCCESSFUL registration, where the deferred
//     release does not run at all, and the seventh (AutoEnrollRefusesRevoked
//     Worker) is refused inside autoEnrollAndRegister before finishRegister is
//     entered, so the closure never exists. This guard is the mutation's only
//     witness in EITHER lane.
//     `if h.pool != nil { return }` used to be the example here, on the grounds
//     that newStrandHandler left pool nil; that is no longer true - both
//     default-lane fixtures now carry a fake pool, and mutation M13 confirmed
//     the edit reddens them as well as this clause. The general shape is what
//     this clause is for, not that one instance.
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
	// Rejections below share one message. The two accepted forms are spelled out
	// in full because the useful thing to know at a failure is what the closure
	// must look like, not which clause of this function noticed.
	reject := func(reason string) {
		t.Fatalf("finishRegister's deferred release closure is not a bare handoff guard: %s.\n"+
			"Exactly two forms are accepted, and each must be the closure's WHOLE body:\n"+
			"    defer func() { if !flag { h.%s(...) } }()\n"+
			"    defer func() { if flag { return }; h.%s(...) }()\n"+
			"The body is pinned that tightly because any additional statement can skip the release on "+
			"a condition this test cannot evaluate and no fixture in EITHER lane varies - so no runtime "+
			"test notices it either, which is why this clause has no behavioural successor. Work that "+
			"genuinely belongs on this path belongs inside %s, "+
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
		// The length test comes FIRST because it is the one that makes the index
		// below safe. Written after it, `if handedOff { }` followed by the release
		// - a total defeat that releases the generation on every SUCCESSFUL
		// registration - died as `panic: index out of range [0] with length 0`
		// instead of with the message that says what was lost. Fail-closed either
		// way, but a panic reports the guard as broken rather than the code.
		if len(guard.Body.List) != 1 {
			reject("the early-return branch is not exactly a bare `return`")
		}
		ret, isRet := guard.Body.List[0].(*ast.ReturnStmt)
		if !isRet || len(ret.Results) != 0 {
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
// distinction is the whole point: `if h.AllowAutoEnroll { return }` followed by
// the release contains the call, and containment is what a recursive check
// accepts.
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
