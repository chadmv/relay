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
// Known weakness, accepted: a rename defeats it. Same weakness and same trade as
// TestUpdateTaskStatusEpochHasNoProductionCaller.
func TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath(t *testing.T) {
	root := repoRoot(t)
	const ident = "IncrementTaskRetryCount"

	// The generated store layer necessarily defines it; the agent status path is
	// its one legitimate caller.
	allowed := map[string]bool{
		filepath.Join(root, "internal", "store", "tasks.sql.go"): true,
		filepath.Join(root, "internal", "worker", "handler.go"):  true,
	}

	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if allowed[path] {
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
		t.Fatalf("walking internal/: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("%s is the AGENT-DRIVEN retry and must be called only from "+
			"internal/worker/handler.go, but it appears in: %v\n"+
			"An operator re-run (POST /v1/jobs/{id}/retry) must use RetryJobTasks: every "+
			"predicate on %s would reject it. See the note on the statement in "+
			"internal/store/query/tasks.sql.", ident, offenders, ident)
	}
}
