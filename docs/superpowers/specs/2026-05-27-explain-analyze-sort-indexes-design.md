# EXPLAIN ANALYZE verification for sort indexes

Date: 2026-05-27
Status: Approved (brainstorm)
Closes: [bug-2026-05-27-explain-analyze-sort-indexes](../../backlog/bug-2026-05-27-explain-analyze-sort-indexes.md)

## Goal

Confirm that every paginated list endpoint with a configurable `?sort=` actually uses a composite index (not a `Seq Scan` + `Sort` node) when the underlying table is realistically populated. Capture the EXPLAIN output as a committed artifact so we have evidence on file, and fix any path that turns out to seq-scan.

The verification was called for by the list-endpoint-sort design but skipped during implementation. This spec closes that gap.

## Approach

A one-shot Go program under `scripts/explain_sort_indexes/` that spins up a fresh Postgres 16 container via testcontainers, runs the embedded migrations, seeds each table with realistic data, and runs `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` over every (table, sort_key, direction) tuple supported by the six `SortSpec` allowlists. Each plan is asserted to use the expected composite index; the script exits non-zero if any path fails.

The full EXPLAIN output is written to `docs/retros/2026-05-27-explain-sort-indexes.md` (committed) as the closing artifact for the backlog item.

Not a test. Not part of `make build`. Run once, archive the output, close the bug.

## Coverage

22 (table, sort_key) combinations × 2 directions = 44 sort paths. Each path is EXPLAINed twice (initial page + cursor-resume) = 88 EXPLAIN runs total.

| Table              | Sort keys                                            | Variants (×2 for asc/desc) |
| ------------------ | ---------------------------------------------------- | -------------------------- |
| jobs               | created_at, name, priority, status, updated_at       | 10                         |
| workers            | created_at, name, status, last_seen_at               | 8                          |
| users              | created_at, name, email                              | 6                          |
| scheduled_jobs     | created_at, name, next_run_at, updated_at            | 8                          |
| reservations       | created_at, name, starts_at, ends_at                 | 8                          |
| agent_enrollments  | created_at, expires_at                               | 4                          |

`created_at` sorts use the indexes from migration `000011`; the other keys use `000013`. Both are covered.

## Components

Four files under `scripts/explain_sort_indexes/`, each focused, each independently reviewable:

### `main.go` - orchestration

Top-level flow:

1. Parse flags: `-out <path>` (default stdout), `-skip-seed-on-error <bool>` (default false).
2. Start a Postgres 16 testcontainer; defer terminate.
3. Run `store.Migrate(...)` to apply all migrations including `000011` and `000013`.
4. Call `seed(pool)` to populate every table.
5. Run `ANALYZE` on every table.
6. Build the case list from `cases.go`.
7. For each case, call `explainCase(pool, c)`; collect results.
8. Render results to the output writer.
9. Exit 0 if every case passed, 1 if any case failed, 2 on infrastructure failure (container start / migration / seed).

### `seed.go` - populates tables with realistic skew

One exported `seed(ctx, pool *pgxpool.Pool) error` plus per-table helpers. Uses `pgx.CopyFrom` for bulk loading.

Seed targets:

- **users (200 base + 10k extra for the users sort test):** unique emails, names drawn from a small pool with some repeats.
- **jobs (100k):** `name` from a 50k-cardinality pool, `priority` ∈ `{1, 5, 10, 20, 50}` weighted toward 10, `status` ∈ `{pending, running, completed, failed, cancelled}` weighted toward `completed`, `created_at` / `updated_at` spread across the last 90 days, `submitted_by` cycled across the first 200 users.
- **workers (10k):** `name` mostly unique, `status` ∈ `{online, offline, busy}`, `last_seen_at` ~30% NULL to exercise both `NULLS LAST` and `NULLS FIRST` indexes.
- **scheduled_jobs (10k):** `name` mostly unique, `next_run_at` and `updated_at` spread across the next/last 30 days.
- **reservations (10k):** `name` mostly unique, `starts_at` and `ends_at` each ~30% NULL.
- **agent_enrollments (10k):** ~80% active (`consumed_at IS NULL`), `expires_at` spread across ±7 days. The 20% consumed rows ensure the partial index from migration `000011` is exercising its `WHERE consumed_at IS NULL` predicate.

After all inserts, `ANALYZE` runs on every table (done from `main.go`, not `seed.go`, to keep responsibilities clean).

### `cases.go` - table-driven enumeration of every sort path

Exports `func buildCases(ctx, pool) ([]explainCase, error)`. Each case:

```go
type explainCase struct {
    Table         string
    SortKey       string
    Direction     string // "asc" or "desc"
    ExpectedIndex string // e.g. "idx_jobs_priority_id"
    InitialSQL    string // EXPLAIN ... LIMIT 50
    CursorSQL     string // EXPLAIN ... WHERE (col,id) <op> (val,id) LIMIT 50
}
```

Cases are generated by iterating the six `SortSpec` definitions imported from `relay/internal/api`. For each `(spec, key, direction)` triple, the code looks up `ExpectedIndex` in a hand-written `map[caseKey]string` where `caseKey = {table, sortKey, direction}`. If a sort key is present in a `SortSpec` but missing from the expected-index map, `buildCases` returns an error naming the missing entry - same drift-protection pattern as the existing MCP sort allowlist test (`internal/mcp/sort_drift_test.go`).

Cursor midpoint values are computed inside `buildCases` after seed by running `SELECT <col>, id FROM <table> ORDER BY <col> <dir>, id <dir> OFFSET <n/2> LIMIT 1` for each (table, sort_key, direction). The returned `(value, id)` pair is interpolated into `CursorSQL`. This produces a cursor predicate that prunes roughly half the table - realistic and unambiguously index-friendly.

The `agent_enrollments` cases include `WHERE consumed_at IS NULL` in both `InitialSQL` and `CursorSQL` to match `ListActiveAgentEnrollmentsPage`. Without that filter the planner picks a different index and the result is misleading.

The `jobs` cases include the email join (`JOIN users u ON u.id = j.submitted_by`) to mirror `ListJobsWithEmailPage*` and confirm the join doesn't disrupt the index scan.

### `explain.go` - runs EXPLAIN and asserts plan shape

Exports `func explainCase(ctx, pool, c explainCase) caseResult`. Result:

```go
type caseResult struct {
    Case        explainCase
    Status      string // "PASS" | "FAIL" | "ERROR"
    Reason      string // populated when Status != PASS
    InitialPlan string // full EXPLAIN output
    CursorPlan  string
}
```

The plan-shape check looks at the first non-`Limit` node line in the plan text and confirms it matches `Index Scan using <ExpectedIndex>`, `Index Only Scan using <ExpectedIndex>`, or `Index Scan Backward using <ExpectedIndex>`. Anything else - `Seq Scan`, `Bitmap Heap Scan`, `Sort` on top of `Seq Scan`, or the right kind of scan against the wrong index - produces `FAIL` with a one-line reason.

If running the EXPLAIN itself errors (SQL syntax, missing column), the case is `ERROR` and the error message is the reason. The script continues with the next case rather than aborting; ERROR cases count as failures for the exit code.

## Output

Written to the path given by `-out` (default stdout) as markdown.

```markdown
# EXPLAIN ANALYZE sort index verification

Generated: 2026-05-27T14:23:01Z
Postgres: 16.x
Result: 88/88 PASS

## Summary

| Table              | Sort key       | Dir  | Index                          | Status |
| ------------------ | -------------- | ---- | ------------------------------ | ------ |
| jobs               | created_at     | desc | idx_jobs_created_id            | PASS   |
| jobs               | created_at     | asc  | idx_jobs_created_id            | PASS   |
| ...                                                                                   |

## Plans

### jobs · created_at · desc

Index: `idx_jobs_created_id` - PASS

<details><summary>Initial page plan</summary>

```
Limit  (cost=0.42..2.51 rows=50 ...) (actual time=0.018..0.054 rows=50 loops=1)
  ->  Index Scan using idx_jobs_created_id on jobs j  (cost=...)
        ...
```

</details>

<details><summary>Cursor-resume plan</summary>

```
...
```

</details>

...
```

The header summary table is generated last (after all cases finish) so a reader can skim status before drilling into individual plans.

## Failure mode

Exit 1: at least one case `FAIL` or `ERROR`. Script writes the output file regardless; the reader can inspect what went wrong. Stderr gets a one-line-per-failure summary so CI / a human watching the terminal sees it without opening the doc.

Exit 2: container start, migration, or seed failed. No output file written (nothing to say yet).

If a `FAIL` is legitimate (index doesn't help and shouldn't), the response is to fix migration `000013` (or `000011`) before closing the backlog item. If a `FAIL` is a planner quirk on the seed data (a stats issue rather than a real problem), document it in the retro and consider tuning the seed.

## What this is NOT

- Not a regression test in `make test` or `make test-integration`. A one-shot script that runs once and gets archived.
- Not a benchmark - `EXPLAIN ANALYZE` reports actual time but we're not asserting any time threshold, only plan shape.
- Not tied to CI. Local run, committed artifact, done.
- Not a production load test - 100k jobs is realistic for "is the planner using the index", not for "how does this perform at 10M rows".

## Open questions

None. Both nuances surfaced during brainstorming (partial index on `agent_enrollments`, email join on `jobs`) are folded into the case definitions above.

## Out of scope

- The Python SDK pagination envelope bug ([bug-2026-05-26-python-sdk-list-pagination-envelope](../../backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md)) - separate.
- CLI help examples for `--sort` ([bug-2026-05-27-sort-flag-cli-help-examples](../../backlog/bug-2026-05-27-sort-flag-cli-help-examples.md)) - separate.
- Filtered-list sort paths (`ListJobsByStatusWithEmailPage`, `ListScheduledJobsByOwnerPage`) - those use the older composite indexes from migration `000011` and are unaffected by the configurable-sort feature; out of scope here.

## Deliverables

1. `scripts/explain_sort_indexes/main.go`, `seed.go`, `cases.go`, `explain.go`
2. `scripts/explain_sort_indexes/README.md` - one paragraph: purpose, `go run` command, how to read the output
3. `docs/retros/2026-05-27-explain-sort-indexes.md` - captured EXPLAIN output from a clean run (or, if any case failed, the failure plus the fix)
4. `git mv docs/backlog/bug-2026-05-27-explain-analyze-sort-indexes.md docs/backlog/closed/`
