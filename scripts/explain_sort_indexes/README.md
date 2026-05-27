# explain_sort_indexes

One-shot Go program that verifies every configurable `?sort=` path on
the paginated list endpoints uses a composite index (not a Seq Scan +
Sort node). Closes the EXPLAIN ANALYZE step from the list-endpoint-sort
design that was skipped during implementation.

## What it does

1. Spins up a Postgres 16 testcontainer.
2. Runs every embedded migration (including the index-bearing
   migrations 000011 and 000013).
3. Seeds users (10k), jobs (100k), workers / scheduled_jobs /
   reservations / agent_enrollments (10k each) with realistic skew.
4. Runs `ANALYZE` on every table.
5. For each (table, sort_key, direction) tuple in the six SortSpec
   allowlists, runs `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` over an
   initial-page query and a cursor-resumption query.
6. Asserts each plan's first non-Limit node is an Index Scan / Index
   Only Scan / Index Scan Backward on the expected index.
7. Writes a markdown report with pass/fail summary + every plan.

## Run

```bash
go run ./scripts/explain_sort_indexes -out docs/retros/2026-05-27-explain-sort-indexes.md
```

Requires Docker. Takes ~20-30 seconds.

## Exit codes

- 0 - every plan passed
- 1 - one or more cases FAIL (wrong index, Seq Scan, etc.) or ERROR
- 2 - container start, migration, or seed failed

## When to re-run

Re-run after:
- Adding a new sort key to any `SortSpec` in `internal/api/` (update
  `expectedIndexes` in `cases.go` to point at the new index, then re-run).
- Modifying migration 000011 or 000013.
- Changing the per-endpoint list query's WHERE clause - that may break
  the filters in `columns()` in `cases.go`.

## Reading the output

The summary table at the top shows pass/fail status for all 44 cases.
For any FAIL, drill into the `### <table> · <key> · <dir>` section to
see the actual plan. Typical failure modes:

- "plan contains Seq Scan" - the index doesn't exist or the planner
  decided it was cheaper to seq-scan. Check that the index migration
  ran and that the seed is large enough.
- "used index X, expected Y" - the planner picked a different index
  than the one migration 000013 was meant to create. Usually means
  another index incidentally covers the predicate; the named index
  may be redundant.
