package store_test

import (
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
// The check is a substring match, so it also sees the identifier in PROSE. Every
// generated internal/store/*.sql.go file is therefore exempt: tasks.sql.go
// defines the statement, and jobs.sql.go carries the JobStatusCounts comment that
// names it while explaining which statements keep a terminal task unwritable.
// Those files are emitted by sqlc from query/*.sql and cannot contain a
// hand-written call site, so exempting them costs the guard nothing - a real
// caller would live in a hand-written file, which no exemption covers.
func TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath(t *testing.T) {
	root := repoRoot(t)
	const ident = "IncrementTaskRetryCount"

	// The agent status path is its one legitimate caller.
	allowed := map[string]bool{
		filepath.Join(root, "internal", "worker", "handler.go"): true,
	}
	storeDir := filepath.Join(root, "internal", "store")

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
		// sqlc-generated query files: definitions and comments, never call sites.
		if filepath.Dir(path) == storeDir && strings.HasSuffix(path, ".sql.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), ident) {
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
			"internal/worker/handler.go, but it appears in: %v\n"+
			"An operator re-run (POST /v1/jobs/{id}/retry) must use RetryJobTasks: every "+
			"predicate on %s would reject it. See the note on the statement in "+
			"internal/store/query/tasks.sql.", ident, offenders, ident)
	}
}
