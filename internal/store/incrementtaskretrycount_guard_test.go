package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
// It went RED on a comment in internal/scheduler/dispatch.go that enumerates
// relay's five Go-side fence-rejection sites by statement name - prose that is
// correct, load-bearing, and not a call. Rewording that comment would have been
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

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Prune trees that hold no module source. .claude is where this
			// repo's git worktrees live, so walking it would rediscover a second
			// copy of every allowed file under a path no allow-list can name.
			switch d.Name() {
			case ".git", ".claude", "node_modules":
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
		// parser.SkipObjectResolution and no parser.ParseComments: comments are
		// not part of the tree that gets walked, which is the whole point.
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if found {
				return false
			}
			// A FuncDecl's own NAME is the definition, not a use. sqlc emits
			// `func (q *Queries) IncrementTaskRetryCount(...)` in tasks.sql.go,
			// and that declaration is why the generated files used to be
			// exempted wholesale. Skip the name and the receiver; the body is
			// still walked, so a call hand-added inside a generated file is
			// caught like any other.
			if fd, ok := n.(*ast.FuncDecl); ok {
				if fd.Recv != nil {
					ast.Inspect(fd.Recv, func(m ast.Node) bool { return true })
				}
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
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
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
