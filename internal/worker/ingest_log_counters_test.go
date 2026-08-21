package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIngestLogKindsAreADenseRunFromOne pins the property ingestLogCounters'
// array depends on: the kinds are 1, 2, 3, ... with kindCount immediately after
// the last one.
//
// This is the LAST rung of the guard ladder (match a shape) and it is used here
// because the property is one the compiler cannot express. It is load-bearing
// anyway: ingestLogCounters.record fails CLOSED on an out-of-range kind rather
// than panicking, because a panic on the gRPC recv goroutine kills the process -
// so a sparse or renumbered kind is a SILENT loss of that kind's counts, which
// is the exact defect this whole slice exists to close.
func TestIngestLogKindsAreADenseRunFromOne(t *testing.T) {
	run := []logKind{
		kindTaskLogPersist,
		kindBadTaskIDLog,
		kindBadTaskIDStatus,
		kindStatusGetTask,
		kindInventory,
	}
	for i, k := range run {
		require.Equal(t, logKind(i+1), k,
			"kind #%d is %d. The kinds index ingestLogCounters' array, so they must stay a DENSE RUN "+
				"starting at 1: a gap makes record() drop that kind's counts silently.", i, k)
	}
	require.Equal(t, logKind(len(run)+1), kindCount,
		"kindCount must be the sentinel immediately after the last kind: it is the LENGTH of the "+
			"counters array, so a kind at or beyond it is never counted")
}

// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished counts a PROPERTY
// rather than matching a spelling: every `kind:` expression in every logKey
// composite literal in the package's non-test sources must be one of the kind
// constants declared inside the sentinel.
//
// It parses the PACKAGE, not one file, and it resolves const types the way Go
// does (a ConstSpec with no type and no values inherits the previous spec's
// type), so these evasions are all RED:
//
//   - a sixth kind declared in a SEPARATE const block;
//   - a sixth kind declared in a SIBLING FILE of the same package;
//   - a sixth kind declared AFTER kindCount (the dense-run test above cannot
//     see that one: kindCount stays kindInventory+1 and everything still lines
//     up);
//   - `logKey{kind: someLocalVariable}`, which is not a counted constant and so
//     fails closed rather than being skipped.
//
// THE KNOWN RESIDUAL, stated so the next reader does not assume it is covered:
// an UNTYPED `kindFoo = 9` in the same block is not a logKind constant to this
// walk, so declaring one and using it fails here (good) but for the "not a
// counted constant" reason rather than the "outside the sentinel" reason. The
// failure message says both.
func TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	declared := map[string]bool{}
	var literalKinds []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				typ := ""
				for _, s := range gd.Specs {
					vs, ok := s.(*ast.ValueSpec)
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
					if typ != "logKind" {
						continue
					}
					for _, n := range vs.Names {
						declared[n.Name] = true
					}
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != "logKey" {
					return true
				}
				for _, e := range cl.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "kind" {
						continue
					}
					id, ok := kv.Value.(*ast.Ident)
					require.True(t, ok,
						"a logKey literal names its kind as %T. It must name one of the logKind "+
							"constants directly, or this guard cannot tell whether that kind is counted.",
						kv.Value)
					literalKinds = append(literalKinds, id.Name)
				}
				return true
			})
		}
	}

	require.Equal(t, int(kindCount), len(declared),
		"the package declares %d logKind constants but kindCount is %d. Every kind must be declared "+
			"INSIDE the sentinel run: one declared after kindCount, or with an explicit out-of-run "+
			"value, is never counted and never published, and record() drops it in silence.",
		len(declared), int(kindCount))
	require.NotEmpty(t, literalKinds, "this walk found no logKey literals at all, so it proved nothing")
	for _, name := range literalKinds {
		require.True(t, declared[name],
			"logKey{kind: %s} names something that is not a logKind constant declared inside the "+
				"sentinel run. Its drops are counted into no cell and published under no JSON key.", name)
	}
}

// kindFieldValues returns the by-kind struct's fields by name, so the mapping
// test below asserts on a SET of values rather than on five hand-written
// equalities that a crossed assignment could satisfy in pairs.
func kindFieldValues(t *testing.T, v IngestLogDropsByKind) []uint64 {
	t.Helper()
	rv := reflect.ValueOf(v)
	out := make([]uint64, 0, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		out = append(out, rv.Field(i).Uint())
	}
	return out
}

// TestIngestLogCounters_EveryKindIsPublishedDistinctly drives every (kind, arm)
// cell a DIFFERENT number of times and then requires the published struct to
// carry exactly those numbers, per arm.
//
// Distinct values per cell are what make this discriminating. Equal values would
// pass under a crossed field assignment, an off-by-one index, or a snapshot that
// read the same arm twice. Asserting the two arms SEPARATELY is what catches an
// arm swap: the combined multiset is identical either way.
func TestIngestLogCounters_EveryKindIsPublishedDistinctly(t *testing.T) {
	var c ingestLogCounters

	var wantDeduped, wantSuppressed []uint64
	n := uint64(1)
	for k := logKind(1); k < kindCount; k++ {
		for _, arm := range []int{ingestDropDeduped, ingestDropSuppressed} {
			for i := uint64(0); i < n; i++ {
				c.record(k, arm)
			}
			if arm == ingestDropDeduped {
				wantDeduped = append(wantDeduped, n)
			} else {
				wantSuppressed = append(wantSuppressed, n)
			}
			n++
		}
	}

	snap := c.snapshot()
	require.ElementsMatch(t, wantDeduped, kindFieldValues(t, snap.Deduped),
		"every kind must publish its OWN deduped cell. A missing value means a kind is counted but "+
			"never published; a duplicated value means two fields read one cell; a shifted set means "+
			"the array is indexed off by one.")
	require.ElementsMatch(t, wantSuppressed, kindFieldValues(t, snap.Suppressed),
		"the suppressed half must read the suppressed arm. Swapping the two arms leaves the COMBINED "+
			"multiset unchanged, which is why this assertion is per half.")
	require.Len(t, wantDeduped, reflect.TypeOf(IngestLogDropsByKind{}).NumField(),
		"there are %d kinds inside the sentinel and %d published fields. A kind with no field is "+
			"counted into a cell nobody reads.", len(wantDeduped),
		reflect.TypeOf(IngestLogDropsByKind{}).NumField())
}

// TestIngestLogCounters_AnOutOfRangeKindIsDroppedNotPanicked. record's bounds
// check exists because the alternative on the gRPC recv goroutine is a panic
// that kills the process (Connect has no recover; grpc-go does not recover
// handler panics). It is unreachable while the two kind guards above are green,
// and this test exists so that "unreachable" does not mean "untested".
func TestIngestLogCounters_AnOutOfRangeKindIsDroppedNotPanicked(t *testing.T) {
	var c ingestLogCounters
	require.NotPanics(t, func() {
		c.record(logKind(0), ingestDropDeduped)
		c.record(kindCount, ingestDropDeduped)
		c.record(logKind(200), ingestDropSuppressed)
		c.record(kindInventory, 7)
		c.record(kindInventory, -1)
	})
	require.Equal(t, IngestLogDrops{}, c.snapshot(),
		"an out-of-range kind or arm must be dropped, not folded into some other cell")
}

// TestIngestLogLimiter_TheDedupeArmCountsDeduped. One key logged twice inside
// the dedupe window: the second call is folded into the first, and that is the
// arm an operator reads as "a repeating failure is being collapsed".
func TestIngestLogLimiter_TheDedupeArmCountsDeduped(t *testing.T) {
	l, _ := newFrozen()
	k := logKey{kind: kindTaskLogPersist, id: "task-a", epoch: 1}

	require.True(t, l.allow(k), "fixture: the first line is allowed")
	require.False(t, l.allow(k), "fixture: the second is deduped")

	got := l.drops.snapshot()
	require.Equal(t, uint64(1), got.Deduped.TaskLogPersist,
		"a deduped occurrence must increment the DEDUPED arm of its own kind")
	require.Equal(t, IngestLogDropsByKind{}, got.Suppressed,
		"a deduped occurrence must not touch the suppressed arm: the two mean different things - "+
			"one is a healthy collapse, the other is an attack or a misconfiguration")
}

// TestIngestLogLimiter_TheEmptyBucketCountsSuppressed. Distinct keys, so the
// dedupe arm never fires: the burst is spent and every later line is dropped
// entirely.
func TestIngestLogLimiter_TheEmptyBucketCountsSuppressed(t *testing.T) {
	l, _ := newFrozen()
	key := func(i int) logKey {
		return logKey{kind: kindTaskLogPersist, id: "task", epoch: int64(i)}
	}
	for i := 0; i < ingestLogBurst; i++ {
		require.True(t, l.allow(key(i)), "fixture: the burst must be spendable")
	}
	require.False(t, l.allow(key(ingestLogBurst)), "fixture: the bucket is now empty")
	require.False(t, l.allow(key(ingestLogBurst+1)))

	got := l.drops.snapshot()
	require.Equal(t, uint64(2), got.Suppressed.TaskLogPersist,
		"a line dropped for lack of a token must increment the SUPPRESSED arm")
	require.Equal(t, IngestLogDropsByKind{}, got.Deduped,
		"none of these keys repeated, so nothing was deduped")
}

// TestIngestLogLimiter_AnAllowedLineCountsNothing. The counter is a DROP
// counter. An increment on the allowed path would make every number here the
// message count, which is the one thing the log line already tells you.
func TestIngestLogLimiter_AnAllowedLineCountsNothing(t *testing.T) {
	l, _ := newFrozen()
	for k := logKind(1); k < kindCount; k++ {
		require.True(t, l.allow(logKey{kind: k}), "fixture: first line of each kind is allowed")
	}
	require.Equal(t, IngestLogDrops{}, l.drops.snapshot(),
		"an ALLOWED line is not a drop")
}

// TestIngestLogLimiter_TheNilArmCountsNothing.
//
// SAY WHAT THIS DOES AND DOES NOT BUY. The nil arm is fail-closed and
// deliberately unreachable in production, and NO EVENT WAS SUPPRESSED there
// because there was no limiter - counting it would count a phantom. What
// actually prevents the count is structural: the counters are reached through
// the limiter, so a nil receiver has nothing to increment. This test therefore
// kills exactly one mutation - adding a package-level fallback counter that the
// snapshot also reads - and NOT the general claim. Keep it for that one.
func TestIngestLogLimiter_TheNilArmCountsNothing(t *testing.T) {
	var h Handler
	var l *ingestLogLimiter
	require.False(t, l.allow(logKey{kind: kindBadTaskIDLog}))
	require.Equal(t, IngestLogDrops{}, h.IngestLogDropCounts(),
		"the l == nil arm suppressed no event and must count nothing anywhere")
}

// TestIngestLogCounters_TwoLimitersOnOneHandlerAggregate is the item's headline
// property at the cheapest layer that can express it: THE COUNT OUTLIVES THE
// CONNECTION. Two independent budgets, one Handler, one set of numbers.
//
// It does NOT prove that Connect passes the Handler's counters rather than a
// fresh set - that needs a real stream and lives in
// TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections in the
// integration lane.
func TestIngestLogCounters_TwoLimitersOnOneHandlerAggregate(t *testing.T) {
	var h Handler
	a := newIngestLogLimiter(&h.ingestDrops)
	b := newIngestLogLimiter(&h.ingestDrops)
	k := logKey{kind: kindInventory}

	for _, l := range []*ingestLogLimiter{a, b} {
		require.True(t, l.allow(k))
		require.False(t, l.allow(k))
		require.False(t, l.allow(k))
	}

	require.Equal(t, uint64(4), h.IngestLogDropCounts().Deduped.Inventory,
		"two connections' drops must land in ONE process-lifetime counter. Per-connection "+
			"accumulation flushed at teardown was refuted at spec time: it reports nothing at all "+
			"for as long as the flood continues.")
}

// TestIngestLogCounters_TwoHandlersDoNotShareCounts pins the choice of home
// against the one the spec proposed (a package-level array). A global would make
// every exact-count assertion in this package order-dependent on every other
// test in the binary.
func TestIngestLogCounters_TwoHandlersDoNotShareCounts(t *testing.T) {
	var a, b Handler
	l := newIngestLogLimiter(&a.ingestDrops)
	k := logKey{kind: kindStatusGetTask}
	require.True(t, l.allow(k))
	require.False(t, l.allow(k))

	require.Equal(t, uint64(1), a.IngestLogDropCounts().Deduped.StatusGetTask)
	require.Equal(t, IngestLogDrops{}, b.IngestLogDropCounts(),
		"counters are per Handler. Production has exactly one Handler, so that is process-wide "+
			"there; a package-level array would make every test in this binary share these numbers.")
}
