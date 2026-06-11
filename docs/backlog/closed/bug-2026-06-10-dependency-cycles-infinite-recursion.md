---
title: Dependency cycles pass validation and drive FailDependentTasks into infinite recursion
type: bug
status: closed
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

# Dependency cycles pass validation and drive FailDependentTasks into infinite recursion

## Summary
`jobspec.Validate` only checks that `depends_on` names exist, so self-references and cycles (`A; B->[A,C]; C->[B]`) pass validation; the schema has no `CHECK (task_id <> depends_on_task_id)`; and the `FailDependentTasks` recursive CTE uses `UNION ALL` with no cycle guard. When a task in a cyclic job fails, the CTE recurses forever, pinning a pool connection. Even without triggering the CTE, cyclic jobs are never eligible and sit non-terminal forever. Any authenticated user can submit such a spec.

## Proposal
Three layers:
1. `UNION` instead of `UNION ALL` in `FailDependentTasks` (the single-column set dedupes, guaranteeing termination).
2. Migration: `ALTER TABLE task_dependencies ADD CONSTRAINT no_self_dep CHECK (task_id <> depends_on_task_id);`
3. Cycle detection (Kahn's algorithm or DFS) in `jobspec.Validate`, which fixes API, CLI, MCP, and schedrunner at once since they share the validator.

## Related
- `internal/store/query/tasks.sql:60-73` (`FailDependentTasks`)
- `internal/jobspec/jobspec.go:91-97` (`Validate`)
- `internal/store/migrations/000001_initial.up.sql:69-73` (task_dependencies)
