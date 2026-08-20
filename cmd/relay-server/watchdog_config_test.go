package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"relay/internal/scheduler"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWatchdogDuration(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		want     time.Duration
		wantWarn string
	}{
		{"unset keeps the default and does NOT warn", "", scheduler.DefaultWatchdogMargin, ""},
		{"a sensible value is used as-is", "45m", 45 * time.Minute, ""},
		{"zero is ACCEPTED and disables the arm, with an informational line", "0s", 0, "disabled"},
		{"unparseable keeps the default and warns", "thirty minutes", scheduler.DefaultWatchdogMargin, "not a Go duration"},
		{"negative keeps the default and warns", "-5m", scheduler.DefaultWatchdogMargin, "not a Go duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warn := parseWatchdogDuration(
				"RELAY_TASK_WATCHDOG_MARGIN", tc.raw, scheduler.DefaultWatchdogMargin)
			assert.Equal(t, tc.want, got)
			if tc.wantWarn == "" {
				assert.Empty(t, warn, "a valid value must not produce startup noise")
				return
			}
			require.Contains(t, warn, tc.wantWarn,
				"the message is the only signal an operator gets; it must name the consequence")
			assert.Contains(t, warn, "RELAY_TASK_WATCHDOG_MARGIN",
				"the message must name the variable it is about")
		})
	}
}

// TestWatchdogIsStartedByMain is a structural guard in the same spirit as
// TestTrailingLogWindowIsWiredIntoTheHandler. Deleting the wiring block in main()
// compiles and leaves `go build ./... && go test ./...` fully green across every
// package: the watchdog keeps its own passing unit tests, the statement keeps its
// own passing store tests, and the coordinator silently has no bound on task
// duration again - which is the entire bug.
//
// go/ast, NOT a regex. A source-scanning regex guard in this repo was proven
// breakable by a single stray comment.
func TestWatchdogIsStartedByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name assigned -> identifiers its RHS mentions, so the walk can follow
	// `x, warn := f(...)` and then NewWatchdog(..., x, ...).
	from := map[string][]string{}
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
			if id, ok := l.(*ast.Ident); ok {
				from[id.Name] = append(from[id.Name], rhs...)
			}
		}
		return true
	})

	var seeds []string
	ast.Inspect(file, func(n ast.Node) bool {
		gs, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		var idents []string
		ast.Inspect(gs.Call, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok {
				idents = append(idents, id.Name)
			}
			return true
		})
		for _, name := range idents {
			if name == "NewWatchdog" {
				seeds = append(seeds, idents...)
				break
			}
		}
		return true
	})
	require.NotEmpty(t, seeds,
		"main.go starts no goroutine mentioning NewWatchdog: the stale-task watchdog never runs and nothing else fails")

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
		if name == "parseWatchdogDuration" {
			found = true
			break
		}
		queue = append(queue, from[name]...)
	}
	require.True(t, found,
		"main.go starts the watchdog but its bounds do not derive from parseWatchdogDuration, "+
			"so RELAY_TASK_WATCHDOG_MARGIN and RELAY_TASK_MAX_ASSIGNMENT are no longer what reaches it")
}
