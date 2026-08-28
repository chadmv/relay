package store_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestCreateTaskHasNoCallerOutsideJobcreate is a STRUCTURAL guard, deliberately
// NOT integration-tagged so it runs in the plain `go test ./...` gate.
//
// IT IS WHAT STANDS IN FOR A DATABASE CHECK CONSTRAINT, and that is the whole
// reason it exists. tasks.retries and tasks.timeout_seconds carry no CHECK: the
// spec declined one because `ALTER TABLE tasks ADD CONSTRAINT ... CHECK (...)`
// validates every existing row at startup, and migrations run in-process, so a
// deployment that already holds an out-of-range row would refuse to start - on
// exactly the population that has the bug. NOT VALID avoids the scan and buys a
// worse failure: the constraint still fires on any UPDATE of a pre-existing bad
// row, which lands in handleTaskStatus's non-ErrNoRows arm as one budgeted log
// line and a task stuck until the 24h watchdog.
//
// What makes that trade acceptable is that the bound has exactly one enforcement
// point and these two statements have exactly one caller. This test keeps the
// second half of that sentence true. If it goes RED, the new caller must either
// route through jobcreate.CreateJobFromSpec (which calls jobspec.Validate first)
// or validate the spec itself and say so here - do not add an exemption without
// doing one of those two.
//
// THE FIRST HALF IS NOT CHECKED HERE, AND IT IS NOT UNCHECKED EITHER. Deleting
// the jobspec.Validate call from jobcreate.CreateJobFromSpec left this test, and
// the whole plain lane, green. Its subject is
// TestCreateJobFromSpec_RefusesAnOverBoundSpecBeforeTouchingTheDatabase in
// internal/jobcreate, added for exactly that mutant and in this same untagged
// lane. Neither test proves the sentence alone.
//
// THE IDENTIFIER LIST IS DERIVED, NOT WRITTEN DOWN. It used to be a literal
// []string{"CreateTask", "CreateTaskWithSource"}, which meant a THIRD statement
// inserting a tasks row would get a generated method, zero coverage, and would
// silently falsify this comment's "exactly one caller" claim. Deriving it from
// the .sql that sqlc compiles makes the guard notice the statement itself, not
// just a spelling somebody remembered.
//
// Known weakness, accepted, and identical to the weakness on
// TestIncrementTaskRetryCountHasNoCallerOutsideTheAgentPath: a rename defeats it,
// and an identifier reached reflectively through a string literal is invisible to
// an AST walk. The walk covers the whole module, not just internal/, because
// cmd/relay-server wires the store layer by hand and can reach *store.Queries as
// directly as anything under internal/.
func TestCreateTaskHasNoCallerOutsideJobcreate(t *testing.T) {
	root := repoRoot(t)

	// The single job-creation path. It calls jobspec.Validate before either
	// INSERT, which is the property the whole bound rests on.
	allowed := map[string]bool{
		filepath.Join(root, "internal", "jobcreate", "jobcreate.go"): true,
	}

	idents := insertIntoTasksQueryNames(t, root)
	if want := []string{"CreateTask", "CreateTaskWithSource"}; !slices.Equal(idents, want) {
		t.Fatalf("the set of sqlc statements that INSERT INTO tasks is %v, want %v.\n"+
			"A statement was added, removed or renamed. There is NO CHECK constraint on tasks.retries "+
			"or tasks.timeout_seconds, so every such statement must be reachable only from a caller "+
			"that has run jobspec.Validate. Route it through internal/jobcreate, then update this "+
			"list in the same commit.", idents, want)
	}

	for _, ident := range idents {
		offenders, unparseable, err := scanForIdentReferences(root, ident, allowed)
		if err != nil {
			t.Fatalf("walking %s for %s: %v", root, ident, err)
		}
		for _, f := range unparseable {
			t.Errorf("%s could not be parsed, so %s could be referenced there and this guard would not "+
				"see it. Every other file was still scanned.", f, ident)
		}
		if len(offenders) > 0 {
			t.Fatalf("%s inserts a tasks row with a caller-supplied retries and timeout_seconds, and it "+
				"must be called only from internal/jobcreate/jobcreate.go, but it is REFERENCED IN CODE "+
				"in: %v\n"+
				"There is NO CHECK constraint on either column (see this test's comment for why), so "+
				"jobspec.Validate at the single job-creation path is the entire enforcement. Route the "+
				"new caller through jobcreate.CreateJobFromSpec, or validate the spec at the new site "+
				"and record that decision here.", ident, offenders)
		}
	}
}

// TestScanForIdentReferences_FindsAPlantedCreateTaskCallAndHonoursTheAllowList is
// the PERMANENT discriminating input for the guard above.
//
// A guard proved only by planting a call site and reverting it locks nothing in:
// the reverted plant leaves no test behind. This keeps the plant, over a
// synthetic root, so the guard's two halves - "an unlisted reference is
// reported" and "the listed one is not" - are both asserted forever.
//
// THE OFFENDER SORTS FIRST. WalkDir visits in lexical order, and a poisoned
// input placed last cannot distinguish "the walk found it" from "the walk
// stopped before reaching it".
func TestScanForIdentReferences_FindsAPlantedCreateTaskCallAndHonoursTheAllowList(t *testing.T) {
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
	write("aaa_offender.go", "package p\n\nfunc f(q Q) { q.CreateTask() }\n")
	write("internal/jobcreate/jobcreate.go",
		"package jobcreate\n\nfunc g(q Q) { q.CreateTask(); q.CreateTaskWithSource() }\n")

	allowed := map[string]bool{
		filepath.Join(root, "internal", "jobcreate", "jobcreate.go"): true,
	}

	offenders, unparseable, err := scanForIdentReferences(root, "CreateTask", allowed)
	if err != nil {
		t.Fatalf("the walk must not abort: %v", err)
	}
	if len(unparseable) != 0 {
		t.Errorf("unparseable = %v, want none", unparseable)
	}
	if want := []string{"aaa_offender.go"}; !slices.Equal(offenders, want) {
		t.Errorf("offenders = %v, want %v. Either the guard cannot see a plain method call on a "+
			"non-allowed file, or it is reporting the allowed one.", offenders, want)
	}
}

// insertIntoTasksQueryNames returns, sorted, the names of every sqlc statement
// under internal/store/query that INSERTs a row into `tasks`.
//
// It parses the SQL rather than the generated Go because the .sql files are the
// authored artifact: a new statement exists here one `make generate` before its
// method does, and the point of the guard above is to meet it on the way in.
// Scanning the whole query directory rather than tasks.sql alone is deliberate -
// nothing forces a tasks INSERT to be filed under that name.
//
// The table match is anchored on a word boundary so `task_logs`,
// `task_dependencies` and `task_reservations` do not read as `tasks`.
func insertIntoTasksQueryNames(t *testing.T, root string) []string {
	t.Helper()
	nameRe := regexp.MustCompile(`(?m)^-- name:\s+(\w+)\s`)
	insertRe := regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+tasks\b`)

	dir := filepath.Join(root, "internal", "store", "query")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var names []string
	seenAnyStatement := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		locs := nameRe.FindAllSubmatchIndex(src, -1)
		for i, loc := range locs {
			seenAnyStatement = true
			end := len(src)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			if insertRe.Match(src[loc[1]:end]) {
				names = append(names, string(src[loc[2]:loc[3]]))
			}
		}
	}

	// A regex that silently matched nothing would make this guard pass for every
	// possible repository state, which is the failure mode a derived list invites.
	if !seenAnyStatement {
		t.Fatalf("found no `-- name:` statements at all under %s: the statement-splitting regex no "+
			"longer matches sqlc's format, so this guard is inert rather than satisfied", dir)
	}
	sort.Strings(names)
	return names
}
