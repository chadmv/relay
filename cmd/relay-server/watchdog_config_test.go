package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
	"time"

	"relay/internal/scheduler"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minWatchdogMargin is the floor's literal spelling, so the table can assert the
// boundary is inclusive without restating the number.
const minWatchdogMargin = "2m"

// TestParseWatchdogDuration_MaxAssignmentFloorIsItsOwn proves the floor is
// per-knob rather than shared: an hour is a perfectly sane MARGIN and a
// fleet-destroying MAX ASSIGNMENT is something else entirely, so the two knobs
// cannot share one number.
func TestParseWatchdogDuration_MaxAssignmentFloorIsItsOwn(t *testing.T) {
	// 24m from an operator who meant 24h: parses clean, and within 60 seconds
	// marks every assigned task in the fleet timed_out and cascades each one's
	// transitive dependents to failed, with no automatic retry.
	got, warn := parseWatchdogDuration(
		"RELAY_TASK_MAX_ASSIGNMENT", "24m", scheduler.DefaultMaxAssignment, minMaxAssignmentDur)
	assert.Equal(t, 24*time.Minute, got, "the value is kept, following parseTrailingLogWindow")
	require.NotEmpty(t, warn, "24m for 24h must not pass silently: it ends every assignment in the fleet")
	assert.Contains(t, warn, "RELAY_TASK_MAX_ASSIGNMENT")

	_, warn = parseWatchdogDuration(
		"RELAY_TASK_MAX_ASSIGNMENT", "2m", scheduler.DefaultMaxAssignment, minMaxAssignmentDur)
	require.NotEmpty(t, warn,
		"2m clears the MARGIN floor but must not clear the MAX ASSIGNMENT floor - the knobs are not interchangeable")

	_, warn = parseWatchdogDuration(
		"RELAY_TASK_MAX_ASSIGNMENT", "6h", scheduler.DefaultMaxAssignment, minMaxAssignmentDur)
	assert.Empty(t, warn, "6h is a legitimate tightening and must be silent")
}

// TestWatchdogBoundsLine is the unconditional startup line. A mechanism that can
// terminate a user's work must state its limits at every boot: today the ordinary
// path prints nothing, so an operator reading the log cannot tell what the
// watchdog will do, or that it is switched off.
func TestWatchdogBoundsLine(t *testing.T) {
	line := watchdogBoundsLine(30*time.Minute, 24*time.Hour)
	assert.Contains(t, line, "30m")
	assert.Contains(t, line, "24h")

	off := watchdogBoundsLine(0, 0)
	assert.Contains(t, off, "disabled",
		"a fully disabled watchdog is the single most important thing this line can say")

	half := watchdogBoundsLine(0, 24*time.Hour)
	assert.Contains(t, half, "24h")
	assert.NotContains(t, half, "entirely disabled",
		"one arm off is not the whole watchdog off; saying so would send an operator hunting the wrong thing")
}

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
		// The fail-AGGRESSIVE direction, and the one that destroys work rather
		// than failing to protect it. `30s` for `30m` parses clean.
		{"below the floor keeps the value and warns", "30s", 30 * time.Second, "below"},
		{"below the floor names the units mistake", "30s", 30 * time.Second, "units"},
		{"exactly the floor does not warn", minWatchdogMargin, minWatchdogMarginDur, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warn := parseWatchdogDuration(
				"RELAY_TASK_WATCHDOG_MARGIN", tc.raw, scheduler.DefaultWatchdogMargin, minWatchdogMarginDur)
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
// TestTrailingLogWindowIsWiredIntoTheHandler. Deleting or neutering the wiring
// block in main() compiles and leaves `go build ./... && go test ./...` fully
// green across every package: the watchdog keeps its own passing unit tests, the
// statement keeps its own passing store tests, and the coordinator silently has
// no bound on task duration again - which is the entire bug.
//
// go/ast, NOT a regex. A source-scanning regex guard in this repo was proven
// breakable by a single stray comment.
//
// IT CHECKS EXACTLY FOUR THINGS, and the earlier version of it silently passed
// on all four ways a reviewer broke the wiring - two of which are fixed below
// and two of which no scanner can reach (see WHAT IT CANNOT REACH):
//
//  1. NewWatchdog is called in EXACTLY ONE assignment that is a DIRECT child of
//     a function body, binding a plain identifier. Wrapping the construction in
//     `if watchdogMargin > 0 { ... }` leaves the watchdog a typed nil, and a
//     plain ast.Inspect walk cannot tell the difference because it descends into
//     the if-body. EXACTLY ONE rather than at-least-one because (2) below
//     otherwise has no unambiguous subject: a second construction, started while
//     the first is the one buildHTTPServer reports on, was MEASURED to pass a
//     last-wins version of this walk.
//  2. A `go` statement calling Run on THAT SAME identifier exists and is a
//     DIRECT child of a function body. Wrapping it in
//     `if watchdogMargin < 0 { ... }` leaves the goroutine unreachable, and
//     naming a different identifier runs some other watchdog.
//  3. TWO DISTINCT arguments of that NewWatchdog call each trace back to
//     parseWatchdogDuration. One is not enough: hardcoding only
//     scheduler.DefaultMaxAssignment while leaving the margin parsed silently
//     ignores RELAY_TASK_MAX_ASSIGNMENT, and a BFS that stops at the first seed
//     reaching parseWatchdogDuration reports success while this test's own
//     failure message names both variables.
//  4. EACH BOUND IS IN ITS OWN PARAMETER POSITION. (3) says two parsed bounds
//     arrive; it does not say which arrives where, and both trailing parameters
//     of NewWatchdog are time.Duration, so swapping them compiles and was
//     MEASURED to leave `go test ./cmd/relay-server/...` green - with margin 24h
//     and the absolute cap 30m in production. The discriminator has to be the
//     env var name each argument's chain was parsed from, which is why the
//     assignment walk above collects string literals as well as identifiers.
//
// WHY THE POSITIONAL CHECK IS WORTH ITS COMPLEXITY, since the cheaper move was
// to disclose the gap in WHAT IT CANNOT REACH: a transposition is not a wiring
// mistake that leaves the watchdog dead, which is what every other check here
// is about. It leaves the watchdog ALIVE and destroying work - within one 60s
// tick it stamps every assignment older than half an hour timed_out and
// cascades each one's transitive dependents to failed, with nothing retried.
// That is precisely the catastrophe
// TestParseWatchdogDuration_MaxAssignmentFloorIsItsOwn exists to prevent, and
// leaving it reachable one argument-swap away at the call site would make that
// test decorative.
//
// (1) AND (2) WERE ONE CHECK UNTIL SLICE 4 SPLIT THE STATEMENT, and the split is
// why they are now two. main used to write `go scheduler.NewWatchdog(...).Run(ctx)`,
// so one `go` statement carried both the construction and the start. The
// counters endpoint reports this watchdog's sweeps and buildHTTPServer is the
// only place the api.Server is built, so the watchdog now has to exist as a
// BOUND LOCAL above that call. Nothing was relaxed: "constructed
// unconditionally" and "started unconditionally" are each still required, on
// statements that must each be a direct child of a function body, and the two
// are now additionally required to name the SAME watchdog - a question the
// single-statement form could not pose.
//
// WHAT IT CANNOT REACH, stated rather than overclaimed: a scanner cannot tell
// that `watchdogMargin, maxAssignment = 0, 0` inserted just above the call
// disables both arms, and cannot tell that Run was handed a pre-cancelled
// context. Both compile, both leave every package green, and both leave the
// watchdog dead. A guard whose message promises more than it checks is worse
// than no guard, so this comment is the honest boundary.
func TestWatchdogIsStartedByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name assigned -> identifiers its RHS mentions, so the walk can follow
	// `x, warn := f(...)` and then NewWatchdog(..., x, ...).
	//
	// STRING LITERALS ARE COLLECTED ALONGSIDE IDENTIFIERS, unquoted, and that is
	// what makes check (4) below possible at all. The two bounds are both
	// time.Durations, so nothing about their TYPES distinguishes them; the only
	// thing that does is the env var name each was parsed from, and that name
	// exists in main.go solely as an *ast.BasicLit.
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
				if bl, ok := m.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if s, err := strconv.Unquote(bl.Value); err == nil {
						rhs = append(rhs, s)
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
		return true
	})

	newWatchdogCall := func(n ast.Node) *ast.CallExpr {
		var found *ast.CallExpr
		ast.Inspect(n, func(m ast.Node) bool {
			ce, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := ce.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewWatchdog" {
				found = ce
				return false
			}
			if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "NewWatchdog" {
				found = ce
				return false
			}
			return true
		})
		return found
	}

	// Only statements that are DIRECT elements of a function body count, for BOTH
	// halves. That is what makes `if cond { watchdog = ... }` and
	// `if cond { go watchdog.Run(ctx) }` fail: ast.Inspect would happily find
	// either node inside the if-body, and the watchdog would still be a typed nil
	// or the goroutine would still never start.
	// EVERY unconditional construction, not the last one seen. Collecting them
	// all is what lets the count below be asserted: with last-wins, adding a
	// SECOND `other := scheduler.NewWatchdog(...)` and starting THAT one passes
	// every other check here while the watchdog buildHTTPServer reports on never
	// runs - measured, and the reason this is a slice rather than a variable.
	var calls []*ast.CallExpr
	var bounds []string // the identifiers NewWatchdog's results are assigned to
	started := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.List {
			if as, ok := stmt.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
				for i, l := range as.Lhs {
					id, ok := l.(*ast.Ident)
					if !ok {
						continue
					}
					if ce := newWatchdogCall(as.Rhs[i]); ce != nil {
						calls = append(calls, ce)
						bounds = append(bounds, id.Name)
					}
				}
			}
			gs, ok := stmt.(*ast.GoStmt)
			if !ok {
				continue
			}
			// `go <ident>.Run(...)` only. A Run reached through a field, an
			// index or a helper call is not an identifier this test can tie back
			// to the construction above, so it does not count.
			sel, ok := gs.Call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" {
				continue
			}
			if recv, ok := sel.X.(*ast.Ident); ok {
				started[recv.Name] = true
			}
		}
	}
	require.Len(t, calls, 1,
		"main.go must contain EXACTLY ONE `x := ...NewWatchdog(...)` assignment directly in a function "+
			"body, and has %d (bound to %v). None means the stale-task watchdog is never constructed, or "+
			"is nested inside a conditional that may never execute - in which case it reaches "+
			"buildHTTPServer as a typed nil and the coordinator silently has no bound on task duration "+
			"again. More than one means this test cannot say WHICH watchdog the `go ... .Run(...)` below "+
			"starts, and a second watchdog that is run while the FIRST is the one buildHTTPServer reports "+
			"on publishes a permanent zero for a control that is working - a confident zero, which is "+
			"worse than an absent section. If a second watchdog is legitimate, this guard can no longer "+
			"answer the question it asks and needs REPLACING, not its count bumped.",
		len(calls), bounds)
	call, bound := calls[0], bounds[0]
	require.True(t, started[bound],
		"main.go constructs the watchdog as %q but has no `go %s.Run(...)` statement directly in a function "+
			"body. Constructing it is not running it: the counters section would report an honest zero "+
			"forever while no assignment was ever bounded. Started: %v", bound, bound, keysOf(started))

	// reachedFrom returns every name - identifier or unquoted string literal -
	// reachable from seed through main's assignment graph.
	reachedFrom := func(seed string) map[string]bool {
		seen := map[string]bool{}
		queue := []string{seed}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			queue = append(queue, from[name]...)
		}
		return seen
	}
	reaches := func(seed string) bool { return reachedFrom(seed)["parseWatchdogDuration"] }

	derived := map[string]bool{}
	for _, arg := range call.Args {
		id, ok := arg.(*ast.Ident)
		if !ok {
			continue
		}
		if reaches(id.Name) {
			derived[id.Name] = true
		}
	}
	require.GreaterOrEqual(t, len(derived), 2,
		"NewWatchdog must be handed TWO distinct arguments that each derive from parseWatchdogDuration, one per "+
			"bound. Proving only one proves nothing about the other: hardcoding just the absolute cap leaves "+
			"RELAY_TASK_MAX_ASSIGNMENT silently ignored while the margin still looks wired. Found: %v", derived)

	// (4) EACH BOUND MUST BE IN ITS OWN PARAMETER POSITION.
	//
	// NewWatchdog(q, canceller, broker, margin, maxAssignment): both trailing
	// parameters are time.Duration, so swapping them COMPILES, and check (3)
	// above still finds two distinct arguments deriving from
	// parseWatchdogDuration. Measured: the swap left `go test
	// ./cmd/relay-server/...` fully green. In production it means margin 24h and
	// maxAssignment 30m, so within one 60s tick every assignment older than half
	// an hour is stamped timed_out and its transitive dependents cascade to
	// failed - the exact catastrophe
	// TestParseWatchdogDuration_MaxAssignmentFloorIsItsOwn exists to prevent,
	// walked around at the call site.
	//
	// NewWatchdog's signature is fixed, so this is asserted POSITIONALLY, and
	// the discriminator is the env var name each argument's chain was parsed
	// from - the only thing about the two that differs, since their types do
	// not. It is a two-sided check: the right name must be reachable AND the
	// other must not, so an argument built from both is RED rather than passing
	// on the half it happens to mention.
	positions := []struct {
		i    int
		env  string
		what string
	}{
		{3, "RELAY_TASK_WATCHDOG_MARGIN", "the execution-arm margin added to a task's own timeout_sec"},
		{4, "RELAY_TASK_MAX_ASSIGNMENT", "the absolute cap on how long a task may hold an assignment"},
	}
	require.Len(t, call.Args, 5,
		"NewWatchdog is called with %d arguments; this positional check is written against the "+
			"5-parameter signature (q, canceller, broker, margin, maxAssignment). If the signature "+
			"changed, update the positions below - do not delete the check.", len(call.Args))
	for _, p := range positions {
		id, ok := call.Args[p.i].(*ast.Ident)
		require.True(t, ok,
			"NewWatchdog argument %d must be a plain identifier so this test can trace which env var "+
				"it was parsed from; got %T. It is %s.", p.i, call.Args[p.i], p.what)
		r := reachedFrom(id.Name)
		require.True(t, r["parseWatchdogDuration"],
			"NewWatchdog argument %d is %q, which does not derive from parseWatchdogDuration. It is %s.",
			p.i, id.Name, p.what)
		require.True(t, r[p.env],
			"NewWatchdog argument %d is %q, whose chain never mentions %s. That parameter is %s, and "+
				"BOTH trailing parameters are time.Duration, so passing the other bound here compiles "+
				"and every other check in this test still passes. In production a swap means the "+
				"absolute cap is 30m: within one 60s tick every assignment older than half an hour is "+
				"stamped timed_out and its transitive dependents cascade to failed, with nothing "+
				"retried.", p.i, id.Name, p.env, p.what)
		for _, other := range positions {
			if other.env == p.env {
				continue
			}
			require.False(t, r[other.env],
				"NewWatchdog argument %d is %q, whose chain mentions BOTH %s and %s, so this test "+
					"cannot say which bound it carries. Bind each bound to its own local from its own "+
					"parseWatchdogDuration call.", p.i, id.Name, p.env, other.env)
		}
	}
}
