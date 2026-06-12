# Dependency-cycle validation and recursion guard

- Date: 2026-06-11
- Backlog item: `bug-2026-06-10-dependency-cycles-infinite-recursion`
- Status: approved

## Problem

`jobspec.Validate` only checks that each `depends_on` name exists, so
self-references and cycles (`A; B->[A,C]; C->[B]`) pass validation. The schema
has no `CHECK (task_id <> depends_on_task_id)`, and the `FailDependentTasks`
recursive CTE uses `UNION ALL` with no cycle guard. Consequences:

- A cyclic job whose task fails drives the CTE into infinite recursion, pinning
  a pool connection.
- Even without triggering the CTE, cyclic tasks are never eligible
  (`GetEligibleTasks` requires all deps `done`) and sit non-terminal forever.
- Any authenticated user can submit such a spec.

## Design

Defense in depth across three layers. Each is cheap and independently
sufficient against part of the failure; together they close the ingestion path,
the schema, and the recursion.

### Layer 1 - Cycle detection in `jobspec.Validate` (primary fix)

After the existing unknown-`depends_on` check, run a topological sort (Kahn's
algorithm) over the task graph:

1. Build in-degree per task from each task's `DependsOn` edges.
2. Seed a queue with all zero-in-degree tasks.
3. Repeatedly pop a task and decrement the in-degree of tasks that depend on it,
   enqueuing any that reach zero.
4. If the number of processed tasks is less than the total, the unprocessed
   tasks form or are blocked by a cycle.

On a cycle, return `dependency cycle detected involving tasks: <names>` where
`<names>` is the sorted list of unresolved task names. A self-reference
(`A->[A]`) is a 1-cycle and is caught here.

This lives in `internal/jobspec/jobspec.go` inside `Validate`, after the
unknown-dependency loop and before the source-spec loop. Because every ingestion
path (REST API, CLI, MCP, schedrunner) shares `Validate`, this fixes all of them
at once.

### Layer 2 - DB self-dependency constraint (migration 000015)

New migration `000015_no_self_dep.{up,down}.sql`:

- up: `ALTER TABLE task_dependencies ADD CONSTRAINT no_self_dep CHECK (task_id <> depends_on_task_id);`
- down: `ALTER TABLE task_dependencies DROP CONSTRAINT no_self_dep;`

Schema-level guard so no insert path can create a self-loop even if it bypasses
the validator. The constraint only blocks future rows; `ALTER TABLE ADD
CONSTRAINT` would fail if a self-dep row already existed, but none can - no
current path creates them.

### Layer 3 - CTE termination guard

In `internal/store/query/tasks.sql`, change `UNION ALL` to `UNION` in
`FailDependentTasks`. The single-column `blocked` set dedupes, so recursion
terminates even if a cycle somehow reaches the table. Run `make generate` to
regenerate `tasks.sql.go`. (Per CLAUDE.md, never edit `*.sql.go` by hand.)

## Testing

### Unit (no Docker)

Table-driven additions to `internal/jobspec/jobspec_test.go`:

- self-reference `A->[A]` rejected with a cycle error.
- 2-cycle `A->[B]; B->[A]` rejected.
- 3-cycle `A->[B]; B->[C]; C->[A]` rejected.
- valid diamond `A; B->[A]; C->[A]; D->[B,C]` passes (legitimate DAG not broken).
- valid linear chain still passes (regression guard).

### Integration (Docker)

- A direct insert of a self-dependency row into `task_dependencies` violates the
  `no_self_dep` constraint.
- `FailDependentTasks` terminates and marks the expected tasks on a normal DAG
  (existing behavior preserved; the `UNION` change must not drop legitimate
  transitive dependents).

Exact integration-test placement/scope to be settled in the implementation plan.

## Out of scope

- General DAG validation beyond cycles (e.g., depth limits) - not requested.
- Configurable behavior or env vars - YAGNI.
- Reworking `GetEligibleTasks`; the validator prevents cyclic specs upstream.

## Affected files

- `internal/jobspec/jobspec.go` - cycle detection in `Validate`.
- `internal/jobspec/jobspec_test.go` - unit tests.
- `internal/store/migrations/000015_no_self_dep.up.sql` / `.down.sql` - new.
- `internal/store/query/tasks.sql` - `UNION ALL` -> `UNION`.
- `internal/store/tasks.sql.go` - regenerated via `make generate`.
