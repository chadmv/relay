package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsCrossCompileStepCommandsArePresent pins PRESENCE, not effect. It
// is a string match on YAML: it cannot prove GitHub schedules the step, that
// the step runs, or that it passes. What it catches is a deletion, which today
// leaves every lane green.
//
// The three commands reach disjoint sets of files, so none is redundant with
// another. `go build` does not read _test.go files, so the plain vet line is
// what reaches this package's windows-constrained test files. Neither untagged
// line compiles a `//go:build windows && integration` file, and
// `make vet-integration` builds for linux, so the tagged vet line is what
// reaches that combination.
func TestWindowsCrossCompileStepCommandsArePresent(t *testing.T) {
	workflow := filepath.Join(moduleRoot(t), ".github", "workflows", "go-ci.yml")
	b, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("reading %s: %v", workflow, err)
	}
	got := string(b)

	for _, cmd := range []string{
		"GOOS=windows go build ./...",
		"GOOS=windows go vet ./...",
		"GOOS=windows go vet -tags integration ./...",
	} {
		if !strings.Contains(got, cmd) {
			t.Errorf("go-ci.yml no longer runs %q; dropping it shrinks the set of "+
				"//go:build windows files a compiler reads in CI, and no other lane "+
				"goes red when it does", cmd)
		}
	}
}

// moduleRoot walks up from the package directory to the directory holding
// go.mod. Tests run with the package directory as cwd.
func moduleRoot(t *testing.T) string {
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
