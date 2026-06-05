# Workers Stats Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `GET /v1/workers/stats` returning fleet-wide worker counts (online/stale/offline/disabled + total), and rewire the Workers page summary strip to show those fleet-wide totals instead of page-scoped counts.

**Architecture:** A single `FILTER`-based aggregate query computes the four buckets, mirroring the per-row "disabled is an overlay that wins over internal status" rule and excluding `revoked` workers. A thin handler sums the buckets into `total`. The React summary strip consumes a new polling hook, falling back to the existing page-scoped counts only until the first stats response arrives.

**Tech Stack:** Go, sqlc, pgx, Go 1.22 `net/http.ServeMux`; React + TypeScript, @tanstack/react-query, MSW + Vitest.

**Spec:** `docs/superpowers/specs/2026-06-04-workers-stats-endpoint-design.md`

---

## File Structure

**Backend**
- Modify `internal/store/query/workers.sql` — add the `WorkerStatusCounts` query.
- Generated (do not hand-edit): `internal/store/workers.sql.go`, via `make generate`.
- Modify `internal/api/workers.go` — add `workerStatsResponse` struct + `handleWorkerStats`.
- Modify `internal/api/server.go` — register `GET /v1/workers/stats`.
- Create `internal/api/workers_stats_integration_test.go` — integration test.
- Modify `README.md` — document the endpoint.

**Frontend**
- Modify `web/src/workers/api.ts` — add `WorkerStats` type + `getWorkerStats()`.
- Modify `web/src/workers/api.test.ts` — test `getWorkerStats()`.
- Create `web/src/workers/useWorkerStats.ts` — polling hook.
- Create `web/src/workers/useWorkerStats.test.tsx` — hook test.
- Modify `web/src/workers/WorkersPage.tsx` — consume the hook in the strip.
- Modify `web/src/workers/WorkersPage.test.tsx` — mock the stats endpoint; add a fleet-wide assertion.

**Cleanup**
- `git mv docs/backlog/idea-2026-06-03-workers-stats-endpoint.md docs/backlog/closed/`

---

## Task 1: Add the `WorkerStatusCounts` query and generate the store

**Files:**
- Modify: `internal/store/query/workers.sql`
- Generated: `internal/store/workers.sql.go` (via `make generate`)

- [ ] **Step 1: Add the query to `internal/store/query/workers.sql`**

Append at the end of the file:

```sql
-- name: WorkerStatusCounts :one
-- Fleet-wide worker counts for the dashboard summary strip. "disabled" is an
-- overlay (disabled_at IS NOT NULL) that wins over the internal status, mirroring
-- toWorkerResponse. Revoked workers (not disabled) fall into no bucket and are
-- excluded from the total computed by the caller.
SELECT
    COUNT(*) FILTER (WHERE disabled_at IS NOT NULL)                    AS disabled,
    COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'online')  AS online,
    COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'stale')   AS stale,
    COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'offline') AS offline
FROM workers;
```

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: succeeds with no errors; `git status` shows `internal/store/workers.sql.go` modified.

- [ ] **Step 3: Verify the generated method exists**

Run: `git diff internal/store/workers.sql.go`
Expected: a new `func (q *Queries) WorkerStatusCounts(ctx context.Context) (WorkerStatusCountsRow, error)` and a `WorkerStatusCountsRow` struct with `Disabled`, `Online`, `Stale`, `Offline` (all `int64`).

- [ ] **Step 4: Confirm it still compiles**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go
git commit -m "feat(store): add WorkerStatusCounts aggregate query"
```

---

## Task 2: Add the handler and route

**Files:**
- Modify: `internal/api/workers.go`
- Modify: `internal/api/server.go:113-116` (workers route block)

- [ ] **Step 1: Add the response struct and handler to `internal/api/workers.go`**

Add after the `toWorkerResponse` function (before `var WorkersSortSpec`):

```go
// workerStatsResponse is the fleet-wide summary returned by GET /v1/workers/stats.
// total is the sum of the four buckets; revoked workers are in no bucket and are
// therefore excluded from total.
type workerStatsResponse struct {
	Online   int64 `json:"online"`
	Stale    int64 `json:"stale"`
	Offline  int64 `json:"offline"`
	Disabled int64 `json:"disabled"`
	Total    int64 `json:"total"`
}

func (s *Server) handleWorkerStats(w http.ResponseWriter, r *http.Request) {
	c, err := s.q.WorkerStatusCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "worker stats failed")
		return
	}
	writeJSON(w, http.StatusOK, workerStatsResponse{
		Online:   c.Online,
		Stale:    c.Stale,
		Offline:  c.Offline,
		Disabled: c.Disabled,
		Total:    c.Online + c.Stale + c.Offline + c.Disabled,
	})
}
```

- [ ] **Step 2: Register the route in `internal/api/server.go`**

Find this line (currently `internal/api/server.go:113`):

```go
	mux.Handle("GET /v1/workers", auth(http.HandlerFunc(s.handleListWorkers)))
```

Add immediately after it:

```go
	mux.Handle("GET /v1/workers/stats", auth(http.HandlerFunc(s.handleWorkerStats)))
```

(The literal `stats` segment takes precedence over the `{id}` wildcard in Go 1.22's ServeMux, so ordering relative to `GET /v1/workers/{id}` does not matter.)

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/api/workers.go internal/api/server.go
git commit -m "feat(api): add GET /v1/workers/stats handler and route"
```

---

## Task 3: Integration test for the stats endpoint

**Files:**
- Create: `internal/api/workers_stats_integration_test.go`

This is an integration test (requires Docker; runs under `make test-integration`). It reuses helpers from the `api_test` package: `newTestServerWithPool`, `createTestUser`, `createTestToken`, `seedWorker`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/workers_stats_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func getWorkerStats(t *testing.T, srv interface {
	Handler() http.Handler
}, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/workers/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body map[string]any
	if rec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	}
	return rec.Code, body
}

func TestWorkerStats_BucketsAndTotal(t *testing.T) {
	srv, q, pool := newTestServerWithPool(t)
	// Non-admin user: the endpoint is not admin-only.
	user := createTestUser(t, q, "Stats", "stats-user@test.com", false)
	token := createTestToken(t, q, user.ID)

	// Buckets we expect: online=2, stale=1, offline=1, disabled=2, total=6.
	// revoked-only worker is excluded; the disabled+revoked worker counts as disabled.
	seedWorker(t, pool, "on-1", "online", nil)
	seedWorker(t, pool, "on-2", "online", nil)
	seedWorker(t, pool, "st-1", "stale", nil)
	seedWorker(t, pool, "off-1", "offline", nil)
	seedWorker(t, pool, "rev-1", "revoked", nil) // excluded entirely

	// disabled with an internal online status -> counts as disabled
	dis := seedWorker(t, pool, "dis-1", "online", nil)
	_, err := pool.Exec(t.Context(), "UPDATE workers SET disabled_at = NOW() WHERE id = $1", dis)
	require.NoError(t, err)

	// disabled AND revoked -> overlay wins, counts as disabled
	disRev := seedWorker(t, pool, "dis-rev-1", "revoked", nil)
	_, err = pool.Exec(t.Context(), "UPDATE workers SET disabled_at = NOW() WHERE id = $1", disRev)
	require.NoError(t, err)

	code, body := getWorkerStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 2, body["online"])
	require.EqualValues(t, 1, body["stale"])
	require.EqualValues(t, 1, body["offline"])
	require.EqualValues(t, 2, body["disabled"])
	// total excludes the revoked-only worker: 2+1+1+2 = 6 (not 7).
	require.EqualValues(t, 6, body["total"])
}

func TestWorkerStats_EmptyFleet(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Empty", "stats-empty@test.com", false)
	token := createTestToken(t, q, user.ID)

	code, body := getWorkerStats(t, srv, token)
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, body["online"])
	require.EqualValues(t, 0, body["disabled"])
	require.EqualValues(t, 0, body["total"])
}
```

- [ ] **Step 2: Run the test to verify it passes**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestWorkerStats -v -timeout 120s`
Expected: `TestWorkerStats_BucketsAndTotal` and `TestWorkerStats_EmptyFleet` both PASS. (Requires Docker Desktop running.)

Note: `seedWorker` only sets columns via direct INSERT and does not expose `disabled_at`; the test stamps it with an inline `pool.Exec` `UPDATE`, which is why the disabled and disabled+revoked workers are built in two steps.

- [ ] **Step 3: Commit**

```bash
git add internal/api/workers_stats_integration_test.go
git commit -m "test(api): cover GET /v1/workers/stats buckets, overlay, and revoked exclusion"
```

---

## Task 4: Document the endpoint in the README

**Files:**
- Modify: `README.md` (workers endpoint table, currently around `README.md:1175`)

- [ ] **Step 1: Add the row**

Find this line:

```
| `GET` | `/v1/workers` | List workers. Paginated. Order: created_at DESC (changed from name ASC). |
```

Add immediately after it:

```
| `GET` | `/v1/workers/stats` | Fleet-wide worker counts: `online`, `stale`, `offline`, `disabled`, and `total`. `total` is the sum of those buckets; revoked workers are excluded. Same bearer-auth as `GET /v1/workers`. |
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: document GET /v1/workers/stats"
```

---

## Task 5: Frontend API client for stats

**Files:**
- Modify: `web/src/workers/api.ts`
- Modify: `web/src/workers/api.test.ts`

All web commands run from the `web/` directory.

- [ ] **Step 1: Write the failing test**

Add to `web/src/workers/api.test.ts` (extend the imports and add tests):

Change the import line:

```ts
import { listWorkers, getWorkerStats, type WorkersPage } from './api'
```

Add these tests at the end of the file:

```ts
test('getWorkerStats fetches /workers/stats', async () => {
  let captured: string | undefined
  server.use(
    http.get('/v1/workers/stats', ({ request }) => {
      captured = new URL(request.url).pathname
      return HttpResponse.json({ online: 3, stale: 1, offline: 2, disabled: 1, total: 7 })
    }),
  )
  const stats = await getWorkerStats()
  expect(captured).toBe('/v1/workers/stats')
  expect(stats.total).toBe(7)
  expect(stats.online).toBe(3)
})

test('getWorkerStats throws ApiError on the error envelope', async () => {
  server.use(
    http.get('/v1/workers/stats', () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
  )
  await expect(getWorkerStats()).rejects.toBeInstanceOf(ApiError)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npm test -- api.test.ts`
Expected: FAIL — `getWorkerStats` is not exported from `./api`.

- [ ] **Step 3: Implement in `web/src/workers/api.ts`**

Add after the `WorkerStatus` type (or near the other interfaces):

```ts
export interface WorkerStats {
  online: number
  stale: number
  offline: number
  disabled: number
  total: number
}
```

Add at the end of the file:

```ts
// Fleet-wide worker counts for the summary strip. Buckets sum to total; revoked
// workers are excluded server-side.
export function getWorkerStats(): Promise<WorkerStats> {
  return apiFetch<WorkerStats>('/workers/stats')
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npm test -- api.test.ts`
Expected: PASS (all api.test.ts tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/api.ts web/src/workers/api.test.ts
git commit -m "feat(web): add getWorkerStats API client"
```

---

## Task 6: `useWorkerStats` polling hook

**Files:**
- Create: `web/src/workers/useWorkerStats.ts`
- Create: `web/src/workers/useWorkerStats.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/workers/useWorkerStats.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { expect, test } from 'vitest'
import { server } from '../test/setup-helpers'
import { useWorkerStats } from './useWorkerStats'

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

test('fetches stats and refetches on the interval', async () => {
  let count = 0
  server.use(
    http.get('/v1/workers/stats', () => {
      count++
      return HttpResponse.json({ online: 1, stale: 0, offline: 0, disabled: 0, total: 1 })
    }),
  )

  const { result } = renderHook(() => useWorkerStats(20), { wrapper })

  await waitFor(() => expect(count).toBeGreaterThanOrEqual(1))
  await waitFor(() => expect(count).toBeGreaterThanOrEqual(2))
  await waitFor(() => expect(result.current.data?.total).toBe(1))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npm test -- useWorkerStats.test.tsx`
Expected: FAIL — cannot resolve `./useWorkerStats`.

- [ ] **Step 3: Implement `web/src/workers/useWorkerStats.ts`**

```ts
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getWorkerStats } from './api'

// Polls fleet-wide worker counts for the summary strip. Same cadence as
// useWorkers. intervalMs defaults to 3000; tests inject a small value.
export function useWorkerStats(intervalMs = 3000) {
  return useQuery({
    queryKey: ['workers', 'stats'],
    queryFn: getWorkerStats,
    refetchInterval: intervalMs,
    placeholderData: keepPreviousData,
  })
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npm test -- useWorkerStats.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/workers/useWorkerStats.ts web/src/workers/useWorkerStats.test.tsx
git commit -m "feat(web): add useWorkerStats polling hook"
```

---

## Task 7: Rewire the summary strip to fleet-wide counts

**Files:**
- Modify: `web/src/workers/WorkersPage.tsx`
- Modify: `web/src/workers/WorkersPage.test.tsx`

- [ ] **Step 1: Update the existing tests to mock the stats endpoint and add a fleet-wide assertion**

In `web/src/workers/WorkersPage.test.tsx`, change the imports line:

```ts
import { afterEach, beforeEach, expect, test } from 'vitest'
```

Add a default stats fixture + handler right after the `page` constant (so every test that renders the page has the stats endpoint mocked — required because `onUnhandledRequest: 'error'` and the hook fires on every render, including before the loading/empty/error early returns):

```ts
const stats = { online: 1, stale: 0, offline: 1, disabled: 0, total: 2 }

beforeEach(() => {
  server.use(http.get('/v1/workers/stats', () => HttpResponse.json(stats)))
})
```

The existing `'renders workers and the page-scoped summary'` test asserts `screen.getByText('2 workers')`. With the default stats fixture above (`total: 2`) this still passes; rename it for accuracy by changing its title to:

```ts
test('renders workers and the fleet-wide summary', async () => {
```

Add a new test at the end of the file proving the strip reads the stats endpoint, not the page total:

```ts
test('summary strip shows fleet-wide totals from the stats endpoint', async () => {
  server.use(http.get('/v1/workers', () => HttpResponse.json(page))) // page total = 2
  server.use(
    http.get('/v1/workers/stats', () =>
      HttpResponse.json({ online: 4, stale: 0, offline: 1, disabled: 0, total: 5 }),
    ),
  )
  renderWithQuery(<WorkersPage />)
  // page.total is 2, but the strip must show the fleet-wide total of 5.
  expect(await screen.findByText('5 workers')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the page tests to verify the new test fails**

Run: `cd web && npm test -- WorkersPage.test.tsx`
Expected: the new `fleet-wide totals` test FAILS (strip still shows `2 workers` from `data.total`); other tests pass.

- [ ] **Step 3: Implement the rewire in `web/src/workers/WorkersPage.tsx`**

Add the hook import near the top (with the other `./` imports):

```ts
import { useWorkerStats } from './useWorkerStats'
```

Add `WorkerStats` to the type import from `./api`:

```ts
import type { Worker, WorkerSort, WorkerStatus } from './api'
```

becomes:

```ts
import type { Worker, WorkerSort, WorkerStats, WorkerStatus } from './api'
```

Inside `WorkersPage`, add the hook call right after the existing `useWorkers` call:

```ts
  const { data: stats } = useWorkerStats()
```

Replace the existing `const counts = countByStatus(workers)` line (currently `WorkersPage.tsx:69`) with:

```ts
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
```

Replace the summary strip `<div>` (currently `WorkersPage.tsx:78-87`):

```tsx
        <div
          className="flex gap-4 font-mono text-[11px] text-fg-mute"
          title="Counts for the loaded page"
        >
          <span><b className="text-ok">{counts.online}</b> ONLINE</span>
          <span><b className="text-warn">{counts.stale}</b> STALE</span>
          <span><b className="text-fg-mute">{counts.disabled}</b> DISABLED</span>
          <span><b className="text-err">{counts.offline}</b> OFFLINE</span>
          <span className="text-fg-dim">· <span>{`${data?.total ?? workers.length} workers`}</span></span>
        </div>
```

with (drop the `title` caveat; use `counts.total`):

```tsx
        <div className="flex gap-4 font-mono text-[11px] text-fg-mute">
          <span><b className="text-ok">{counts.online}</b> ONLINE</span>
          <span><b className="text-warn">{counts.stale}</b> STALE</span>
          <span><b className="text-fg-mute">{counts.disabled}</b> DISABLED</span>
          <span><b className="text-err">{counts.offline}</b> OFFLINE</span>
          <span className="text-fg-dim">· <span>{`${counts.total} workers`}</span></span>
        </div>
```

(`countByStatus` remains in use as the fallback, so no orphaned code.)

- [ ] **Step 4: Run the page tests to verify they pass**

Run: `cd web && npm test -- WorkersPage.test.tsx`
Expected: PASS, including the new `fleet-wide totals` test.

- [ ] **Step 5: Typecheck and lint the web package**

Run: `cd web && npm run build`
Expected: TypeScript build succeeds with no type errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/workers/WorkersPage.tsx web/src/workers/WorkersPage.test.tsx
git commit -m "feat(web): summary strip shows fleet-wide worker counts"
```

---

## Task 8: Close the backlog item

**Files:**
- Move: `docs/backlog/idea-2026-06-03-workers-stats-endpoint.md` → `docs/backlog/closed/`

- [ ] **Step 1: Confirm the closed directory exists**

Run: `ls docs/backlog/closed/`
Expected: lists existing closed items (the directory exists). If it does not exist, create it: `mkdir -p docs/backlog/closed`.

- [ ] **Step 2: Move the file**

Run: `git mv docs/backlog/idea-2026-06-03-workers-stats-endpoint.md docs/backlog/closed/`

- [ ] **Step 3: Commit**

```bash
git add -A docs/backlog
git commit -m "backlog: close workers-stats-endpoint (implemented)"
```

---

## Final Verification

- [ ] **Step 1: Full unit test suite (no Docker)**

Run: `make test`
Expected: PASS.

- [ ] **Step 2: Integration tests for the api package**

Run: `go test -tags integration -p 1 ./internal/api/... -run TestWorkerStats -v -timeout 120s`
Expected: PASS (Docker Desktop running).

- [ ] **Step 3: Web test suite + build**

Run: `cd web && npm test && npm run build`
Expected: all Vitest tests pass; build succeeds.

- [ ] **Step 4: Confirm working tree is clean and all work committed**

Run: `git status`
Expected: nothing to commit, working tree clean.
