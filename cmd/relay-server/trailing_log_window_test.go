package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"relay/internal/worker"

	"github.com/stretchr/testify/require"
)

// No build tag: this is a pure function and it runs under `make test`, unlike
// every other test file in this package.
//
// The second return is the warning text, empty when there is nothing to say.
// It is a string rather than a second bool because there are three outcomes,
// not two: accepted silently, rejected-and-defaulted, and accepted-but-alarming
// (a parseable value so small it truncates real output). The last one keeps the
// operator's value - narrowing the window is their prerogative - so a bool
// pairing "did we take it" with "should we warn" would have to lie about one of
// them.
func TestParseTrailingLogWindow(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
		// wantWarn is a substring the warning must contain; empty means the
		// warning must be empty.
		wantWarn string
	}{
		{"unset keeps the default and does NOT warn", "", worker.DefaultTrailingLogWindow, ""},
		{"a valid duration is used", "45m", 45 * time.Minute, ""},
		{"the documented escape hatch is honoured", "8760h", 8760 * time.Hour, ""},
		{"unparseable keeps the default and warns", "fifteen minutes", worker.DefaultTrailingLogWindow, "not a positive Go duration"},
		{"zero keeps the default and warns", "0s", worker.DefaultTrailingLogWindow, "not a positive Go duration"},
		{"negative keeps the default and warns", "-5m", worker.DefaultTrailingLogWindow, "not a positive Go duration"},
		// Units confusion is the likelier operator mistake than a typo, and the
		// only one of these that loses data rather than being a no-op: 15s is
		// parseable, positive and accepted, and then silently truncates
		// essentially all trailing output.
		{"far-too-small is KEPT but warns about truncation", "15s", 15 * time.Second, "silently truncated"},
		{"just under the worst case still warns", "1m59s", 119 * time.Second, "silently truncated"},
		// The threshold is the documented worst case for a legitimately late
		// chunk, and it is inclusive on the safe side: exactly 2m is not "too
		// small", so the boundary cannot warn about a value that is precisely
		// the bound it is compared against.
		{"exactly the worst case does not warn", "2m", 2 * time.Minute, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warn := parseTrailingLogWindow(tc.raw)
			require.Equal(t, tc.want, got)
			if tc.wantWarn == "" {
				require.Empty(t, warn,
					"warning on the ordinary unset or sensible case is as wrong as staying silent on a typo")
				return
			}
			require.Contains(t, warn, tc.wantWarn,
				"the warning is the only signal an operator gets; it must name the consequence, not just the value")
		})
	}
}

// TestTrailingLogWindowIsWiredIntoTheHandler is a structural guard, in the same
// spirit as internal/store/incrementtaskretrycount_guard_test.go: it asserts
// that main() still assigns something derived from parseTrailingLogWindow into
// a .TrailingLogWindow field.
//
// It exists because deleting the six-line wiring block in main() compiles and
// leaves `go build ./... && go test ./...` fully green across every package.
// parseTrailingLogWindow would keep its own passing table test, the handler
// would keep falling back to DefaultTrailingLogWindow, and
// RELAY_TASKLOG_TRAILING_WINDOW would silently stop doing anything - which is
// precisely the class of failure this whole slice exists to eliminate.
//
// go/ast, NOT a regex. A source-scanning regex guard in this repo was proven
// breakable by a single stray comment, and the fix was to delete it in favour
// of something structural. Matching on the parsed assignment ignores
// formatting, comments, string literals and the name of the local variable.
//
// agentHandler.Metrics and agentHandler.AllowAutoEnroll have the identical gap
// (both are set by main() after construction and nothing fails if the line
// goes away), so this guard is worth generalizing rather than pasting a third
// time. The conductor is filing that as its own item - do not generalize here.
func TestTrailingLogWindowIsWiredIntoTheHandler(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name assigned -> identifiers its RHS mentions. Built over the whole file
	// so the walk below can follow `x := f(...)` then `h.Field = x`.
	from := map[string][]string{}
	// LHS selector field name -> identifiers its RHS mentions.
	intoField := map[string][]string{}

	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		var rhs []string
		for _, e := range as.Rhs {
			ast.Inspect(e, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					rhs = append(rhs, id.Name)
				}
				return true
			})
		}
		for _, l := range as.Lhs {
			switch lhs := l.(type) {
			case *ast.Ident:
				from[lhs.Name] = append(from[lhs.Name], rhs...)
			case *ast.SelectorExpr:
				intoField[lhs.Sel.Name] = append(intoField[lhs.Sel.Name], rhs...)
			}
		}
		return true
	})

	seeds, ok := intoField["TrailingLogWindow"]
	require.True(t, ok,
		"main.go assigns nothing to a .TrailingLogWindow field: RELAY_TASKLOG_TRAILING_WINDOW is dead and nothing else fails")

	// Transitive: the value may reach the field through a local, and the
	// intermediate name is not part of the contract.
	seen := map[string]bool{}
	queue := append([]string(nil), seeds...)
	found := false
	for len(queue) > 0 && !found {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if name == "parseTrailingLogWindow" {
			found = true
			break
		}
		queue = append(queue, from[name]...)
	}
	require.True(t, found,
		"main.go assigns to .TrailingLogWindow but the value does not derive from parseTrailingLogWindow, "+
			"so RELAY_TASKLOG_TRAILING_WINDOW is no longer what reaches the handler")
}
