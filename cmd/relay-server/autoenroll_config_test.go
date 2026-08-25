package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/worker"
)

func TestParseAutoEnrollCeiling(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		msgPart string
	}{
		{"unset uses the default and says nothing", "", worker.DefaultAutoEnrollWorkerCeiling, ""},
		{"a positive value is used silently", "50", 50, ""},
		{"zero is ACCEPTED and disables, loudly", "0", 0, "disabled"},
		{"negative uses the default and warns", "-1", worker.DefaultAutoEnrollWorkerCeiling, "not a non-negative integer"},
		{"unparseable uses the default and warns", "lots", worker.DefaultAutoEnrollWorkerCeiling, "not a non-negative integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseAutoEnrollCeiling("RELAY_AUTO_ENROLL_WORKER_CEILING", tc.raw)
			require.Equal(t, tc.want, got)
			if tc.msgPart == "" {
				assert.Empty(t, msg)
				return
			}
			assert.Contains(t, msg, tc.msgPart)
		})
	}
}

// TestAutoEnrollCeilingLineIsUnconditionalAndNamesTheDisabledState. A mechanism
// that can refuse an agent must state its limit at every boot, and disabling a
// bound must never be silent.
func TestAutoEnrollCeilingLineIsUnconditionalAndNamesTheDisabledState(t *testing.T) {
	on := autoEnrollCeilingLine(1024, true)
	assert.Contains(t, on, "1024")

	off := autoEnrollCeilingLine(0, true)
	assert.Contains(t, off, "no bound")

	// Auto-enroll itself off: the line must say the ceiling is moot rather than
	// implying a bound is active.
	moot := autoEnrollCeilingLine(1024, false)
	assert.Contains(t, moot, "RELAY_ALLOW_AUTO_ENROLL")
}

// TestAutoEnrollCeilingIsWiredIntoTheHandler is a copy of
// TestTrailingLogWindowIsWiredIntoTheHandler, and it exists for the same reason:
// a passing parser test proves nothing about main() consuming the parser. It
// fails if main() assigns nothing to the field, or assigns something not derived
// from parseAutoEnrollCeiling - the exact gap that would otherwise leave
// RELAY_AUTO_ENROLL_WORKER_CEILING dead with every other test green.
func TestAutoEnrollCeilingIsWiredIntoTheHandler(t *testing.T) {
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

	seeds, ok := intoField["AutoEnrollWorkerCeiling"]
	require.True(t, ok,
		"main.go assigns nothing to a .AutoEnrollWorkerCeiling field: RELAY_AUTO_ENROLL_WORKER_CEILING is dead and nothing else fails")

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
		if name == "parseAutoEnrollCeiling" {
			found = true
			break
		}
		queue = append(queue, from[name]...)
	}
	require.True(t, found,
		"main.go assigns to .AutoEnrollWorkerCeiling but the value does not derive from parseAutoEnrollCeiling, "+
			"so RELAY_AUTO_ENROLL_WORKER_CEILING is no longer what reaches the handler")
}
