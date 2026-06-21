# Hot-path indexes consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one golang-migrate migration (`000018_hot_path_indexes`) that adds 5 supporting indexes for hot-path queries and drops 4 redundant indexes (3 that duplicate UNIQUE constraints plus 1 superseded log index), with an existence-assertion integration test and a backlog cleanup.

**Architecture:** Pure DDL. A single up/down migration pair under `internal/store/migrations/`. No Go logic, no query (`.sql`) change, no sqlc regeneration. Migrations are embedded (`go:embed`) and run on startup via `store.Migrate`; the integration harness spins up real Postgres and applies them. Verification follows the existing migration-test precedents already in `internal/store`.

**Tech Stack:** PostgreSQL 16, golang-migrate (pgx/v5 driver), testcontainers-go, testify.

---

## Slice independence

**Single sequential backend / store slice. No frontend. No parallelism.**

This is one DDL migration plus one integration test plus a docs `git mv`. There is no frontend work and no second backend slice, so there is nothing for the conductor to parallelize in Phase 3. Execute the tasks below in order.

## Critical files (read before starting)

- `internal/store/migrations/000017_workers_supports_workspaces.{up,down}.sql` - confirms 000017 is the current highest number; 000018 is the next free.
- `internal/store/migrations/000011_pagination_indexes.{up,down}.sql` and `000013_paginated_sort_indexes.{up,down}.sql` - the exact index-migration style this plan matches (plain `CREATE INDEX`, `DROP INDEX IF EXISTS`, header comment).
- `internal/store/migrations/000001_initial.up.sql:97-101` and `000005_agent_auth.up.sql:1-15` - original definitions of the 4 dropped indexes; the down migration must restore them byte-faithfully.
- `internal/store/sort_indexes_integration_test.go` - the `pg_indexes` existence-assertion precedent (`TestSortIndexesExist`) this plan's test mirrors.
- `internal/store/migrate_test.go` (`TestMigrate`) - proves up applies cleanly + is idempotent; this migration is covered by it automatically.
- `internal/store/migrate_down_test.go` and `internal/store/export_test.go` (`MigrateTo`) - the down-migration round-trip pattern via `store.MigrateTo(dsn, version)`.

## Verification approach (and why)

**How the project verifies a new migration today.** It already has integration coverage, build-tagged `//go:build integration`, run by `make test-integration`:

1. `TestMigrate` (`internal/store/migrate_test.go`) spins up a real Postgres container, calls `store.Migrate(dsn)` (the same embedded path the server runs on startup), asserts it succeeds, then runs it again to assert idempotency. **Any new migration that fails to apply cleanly up fails this test automatically.** No edit to this test is needed.
2. `TestSortIndexesExist` (`internal/store/sort_indexes_integration_test.go`) queries `pg_indexes` and asserts migration 000013's indexes exist. This is the project's established precedent for "assert an index migration produced the expected index set."
3. `migrate_down_test.go` drives `store.MigrateTo(dsn, version)` (exported test-only in `export_test.go`) to exercise a specific down migration and round-trip.

**What this plan adds, and why it is proportionate (not over-engineered).** This is additive/idempotent DDL with no Go logic, so classic red-green TDD does not apply (there is no function to drive). The proportionate bar is:

- **Up applies cleanly:** already covered by `TestMigrate` (startup-apply path). No new test needed for the "up applies" claim.
- **The new index set is correct AND the redundant indexes are actually gone:** add one focused integration test, `TestHotPathIndexes` in a new `hot_path_indexes_integration_test.go`, mirroring `TestSortIndexesExist`. It asserts (a) the 5 new indexes are present after the full up migration and (b) the 4 dropped indexes are absent. This is the single highest-value assertion: it pins the exact intent of the migration and would catch a typo'd index name or a forgotten drop, which `TestMigrate` alone cannot.
- **Down reverses cleanly (up -> down -> up round-trips):** add `TestHotPathIndexesDownUp` in the same file, using `store.MigrateTo(dsn, 17)` then `store.Migrate(dsn)` (back to head), then re-assert the up-state index set. This proves the down migration restores the original 4 indexes (no duplicate-name collision on the second up) and removes the 5 new ones, and that a rollback is clean. This directly exercises the down `.sql` the spec requires.

We deliberately do **not** add `EXPLAIN`/query-plan assertions. The spec's redundancy claims (dropped indexes are served by UNIQUE-constraint btrees, equality lookups only) were verified by reading the queries; asserting query plans in a test would be brittle (planner-version-dependent) and is not the project's pattern. The two existence tests plus the pre-existing `TestMigrate` are the proportionate, in-pattern bar for index DDL.

**Closing checks for `make test` / integration:** `make test` (unit, no Docker) must stay green - it does not run integration-tagged files, so it proves nothing about the DDL but confirms no accidental breakage. `make test-integration` for `internal/store/...` runs `TestMigrate`, the two new tests, and the down tests, and must pass. `make generate` must produce **no diff** (no `.sql` query or schema-type change, so no sqlc/proto regeneration).

## File structure

- Create: `internal/store/migrations/000018_hot_path_indexes.up.sql` - 5 `CREATE INDEX` then 4 `DROP INDEX IF EXISTS`.
- Create: `internal/store/migrations/000018_hot_path_indexes.down.sql` - drop the 5 added, recreate the 4 dropped with their original definitions.
- Create: `internal/store/hot_path_indexes_integration_test.go` - existence assertions + down/up round-trip (`//go:build integration`).
- Move: `docs/backlog/feature-2026-06-10-hot-path-indexes.md` -> `docs/backlog/closed/` (on ship).
- Move: `docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` -> `docs/backlog/closed/` (on ship).

---

### Task 1: Write the up and down migration files

**Files:**
- Create: `internal/store/migrations/000018_hot_path_indexes.up.sql`
- Create: `internal/store/migrations/000018_hot_path_indexes.down.sql`

This task creates the DDL only. There is no unit test to write first (it is embedded SQL with no Go entry point); Task 2 supplies the failing-then-passing integration coverage. Create both files in one task so the migration pair is never half-present (golang-migrate requires both `.up.sql` and `.down.sql`).

- [ ] **Step 1: Write the up migration**

Create `internal/store/migrations/000018_hot_path_indexes.up.sql` with exactly this content. Creates come before drops so the new composite `idx_task_logs_task_id_id` exists before the superseded single-column `idx_task_logs_task_id` is removed.

```sql
-- Hot-path indexes consolidation: add 5 supporting indexes for hot queries,
-- drop 4 redundant ones (3 duplicate a UNIQUE-constraint btree, 1 superseded
-- by a new composite). Plain CREATE/DROP INDEX (no CONCURRENTLY): golang-migrate
-- wraps each migration in a transaction, and CONCURRENTLY cannot run in one.

-- Add: missing supporting indexes for hot-path queries.
CREATE INDEX idx_task_deps_depends_on
  ON task_dependencies(depends_on_task_id);

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');

CREATE INDEX idx_task_logs_task_id_id
  ON task_logs(task_id, id);

CREATE INDEX idx_jobs_status_updated
  ON jobs(status, updated_at);

CREATE INDEX idx_workers_status_disabled
  ON workers(status, disabled_at);

-- Drop: redundant indexes (created after the new composite so nothing is
-- briefly unindexed). IF EXISTS keeps the drop idempotent.
DROP INDEX IF EXISTS idx_task_logs_task_id;           -- 000001:100, superseded by idx_task_logs_task_id_id
DROP INDEX IF EXISTS idx_api_tokens_token_hash;       -- 000001:101, dup of UNIQUE(token_hash)
DROP INDEX IF EXISTS ix_agent_enrollments_token_hash; -- 000005:11,  dup of UNIQUE(token_hash)
DROP INDEX IF EXISTS ix_workers_agent_token_hash;     -- 000005:14,  dup of UNIQUE(agent_token_hash)
```

- [ ] **Step 2: Write the down migration**

Create `internal/store/migrations/000018_hot_path_indexes.down.sql`. It drops the 5 added indexes and recreates the 4 dropped ones with their **original** definitions, copied byte-faithfully from `000001_initial.up.sql:100-101` and `000005_agent_auth.up.sql:11,14-15` (including the partial `WHERE agent_token_hash IS NOT NULL` clause on the workers index).

```sql
-- Drop indexes added by the up migration.
DROP INDEX IF EXISTS idx_task_deps_depends_on;
DROP INDEX IF EXISTS idx_tasks_worker_active;
DROP INDEX IF EXISTS idx_task_logs_task_id_id;
DROP INDEX IF EXISTS idx_jobs_status_updated;
DROP INDEX IF EXISTS idx_workers_status_disabled;

-- Recreate indexes the up migration dropped, with their original definitions.
CREATE INDEX idx_task_logs_task_id ON task_logs(task_id);                       -- 000001:100
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);               -- 000001:101
CREATE INDEX ix_agent_enrollments_token_hash ON agent_enrollments(token_hash);  -- 000005:11
CREATE INDEX ix_workers_agent_token_hash
  ON workers(agent_token_hash) WHERE agent_token_hash IS NOT NULL;              -- 000005:14-15
```

- [ ] **Step 3: Confirm no sqlc/proto regeneration is needed**

Run: `make generate`
Expected: no change to any tracked file. Verify with `git diff --ignore-all-space` - it should print nothing (no `*.sql.go` / `models.go` / proto diff). If line-ending-only hunks appear from the generator, revert them with `git checkout -- <file>`. There is no `.sql` query change here, so `make generate` is only a guard that nothing regenerated.

Run: `git status --porcelain internal/store/migrations`
Expected: shows only the two new untracked `000018_*` files.

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations/000018_hot_path_indexes.up.sql internal/store/migrations/000018_hot_path_indexes.down.sql
git commit -m "feat(store): add migration 000018 hot-path indexes"
```

---

### Task 2: Add the index-existence and down/up round-trip integration test

**Files:**
- Create: `internal/store/hot_path_indexes_integration_test.go`

This test mirrors `internal/store/sort_indexes_integration_test.go` (existence assertion via `pg_indexes`) and `internal/store/migrate_down_test.go` (down round-trip via `store.MigrateTo`). It uses the existing `newTestPool(t)` helper (from `testhelper_test.go`) which spins up Postgres and applies all migrations up to head, and `newMigratedPoolWithDSN(t)` (from `migrate_down_test.go`) which also returns the `pgx5://` DSN for driving `MigrateTo`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/hot_path_indexes_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/store"
)

// hotPathDownTarget is the schema version just below 000018_hot_path_indexes,
// i.e. the state its down migration restores.
const hotPathDownTarget = 17

// indexesAdded are the 5 indexes migration 000018 must create.
var indexesAdded = []string{
	"idx_task_deps_depends_on",
	"idx_tasks_worker_active",
	"idx_task_logs_task_id_id",
	"idx_jobs_status_updated",
	"idx_workers_status_disabled",
}

// indexesDropped are the 4 indexes migration 000018 must remove.
var indexesDropped = []string{
	"idx_task_logs_task_id",
	"idx_api_tokens_token_hash",
	"ix_agent_enrollments_token_hash",
	"ix_workers_agent_token_hash",
}

// publicIndexSet returns the set of index names in the public schema.
func publicIndexSet(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT indexname FROM pg_indexes WHERE schemaname = 'public'`)
	require.NoError(t, err)
	defer rows.Close()

	got := make(map[string]bool)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		got[name] = true
	}
	require.NoError(t, rows.Err())
	return got
}

// TestHotPathIndexes confirms migration 000018 added its 5 indexes and removed
// the 4 redundant ones after the full up migration (the newTestPool path).
func TestHotPathIndexes(t *testing.T) {
	pool := newTestPool(t)
	got := publicIndexSet(t, pool)

	for _, name := range indexesAdded {
		assert.True(t, got[name], "expected index %q to exist after up (check migration 000018)", name)
	}
	for _, name := range indexesDropped {
		assert.False(t, got[name], "expected index %q to be dropped by up (check migration 000018)", name)
	}
}

// TestHotPathIndexesDownUp confirms the 000018 down migration restores the
// original 4 indexes and removes the new 5, and that migrating back up
// round-trips cleanly (no duplicate-name collision on the second up).
func TestHotPathIndexesDownUp(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)

	// Roll back just 000018.
	require.NoError(t, store.MigrateTo(dsn, hotPathDownTarget),
		"down migration to 000017 must succeed")

	down := publicIndexSet(t, pool)
	for _, name := range indexesDropped {
		assert.True(t, down[name], "down must restore original index %q", name)
	}
	for _, name := range indexesAdded {
		assert.False(t, down[name], "down must remove new index %q", name)
	}

	// Re-apply 000018; a clean re-up proves the down left a consistent state.
	require.NoError(t, store.Migrate(dsn), "re-applying up after down must succeed")

	up := publicIndexSet(t, pool)
	for _, name := range indexesAdded {
		assert.True(t, up[name], "second up must re-create index %q", name)
	}
	for _, name := range indexesDropped {
		assert.False(t, up[name], "second up must re-drop index %q", name)
	}
}
```

- [ ] **Step 2: Run the test to verify it passes against the migration from Task 1**

Because the migration already exists (Task 1), these tests should pass on first run. Run them to confirm the migration produces exactly the intended index set.

Run: `go test -tags integration -p 1 ./internal/store/... -run 'TestHotPathIndexes' -v -timeout 300s`
Expected: PASS for both `TestHotPathIndexes` and `TestHotPathIndexesDownUp`.

Note: this requires Docker Desktop running. If `TestHotPathIndexes` fails on a "dropped" index still being present, re-check the `DROP INDEX IF EXISTS` names in `000018_hot_path_indexes.up.sql` match the originals. If `TestHotPathIndexesDownUp` fails on the second up with a "relation already exists" error, the down migration failed to drop one of the 5 added indexes - re-check `000018_hot_path_indexes.down.sql`.

- [ ] **Step 3: Run the pre-existing migration tests to confirm clean up + idempotency are unaffected**

Run: `go test -tags integration -p 1 ./internal/store/... -run 'TestMigrate' -v -timeout 300s`
Expected: PASS for `TestMigrate` (clean up + idempotent re-run) and the `TestMigrateDownTaskCommands*` tests (unchanged by this migration).

- [ ] **Step 4: Commit**

```bash
git add internal/store/hot_path_indexes_integration_test.go
git commit -m "test(store): assert 000018 hot-path index set and down/up round-trip"
```

---

### Task 3: Run the full relevant suites

**Files:** none (verification only).

- [ ] **Step 1: Unit tests (no Docker)**

Run: `make test`
Expected: PASS. Integration-tagged files do not run here; this only confirms nothing else broke.

- [ ] **Step 2: Store integration suite**

Run: `go test -tags integration -p 1 ./internal/store/... -v -timeout 600s`
Expected: PASS (all store integration tests, including `TestMigrate`, `TestSortIndexesExist`, the two new hot-path tests, and the down tests). Requires Docker Desktop running and the `desktop-linux` context.

- [ ] **Step 3: Confirm `make generate` is still clean**

Run: `make generate` then `git diff --ignore-all-space`
Expected: no diff. Revert any LF-only generator hunks with `git checkout -- <file>` if they appear.

---

### Task 4: Close the backlog items

**Files:**
- Move: `docs/backlog/feature-2026-06-10-hot-path-indexes.md` -> `docs/backlog/closed/feature-2026-06-10-hot-path-indexes.md`
- Move: `docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` -> `docs/backlog/closed/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md`

Both items are fully delivered by migration 000018: the feature item's 3 adds + 3 drops, plus the bug item's two stats indexes (`idx_jobs_status_updated` for `JobStatusCounts`, `idx_workers_status_disabled` for `WorkerStatusCounts`), are all in this migration. Per the relay convention, the `git mv` to `docs/backlog/closed/` is required scope.

- [ ] **Step 1: Move both backlog files**

```bash
git mv docs/backlog/feature-2026-06-10-hot-path-indexes.md docs/backlog/closed/feature-2026-06-10-hot-path-indexes.md
git mv docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md docs/backlog/closed/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md
```

- [ ] **Step 2: Verify the moves**

Run: `git status --porcelain docs/backlog`
Expected: two renamed (`R`) entries, both into `docs/backlog/closed/`.

- [ ] **Step 3: Commit**

```bash
git add -A docs/backlog
git commit -m "docs(backlog): close hot-path-indexes and jobstatuscounts items (migration 000018)"
```

---

## Self-review

**Spec coverage:**
- 5 adds and 4 drops with exact definitions: Task 1 (matches the spec's "Final index set", including the non-partial `idx_jobs_status_updated` correction).
- Down reverses exactly with original definitions: Task 1 Step 2 (byte-faithful from 000001/000005).
- No CONCURRENTLY (transaction-wrapped): documented in the up-migration header, matching 000011/000013 convention.
- Migration applies on startup: covered by existing `TestMigrate` (noted in verification approach), exercised in Task 2 Step 3 and Task 3 Step 2.
- Down/up round-trip: Task 2 `TestHotPathIndexesDownUp`.
- No `make generate` diff: Task 1 Step 3 and Task 3 Step 3.
- Both backlog items closed via `git mv`: Task 4.
- Next free number 000018: confirmed (highest existing is 000017).

**Placeholder scan:** none - every SQL statement and every Go test body is shown in full; all commands have expected output.

**Type/name consistency:** index names in the up SQL, down SQL, and the test's `indexesAdded`/`indexesDropped` slices all match. `hotPathDownTarget = 17` matches the spec's down target (state below 000018). Helper names `newTestPool` and `newMigratedPoolWithDSN` match the existing files they come from.
