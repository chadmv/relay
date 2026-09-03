---
title: pg_trgm index for the ?q= substring search, behind a measurement
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# pg_trgm index for the ?q= substring search, behind a measurement

## Summary
The ?q= substring filter runs strpos over every candidate row; on a 50k-row probe that scan dominated the plan at about 31 ms and the LATERAL join did not. A trigram GIN index is the standard remedy for unranked ILIKE or strpos search and should be evaluated against the same probe before it is added, since it costs write amplification on every job insert.

## Context
Lane JB measured this with EXPLAIN ANALYZE during PR #178 and chose not to restructure the CTE; the probe test files were left uncommitted in that lane's worktree and would need re-creating.

## Proposal
Add the extension and a GIN index on jobs.name and users.email in a migration, re-run the probe at 50k and 200k rows, and keep the index only if the no-match needle improves by more than the insert cost it adds.

## Related
- [[feature-2026-09-03-server-side-bound-for-text-search]]
- internal/store/query/jobs.sql (the text-list and text-count statements)
