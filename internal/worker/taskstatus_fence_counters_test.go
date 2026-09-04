package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// TestTaskStatusFenceReasonsAreADenseRunFromZero pins the property the counter
// array depends on. UNLIKE logKind, THESE START AT 0: the array is sized
// [fenceReasonCount] and indexed directly, so a run starting at 1 would put the
// last reason one past the end. record fails closed rather than panicking (a
// panic on the recv goroutine kills the process), so getting this wrong is a
// SILENT loss of that reason's counts - the exact defect this slice closes.
func TestTaskStatusFenceReasonsAreADenseRunFromZero(t *testing.T) {
	run := []taskStatusFenceReason{
		fenceReasonRaced,
		fenceReasonDuplicate,
		fenceReasonConflicting,
	}
	for i, r := range run {
		require.Equal(t, taskStatusFenceReason(i), r,
			"reason #%d is %d. The reasons index the counter array directly, so they must stay a DENSE "+
				"RUN starting at 0.", i, r)
	}
	require.Equal(t, taskStatusFenceReason(len(run)), fenceReasonCount,
		"this test's `run` list has %d entries and fenceReasonCount is %d. IF YOU JUST ADDED A REASON, "+
			"ADD IT TO `run` ABOVE - this compares the hardcoded list's length to the sentinel, so a "+
			"correctly-added reason fails here first. OTHERWISE fenceReasonCount is no longer the length "+
			"of the counter array and a reason at or beyond it is never counted.",
		len(run), int(fenceReasonCount))
}

// TestEveryTaskStatusFenceReasonIsDeclaredInsideTheSentinel is the AST rung the
// dense-run test above CANNOT be, and it closes an evasion that was MEASURED
// rather than imagined: declare a fourth reason immediately AFTER
// fenceReasonCount and record it from a real call site - the GetTask
// pgx.ErrNoRows arm, i.e. the plausible "count the vanished-task case too" edit
// - and internal/worker, internal/api and cmd/relay-server ALL STAY GREEN. The
// dense-run test cannot see it (its hardcoded run list still has three entries
// and the sentinel is still 3) and the publish test iterates
// r < fenceReasonCount, so it never reaches the new cell either. record's bounds
// check then drops every increment in silence, which is slice 2's defect one
// layer down.
//
// THE SIBLING TYPE ALREADY ENUMERATED THIS HOLE BY NAME - see
// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished's third bullet,
// "a sixth kind declared AFTER kindCount (the dense-run test above cannot see
// that one)". taskStatusFenceReason shipped with the dense-run rung and no AST
// rung, so the enumeration existed and its counterpart did not.
//
// It parses the PACKAGE, not one file, and resolves const types the way Go does
// (a ConstSpec with no type and no values inherits the previous spec's type), so
// a fourth reason in a SEPARATE const block or a SIBLING FILE is caught by the
// same arity assertion.
//
// THREE INDEPENDENT PROPERTIES, and the first two both kill the measured
// mutation:
//
//   - ARITY: the package declares exactly fenceReasonCount+1 constants of this
//     type (the reasons, plus the sentinel). One declared anywhere else is one
//     too many.
//   - ORDER: in whatever block declares it, fenceReasonCount is LAST.
//   - CALL SITES: every value reaching statusFence.record is either a declared
//     reason constant or a call to a package function whose result type is
//     taskStatusFenceReason, and every such function returns only declared
//     constants. Anything else fails CLOSED rather than being skipped.
func TestEveryTaskStatusFenceReasonIsDeclaredInsideTheSentinel(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	// PASS 1: every taskStatusFenceReason constant in the package, plus the
	// per-block ordering of the sentinel.
	declared := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				typ := ""
				var run []string // this block's reason constants, IN SOURCE ORDER
				for _, sp := range gd.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok {
						continue
					}
					switch {
					case vs.Type != nil:
						typ = ""
						if id, ok := vs.Type.(*ast.Ident); ok {
							typ = id.Name
						}
					case len(vs.Values) > 0:
						// A fresh expression list takes its type from the
						// expression, NOT from the previous spec.
						typ = ""
					}
					if typ != "taskStatusFenceReason" {
						continue
					}
					for _, n := range vs.Names {
						declared[n.Name] = true
						run = append(run, n.Name)
					}
				}
				for i, name := range run {
					if name != "fenceReasonCount" {
						continue
					}
					require.Equal(t, len(run)-1, i,
						"fenceReasonCount is declared at position %d of %d in its const block at %s, so "+
							"%v is declared AFTER it. The sentinel is the LENGTH of the counter array, so "+
							"a reason declared after it has a value at or beyond that length: record's "+
							"bounds check drops every one of its increments in silence, and no other test "+
							"in this package can see it.", i, len(run), fset.Position(gd.Pos()), run[i+1:])
				}
			}
		}
	}

	require.Equal(t, int(fenceReasonCount)+1, len(declared),
		"the package declares %d taskStatusFenceReason constants and fenceReasonCount is %d, so the "+
			"expected count is %d (the reasons, plus the sentinel itself). Every reason must be declared "+
			"INSIDE the sentinel run: one declared after fenceReasonCount, in a separate const block, in "+
			"a sibling file, or with an explicit out-of-run value is never counted and never published, "+
			"and record() drops it in silence. IF YOU JUST ADDED A REASON PROPERLY this assertion still "+
			"passes and TestTaskStatusFenceReasonsAreADenseRunFromZero fails instead - add the reason to "+
			"that test's `run` list and to snapshot().",
		len(declared), int(fenceReasonCount), int(fenceReasonCount)+1)

	// PASS 2: functions that PRODUCE a reason. classifyStatusFenceRejection is
	// the only one today, and the call-site pass below accepts a call to one of
	// these in place of a bare constant precisely because its returns are checked
	// here.
	reasonFuncs := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Type.Results == nil {
					continue
				}
				produces := false
				for _, r := range fd.Type.Results.List {
					if id, ok := r.Type.(*ast.Ident); ok && id.Name == "taskStatusFenceReason" {
						produces = true
					}
				}
				if !produces {
					continue
				}
				reasonFuncs[fd.Name.Name] = true
				ast.Inspect(fd, func(n ast.Node) bool {
					ret, ok := n.(*ast.ReturnStmt)
					if !ok {
						return true
					}
					for _, res := range ret.Results {
						id, ok := res.(*ast.Ident)
						require.True(t, ok,
							"%s returns a %T at %s. A reason producer must return one of the declared "+
								"constants directly, or this guard cannot tell whether the value it hands "+
								"to record is a counted one.", fd.Name.Name, res, fset.Position(res.Pos()))
						require.True(t, declared[id.Name],
							"%s returns %s, which is not a taskStatusFenceReason constant declared in "+
								"this package. Its increments are counted into no cell and published "+
								"under no JSON key.", fd.Name.Name, id.Name)
					}
					return true
				})
			}
		}
	}

	// PASS 3: the call sites. ingestLogCounters has a record method too, so the
	// RECEIVER is matched rather than the method name alone; renaming the
	// statusFence field makes this walk find nothing, and the assertion after the
	// loop then fails loudly rather than passing vacuously.
	recordCalls := 0
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "record" {
					return true
				}
				var recv bytes.Buffer
				require.NoError(t, printer.Fprint(&recv, fset, sel.X))
				if !strings.Contains(recv.String(), "statusFence") {
					return true
				}
				recordCalls++
				require.Len(t, call.Args, 1, "statusFence.record takes exactly one reason")
				switch a := call.Args[0].(type) {
				case *ast.Ident:
					require.True(t, declared[a.Name],
						"statusFence.record(%s) at %s names something that is not a "+
							"taskStatusFenceReason constant declared in this package.",
						a.Name, fset.Position(call.Pos()))
				case *ast.CallExpr:
					id, ok := a.Fun.(*ast.Ident)
					require.True(t, ok,
						"statusFence.record at %s is passed a call this guard cannot name (%T)",
						fset.Position(call.Pos()), a.Fun)
					require.True(t, reasonFuncs[id.Name],
						"statusFence.record at %s is passed %s(...), which does not declare "+
							"taskStatusFenceReason as its result type, so its returns are unchecked.",
						fset.Position(call.Pos()), id.Name)
				default:
					require.Fail(t,
						"statusFence.record is passed an unusable expression",
						"a %T at %s. It must be a declared reason constant or a call to a "+
							"reason-producing function, or this guard cannot tell whether the cell it "+
							"lands in exists.", call.Args[0], fset.Position(call.Pos()))
				}
				return true
			})
		}
	}
	require.GreaterOrEqual(t, recordCalls, 2,
		"this walk found %d statusFence.record call sites and handleTaskStatus has two (the retry arm "+
			"and the update arm), so it proved nothing. If the field was renamed, update the receiver "+
			"match above.", recordCalls)
}

// TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly drives every
// reason a DIFFERENT number of times and requires the published struct to carry
// exactly those numbers IN ORDER.
//
// ORDERED, NOT ElementsMatch, and for the measured reason recorded in
// TestIngestLogCounters_EveryKindIsPublishedDistinctly: an order-insensitive
// match leaves a crossed pair of assignments in snapshot() green.
func TestTaskStatusFenceCounters_EveryReasonIsPublishedDistinctly(t *testing.T) {
	var c statusFenceCounters

	var want []uint64
	n := uint64(1)
	for r := taskStatusFenceReason(0); r < fenceReasonCount; r++ {
		for i := uint64(0); i < n; i++ {
			c.record(r)
		}
		want = append(want, n)
		n++
	}

	got := c.snapshot()
	rv := reflect.ValueOf(got)
	require.Equal(t, len(want), rv.NumField(),
		"there are %d reasons and %d published fields. A reason with no field is counted into a cell "+
			"nobody reads - which is slice 2's defect, one package smaller.", len(want), rv.NumField())
	fields := make([]uint64, 0, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		fields = append(fields, rv.Field(i).Uint())
	}
	require.Equal(t, want, fields,
		"every reason must publish its OWN cell, IN ORDER: field i of TaskStatusFenceCounts must read "+
			"reason i. A missing value means a reason is counted but never published; a permutation "+
			"means two fields are crossed.")
}

// TestTaskStatusFenceCounters_AnOutOfRangeReasonIsDroppedNotPanicked. The bounds
// check exists because the alternative on the gRPC recv goroutine is a panic
// that kills the process. It is unreachable while the dense-run test is green,
// and this exists so "unreachable" does not mean "untested".
func TestTaskStatusFenceCounters_AnOutOfRangeReasonIsDroppedNotPanicked(t *testing.T) {
	var c statusFenceCounters
	require.NotPanics(t, func() {
		c.record(fenceReasonCount)
		c.record(taskStatusFenceReason(200))
	})
	require.Equal(t, TaskStatusFenceCounts{}, c.snapshot(),
		"an out-of-range reason must be DROPPED, not folded into some other cell")
}

// TestTaskStatusFenceRejections_TwoHandlersDoNotShareCounts pins the HOME. A
// package-level var would make every exact-count assertion in this package
// order-dependent on every other test in the binary; production has exactly one
// Handler, so a value field IS process-wide there.
func TestTaskStatusFenceRejections_TwoHandlersDoNotShareCounts(t *testing.T) {
	var a, b Handler
	a.statusFence.record(fenceReasonDuplicate)

	require.Equal(t, uint64(1), a.TaskStatusFenceRejections().Duplicate)
	require.Equal(t, TaskStatusFenceCounts{}, b.TaskStatusFenceRejections(),
		"counters are per Handler, not per package")
}

// TestTaskStatusWritableSetMatchesTheSQLAllowList reads the allow-list out of
// internal/store/query/tasks.sql and requires the Go mirror to be exactly it,
// for BOTH statements handleTaskStatus writes through.
//
// WHY A GUARD AND NOT JUST A COMMENT. taskStatusIsWritable restates a set that
// lives in SQL, and this repo's recorded lesson is that a hand-written copy
// needs something comparing it to its source. The parse is deliberately narrow:
// it slices the file between one `-- name: X` header and the next, DROPS EVERY
// `--` COMMENT LINE, then reads the single `status IN (...)` clause left in the
// executable text, so a predicate added to a DIFFERENT statement cannot satisfy
// it.
//
// THE COMMENT STRIP IS LOAD-BEARING AND WAS FOUND BY RUNNING IT, not assumed:
// IncrementTaskRetryCount's own doc block quotes RetryJobTasks' allow-list
// (`status IN ('failed','timed_out')`, tasks.sql, in the paragraph that
// separates the two statements), so a parse over the raw block sees TWO clauses
// and reads a set that is not a predicate at all. A quoted allow-list in prose
// is exactly the thing this guard must not mistake for the statement's own.
//
// IT IS TWO-DIRECTIONAL, AND THE SECOND DIRECTION TOOK TWO GOES. The first two
// loops below assert SQL -> Go (everything the statement admits,
// taskStatusIsWritable calls writable) and that the terminal triple is not
// writable. Neither sees a GO-SIDE EXTRA - a status the mirror calls writable
// that the statement does not admit - the edit this file's own production comment
// warns about. That drift mislabels every genuine terminality rejection for such
// a row as `raced`, quietly zeroing the actionable key for it.
//
// THE FIRST ATTEMPT AT THAT DIRECTION WAS A UNIVERSE LOOP, AND IT ONLY CLOSED
// HALF OF IT - measured, not reasoned. Iterating a candidate set and requiring
// both sides to agree catches a Go-side extra only when SOMETHING ENUMERATES THE
// STATUS. Adding `cancelled` to taskStatusIsWritable left `go test
// ./internal/worker/` GREEN through three separate widenings of that candidate
// set, because `cancelled` is not in the SQL allow-list, not in
// tasks_status_check, and not in the proto - so no source produced it and
// nothing iterated it. A universe can only ever contain statuses somebody has
// already written down somewhere; the edit this guard exists to catch is
// somebody writing one down HERE FIRST.
//
// SO THE CLOSING RUNG READS THE MIRROR'S OWN SOURCE. taskStatusWritableLiterals
// pulls every string literal out of taskStatusIsWritable's body via go/ast and
// the last block below compares that to the SQL allow-list AS A SET, in both
// directions, with no candidate set in the middle. A status of ANY spelling
// added to the Go mirror is then caught whether or not any other source has ever
// heard of it. It is an AST parse and not a text scan for the reason the
// Table-minWidth guard was deleted over: a `//` comment naming a status is not a
// literal in the AST, so it cannot feed this parse the way it could feed a grep.
//
// THE UNIVERSE LOOP STAYS, and it is NOT redundant with the rung above even
// though it is weaker. It is the BEHAVIOURAL half: it calls the function rather
// than reading it, so it survives the function being rewritten into a shape the
// literal parse cannot interpret, and it is what kills the deny-list rewrite
// (`case "done","failed","timed_out": return false` - measured, fails naming
// `prepare_failed`). Keep both; they fail on disjoint edits.
//
// STATE THE STAKE HONESTLY, because it is lower than every other status
// allow-list in this tree and a reader who assumes otherwise will over-react to
// a failure here: this set gates NOTHING. It labels a counter. Drift mislabels a
// number; it cannot admit a write. That is exactly why the guard is cheap enough
// to be worth having.
func TestTaskStatusWritableSetMatchesTheSQLAllowList(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "store", "query", "tasks.sql"))
	require.NoError(t, err)
	sql := string(src)

	clause := regexp.MustCompile(`status IN \(([^)]*)\)`)
	quoted := regexp.MustCompile(`'([a-z_]+)'`)

	for _, stmt := range []string{"UpdateTaskStatus", "IncrementTaskRetryCount"} {
		start := strings.Index(sql, "-- name: "+stmt+" ")
		require.GreaterOrEqual(t, start, 0,
			"tasks.sql no longer declares %s. handleTaskStatus writes through it, so either it was "+
				"renamed (update this list) or the write path changed (re-derive this whole guard).", stmt)
		end := strings.Index(sql[start+1:], "-- name: ")
		require.GreaterOrEqual(t, end, 0, "%s is the last statement in tasks.sql; this parse needs a terminator", stmt)
		body := sql[start : start+1+end]

		// Executable text only. See the comment-strip paragraph above.
		var stripped []string
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			stripped = append(stripped, line)
		}
		body = strings.Join(stripped, "\n")

		found := clause.FindAllStringSubmatch(body, -1)
		require.Len(t, found, 1,
			"%s carries %d `status IN (...)` clauses in its EXECUTABLE text. This guard reads exactly "+
				"one; if the statement now has two, decide which one taskStatusIsWritable mirrors and "+
				"say so here.", stmt, len(found))

		var want []string
		for _, m := range quoted.FindAllStringSubmatch(found[0][1], -1) {
			want = append(want, m[1])
		}
		require.NotEmpty(t, want, "parsed no statuses out of %s's allow-list; the parse is broken, not the code", stmt)

		for _, s := range want {
			require.True(t, taskStatusIsWritable(s),
				"tasks.sql's %s admits status %q and taskStatusIsWritable says it is NOT writable. The "+
					"two have drifted: a rejection for a %q row would now be labelled `duplicate` or "+
					"`conflicting` when it is in fact a `raced`. Add it.", stmt, s, s)
		}
		for _, s := range []string{"done", "failed", "timed_out"} {
			require.False(t, taskStatusIsWritable(s),
				"%q is not in %s's allow-list but taskStatusIsWritable says it is writable. Every "+
					"terminality rejection would then be labelled `raced` and conflicting_total would "+
					"read zero forever - the actionable key silenced.", s, stmt)
		}

		// GO -> SQL, BEHAVIOURALLY, over a candidate set. Weaker than the set
		// comparison after the loop - it sees only statuses some source
		// enumerates - but it calls the function instead of reading it, which is
		// what kills a rewritten body.
		inSQL := map[string]bool{}
		for _, s := range want {
			inSQL[s] = true
		}
		for _, c := range taskStatusUniverse(t, want) {
			require.Equal(t, inSQL[c], taskStatusIsWritable(c),
				"taskStatusIsWritable(%q) is %v and %s's allow-list %s it. The two must agree in BOTH "+
					"directions: a status the Go mirror admits and the statement does not means every "+
					"genuine terminality rejection for a %q row is labelled `raced` instead of "+
					"`duplicate` or `conflicting`, which reads as a healthy race and silences the "+
					"actionable key for that state.",
				c, taskStatusIsWritable(c), stmt, map[bool]string{true: "admits", false: "does not admit"}[inSQL[c]], c)
		}

		// GO -> SQL AS A SET, with no candidate set in the middle. This is the
		// rung that sees a status nothing else in the tree has ever named.
		sort.Strings(want)
		require.Equal(t, want, taskStatusWritableLiterals(t),
			"taskStatusIsWritable's own source names a different set of statuses than %s's allow-list. "+
				"A status the Go mirror admits and the statement does not mislabels every genuine "+
				"terminality rejection for such a row as `raced`, silencing the actionable key for it; "+
				"one the statement admits and the mirror does not does the reverse. Unlike the loop "+
				"above this comparison needs no source to have enumerated the status, which is the "+
				"whole point - it is the only thing here that catches a status invented at this "+
				"function.", stmt)
	}
}

// taskStatusWritableLiterals returns every string literal in the body of
// taskStatusIsWritable, sorted and deduplicated.
//
// READING THE FUNCTION IS THE POINT. Every other assertion in this guard CALLS
// it, and a call can only ask about a status the caller already knows to ask
// about. This reads the set the function was written with, so a status that
// exists nowhere else in the tree is still compared against the SQL.
//
// IT FAILS CLOSED IN THREE WAYS, because a parse that silently returns nothing
// would make the set comparison it feeds pass by agreeing with an empty SQL
// allow-list only, and quietly do nothing otherwise. The function must be found,
// it must contain at least one literal, and every literal in it must be an
// untyped string constant - so a body rewritten to compare against a variable,
// a constant identifier or another function's result fails here with a message
// saying to re-derive the guard, rather than reporting an empty set.
//
// COMMENTS CANNOT REACH IT: parser.ParseDir is called with mode 0, so comments
// are not attached and are not nodes. That is the difference between this and a
// grep, and it is the reason this rung is allowed to exist at all given what a
// one-comment evasion did to the Table-minWidth guard.
func taskStatusWritableLiterals(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	var fn *ast.FuncDecl
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "taskStatusIsWritable" {
					require.Nil(t, fn, "taskStatusIsWritable is declared more than once in this package")
					fn = fd
				}
			}
		}
	}
	require.NotNil(t, fn,
		"no func taskStatusIsWritable in package worker. It was renamed or removed; this guard reads "+
			"its source, so re-point it rather than deleting it - the SQL mirror still needs comparing.")

	seen := map[string]bool{}
	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok {
			return true
		}
		require.Equal(t, token.STRING, lit.Kind,
			"taskStatusIsWritable contains a non-string literal %s at %s. This guard assumes every "+
				"literal in the body is a status; re-derive it.", lit.Value, fset.Position(lit.Pos()))
		s, err := strconv.Unquote(lit.Value)
		require.NoError(t, err)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
		return true
	})
	require.NotEmpty(t, out,
		"parsed no string literals out of taskStatusIsWritable's body. It no longer names its statuses "+
			"inline - it now reads them from somewhere else - so this guard is comparing nothing and "+
			"would pass vacuously. Re-point it at whatever now holds the set.")

	sort.Strings(out)
	return out
}

// taskStatusUniverse is the candidate set the two-directional comparison above
// runs over. It is the union of THREE sources, and it took all three:
//
//   - THE SQL ALLOW-LIST the caller just parsed. Everything the statement
//     admits.
//   - THE COLUMN VOCABULARY - tasks_status_check, read out of the migration that
//     adds it. WHAT A ROW CAN ACTUALLY HOLD, which is the half the first two
//     sources omit and the reason this helper was one-directional in practice
//     after being written as two. Measured: with only the allow-list and the
//     proto in the universe, adding `cancelled` to taskStatusIsWritable left
//     `go test ./internal/worker/` GREEN, because `cancelled` is in neither
//     source and so nothing iterated it. `cancelled` is the live candidate -
//     CancelJobTasks squashes cancellation onto `failed` today and
//     internal/store/tasks_status_vocabulary_lockstep_test.go names it as the
//     status somebody will eventually want for real.
//   - THE WIRE - proto TaskStatus, less UNSPECIFIED, lowercased off its enum
//     prefix.
//
// THE PROTO IS NOT REDUNDANT WITH THE VOCABULARY and neither subsumes the other,
// which is why both are here. A status appears in relay.proto BEFORE it is a
// value in tasks_status_check: TASK_STATUS_PREPARE_FAILED is in the proto today
// and the agent already sends it, so `prepare_failed` is a candidate here while
// the column cannot hold it. Going the other way, a
// status can be a legal column value with no wire spelling at all, which is
// exactly the `cancelled` shape above. The union is what makes this a property
// rather than a spelling.
//
// Candidates that are not database statuses are harmless rather than excluded:
// `prepare_failed` is a wire-only value the handler maps onto `failed`, so both
// sides say "not writable" and the comparison holds. Excluding it by name would
// be the spelling rung; letting it through is the property rung.
//
// Sorted so a failure names the same status every run.
func taskStatusUniverse(t *testing.T, sqlAllowList []string) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range sqlAllowList {
		add(s)
	}
	for _, s := range tasksStatusVocabulary(t) {
		add(s)
	}
	for _, n := range relayv1.TaskStatus_name {
		if n == "TASK_STATUS_UNSPECIFIED" {
			continue
		}
		add(strings.ToLower(strings.TrimPrefix(n, "TASK_STATUS_")))
	}
	sort.Strings(out)
	return out
}

// tasksStatusVocabulary reads the literal set of tasks_status_check out of the
// LAST up-migration that adds it - the same constraint
// internal/store/tasks_status_vocabulary_lockstep_test.go reads back off a live
// Postgres. This lane cannot reach a database (it is the no-tag CI lane), so it
// reads the source the constraint is built from instead.
//
// IT SCANS EVERY up-MIGRATION, TAKES THE LEXICALLY GREATEST MATCH, AND ASSERTS
// THAT IT DID. A hard-coded path goes stale silently the day a later migration
// drops and re-adds the constraint with a wider set. So does taking the FIRST
// match, or taking any match without checking it is the greatest: either reads a
// superseded vocabulary forever while passing, which is a fail-open, because the
// universe it feeds would no longer contain every status a row can hold.
//
// `--` COMMENT LINES ARE STRIPPED BEFORE MATCHING. A migration's own doc block
// may legitimately quote a prior vocabulary, and a quoted definition in prose is
// exactly the thing this parse must not mistake for a real one. The
// down-migrations are excluded because they drop or re-add the constraint by
// name and would otherwise be extra hits.
func tasksStatusVocabulary(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "store", "migrations")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	// `\s` spans newlines, which matters: the ALTER TABLE is written across
	// three lines with the constraint name on its own.
	def := regexp.MustCompile(`ADD CONSTRAINT tasks_status_check\s+CHECK \(status IN \(([^)]*)\)`)
	quoted := regexp.MustCompile(`'([a-z_]+)'`)

	type hit struct {
		file     string
		statuses []string
	}
	var hits []hit
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)

		// Executable text only. See the comment-strip paragraph above.
		var stripped []string
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			stripped = append(stripped, line)
		}
		body := strings.Join(stripped, "\n")

		for _, m := range def.FindAllStringSubmatch(body, -1) {
			var statuses []string
			for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
				statuses = append(statuses, q[1])
			}
			hits = append(hits, hit{file: e.Name(), statuses: statuses})
		}
	}

	require.NotEmpty(t, hits,
		"no up-migration ADDs CONSTRAINT tasks_status_check. Either the constraint moved, or this "+
			"parse no longer matches the migration's formatting - which is a FAIL-OPEN, because a "+
			"parse that silently returns nothing makes every comparison it feeds vacuous. Re-derive it.")

	last := hits[len(hits)-1]
	for _, h := range hits {
		require.LessOrEqual(t, h.file, last.file,
			"this parse takes the LAST match in os.ReadDir order and got %s, but %s sorts after it. "+
				"os.ReadDir returns entries sorted by filename and migration filenames are "+
				"zero-padded, so the last match is the newest definition. If that stopped being "+
				"true, this helper is reading a STALE vocabulary and the universe it feeds no "+
				"longer contains every status a row can hold.", last.file, h.file)
	}
	require.NotEmpty(t, last.statuses,
		"parsed no statuses out of tasks_status_check in %s; the parse is broken, not the code", last.file)
	return last.statuses
}

// TestClassifyStatusFenceRejection is the classifier's own truth table, with the
// watchdog case named because it is the reason this slice exists.
func TestClassifyStatusFenceRejection(t *testing.T) {
	tests := []struct {
		name          string
		row, reported string
		want          taskStatusFenceReason
	}{
		{"still writable at T0 is a race", "running", "done", fenceReasonRaced},
		{"dispatched is writable too", "dispatched", "running", fenceReasonRaced},
		{"pending is writable too", "pending", "failed", fenceReasonRaced},
		{"the agent repeats its own terminal", "done", "done", fenceReasonDuplicate},
		{"a repeated failure", "failed", "failed", fenceReasonDuplicate},
		{"watchdog swept it and the agent reports success", "timed_out", "done", fenceReasonConflicting},
		{"watchdog swept it and the agent is still heartbeating", "timed_out", "running", fenceReasonConflicting},
		{"the agent contradicts its own outcome", "done", "failed", fenceReasonConflicting},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyStatusFenceRejection(tc.row, tc.reported))
		})
	}
}

// stubStatusDB is the narrowest store.DBTX that drives handleTaskStatus's write
// path WITHOUT Postgres, which is what puts this proof in the lane CI actually
// runs (go-ci: `go test -race ./...`, no tag, no container).
//
// IT DISPATCHES ON THE STATEMENT'S OWN `-- name:` HEADER, which sqlc emits as
// the first line of every generated SQL constant. That is a property of the
// generated code rather than of a hand-copied SQL fragment, so a reformatted
// statement cannot silently re-route a call.
//
// Unlike handleTaskLog, this handler is MORE than one statement - GetTask, then
// one of two writes, then (on the success path) FailDependentTasks,
// RecomputeJobStatus and NotifyTaskCompleted - so Exec and Query return benign
// values instead of panicking. calls records what was actually reached, which is
// how the success leg establishes acceptance POSITIVELY rather than through a
// projection every other arm also produces.
type stubStatusDB struct {
	task     store.Task // what GetTask returns
	getErr   error      // what GetTask returns INSTEAD, when set
	writeErr error      // what the retry/update statement returns
	execErr  error

	// mu protects calls ONLY. It is fixture bookkeeping: task, writeErr and
	// execErr are written once before any goroutine starts and are read-only
	// afterwards, so the subject under test acquires nothing this stub owns and
	// the no-new-lock constraint on the recv goroutine is not violated by the
	// production path. Without it the concurrency test below races on the
	// FIXTURE rather than on the subject, which would make its -race half prove
	// the wrong thing.
	mu    sync.Mutex
	calls []string
}

// callsSnapshot is how the single-threaded tests read `calls`. Reading the slice
// directly would be a race the concurrency test can see, and -race reporting the
// fixture is indistinguishable in the output from -race reporting the counters.
func (d *stubStatusDB) callsSnapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *stubStatusDB) note(sql string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, name := range []string{
		"GetTask", "UpdateTaskStatus", "IncrementTaskRetryCount",
		"FailDependentTasks", "RecomputeJobStatus", "NotifyTaskCompleted", "NotifyTaskSubmitted",
	} {
		if strings.Contains(sql, "-- name: "+name+" ") {
			d.calls = append(d.calls, name)
			return name
		}
	}
	d.calls = append(d.calls, "UNKNOWN")
	return "UNKNOWN"
}

func (d *stubStatusDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.note(sql)
	return pgconn.CommandTag{}, d.execErr
}

func (d *stubStatusDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.note(sql)
	return nil, errors.New("stubStatusDB: handleTaskStatus performs no multi-row Query")
}

func (d *stubStatusDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch d.note(sql) {
	case "GetTask":
		return stubTaskRow{task: d.task, err: d.getErr}
	case "UpdateTaskStatus", "IncrementTaskRetryCount":
		return stubTaskRow{task: d.task, err: d.writeErr}
	case "RecomputeJobStatus":
		return stubStringRow{s: "running"}
	}
	return stubTaskRow{err: errors.New("stubStatusDB: unexpected QueryRow")}
}

// stubTaskRow fills a store.Task BY POSITION, and the positional copy is safe
// for a checked reason rather than by luck: sqlc scans a `SELECT *` row in
// MODEL FIELD ORDER (internal/store/tasks.sql.go: &i.ID, &i.JobID, ... matches
// store.Task's declaration exactly), so reflecting over the value gives the same
// order the generated Scan asks for. The arity assertion is what makes that a
// checked claim: a regenerated model with a new column fails here loudly instead
// of silently shifting every field by one.
type stubTaskRow struct {
	task store.Task
	err  error
}

func (r stubTaskRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	rv := reflect.ValueOf(r.task)
	if len(dest) != rv.NumField() {
		return fmt.Errorf("stubTaskRow: generated Scan wants %d columns and store.Task has %d fields. "+
			"This stub copies by position because sqlc scans in model field order; that assumption no "+
			"longer holds, so re-derive it rather than padding the list", len(dest), rv.NumField())
	}
	for i, d := range dest {
		dv := reflect.ValueOf(d)
		if dv.Kind() != reflect.Pointer || dv.Elem().Type() != rv.Field(i).Type() {
			return fmt.Errorf("stubTaskRow: column %d is %T and store.Task field %d is %s - the scan "+
				"order and the field order have diverged", i, d, i, rv.Field(i).Type())
		}
		dv.Elem().Set(rv.Field(i))
	}
	return nil
}

type stubStringRow struct{ s string }

func (r stubStringRow) Scan(dest ...any) error {
	if len(dest) == 1 {
		if p, ok := dest[0].(*string); ok {
			*p = r.s
		}
	}
	return nil
}

func statusWorkerID() pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{9}, Valid: true} }

const statusTaskID = "3f1c0a2e-7b64-4d8a-9c31-0e5b6a7d8c90"

func statusTaskIDUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	require.NoError(t, u.Scan(statusTaskID))
	return u
}

// newStatusHandler wires a Handler over the stub with a task the connection's
// worker OWNS at the CURRENT epoch, so both Go-side gates pass and control
// really reaches the write. Any test that wants a gate to reject changes the
// fixture, never the handler.
func newStatusHandler(t *testing.T, rowStatus string, retries, retryCount int32, writeErr error) (*Handler, *stubStatusDB) {
	t.Helper()
	db := &stubStatusDB{
		task: store.Task{
			ID:              statusTaskIDUUID(t),
			JobID:           pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			Status:          rowStatus,
			WorkerID:        statusWorkerID(),
			AssignmentEpoch: 7,
			Retries:         retries,
			RetryCount:      retryCount,
		},
		writeErr: writeErr,
	}
	return &Handler{q: store.New(db), broker: events.NewBroker()}, db
}

func statusUpdate(s relayv1.TaskStatus) *relayv1.TaskStatusUpdate {
	return &relayv1.TaskStatusUpdate{TaskId: statusTaskID, Status: s, Epoch: 7}
}

// statusSubscribe tails every status event this handler publishes, and
// SUBSCRIBING IS LOAD-BEARING rather than fixture noise: Publish on an
// unsubscribed broker is a map lookup that finds nothing, so an added
// h.broker.Publish on a rejection arm is INVISIBLE without a subscriber. Slice 3
// closed exactly this for the log path (fenceSubscribe in
// tasklog_fence_counter_test.go); the status path's tests had no
// broker.Subscribe anywhere, and measured, a Publish added to the
// UpdateTaskStatus pgx.ErrNoRows arm left internal/worker, internal/api and
// cmd/relay-server all green.
//
// It is what CLAUDE.md's epoch fence calls the named consequence: "Gate any side
// effect on the fence having actually matched". A rejected status report that
// reaches the broker puts a swept or contradicted outcome into a live SSE view,
// where it then vanishes on refresh because it was correctly never stored.
//
// The zero Filter takes ALL status events - "task" and "job" alike - so an added
// publish of either type is seen. The drain is non-blocking because Publish is
// synchronous under the broker's own mutex and completes before
// handleTaskStatus returns.
func statusSubscribe(t *testing.T, h *Handler) func() []events.Event {
	t.Helper()
	ch, cancel := h.broker.Subscribe(events.Filter{})
	t.Cleanup(cancel)
	return func() []events.Event {
		var got []events.Event
		for {
			select {
			case e := <-ch:
				got = append(got, e)
			default:
				return got
			}
		}
	}
}

// TestHandleTaskStatus_TheUpdateArmCountsEachRejectionReasonAndASuccessCountsNothing
// is item 1's own Done-When at the UpdateTaskStatus arm: read the counters
// across each rejection AND across a success.
//
// EACH LEG IS ASSERTED IMMEDIATELY AFTER IT RUNS. A battery that only checks
// totals at the end cannot tell "the success incremented" from "the third
// rejection did not", and a poisoned input observed only at the end cannot
// detect an early-exit mutation.
//
// IT ALSO CARRIES THE DROP-BEFORE-PUBLISH PROPERTY, which was unpinned on this
// path in the unit lane: see statusSubscribe.
func TestHandleTaskStatus_TheUpdateArmCountsEachRejectionReasonAndASuccessCountsNothing(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	const noPublish = "a status report the fence REJECTED must not be published. CLAUDE.md's epoch " +
		"fence names publishing as the consequence to gate on the fence having actually matched: a " +
		"swept or contradicted outcome that reaches the broker appears in a live SSE view and then " +
		"vanishes on refresh, because it was correctly never stored."

	// CONFLICTING FIRST, because it is the leg this slice exists for and a
	// poisoned input placed last cannot detect an early-exit mutation. The
	// watchdog stamped `timed_out`; the agent reports `done`.
	h, db := newStatusHandler(t, "timed_out", 0, 0, pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	published := statusSubscribe(t, h)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Conflicting: 1}, h.TaskStatusFenceRejections(),
		"a task the coordinator marked timed_out whose agent reports done is the ACTIONABLE case: a "+
			"successful task recorded as a timeout. Before this number there was no runtime signal of "+
			"any kind for it.")
	require.Empty(t, published(), noPublish)
	require.Contains(t, db.callsSnapshot(), "UpdateTaskStatus", "fixture: control must reach the write")
	require.NotContains(t, db.callsSnapshot(), "FailDependentTasks",
		"fixture: a rejected write must return before any follow-on effect")

	// DUPLICATE: same row status as the report. The expected healthy floor.
	h, _ = newStatusHandler(t, "done", 0, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	published = statusSubscribe(t, h)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Duplicate: 1}, h.TaskStatusFenceRejections(),
		"a duplicate terminal from a healthy assignee is an EXPECTED rejection and must be counted "+
			"under its own key, or the actionable number reads as constant alarm")
	require.Empty(t, published(), noPublish)

	// RACED: the row was still writable at T0, so something ended the generation
	// inside this handler's own window.
	h, _ = newStatusHandler(t, "running", 0, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	published = statusSubscribe(t, h)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Raced: 1}, h.TaskStatusFenceRejections())

	// ACCUMULATION on ONE handler: an Add, never a Store.
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Raced: 2}, h.TaskStatusFenceRejections())
	require.Empty(t, published(), noPublish)

	// SUCCESS MUST NOT COUNT, on the SAME handler whose counter has already
	// moved: a counter that increments unconditionally passes a fresh-handler
	// check. Acceptance is established POSITIVELY, by the follow-on effect only
	// the accepted path produces.
	db2 := &stubStatusDB{task: store.Task{
		ID: statusTaskIDUUID(t), JobID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		Status: "running", WorkerID: statusWorkerID(), AssignmentEpoch: 7,
	}}
	h2 := &Handler{q: store.New(db2), broker: events.NewBroker()}
	published2 := statusSubscribe(t, h2)
	h2.handleTaskStatus(ctx, statusWorkerID(), newIngestLogLimiter(&h2.ingestDrops),
		statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{}, h2.TaskStatusFenceRejections(),
		"an ACCEPTED status report must not be counted as a rejection. This number is what an operator "+
			"reads as 'reports are being discarded'; incrementing it on the happy path makes it noise.")
	require.Contains(t, db2.callsSnapshot(), "RecomputeJobStatus",
		"THE REPORT WAS ACCEPTED, and this is what says so. Without a positive marker the leg above "+
			"asserts a negative through a projection every other arm shares.")
	require.Contains(t, db2.callsSnapshot(), "NotifyTaskCompleted")

	// THE POSITIVE HALF OF DROP-BEFORE-PUBLISH, and it is what stops every
	// require.Empty above being satisfied by a handler that publishes nothing at
	// all. Exactly ONE event: the "task" frame. The stub's RecomputeJobStatus
	// answers "running", so the job frame is correctly not sent.
	accepted := published2()
	require.Len(t, accepted, 1,
		"an ACCEPTED status report must publish exactly one task event. Without this leg the "+
			"no-publish assertions above hold vacuously against a handler that never publishes.")
	require.Equal(t, "task", accepted[0].Type)

	require.Equal(t, "", logged(),
		"a fence rejection must emit NO log line of any wording, including a budgeted one: it is "+
			"caller-driven volume on the recv goroutine, firing on the legitimate duplicate-terminal case")
}

// TestHandleTaskStatus_TheRetryArmCountsItsOwnRejections. The retry branch is
// reached instead of the update when the report is terminal and a retry is
// left, so it needs its own fixture and its own leg.
//
// THE NO-PUBLISH ASSERTION HERE IS THE WHOLE BRANCH, NOT JUST ITS REJECTIONS,
// and that is deliberate rather than a copy of the update arm: an accepted retry
// publishes NOTHING - it wakes the dispatcher and recomputes the job status and
// returns - so the discriminating positive marker for this branch is
// NotifyTaskSubmitted, already asserted below, and the event count is zero on
// every leg. A Publish added to either arm of this branch moves it off zero.
func TestHandleTaskStatus_TheRetryArmCountsItsOwnRejections(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	// CONFLICTING FIRST again. The watchdog stamped timed_out; the agent reports
	// failed and still has a retry left.
	h, db := newStatusHandler(t, "timed_out", 3, 0, pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)
	published := statusSubscribe(t, h)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Contains(t, db.callsSnapshot(), "IncrementTaskRetryCount",
		"fixture: terminal + retries remaining must take the RETRY branch")
	require.NotContains(t, db.callsSnapshot(), "UpdateTaskStatus",
		"fixture: the retry branch returns; the two arms are mutually exclusive and no input executes both")
	require.Equal(t, TaskStatusFenceCounts{Conflicting: 1}, h.TaskStatusFenceRejections(),
		"the retry arm's rejections are the SAME noun as the update arm's - the agent's report of this "+
			"task's outcome was discarded - so they share the section and are split by REASON, not by "+
			"statement")
	require.Empty(t, published(),
		"a retry the fence REJECTED must not be published - and the retry branch publishes nothing on "+
			"any leg, so any event here is an added side effect that is not gated on the fence matching")

	// DUPLICATE at the retry arm.
	h, _ = newStatusHandler(t, "failed", 3, 0, pgx.ErrNoRows)
	lim = newIngestLogLimiter(&h.ingestDrops)
	published = statusSubscribe(t, h)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Equal(t, TaskStatusFenceCounts{Duplicate: 1}, h.TaskStatusFenceRejections())
	require.Empty(t, published(), "no leg of the retry branch publishes")

	// A SUCCESSFUL retry must not count, and must still wake the dispatcher.
	h, db = newStatusHandler(t, "running", 3, 0, nil)
	lim = newIngestLogLimiter(&h.ingestDrops)
	published = statusSubscribe(t, h)
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections())
	require.Contains(t, db.callsSnapshot(), "NotifyTaskSubmitted",
		"the accepted retry must still wake the dispatcher; this is the positive marker for this arm")
	require.Empty(t, published(), "no leg of the retry branch publishes")

	require.Equal(t, "", logged(), "no arm of the retry branch logs")
}

// statusOtherWorkerID is a registered peer that is NOT this task's assignee.
// Different Bytes, same Valid: the only thing separating it from the assignee is
// the identity the gate checks.
func statusOtherWorkerID() pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{11}, Valid: true} }

// TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters pins the identity
// gate's FOURTH job, which shipped with task_status_fence and arrived unguarded.
//
// The gate's first three jobs are one saved round trip, a second question, and
// defense in depth - none of them observable, which is why handleTaskStatus's
// own comment records that the gate's discriminating power moved into SQL and
// that no test discriminates it. This slice then added a property that depends
// on it: conflicting_total is documented as ATTRIBUTABLE TO THE TASK'S OWN
// ASSIGNEE. That property has a subject and a test can have one too.
//
// MEASURED: with the three-term gate deleted, internal/worker, internal/api and
// cmd/relay-server all stay green while a registered peer naming a task it does
// not own drives Conflicting one per message, unbudgeted and forever - the key
// the README calls "the actionable number", moved by an unrelated agent. Task
// STATE stays correct throughout (both statements carry their own worker_id
// predicate), so nothing functional reddens. That is the whole point: the SQL
// identity predicate protects the ROW, and only this Go gate protects the
// COUNTER.
//
// THE POSITIVE CONTROL IS ON THE SAME HANDLER and it is not decoration: without
// it a handler mutated into rejecting everything - a `return` at the top of the
// function - passes the first leg outright.
func TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	// The task is assigned to statusWorkerID at epoch 7 and the coordinator has
	// already stamped it timed_out, so an accepted `done` report here classifies
	// as CONFLICTING - the one key an operator is told to act on.
	h, db := newStatusHandler(t, "timed_out", 0, 0, pgx.ErrNoRows)
	lim := newIngestLogLimiter(&h.ingestDrops)

	// LEG 1, THE POISONED INPUT, AND IT RUNS FIRST: a different registered peer
	// sends the same message the assignee would. One thousand of them, because
	// the exposure is not "one forged count" - nothing rate-limits a status
	// message and the gate is the only thing between a peer and unbounded
	// inflation of somebody else's number.
	const forged = 1000
	for i := 0; i < forged; i++ {
		h.handleTaskStatus(ctx, statusOtherWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	}
	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"%d status reports from a peer that is NOT this task's assignee moved a fence counter. The "+
			"identity gate is what makes these numbers attributable: a peer that can name any task id "+
			"can otherwise manufacture conflicting_total at one message each, and the README's "+
			"prescribed response to a climbing conflicting_total is to RAISE "+
			"RELAY_TASK_WATCHDOG_MARGIN - widening the unbounded-assignment window the watchdog exists "+
			"to close.", forged)
	calls := db.callsSnapshot()
	require.Contains(t, calls, "GetTask",
		"fixture: control must reach the identity gate. Without this the leg above could be satisfied "+
			"by a return at the task-id parse, which is a different gate entirely.")
	require.NotContains(t, calls, "UpdateTaskStatus",
		"a non-assignee's report must be dropped a round trip BEFORE the write, not merely refused by "+
			"the write's own worker_id predicate: it is reaching the write that reaches the counter.")

	// LEG 2, THE POSITIVE CONTROL, ON THE SAME HANDLER: the assignee sends the
	// identical message and it IS counted. This is what makes the zero above a
	// statement about identity rather than about the handler doing nothing.
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Equal(t, TaskStatusFenceCounts{Conflicting: 1}, h.TaskStatusFenceRejections(),
		"the ASSIGNEE's own report of a conflicting outcome must still be counted - that is the signal "+
			"this section exists to carry. A zero here means the first leg proved nothing.")
	require.Contains(t, db.callsSnapshot(), "UpdateTaskStatus",
		"fixture: the assignee's message must reach the write")

	require.Equal(t, "", logged(),
		"a message dropped at the identity gate must emit no log line of any wording: it is "+
			"attacker-keyed volume on the recv goroutine with no sink to send it to")
}

// TestHandleTaskStatus_AZeroValueWorkerCannotMoveTheCountersOnANeverClaimedTask
// is the NULL-rejection half of the same gate, and it is the test
// handleTaskStatus's comment said did not exist.
//
// The two .Valid terms are mutually redundant with the Bytes comparison against
// a real worker id, so their whole value is this case: pgtype.UUID is a
// comparable struct, so with BOTH .Valid checks dropped a zero-value workerID
// compares EQUAL to a never-claimed task's NULL worker_id - the Go form of SQL's
// IS NOT DISTINCT FROM - and the gate fails OPEN. Removing either term alone
// leaves the hole closed; removing both opens it, which is why this test drives
// the pair rather than each one.
//
// It has no same-handler positive control by construction - the fixture's task
// is assigned to NOBODY, so no caller passes the gate - so the reached-the-gate
// marker below is what stops it passing vacuously, and the counted case is
// established next door in TestHandleTaskStatus_OnlyTheAssigneeMovesTheFenceCounters.
func TestHandleTaskStatus_AZeroValueWorkerCannotMoveTheCountersOnANeverClaimedTask(t *testing.T) {
	ctx := context.Background()

	db := &stubStatusDB{
		task: store.Task{
			ID:              statusTaskIDUUID(t),
			JobID:           pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			Status:          "timed_out",
			WorkerID:        pgtype.UUID{}, // never claimed: worker_id IS NULL
			AssignmentEpoch: 7,
		},
		writeErr: pgx.ErrNoRows,
	}
	h := &Handler{q: store.New(db), broker: events.NewBroker()}
	lim := newIngestLogLimiter(&h.ingestDrops)

	h.handleTaskStatus(ctx, pgtype.UUID{}, lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))

	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"a caller that lost its identity drove a counter on a task NOBODY is assigned to. Both .Valid "+
			"terms of the identity gate are what reject this: a zero-value pgtype.UUID equals a NULL "+
			"worker_id under Go's struct comparison, so dropping both makes the gate an "+
			"IS NOT DISTINCT FROM and it fails OPEN.")
	calls := db.callsSnapshot()
	require.Contains(t, calls, "GetTask", "fixture: control must reach the identity gate")
	require.NotContains(t, calls, "UpdateTaskStatus",
		"the write must never be reached, or the counter is one SQL predicate away from moving")
}

// TestHandleTaskStatus_ARealDatabaseErrorIsNotAFenceRejection is the poisoned
// input in its own test, and it is what kills a record() written ABOVE the
// errors.Is check.
func TestHandleTaskStatus_ARealDatabaseErrorIsNotAFenceRejection(t *testing.T) {
	h, _ := newStatusHandler(t, "running", 0, 0,
		errors.New(`ERROR: could not serialize access due to concurrent update (SQLSTATE 40001)`))
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	h.handleTaskStatus(context.Background(), statusWorkerID(), lim,
		statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))

	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"a REAL database error is a different arm with a different meaning. A record() placed above the "+
			"errors.Is check counts every database error and makes the number unreadable: the whole "+
			"value of this section is that it means the FENCE refused something.")
	require.Contains(t, logged(), "handleTaskStatus UpdateTaskStatus",
		"fixture: the other arm still logs, so this test is exercising it rather than falling through")
}

// TestTaskStatusFenceCounters_ConcurrentRejectionsAreExact is what makes
// "atomics, not a mutex" a checked decision rather than a comment: in production
// every connection has its own recv goroutine and they all write these three
// words.
//
// EACH GOROUTINE HAS ITS OWN Handler-FREE FIXTURE? NO - deliberately the
// opposite. They share ONE Handler, because that is the production shape (one
// Handler per process, one limiter per connection) and it is the only
// arrangement in which the mutation this test exists for is observable.
//
// WHAT KILLS WHAT, and the halves are not equally strong. The mutation is
// statusFenceCounters.n changed from atomic.Uint64 to a plain uint64 with `++`,
// WITH the .Load() calls in snapshot dropped to match - leave them in and the
// "kill" is a compile error, which measures nothing. The -race half kills
// through happens-before analysis and does not need true parallelism; the
// exactness half only catches a lost update when two goroutines interleave
// inside the read-modify-write and is inert at GOMAXPROCS=1. Both are live in
// CI, which runs `go test -race ./...` on 2-4 vCPUs.
//
// MEASURED, NOT REASONED (M6 of this slice's mutation matrix; 10 runs of this
// test per configuration, mutation as described above). THE FOUR ROWS ARE NOT
// ALL FROM THE SAME MACHINE, which is why each carries a footnote: the no-race
// rows are this Windows host, the -race rows are the Linux container lane,
// because -race does not run here at all - see [2].
//
//	                    unmutated        mutated
//	no -race, -cpu=1    0/10 failures    0/10 failures            <- INERT
//	no -race, -cpu=2    0/10 failures    3/10 to 7/10 failures    [1]
//	-race,    -cpu=1    0/10 failures    10/10 failures, 10/10 DATA RACE  [2]
//	-race,    -cpu=2    0/10 failures    10/10 failures, 10/10 DATA RACE  [2]
//
// [1] READ THAT CELL AS A RANGE, NOT AS A PROPERTY OF THE MUTATION. Whether two
// goroutines interleave inside a read-modify-write depends on what else the
// machine is doing, and it moves: three independent measurements of the SAME
// mutation in the SAME configuration on this host came out 7/10, 3/10 and 4/10.
// A future run landing outside 3-7 is a different machine load, not a
// regression. The cell that carries the claim is the INERT one, and it
// reproduced at 0/10 every time it was measured.
//
// [2] NOT MEASURABLE ON THIS HOST, AND MEASURED SOMEWHERE ELSE. ThreadSanitizer
// fails to allocate on this Windows host - `ThreadSanitizer failed to allocate
// ... (error code: 87)` - for every package including unmutated ones, so -race
// says nothing here and its failure is environmental rather than a finding.
// These two rows were first taken 2026-08-23 and then REPRODUCED 2026-08-24 in
// the Linux container lane (golang:1.26, `go test -race`), which returned
// 10/10 failures and 10/10 DATA RACE at BOTH -cpu=1 and -cpu=2 against an
// unmutated 0/10 baseline in the same container. Unlike [1] these cells are
// deterministic, which is the reason the -race half is what the comment above
// calls the strong one: it kills through happens-before analysis and does not
// need two goroutines to actually collide. Re-measure in that lane, not on this
// host.
//
// The unmutated column is the green baseline: uniform results across the four
// configurations would have meant the harness was broken rather than that the
// coverage was good. The inert cell is the point - the exactness half alone
// would have been a coverage claim this host could not cash at GOMAXPROCS=1,
// and -race is what makes the kill deterministic. Do not drop either half.
func TestTaskStatusFenceCounters_ConcurrentRejectionsAreExact(t *testing.T) {
	h, _ := newStatusHandler(t, "timed_out", 0, 0, pgx.ErrNoRows)
	const goroutines, each = 8, 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One limiter per goroutine, exactly as there is one per connection.
			lim := newIngestLogLimiter(&h.ingestDrops)
			for i := 0; i < each; i++ {
				h.handleTaskStatus(context.Background(), statusWorkerID(), lim,
					statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
			}
		}()
	}
	wg.Wait()

	require.Equal(t, TaskStatusFenceCounts{Conflicting: goroutines * each}, h.TaskStatusFenceRejections(),
		"every rejection from every connection must land, and all of them under ONE reason. A plain "+
			"uint64 here loses counts silently and is a data race -race can see.")
}

// TestHandleTaskStatus_AWriteFailureFloodIsBoundedAndCountedPerSite is item 2's
// own Done-When: a flood of status updates whose write fails must produce at
// most the burst plus the refill rate of log lines, and every drop must be
// counted under the site that lost it.
//
// THE THREE SITES ARE ASSERTED SEPARATELY. One shared kind would have been one
// JSON key; three is the decision recorded in the logKind block, and this is
// what makes it checkable - a mutation that points two sites at one kind leaves
// one number at zero here.
func TestHandleTaskStatus_AWriteFailureFloodIsBoundedAndCountedPerSite(t *testing.T) {
	ctx := context.Background()
	dbErr := errors.New(`ERROR: canceling statement due to statement timeout (SQLSTATE 57014)`)

	// SITE 1: the UpdateTaskStatus arm.
	h, _ := newStatusHandler(t, "running", 0, 0, dbErr)
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)
	const flood = 100
	for i := 0; i < flood; i++ {
		h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	}
	require.Equal(t, 1, strings.Count(logged(), "handleTaskStatus UpdateTaskStatus"),
		"this kind carries NO wire value, so the flood is ONE key and one line. Before this slice it "+
			"was %d lines, one per message, at whatever rate the agent chose to send.", flood)
	got := h.IngestLogDropCounts()
	require.Equal(t, uint64(flood-1), got.Deduped.StatusUpdateWrite,
		"the %d messages folded into that one line must be COUNTED. Until this slice ingest_log_budget "+
			"read all zeros while these lines carried the volume - a control reporting zero is worse "+
			"than one reporting nothing.", flood-1)
	require.Zero(t, got.Deduped.StatusRetryWrite, "the count must be attributed to the RIGHT site")
	require.Zero(t, got.Deduped.StatusFailDependents)
	require.Equal(t, TaskStatusFenceCounts{}, h.TaskStatusFenceRejections(),
		"A REAL DATABASE ERROR IS NOT A FENCE REJECTION. The two arms are the subjects of two separate "+
			"items and no input executes both; this assertion is what keeps them disjoint.")

	// SITE 2: the retry arm.
	h, _ = newStatusHandler(t, "running", 3, 0, dbErr)
	lim = newIngestLogLimiter(&h.ingestDrops)
	logged = captureUnitLog(t)
	for i := 0; i < flood; i++ {
		h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	}
	require.Equal(t, 1, strings.Count(logged(), "handleTaskStatus IncrementTaskRetryCount"))
	require.Equal(t, uint64(flood-1), h.IngestLogDropCounts().Deduped.StatusRetryWrite)

	// SITE 3: FailDependentTasks, which is reached only AFTER a successful
	// UpdateTaskStatus and is NOT gated on pgx.ErrNoRows at all - it is an
	// :exec, so ErrNoRows is not a shape it can return.
	db := &stubStatusDB{
		task: store.Task{
			ID: statusTaskIDUUID(t), JobID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			Status: "running", WorkerID: statusWorkerID(), AssignmentEpoch: 7,
		},
		execErr: errors.New(`ERROR: deadlock detected (SQLSTATE 40P01)`),
	}
	h = &Handler{q: store.New(db), broker: events.NewBroker()}
	lim = newIngestLogLimiter(&h.ingestDrops)
	logged = captureUnitLog(t)
	for i := 0; i < flood; i++ {
		h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	}
	require.Equal(t, 1, strings.Count(logged(), "handleTaskStatus FailDependentTasks"),
		"the recursive CTE is the most expensive statement on this path and the first to deadlock under "+
			"contention; its line was outside the budget entirely")
	require.Equal(t, uint64(flood-1), h.IngestLogDropCounts().Deduped.StatusFailDependents)
}

// TestHandleTaskStatus_TheSilentArmsSpendNoBudget closes a MUTATION SURVIVOR
// found by this slice's own matrix (M18), and it is about the ORDER of the two
// operands of an `&&`, not about anything either operand does on its own.
//
// Two sites in this function guard a budgeted log line with a short-circuit:
//
//	if !errors.Is(err, pgx.ErrNoRows) && lim.allow(...)     // GetTask
//	if err := ...; err != nil && lim.allow(...)             // FailDependentTasks
//
// SWAPPING EITHER PAIR COMPILES, VETS CLEAN, CHANGES NO LOG LINE, AND LEFT THE
// WHOLE MODULE GREEN - measured, not hypothesised. What it changes is who pays:
// with lim.allow first, the cheapest message an unauthenticated-ish peer can
// send (a well-formed uuid naming no task) SPENDS a token and claims a dedupe
// slot on every call, and so does every SUCCESSFUL dependency cascade. The
// budget is 16 tokens refilling at 6/min for the whole connection, shared across
// every kind, so draining it there silences the diagnostics that matter -
// which is the exact failure mode the limiter exists to prevent, reintroduced by
// an operand swap.
//
// It also corrupts the numbers: ingest_log_budget.counts.deduped.status_get_task
// would climb for a kind that never logged anything, so an operator reading
// "these lines are being folded" would be reading an event that produced no
// line at all.
//
// THE POISONED INPUT IS FIRST, and it is the point of the test rather than
// setup. The positive control after it is what proves the limiter was still
// working - without it, a limiter broken into always-refusing would pass every
// assertion above.
func TestHandleTaskStatus_TheSilentArmsSpendNoBudget(t *testing.T) {
	ctx := context.Background()
	logged := captureUnitLog(t)

	// SITE 1: GetTask returning pgx.ErrNoRows - the task does not exist. Dropped
	// SILENTLY by design, so it must not touch the budget either.
	h, _ := newStatusHandler(t, "running", 0, 0, nil)
	h.q = store.New(&stubStatusDB{getErr: pgx.ErrNoRows})
	lim := newIngestLogLimiter(&h.ingestDrops)
	const flood = 100
	for i := 0; i < flood; i++ {
		h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	}
	require.Equal(t, ingestLogBurst, lim.tokens,
		"a silently-dropped message must not spend a token. The whole bucket is 16 for the connection "+
			"across every kind, so a peer that can drain it by naming tasks that do not exist has "+
			"silenced every other diagnostic on that stream.")
	require.Empty(t, lim.seen,
		"nor may it claim a dedupe slot: a key recorded for a line that was never emitted suppresses "+
			"the FIRST REAL occurrence of that kind for a whole dedupe window")
	require.Equal(t, IngestLogDrops{}, h.IngestLogDropCounts(),
		"and nothing may be COUNTED as dropped, because nothing was dropped - no line was ever a "+
			"candidate. A non-zero status_get_task here means the number is reporting events that "+
			"produced no log line at all.")

	// SITE 2: a SUCCESSFUL FailDependentTasks on the same shape of guard. The
	// cascade runs on every terminal task in a healthy fleet, so a swap here
	// spends a token per completed task.
	db := &stubStatusDB{task: store.Task{
		ID: statusTaskIDUUID(t), JobID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		Status: "running", WorkerID: statusWorkerID(), AssignmentEpoch: 7,
	}}
	h2 := &Handler{q: store.New(db), broker: events.NewBroker()}
	lim2 := newIngestLogLimiter(&h2.ingestDrops)
	for i := 0; i < flood; i++ {
		h2.handleTaskStatus(ctx, statusWorkerID(), lim2, statusUpdate(relayv1.TaskStatus_TASK_STATUS_FAILED))
	}
	require.Contains(t, db.callsSnapshot(), "FailDependentTasks",
		"fixture: control must reach the cascade, or the assertion below is vacuous")
	require.Equal(t, ingestLogBurst, lim2.tokens,
		"a SUCCEEDING statement must not spend a log token. Every terminal task in a healthy fleet "+
			"runs this cascade, so this is not an adversarial case - it is the common one.")
	require.Empty(t, lim2.seen)
	require.Equal(t, IngestLogDrops{}, h2.IngestLogDropCounts())

	require.Equal(t, "", logged(), "neither site logs on these inputs")

	// POSITIVE CONTROL, on the SAME limiter as site 1: a genuine infrastructure
	// failure at that same site must still get its line out of the full bucket.
	h.q = store.New(&stubStatusDB{getErr: errors.New(`ERROR: server closed the connection unexpectedly`)})
	h.handleTaskStatus(ctx, statusWorkerID(), lim, statusUpdate(relayv1.TaskStatus_TASK_STATUS_DONE))
	require.Contains(t, logged(), "handleTaskStatus GetTask",
		"POSITIVE CONTROL: the budget must still be spendable. Without this, a limiter mutated into "+
			"always-refusing satisfies every assertion above.")
	require.Equal(t, ingestLogBurst-1, lim.tokens, "and exactly one token buys exactly one line")
}
