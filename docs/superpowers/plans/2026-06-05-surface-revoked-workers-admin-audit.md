# Surface Revoked Workers for Admin Audit - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only, admin-only surface (REST + CLI + web) that lists revoked workers and when they were revoked, so admins can audit decommissioned workers.

**Architecture:** A new `revoked_at` timestamp column is stamped on revoke and cleared on re-enrollment. A dedicated `GET /v1/workers/revoked` endpoint (admin-only) lists `status = 'revoked'` workers ordered `revoked_at DESC NULLS LAST, id DESC`, reusing the existing cursor-pagination machinery. The CLI exposes it via `relay workers list --revoked`; the web exposes it as a "Decommissioned" tab on the Workers page. Stats and the operational list are untouched (revoked stays excluded there).

**Tech Stack:** Go, sqlc (Postgres/pgx), golang-migrate, stdlib `net/http`, testify + testcontainers integration tests, React + Vite + @tanstack/react-query + vitest + msw.

**Spec:** [docs/superpowers/specs/2026-06-05-surface-revoked-workers-admin-audit-design.md](../specs/2026-06-05-surface-revoked-workers-admin-audit-design.md)

**Conventions (read before starting):**
- Never edit `internal/store/*.sql.go` or `internal/store/models.go` by hand. Edit `internal/store/query/workers.sql` + migrations, then run `make generate`.
- `make generate` on Windows may rewrite line endings (CRLF) on unrelated `internal/store/*.sql.go` files. Stage only the files you intended to change; `git checkout -- internal/store/<other>.sql.go` to discard line-ending-only noise.
- No em dashes or en dashes anywhere (use `-`).
- Integration tests use `//go:build integration` and require Docker. Run with `-p 1`.

---

## File Structure

- `internal/store/migrations/000014_workers_revoked_at.up.sql` / `.down.sql` - new column.
- `internal/store/query/workers.sql` - edit `ClearWorkerAgentToken`, `SetWorkerAgentToken`; add `ListRevokedWorkersPage`, `CountRevokedWorkers`.
- `internal/store/workers_revoked_test.go` - new; store-layer tests for stamp/clear/list.
- `internal/api/workers.go` - `revoked_at` on `workerResponse`/`toWorkerResponse`; `RevokedWorkersSortSpec`; `workersRowKeyByRevoked`; `handleListRevokedWorkers`.
- `internal/api/server.go` - register the route.
- `internal/api/workers_revoked_list_integration_test.go` - new; endpoint behavior + admin-only.
- `internal/cli/workers.go` - `--revoked` flag, `REVOKED AT` column, `RevokedAt` field.
- `internal/cli/workers_revoked_list_test.go` - new; CLI flag routing/rendering.
- `web/src/workers/api.ts` - `'revoked'` status, `revoked_at`, `listRevokedWorkers()`.
- `web/src/workers/api.test.ts` - add a test for `listRevokedWorkers`.
- `web/src/workers/useRevokedWorkers.ts` - new hook.
- `web/src/workers/RevokedWorkersTable.tsx` - new read-only table.
- `web/src/workers/RevokedWorkersTable.test.tsx` - new render test.
- `web/src/workers/WorkersPage.tsx` - Active / Decommissioned tab.
- `README.md` - document endpoint + CLI flag.

---

## Task 1: Schema + store query layer

**Files:**
- Create: `internal/store/migrations/000014_workers_revoked_at.up.sql`
- Create: `internal/store/migrations/000014_workers_revoked_at.down.sql`
- Modify: `internal/store/query/workers.sql`
- Create: `internal/store/workers_revoked_test.go`
- Regenerated (do not hand-edit): `internal/store/workers.sql.go`, `internal/store/models.go`

- [ ] **Step 1: Create the migration files**

Create `internal/store/migrations/000014_workers_revoked_at.up.sql`:

```sql
ALTER TABLE workers ADD COLUMN revoked_at TIMESTAMPTZ NULL;
```

Create `internal/store/migrations/000014_workers_revoked_at.down.sql`:

```sql
ALTER TABLE workers DROP COLUMN revoked_at;
```

- [ ] **Step 2: Edit the two existing queries and add the two new queries**

In `internal/store/query/workers.sql`, replace the `ClearWorkerAgentToken` block:

```sql
-- name: ClearWorkerAgentToken :execrows
UPDATE workers
SET agent_token_hash = NULL, status = 'revoked', revoked_at = NOW()
WHERE id = $1;
```

Replace the `SetWorkerAgentToken` block:

```sql
-- name: SetWorkerAgentToken :exec
-- Sets the long-lived agent token on (re)enrollment. Clears revoked_at because
-- regaining a valid token means the worker is no longer revoked; this is the
-- one place a revoked worker is revived (revocation nulls the token, so the
-- reconnect-by-token path can no longer find it).
UPDATE workers SET agent_token_hash = $2, revoked_at = NULL WHERE id = $1;
```

Append two new queries at the end of the file:

```sql
-- name: CountRevokedWorkers :one
SELECT COUNT(*) FROM workers WHERE status = 'revoked';

-- name: ListRevokedWorkersPage :many
-- Revoked workers for the admin audit endpoint, newest revocation first.
-- revoked_at is nullable (rows revoked before the column existed), so the
-- cursor predicate mirrors ListWorkersPageByLastSeenDesc's NULLS LAST handling.
SELECT * FROM workers
WHERE status = 'revoked'
  AND (
       NOT @cursor_set::bool
    OR (
       CASE WHEN @cursor_is_null::bool THEN
            revoked_at IS NULL AND id < @cursor_id::uuid
       ELSE
            (revoked_at IS NOT NULL AND
             (revoked_at, id) < (@cursor_ts::timestamptz, @cursor_id::uuid))
         OR revoked_at IS NULL
       END
   ))
ORDER BY revoked_at DESC NULLS LAST, id DESC
LIMIT @page_limit + 1;
```

- [ ] **Step 3: Regenerate the store layer**

Run: `make generate`
Expected: `internal/store/models.go` gains `RevokedAt pgtype.Timestamptz` on the `Worker` struct; `internal/store/workers.sql.go` gains `CountRevokedWorkers`, `ListRevokedWorkersPage`, and the updated `ClearWorkerAgentToken`/`SetWorkerAgentToken`. Stage only `internal/store/models.go` and `internal/store/workers.sql.go`; discard line-ending-only changes on other `internal/store/*.sql.go` files with `git checkout -- <file>`.

- [ ] **Step 4: Write the failing store tests**

Create `internal/store/workers_revoked_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/require"
)

func TestClearWorkerAgentToken_StampsRevokedAt(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-1"),
	}))

	_, err := q.ClearWorkerAgentToken(ctx, w.ID)
	require.NoError(t, err)

	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", reloaded.Status)
	require.True(t, reloaded.RevokedAt.Valid, "revoked_at must be stamped on revoke")
}

func TestSetWorkerAgentToken_ClearsRevokedAt(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	w := newTestWorker(t, q)

	// Revoke, then re-enroll by setting a fresh token.
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-1"),
	}))
	_, err := q.ClearWorkerAgentToken(ctx, w.ID)
	require.NoError(t, err)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: w.ID, AgentTokenHash: ptrStr("hash-2"),
	}))

	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.False(t, reloaded.RevokedAt.Valid, "revoked_at must be cleared on re-enroll")
}

func TestListRevokedWorkersPage_ReturnsOnlyRevoked(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)

	live := newTestWorker(t, q)
	gone := newTestWorker(t, q)
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: gone.ID, AgentTokenHash: ptrStr("h"),
	}))
	_, err := q.ClearWorkerAgentToken(ctx, gone.ID)
	require.NoError(t, err)

	rows, err := q.ListRevokedWorkersPage(ctx, store.ListRevokedWorkersPageParams{
		CursorSet: false,
		PageLimit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, gone.ID, rows[0].ID)
	require.NotEqual(t, live.ID, rows[0].ID)
}
```

Note: `newTestQueries`, `newTestWorker`, `ptrStr` already exist in `internal/store/testhelper_test.go`. `newTestWorker` uses a hostname derived from `t.Name()`, so each test gets distinct workers.

- [ ] **Step 5: Run the store tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/store/... -run "TestClearWorkerAgentToken_StampsRevokedAt|TestSetWorkerAgentToken_ClearsRevokedAt|TestListRevokedWorkersPage_ReturnsOnlyRevoked" -v -timeout 180s`
Expected: PASS (3 tests). Requires Docker.

- [ ] **Step 6: Verify existing store tests still pass**

Run: `go test -tags integration -p 1 ./internal/store/... -timeout 300s`
Expected: PASS. `workers_token_test.go` still passes (its assertions on status/lookup are unaffected by the new column).

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/000014_workers_revoked_at.up.sql internal/store/migrations/000014_workers_revoked_at.down.sql internal/store/query/workers.sql internal/store/workers.sql.go internal/store/models.go internal/store/workers_revoked_test.go
git commit -m "feat(store): add revoked_at column and revoked-worker queries"
```

---

## Task 2: REST API endpoint

**Files:**
- Modify: `internal/api/workers.go`
- Modify: `internal/api/server.go:130` (route block)
- Create: `internal/api/workers_revoked_list_integration_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/api/workers_revoked_list_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedRevokedWorker inserts a worker with status 'revoked' and a revoked_at.
func seedRevokedWorker(t *testing.T, pool *pgxpool.Pool, name string, revokedAt time.Time) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, status, revoked_at)
		 VALUES ($1, $2, 4, 16, 0, '', 'linux', 'revoked', $3)`,
		name, name+"-host", revokedAt)
	require.NoError(t, err)
}

func getRevokedWorkersPage(t *testing.T, srv interface{ Handler() http.Handler }, token, query string) (int, pageEnvelope[map[string]any]) {
	t.Helper()
	url := "/v1/workers/revoked"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp pageEnvelope[map[string]any]
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	}
	return rec.Code, resp
}

func TestListRevokedWorkers_ReturnsOnlyRevokedWithTimestamp(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "revoked-admin@test.com", true)
	token := createTestToken(t, q, admin.ID)

	seedWorker(t, pool, "live-1", "online", nil)
	seedRevokedWorker(t, pool, "gone-1", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	code, p := getRevokedWorkersPage(t, srv, token, "limit=50")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, p.Items, 1)
	require.Equal(t, "gone-1", p.Items[0]["name"])
	require.NotEmpty(t, p.Items[0]["revoked_at"], "revoked_at must be present")
	require.EqualValues(t, 1, p.Total)
}

func TestListRevokedWorkers_AdminOnly(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Plain", "revoked-plain@test.com", false)
	token := createTestToken(t, q, user.ID)

	code, _ := getRevokedWorkersPage(t, srv, token, "limit=50")
	require.Equal(t, http.StatusForbidden, code)
}
```

Note: `pageEnvelope`, `seedWorker`, `newTestServerWithPool`, `createTestUser`, `createTestToken` already exist in the `api_test` package. `srv.Handler()` is used elsewhere (see `getWorkersPage`).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListRevokedWorkers_" -v -timeout 180s`
Expected: FAIL - route not registered (404, so `TestListRevokedWorkers_AdminOnly` sees 404 not 403, and the first test sees 404).

- [ ] **Step 3: Add the `revoked_at` field to the response**

In `internal/api/workers.go`, in the `workerResponse` struct, add a field after `DisabledAt`:

```go
	DisabledAt   *time.Time      `json:"disabled_at,omitempty"`
	RevokedAt    *time.Time      `json:"revoked_at,omitempty"`
```

In `toWorkerResponse`, after the `disabledAt` block and before the `return`, add:

```go
	var revokedAt *time.Time
	if w.RevokedAt.Valid {
		t := w.RevokedAt.Time
		revokedAt = &t
	}
```

And add `RevokedAt: revokedAt,` to the returned `workerResponse{...}` literal (next to `LastSeenAt`/`DisabledAt`).

- [ ] **Step 4: Add the sort spec, row-key helper, and handler**

In `internal/api/workers.go`, after the `WorkersSortSpec` var, add:

```go
var RevokedWorkersSortSpec = SortSpec{
	Default: "-revoked_at",
	Keys: map[string]SortKeyKind{
		"revoked_at": SortKeyTimestamp,
	},
}

func workersRowKeyByRevoked(w store.Worker) (anySortVal, pgtype.UUID) {
	if !w.RevokedAt.Valid {
		return (*time.Time)(nil), w.ID
	}
	t := w.RevokedAt.Time
	return &t, w.ID
}
```

After `handleListWorkers`, add the handler:

```go
// handleListRevokedWorkers lists workers with status 'revoked' for admin audit.
// Admin-only. Ordered revoked_at DESC NULLS LAST, id DESC. Revoked workers are
// excluded from every other list/stats endpoint; this is the only surface for them.
func (s *Server) handleListRevokedWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, RevokedWorkersSortSpec)
	if !ok {
		return
	}

	total, err := s.q.CountRevokedWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count revoked workers failed")
		return
	}

	rows, err := s.q.ListRevokedWorkersPage(ctx, store.ListRevokedWorkersPageParams{
		CursorSet:    pp.Cursor.Set,
		CursorIsNull: pp.Cursor.IsNull,
		CursorTs:     pp.CursorTs(),
		CursorID:     pp.Cursor.ID,
		PageLimit:    pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list revoked workers failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKeyByRevoked)
	writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})
}
```

Note: `ListRevokedWorkersPageParams` field names (`CursorSet`, `CursorIsNull`, `CursorTs`, `CursorID`, `PageLimit`) are generated from the `@cursor_set`, `@cursor_is_null`, `@cursor_ts`, `@cursor_id`, `@page_limit` sqlc args in Task 1 - identical to `ListWorkersPageByLastSeenDescParams`. If `make generate` produced different names, match what it generated.

- [ ] **Step 5: Register the route (admin-only)**

In `internal/api/server.go`, in the workers route block (right after the line registering `DELETE /v1/workers/{id}/token` at line ~130), add:

```go
	mux.Handle("GET /v1/workers/revoked", auth(admin(http.HandlerFunc(s.handleListRevokedWorkers))))
```

Place it before the `GET /v1/workers/{id}` route is matched. Go's `net/http` ServeMux matches the more specific literal `/v1/workers/revoked` over the wildcard `/v1/workers/{id}`, so ordering in the file does not matter, but keep it grouped with the other worker routes.

- [ ] **Step 6: Run the integration test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListRevokedWorkers_" -v -timeout 180s`
Expected: PASS (2 tests).

- [ ] **Step 7: Run the existing workers/stats regression tests**

Run: `go test -tags integration -p 1 ./internal/api/... -run "TestListWorkers_ExcludesRevoked|TestWorkerStats" -v -timeout 240s`
Expected: PASS - the operational list and stats still exclude revoked workers (no behavior change there).

- [ ] **Step 8: Verify the package builds and unit tests pass**

Run: `go build ./... && go test ./internal/api/... -timeout 60s`
Expected: builds clean; non-integration tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/api/workers.go internal/api/server.go internal/api/workers_revoked_list_integration_test.go
git commit -m "feat(api): add admin-only GET /v1/workers/revoked endpoint"
```

---

## Task 3: CLI `relay workers list --revoked`

**Files:**
- Modify: `internal/cli/workers.go`
- Create: `internal/cli/workers_revoked_list_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cli/workers_revoked_list_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkersList_Revoked_HitsRevokedEndpoint(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "w1", "name": "gone-1", "status": "revoked", "revoked_at": "2026-01-02T03:04:05Z"},
			},
			"next_cursor": "",
			"total":       1,
		})
	}))
	defer ts.Close()

	cfg := &Config{ServerURL: ts.URL, Token: "t"}
	var buf strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"list", "--revoked"}, &buf)
	if err != nil {
		t.Fatalf("doWorkers: %v", err)
	}
	if gotPath != "/v1/workers/revoked" {
		t.Fatalf("expected /v1/workers/revoked, got %s", gotPath)
	}
	out := buf.String()
	if !strings.Contains(out, "REVOKED AT") {
		t.Fatalf("expected REVOKED AT column header, got:\n%s", out)
	}
	if !strings.Contains(out, "2026-01-02T03:04:05Z") {
		t.Fatalf("expected revoked_at value in output, got:\n%s", out)
	}
}
```

Note: `Config` has `ServerURL` and `Token` fields and a `NewClient()` method (see `doWorkers`). If the field name for the base URL differs, match the actual struct in `internal/cli/config.go`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/... -run TestWorkersList_Revoked_HitsRevokedEndpoint -v -timeout 60s`
Expected: FAIL - `--revoked` flag unknown / output lacks `REVOKED AT`.

- [ ] **Step 3: Add `RevokedAt` to the CLI response struct**

In `internal/cli/workers.go`, add a field to `workerResp`:

```go
	Status   string `json:"status"`
	RevokedAt string `json:"revoked_at"`
```

- [ ] **Step 4: Add the `--revoked` flag and branch in `doWorkersList`**

Replace the body of `doWorkersList` in `internal/cli/workers.go` with:

```go
func doWorkersList(ctx context.Context, c *relayclient.Client, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("workers list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output raw JSON")
	limitFlag := fs.Int("limit", 0, "cap output at N rows (0 = all)")
	sortFlag := fs.String("sort", "", "sort order; e.g. -name or status (server-validated)")
	revoked := fs.Bool("revoked", false, "list revoked (decommissioned) workers instead (admin only)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	path := "/v1/workers"
	if *revoked {
		path = "/v1/workers/revoked"
	}

	var params url.Values
	if *sortFlag != "" {
		params = url.Values{}
		params.Set("sort", *sortFlag)
	}
	workers, total, err := relayclient.FetchAllPages[workerResp](ctx, c, path, params, *limitFlag)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(w).Encode(workers)
	}
	fmt.Fprintf(w, "Total: %d\n", total)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if *revoked {
		fmt.Fprintln(tw, "ID\tNAME\tHOSTNAME\tREVOKED AT")
		for _, wk := range workers {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", wk.ID, wk.Name, wk.Hostname, wk.RevokedAt)
		}
		return tw.Flush()
	}
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tCPU\tRAM GB\tGPUS\tGPU MODEL")
	for _, wk := range workers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			wk.ID, wk.Name, wk.Status, wk.CpuCores, wk.RamGb, wk.GpuCount, wk.GpuModel)
	}
	return tw.Flush()
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/cli/... -run TestWorkersList_Revoked_HitsRevokedEndpoint -v -timeout 60s`
Expected: PASS.

- [ ] **Step 6: Run the full CLI test package**

Run: `go test ./internal/cli/... -timeout 60s`
Expected: PASS - existing `workers list` tests unaffected.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/workers.go internal/cli/workers_revoked_list_test.go
git commit -m "feat(cli): add 'relay workers list --revoked' for revoked workers"
```

---

## Task 4: Web "Decommissioned" tab

**Files:**
- Modify: `web/src/workers/api.ts`
- Modify: `web/src/workers/api.test.ts`
- Create: `web/src/workers/useRevokedWorkers.ts`
- Create: `web/src/workers/RevokedWorkersTable.tsx`
- Create: `web/src/workers/RevokedWorkersTable.test.tsx`
- Modify: `web/src/workers/WorkersPage.tsx`

Run web commands from the `web/` directory.

- [ ] **Step 1: Write the failing api.test.ts case**

In `web/src/workers/api.test.ts`, update the import line and add a test:

```ts
import { listWorkers, getWorkerStats, listRevokedWorkers, type WorkersPage } from './api'
```

Add at the end of the file:

```ts
test('listRevokedWorkers fetches /workers/revoked with limit=50', async () => {
  let captured: string | undefined
  server.use(
    http.get('/v1/workers/revoked', ({ request }) => {
      captured = new URL(request.url).pathname
      return HttpResponse.json({
        items: [{ id: 'w1', name: 'gone-1', status: 'revoked', revoked_at: '2026-01-02T03:04:05Z' }],
        next_cursor: '',
        total: 1,
      })
    }),
  )
  const page = await listRevokedWorkers()
  expect(captured).toBe('/v1/workers/revoked')
  expect(page.items[0].revoked_at).toBe('2026-01-02T03:04:05Z')
})
```

- [ ] **Step 2: Run the web test to verify it fails**

Run (in `web/`): `npm run test -- --run src/workers/api.test.ts`
Expected: FAIL - `listRevokedWorkers` is not exported.

- [ ] **Step 3: Extend api.ts**

In `web/src/workers/api.ts`:

Change the status union:

```ts
export type WorkerStatus = 'online' | 'stale' | 'offline' | 'disabled' | 'revoked'
```

Add `revoked_at` to the `Worker` interface (next to `disabled_at`):

```ts
  disabled_at?: string
  revoked_at?: string
```

Add the new client function at the end of the file:

```ts
// Admin-only. Lists revoked (decommissioned) workers, newest revocation first.
// First page only; limit=50 matches listWorkers.
export function listRevokedWorkers(): Promise<WorkersPage> {
  const q = new URLSearchParams({ limit: '50' })
  return apiFetch<WorkersPage>(`/workers/revoked?${q}`)
}
```

- [ ] **Step 4: Run the web api test to verify it passes**

Run (in `web/`): `npm run test -- --run src/workers/api.test.ts`
Expected: PASS.

- [ ] **Step 5: Create the hook**

Create `web/src/workers/useRevokedWorkers.ts`:

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { listRevokedWorkers } from './api'

// Polls the first page of revoked workers. enabled gates the query so it only
// runs while the Decommissioned tab is active. intervalMs defaults to 3000.
export function useRevokedWorkers(enabled: boolean, intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', 'revoked'],
    queryFn: listRevokedWorkers,
    enabled,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 6: Write the failing RevokedWorkersTable render test**

Create `web/src/workers/RevokedWorkersTable.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { RevokedWorkersTable } from './RevokedWorkersTable'
import type { Worker } from './api'

const revoked: Worker = {
  id: 'w1',
  name: 'gone-1',
  hostname: 'gone-1-host',
  cpu_cores: 4,
  ram_gb: 16,
  gpu_count: 0,
  gpu_model: '',
  os: 'linux',
  max_slots: 1,
  labels: null,
  status: 'revoked',
  revoked_at: '2026-01-02T03:04:05Z',
}

test('renders revoked workers with hostname and revoked time', () => {
  render(<RevokedWorkersTable workers={[revoked]} />)
  expect(screen.getByText('gone-1')).toBeInTheDocument()
  expect(screen.getByText('gone-1-host')).toBeInTheDocument()
})

test('renders an empty state when there are no revoked workers', () => {
  render(<RevokedWorkersTable workers={[]} />)
  expect(screen.getByText('No revoked workers.')).toBeInTheDocument()
})
```

- [ ] **Step 7: Run the render test to verify it fails**

Run (in `web/`): `npm run test -- --run src/workers/RevokedWorkersTable.test.tsx`
Expected: FAIL - `RevokedWorkersTable` does not exist.

- [ ] **Step 8: Create the table component**

Create `web/src/workers/RevokedWorkersTable.tsx`:

```tsx
import type { Worker } from './api'

function formatRevokedAt(iso?: string): string {
  if (!iso) return 'unknown'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? 'unknown' : d.toLocaleString()
}

export function RevokedWorkersTable({ workers }: { workers: Worker[] }) {
  if (workers.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No revoked workers.
      </div>
    )
  }
  return (
    <table className="w-full text-left text-[13px]">
      <thead className="font-mono text-[11px] text-fg-mute">
        <tr>
          <th className="py-2 pr-4">NAME</th>
          <th className="py-2 pr-4">HOSTNAME</th>
          <th className="py-2 pr-4">REVOKED AT</th>
        </tr>
      </thead>
      <tbody>
        {workers.map((w) => (
          <tr key={w.id} className="border-t border-border">
            <td className="py-2 pr-4">{w.name}</td>
            <td className="py-2 pr-4 text-fg-mute">{w.hostname}</td>
            <td className="py-2 pr-4 text-fg-mute">{formatRevokedAt(w.revoked_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
```

- [ ] **Step 9: Run the render test to verify it passes**

Run (in `web/`): `npm run test -- --run src/workers/RevokedWorkersTable.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 10: Wire the Decommissioned tab into WorkersPage**

Replace the entire contents of `web/src/workers/WorkersPage.tsx` with:

```tsx
import { useState } from 'react'
import { Button } from '../components/Button'
import { useWorkers } from './useWorkers'
import { useWorkerStats } from './useWorkerStats'
import { useRevokedWorkers } from './useRevokedWorkers'
import { WorkersGrid } from './WorkersGrid'
import { WorkersTable, type SortField } from './WorkersTable'
import { RevokedWorkersTable } from './RevokedWorkersTable'
import type { Worker, WorkerSort, WorkerStats, WorkerStatus } from './api'

type View = 'grid' | 'table'
type Section = 'active' | 'decommissioned'

const VIEW_KEY = 'relay.workers.view'

function loadView(): View {
  return localStorage.getItem(VIEW_KEY) === 'table' ? 'table' : 'grid'
}

function toggleSort(field: SortField, current: WorkerSort): WorkerSort {
  if (current.replace('-', '') === field) {
    return (current.startsWith('-') ? field : `-${field}`) as WorkerSort
  }
  return field
}

function countByStatus(workers: Worker[]): Record<WorkerStatus, number> {
  const counts: Record<WorkerStatus, number> = { online: 0, stale: 0, offline: 0, disabled: 0, revoked: 0 }
  for (const w of workers) counts[w.status]++
  return counts
}

export function WorkersPage() {
  const [sort, setSort] = useState<WorkerSort>('-created_at')
  const [view, setView] = useState<View>(loadView)
  const [section, setSection] = useState<Section>('active')
  const { data, error, isLoading, isFetching, refetch } = useWorkers(sort)
  const { data: stats } = useWorkerStats()
  const revoked = useRevokedWorkers(section === 'decommissioned')

  function chooseView(v: View) {
    setView(v)
    localStorage.setItem(VIEW_KEY, v)
  }

  const sectionTabs = (
    <div className="flex rounded-full border border-border p-0.5">
      {(['active', 'decommissioned'] as Section[]).map((s) => (
        <button
          key={s}
          type="button"
          aria-pressed={section === s}
          onClick={() => setSection(s)}
          className={`rounded-full px-3 py-1 text-[12px] ${section === s ? 'bg-accent text-bg' : 'text-fg-mute'}`}
        >
          {s === 'active' ? 'Active' : 'Decommissioned'}
        </button>
      ))}
    </div>
  )

  const header = (
    <div className="flex flex-wrap items-end gap-6">
      <div>
        <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
        <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
      </div>
      <div className="ml-auto">{sectionTabs}</div>
    </div>
  )

  if (section === 'decommissioned') {
    return (
      <div className="flex flex-col gap-4">
        {header}
        {revoked.isLoading && !revoked.data ? (
          <div className="text-[13px] text-fg-mute">Loading...</div>
        ) : revoked.error && !revoked.data ? (
          <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
            <div className="mb-3 text-[13px] text-err">{(revoked.error as Error).message}</div>
            <Button className="w-auto px-4" onClick={() => revoked.refetch()}>
              Retry
            </Button>
          </div>
        ) : (
          <RevokedWorkersTable workers={revoked.data?.items ?? []} />
        )}
      </div>
    )
  }

  if (isLoading && !data) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-28 rounded-card border border-border bg-white/5" />
          ))}
        </div>
      </div>
    )
  }

  if (error && !data) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center">
          <div className="mb-3 text-[13px] text-err">{(error as Error).message}</div>
          <Button className="w-auto px-4" onClick={() => refetch()}>
            Retry
          </Button>
        </div>
      </div>
    )
  }

  const workers = data?.items ?? []
  if (workers.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        {header}
        <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
          No workers enrolled yet.
        </div>
      </div>
    )
  }

  // Prefer fleet-wide counts from the stats endpoint. Until the first stats
  // response arrives, fall back to page-scoped counts so the strip is never empty.
  const fallback = countByStatus(workers)
  const counts: WorkerStats = stats ?? {
    online: fallback.online,
    stale: fallback.stale,
    offline: fallback.offline,
    disabled: fallback.disabled,
    total: data?.total ?? workers.length,
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-6">
        <div>
          <div className="font-mono text-[11px] tracking-widest text-fg-mute">FLEET</div>
          <h1 className="text-[32px] font-normal tracking-tight">Workers</h1>
        </div>
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-ok">{counts.online}</b> ONLINE</span>
          <span><b className="text-warn">{counts.stale}</b> STALE</span>
          <span><b className="text-fg-mute">{counts.disabled}</b> DISABLED</span>
          <span><b className="text-err">{counts.offline}</b> OFFLINE</span>
          <span className="text-fg-dim">· <span>{`${counts.total} workers`}</span></span>
        </div>
        <div className="ml-auto flex items-center gap-3">
          {sectionTabs}
          <span className="font-mono text-[10px] text-fg-mute">
            <span className={isFetching ? 'text-ok' : 'text-fg-dim'}>●</span> live · auto-refreshing
          </span>
          <div className="flex rounded-full border border-border p-0.5">
            {(['grid', 'table'] as View[]).map((v) => (
              <button
                key={v}
                type="button"
                aria-pressed={view === v}
                onClick={() => chooseView(v)}
                className={`rounded-full px-3 py-1 text-[12px] ${view === v ? 'bg-accent text-bg' : 'text-fg-mute'}`}
              >
                {v === 'grid' ? 'Grid' : 'Table'}
              </button>
            ))}
          </div>
        </div>
      </div>

      {view === 'grid' ? (
        <WorkersGrid workers={workers} />
      ) : (
        <WorkersTable workers={workers} sort={sort} onSort={(f) => setSort((cur) => toggleSort(f, cur))} />
      )}
    </div>
  )
}
```

- [ ] **Step 11: Run the full workers web test suite**

Run (in `web/`): `npm run test -- --run src/workers/`
Expected: PASS. If `WorkersPage.test.tsx` asserts on header markup that moved, update those assertions minimally to match (the FLEET label, "Workers" heading, and status strip are all still present in the active section).

- [ ] **Step 12: Typecheck and build the web app**

Run (in `web/`): `npm run build`
Expected: TypeScript compiles with no errors (the `countByStatus` record now includes `revoked`, satisfying the widened `WorkerStatus` union).

- [ ] **Step 13: Commit**

```bash
git add web/src/workers/api.ts web/src/workers/api.test.ts web/src/workers/useRevokedWorkers.ts web/src/workers/RevokedWorkersTable.tsx web/src/workers/RevokedWorkersTable.test.tsx web/src/workers/WorkersPage.tsx
git commit -m "feat(web): add Decommissioned tab listing revoked workers"
```

---

## Task 5: Documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the REST endpoint**

In `README.md`, in the workers REST API table (near line 1187, after the `GET /v1/workers/stats` row), add:

```
| `GET` | `/v1/workers/revoked` | List revoked (decommissioned) workers for audit, newest revocation first (admin only). Paginated, same `page` envelope as `GET /v1/workers`. Each item includes `revoked_at`. |
```

- [ ] **Step 2: Document the CLI flag**

In `README.md`, find the `relay workers list` CLI reference section and add a note/example:

```
relay workers list --revoked    # list revoked (decommissioned) workers (admin only)
```

If there is a dedicated CLI examples block for workers, place it alongside the existing `relay workers list` examples; otherwise add it to the workers command description.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document revoked workers endpoint and CLI flag"
```

---

## Final verification

- [ ] **Step 1: Full unit test suite**

Run: `make test`
Expected: PASS.

- [ ] **Step 2: Full integration suite for touched packages**

Run: `go test -tags integration -p 1 ./internal/store/... ./internal/api/... -timeout 600s`
Expected: PASS (requires Docker).

- [ ] **Step 3: Web suite + build**

Run (in `web/`): `npm run test -- --run && npm run build`
Expected: PASS, clean build.

- [ ] **Step 4: Close the backlog item**

```bash
git mv docs/backlog/idea-2026-06-04-surface-revoked-workers-admin-audit.md docs/backlog/closed/
```

Edit the moved file's frontmatter `status: open` to `status: closed`. Commit:

```bash
git add docs/backlog/
git commit -m "chore(backlog): close surface-revoked-workers item"
```

---

## Self-Review Notes

- **Spec coverage:** schema (Task 1), `ClearWorkerAgentToken` stamp + `SetWorkerAgentToken` clear (Task 1), `ListRevokedWorkersPage`/`CountRevokedWorkers` (Task 1), admin-only REST endpoint + `revoked_at` response field (Task 2), CLI `--revoked` (Task 3), web Decommissioned tab + `revoked_at` (Task 4), README (Task 5). Stats/operational-list exclusion preserved and regression-tested (Task 2 Step 7).
- **Invariant tested:** re-enrollment clears `revoked_at` (Task 1 `TestSetWorkerAgentToken_ClearsRevokedAt`).
- **Type consistency:** `RevokedWorkersSortSpec`, `workersRowKeyByRevoked`, `handleListRevokedWorkers`, `ListRevokedWorkersPageParams` (Go); `listRevokedWorkers`, `useRevokedWorkers`, `RevokedWorkersTable`, `WorkerStatus` with `'revoked'` (web) - names match across tasks.
- **Backlog housekeeping** (final verification Step 4) is required scope per project convention.
