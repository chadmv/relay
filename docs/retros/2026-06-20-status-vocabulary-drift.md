---
date: 2026-06-20
topic: status-vocabulary-drift
branch: claude/dazzling-easley-b7e79e
pr: 2026-06-20 / status-vocabulary-drift
merge: 2026-06-20 / status-vocabulary-drift
---

# Session Retro: 2026-06-20 - Status Vocabulary Drift

**TL;DR:** Closed `bug-2026-06-10-status-vocabulary-drift` by adding migration `000019_status_vocabulary_checks` (6 CHECK constraints whose value sets were each enumerated from the actual write sites, not from the backlog text), reconciling `JobStatusCounts` to the real status vocabulary, and teaching `jobspec.Validate` to reject priority typos. The headline lesson: tightening a column with a CHECK constraint means auditing EVERY writer of literal values - production code, tests, AND dev scripts - and the only reliable way to prove that is running the whole integration suite, not a filtered `-run` pass. A filtered run hid a second violating test; a code reviewer found a third violation in a seed script.

## What Was Built

The database now enforces the status/enum vocabularies that were previously only conventions, so a typo'd literal or a stale value can no longer silently land in a status column.

- **Migration `000019_status_vocabulary_checks`** (`internal/store/migrations/000019_status_vocabulary_checks.up.sql` / `.down.sql`) - 6 CHECK constraints, each derived from the code that writes the column: `workers.status` (online/offline/stale/revoked), `jobs.status` (pending/running/done/failed/cancelled), `jobs.priority` (low/normal/high), `tasks.status` (pending/dispatched/running/done/failed/timed_out), `task_logs.stream` (stdout/stderr), `scheduled_jobs.overlap_policy` (skip/allow). The up migration runs a priority-normalization `UPDATE` (drifted priorities -> `normal`) before adding `jobs_priority_check`, because priority was never validated before this change so a typo could already be persisted. The down migration drops the 6 constraints.
- **`JobStatusCounts` reconciliation** (`internal/store/query/jobs.sql`) - the aggregate now windows on the real vocabulary: `running` counts `status='running'`, `queued` counts `status='pending'`, `failed_24h` counts `status IN ('failed','cancelled')`. The public JSON field names (including `queued`) were kept to avoid breaking the API/frontend contract.
- **`jobspec.Validate` priority check** (`internal/jobspec/jobspec.go`) - now rejects priority typos, accepting only `""`/`low`/`normal`/`high`, through the single shared ingestion path so REST/CLI/MCP/schedrunner all get it.
- **Tests and fixtures** - new `status_vocabulary_constraints_test.go` (per-column rejection plus a down/up round-trip); rewritten `jobs_stats_integration_test.go`; fixed `jobs_sort_integration_test.go`. Phase-4 fixes removed speculative `preparing`/`prepare_failed` task-status writes from `store_test.go` (`TestWorkerWorkspacesAndSourceColumn`) and fixed `scripts/explain_sort_indexes/seed.go`, which seeded the non-existent `critical`/`queued`/`dispatched` literals.

## Key Decisions

- **Every vocabulary was enumerated from write sites with file:line citations, not from the backlog text.** A CHECK set that does not exactly match every writer is a production outage: the migration fails on existing rows, or a legitimate write gets rejected at runtime. So the value sets came from auditing the code that writes each column, and `tasks.status` (6 values, including `dispatched`/`timed_out`) is deliberately a superset of `jobs.status` (5 values, no `dispatched`/`queued`/`timed_out`) - they are NOT the same vocabulary even though both are "status".
- **`critical` was confirmed not a real priority before tightening `jobs_priority_check`.** README documents only low/normal/high; the `critical` literal in the seed script was a fiction, so it was safe to exclude it and fix the script rather than widen the constraint.
- **Kept the public JSON field name `queued` (now counting `pending`).** Renaming the field to match the backend vocabulary would have broken the frontend and any API consumer mid-flight. For an autonomous run the conservative choice was to reconcile what the bucket counts while leaving the wire name stable, and to file the resulting frontend-side drift as a backlog item rather than fixing it here.
- **Normalize-then-constrain for priority only.** Only `jobs.priority` had an unbounded historical writer (it was never validated), so only it needs a cleanup `UPDATE` before its constraint. Every other constrained column has bounded writers, so no data migration was needed - documented inline in the up migration.

## Problems Encountered

- **A filtered `-run` test pass hid a real failure.** The plan's grep for violating literals found exactly one offending test (`jobs_stats`). Running only the targeted tests passed. But the full integration suite then failed on `store_test.go`'s `TestWorkerWorkspacesAndSourceColumn`, which wrote speculative `preparing`/`prepare_failed` task statuses that the new `tasks_status_check` rejects. A scoped test run cannot prove a constraint-tightening change is safe, because the violating writer may live in a test the filter never selected.
- **A third violation lived in a dev script the test suite never exercises.** A code reviewer caught `scripts/explain_sort_indexes/seed.go` seeding `critical`/`queued`/`dispatched`. Nothing in `make test` runs that script, so neither the filtered nor the full suite would have flagged it - only a literal search across `scripts/` plus a human/agent reviewer caught it.

## Improvement Goals

- **For any constraint-tightening change, the verify step MUST run the entire integration suite, never a filtered `-run` subset.** The whole point of a CHECK constraint is that any writer anywhere can violate it; a scoped run only certifies the writers you already suspected. Make "ran the full suite, observed green" the closing criterion for migrations that add CHECK/NOT NULL/FK constraints.
- **The search for violating literals must include `_test.go` and `scripts/`, not just `internal/` production code.** Tests and dev/seed scripts write literal status values too, and they drift independently. When tightening a column, grep the literal vocabulary across the entire repo (tests and scripts included) and reconcile every hit before adding the constraint. This iteration needed three passes (plan grep, full-suite run, code reviewer) to find all three violators; a repo-wide literal search up front would have found all three at once.

## Backlog Triage

The code reviewer surfaced two pre-existing drift items, both out of scope here (not introduced by this change) and both verified against current code before filing. Both filed:

- **FILED** `docs/backlog/bug-2026-06-20-web-job-status-vocabulary-drift.md` (medium) - the frontend `JobStatus` type (`web/src/jobs/api.ts:3-11`), the `statusColor` switch (`web/src/jobs/status.ts:11-26`), and the "Queued" filter (`web/src/jobs/JobsPage.tsx:12`) still model `queued`/`dispatched`/`timed_out` as job statuses. `jobs.status` is now constraint-locked to pending/running/done/failed/cancelled and never emits those three, so the SPA models statuses the backend cannot produce. Now that the backend vocabulary is enforced in the database, this is a concrete correctness/clarity gap worth tracking.
- **FILED** `docs/backlog/bug-2026-06-20-mcp-overlap-policy-description-says-queue.md` (low) - `internal/mcp/schedules_write.go:18` jsonschema description says the overlap policy is "skip or queue", but the accepted values are `skip`/`allow` (`runner.go:111` special-cases only `skip`; the new `scheduled_jobs_overlap_policy_check` enforces `IN ('skip','allow')`). `queue` is not a real value. A one-line doc-string fix; low but concrete and verified, so tracked rather than dropped.

## Files Most Touched

- `internal/store/migrations/000019_status_vocabulary_checks.up.sql` / `.down.sql` - the 6 CHECK constraints plus the priority-normalization UPDATE.
- `internal/store/query/jobs.sql` - `JobStatusCounts` reconciled to the real status vocabulary (running/pending, failed+cancelled), public JSON names preserved.
- `internal/jobspec/jobspec.go` - `Validate` now rejects priority typos via the single shared ingestion path.
- `internal/store/status_vocabulary_constraints_test.go` - new per-column rejection and down/up round-trip coverage.
- `internal/store/jobs_stats_integration_test.go`, `internal/store/jobs_sort_integration_test.go` - reconciled/fixed to the new vocabulary.
- `internal/store/store_test.go`, `scripts/explain_sort_indexes/seed.go` - Phase-4 removal of speculative/fictional status literals that the new constraints reject.
