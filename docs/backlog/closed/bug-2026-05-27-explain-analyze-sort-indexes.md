---
title: Run EXPLAIN ANALYZE on sort indexes against populated jobs table
type: bug
status: open
created: 2026-05-27
priority: medium
source: list-endpoint-sort retro (docs/retros/2026-05-27-list-endpoint-sort.md)
---

# Run EXPLAIN ANALYZE on sort indexes against populated jobs table

## Summary

The list-endpoint-sort design (`docs/superpowers/specs/2026-05-26-list-endpoint-sort-design.md`) called for a manual `EXPLAIN ANALYZE` pre-merge check against a populated dev `jobs` table to confirm the new composite indexes from migration `000013` are actually being used (not a seq-scan + sort node fallback). This step was not completed during the implementation session and the feature merged without it.

## Repro / Symptoms

Currently unknown — that's the point of the check. If the planner is falling back to seq-scan + sort on any of the new sort paths, large-table performance will degrade silently relative to expectations.

## Proposal

1. Populate a dev `jobs` table with ~100k rows (mix of priorities, statuses, names) plus a few hundred users.
2. For each `pp.Sort` value the dispatch switch handles, run:

   ```sql
   EXPLAIN ANALYZE SELECT j.*, u.email
   FROM jobs j JOIN users u ON u.id = j.submitted_by
   ORDER BY <col> <DIR>, j.id <DIR> LIMIT 50;
   ```

   Plus a cursor-resumption variant:

   ```sql
   EXPLAIN ANALYZE SELECT j.*, u.email
   FROM jobs j JOIN users u ON u.id = j.submitted_by
   WHERE (<col>, j.id) <op> (<cursor_val>, <cursor_id>)
   ORDER BY <col> <DIR>, j.id <DIR> LIMIT 50;
   ```

3. Confirm each plan shows `Index Scan using idx_jobs_*` (forward or backward), NOT `Seq Scan` + `Sort` node.
4. Repeat for `workers`, `users`, `scheduled_jobs`, `reservations`, `agent_enrollments` with a smaller seed (~10k each) — focus is jobs since that's the highest-volume table.
5. If any path seq-scans, fix the index definition before treating the feature as production-ready.

## Acceptance / Done When

- EXPLAIN ANALYZE output for every (table, sort_key, direction) tuple captured in a comment or follow-up retro.
- Each plan uses an index scan, not a sort node.
- Any indexes that don't help are either fixed or documented as known.

## Related

- `internal/store/migrations/000013_paginated_sort_indexes.up.sql` — the 19 indexes under test
- `docs/superpowers/specs/2026-05-26-list-endpoint-sort-design.md` — original spec that called for this verification
