package main

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStartupValidationDeadlineIsWiredAndLoggedByMain pins the two jobs this
// slice gives main, neither of which any executable test can see.
//
// (1) THE THIRD ARGUMENT IS THE OPERATOR'S BUDGET AND NOT ANOTHER DURATION.
// main holds several time.Duration locals and every one of them compiles in that
// position, so the type says nothing about which number arrived. The chain is
// required to reach parseStartupValidationDeadline AND to mention
// RELAY_STARTUP_VALIDATION_DEADLINE, and to mention no other RELAY_ name, so an
// argument built from two controls is RED rather than passing on the half it
// happens to name.
//
// (2) THE OUTCOME LINE IS PRINTED AT ALL. Deleting the result line leaves every
// package green and the boot silent about a truncated pass, which is the defect
// the whole slice exists to close.
//
// IT DOES NOT OWN THE CALL'S PLACEMENT. TestSchedrunnerStartupSweepIsWiredInOrderByMain
// owns that; the single-call requirement here is only so there is one argument
// list to index.
//
// WHAT IT CANNOT SEE, so its name is not read as more than it checks: a value
// laundered through an intermediate local is followed, a value TRANSFORMED on the
// way is not; and a result line computed and then discarded satisfies it.
func TestStartupValidationDeadlineIsWiredAndLoggedByMain(t *testing.T) {
	body, pkg := parseMainBodyAndPkgName(t, schedrunnerPkgPath)

	// from[name] = identifiers AND unquoted string literals the RHS mentions,
	// collected only from assignments that are DIRECT children of main's body, so
	// a parse moved inside an if reaches nothing.
	from := map[string][]string{}
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		var rhs []string
		for _, e := range as.Rhs {
			ast.Inspect(e, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					rhs = append(rhs, id.Name)
				}
				if bl, ok := m.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if unquoted, err := strconv.Unquote(bl.Value); err == nil {
						rhs = append(rhs, unquoted)
					}
				}
				return true
			})
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				from[id.Name] = append(from[id.Name], rhs...)
			}
		}
	}

	var sweepCalls []*ast.CallExpr
	plainCalls := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := ce.Fun.(type) {
		case *ast.SelectorExpr:
			id, ok := fn.X.(*ast.Ident)
			if ok && id.Name == pkg && fn.Sel.Name == "ValidateStoredSpecsOnStartup" {
				sweepCalls = append(sweepCalls, ce)
			}
		case *ast.Ident:
			plainCalls[fn.Name]++
		}
		return true
	})

	require.Len(t, sweepCalls, 1,
		"main's body must call %s.ValidateStoredSpecsOnStartup exactly once so this guard has one "+
			"argument list to index. Found %d.", pkg, len(sweepCalls))
	call := sweepCalls[0]
	require.Len(t, call.Args, 3,
		"%s.ValidateStoredSpecsOnStartup is called with %d arguments; this positional check is written "+
			"against the 3-parameter signature (ctx, q, budget). If the signature changed, update the "+
			"position below - do not delete the check.", pkg, len(call.Args))

	id, isIdent := call.Args[2].(*ast.Ident)
	require.True(t, isIdent,
		"the budget argument must be a plain identifier so this guard can trace which env var it was "+
			"parsed from; got %T. A literal there is a hard-coded budget that "+
			"RELAY_STARTUP_VALIDATION_DEADLINE no longer controls.", call.Args[2])

	seen := map[string]bool{}
	queue := []string{id.Name}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		queue = append(queue, from[name]...)
	}
	var otherEnv []string
	for name := range seen {
		if strings.HasPrefix(name, "RELAY_") && name != "RELAY_STARTUP_VALIDATION_DEADLINE" {
			otherEnv = append(otherEnv, name)
		}
	}

	require.True(t, seen["parseStartupValidationDeadline"],
		"the budget argument is %q, which does not derive from parseStartupValidationDeadline through "+
			"an unconditional assignment in main's body. Crossing it with another duration local - "+
			"staleAfter, watchdogMargin, trailingLogWindow - compiles and leaves every package green "+
			"while the boot runs on some other control's number.", id.Name)
	require.True(t, seen["RELAY_STARTUP_VALIDATION_DEADLINE"],
		"the budget argument is %q, whose chain never mentions RELAY_STARTUP_VALIDATION_DEADLINE. Every "+
			"candidate local here is a time.Duration, so the env-var name is the only thing that says "+
			"WHICH duration arrived.", id.Name)
	require.Empty(t, otherEnv,
		"the budget argument is %q, whose chain reaches %v - another control's variable - so this guard "+
			"cannot say which number it carries.", id.Name, otherEnv)

	require.Equal(t, 1, plainCalls["startupValidationLines"],
		"main's body must call startupValidationLines exactly once. Zero means a truncated sweep says "+
			"NOTHING in the boot log, which is the lossy-aggregate defect this slice exists to close; "+
			"more than once means two outcome lines for one pass. Found %d.",
		plainCalls["startupValidationLines"])
	require.GreaterOrEqual(t, plainCalls["startupValidationDeadlineLine"], 1,
		"main's body must print the bounds line. The boot log between the dispatcher and "+
			"'HTTP listening on' is otherwise silent, so without it an operator watching a slow boot "+
			"sees nothing at all during the window this knob governs.")
}
