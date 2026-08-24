package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath is a STRUCTURAL
// guard, deliberately NOT integration-tagged so it runs in the plain
// `go test ./...` gate on every change.
//
// IncrementTaskRetryCount (query/tasks.sql) is the AGENT-DRIVEN retry. Its three
// predicates - assignment_epoch, worker_id, and
// status IN ('pending','dispatched','running') - are the exact inverse of an
// operator re-run's preconditions: POST /v1/jobs/{id}/retry reopens tasks that
// ARE terminal and has no worker identity to supply, so both the status and the
// worker predicate would reject every call it made. The symptom of that mistake
// is not a crash: it is "the endpoint silently does nothing", which a test
// asserting only a 200 would not catch.
//
// The operator path has its own statement, RetryJobTasks. This test keeps the
// two apart: sqlc re-exports IncrementTaskRetryCount on *store.Queries on every
// regeneration, so it is permanently one autocomplete away from the wrong site.
//
// If this goes RED, do not add an exception. Either the caller wants
// RetryJobTasks (the operator analogue of RequeueTaskByID), or it genuinely
// drives the agent-side retry budget and belongs in internal/worker/handler.go.
//
// The walk covers the whole module, not just internal/. cmd/relay-server/main.go
// wires the store layer up by hand and can reach *store.Queries as directly as
// any package under internal/, so a guard scoped to internal/ would not see a
// call site there at all.
//
// Known weakness, accepted: a rename defeats it. Same weakness and same trade as
// TestUpdateTaskStatusEpochHasNoProductionCaller.
//
// IT PARSES GO, AND IT USED TO BE A SUBSTRING MATCH. That is not a tidy-up: a
// substring match asks "does this text appear", and the question this guard
// exists to ask is "does this get CALLED". The two differ exactly where prose
// mentions the statement, and the old version PAID FOR THE DIFFERENCE WITH
// WHOLE-FILE EXEMPTIONS - internal/store/*.sql.go was skipped entirely because
// tasks.sql.go defines the method and jobs.sql.go names it in a comment. An
// exemption granted to a PATH is an exemption from every question, so those two
// generated files were the one place in the module where a hand-added call site
// would have been invisible to this guard. Both exemptions are now GONE, because
// an AST walk skips the defining FuncDecl's own name and never visits a comment
// at all.
//
// THE AST FORM IS NOT STRICTLY STRONGER, AND THE DIRECTION IT LOSES IS NAMED
// HERE RATHER THAN CLAIMED AWAY. An identifier inside a STRING LITERAL is an
// *ast.BasicLit, not an *ast.Ident, so
// `reflect.ValueOf(q).MethodByName("IncrementTaskRetryCount").Call(...)` is
// invisible to this walk where the substring version caught it - as would be a
// name assembled from pieces, or reached through any other reflective or
// generated indirection. The trade was taken knowingly: the substring form paid
// for its reach with two whole-file exemptions covering the one place a
// hand-added call site could hide, and reflection into *store.Queries is not a
// shape this codebase writes anywhere. If it ever does, this guard needs a
// second pass over BasicLits, not a return to substrings.
//
// It went RED on a comment in internal/scheduler/dispatch.go that enumerates
// relay's Go-side fence-rejection sites by statement name - prose that is
// load-bearing and not a call. Rewording that comment would have been
// the cheap fix and would have left the guard's real defect in place, one file
// exemption away from the next false positive.
//
// WHAT COUNTS AS A REFERENCE: any identifier in the syntax tree, not only a call
// expression. `f := q.IncrementTaskRetryCount` takes a method value and invokes
// it one line later, which a call-expression-only walk would miss.
func TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath(t *testing.T) {
	root := repoRoot(t)
	const ident = "IncrementTaskRetryCount"

	// The agent status path is its one legitimate caller.
	allowed := map[string]bool{
		filepath.Join(root, "internal", "worker", "handler.go"): true,
	}

	offenders, unparseable, err := scanForIdentReferences(root, ident, allowed)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	for _, f := range unparseable {
		// NAMED, NOT FATAL, AND THE WALK ALREADY CONTINUED PAST IT. A parse
		// failure means this guard did not read that file, which is a gap worth
		// reporting - but aborting the walk on it reported `walking <root>: ...`,
		// a message naming neither the guard nor its subject, and left the rest
		// of the module unscanned.
		t.Errorf("%s could not be parsed, so %s could be referenced there and this guard would not "+
			"see it. Every other file was still scanned.", f, ident)
	}

	if len(offenders) > 0 {
		t.Fatalf("%s is the AGENT-DRIVEN retry and must be called only from "+
			"internal/worker/handler.go, but it is REFERENCED IN CODE in: %v\n"+
			"An operator re-run (POST /v1/jobs/{id}/retry) must use RetryJobTasks: every "+
			"predicate on %s would reject it. See the note on the statement in "+
			"internal/store/query/tasks.sql.\n"+
			"This walk parses Go and never sees comments, so a hit here is a real reference, "+
			"not a mention.", ident, offenders, ident)
	}
}

// scanForIdentReferences walks root and reports which .go files REFERENCE ident
// in code, as repo-relative slash paths, plus the files it could not parse.
//
// IT IS A SEPARATE FUNCTION SO ITS OWN EDGE CASES ARE TESTABLE. As an inline
// closure over repoRoot, the only input it could ever be given was this
// repository, so two behaviours nothing in the module happens to exercise -
// pruning testdata, and surviving an unparseable file - were unasserted, and one
// of them was wrong.
func scanForIdentReferences(root, ident string, allowed map[string]bool) (offenders, unparseable []string, err error) {
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			// Prune trees that hold no module source. .claude is where this
			// repo's git worktrees live, so walking it would rediscover a second
			// copy of every allowed file under a path no allow-list can name.
			//
			// testdata AND the `_`/`.` prefixes are pruned by the SAME RULE THE
			// GO TOOLCHAIN USES, and that is why they belong here rather than in
			// the switch: the toolchain deliberately ignores those trees, so
			// anything in them is by definition not module source and is under
			// no obligation to parse. A single testdata/*.go fixture - a shape
			// this repo is entitled to add at any time - used to abort the whole
			// walk and report `walking <root>: ...`, a failure naming neither
			// this guard nor its subject.
			name := d.Name()
			if name == "testdata" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			switch name {
			case "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if allowed[path] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		// parser.SkipObjectResolution and no parser.ParseComments: comments are
		// not part of the tree that gets walked, which is the whole point.
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if perr != nil {
			// REPORTED, NOT RETURNED. Returning it aborts WalkDir, so one
			// unreadable file silently stops the scan of everything after it -
			// the opposite of what a guard should do when it loses coverage.
			unparseable = append(unparseable, rel)
			return nil
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if found {
				return false
			}
			// A FuncDecl's own NAME is the definition, not a use. sqlc emits
			// `func (q *Queries) IncrementTaskRetryCount(...)` in tasks.sql.go,
			// and that declaration is why the generated files used to be
			// exempted wholesale. The `return false` at the end of this branch
			// is what skips the name AND the receiver; Type and Body are walked
			// explicitly, so a call hand-added inside a generated file is caught
			// like any other.
			if fd, ok := n.(*ast.FuncDecl); ok {
				if fd.Type != nil {
					ast.Inspect(fd.Type, func(m ast.Node) bool {
						if id, ok := m.(*ast.Ident); ok && id.Name == ident {
							found = true
						}
						return !found
					})
				}
				if fd.Body != nil {
					ast.Inspect(fd.Body, func(m ast.Node) bool {
						if id, ok := m.(*ast.Ident); ok && id.Name == ident {
							found = true
						}
						return !found
					})
				}
				return false
			}
			if id, ok := n.(*ast.Ident); ok && id.Name == ident {
				found = true
			}
			return !found
		})
		if found {
			offenders = append(offenders, rel)
		}
		return nil
	})
	return offenders, unparseable, err
}

// TestScanForIdentReferences_PrunesToolchainIgnoredTreesAndSurvivesABadParse.
//
// TWO BEHAVIOURS THE REAL REPOSITORY CANNOT EXERCISE TODAY, one of which was
// wrong. A .go file under testdata/ is deliberately ignored by the Go toolchain
// and is under no obligation to parse; the guard used to `return perr` on it,
// which aborts WalkDir and fails as `walking <root>: ...` - a message naming
// neither the guard nor IncrementTaskRetryCount, from a fixture that is not a
// defect at all.
//
// THE POISONED FILE SORTS FIRST ON PURPOSE. WalkDir visits in lexical order, so
// aaa_broken.go is read before zzz_offender.go: a bad input placed last cannot
// distinguish "the walk continued" from "the walk stopped after it".
func TestScanForIdentReferences_PrunesToolchainIgnoredTreesAndSurvivesABadParse(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const badGo = "this is not go at all {{{\n"
	write("testdata/fixture.go", badGo)
	write("_scratch/fixture.go", badGo)
	write(".hidden/fixture.go", badGo)
	write("aaa_broken.go", badGo)
	write("zzz_offender.go", "package p\n\nfunc f(q Q) { q.IncrementTaskRetryCount() }\n")

	offenders, unparseable, err := scanForIdentReferences(root, "IncrementTaskRetryCount", nil)
	if err != nil {
		t.Fatalf("the walk must not abort: %v", err)
	}
	if want := []string{"zzz_offender.go"}; !slices.Equal(offenders, want) {
		t.Errorf("offenders = %v, want %v. A file sorting AFTER an unparseable one must still be "+
			"scanned; if it is missing, one bad parse is silently ending the whole walk.", offenders, want)
	}
	if want := []string{"aaa_broken.go"}; !slices.Equal(unparseable, want) {
		t.Errorf("unparseable = %v, want %v. Files under testdata/, _-prefixed and .-prefixed trees "+
			"are ignored by the Go toolchain and must be pruned, not parsed and reported.",
			unparseable, want)
	}
}
