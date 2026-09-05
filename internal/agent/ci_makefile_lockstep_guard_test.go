package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// integrationTargetPattern matches a Makefile target line for one of this
// repo's integration-lane targets: test-cli-integration, test-pg-integration,
// test-api-integration. The infix is [a-z-]+, not [a-z-]*, so the pattern
// does not also match test-integration - the container-mode target for a
// developer with Docker and no RELAY_TEST_DATABASE_URL, which no CI job
// invokes and which would make this guard permanently red if it were
// included.
var integrationTargetPattern = regexp.MustCompile(`(?m)^(test-[a-z-]+integration):`)

// stepRunMakePattern matches a workflow step's `run: make <target>` line.
var stepRunMakePattern = regexp.MustCompile(`(?m)^\s*run:\s*make\s+(\S+)\s*$`)

// TestMakefileIntegrationTargetsHaveACIStep pins PRESENCE, not effect: that
// every test-*-integration target the Makefile defines is invoked by a
// `run: make <target>` step somewhere in go-ci.yml. Three targets and three
// jobs are kept in step by hand today, and this is the check that catches a
// target added without a job, or a job's run line deleted without the
// Makefile target following it.
//
// It deliberately matches the `run: make <target>` STEP form rather than a
// bare substring search for `make <target>` anywhere in the file: go-ci.yml's
// own comments quote several of these targets in prose (e.g. "lane locally
// (`make test-cli-integration`)"), and a bare substring check would still
// pass after the real run line was deleted, defeating the guard it was meant
// to be.
func TestMakefileIntegrationTargetsHaveACIStep(t *testing.T) {
	root := moduleRoot(t)

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "go-ci.yml"))
	if err != nil {
		t.Fatalf("reading go-ci.yml: %v", err)
	}

	targets := integrationTargetPattern.FindAllStringSubmatch(string(makefile), -1)
	if len(targets) == 0 {
		t.Fatal("no test-*-integration target found in Makefile; this guard's pattern may be stale")
	}

	steps := map[string]bool{}
	for _, m := range stepRunMakePattern.FindAllStringSubmatch(string(workflow), -1) {
		steps[m[1]] = true
	}

	for _, m := range targets {
		target := m[1]
		if !steps[target] {
			t.Errorf("Makefile target %q has no `run: make %s` step in go-ci.yml; "+
				"either its job's run line was deleted or the target is new and no job "+
				"invokes it yet", target, target)
		}
	}
}
