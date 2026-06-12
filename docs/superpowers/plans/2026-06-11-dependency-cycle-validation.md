# Dependency-Cycle Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject job specs containing dependency cycles (including self-references) at validation time, and add two defense-in-depth guards (a DB self-dependency constraint and a terminating `FailDependentTasks` CTE) so a cyclic spec can never pin a pool connection.

**Architecture:** Three independent layers. (1) Kahn's-algorithm cycle detection added to `jobspec.Validate`, the single shared validator for REST/CLI/MCP/schedrunner. (2) A new migration adds `CHECK (task_id <> depends_on_task_id)` on `task_dependencies`. (3) `FailDependentTasks` switches `UNION ALL` to `UNION` so the recursive set dedupes and terminates.

**Tech Stack:** Go, sqlc-generated store layer, golang-migrate migrations, testify, testcontainers-go (integration).

---

## File Structure

- `internal/jobspec/jobspec.go` - add cycle detection to `Validate` (Layer 1).
- `internal/jobspec/jobspec_test.go` - unit tests for cycle cases and valid DAGs (Layer 1).
- `internal/store/migrations/000015_no_self_dep.up.sql` - new, add CHECK constraint (Layer 2).
- `internal/store/migrations/000015_no_self_dep.down.sql` - new, drop CHECK constraint (Layer 2).
- `internal/store/query/tasks.sql` - `UNION ALL` -> `UNION` in `FailDependentTasks` (Layer 3).
- `internal/store/tasks.sql.go` - regenerated via `make generate` (Layer 3, do not hand-edit).
- `internal/store/store_test.go` - integration tests for the constraint and CTE termination.

---

## Task 1: Cycle detection in `jobspec.Validate` (Layer 1)

**Files:**
- Modify: `internal/jobspec/jobspec.go` (inside `Validate`, after the unknown-`depends_on` loop at lines 91-97)
- Test: `internal/jobspec/jobspec_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/jobspec/jobspec_test.go`:

```go
func TestValidate_SelfDependencyRejected(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}, DependsOn: []string{"a"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle")
}

func TestValidate_TwoCycleRejected(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}, DependsOn: []string{"b"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"a"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle")
	require.Contains(t, err.Error(), "a")
	require.Contains(t, err.Error(), "b")
}

func TestValidate_ThreeCycleRejected(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}, DependsOn: []string{"b"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"c"}},
			{Name: "c", Command: []string{"echo"}, DependsOn: []string{"a"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle")
}

func TestValidate_DiamondDAGAccepted(t *testing.T) {
	// a -> {b, c} -> d. Legitimate DAG, must pass.
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"a"}},
			{Name: "c", Command: []string{"echo"}, DependsOn: []string{"a"}},
			{Name: "d", Command: []string{"echo"}, DependsOn: []string{"b", "c"}},
		},
	})
	require.NoError(t, err)
}

func TestValidate_LinearChainAccepted(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"a"}},
			{Name: "c", Command: []string{"echo"}, DependsOn: []string{"b"}},
		},
	})
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/jobspec/... -run TestValidate_ -v -timeout 30s`
Expected: the two `Accepted` tests PASS (validator currently allows cycles, so DAGs already pass), and the three cycle tests FAIL (no "dependency cycle" error is produced).

- [ ] **Step 3: Add cycle detection to `Validate`**

In `internal/jobspec/jobspec.go`, immediately after the unknown-`depends_on` loop (the block ending at line 97, before the `validateSourceSpec` loop), insert:

```go
	if cyc := detectCycle(spec.Tasks); len(cyc) > 0 {
		return fmt.Errorf("dependency cycle detected involving tasks: %s", strings.Join(cyc, ", "))
	}
```

Then add this unexported helper at the end of the file (it uses `sort`, already-imported `strings`; add `"sort"` to the import block):

```go
// detectCycle returns the sorted names of tasks that participate in or are
// blocked by a dependency cycle, or nil if the DependsOn graph is acyclic.
// Uses Kahn's algorithm: repeatedly remove tasks whose dependencies are all
// satisfied; any tasks left over are part of a cycle. Assumes every DependsOn
// name refers to an existing task (the caller checks this first).
func detectCycle(tasks []TaskSpec) []string {
	indegree := make(map[string]int, len(tasks))
	// dependents[x] = tasks that depend on x.
	dependents := make(map[string][]string, len(tasks))
	for _, ts := range tasks {
		indegree[ts.Name] = len(ts.DependsOn)
		for _, dep := range ts.DependsOn {
			dependents[dep] = append(dependents[dep], ts.Name)
		}
	}
	queue := make([]string, 0, len(tasks))
	for name, deg := range indegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	resolved := 0
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		resolved++
		for _, d := range dependents[name] {
			indegree[d]--
			if indegree[d] == 0 {
				queue = append(queue, d)
			}
		}
	}
	if resolved == len(tasks) {
		return nil
	}
	var stuck []string
	for name, deg := range indegree {
		if deg > 0 {
			stuck = append(stuck, name)
		}
	}
	sort.Strings(stuck)
	return stuck
}
```

Note: a self-reference gives that task indegree 1 with no resolving predecessor, so it is never dequeued and is reported - the self-dep case needs no special handling.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/jobspec/... -run TestValidate_ -v -timeout 30s`
Expected: all `TestValidate_*` tests PASS.

- [ ] **Step 5: Run the full jobspec package and vet**

Run: `go test ./internal/jobspec/... -timeout 30s && go vet ./internal/jobspec/...`
Expected: PASS, no vet complaints.

- [ ] **Step 6: Commit**

```bash
git add internal/jobspec/jobspec.go internal/jobspec/jobspec_test.go
git commit -m "jobspec: reject dependency cycles in Validate"
```

---

## Task 2: DB self-dependency CHECK constraint (Layer 2)

**Files:**
- Create: `internal/store/migrations/000015_no_self_dep.up.sql`
- Create: `internal/store/migrations/000015_no_self_dep.down.sql`

- [ ] **Step 1: Write the up migration**

Create `internal/store/migrations/000015_no_self_dep.up.sql`:

```sql
ALTER TABLE task_dependencies
    ADD CONSTRAINT no_self_dep CHECK (task_id <> depends_on_task_id);
```

- [ ] **Step 2: Write the down migration**

Create `internal/store/migrations/000015_no_self_dep.down.sql`:

```sql
ALTER TABLE task_dependencies DROP CONSTRAINT no_self_dep;
```

- [ ] **Step 3: Verify the migrations build into the binary**

Run: `go build ./...`
Expected: PASS (migrations are embedded; a malformed filename would fail the embed at compile time). The constraint itself is exercised in Task 4.

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/000015_no_self_dep.up.sql internal/store/migrations/000015_no_self_dep.down.sql
git commit -m "store: add no_self_dep check constraint on task_dependencies"
```

---

## Task 3: Terminating `FailDependentTasks` CTE (Layer 3)

**Files:**
- Modify: `internal/store/query/tasks.sql:66` (`UNION ALL` -> `UNION`)
- Regenerate: `internal/store/tasks.sql.go` (via `make generate`)

- [ ] **Step 1: Edit the query**

In `internal/store/query/tasks.sql`, in the `FailDependentTasks` CTE, change the line:

```sql
    UNION ALL
```

to:

```sql
    UNION
```

(This is the `UNION ALL` between the anchor `SELECT task_id FROM task_dependencies WHERE depends_on_task_id = ...` and the recursive `SELECT td.task_id ...`.)

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: `internal/store/tasks.sql.go` updates so `failDependentTasks` const contains `UNION` (not `UNION ALL`). No other generated files should change meaningfully. Never hand-edit `tasks.sql.go`.

If `make` is unavailable on PATH, run the underlying tool directly: `sqlc generate` (from the repo root, where `sqlc.yaml` lives).

- [ ] **Step 3: Verify the regenerated file and build**

Run: `go build ./...`
Expected: PASS. Confirm `internal/store/tasks.sql.go` now shows `UNION` in the `failDependentTasks` SQL string.

- [ ] **Step 4: Commit**

```bash
git add internal/store/query/tasks.sql internal/store/tasks.sql.go
git commit -m "store: dedupe FailDependentTasks CTE with UNION to guarantee termination"
```

---

## Task 4: Integration tests for constraint and CTE termination

**Files:**
- Test: `internal/store/store_test.go` (append; file is `//go:build integration`, package `store_test`)

These tests require Docker. Follow the existing patterns in the file: `newTestQueries(t)`, `makeTestUser`, `q.CreateJob`, `q.CreateTask`, `q.CreateTaskDependency`.

- [ ] **Step 1: Write the failing/passing integration tests**

Append to `internal/store/store_test.go`:

```go
func TestSelfDependencyConstraintRejected(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Cyl", "cyl@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "self-dep", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "a", Commands: []byte(`[["echo","a"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`),
	})
	require.NoError(t, err)

	// A task depending on itself must violate the no_self_dep CHECK constraint.
	err = q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: task.ID, DependsOnTaskID: task.ID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no_self_dep")
}

func TestFailDependentTasksTerminatesOnChain(t *testing.T) {
	q := newTestQueries(t)
	ctx := context.Background()

	user := makeTestUser(t, q, ctx, "Chain", "chain@example.com")
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "chain", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)

	mk := func(name string) store.Task {
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID: job.ID, Name: name, Commands: []byte(`[["echo"]]`),
			Env: []byte(`{}`), Requires: []byte(`{}`),
		})
		require.NoError(t, err)
		return task
	}
	a, b, c := mk("a"), mk("b"), mk("c")

	// b depends on a, c depends on b.
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: b.ID, DependsOnTaskID: a.ID,
	}))
	require.NoError(t, q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
		TaskID: c.ID, DependsOnTaskID: b.ID,
	}))

	// Failing a must transitively fail b and c, and must terminate.
	require.NoError(t, q.FailDependentTasks(ctx, a.ID))

	gb, err := q.GetTask(ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", gb.Status)
	gc, err := q.GetTask(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", gc.Status)
}
```

- [ ] **Step 2: Run the integration tests**

Run: `go test -tags integration -p 1 ./internal/store/... -run "TestSelfDependencyConstraintRejected|TestFailDependentTasksTerminatesOnChain" -v -timeout 180s`
Expected: both PASS. (Requires Docker Desktop running.) If Docker is unavailable in this environment, note that the tests are written and must be run where Docker is present; do not delete them.

- [ ] **Step 3: Commit**

```bash
git add internal/store/store_test.go
git commit -m "store: integration tests for no_self_dep constraint and CTE termination"
```

---

## Task 5: Final verification

- [ ] **Step 1: Full unit test suite**

Run: `go test ./... -timeout 180s`
Expected: PASS across all packages (unit; no Docker).

- [ ] **Step 2: Build all binaries**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 3: Integration suite (if Docker available)**

Run: `make test-integration` (or `go test -tags integration -p 1 ./... -timeout 600s`)
Expected: PASS. If Docker is unavailable here, record that integration tests must run in a Docker-enabled environment before merge.

- [ ] **Step 4: Close the backlog item**

`git mv docs/backlog/bug-2026-06-10-dependency-cycles-infinite-recursion.md docs/backlog/closed/` and update the front-matter `status: open` -> `status: closed`. Commit:

```bash
git add docs/backlog/
git commit -m "backlog: close bug-2026-06-10-dependency-cycles-infinite-recursion"
```
