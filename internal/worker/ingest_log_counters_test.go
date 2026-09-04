package worker

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
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
		kindStatusRetryWrite,
		kindStatusUpdateWrite,
		kindStatusFailDependents,
		kindStatusLogPersist,
	}
	for i, k := range run {
		require.Equal(t, logKind(i+1), k,
			"kind #%d is %d. The kinds index ingestLogCounters' array, so they must stay a DENSE RUN "+
				"starting at 1: a gap makes record() drop that kind's counts silently.", i, k)
	}
	// THE MESSAGE NAMES THE TEST'S OWN LIST FIRST, because that is the likelier
	// cause and the one a correctly-added sixth kind produces. A kind added
	// properly - inside the sentinel, with kindCount still immediately after it -
	// fails HERE with 6 != 7 under a message that says "kindCount must be the
	// sentinel immediately after the last kind", which it is. The list above is
	// hardcoded and is what actually went stale. It is kept hardcoded rather than
	// derived from kindCount because deriving it would delete the only thing that
	// pins each NAME to its VALUE.
	require.Equal(t, logKind(len(run)+1), kindCount,
		"this test's `run` list has %d entries and kindCount is %d. IF YOU JUST ADDED A KIND, ADD IT "+
			"TO `run` ABOVE - this assertion compares the hardcoded list's length to the sentinel, so "+
			"a correctly-added kind fails here first. OTHERWISE kindCount is no longer the sentinel "+
			"immediately after the last kind: it is the LENGTH of the counters array, so a kind at or "+
			"beyond it is never counted.", len(run), int(kindCount))
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
				// EVERY ELEMENT MUST BE KEYED, AND `kind` MUST BE ONE OF THEM.
				// This loop used to `continue` past anything that was not a
				// KeyValueExpr, which made the whole walk blind to the
				// POSITIONAL form: `logKey{someLocalVar, "", 0}` has plain
				// expressions for elements, so every assertion below was
				// skipped and the site was never checked at all. Measured -
				// that literal with an uncounted kind compiled, vetted clean
				// and left the package green, while consuming the shared
				// per-connection token bucket and having every drop it caused
				// discarded by record's fail-closed branch.
				//
				// require.NotEmpty on literalKinds does not save it: the four
				// untouched keyed sites satisfy that on their own.
				//
				// An ABSENT kind key is the same hole one step quieter -
				// `logKey{id: x}` leaves kind at 0, which record drops - so it
				// is required rather than merely checked when present.
				sawKind := false
				for _, e := range cl.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					require.True(t, ok,
						"a logKey literal at %s uses the POSITIONAL form (%T). Write it keyed - "+
							"logKey{kind: ...} - or this guard cannot see which kind it names, and an "+
							"uncounted kind there spends the connection's token bucket while every drop "+
							"it causes is discarded by record's fail-closed branch.",
						fset.Position(e.Pos()), e)
					k, ok := kv.Key.(*ast.Ident)
					require.True(t, ok, "a logKey literal at %s keys a field as %T", fset.Position(kv.Pos()), kv.Key)
					if k.Name != "kind" {
						continue
					}
					sawKind = true
					id, ok := kv.Value.(*ast.Ident)
					require.True(t, ok,
						"a logKey literal names its kind as %T. It must name one of the logKind "+
							"constants directly, or this guard cannot tell whether that kind is counted.",
						kv.Value)
					literalKinds = append(literalKinds, id.Name)
				}
				require.True(t, sawKind,
					"a logKey literal at %s names no kind, so the field defaults to 0 - which is not a "+
						"kind, is published under no JSON key, and is exactly what record's `i <= 0` "+
						"branch drops in silence.", fset.Position(cl.Pos()))
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

	// ORDERED, NOT ElementsMatch. wantDeduped is built in KIND order and
	// kindFieldValues returns FIELD order, so the association each field reads
	// its own kind's cell is exactly the pairing of the two orders - and an
	// order-insensitive match discards it. Measured: swapping the StatusGetTask
	// and Inventory lines in byKind so each reads the other's cell left this test
	// GREEN under ElementsMatch. (The package still reddened, via
	// TwoLimitersOnOneHandlerAggregate and TwoHandlersDoNotShareCounts, so there
	// was no shipped hole - but the test named for the property was not the one
	// catching it, and its comment said otherwise.)
	snap := c.snapshot()
	require.Equal(t, wantDeduped, kindFieldValues(t, snap.Deduped),
		"every kind must publish its OWN deduped cell, IN ORDER: field i of IngestLogDropsByKind must "+
			"read kind i+1. A missing value means a kind is counted but never published; a duplicated "+
			"value means two fields read one cell; a permutation means two fields are crossed.")
	require.Equal(t, wantSuppressed, kindFieldValues(t, snap.Suppressed),
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
//
// IT ASSERTS ON THE WHOLE ARRAY, NOT ON THE SNAPSHOT, and that is not
// thoroughness for its own sake - the snapshot version SURVIVED a mutation.
// Slot 0 exists (the kinds start at 1) and no published field reads it, so
// relaxing the guard from `i <= 0` to `i < 0` files kind 0 into a cell nobody
// looks at: no panic, no visible number, and a snapshot-only assertion stays
// green. Reading c.n directly is what makes the message's own claim - "not
// folded into SOME OTHER CELL" - checkable.
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
	for k := range c.n {
		for arm := range c.n[k] {
			require.Zero(t, c.n[k][arm].Load(),
				"cell [%d][%d] is non-zero. Every call above was out of range, so NOTHING may have "+
					"been written anywhere in this array - including slot 0, which is unpublished and "+
					"therefore invisible to the snapshot assertion above.", k, arm)
		}
	}
}

// TestIngestLogCounters_ANilCounterSetIsDroppedNotPanicked. record's bounds
// check does NOT cover a nil receiver, and it reads as though it does: len(c.n)
// has an ARRAY-typed operand, so it is a compile-time constant and never
// dereferences c. An out-of-range kind on a nil receiver therefore returns
// harmlessly while an IN-RANGE one panics - the guard's own comment says "fail
// closed, do not panic", and the shape it fails on is the shape production takes.
//
// UNREACHABLE TODAY AND STILL WORTH THE REGISTER COMPARE. Connect,
// newIngestLogLimiter and shimLimiterFor all pass &h.ingestDrops, and Handler's
// field is a VALUE so its zero value works. But both shapes below compile:
// newIngestLogLimiterAt(now, nil) is an exported-to-the-package constructor with
// a nil-able parameter, and allow already guards `l == nil` without guarding
// `l.drops == nil`, which sets the reader expectation that this type tolerates
// being unwired. The failure it would produce is a nil dereference on the gRPC
// recv goroutine, which Connect does not recover and grpc-go does not recover -
// the single failure mode the whole file is written against.
func TestIngestLogCounters_ANilCounterSetIsDroppedNotPanicked(t *testing.T) {
	var c *ingestLogCounters
	require.NotPanics(t, func() {
		c.record(kindInventory, ingestDropDeduped)
		c.record(logKind(200), ingestDropSuppressed)
	}, "a nil counter set must lose the count, not kill the recv goroutine. The IN-RANGE kind is the "+
		"one that matters: the out-of-range one already survived, because len(c.n) is a constant and "+
		"the bounds check returns before any dereference.")

	// The limiter reached through a nil counter set must still BUDGET normally:
	// losing a count may not cost a diagnostic.
	l := newIngestLogLimiterAt(time.Now, nil)
	var first, second bool
	require.NotPanics(t, func() {
		first = l.allow(logKey{kind: kindInventory})
		second = l.allow(logKey{kind: kindInventory})
	}, "allow guards `l == nil` but not `l.drops == nil`; the second call takes the dedupe arm, which "+
		"is the arm that records")
	require.True(t, first, "an unwired counter set must not change what the budget allows")
	require.False(t, second, "the dedupe decision is independent of whether the drop can be counted")
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

// captureUnitLog redirects the standard logger for one test. The package's
// integration lane has its own captureLog in package worker_test; this is the
// default-lane twin. No test in this package calls t.Parallel, which is what
// makes a process-global redirect safe here.
func captureUnitLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf.String
}

// TestHandleTaskLog_ABadTaskIDFloodCountsTheDedupedArm drives the item's own
// Repro through the REAL handler: one connection, a flood of chunks whose task
// id does not parse. The operator-visible signature is 1 log line and 99 silent
// drops, and until this slice nothing anywhere said so.
//
// NO DATABASE AND NO BUILD TAG. handleTaskLog's bad-id arm returns before h.q is
// touched, so a bare &Handler{} is a complete fixture and this proof runs in the
// lane CI actually executes (go-ci runs `go test -race ./...` with no tag).
func TestHandleTaskLog_ABadTaskIDFloodCountsTheDedupedArm(t *testing.T) {
	h := &Handler{}
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	const flood = 100
	for i := 0; i < flood; i++ {
		h.handleTaskLog(context.Background(), pgtype.UUID{}, lim, &relayv1.TaskLogChunk{
			TaskId:  "not-a-uuid",
			Content: []byte("x"),
		})
	}

	require.Equal(t, 1, strings.Count(logged(), "handleTaskLog bad task id"),
		"fixture: this kind carries no wire value, so the flood is ONE key and one line")

	got := h.IngestLogDropCounts()
	require.Equal(t, uint64(flood-1), got.Deduped.BadTaskIDLog,
		"the 99 chunks folded into that one line must be counted. That number is the whole point of "+
			"this slice: without it, a flood is indistinguishable from a healthy fleet.")
	require.Zero(t, got.Suppressed.BadTaskIDLog, "nothing was budget-suppressed: the key repeated")
	require.Zero(t, got.Deduped.TaskLogPersist, "the count must be attributed to the RIGHT kind")
}

// TestHandleTaskStatus_ADroppedLineUnderAnEmptyBudgetCountsSuppressed is the
// other arm at the handler layer, and it is the arm that matters under attack.
// The bucket is drained by a DIFFERENT kind first, which is the realistic shape:
// the budget is per connection and shared across all five kinds, so one flooding
// site silences the others.
func TestHandleTaskStatus_ADroppedLineUnderAnEmptyBudgetCountsSuppressed(t *testing.T) {
	h := &Handler{}
	lim := newIngestLogLimiter(&h.ingestDrops)
	for i := 0; i < ingestLogBurst; i++ {
		require.True(t, lim.allow(logKey{kind: kindTaskLogPersist, id: "t", epoch: int64(i)}),
			"fixture: drain the whole burst through another kind")
	}

	logged := captureUnitLog(t)
	h.handleTaskStatus(context.Background(), pgtype.UUID{}, lim, &relayv1.TaskStatusUpdate{
		TaskId: "also-not-a-uuid",
	})

	require.Equal(t, "", logged(),
		"fixture: with an empty bucket the line is dropped entirely")
	got := h.IngestLogDropCounts()
	require.Equal(t, uint64(1), got.Suppressed.BadTaskIDStatus,
		"a line dropped for lack of a token is the arm that means attack or misconfiguration, and it "+
			"must be counted under the kind that lost it")
	require.Zero(t, got.Deduped.BadTaskIDStatus)
}

// TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact is what makes
// "atomics, not a mutex" a checked decision rather than a comment. Every
// goroutine owns its own limiter, exactly as every connection does, and all of
// them write the same cells.
//
// WHAT KILLS WHAT, MEASURED RATHER THAN ASSUMED, because a test can be robust
// and inert on the same machine. Every figure below is against the mutation this
// test exists for - ingestLogCounters.n changed from atomic.Uint64 to a plain
// uint64 with `++`, WITH the .Load() calls in this file dropped to match - run in
// the golang:1.26 Linux container. That last detail is load-bearing: leaving the
// .Load() calls in place makes every "kill" a COMPILE ERROR rather than a
// behavioural one, which measures nothing about this test.
//
// THE FIRST VERSION OF THIS PARAGRAPH GOT THE -race ROW BACKWARDS and is
// corrected here rather than quietly replaced, because the wrong version
// licensed a maintainer on a constrained runner to treat this test as proving
// nothing. It claimed 1/10 at -cpu=1 and called the test "very nearly INERT"
// there. Re-measured, that does not reproduce:
//
//   - THE -race RUN IS THE LOAD-BEARING HALF, and it is the one CI executes. It
//     kills the mutation through happens-before analysis, and it does NOT need
//     true parallelism to do it: 10/10 at -cpu=1, 10/10 at -cpu=2, and 10/10 at
//     -cpu=1 again inside `docker run --cpus=1 --cpuset-cpus=0`, every one of
//     them a WARNING: DATA RACE. TSan's vector clocks see two sibling goroutines
//     writing one word with no happens-before edge between them whether or not
//     the writes ever overlap in real time.
//   - THE EXACTNESS ASSERTION is the weaker half, and it is the half that is
//     inert at one CPU. It catches a lost update only when two goroutines
//     interleave inside the read-modify-write: 0/20 at -cpu=1, 12/20 at -cpu=2,
//     13/20 at -cpu=4. Green at GOMAXPROCS=1 means "did not detect", not
//     "verified" - FOR THIS ASSERTION ONLY. It is kept because it is the only
//     half that fails with a NUMBER an operator would care about.
//
// The battery has a green baseline: unmutated, the same harness gives 0/10 data
// races and 0/20 exactness failures, so the numbers above are discrimination
// rather than a broken harness.
//
// go-ci runs `go test -race ./... -timeout 180s` on ubuntu-latest with no -cpu
// flag and no build tag, and a GitHub-hosted runner has 2 or 4 vCPUs - the
// -race kill is 10/10 at every core count measured, so it is live there either
// way. Locally, -race must be run in a Linux container: ThreadSanitizer cannot
// allocate its shadow memory on the Windows authoring host and fails on
// untouched packages too.
//
// A busy-spin reader bounded by a done channel, rather than the fixed 5000
// iterations below, was tried and is WORSE: it starves the writers and drops the
// -cpu=2 race kill from 10/10 to 4/10. Do not "improve" it that way.
//
// The fixture assertions inside the goroutines are assert, NOT require: require
// calls t.FailNow, which is runtime.Goexit on whatever goroutine reaches it, and
// testify documents that as unsupported off the test goroutine.
func TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact(t *testing.T) {
	var h Handler
	const conns, perConn = 8, 200
	k := logKey{kind: kindInventory}

	var wg sync.WaitGroup
	for c := 0; c < conns; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := newIngestLogLimiter(&h.ingestDrops)
			assert.True(t, l.allow(k), "fixture: a fresh budget allows the first line of a kind")
			for i := 0; i < perConn; i++ {
				l.allow(k)
			}
		}()
	}

	// A concurrent READER as well, so -race sees both sides of the access.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			_ = h.IngestLogDropCounts()
		}
	}()
	wg.Wait()
	<-done

	require.Equal(t, uint64(conns*perConn), h.IngestLogDropCounts().Deduped.Inventory,
		"every drop must land exactly once. A short count here is a lost update, which is what a "+
			"plain uint64 increment produces under concurrency.")
}
