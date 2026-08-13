# Web-enabler List Endpoints (GET /v1/invites, GET /v1/auth/tokens) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the two read-only list endpoints that unblock the Admin Invites tab and the Profile Sessions tab: `GET /v1/invites` (admin, every invite in every state) and `GET /v1/auth/tokens` (any authenticated user, own unexpired rows only, with a current-session flag), both on the shipped `parsePage`/`buildPage`/`page[T]` machinery.

**Architecture:** Two query families appended to existing `.sql` files, one index migration, one handler added to `internal/api/invites.go`, one new `internal/api/tokens.go`, two route registrations. Both projections enumerate columns and never select `token_hash`, so the generated row types have no field for it and a leak is a compile error rather than a review miss. `is_current` is a `pgtype.UUID` comparison in Go against the `TokenID` that `BearerAuth` already resolved; neither handler hashes anything. No writes, no transactions, no gRPC, no epoch fence.

**Tech Stack:** Go 1.24, PostgreSQL 16, sqlc v1.30.0 (`sql_package: pgx/v5`, `emit_pointers_for_null_types: true`, `emit_sql_as_comment: true`), pgx/v5, golang-migrate, testcontainers-go, testify.

**Spec:** `docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md` (approved; do not reopen its decisions except where "Deviations from the spec" below records one).

**Backlog item:** `docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md` is **amended, not closed** - it still owns `POST /v1/jobs/{id}/retry`, which is out of scope here and gets its own item in Phase 6. Do **not** run `/backlog close` on it.

---

## Slice independence declaration

- **Backend slice: ONE `relay-backend-engineer`. Frontend slice: NONE.** Every file touched is `.sql`, `.go`, or `.md`. **Zero files under `web/`.** The conductor must not allocate a frontend slice for Phase 3, and the `web/dist` revert rule (`git checkout -- web/dist/`) does not apply because no `web/` build runs. If any task ends with a dirty `web/` path, that is a defect in execution, not expected churn.
- **Tasks are strictly SEQUENTIAL. No parallelism is available within this plan.** The dependency chain is hard: Tasks 3, 5, 6 need the sqlc types Task 2 generates; Tasks 8-11 need the types Tasks 7 and 9 generate; Task 11's reflect gate needs all six row types to exist; and Tasks 2, 5, 7 and 9 each run `sqlc generate`, which rewrites **every** file in `internal/store/`. Two engineers running `sqlc generate` against the same worktree concurrently will interleave line-ending churn and lose each other's content. Task 3 and Task 7 both edit `internal/api/server.go`; concurrent writers there have burned this project before.
- **`relay-integration-tester`: not in Phase 3.** The integration tests in this plan **are** the acceptance evidence and are written by the implementing engineer under TDD - they cannot be deferred to a later agent without destroying the RED evidence. Dispatch `relay-integration-tester` in Phase 4 as usual, to attack the surface independently (it should look hardest at cursor-walk correctness under concurrent inserts and at the `total`-vs-page consistency claim).

---

## Critical files

Read these before starting. They are the entire blast radius plus the precedents.

- `internal/api/agent_enrollments.go:75-225` - **the template for both new handlers.** `AgentEnrollmentsSortSpec` at `:75-81`, the four row-to-map / row-key pairs at `:83-149`, `handleListAgentEnrollments` at `:151-225`. Note the `default: panic(...)` at `:215-217`: it is reachable from user input for any allowlisted key that lacks a dispatch arm.
- `internal/store/query/agent_enrollments.sql:23-65` - the template for both new query families: explicit column list, `(sort_col, id)` keyset comparison gated on `cursor_set`, `LIMIT page_limit + 1`, one query per sort arm, plus a `COUNT(*)` over the same predicate.
- `internal/api/pagination.go` - `SortSpec`/`SortKeyKind` `:140-155`; `parseSort` `:174-203` (**strips a leading `-` before the allowlist check at `:178-181`, which is why every key is reachable in both directions**); limits `:205-206`; `parsePage` `:239-286` (writes its own 400s); `page[T]` `:288-293`; `buildPage` `:305-329`.
- `internal/api/middleware.go:16-46` - `BearerAuth`. Hashes once at `:25`, resolves the row at `:27`, injects `AuthUser.TokenID` at `:36-42`. The expiry check at `:32-35` is `row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now())` - **a NULL `expires_at` authenticates forever.** `AdminOnly` at `:50-59` returns 403.
- `internal/api/context.go:14-20` - `AuthUser`, including `TokenID pgtype.UUID`. Already exists; no change needed.
- `internal/api/server.go:96-100` (self-service auth block, where `GET /v1/auth/tokens` goes) and `:138-139` (invites block, where `GET /v1/invites` goes). `writeJSON` `:182-186`, `writeError` `:188-190`, `readJSON` `:199-211` (**not called by either new handler - they have no body**), `uuidStr` `:217-224`.
- `internal/store/query/invites.sql:1-12` - three existing queries, untouched. `GetInviteByTokenHash:7` is `SELECT *` - **do not copy that shape**, it is exactly how `token_hash` reaches a handler.
- `internal/store/query/tokens.sql:1-29` - five existing queries, untouched. `DeleteOtherTokensForUser:28-29` is the password-change path that the new `api_tokens` index also de-seq-scans.
- `internal/store/migrations/000001_initial.up.sql:13-19` - `api_tokens`: five columns, `expires_at` **nullable**, no `last_used_at`.
- `internal/store/migrations/000002_invites.up.sql:1-10` - `invites`: `expires_at NOT NULL`, `email`/`used_at`/`used_by` nullable, `created_by` NOT NULL FK to `users`.
- `internal/store/migrations/000013_paginated_sort_indexes.up.sql:1-35` and `000018_hot_path_indexes.up.sql:1-4` - index-migration house style (DESC-only variants; plain `CREATE INDEX`, never `CONCURRENTLY`, because golang-migrate wraps each migration in a transaction).
- `internal/store/sort_indexes_integration_test.go:16-46` - the `pg_indexes` assertion pattern reused in Task 1.
- `internal/store/migrate_down_test.go:20-51,74-97` - `store.MigrateTo(dsn, version)` and `newMigratedPoolWithDSN`, which make the **down** migration testable. Reused in Task 1.
- `internal/api/testhelper_test.go:19-25` - `pageEnvelope[T]`, the decode target for every list assertion. `:72-98` - `newTestPool`.
- `internal/api/api_test.go:27-61` - `newTestServer`, `createTestUser`, and `createTestToken`, **which mints a NULL-`expires_at` token** (`:57`). Task 7 explains why the tokens tests must not use it for the caller.
- `internal/api/tasks_integration_test.go:60-68` - `newTestServerWithPool`, needed wherever a test seeds rows directly through the pool.
- `internal/api/workspaces_test.go:20-27` - `fmtUUID(pgtype.UUID) string`, already in package `api_test`. Reuse it; do not define a second one.
- `internal/api/agent_enrollments_sort_integration_test.go:19-192` - the sort/pagination integration-test shape for a list endpoint (per-arm subtests, single-page baseline vs paged walk, cursor-mismatch 400).
- `internal/api/agent_enrollments_test.go:79-121` - the 403/200 admin-gating test plus the existing "no `token`/`token_hash` key" loop, which Task 11 strengthens into a raw-body sweep.
- `internal/api/workers_response_test.go:1-22` - proof that **unit tests in `package api` with no build tag** are house style. That is where the pure mapping-helper tests and the reflect projection gate go.
- `README.md:1088-1097` (sort allowlist table), `:1147-1153` (Session endpoints), `:1235-1255` (Invites section) - the documented REST reference, updated in Task 12.
- `CLAUDE.md` Invariants block - read once. Only three apply here; see below.

### Invariants: which apply

Do **not** apply, stated explicitly so nobody re-derives it: **epoch fence** (no write to `tasks.status` or `task_logs`, no `assignment_epoch`, no `worker_id`), **end the generation before releasing the resource** (no async lifecycle), **single job-spec pipeline** (no spec parsed), **one bounded sender per gRPC stream** (no gRPC surface), **identity-checked teardown** (no connection state), **no interior pointers across locks** (no shared registry read or written).

Do apply:

1. **Single JSON entry point.** Both endpoints are `GET`s with no body, so `readJSON` is not called. The live obligation is negative: **neither handler may introduce a body reader** and neither may decode query parameters through a decoder. All input arrives via `parsePage` and `r.URL.Query()`. Output goes through `writeJSON`/`writeError`.
2. **All hashing goes through `internal/tokenhash.Hash`; never inline `sha256.Sum256` at a new site.** Honored in the strongest form available: **neither handler hashes anything at all.** If a diff adds a hash call to either handler, that is a design regression, not a detail.
3. **Authorization is resolved server-side, never taken from the wire.** Invites is admin-gated by middleware. Tokens is scoped by `authUser.ID` from the context. There is no `user_id` parameter and there must never be one.

---

## Conventions and gotchas (read once, apply everywhere)

1. **SQL is the source of truth.** Edit `internal/store/query/*.sql`, then regenerate. **Never hand-edit `internal/store/*.sql.go` or `internal/store/models.go`.**

2. **`make` is NOT on PATH in this shell.** Every command in this plan is the raw tool invocation. For reference: `make generate` = `sqlc generate` then `buf generate`; `make test` = `go test ./... -timeout 120s`; `make test-integration` = `go test -tags integration -p 1 ./... -timeout 900s`. **Run `sqlc generate` alone**, never `buf generate`: no `.proto` changes here, and `buf generate` would churn `internal/proto/` for nothing.

3. **The sqlc CRLF hazard, in full.** sqlc emits LF; this repo is checked out CRLF, so `sqlc generate` rewrites line endings across **every** file it emits, not only the ones whose content changed. After every generate, run:

   ```
   sqlc generate
   git status --short internal/store/
   git diff --ignore-all-space
   ```

   - The **only** files expected to show a real content change in this plan are `internal/store/invites.sql.go` (Tasks 2 and 5) and `internal/store/tokens.sql.go` (Tasks 7 and 9).
   - `internal/store/models.go` **must show no content change**: this plan adds no column, no table, no type. If `git diff --ignore-all-space internal/store/models.go` is empty but `git status` lists it, that is pure line-ending churn: `git checkout -- internal/store/models.go`.
   - Same test, file by file, for every other generated file `git status` lists. The full candidate set is: `internal/store/agent_enrollments.sql.go`, `db.go`, `jobs.sql.go`, `models.go`, `reservations.sql.go`, `scheduled_jobs.sql.go`, `tasks.sql.go`, `users.sql.go`, `worker_workspaces.sql.go`, `workers.sql.go`. For each: if `git diff --ignore-all-space <file>` prints nothing, run `git checkout -- <file>`.
   - **Known trap - the revert can silently discard the regenerated file.** A previous iteration lost a regenerated query this way and shipped a doc comment that contradicted its own SQL source. `git checkout --` on the wrong file here loses the new query functions and the build stops compiling, or worse, compiles with a stale doc comment. **After every cleanup, re-open the file you changed and confirm by eye:**
     - Task 2: `internal/store/invites.sql.go` contains `func (q *Queries) ListInvitesPage`, `ListInvitesPageByCreatedAsc`, `CountInvites`, the `ListInvitesPageRow` / `ListInvitesPageByCreatedAscRow` structs, and doc comments whose SQL text matches `internal/store/query/invites.sql` word for word (`emit_sql_as_comment: true` copies the comment block and the statement into the doc comment).
     - Task 5: the same file additionally contains `ListInvitesPageByExpiresDesc` and `ListInvitesPageByExpiresAsc`.
     - Tasks 7 and 9: `internal/store/tokens.sql.go` contains `ListActiveTokensForUserPage`, `ListActiveTokensForUserPageByCreatedAsc`, `CountActiveTokensForUser`, and after Task 9 their doc comments must contain `expires_at IS NULL OR expires_at > NOW()`. A doc comment that still shows the naive `expires_at > NOW()` after Task 9 means the revert ate the regeneration.
     - Never run `git checkout -- internal/store/` as a directory.

4. **Weak REDs, and where they apply.** For a read endpoint, "the route does not exist yet" is the default RED and it is **weak evidence**: it proves the handler is absent, not that the handler is right. In this codebase the shape of that weak RED is specific and worth predicting exactly:
   - `GET /v1/invites` before Task 3 returns **405 Method Not Allowed**, not 404, because `POST /v1/invites` is registered at `server.go:139` and Go's `ServeMux` returns 405 when a pattern matches the path but not the method.
   - `GET /v1/auth/tokens` before Task 7 returns **405** for the same reason (`DELETE /v1/auth/tokens` at `server.go:100`).
   - In production, where `s.StaticHandler` is non-nil and `mux.Handle("/", ...)` is registered (`server.go:173-175`), the same request would instead be served the SPA's `index.html`. Tests construct the server with `StaticHandler` nil, so 405 is what the test sees. This asymmetry is itself the reason a 405 RED is weak: it is an artifact of routing, not of behavior.

   Wherever a task's RED is the 405, the task **names its substitute evidence**: a later assertion in the same or the next task that fails against a plausible-but-wrong implementation, not merely against an absent one. Tasks 5, 9 and 11 carry the real discriminating REDs.

5. **Integration tests are the gate. `go test ./...` on Windows proves nothing about this slice.** Every behavioral test here is `//go:build integration` and needs Docker Desktop running (this machine uses the `desktop-linux` context automatically). `-p 1` is mandatory - each test spins up its own Postgres container. A green unit run is a no-regression signal and **is never evidence that either endpoint works**. The exceptions are the three unit-test files (Tasks 4, 8, 11's reflect gate), which are `package api` with no build tag and do run under `go test ./...`.

6. **`go vet -tags integration ./...` is the compile gate for integration-tagged code.** `go build ./...` does not compile `//go:build integration` files. Run both after every task that touches a test file.

7. **Hard gate: no existing test may have an assertion changed.** No existing test file is edited by this plan at all. If any existing test goes red, STOP and report it as a finding rather than adjusting it. The one predictable near-miss: `TestListAgentEnrollments_AdminOnly` (`agent_enrollments_test.go:79-121`) must stay green and byte-identical throughout.

8. **Commit cadence.** Commit at each task boundary except Task 9, whose middle step is a deliberately wrong predicate that must **never** be committed. Every commit must leave the tree compiling, `go vet -tags integration ./...` clean, and the integration tests for everything landed so far green.

9. **No em dashes or en dashes** anywhere, including SQL comments, Go comments, test messages and commit messages. Regular hyphens only.

10. **`SELECT *` is forbidden in both new query families.** It is the single mechanism by which `token_hash` reaches a handler, and `GetInviteByTokenHash` sits four lines above the code you are writing as a tempting pattern to copy. Enumerate columns.

---

## Two evidence regimes, and which task produces which

| Property | Evidence regime | Where |
| --- | --- | --- |
| Route exists, gating is right, response shape is right | **Weak RED (405) plus behavioral assertions** on shape, gating, and content that fail against a wrong handler | Tasks 3, 6, 7 |
| A missing sort dispatch arm 500s on user input | **Behavioral RED**: `?sort=-expires_at` returns 400 before the key is allowlisted, and the spec-driven loop test covers every future key in both directions | Task 5 |
| `expires_at IS NULL` rows must be listed | **Behavioral RED, captured verbatim, produced by the naive predicate itself** | Task 9 |
| Expired rows must not be listed | **Behavioral RED, captured verbatim** (the expired row is present before the filter lands) | Task 9 |
| `token_hash` can never reach a response | **Structural assertion** (reflect over the generated row types) **plus a raw-body substring sweep** | Task 11 |

**Do not collapse Task 9's three steps.** Its middle step is the only point in this plan at which a behavioral RED for the NULL-expiry predicate can exist, and it cannot be reproduced afterwards without reverting.

---

## Task 1: Migration 000020 - three indexes, with up and down both tested

`000019_status_vocabulary_checks` is the highest existing migration, so 000020 is the next free number. `invites` has no index of any kind today, and `api_tokens` has had no `user_id` index since `000018_hot_path_indexes.up.sql:25` dropped the redundant `token_hash` one, so `DeleteTokensForUser` and `DeleteOtherTokensForUser` sequential-scan the table right now.

**Files:**
- Create: `internal/store/migrations/000020_list_endpoint_indexes.up.sql`
- Create: `internal/store/migrations/000020_list_endpoint_indexes.down.sql`
- Create: `internal/store/list_endpoint_indexes_integration_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/list_endpoint_indexes_integration_test.go`:

```go
//go:build integration

package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listEndpointIndexes are the three indexes migration 000020 adds to support
// GET /v1/invites and GET /v1/auth/tokens. The expected indexdef fragment is
// asserted, not just the name, because an index with the right name over the
// wrong columns supports nothing and would leave the keyset scans on a
// sequential plan while this test stayed green.
var listEndpointIndexes = []struct {
	table    string
	index    string
	columns  string
}{
	{"invites", "idx_invites_created_id", "(created_at DESC, id DESC)"},
	{"invites", "idx_invites_expires_id", "(expires_at DESC, id DESC)"},
	{"api_tokens", "idx_api_tokens_user_created_id", "(user_id, created_at DESC, id DESC)"},
}

func TestMigration000020_ListEndpointIndexesExist(t *testing.T) {
	pool := newTestPool(t)
	ctx := t.Context()

	for _, tc := range listEndpointIndexes {
		var def string
		err := pool.QueryRow(ctx,
			`SELECT indexdef FROM pg_indexes
			 WHERE schemaname = 'public' AND tablename = $1 AND indexname = $2`,
			tc.table, tc.index).Scan(&def)
		require.NoError(t, err, "index %s on %s is missing (check migration 000020)", tc.index, tc.table)
		assert.Contains(t, def, tc.columns,
			"index %s exists but over the wrong columns: %s", tc.index, def)
	}
}

// The down migration must actually drop what the up migration created.
// store.MigrateTo drives golang-migrate down past the up-only startup path;
// migrate_down_test.go:24-51 established the fixture.
func TestMigration000020_DownDropsListEndpointIndexes(t *testing.T) {
	pool, dsn := newMigratedPoolWithDSN(t)
	ctx := t.Context()

	// Positive control: they exist before the down migration runs, so a green
	// result below cannot come from them never having been created.
	for _, tc := range listEndpointIndexes {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`,
			tc.index).Scan(&n))
		require.Equal(t, 1, n, "fixture: %s must exist before the down migration", tc.index)
	}

	require.NoError(t, storeMigrateTo(dsn, 19))

	for _, tc := range listEndpointIndexes {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`,
			tc.index).Scan(&n))
		assert.Equal(t, 0, n, "%s must be dropped by 000020_list_endpoint_indexes.down.sql", tc.index)
	}

	// The tables themselves must survive: a down migration that dropped the
	// table would also satisfy every assertion above.
	for _, table := range []string{"invites", "api_tokens"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = $1`, table).Scan(&n))
		assert.Equal(t, 1, n, "%s must still exist after the down migration", table)
	}
}
```

Add the import and the tiny alias so the test reads cleanly - put this at the bottom of the same file:

```go
// storeMigrateTo is a one-line alias so the import of relay/internal/store does
// not shadow the package-level helpers this file already uses.
func storeMigrateTo(dsn string, version uint) error { return store.MigrateTo(dsn, version) }
```

and add `"relay/internal/store"` to the import block.

- [ ] **Step 2: Run the tests to verify they fail**

Docker Desktop must be running.

```
go test -tags integration -p 1 ./internal/store/... -run "TestMigration000020" -v -timeout 600s
```

Expected: **both FAIL.** `TestMigration000020_ListEndpointIndexesExist` fails on `index idx_invites_created_id on invites is missing (check migration 000020)` with a `no rows in result set` error. `TestMigration000020_DownDropsListEndpointIndexes` fails on the fixture line `fixture: idx_invites_created_id must exist before the down migration`. This is a genuine behavioral RED against the schema, not a compile error.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/000020_list_endpoint_indexes.up.sql`:

```sql
-- Supporting indexes for the two list endpoints added alongside this migration:
-- GET /v1/invites (keyset over created_at and expires_at) and
-- GET /v1/auth/tokens (keyset over created_at, scoped to one user).
--
-- Only DESC variants, matching 000013_paginated_sort_indexes.up.sql:7-10:
-- Postgres scans a btree backwards for the ascending arms, so a second ASC
-- index would be dead weight. Both sort columns here are NOT NULL on invites,
-- so no NULLS FIRST/LAST pair is needed (contrast 000013:15-16, which needed
-- one for the nullable workers.last_seen_at).
--
-- Plain CREATE INDEX, never CONCURRENTLY: golang-migrate wraps each migration
-- in a transaction and CONCURRENTLY cannot run inside one (000018:2-4).

CREATE INDEX idx_invites_created_id ON invites (created_at DESC, id DESC);
CREATE INDEX idx_invites_expires_id ON invites (expires_at DESC, id DESC);

-- api_tokens has had no user_id index since 000018:25 dropped the redundant
-- token_hash one, leaving only the UNIQUE(token_hash) btree. DeleteTokensForUser
-- and DeleteOtherTokensForUser (the password-change path) sequential-scan the
-- table today; this composite serves the new keyset list and both of them.
CREATE INDEX idx_api_tokens_user_created_id ON api_tokens (user_id, created_at DESC, id DESC);
```

Create `internal/store/migrations/000020_list_endpoint_indexes.down.sql`:

```sql
DROP INDEX IF EXISTS idx_api_tokens_user_created_id;
DROP INDEX IF EXISTS idx_invites_expires_id;
DROP INDEX IF EXISTS idx_invites_created_id;
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test -tags integration -p 1 ./internal/store/... -run "TestMigration000020" -v -timeout 600s
```

Expected: both PASS.

- [ ] **Step 5: Run the whole store package so no existing migration test regressed**

```
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/store/... -timeout 900s
```

Expected: PASS. `TestSortIndexesExist`, `TestHotPathIndexesExist` (if present) and both `migrate_down_test.go` tests must stay green **and unedited**.

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/000020_list_endpoint_indexes.up.sql internal/store/migrations/000020_list_endpoint_indexes.down.sql internal/store/list_endpoint_indexes_integration_test.go
git commit -m "feat(store): add list-endpoint indexes for invites and api_tokens"
```

---

## Task 2: Invites page queries (created_at, both directions) and CountInvites

Two list queries plus the count. Both `created_at` arms land together so the tree never carries a reachable `default: panic`. The `expires_at` arms are Task 5.

**Files:**
- Modify (append): `internal/store/query/invites.sql:12` (after the existing `MarkInviteUsed`)
- Regenerate: `internal/store/invites.sql.go`

- [ ] **Step 1: Append the three queries**

Append to `internal/store/query/invites.sql`:

```sql

-- name: ListInvitesPage :many
-- One page of the admin invite list, newest first. Every state is included -
-- active, expired and redeemed - because those are exactly what the Admin
-- Invites tab exists to show. There is no WHERE filter and no filter parameter;
-- if one is ever added, the sort+filter 400 rule at internal/api/jobs.go:417-422
-- becomes live for this endpoint.
--
-- The projection is EXPLICIT and deliberately omits i.token_hash. That omission
-- is the endpoint's entire security control: with the column absent from the
-- SELECT, the generated row type has no field for it, so returning it is a
-- compile error rather than a review miss. Never change this to SELECT *, and
-- never add token_hash "for debugging".
--
-- The JOIN to users is INNER, which is safe because users are archived, never
-- hard-deleted: no DELETE FROM users statement exists anywhere in
-- internal/store/query/. Precedent for the email projection is submitted_by_email
-- in jobs.sql:16,20.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.created_at, i.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListInvitesPageByCreatedAsc :many
-- The ascending arm of created_at. parseSort strips a leading '-' before
-- checking the allowlist (internal/api/pagination.go:178-181), so every key in
-- InvitesSortSpec.Keys is reachable in BOTH directions and each direction needs
-- its own statement and its own dispatch arm. A missing arm is a
-- client-triggerable panic, not a 400.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.created_at, i.id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.created_at ASC, i.id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountInvites :one
-- The `total` for the invites list. It carries the SAME join as the list
-- statements and the same (empty) filter predicate, so the pagination footer
-- can never state a number the client cannot page to. The join is redundant
-- against today's FK, and it is kept anyway so "total uses the list's own
-- predicate" is literally true rather than true by argument.
SELECT COUNT(*) FROM invites i JOIN users u ON u.id = i.created_by;
```

- [ ] **Step 2: Regenerate and clean up the line-ending churn**

```
sqlc generate
git status --short internal/store/
git diff --ignore-all-space
```

Expected real content change in exactly one file: `internal/store/invites.sql.go`. Revert every other listed file whose `git diff --ignore-all-space <file>` prints nothing, one `git checkout -- <file>` at a time. `internal/store/models.go` must be among the reverted ones - no column was added.

- [ ] **Step 3: Verify the regeneration survived the cleanup**

Open `internal/store/invites.sql.go` and confirm all of:

- `type ListInvitesPageParams struct` with fields `CursorSet bool`, `CursorTs pgtype.Timestamptz`, `CursorID pgtype.UUID`, `PageLimit int32`.
- `type ListInvitesPageRow struct` with exactly `ID pgtype.UUID`, `Email *string`, `CreatedBy pgtype.UUID`, `CreatedAt pgtype.Timestamptz`, `ExpiresAt pgtype.Timestamptz`, `UsedAt pgtype.Timestamptz`, `CreatedByEmail string`. **There must be no `TokenHash` field.**
- `type ListInvitesPageByCreatedAscRow struct` with the same seven fields.
- `func (q *Queries) CountInvites(ctx context.Context) (int64, error)`.
- The doc comment above each function reproduces the comment block and SQL you just wrote. If a doc comment is missing or shows different SQL, the CRLF revert ate the regeneration: re-run `sqlc generate` and redo the cleanup more carefully.

- [ ] **Step 4: Compile**

```
go build ./...
go vet -tags integration ./...
```
Expected: both succeed with no output.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/invites.sql internal/store/invites.sql.go
git commit -m "feat(store): add paged invite list queries and CountInvites"
```

---

## Task 3: GET /v1/invites - handler, route, gating and shape

**Files:**
- Modify (append): `internal/api/invites.go:88`
- Modify: `internal/api/server.go:139`
- Create: `internal/api/invites_list_integration_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/invites_list_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getInvitesPage issues GET /v1/invites with the given bearer token and raw
// query string, returning the status, the decoded envelope, and the RAW body
// string. The raw body is returned because the hash-leak sweep in
// invites_leak_integration_test.go asserts on bytes, not on parsed keys: a
// handler that leaked the hash under a differently spelled key would pass a
// parsed-struct assertion.
func getInvitesPage(t *testing.T, srv *api.Server, token, query string) (int, pageEnvelope[map[string]any], string) {
	t.Helper()
	url := "/v1/invites"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(strings.NewReader(body)).Decode(&resp))
	}
	return rec.Code, resp, body
}

// createInviteViaAPI posts an invite as the given admin and returns the raw
// token and the invite id. Going through the API rather than the store keeps
// the raw token in hand for the leak sweep.
func createInviteViaAPI(t *testing.T, srv *api.Server, adminToken, body string) (rawToken, id string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/invites", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "seed invite: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp["token"].(string), resp["id"].(string)
}

func TestListInvites_Gating(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Inv Admin", "inv-list-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	user := createTestUser(t, q, "Inv User", "inv-list-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)

	// Unauthenticated -> 401.
	req := httptest.NewRequest("GET", "/v1/invites", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no bearer token must be 401")

	// Authenticated non-admin -> 403. Invites carry invitee email addresses, so
	// a non-admin read is a disclosure, not just a policy miss.
	code, _, _ := getInvitesPage(t, srv, userToken, "")
	assert.Equal(t, http.StatusForbidden, code, "a non-admin must not read invites")

	// Admin -> 200.
	code, _, _ = getInvitesPage(t, srv, adminToken, "")
	assert.Equal(t, http.StatusOK, code, "an admin must be able to read invites")
}

func TestListInvites_ItemShapeIsExactly(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Shape Admin", "inv-shape-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	// One email-bound invite and one unbound invite. Neither is redeemed, so
	// used_at must be absent from both.
	createInviteViaAPI(t, srv, adminToken, `{"email":"bound@example.com","expires_in":"24h"}`)
	createInviteViaAPI(t, srv, adminToken, `{"expires_in":"24h"}`)

	code, p, _ := getInvitesPage(t, srv, adminToken, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 2)
	assert.Equal(t, int64(2), p.Total)
	assert.Equal(t, "", p.NextCursor, "two rows under the default limit is the last page")

	var bound, unbound map[string]any
	for _, it := range p.Items {
		if _, ok := it["email"]; ok {
			bound = it
		} else {
			unbound = it
		}
	}
	require.NotNil(t, bound, "the email-bound invite must carry an email key")
	require.NotNil(t, unbound, "the unbound invite must omit the email key entirely")

	// Key-set equality, not a series of per-key assertions: an ADDED key must
	// fail this test. That is what makes it the regression gate on the shape.
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email", "email"},
		keysOf(bound), "email-bound invite key set")
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email"},
		keysOf(unbound), "unbound invite key set")

	assert.Equal(t, "bound@example.com", bound["email"])
	assert.Equal(t, "inv-shape-admin@example.com", bound["created_by_email"],
		"created_by_email must be the creating admin's address, from the JOIN")
	assert.Equal(t, fmtUUID(admin.ID), bound["created_by"])

	// Two-return lookup, not a nil comparison: a nulled key and an absent key
	// are different wire shapes and only one of them is correct here.
	_, hasUsedAt := unbound["used_at"]
	assert.False(t, hasUsedAt, "used_at must be ABSENT on an unredeemed invite, not null")
}

// keysOf returns the key set of a decoded item for ElementsMatch comparison.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

var _ = store.Invite{} // keep the store import honest for later tests in this file
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test -tags integration -p 1 ./internal/api/... -run "TestListInvites_" -v -timeout 600s
```

Expected: **FAIL.** `TestListInvites_Gating` fails at the non-admin assertion with `expected: 403 actual: 405` - the 405 is Go's `ServeMux` reporting that `/v1/invites` exists but only for `POST` (gotcha 4). `TestListInvites_ItemShapeIsExactly` fails on `require.Equal(200, 405)`.

**This RED is weak.** It proves only that no handler is registered. The substitute evidence, which fails against a wrong handler rather than an absent one, is: the 403-vs-200 pair in `TestListInvites_Gating` (a handler registered without `admin(...)` returns 200 to the non-admin and fails), and the two `ElementsMatch` key-set assertions in `TestListInvites_ItemShapeIsExactly` (a handler that adds, omits, or nulls any key fails). Task 5 and Task 11 carry the strong REDs.

- [ ] **Step 3: Write the handler**

Append to `internal/api/invites.go`:

```go
// InvitesSortSpec is the ?sort= allowlist for GET /v1/invites. parseSort strips
// a leading '-' before checking this map (pagination.go:178-181), so EVERY key
// here is reachable in BOTH directions and each direction needs its own
// dispatch arm in handleListInvites. A key without an arm reaches the default:
// panic below, which net/http recovers per connection as a 500 plus a dropped
// connection - remotely triggerable by any authenticated admin. If you add a
// key here, add two arms and two queries in the same change.
var InvitesSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
	},
}

// inviteEntry builds one item of the GET /v1/invites response.
//
// It takes loose columns rather than a row struct because sqlc emits one
// structurally identical row type per sort arm, and a single function shared by
// all of them keeps the response shape defined in exactly one place. (The
// enrollments handler duplicates its body four times; do not copy that.)
//
// token_hash is not a parameter and must never become one. No status field is
// returned either: the client derives ACTIVE/EXPIRING/EXPIRED/REDEEMED from
// expires_at and used_at, exactly as web/src/admin/enrollments/enrollmentStatus.ts:7-26
// already does, because a server-asserted "expired" is stale the moment the row
// is on screen and "expiring" needs an invented threshold.
//
// Optional keys are OMITTED, never nulled: an absent email means the invite is
// not bound to an address, and an absent used_at means it has not been redeemed.
func inviteEntry(
	id pgtype.UUID,
	email *string,
	createdBy pgtype.UUID,
	createdByEmail string,
	createdAt, expiresAt, usedAt pgtype.Timestamptz,
) map[string]any {
	entry := map[string]any{
		"id":               uuidStr(id),
		"created_at":       createdAt.Time,
		"expires_at":       expiresAt.Time,
		"created_by":       uuidStr(createdBy),
		"created_by_email": createdByEmail,
	}
	if email != nil {
		entry["email"] = *email
	}
	if usedAt.Valid {
		entry["used_at"] = usedAt.Time
	}
	return entry
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, InvitesSortSpec)
	if !ok {
		return
	}

	var items []map[string]any
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListInvitesPage(ctx, store.ListInvitesPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list invites")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListInvitesPageRow) map[string]any {
				return inviteEntry(row.ID, row.Email, row.CreatedBy, row.CreatedByEmail,
					row.CreatedAt, row.ExpiresAt, row.UsedAt)
			},
			func(row store.ListInvitesPageRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	case "created_at":
		rows, err := s.q.ListInvitesPageByCreatedAsc(ctx, store.ListInvitesPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list invites")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListInvitesPageByCreatedAscRow) map[string]any {
				return inviteEntry(row.ID, row.Email, row.CreatedBy, row.CreatedByEmail,
					row.CreatedAt, row.ExpiresAt, row.UsedAt)
			},
			func(row store.ListInvitesPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	default:
		panic("handleListInvites: missing dispatch arm for sort key " + pp.Sort)
	}

	total, err := s.q.CountInvites(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count invites")
		return
	}
	writeJSON(w, http.StatusOK, page[map[string]any]{Items: items, NextCursor: next, Total: total})
}
```

- [ ] **Step 4: Register the route**

In `internal/api/server.go`, the invites block at line 138-139 currently reads:

```go
	// Invites (admin-only)
	mux.Handle("POST /v1/invites", auth(admin(http.HandlerFunc(s.handleCreateInvite))))
```

Replace with:

```go
	// Invites (admin-only)
	mux.Handle("POST /v1/invites", auth(admin(http.HandlerFunc(s.handleCreateInvite))))
	mux.Handle("GET /v1/invites", auth(admin(http.HandlerFunc(s.handleListInvites))))
```

- [ ] **Step 5: Run the tests to verify they pass**

```
go build ./...
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListInvites_" -v -timeout 600s
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/invites.go internal/api/server.go internal/api/invites_list_integration_test.go
git commit -m "feat(api): add GET /v1/invites"
```

---

## Task 4: Unit tests for inviteEntry (no Docker)

The pure mapping helper is the one part of this endpoint that is testable without Postgres. These tests run under `go test ./...` and are the fast feedback loop for the response shape.

**Files:**
- Create: `internal/api/invites_response_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/invites_response_test.go` (package `api`, **no build tag** - mirrors `workers_response_test.go:1`):

```go
package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func testUUID(b byte) pgtype.UUID {
	var u pgtype.UUID
	for i := range u.Bytes {
		u.Bytes[i] = b
	}
	u.Valid = true
	return u
}

func TestInviteEntry_FullRowKeySetIsExact(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	expires := created.Add(72 * time.Hour)
	used := created.Add(time.Hour)
	email := "bound@example.com"

	entry := inviteEntry(testUUID(0x11), &email, testUUID(0x22), "admin@example.com",
		ts(created), ts(expires), ts(used))

	// Key-set equality. An added key fails here, which is the point: this is the
	// regression gate on the wire shape, and token_hash must never be able to
	// appear even under a different spelling.
	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email", "email", "used_at"},
		got)

	assert.Equal(t, "11111111-1111-1111-1111-111111111111", entry["id"])
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", entry["created_by"])
	assert.Equal(t, "admin@example.com", entry["created_by_email"])
	assert.Equal(t, "bound@example.com", entry["email"])
	assert.Equal(t, created, entry["created_at"])
	assert.Equal(t, expires, entry["expires_at"])
	assert.Equal(t, used, entry["used_at"])
}

func TestInviteEntry_OmitsUnsetOptionalKeys(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	entry := inviteEntry(testUUID(0x11), nil, testUUID(0x22), "admin@example.com",
		ts(created), ts(created.Add(72*time.Hour)), pgtype.Timestamptz{})

	// Two-return lookups. Comparing to nil would pass against a handler that
	// wrote an explicit null, which is a different wire shape and the wrong one:
	// with `used_at?: string` in TypeScript, an absent key makes the wrong
	// client check (`used_at !== null`) a compile error, and a nulled field
	// would let that same mistake compile.
	_, hasEmail := entry["email"]
	require.False(t, hasEmail, "email must be absent, not null, on an unbound invite")
	_, hasUsedAt := entry["used_at"]
	require.False(t, hasUsedAt, "used_at must be absent, not null, on an unredeemed invite")

	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "created_by", "created_by_email"}, got)
}

func TestInviteEntry_EmptyStringEmailIsStillPresent(t *testing.T) {
	// A non-nil pointer to the empty string is a bound-but-empty address, which
	// CreateInvite cannot produce today (invites.go:65-71 only sets the pointer
	// for a non-empty, parseable address). The case is pinned anyway so that the
	// presence rule is "the pointer is non-nil", not "the string is non-empty":
	// those differ, and only the first one matches the column semantics.
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	empty := ""
	entry := inviteEntry(testUUID(0x11), &empty, testUUID(0x22), "admin@example.com",
		ts(created), ts(created), pgtype.Timestamptz{})
	v, ok := entry["email"]
	require.True(t, ok, "a non-nil email pointer must produce the key")
	assert.Equal(t, "", v)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/api/ -run "TestInviteEntry_" -v -timeout 60s
```
Expected: **FAIL to compile** if `inviteEntry` were missing. Since Task 3 already added it, expect **PASS on first run**. That is acceptable and expected here: these are characterization tests for a helper that already landed with its consumer, and their value is the key-set gate they leave behind, not a RED. **Do not delete and re-add `inviteEntry` to manufacture a RED.** If any assertion fails, the helper is wrong - fix the helper, not the test.

- [ ] **Step 3: Run the whole unit suite**

```
go build ./...
go vet ./...
go test ./... -timeout 120s
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/invites_response_test.go
git commit -m "test(api): pin the GET /v1/invites item key set"
```

---

## Task 5: The expires_at sort arms - the behavioral RED for a missing dispatch arm

This is the first strong RED in the plan. The spec names the `default: panic` as the shape's single most dangerous property; this task makes "every allowlisted key works in both directions" an assertion driven off the `SortSpec` itself, so a future added key is covered without editing the test.

**Files:**
- Modify (append): `internal/store/query/invites.sql`
- Regenerate: `internal/store/invites.sql.go`
- Modify: `internal/api/invites.go` (`InvitesSortSpec`, two new dispatch arms)
- Create: `internal/api/invites_sort_integration_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/invites_sort_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"relay/internal/api"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedInvite inserts an invite directly through the pool so the test controls
// created_at, expires_at and used_at, none of which POST /v1/invites exposes.
func seedInvite(t *testing.T, pool *pgxpool.Pool, createdBy pgtype.UUID, tokenHash string,
	createdAt, expiresAt time.Time, usedAt *time.Time) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO invites (token_hash, created_by, created_at, expires_at, used_at)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		tokenHash, createdBy, createdAt, expiresAt, usedAt,
	).Scan(&id)
	require.NoError(t, err, "seedInvite %s", tokenHash)
	return id
}

// seedInvitesForSort seeds three invites whose created_at and expires_at
// orderings are REVERSED relative to each other, so a handler that dispatched
// -expires_at to the created_at query would produce the wrong order and fail.
// Returns the ids in created_at ASC order.
func seedInvitesForSort(t *testing.T, pool *pgxpool.Pool, createdBy pgtype.UUID) []string {
	t.Helper()
	baseCreated := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	baseExpires := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	ids := make([]string, 0, 3)
	for i, name := range []string{"alpha", "bravo", "charlie"} {
		ids = append(ids, seedInvite(t, pool, createdBy, "hash-inv-sort-"+name,
			baseCreated.Add(time.Duration(i)*time.Hour),
			baseExpires.Add(time.Duration(3-i)*time.Hour), nil))
	}
	return ids
}

// TestListInvites_EverySortKeyWorksInBothDirections is the panic gate. It is
// driven off api.InvitesSortSpec.Keys rather than a literal list, so a key
// added to the spec without a dispatch arm turns this test red automatically -
// which is exactly the failure mode the spec calls out: parseSort strips the
// leading '-' before the allowlist check, so both directions of every key reach
// the switch, and an unimplemented arm is a 500 plus a dropped connection
// triggered by an ordinary query parameter.
func TestListInvites_EverySortKeyWorksInBothDirections(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Sort Admin", "inv-sort-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	seedInvitesForSort(t, pool, admin.ID)

	require.NotEmpty(t, api.InvitesSortSpec.Keys, "the spec must allowlist at least one key")
	for key := range api.InvitesSortSpec.Keys {
		for _, sortStr := range []string{key, "-" + key} {
			t.Run(sortStr, func(t *testing.T) {
				code, p, body := getInvitesPage(t, srv, adminToken, "sort="+sortStr+"&limit=50")
				require.Equal(t, http.StatusOK, code,
					"sort=%s must not 500; a missing dispatch arm panics. body: %s", sortStr, body)
				require.Len(t, p.Items, 3)
				assertItemsSorted(t, p.Items, sortStr)
			})
		}
	}
}

// assertItemsSorted confirms the field implied by sortStr is monotonic across
// the page. RFC3339 timestamps compare correctly as strings when they share a
// zone, which the Go JSON encoder guarantees here.
func assertItemsSorted(t *testing.T, items []map[string]any, sortStr string) {
	t.Helper()
	desc := len(sortStr) > 0 && sortStr[0] == '-'
	key := sortStr
	if desc {
		key = sortStr[1:]
	}
	values := make([]string, len(items))
	for i, it := range items {
		v, ok := it[key].(string)
		require.True(t, ok, "item %d has no string %q: %v", i, key, it)
		values[i] = v
	}
	for i := 1; i < len(values); i++ {
		if desc {
			assert.GreaterOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortStr, i, values[i-1], values[i])
		} else {
			assert.LessOrEqual(t, values[i-1], values[i],
				"sort=%s not monotonic at i=%d (%v vs %v)", sortStr, i, values[i-1], values[i])
		}
	}
}

// The orderings of created_at and expires_at are reversed in the fixture, so
// this pins that each arm dispatched to its OWN query. A 200 with monotonic
// output on the wrong column would still pass assertItemsSorted for that
// column's key, but cannot produce both of these id orders at once.
func TestListInvites_SortArmsDispatchToTheirOwnQuery(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Dispatch Admin", "inv-dispatch-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	ids := seedInvitesForSort(t, pool, admin.ID) // created ASC: [0]=alpha [1]=bravo [2]=charlie

	code, p, _ := getInvitesPage(t, srv, adminToken, "sort=created_at&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 3)
	assert.Equal(t, []any{ids[0], ids[1], ids[2]},
		[]any{p.Items[0]["id"], p.Items[1]["id"], p.Items[2]["id"]},
		"created_at ASC order")

	// expires_at is seeded in the REVERSE order of created_at.
	code, p, _ = getInvitesPage(t, srv, adminToken, "sort=expires_at&limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 3)
	assert.Equal(t, []any{ids[2], ids[1], ids[0]},
		[]any{p.Items[0]["id"], p.Items[1]["id"], p.Items[2]["id"]},
		"expires_at ASC order must be the reverse of created_at ASC in this fixture")
}

func TestListInvites_UnknownSortKeyIs400NamingKeysAndPath(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Bad Sort Admin", "inv-badsort-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	code, _, body := getInvitesPage(t, srv, adminToken, "sort=token_hash")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "unsupported sort key 'token_hash'")
	assert.Contains(t, body, "/v1/invites", "the 400 must name the request path")
	assert.Contains(t, body, "created_at")
	assert.Contains(t, body, "expires_at")
}

func TestListInvites_CursorFromAnotherSortIsRejected(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Cursor Admin", "inv-cursor-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	seedInvitesForSort(t, pool, admin.ID)

	code, p, _ := getInvitesPage(t, srv, adminToken, "sort=-created_at&limit=1")
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, p.NextCursor, "three rows at limit=1 must yield a cursor")

	code, _, body := getInvitesPage(t, srv, adminToken,
		fmt.Sprintf("sort=expires_at&limit=1&cursor=%s", p.NextCursor))
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "cursor sort key does not match requested sort")

	code, _, body = getInvitesPage(t, srv, adminToken, "sort=-created_at&cursor=not-a-cursor")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "invalid cursor")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListInvites_" -v -timeout 900s
```

Expected: **FAIL, behaviorally.**

- `TestListInvites_SortArmsDispatchToTheirOwnQuery` fails on `sort=expires_at` with `expected: 200 actual: 400`, because `expires_at` is not yet in `InvitesSortSpec.Keys` and `parsePage` rejects it.
- `TestListInvites_UnknownSortKeyIs400NamingKeysAndPath` fails on `assert.Contains(body, "expires_at")` - the 400 currently names only `created_at`.
- `TestListInvites_CursorFromAnotherSortIsRejected` fails on the `sort=expires_at` leg, which is a 400 for the wrong reason (unsupported key, not cursor mismatch) - check the message in the output to confirm.
- `TestListInvites_EverySortKeyWorksInBothDirections` **passes** at this point, because it is spec-driven and the spec has only one key. That is not a bug: it is the test that will catch the next added key, and Step 5 is where it starts covering `expires_at`.

Capture this output in the task report.

- [ ] **Step 3: Append the two expires_at queries**

Append to `internal/store/query/invites.sql`:

```sql

-- name: ListInvitesPageByExpiresDesc :many
-- The expires_at descending arm. invites.expires_at is NOT NULL
-- (000002_invites.up.sql:7), so unlike workers.last_seen_at this needs no
-- NULLS FIRST/LAST index pair and no cursor-null handling.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.expires_at, i.id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.expires_at DESC, i.id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListInvitesPageByExpiresAsc :many
-- The expires_at ascending arm. See ListInvitesPageByCreatedAsc for why both
-- directions of every allowlisted key need their own statement.
SELECT i.id, i.email, i.created_by, i.created_at, i.expires_at, i.used_at,
       u.email AS created_by_email
FROM invites i
JOIN users u ON u.id = i.created_by
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (i.expires_at, i.id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY i.expires_at ASC, i.id ASC
LIMIT sqlc.arg(page_limit)::int + 1;
```

- [ ] **Step 4: Regenerate and clean up the line-ending churn**

```
sqlc generate
git status --short internal/store/
git diff --ignore-all-space
```

Only `internal/store/invites.sql.go` may show a real content change; `git checkout -- <file>` every other listed file whose `git diff --ignore-all-space` is empty. Then re-open `internal/store/invites.sql.go` and confirm `ListInvitesPageByExpiresDesc`, `ListInvitesPageByExpiresAsc`, their `...Row` and `...Params` structs, and their doc comments are all present (gotcha 3).

- [ ] **Step 5: Allowlist the key and add the two dispatch arms**

In `internal/api/invites.go`, `InvitesSortSpec.Keys` currently reads:

```go
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
	},
```

Replace with:

```go
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
		"expires_at": SortKeyTimestamp,
	},
```

Then, in `handleListInvites`, insert these two cases immediately **before** the `default:` arm:

```go
	case "-expires_at":
		rows, err := s.q.ListInvitesPageByExpiresDesc(ctx, store.ListInvitesPageByExpiresDescParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list invites")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListInvitesPageByExpiresDescRow) map[string]any {
				return inviteEntry(row.ID, row.Email, row.CreatedBy, row.CreatedByEmail,
					row.CreatedAt, row.ExpiresAt, row.UsedAt)
			},
			func(row store.ListInvitesPageByExpiresDescRow) (anySortVal, pgtype.UUID) {
				return row.ExpiresAt.Time, row.ID
			})

	case "expires_at":
		rows, err := s.q.ListInvitesPageByExpiresAsc(ctx, store.ListInvitesPageByExpiresAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list invites")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListInvitesPageByExpiresAscRow) map[string]any {
				return inviteEntry(row.ID, row.Email, row.CreatedBy, row.CreatedByEmail,
					row.CreatedAt, row.ExpiresAt, row.UsedAt)
			},
			func(row store.ListInvitesPageByExpiresAscRow) (anySortVal, pgtype.UUID) {
				return row.ExpiresAt.Time, row.ID
			})
```

Note the row-key callbacks return `row.ExpiresAt.Time`, not `row.CreatedAt.Time`. The cursor must carry the value of the column actually being ordered on, or the next page skips or repeats rows.

- [ ] **Step 6: Run the tests to verify they pass**

```
go build ./...
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListInvites_" -v -timeout 900s
```

Expected: PASS, and `TestListInvites_EverySortKeyWorksInBothDirections` now runs **four** subtests (`created_at`, `-created_at`, `expires_at`, `-expires_at`). Confirm all four appear in the `-v` output; a run showing only two means the spec edit in Step 5 did not land.

- [ ] **Step 7: Commit**

```bash
git add internal/store/query/invites.sql internal/store/invites.sql.go internal/api/invites.go internal/api/invites_sort_integration_test.go
git commit -m "feat(api): sort GET /v1/invites by expires_at in both directions"
```

---

## Task 6: Invites - state coverage, paging and total

Everything the tab depends on that Tasks 3 and 5 did not cover: all four presentation states present in one unfiltered list, `used_at` presence and absence, the redeemed-and-expired precedence case, a cursor walk to exhaustion, and `total` stability across pages.

**Files:**
- Modify (append): `internal/api/invites_list_integration_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/invites_list_integration_test.go`:

```go
// The invites list applies NO filter: redeemed and expired invites are exactly
// what the tab exists to show, unlike GET /v1/agent-enrollments where a
// consumed row simply vanishes. All four client-side pill states must be
// derivable from the rows this returns.
func TestListInvites_ReturnsEveryStateUnfiltered(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "State Admin", "inv-state-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	now := time.Now()
	past := now.Add(-48 * time.Hour)
	redeemedAt := now.Add(-time.Hour)

	activeID := seedInvite(t, pool, admin.ID, "hash-inv-active",
		now.Add(-time.Hour), now.Add(72*time.Hour), nil)
	expiringID := seedInvite(t, pool, admin.ID, "hash-inv-expiring",
		now.Add(-time.Hour), now.Add(30*time.Minute), nil)
	expiredID := seedInvite(t, pool, admin.ID, "hash-inv-expired",
		past, now.Add(-time.Hour), nil)
	redeemedID := seedInvite(t, pool, admin.ID, "hash-inv-redeemed",
		past, now.Add(72*time.Hour), &redeemedAt)
	// Redeemed AND past its expiry: the client's precedence rule checks used_at
	// FIRST, so this row must still carry used_at and must still be returned.
	redeemedExpiredID := seedInvite(t, pool, admin.ID, "hash-inv-redeemed-expired",
		past, now.Add(-time.Hour), &redeemedAt)

	code, p, _ := getInvitesPage(t, srv, adminToken, "limit=200")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, int64(5), p.Total, "total is the unfiltered row count")
	require.Len(t, p.Items, 5)

	byID := map[string]map[string]any{}
	for _, it := range p.Items {
		byID[it["id"].(string)] = it
	}
	for _, id := range []string{activeID, expiringID, expiredID, redeemedID, redeemedExpiredID} {
		require.Contains(t, byID, id, "every invite in every state must be listed")
	}

	// used_at presence discriminates redeemed from unredeemed at the data level.
	for _, id := range []string{activeID, expiringID, expiredID} {
		_, has := byID[id]["used_at"]
		assert.False(t, has, "unredeemed invite %s must omit used_at", id)
	}
	for _, id := range []string{redeemedID, redeemedExpiredID} {
		_, has := byID[id]["used_at"]
		assert.True(t, has, "redeemed invite %s must carry used_at", id)
	}

	// Every row carries the creator's email from the inner JOIN.
	for id, it := range byID {
		assert.Equal(t, "inv-state-admin@example.com", it["created_by_email"],
			"created_by_email missing or wrong on %s", id)
	}
}

func TestListInvites_CursorWalkVisitsEveryRowExactlyOnce(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Walk Admin", "inv-walk-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	seedInvitesForSort(t, pool, admin.ID)

	code, single, _ := getInvitesPage(t, srv, adminToken, "limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, single.Items, 3)
	require.Equal(t, int64(3), single.Total)

	var walked []string
	cursor := ""
	for i := 0; i < 5; i++ { // safety bound; 3 rows at limit=1 needs 3 pages
		qs := "limit=1"
		if cursor != "" {
			qs += "&cursor=" + cursor
		}
		code, p, _ := getInvitesPage(t, srv, adminToken, qs)
		require.Equal(t, http.StatusOK, code, "page %d", i)
		require.Equal(t, int64(3), p.Total,
			"total must be the full row count on every page, not the page size")
		for _, it := range p.Items {
			walked = append(walked, it["id"].(string))
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	require.Len(t, walked, 3, "the walk must visit every row with no duplicate and no omission")
	for i, it := range single.Items {
		assert.Equal(t, it["id"], walked[i], "row %d differs between single-page and paged walk", i)
	}
	assert.NotEqual(t, walked[0], walked[1])
	assert.NotEqual(t, walked[1], walked[2])
}

func TestListInvites_LimitOutOfRangeIs400(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Limit Admin", "inv-limit-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	for _, bad := range []string{"0", "201", "-1", "abc"} {
		code, _, body := getInvitesPage(t, srv, adminToken, "limit="+bad)
		assert.Equal(t, http.StatusBadRequest, code, "limit=%s must be rejected", bad)
		assert.Contains(t, body, "invalid limit")
	}
}
```

Add `"time"` to the file's import block.

- [ ] **Step 2: Run the tests to verify they fail**

```
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListInvites_ReturnsEveryStateUnfiltered|TestListInvites_CursorWalkVisitsEveryRowExactlyOnce|TestListInvites_LimitOutOfRangeIs400" -v -timeout 900s
```

Expected: **PASS on first run.** Tasks 3 and 5 already implemented every behavior these assert. State this plainly in the task report rather than manufacturing a RED: these are coverage tests over shipped behavior, and their value is the regression gate. **If any of them fails, that is a real defect** - most likely in the cursor walk (a row-key callback returning the wrong column) or in `total` (a count with a different predicate from the list). Fix the implementation, never the assertion.

- [ ] **Step 3: Commit**

```bash
git add internal/api/invites_list_integration_test.go
git commit -m "test(api): cover invite states, cursor walk and total on GET /v1/invites"
```

---

## Task 7: GET /v1/auth/tokens - queries, handler, route, scoping and is_current

Everything except the expiry filter, which is Task 9. Landing the filter separately is what makes a behavioral RED for the NULL-expiry predicate possible.

**Files:**
- Modify (append): `internal/store/query/tokens.sql:29`
- Regenerate: `internal/store/tokens.sql.go`
- Create: `internal/api/tokens.go`
- Modify: `internal/api/server.go:100`
- Create: `internal/api/tokens_list_integration_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/tokens_list_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mintToken creates an api_token for userID with an EXPLICIT expires_at and
// returns the raw hex.
//
// Why not createTestToken (api_test.go:48-61)? That helper mints a
// NULL-expires_at token. Every test in this file except the dedicated
// NULL-expiry one must use an explicitly-expiring token, so that the
// discriminating test in Task 9 is the ONLY test that a naive
// `expires_at > NOW()` predicate turns red. If the caller's own token were
// NULL-expiry, that predicate would empty every list in this file and the
// signal would be lost in the noise.
func mintToken(t *testing.T, q *store.Queries, userID pgtype.UUID, expiresAt pgtype.Timestamptz) string {
	t.Helper()
	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(t.Context(), store.CreateTokenParams{
		UserID:    userID,
		TokenHash: tokenhash.Hash(rawHex),
		ExpiresAt: expiresAt,
	})
	require.NoError(t, err)
	return rawHex
}

func future(d time.Duration) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().Add(d), Valid: true}
}

// getTokensPage issues GET /v1/auth/tokens and returns status, envelope and the
// RAW body string (needed by the leak sweep and by the ?user_id= equality test).
func getTokensPage(t *testing.T, srv *api.Server, token, query string) (int, pageEnvelope[map[string]any], string) {
	t.Helper()
	url := "/v1/auth/tokens"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(strings.NewReader(body)).Decode(&resp))
	}
	return rec.Code, resp, body
}

func TestListTokens_Gating(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Tok User", "tok-gate-user@example.com", false)
	userToken := mintToken(t, q, user.ID, future(30*24*time.Hour))

	req := httptest.NewRequest("GET", "/v1/auth/tokens", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no bearer token must be 401")

	// 200 for a NON-admin. This is the paired positive against the invites 403
	// test: writing both together is what catches a mis-chained admin(...).
	code, _, _ := getTokensPage(t, srv, userToken, "")
	assert.Equal(t, http.StatusOK, code, "this is the self-service block, not the admin block")
}

func TestListTokens_ScopedToCallerAndIgnoresUserIDParam(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Me", "tok-scope-me@example.com", false)
	other := createTestUser(t, q, "Other", "tok-scope-other@example.com", false)

	myToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	mySecond := mintToken(t, q, me.ID, future(30*24*time.Hour))
	_ = mySecond
	mintToken(t, q, other.ID, future(30*24*time.Hour))
	mintToken(t, q, other.ID, future(30*24*time.Hour))

	code, p, body := getTokensPage(t, srv, myToken, "")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, p.Items, 2, "exactly the caller's two rows")
	assert.Equal(t, int64(2), p.Total, "total must be scoped to the caller too")

	// The other user's rows must be absent by id, not merely by count.
	otherRows, err := q.ListActiveTokensForUserPage(t.Context(), store.ListActiveTokensForUserPageParams{
		UserID: other.ID, PageLimit: 50,
	})
	require.NoError(t, err)
	require.Len(t, otherRows, 2, "fixture: the other user really has two rows")
	for _, r := range otherRows {
		assert.NotContains(t, body, fmtUUID(r.ID), "another user's token id must never appear")
	}

	// A user_id query parameter must be ignored outright. The identity is the
	// bearer token; the caller does not get to name the rows they receive.
	code2, _, body2 := getTokensPage(t, srv, myToken, "user_id="+fmtUUID(other.ID))
	require.Equal(t, http.StatusOK, code2)
	assert.Equal(t, body, body2, "?user_id= must change nothing at all")
}

// is_current must be true for EXACTLY ONE row and it must be the presented
// token's row. A test asserting only "some row is current" passes against a
// handler that marks the first row.
func TestListTokens_IsCurrentIdentifiesThePresentedToken(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Cur", "tok-current@example.com", false)

	older := mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond) // distinct created_at so the order is stable
	newer := mintToken(t, q, me.ID, future(30*24*time.Hour))
	_ = newer

	// Authenticate with the OLDER token, which under the default -created_at
	// sort is the LAST row. A handler that flagged items[0] would fail here.
	code, p, _ := getTokensPage(t, srv, older, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 2)

	currentIDs := []string{}
	for _, it := range p.Items {
		v, ok := it["is_current"]
		require.True(t, ok, "is_current must be present on every row, never omitted")
		b, ok := v.(bool)
		require.True(t, ok, "is_current must be a bool, got %T", v)
		if b {
			currentIDs = append(currentIDs, it["id"].(string))
		}
	}
	require.Len(t, currentIDs, 1, "exactly one row is the caller's current session")

	// Resolve the presented token's row id independently, through the same
	// lookup BearerAuth uses, so the assertion is against the real identity
	// rather than against the handler's own answer.
	row, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(older))
	require.NoError(t, err)
	assert.Equal(t, fmtUUID(row.TokenID), currentIDs[0],
		"is_current must mark the row whose id equals AuthUser.TokenID")
	assert.Equal(t, fmtUUID(row.TokenID), p.Items[1]["id"],
		"fixture: the older token must be the last row under -created_at")
}

func TestListTokens_ItemShapeIsExactly(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Shape", "tok-shape@example.com", false)
	tok := mintToken(t, q, me.ID, future(30*24*time.Hour))

	code, p, _ := getTokensPage(t, srv, tok, "")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)

	assert.ElementsMatch(t,
		[]string{"id", "created_at", "expires_at", "is_current"},
		keysOf(p.Items[0]))
	assert.Equal(t, true, p.Items[0]["is_current"])
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go vet -tags integration ./...
```

Expected: **FAIL to compile** - `q.ListActiveTokensForUserPage` and `store.ListActiveTokensForUserPageParams` do not exist yet. That is a compile RED and proves nothing behavioral; it is unavoidable for a store query that a test calls directly.

To see the behavioral RED, comment out the two `otherRows` lines in `TestListTokens_ScopedToCallerAndIgnoresUserIDParam` temporarily and run:

```
go test -tags integration -p 1 ./internal/api/... -run "TestListTokens_" -v -timeout 900s
```

Expected: every test FAILS with `expected: 200 actual: 405` (Go's `ServeMux` reporting that `/v1/auth/tokens` exists only for `DELETE`; gotcha 4). **Restore the commented lines before continuing.**

**This RED is weak** - it proves only absence. The substitute evidence, which fails against a wrong handler rather than an absent one: the 200-for-a-non-admin assertion (a mis-chained `admin(...)` returns 403), the `?user_id=` body-equality assertion (a handler honoring the parameter returns different bytes), the exactly-one-`is_current` assertion combined with authenticating as the *last* row (a handler flagging `items[0]` fails), and the `ElementsMatch` key set. The strong REDs for this endpoint are Tasks 9 and 11.

- [ ] **Step 3: Append the three queries (NO expiry filter yet)**

Append to `internal/store/query/tokens.sql`:

```sql

-- name: ListActiveTokensForUserPage :many
-- One page of the caller's own API tokens, newest first.
--
-- The projection is EXPLICIT and omits token_hash. That is the endpoint's
-- security control: with the column absent from the SELECT, the generated row
-- type has no field for it, so returning it is a compile error rather than a
-- review miss. The handler has no reason to hold a hash at all - the
-- current-session flag is a UUID comparison against the token id BearerAuth
-- already resolved (internal/api/middleware.go:36-42), not a re-hash of the
-- presented credential.
--
-- user_id comes from the request context, never from the query string. There is
-- no user_id parameter on the endpoint and there must never be one.
SELECT id, created_at, expires_at
FROM api_tokens
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListActiveTokensForUserPageByCreatedAsc :many
-- The ascending arm. parseSort strips a leading '-' before the allowlist check
-- (internal/api/pagination.go:178-181), so both directions of created_at are
-- reachable and each needs its own statement and dispatch arm.
--
-- expires_at is deliberately NOT a sort key: the column is nullable, so it
-- would need the NULLS LAST / NULLS FIRST index pair and the cursor-null
-- handling that 000013_paginated_sort_indexes.up.sql:15-16 needed for
-- workers.last_seen_at, for a list whose realistic length is single digits.
SELECT id, created_at, expires_at
FROM api_tokens
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountActiveTokensForUser :one
-- The `total` for the sessions list, over the SAME predicate as the list
-- statements, so the pagination footer cannot state a number the caller cannot
-- page to.
SELECT COUNT(*) FROM api_tokens WHERE user_id = $1;
```

- [ ] **Step 4: Regenerate and clean up the line-ending churn**

```
sqlc generate
git status --short internal/store/
git diff --ignore-all-space
```

Only `internal/store/tokens.sql.go` may show a real content change; `git checkout -- <file>` every other listed file whose `git diff --ignore-all-space` is empty, `models.go` included. Then re-open `internal/store/tokens.sql.go` and confirm `ListActiveTokensForUserPage`, `ListActiveTokensForUserPageByCreatedAsc`, `CountActiveTokensForUser`, their `...Params` and `...Row` structs, and their doc comments are present and match the SQL. `ListActiveTokensForUserPageRow` must have exactly `ID`, `CreatedAt`, `ExpiresAt` - **no `TokenHash`, no `UserID`.**

- [ ] **Step 5: Write the handler**

Create `internal/api/tokens.go`:

```go
package api

import (
	"net/http"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// handleListTokens lives here rather than in auth.go because the house layout
// is one file per resource (invites.go, reservations.go, workers.go) and
// /v1/auth/tokens is a resource. auth.go already carries register, login,
// password change and both logout paths across 350+ lines; it is not otherwise
// refactored by this change.

// TokensSortSpec is the ?sort= allowlist for GET /v1/auth/tokens. parseSort
// strips a leading '-' before checking this map (pagination.go:178-181), so
// created_at is reachable in BOTH directions and both arms exist below. A key
// added here without a dispatch arm reaches the default: panic and 500s on
// ordinary user input.
//
// expires_at is deliberately absent: the column is nullable and would need a
// NULLS-ordered index pair plus cursor-null handling for a single-digit list.
var TokensSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
	},
}

// tokenEntry builds one item of the GET /v1/auth/tokens response.
//
// is_current is a pgtype.UUID comparison against the token id that BearerAuth
// already resolved from the presented credential (middleware.go:25-42).
// NOTHING HERE HASHES ANYTHING: the query does not select token_hash, so the
// handler never holds one, and adding a tokenhash.Hash call to this file would
// be a design regression rather than a detail. TokenID is resolved server-side
// and never read from the wire; both sides of the comparison carry Valid:true,
// so a zero value would fail closed (no row marked current) rather than marking
// an arbitrary row.
//
// is_current is ALWAYS present, never omitted: "this row is not your current
// session" is a positive fact the UI must be able to state.
//
// expires_at is OMITTED when the column is NULL. NULL means the token never
// expires - BearerAuth only rejects on `Valid && Before(now)` (middleware.go:32-35) -
// and the consuming tab renders the absence as "never", not as the "-"
// placeholder it uses for missing optional strings. A non-expiring credential
// is a security fact, not missing data.
func tokenEntry(id pgtype.UUID, createdAt, expiresAt pgtype.Timestamptz, currentTokenID pgtype.UUID) map[string]any {
	entry := map[string]any{
		"id":         uuidStr(id),
		"created_at": createdAt.Time,
		"is_current": id == currentTokenID,
	}
	if expiresAt.Valid {
		entry["expires_at"] = expiresAt.Time
	}
	return entry
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	authUser, ok := UserFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	pp, ok := parsePage(w, r, TokensSortSpec)
	if !ok {
		return
	}

	var items []map[string]any
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListActiveTokensForUserPage(ctx, store.ListActiveTokensForUserPageParams{
			UserID:    authUser.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListActiveTokensForUserPageRow) map[string]any {
				return tokenEntry(row.ID, row.CreatedAt, row.ExpiresAt, authUser.TokenID)
			},
			func(row store.ListActiveTokensForUserPageRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	case "created_at":
		rows, err := s.q.ListActiveTokensForUserPageByCreatedAsc(ctx, store.ListActiveTokensForUserPageByCreatedAscParams{
			UserID:    authUser.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListActiveTokensForUserPageByCreatedAscRow) map[string]any {
				return tokenEntry(row.ID, row.CreatedAt, row.ExpiresAt, authUser.TokenID)
			},
			func(row store.ListActiveTokensForUserPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	default:
		panic("handleListTokens: missing dispatch arm for sort key " + pp.Sort)
	}

	total, err := s.q.CountActiveTokensForUser(ctx, authUser.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count tokens")
		return
	}
	writeJSON(w, http.StatusOK, page[map[string]any]{Items: items, NextCursor: next, Total: total})
}
```

- [ ] **Step 6: Register the route**

In `internal/api/server.go`, the self-service block at lines 96-100 currently ends:

```go
	mux.Handle("DELETE /v1/auth/token", auth(http.HandlerFunc(s.handleLogoutCurrent)))
	mux.Handle("DELETE /v1/auth/tokens", auth(http.HandlerFunc(s.handleLogoutAll)))
```

Replace with:

```go
	mux.Handle("DELETE /v1/auth/token", auth(http.HandlerFunc(s.handleLogoutCurrent)))
	mux.Handle("DELETE /v1/auth/tokens", auth(http.HandlerFunc(s.handleLogoutAll)))
	// auth(...) only, NOT admin(...): this is the self-service block. Rows are
	// scoped to authUser.ID from the context, never to a query parameter.
	mux.Handle("GET /v1/auth/tokens", auth(http.HandlerFunc(s.handleListTokens)))
```

- [ ] **Step 7: Run the tests to verify they pass**

```
go build ./...
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListTokens_" -v -timeout 900s
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/store/query/tokens.sql internal/store/tokens.sql.go internal/api/tokens.go internal/api/server.go internal/api/tokens_list_integration_test.go
git commit -m "feat(api): add GET /v1/auth/tokens"
```

---

## Task 8: Unit tests for tokenEntry (no Docker)

**Files:**
- Create: `internal/api/tokens_response_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/api/tokens_response_test.go` (package `api`, **no build tag**). `ts` and `testUUID` come from `invites_response_test.go` in the same package - do not redefine them.

```go
package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenEntry_KeySetIsExactAndIsCurrentAlwaysPresent(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	expires := created.Add(30 * 24 * time.Hour)
	id := testUUID(0x33)

	entry := tokenEntry(id, ts(created), ts(expires), id)

	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t, []string{"id", "created_at", "expires_at", "is_current"}, got)
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", entry["id"])
	assert.Equal(t, created, entry["created_at"])
	assert.Equal(t, expires, entry["expires_at"])
	assert.Equal(t, true, entry["is_current"])
}

// A NULL expires_at means the token never expires. The key is OMITTED, and
// is_current is still present - "not your current session" is a positive fact
// the UI states, so it is never omitted even when false.
func TestTokenEntry_NullExpiresAtOmitsTheKeyAndKeepsIsCurrent(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	entry := tokenEntry(testUUID(0x33), ts(created), pgtype.Timestamptz{}, testUUID(0x44))

	_, hasExpires := entry["expires_at"]
	require.False(t, hasExpires, "a never-expiring token must omit expires_at, not null it")
	v, hasCurrent := entry["is_current"]
	require.True(t, hasCurrent, "is_current is never omitted")
	assert.Equal(t, false, v)

	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	assert.ElementsMatch(t, []string{"id", "created_at", "is_current"}, got)
}

// is_current is a full UUID comparison, not a prefix or a Valid check. The
// near-miss case (one differing byte) is what discriminates a real comparison
// from a sloppy one.
func TestTokenEntry_IsCurrentTrueOnlyForTheExactID(t *testing.T) {
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	rowID := testUUID(0x55)

	nearMiss := rowID
	nearMiss.Bytes[15] ^= 0x01

	assert.Equal(t, true, tokenEntry(rowID, ts(created), ts(created), rowID)["is_current"])
	assert.Equal(t, false, tokenEntry(rowID, ts(created), ts(created), nearMiss)["is_current"],
		"a UUID differing in one byte is not the current session")

	// A zero-value current token id must fail CLOSED: no row is marked current,
	// rather than an arbitrary row being marked. This state is unreachable
	// through the handler (BearerAuth must succeed first) and is pinned anyway.
	assert.Equal(t, false, tokenEntry(rowID, ts(created), ts(created), pgtype.UUID{})["is_current"])
}
```

- [ ] **Step 2: Run the tests**

```
go test ./internal/api/ -run "TestTokenEntry_" -v -timeout 60s
```
Expected: PASS. As in Task 4, these are characterization tests over a helper that landed with its consumer; do not manufacture a RED. A failure means `tokenEntry` is wrong.

- [ ] **Step 3: Run the whole unit suite and commit**

```
go vet ./...
go test ./... -timeout 120s
```

```bash
git add internal/api/tokens_response_test.go
git commit -m "test(api): pin the GET /v1/auth/tokens item key set and is_current"
```

---

## Task 9: The expiry predicate - the discriminating behavioral RED

**This is the highest-risk task in the plan and the one the spec singles out.** `api_tokens.expires_at` is nullable and NULL means "never expires"; such a token authenticates forever (`middleware.go:32-35`). A list filtered with a bare `expires_at > NOW()` would hide precisely the most powerful credentials in the system, and would pass every other test in this repo.

The three steps below are ordered so the NULL-expiry predicate gets a **behavioral RED produced by the wrong predicate itself**, not a compile error and not a post-hoc mutation argument. **Do not collapse them. Do not commit the middle step.**

**Files:**
- Modify: `internal/store/query/tokens.sql` (three statements, twice)
- Regenerate: `internal/store/tokens.sql.go` (twice)
- Modify (append): `internal/api/tokens_list_integration_test.go`

- [ ] **Step 1: Write both tests and run them - the expired test goes RED, the NULL test is a passing positive control**

Append to `internal/api/tokens_list_integration_test.go`:

```go
// A token whose expires_at is NULL never expires and authenticates forever
// (internal/api/middleware.go:32-35 only rejects on Valid && Before(now)). It
// MUST appear in the list, with expires_at absent from the item.
//
// This is the discriminating test for the `expires_at > NOW()` trap. An
// implementation whose predicate omits the `expires_at IS NULL OR` arm passes
// every other test in this file and fails only this one - which is why every
// other test in this file mints tokens with an explicit expiry via mintToken
// rather than through createTestToken (api_test.go:48-61, which mints
// NULL-expiry tokens).
func TestListTokens_NeverExpiringTokenIsListed(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Null Exp", "tok-nullexp@example.com", false)

	// The caller authenticates with an explicitly-expiring token so this test
	// fails for the NULL row's absence, never for its own 401.
	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	neverExpires := mintToken(t, q, me.ID, pgtype.Timestamptz{})

	neverRow, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(neverExpires))
	require.NoError(t, err)
	require.False(t, neverRow.ExpiresAt.Valid, "fixture: the seeded token must have a NULL expires_at")

	code, p, body := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)

	var found map[string]any
	for _, it := range p.Items {
		if it["id"] == fmtUUID(neverRow.TokenID) {
			found = it
		}
	}
	require.NotNil(t, found,
		"a NULL-expires_at token never expires and MUST be listed; a bare `expires_at > NOW()` predicate hides exactly the most powerful credentials in the system. body: %s", body)

	_, hasExpires := found["expires_at"]
	assert.False(t, hasExpires,
		"a never-expiring token must OMIT expires_at, so the client can render 'never' rather than a date")
	assert.Equal(t, int64(2), p.Total, "total must count the never-expiring row too")
}

// The mirror of the test above. Paired with it deliberately: neither
// "return everything" nor "return nothing" satisfies both.
func TestListTokens_ExpiredTokenIsNotListed(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Exp", "tok-expired@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	expired := mintToken(t, q, me.ID, pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true})

	expiredRow, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(expired))
	require.NoError(t, err)
	require.True(t, expiredRow.ExpiresAt.Valid, "fixture: the seeded token must have an expiry")
	require.True(t, expiredRow.ExpiresAt.Time.Before(time.Now()), "fixture: it must be in the past")

	code, p, body := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)

	assert.NotContains(t, body, fmtUUID(expiredRow.TokenID),
		"an expired token cannot authenticate (middleware.go:32-35) and must not be listed")
	assert.Len(t, p.Items, 1, "only the caller's live token")
	assert.Equal(t, int64(1), p.Total,
		"total must exclude the expired row too, or the footer states a number the caller cannot page to")
}

// After PUT /v1/users/me/password, DeleteOtherTokensForUser (auth.go:325-328)
// has run, so the list must contain exactly one row and it must be the caller's.
// This exercises the new read path against an existing write path end to end.
func TestListTokens_AfterPasswordChangeExactlyOneCurrentRowRemains(t *testing.T) {
	api.SetBcryptCostForTest()
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Pwd", "tok-pwd@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	mintToken(t, q, me.ID, future(30*24*time.Hour))
	mintToken(t, q, me.ID, future(30*24*time.Hour))

	code, before, _ := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, before.Items, 3, "fixture: three live sessions before the password change")

	req := httptest.NewRequest("PUT", "/v1/users/me/password",
		strings.NewReader(`{"current_password":"testpassword1","new_password":"newpassword1"}`))
	req.Header.Set("Authorization", "Bearer "+callerToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, "password change: %s", rec.Body.String())

	code, after, _ := getTokensPage(t, srv, callerToken, "limit=200")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, after.Items, 1, "a password change revokes every OTHER session")
	assert.Equal(t, int64(1), after.Total)
	assert.Equal(t, true, after.Items[0]["is_current"],
		"the surviving row is the caller's own session")
}
```

`api.SetBcryptCostForTest()` is exported from `internal/api/export_test.go` under `//go:build integration`; `createTestUser` hashes with `bcrypt.MinCost`, so the handler must be told to use the same cost or the current-password check fails. Verify the exact exported name before running; if it differs, use whatever `export_test.go` actually exports and do not add a new export.

Run:

```
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListTokens_NeverExpiringTokenIsListed|TestListTokens_ExpiredTokenIsNotListed|TestListTokens_AfterPasswordChangeExactlyOneCurrentRowRemains" -v -timeout 900s
```

Expected:
- `TestListTokens_ExpiredTokenIsNotListed` **FAILS**, behaviorally: the expired row's id is in the body and `len(p.Items)` is 2, not 1. The message names `middleware.go:32-35`.
- `TestListTokens_NeverExpiringTokenIsListed` **PASSES** - a positive control pinned before the filter lands, so the filter cannot silently break it.
- `TestListTokens_AfterPasswordChangeExactlyOneCurrentRowRemains` **PASSES**.

**Capture this output verbatim in the task report.**

- [ ] **Step 2: Apply the NAIVE predicate on purpose, and capture the NULL test going RED**

This step exists solely to produce the discriminating behavioral RED. `AND expires_at > NOW()` is the minimal implementation that makes Step 1's failing test pass, which is exactly why TDD alone would land it - and it is wrong.

In `internal/store/query/tokens.sql`, add `  AND expires_at > NOW()` immediately after the `WHERE user_id = ...` line of **all three** new statements (`ListActiveTokensForUserPage`, `ListActiveTokensForUserPageByCreatedAsc`, `CountActiveTokensForUser` - the count becomes `SELECT COUNT(*) FROM api_tokens WHERE user_id = $1 AND expires_at > NOW();`). Then:

```
sqlc generate
go test -tags integration -p 1 ./internal/api/... -run "TestListTokens_NeverExpiringTokenIsListed|TestListTokens_ExpiredTokenIsNotListed" -v -timeout 900s
```

Expected:
- `TestListTokens_ExpiredTokenIsNotListed` now **PASSES**.
- `TestListTokens_NeverExpiringTokenIsListed` now **FAILS**, with the message beginning `a NULL-expires_at token never expires and MUST be listed; a bare 'expires_at > NOW()' predicate hides exactly the most powerful credentials in the system`, plus the `total` assertion reporting 1 where 2 was expected.

**Capture this output verbatim in the task report and in the PR description.** It is the only behavioral evidence that the `IS NULL` arm is load-bearing, and it cannot be reproduced after Step 3.

**Do not commit. Do not clean up the CRLF churn yet** - Step 3 regenerates over it.

- [ ] **Step 3: Correct the predicate**

In `internal/store/query/tokens.sql`, replace the naive line in all three statements with the correct predicate, and add the comment paragraph. The three `WHERE` clauses become:

```sql
WHERE user_id = sqlc.arg(user_id)
  AND (expires_at IS NULL OR expires_at > NOW())
```

and the count becomes:

```sql
SELECT COUNT(*) FROM api_tokens
WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW());
```

Insert this paragraph into the comment block above `ListActiveTokensForUserPage`, after the `user_id comes from the request context` paragraph:

```
-- The `expires_at IS NULL OR` arm is MANDATORY, not defensive noise. The column
-- is nullable (000001_initial.up.sql:18) and a NULL means "never expires":
-- BearerAuth rejects only on `Valid && Before(now)` (internal/api/middleware.go:32-35),
-- so a NULL-expiry token authenticates forever. A bare `expires_at > NOW()`
-- would hide exactly the most powerful credentials in the system from the one
-- screen a user goes to in order to find them. Keep this predicate identical in
-- all three statements here, including the count, or the pagination footer
-- states a number the caller cannot page to. See
-- TestListTokens_NeverExpiringTokenIsListed.
--
-- Expired rows are excluded because they cannot authenticate, nothing reaps
-- them (there is no janitor for api_tokens the way cmd/relay-server/main.go:253
-- reaps agent_enrollments), and there is no per-row revoke endpoint, so listing
-- them would render rows with no available action.
```

Add a one-line pointer above the other two statements: `-- Same expiry predicate as ListActiveTokensForUserPage; see the note there.`

Then regenerate and clean up:

```
sqlc generate
git status --short internal/store/
git diff --ignore-all-space
```

Only `internal/store/tokens.sql.go` may show a real content change; `git checkout -- <file>` every other listed file whose `git diff --ignore-all-space` is empty. **Then re-open `internal/store/tokens.sql.go` and confirm all three doc comments contain `expires_at IS NULL OR expires_at > NOW()`.** A doc comment still showing the bare `expires_at > NOW()` means the CRLF revert discarded the regeneration and the shipped SQL contradicts its own documentation - regenerate and redo the cleanup.

- [ ] **Step 4: Run both tests to verify they pass**

```
go build ./...
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListTokens_" -v -timeout 900s
```
Expected: every `TestListTokens_` test PASSES, including both members of the pair.

- [ ] **Step 5: Commit (the naive state is never committed)**

```bash
git add internal/store/query/tokens.sql internal/store/tokens.sql.go internal/api/tokens_list_integration_test.go
git commit -m "fix(store): list never-expiring tokens and exclude expired ones"
```

Confirm with `git show --stat HEAD` that no other `internal/store/*.sql.go` file is in the commit.

---

## Task 10: Tokens - sort arms, cursor walk and is_current across pages

**Files:**
- Create: `internal/api/tokens_sort_integration_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/api/tokens_sort_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"net/http"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/tokenhash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The panic gate for GET /v1/auth/tokens, driven off api.TokensSortSpec.Keys so
// a key added without a dispatch arm turns this red automatically.
func TestListTokens_EverySortKeyWorksInBothDirections(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Sort", "tok-sort@example.com", false)
	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond)
	mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond)
	mintToken(t, q, me.ID, future(30*24*time.Hour))

	require.NotEmpty(t, api.TokensSortSpec.Keys)
	for key := range api.TokensSortSpec.Keys {
		for _, sortStr := range []string{key, "-" + key} {
			t.Run(sortStr, func(t *testing.T) {
				code, p, body := getTokensPage(t, srv, callerToken, "sort="+sortStr+"&limit=50")
				require.Equal(t, http.StatusOK, code,
					"sort=%s must not 500; a missing dispatch arm panics. body: %s", sortStr, body)
				require.Len(t, p.Items, 4)
				assertItemsSorted(t, p.Items, sortStr)
			})
		}
	}
}

func TestListTokens_UnknownSortKeyIs400NamingKeysAndPath(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Bad Sort", "tok-badsort@example.com", false)
	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))

	code, _, body := getTokensPage(t, srv, callerToken, "sort=expires_at")
	require.Equal(t, http.StatusBadRequest, code,
		"expires_at is nullable and deliberately not a sort key here")
	assert.Contains(t, body, "unsupported sort key 'expires_at'")
	assert.Contains(t, body, "/v1/auth/tokens")
	assert.Contains(t, body, "created_at")

	code, _, body = getTokensPage(t, srv, callerToken, "cursor=not-a-cursor")
	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "invalid cursor")
}

// is_current must be computed per ROW against the caller's token id, not per
// page against the first row. With limit=1 and the caller holding the OLDEST
// token, the flag must land on the LAST page.
func TestListTokens_IsCurrentSurvivesPagination(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Page Cur", "tok-pagecur@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	time.Sleep(2 * time.Millisecond)
	mintToken(t, q, me.ID, future(30*24*time.Hour))

	callerRow, err := q.GetTokenWithUser(t.Context(), tokenhash.Hash(callerToken))
	require.NoError(t, err)

	var walked []map[string]any
	cursor := ""
	for i := 0; i < 4; i++ { // safety bound
		qs := "limit=1"
		if cursor != "" {
			qs += "&cursor=" + cursor
		}
		code, p, _ := getTokensPage(t, srv, callerToken, qs)
		require.Equal(t, http.StatusOK, code, "page %d", i)
		require.Equal(t, int64(2), p.Total, "total must be the full count on every page")
		walked = append(walked, p.Items...)
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	require.Len(t, walked, 2, "the walk must visit both rows exactly once")
	assert.Equal(t, false, walked[0]["is_current"],
		"page 1 under -created_at is the NEWER token, which is not the caller's")
	assert.Equal(t, true, walked[1]["is_current"],
		"page 2 is the caller's own older token; a per-page flag would put it on page 1")
	assert.Equal(t, fmtUUID(callerRow.TokenID), walked[1]["id"])
}
```

- [ ] **Step 2: Run the tests**

```
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "TestListTokens_EverySortKeyWorksInBothDirections|TestListTokens_UnknownSortKeyIs400NamingKeysAndPath|TestListTokens_IsCurrentSurvivesPagination" -v -timeout 900s
```
Expected: PASS. Task 7 implemented both arms, so these are coverage rather than RED-driven; a failure is a real defect in the cursor callback or in `tokenEntry`'s use of `authUser.TokenID`.

- [ ] **Step 3: Commit**

```bash
git add internal/api/tokens_sort_integration_test.go
git commit -m "test(api): cover sort arms and paged is_current on GET /v1/auth/tokens"
```

---

## Task 11: The security obligation - no token hash can reach a response

Two complementary gates. The structural one asserts the generated row types have no hash field, so a leak is a compile error. The behavioral one sweeps the **serialized JSON bytes** for the actual hash value, which catches a leak under any key name - something an assertion on parsed keys or on a struct cannot do.

**Files:**
- Create: `internal/api/list_endpoint_projection_test.go` (unit, no build tag)
- Create: `internal/api/list_endpoint_leak_integration_test.go`

- [ ] **Step 1: Write the structural gate**

Create `internal/api/list_endpoint_projection_test.go`:

```go
package api

import (
	"reflect"
	"strings"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/assert"
)

// The two list endpoints must never be able to return a stored token hash. The
// control is column enumeration in the .sql files: with token_hash absent from
// every SELECT, the generated row types have no field for it, so returning it
// is a compile error rather than a review miss.
//
// This test turns that structural property into an assertion. Adding
// `i.token_hash` to any of these queries changes the generated struct and turns
// this red at the next `go test ./...`, with no Docker required.
func TestListEndpointRowTypesHaveExactProjections(t *testing.T) {
	cases := []struct {
		name   string
		typ    reflect.Type
		fields []string
	}{
		{"ListInvitesPageRow", reflect.TypeOf(store.ListInvitesPageRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListInvitesPageByCreatedAscRow", reflect.TypeOf(store.ListInvitesPageByCreatedAscRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListInvitesPageByExpiresDescRow", reflect.TypeOf(store.ListInvitesPageByExpiresDescRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListInvitesPageByExpiresAscRow", reflect.TypeOf(store.ListInvitesPageByExpiresAscRow{}),
			[]string{"ID", "Email", "CreatedBy", "CreatedAt", "ExpiresAt", "UsedAt", "CreatedByEmail"}},
		{"ListActiveTokensForUserPageRow", reflect.TypeOf(store.ListActiveTokensForUserPageRow{}),
			[]string{"ID", "CreatedAt", "ExpiresAt"}},
		{"ListActiveTokensForUserPageByCreatedAscRow", reflect.TypeOf(store.ListActiveTokensForUserPageByCreatedAscRow{}),
			[]string{"ID", "CreatedAt", "ExpiresAt"}},
	}

	for _, tc := range cases {
		got := make([]string, 0, tc.typ.NumField())
		for i := 0; i < tc.typ.NumField(); i++ {
			name := tc.typ.Field(i).Name
			got = append(got, name)
			assert.NotContains(t, strings.ToLower(name), "token",
				"%s must not project any token-bearing column", tc.name)
		}
		assert.ElementsMatch(t, tc.fields, got,
			"%s projection changed; if this is intentional, update the response mapper and the item key-set tests in the same change", tc.name)
	}
}
```

- [ ] **Step 2: Run the structural gate**

```
go test ./internal/api/ -run TestListEndpointRowTypesHaveExactProjections -v -timeout 60s
```
Expected: PASS.

Then prove it discriminates, without committing the mutation: temporarily add `i.token_hash,` to the `ListInvitesPage` projection in `internal/store/query/invites.sql`, run `sqlc generate`, re-run the test above, and confirm it **FAILS** on both the `must not project any token-bearing column` assertion and the `ElementsMatch`. Capture that output, then `git checkout -- internal/store/query/invites.sql internal/store/invites.sql.go` and re-run `sqlc generate` plus the CRLF cleanup to restore a clean tree. Confirm `git status --short` is clean for `internal/store/` before continuing.

- [ ] **Step 3: Write the raw-body sweep**

Create `internal/api/list_endpoint_leak_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"relay/internal/tokenhash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertNoSecretInBody sweeps the SERIALIZED response bytes. Asserting on the
// parsed items would be weaker: it passes against a handler that returns the
// hash under a different key name, or nests it, or puts it in the envelope.
func assertNoSecretInBody(t *testing.T, body string, secrets map[string]string) {
	t.Helper()
	require.NotEmpty(t, body, "an empty body would satisfy every assertion below")
	for label, secret := range secrets {
		require.NotEmpty(t, secret, "fixture: %s must be non-empty or this proves nothing", label)
		assert.NotContains(t, body, secret, "%s leaked into the response body", label)
	}
	assert.NotContains(t, strings.ToLower(body), "token",
		"no response key or value may contain the substring 'token'; body: %s", body)
}

func TestListInvites_NeverLeaksTheInviteTokenOrItsHash(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Leak Admin", "inv-leak-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	rawA, _ := createInviteViaAPI(t, srv, adminToken, `{"email":"a@example.com","expires_in":"24h"}`)
	rawB, _ := createInviteViaAPI(t, srv, adminToken, `{"expires_in":"24h"}`)

	secrets := map[string]string{
		"invite A raw token":  rawA,
		"invite A token hash": tokenhash.Hash(rawA),
		"invite B raw token":  rawB,
		"invite B token hash": tokenhash.Hash(rawB),
		"caller's own token":  adminToken,
		"caller's own hash":   tokenhash.Hash(adminToken),
	}

	// First page.
	code, p, body := getInvitesPage(t, srv, adminToken, "limit=1")
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)
	require.NotEmpty(t, p.NextCursor, "two rows at limit=1 must yield a cursor")

	// A later page, because a leak could plausibly live in one dispatch arm.
	code, _, body = getInvitesPage(t, srv, adminToken, "limit=1&cursor="+p.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)

	// Every sort arm.
	for _, sortStr := range []string{"created_at", "-created_at", "expires_at", "-expires_at"} {
		code, _, body = getInvitesPage(t, srv, adminToken, "sort="+sortStr+"&limit=200")
		require.Equal(t, http.StatusOK, code, "sort=%s", sortStr)
		assertNoSecretInBody(t, body, secrets)
	}
}

func TestListTokens_NeverLeaksTheBearerTokenOrItsHash(t *testing.T) {
	srv, q := newTestServer(t)
	me := createTestUser(t, q, "Leak", "tok-leak@example.com", false)

	callerToken := mintToken(t, q, me.ID, future(30*24*time.Hour))
	other := mintToken(t, q, me.ID, future(30*24*time.Hour))
	never := mintToken(t, q, me.ID, pgtype.Timestamptz{})

	secrets := map[string]string{
		"caller's raw token":   callerToken,
		"caller's token hash":  tokenhash.Hash(callerToken),
		"second raw token":     other,
		"second token hash":    tokenhash.Hash(other),
		"never-expiring token": never,
		"never-expiring hash":  tokenhash.Hash(never),
	}

	code, p, body := getTokensPage(t, srv, callerToken, "limit=1")
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)
	require.NotEmpty(t, p.NextCursor)

	code, _, body = getTokensPage(t, srv, callerToken, "limit=1&cursor="+p.NextCursor)
	require.Equal(t, http.StatusOK, code)
	assertNoSecretInBody(t, body, secrets)

	for _, sortStr := range []string{"created_at", "-created_at"} {
		code, _, body = getTokensPage(t, srv, callerToken, "sort="+sortStr+"&limit=200")
		require.Equal(t, http.StatusOK, code, "sort=%s", sortStr)
		assertNoSecretInBody(t, body, secrets)
	}
}
```

Add `"github.com/jackc/pgx/v5/pgtype"` and `"time"` to the import block.

- [ ] **Step 4: Run the sweep**

```
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/api/... -run "NeverLeaks" -v -timeout 900s
```
Expected: PASS.

Note on the `"token"` substring assertion: it also forbids a future key named `token_prefix`, which is deliberate. Only the SHA-256 is stored (`invites.go:56`); a prefix would be a newly persisted fragment of a secret for a cosmetic column, and `EnrollmentsTable.tsx:7-9` already omits the equivalent header.

- [ ] **Step 5: Commit**

```bash
git add internal/api/list_endpoint_projection_test.go internal/api/list_endpoint_leak_integration_test.go
git commit -m "test(api): gate both list endpoints against token-hash leakage"
```

---

## Task 12: Document both endpoints in README.md

The README is the project's REST reference and consumers implement against it. A missing or wrong contract there is a defect in its own right, not a formatting nicety. **This task is an addition to the spec's file table; see "Deviations from the spec".**

**Files:**
- Modify: `README.md:1095` (sort allowlist table), `:1153` (Session table), `:1241` (Invites table and section)

- [ ] **Step 1: Add both endpoints to the sort allowlist table**

At `README.md:1095`, the table currently ends:

```
| `GET /v1/agent-enrollments` | `-created_at` | `created_at`, `expires_at` |
```

Replace with:

```
| `GET /v1/agent-enrollments` | `-created_at` | `created_at`, `expires_at` |
| `GET /v1/invites` | `-created_at` | `created_at`, `expires_at` |
| `GET /v1/auth/tokens` | `-created_at` | `created_at` |
```

- [ ] **Step 2: Add the sessions list to the Session table**

At `README.md:1149-1153`, replace the table with:

```
| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `\v1\users\me\password` | Change own password (body: `current_password`, `new_password`) |
| `GET` | `/v1/auth/tokens` | List the calling user's own live sessions. Paginated. |
| `DELETE` | `/v1/auth/token` | Revoke the bearer token used on this request |
| `DELETE` | `/v1/auth/tokens` | Revoke every active bearer token for the calling user |
```

Then insert this block immediately after the table:

```markdown
**GET `/v1/auth/tokens`** returns the caller's own tokens only. There is no `user_id`
parameter; the identity is the bearer token, and a `?user_id=` in the query string is
ignored. Items:

```json
{ "id": "<uuid>", "created_at": "2026-08-01T10:00:00Z", "expires_at": "2026-08-31T10:00:00Z", "is_current": true }
```

- `expires_at` is **omitted when the token never expires** (a NULL column). Render the
  absence as `never`, not as a missing value.
- `is_current` is always present and is true for exactly one row: the token presented on
  this request.
- Only tokens that can currently authenticate are listed. Expired tokens are excluded and
  are not counted in `total`.
- There is no per-session revoke endpoint. `DELETE /v1/auth/tokens` revokes every session
  including the caller's; `PUT /v1/users/me/password` revokes every session except the
  caller's, after which this list contains exactly one row.
- No `last_used_at`, IP, user agent or device is available: no such column exists.
```

Note the leading table row `\v1\users\me\password` reproduces an existing typo in the README verbatim. **Do not fix it in this change** - an unrelated correction in a feature diff is scope creep; file it as a backlog proposal in Phase 6 instead.

- [ ] **Step 3: Add the invites list to the Invites section**

At `README.md:1239-1241`, replace the table with:

```
| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/invites` | Create a one-time invite token |
| `GET` | `/v1/invites` | List every invite in every state (active, expired, redeemed). Paginated. |
```

Then append this block at the end of the Invites section, after the existing `POST` response example at `:1252-1255`:

```markdown
**GET `/v1/invites`** returns every invite with no status filter, because redeemed and
expired invites are what the admin view exists to show. Items:

```json
{
  "id": "<uuid>",
  "created_at": "2026-08-01T10:00:00Z",
  "expires_at": "2026-08-04T10:00:00Z",
  "created_by": "<uuid>",
  "created_by_email": "admin@example.com",
  "email": "invitee@example.com",
  "used_at": "2026-08-02T09:00:00Z"
}
```

- `email` is **omitted** when the invite is not bound to an address.
- `used_at` is **omitted** when the invite has not been redeemed. Its presence is the
  complete and terminal test for "redeemed".
- No `status` field is returned. Derive the pill client-side: redeemed (`used_at`
  present, checked first), expired (`expires_at <= now`), expiring (`expires_at - now`
  under one hour), otherwise active. A server-asserted status would be stale the moment
  the row is on screen.
- No token, token hash, or token prefix is returned. The raw token exists exactly once,
  in the `POST` response; only its SHA-256 is stored, and the list query never selects it.
```

- [ ] **Step 4: Verify nothing else changed and commit**

```
git diff --stat README.md
```
Expected: `README.md` only, with additions clustered at the three locations above.

```bash
git add README.md
git commit -m "docs: document GET /v1/invites and GET /v1/auth/tokens"
```

---

## Task 13: Full verification gate

**Files:** none modified.

- [ ] **Step 1: Confirm the working tree contains exactly the intended file set**

```
git status --short
git diff --stat origin/main...HEAD
```

Expected changed/added files, and nothing else:

```
README.md
internal/api/invites.go
internal/api/invites_list_integration_test.go
internal/api/invites_response_test.go
internal/api/invites_sort_integration_test.go
internal/api/list_endpoint_leak_integration_test.go
internal/api/list_endpoint_projection_test.go
internal/api/server.go
internal/api/tokens.go
internal/api/tokens_list_integration_test.go
internal/api/tokens_response_test.go
internal/api/tokens_sort_integration_test.go
internal/store/invites.sql.go
internal/store/list_endpoint_indexes_integration_test.go
internal/store/migrations/000020_list_endpoint_indexes.down.sql
internal/store/migrations/000020_list_endpoint_indexes.up.sql
internal/store/query/invites.sql
internal/store/query/tokens.sql
internal/store/tokens.sql.go
```

**Any file under `web/` in this list is a defect.** Any other `internal/store/*.sql.go` in this list is line-ending churn that escaped a CRLF cleanup: check it with `git diff --ignore-all-space <file>` and `git checkout -- <file>` if empty. `internal/store/models.go` must **not** appear.

- [ ] **Step 2: Build and vet, both tag sets**

```
go build ./...
go vet ./...
go vet -tags integration ./...
```
Expected: all three succeed with no output.

- [ ] **Step 3: Unit suite (no Docker) - a no-regression gate, NOT evidence**

```
go test ./... -timeout 120s
```
Expected: PASS. **State plainly in the task report that this run exercises none of the two endpoints' behavior**: every behavioral test in this plan is `//go:build integration`. The only new tests it does run are `invites_response_test.go`, `tokens_response_test.go` and `list_endpoint_projection_test.go`.

- [ ] **Step 4: Integration suite - this is the actual gate**

Docker Desktop must be running.

```
go test -tags integration -p 1 ./internal/store/... -timeout 900s
go test -tags integration -p 1 ./internal/api/... -timeout 1800s
```
Expected: PASS. Confirm in the `-v` output (add `-v` if needed) that these all ran and passed:

- `TestMigration000020_ListEndpointIndexesExist`, `TestMigration000020_DownDropsListEndpointIndexes`
- `TestListInvites_Gating`, `TestListInvites_ItemShapeIsExactly`, `TestListInvites_ReturnsEveryStateUnfiltered`, `TestListInvites_CursorWalkVisitsEveryRowExactlyOnce`, `TestListInvites_LimitOutOfRangeIs400`
- `TestListInvites_EverySortKeyWorksInBothDirections` (**four** subtests), `TestListInvites_SortArmsDispatchToTheirOwnQuery`, `TestListInvites_UnknownSortKeyIs400NamingKeysAndPath`, `TestListInvites_CursorFromAnotherSortIsRejected`
- `TestListTokens_Gating`, `TestListTokens_ScopedToCallerAndIgnoresUserIDParam`, `TestListTokens_IsCurrentIdentifiesThePresentedToken`, `TestListTokens_ItemShapeIsExactly`
- `TestListTokens_NeverExpiringTokenIsListed`, `TestListTokens_ExpiredTokenIsNotListed`, `TestListTokens_AfterPasswordChangeExactlyOneCurrentRowRemains`
- `TestListTokens_EverySortKeyWorksInBothDirections` (**two** subtests), `TestListTokens_UnknownSortKeyIs400NamingKeysAndPath`, `TestListTokens_IsCurrentSurvivesPagination`
- `TestListInvites_NeverLeaksTheInviteTokenOrItsHash`, `TestListTokens_NeverLeaksTheBearerTokenOrItsHash`
- Unchanged and green: `TestListAgentEnrollments_AdminOnly`, `TestListAgentEnrollments_Sort_*`, `TestCreateInvite_*`, every test in `logout_integration_test.go` and `password_reset_integration_test.go`, `TestSortIndexesExist`, both `migrate_down_test.go` tests.

If any pre-existing test needed an edit to go green, STOP and report it as a finding (gotcha 7).

- [ ] **Step 5: Assemble the evidence for the PR description**

The PR body must contain, verbatim:

1. Task 5 Step 2 RED output (the missing `expires_at` sort arm).
2. Task 9 Step 1 RED output (the expired token being listed).
3. **Task 9 Step 2 RED output (the never-expiring token being hidden by the naive predicate).** This is the load-bearing piece of evidence in the whole change.
4. Task 11 Step 2 mutation output (the projection gate failing when `token_hash` is added).
5. A one-line note that `go test ./...` on Windows exercises none of the endpoint behavior and that `go test -tags integration -p 1 ./internal/api/...` is the gate.

---

## Verification gate (summary)

`make` is not on PATH; these are the raw commands.

```
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./... -timeout 120s
go test -tags integration -p 1 ./internal/store/... -timeout 900s
go test -tags integration -p 1 ./internal/api/... -timeout 1800s
```

Targeted runs used during the tasks:

```
go test -tags integration -p 1 ./internal/api/... -run "TestListInvites_" -v -timeout 900s
go test -tags integration -p 1 ./internal/api/... -run "TestListTokens_" -v -timeout 900s
go test -tags integration -p 1 ./internal/store/... -run "TestMigration000020" -v -timeout 600s
```

No `web/` file changes in this plan, so **the `git checkout -- web/dist/` revert does not apply.** If a `web/` path ever shows up dirty, stop and investigate rather than reverting it away.

---

## Deviations from the spec, and why

Autonomous gate mode: each decided here with a one-line rationale rather than asked.

1. **README.md is added to the file set.** The spec's Architecture table omits it and acceptance criterion 13 says no file outside that table may change. The README is the project's documented REST reference (`README.md:1088-1097,1147-1153,1235-1255`) and consumers implement against prose no test covers; shipping two endpoints undocumented would be a defect at birth. Task 12 covers it; criterion 13 is read as "no *source* file outside the table".
2. **Query names are `ListActiveTokensForUserPage*`, not the spec's `ListTokensForUserPage*`.** The list is expiry-filtered, and the sibling it copies is `ListActiveAgentEnrollmentsPage` with `CountActiveAgentEnrollments`; matching that naming keeps "Active" meaning the same thing in both families.
3. **`CountInvites` carries the same `JOIN users` as the list statements.** The spec writes `SELECT COUNT(*) FROM invites`. The FK plus the absence of any `DELETE FROM users` makes the two equal, but carrying the join makes "total uses the list's own predicate" literally true instead of true by argument, at negligible cost.
4. **One `inviteEntry` helper shared by four row types, instead of four duplicated `...RowToMap` functions.** The enrollments handler duplicates its mapper body four times (`agent_enrollments.go:83-149`); replicating that would define the response shape in four places and let them drift. DRY, and it gives one unit-testable function.
5. **The `expires_at` sort key lands in a separate task (Task 5) from the `created_at` arms (Tasks 2-3).** The spec presents all four arms together. Splitting them is what produces the behavioral RED for a missing dispatch arm; landing them together would leave that risk evidenced only by argument.
6. **Task 9 writes the naive `expires_at > NOW()` predicate on purpose, runs it, and captures the RED before correcting it.** The spec calls for "a dedicated test"; a test written after the correct predicate lands has never been RED and proves nothing. The naive state is never committed.
7. **The invites list gets a store-layer index test (Task 1) rather than only a handler test.** `sort_indexes_integration_test.go:16-46` is the house precedent, and it turns an easily-forgotten migration into a red test rather than a review question.
8. **Unit tests for the mapping helpers are expected to pass on first run.** The spec lists them under Testing without an evidence regime. They are characterization tests over helpers that ship with their consumer; the plan says so explicitly rather than staging a fake RED.
9. **A new `mintToken` helper is added to the tokens integration test file instead of reusing `createTestToken`.** `createTestToken` mints NULL-expiry tokens (`api_test.go:57`); using it for the caller would make the naive predicate redden every test in the file and destroy the NULL test's discriminating power. `createTestToken` is left untouched and is still used by the invites tests, where expiry is irrelevant.

---

## Phase 6 reminders (do not act on these during implementation)

- `docs/backlog/feature-2026-06-26-web-enabler-backend-endpoints.md` is **amended, not closed** - it still owns `POST /v1/jobs/{id}/retry`. Do not run `/backlog close` on it.
- Backlog items to **propose**, not auto-file: the retry endpoint's own item; `last_used_at` tracking on `api_tokens` (with the hot-path write cost and a throttle stated); per-session revoke `DELETE /v1/auth/token/{id}` (now cheap - this endpoint supplies the id, and the store needs only a user-scoped `DeleteTokenForUser(id, user_id)`, never a bare `WHERE id = $1`); reaping expired invites and tokens by extending the existing hourly janitor at `cmd/relay-server/main.go:253`; and the `\v1\users\me\password` typo at `README.md:1151`.
- The two consuming frontend items (`feature-2026-08-08-admin-invites-tab.md` and the Profile Sessions half) can drop their "backend-blocked" caveat once this lands.
