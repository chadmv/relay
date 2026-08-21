package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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
