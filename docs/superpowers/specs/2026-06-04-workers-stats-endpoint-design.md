# Design: `GET /v1/workers/stats` aggregate endpoint

**Date:** 2026-06-04
**Backlog item:** `docs/backlog/idea-2026-06-03-workers-stats-endpoint.md`

## Problem

The Workers list page shows a status summary strip (online / stale / offline / disabled counts plus a total). Because `GET /v1/workers` is cursor-paginated and the UI holds only the first page (50 workers), those counts are page-scoped and labeled "Counts for the loaded page". A dedicated aggregate endpoint lets the strip show true fleet-wide totals.

## Key constraint: "disabled" is an overlay, not a status value

The `workers.status` column only ever holds `online`, `stale`, `offline`, or `revoked`. "Disabled" is a separate dimension derived from `disabled_at IS NOT NULL`. A disabled worker keeps its internal liveness status, but the per-row API (`toWorkerResponse` in `internal/api/workers.go`) reports it as `disabled` - the overlay wins. The stats endpoint must mirror that precedence so the strip's buckets agree with what each row displays in the list/grid.

`revoked` workers (agent token deleted) are never shown in the UI's strip. Per the design decision below, they are excluded from every stats bucket and from the stats total.

## Decisions

- **Revoked workers: excluded entirely, everywhere.** Stats counts only online/stale/offline/disabled. `total` = sum of those four buckets. For consistency, revoked workers are also excluded from the `GET /v1/workers` list endpoint and its `CountWorkers` total (see "List endpoint" below), so the stats `total` and the list `total` agree. The frontend's `WorkerStatus` type (4 values, no `revoked`) is thereby guaranteed by the backend.
- **Disabled-and-revoked worker: excluded.** Revoked exclusion wins over the disabled overlay, so a worker that is both disabled and revoked counts in no bucket - the same as the list endpoint, which excludes it as revoked. (Note: an earlier draft of this spec had the disabled overlay win for this edge case; that was reversed once revoked workers were excluded from the list endpoint, to keep stats and list consistent.)
- **Auth: any authenticated user** (the same `auth` middleware as `GET /v1/workers`), not admin-only.
- **Scope: backend + frontend.** Add the endpoint and rewire the summary strip to consume it.

## Backend

### SQL query

Add to `internal/store/query/workers.sql`, then `make generate`:

```sql
-- name: WorkerStatusCounts :one
-- Fleet-wide worker counts for the dashboard summary strip. "disabled" is an
-- overlay (disabled_at IS NOT NULL) that wins over the internal status, mirroring
-- toWorkerResponse. Revoked workers are excluded from every bucket and from the
-- total, including a worker that is both disabled and revoked.
SELECT
  COUNT(*) FILTER (WHERE disabled_at IS NOT NULL AND status != 'revoked') AS disabled,
  COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'online')       AS online,
  COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'stale')        AS stale,
  COUNT(*) FILTER (WHERE disabled_at IS NULL AND status = 'offline')      AS offline
FROM workers;
```

sqlc generates `WorkerStatusCounts(ctx) (WorkerStatusCountsRow, error)` with four `int64` fields.

### Handler

Add `handleWorkerStats` to `internal/api/workers.go`:

```go
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

`total` is computed in Go as the sum of the four buckets so they always reconcile and revoked never leaks in.

### Route

In `internal/api/server.go`, alongside the other workers routes:

```go
mux.Handle("GET /v1/workers/stats", auth(http.HandlerFunc(s.handleWorkerStats)))
```

Go 1.22's `ServeMux` matches the literal `stats` segment over the `{id}` wildcard, so there is no conflict with `GET /v1/workers/{id}` and registration order does not matter.

## Frontend

### `web/src/workers/api.ts`

```ts
export interface WorkerStats {
  online: number
  stale: number
  offline: number
  disabled: number
  total: number
}

export function getWorkerStats(): Promise<WorkerStats> {
  return apiFetch<WorkerStats>('/workers/stats')
}
```

### `web/src/workers/useWorkerStats.ts` (new)

A hook mirroring `useWorkers`: react-query, 3000 ms `refetchInterval`, `placeholderData: keepPreviousData`, `queryKey: ['workers', 'stats']`.

### `web/src/workers/WorkersPage.tsx`

- Call `useWorkerStats()`.
- The summary strip reads bucket counts and the total from `stats` instead of `countByStatus(workers)` and `data.total`.
- Remove the `title="Counts for the loaded page"` tooltip - the counts are now fleet-wide.
- While `stats` is still undefined on first paint, fall back to the existing page-scoped `countByStatus(workers)` and `data.total` as a transient placeholder, so the strip never renders broken or empty. `countByStatus` is retained solely as that fallback.

## Testing

`internal/api/workers_stats_integration_test.go` (build tag `integration`, following `workers_sort_integration_test.go`):

- Seed workers spanning every relevant state: online, stale, offline, disabled (with an internal online status), revoked, and one worker that is both disabled and revoked.
- Assert the four buckets have the exact expected counts, that the disabled-and-revoked worker appears in no bucket (revoked exclusion wins), that revoked-only workers appear in no bucket, and that `total` equals the sum of the four (excluding all revoked workers).
- Assert a non-admin authenticated user receives 200 (endpoint is not admin-only).

A separate test (`internal/api/workers_list_revoked_integration_test.go`) asserts that revoked workers are excluded from `GET /v1/workers` (both the returned rows and the `total`).

## List endpoint (revoked exclusion)

To keep the system consistent - revoked workers are ignored everywhere in the operational view - the `GET /v1/workers` list path also excludes them:

- Add `status != 'revoked'` to all eight paginated `ListWorkersPage*` queries (wrapping each existing cursor disjunction in parentheses so the `AND` binds across the whole row predicate) and to `CountWorkers`.
- Leave the non-paginated `ListWorkers` (used by the scheduler dispatch loop) and `ListWorkersByLiveness` unchanged.

This makes the list `total` and the stats `total` agree, and guarantees the frontend never receives a worker whose status is outside its 4-value `WorkerStatus` union.

## Docs

Add `GET /v1/workers/stats` to the workers section of `README.md` (endpoint table / REST reference), describing it as fleet-wide status counts excluding revoked workers.

## Cleanup

`git mv docs/backlog/idea-2026-06-03-workers-stats-endpoint.md docs/backlog/closed/` as part of this change.

## Out of scope

- No new `revoked` count surfaced anywhere.
- No change to how the scheduler enumerates workers (`ListWorkers` is left untouched).
- No caching of the count; it is a cheap single-row aggregate polled on the existing 3 s cadence.
