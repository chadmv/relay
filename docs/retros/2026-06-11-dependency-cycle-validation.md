---
date: 2026-06-11
topic: dependency-cycle-validation
branch: claude/vigilant-nightingale-711ad8
range: 6ac89dfccb96cccf2ba335fb0c02e7d06ac95dda..493671e3eb9ba13dd7c15c9208718a063b88760e
---

# Session Retro: 2026-06-11 - Dependency-Cycle Validation

**TL;DR:** Closed the high-priority bug where cyclic job specs passed validation and drove `FailDependentTasks` into infinite recursion, via three defense-in-depth layers (validator cycle detection, DB self-dep constraint, terminating CTE) - all merged on branch, awaiting merge/PR decision.

## What Was Built

Fixed backlog bug `bug-2026-06-10-dependency-cycles-infinite-recursion` end-to-end through the full brainstorm -> spec -> plan -> subagent-driven-development flow. Three independent layers:

- **Layer 1 - validator cycle detection** (`internal/jobspec/jobspec.go`): a `detectCycle` helper (Kahn's algorithm) added to `Validate`, placed *after* the unknown-`depends_on` check so its "all dep names exist" precondition holds. Returns sorted names of tasks in/blocked by a cycle; self-references fall out naturally as never-dequeued nodes. Because every ingestion path (REST, CLI, MCP, schedrunner) shares `Validate`, this one change fixes them all - the "single job-spec pipeline" invariant paying off.
- **Layer 2 - DB self-dep constraint** (migration `000015_no_self_dep`): `CHECK (task_id <> depends_on_task_id)` on `task_dependencies`, a schema-level backstop for any future writer that bypasses the validator.
- **Layer 3 - terminating CTE** (`internal/store/query/tasks.sql`): `UNION ALL` -> `UNION` in `FailDependentTasks`. The single-column `blocked` set dedupes, so the recursive walk provably terminates even if a cycle reaches the table; valid-DAG results are unchanged because the only consumer is an `id IN (...)` membership test.

Tests: five unit cases in `jobspec_test.go` (self/2/3-cycle reject; diamond + linear DAG accept), two integration tests in `store_test.go` (self-dep CHECK rejection; CTE termination on an a->b->c chain), plus a duplicate-`depends_on` regression test added after final review.

## Key Decisions

- **All three layers, not just the validator.** Defense-in-depth was chosen deliberately: the validator blocks all cycles at ingestion, the constraint guards the DB against non-validator writers, and the `UNION` change neutralizes the *symptom* (infinite recursion) regardless of how a cycle might arrive. Each piece is cheap and has a distinct responsibility - not over-built.
- **Kahn's algorithm over DFS** for cycle detection - the leftover-nodes-after-topo-sort formulation gives a natural, deterministic "which tasks are stuck" error message.
- **Combined single-pass review for the no-logic tasks.** The migration (Task 2) and the `UNION` change (Task 3) each got one combined spec+quality verification rather than two separate review passes - the same adaptation flagged as worth keeping in the prior retro, applied again here.
- **Added the duplicate-dep regression test** the final reviewer recommended, even though it was labeled optional polish: it pins the one non-obvious correctness property of the Kahn implementation (duplicate edges inflate indegree and the dependents list symmetrically, so they cancel).

## Problems Encountered

- **sqlc regeneration churned line endings.** `sqlc generate` rewrote all `internal/store/*.sql.go` with LF endings, making git flag 11 unrelated generated files as modified (CRLF-vs-LF only, zero content change). The implementer reverted the noise with `git checkout --` and committed only the two genuinely-changed files. Worth anticipating on this Windows/autocrlf repo whenever `make generate` runs.
- **`make` not on PATH** (recurring from prior sessions). `sqlc` was on PATH directly (`~/go/bin/sqlc`), so `sqlc generate` was run instead of `make generate`. `go` commands were run directly.

## Improvement Goals

- **Combined single review for no-logic tasks** (carried from the 2026-06-10 retro): applied again this session for Tasks 2 and 3. Confirmed as the right default for changes with nothing to quality-review. Keep it.
- **New:** when a task runs `make generate` / `sqlc generate` on this repo, expect generated-file line-ending churn and plan to stage only the intended files. Consider noting this in the plan's regeneration step up front (this plan did call out `make generate` but not the churn).

## Files Most Touched

- `internal/jobspec/jobspec.go` - cycle detection (`detectCycle` + call in `Validate`).
- `internal/jobspec/jobspec_test.go` - six new unit tests (cycles, DAGs, duplicate deps).
- `internal/store/store_test.go` - two integration tests (constraint + CTE termination).
- `internal/store/query/tasks.sql` - the one-line `UNION ALL` -> `UNION` fix.
- `internal/store/tasks.sql.go` - regenerated to match (sqlc, not hand-edited).
- `internal/store/migrations/000015_no_self_dep.{up,down}.sql` - new CHECK constraint.
- `docs/superpowers/specs/2026-06-11-dependency-cycle-validation-design.md` - design spec.
- `docs/superpowers/plans/2026-06-11-dependency-cycle-validation.md` - implementation plan.
