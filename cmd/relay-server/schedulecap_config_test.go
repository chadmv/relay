package main

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/api"
)

// TestParseScheduleCap pins the three-outcome contract as BEHAVIOUR. Whatever
// README says about what the parser refuses must be phrased as what this table
// pins, never written from memory.
//
// THE ZERO ROW IS FIRST. A poisoned input placed after its target is read by
// neither the code nor the mutant: with 0 last, a mutant that returns early on
// the first row never reaches it.
func TestParseScheduleCap(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		msgPart string
	}{
		{"zero is NOT an off switch: it folds to the default and warns", "0", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"negative folds to the default and warns", "-1", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"unparseable folds to the default and warns", "abc", api.DefaultMaxSchedulesPerOwner, "is not a positive integer"},
		{"unset uses the default and says nothing", "", api.DefaultMaxSchedulesPerOwner, ""},
		{"a positive value is used as-is, silently", "7", 7, ""},
		{"a very large value is used as-is: that is the spelling for effectively-unbounded", "9999999999", 9999999999, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseScheduleCap("RELAY_MAX_SCHEDULES_PER_OWNER", tc.raw)
			require.Equal(t, tc.want, got)
			if tc.msgPart == "" {
				assert.Empty(t, msg)
				return
			}
			assert.Contains(t, msg, tc.msgPart)
			assert.Contains(t, msg, tc.raw,
				"a warning that does not name the ignored value leaves an operator believing they "+
					"tightened a bound they did not")
		})
	}
}

// TestScheduleCapLineIsUnconditionalAndNamesGrandfathering. An operator
// upgrading into a new refusal needs to see the number and the retroactivity
// without reading release notes.
func TestScheduleCapLineIsUnconditionalAndNamesGrandfathering(t *testing.T) {
	line := scheduleCapLine(100)
	assert.Contains(t, line, "100")
	assert.Contains(t, line, "keep",
		"the line must say existing owners over the cap KEEP their schedules; an operator who reads "+
			"this as a deletion has no way to find out otherwise before the deploy")
	assert.Contains(t, line, "per owner",
		"the line must not let an operator read the number as a fleet ceiling")
}

// TestMain_PassesTheScheduleCapItParsed closes the one gap the executed tests
// cannot reach: TestScheduleCap_TheThirdCreateIsRefusedAtACapOfTwo supplies the
// cap itself, so it says nothing about what main puts in the httpServerDeps
// literal. Zeroing that literal, or trading it for another of main's same-typed
// int locals, leaves this whole package green while the operator's number is
// ignored in production.
//
// A PARSER GUARD IS THE EXPENSIVE FALLBACK. It is here because nothing
// executable inside the process can see main's literal: main is not callable
// from a test, it opens a pool, and it can log.Fatalf before it reaches that
// line. The tests that drive real requests through buildHTTPServer's output
// cover every hop AFTER it.
//
// WHAT IT CANNOT SEE, so its name is not read as more than it checks: a value
// laundered through an intermediate local is followed, but a value TRANSFORMED
// on the way is not. It proves the wiring was not deleted, zeroed or crossed. It
// proves nothing about fidelity.
//
// DO NOT PASTE ANOTHER COPY OF THIS GUARD FAMILY. The row below is written in
// the shape prescribed by
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md - one
// row per wired field, with the function its value must derive from and the
// env-var literal that distinguishes it from a sibling of the same type - so a
// generalization lifts it without redesign. mainBodyOfPackage is shared with
// TestMain_PassesThePasswordChangeLimitItParsed rather than duplicated.
//
// IT DOES NOT CHECK A FATAL-ON-ERROR FOLLOW-UP, unlike its password sibling, and
// that is deliberate rather than an omission: parseScheduleCap returns no error.
// Its outcomes are pinned by TestParseScheduleCap instead.
func TestMain_PassesTheScheduleCapItParsed(t *testing.T) {
	body := mainBodyOfPackage(t)

	// from[name] = identifiers AND unquoted string literals its RHS mentions,
	// collected only from assignments that are DIRECT children of main's body,
	// so a parse moved inside an if reaches nothing. Arity-tolerant:
	// parseScheduleCap binds two names from one call.
	//
	// STRING LITERALS ARE COLLECTED ALONGSIDE IDENTIFIERS because the env-var
	// check below is the only thing distinguishing this int local from main's
	// same-typed siblings; nothing about its type or its derivation does.
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

	// Every identifier assigned anywhere in main's subtree - ifs, loops,
	// switches and closures included. Derivation alone is defeated by a later
	// assignment of zero inside an if.
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

	fields := map[string]ast.Expr{}
	calls := 0
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := ce.Fun.(*ast.Ident)
		if !ok || id.Name != "buildHTTPServer" {
			return true
		}
		calls++
		require.Len(t, ce.Args, 1)
		cl, ok := ce.Args[0].(*ast.CompositeLit)
		require.True(t, ok,
			"buildHTTPServer must be called with an httpServerDeps composite literal at the call "+
				"site, so every dependency is readable there")
		for _, e := range cl.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := kv.Key.(*ast.Ident); ok {
				fields[k.Name] = kv.Value
			}
		}
		return true
	})
	require.Equal(t, 1, calls,
		"main must call buildHTTPServer exactly once: called twice the last one decides and this "+
			"guard cannot say which")

	const field = "maxSchedulesPerOwner"
	const mustReach = "parseScheduleCap"
	const envVar = "RELAY_MAX_SCHEDULES_PER_OWNER"

	value, present := fields[field]
	require.True(t, present,
		"buildHTTPServer is called with no %s field, so %s is ignored in production while every "+
			"test in this package stays green", field, envVar)

	ident, isIdent := value.(*ast.Ident)
	require.True(t, isIdent,
		"httpServerDeps.%s must be fed a plain identifier, not %T. A literal there is a hard-coded "+
			"cap that %s no longer controls.", field, value, envVar)

	seen := map[string]bool{}
	queue := []string{ident.Name}
	reachedFn, reachedEnv := false, false
	var otherEnv []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		switch {
		case name == mustReach:
			reachedFn = true
		case name == envVar:
			reachedEnv = true
		case strings.HasPrefix(name, "RELAY_"):
			otherEnv = append(otherEnv, name)
		}
		queue = append(queue, from[name]...)
	}

	require.True(t, reachedFn,
		"httpServerDeps.%s is fed %q, which does not derive from %s through an unconditional "+
			"assignment in main's body. Crossing it with another int local - jobSubmitN, searchN, "+
			"loginN - compiles and leaves this package green.", field, ident.Name, mustReach)
	require.True(t, reachedEnv,
		"httpServerDeps.%s is fed %q, whose chain never mentions %s. Every candidate local here is "+
			"an int, so the env-var name is the only thing that says WHICH number arrived.",
		field, ident.Name, envVar)
	require.Empty(t, otherEnv,
		"httpServerDeps.%s is fed %q, whose chain reaches %v - another control's variable. The "+
			"schedule cap would then be set from some other control's number.",
		field, ident.Name, otherEnv)
	require.Equal(t, 1, assignedAnywhere[ident.Name],
		"%q is assigned %d times inside main. Exactly one unconditional assignment is the whole "+
			"basis on which this test concludes anything: a second one, in an if or a loop, can take "+
			"the wiring back on some deployments while every check above still passes.",
		ident.Name, assignedAnywhere[ident.Name])
}
