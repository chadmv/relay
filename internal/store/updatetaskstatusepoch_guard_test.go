package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateTaskStatusEpochHasNoProductionCaller is a STRUCTURAL guard, and it
// is deliberately NOT integration-tagged so it runs in the plain `go test ./...`
// gate on every change.
//
// UpdateTaskStatusEpoch (query/tasks.sql) fences on assignment_epoch only. It
// has no worker predicate and no status predicate, so it is now the one
// statement in the repo that can still write a terminal status over a terminal
// task and resurrect it - the exact capability
// bug-2026-06-26-retry-resurrects-cancelled-task was about. It survives because
// it is test-only: TestRecomputeJobStatus and
// TestIncrementTaskRetryCount_BumpsEpochAndFencesStaleRetry use it to plant
// fixture rows that the fenced statements would (correctly) refuse to write.
//
// A comment saying "TEST-ONLY" does not enforce that, and sqlc re-exports the
// method on *store.Queries on every single regeneration, so it is permanently
// one autocomplete away from a production call site. This test is the
// enforcement: it walks internal/ and fails if the identifier appears in any
// non-test Go file.
//
// If this goes RED, do not add an exception. Either the new caller wants
// UpdateTaskStatus (fenced on identity and terminality, which is almost always
// the right answer), or it genuinely needs an epoch-only write - in which case
// it needs its own statement with its own stated preconditions, the way
// POST /v1/jobs/{id}/retry is required to get one rather than reusing
// IncrementTaskRetryCount.
func TestUpdateTaskStatusEpochHasNoProductionCaller(t *testing.T) {
	root := repoRoot(t)
	const ident = "UpdateTaskStatusEpoch"

	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The generated store layer necessarily defines it; the point of the
		// guard is that nothing outside internal/store CALLS it, and that even
		// inside internal/store no hand-written file does.
		if path == filepath.Join(root, "internal", "store", "tasks.sql.go") {
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
		t.Fatalf("%s is test-only and must have no production caller, but it appears in: %v\n"+
			"Use UpdateTaskStatus (fenced on worker_id and terminality) instead, or add a new "+
			"statement that states its own preconditions. See the TEST-ONLY warning on the "+
			"statement in internal/store/query/tasks.sql.", ident, offenders)
	}
}

// repoRoot walks up from the package directory to the directory holding go.mod.
// Tests run with the package directory as cwd, so the module root is two levels
// up today - but locating go.mod keeps this correct if the package ever moves.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}
