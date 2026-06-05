# Session Retro: 2026-06-04 - Workers Stats Endpoint

## What Was Built

A `GET /v1/workers/stats` aggregate endpoint returning fleet-wide worker counts by status (online/stale/offline/disabled) plus a total, and a rewire of the Workers page summary strip to consume it for true fleet-wide totals instead of page-scoped counts.

The work was scoped via the full brainstorm to spec to plan flow, then executed task-by-task with subagent-driven development (a fresh implementer per task plus a two-stage spec-compliance and code-quality review on each). Mid-session, a user question expanded the scope: revoked workers, which were leaking into the list endpoint and silently mishandled by the frontend, are now excluded consistently everywhere.

Delivered as [relay#12](https://github.com/chadmv/relay/pull/12):
- Backend: `WorkerStatusCounts` sqlc aggregate using `COUNT(*) FILTER (...)`, the `handleWorkerStats` handler (auth, not admin), the route, and integration tests covering every worker state.
- Revoked exclusion: added to all 8 paginated `ListWorkersPage*` queries plus `CountWorkers`, and to the stats `disabled` bucket, so revoked workers appear in no count or list anywhere in the operational view.
- Frontend: `getWorkerStats` client, `useWorkerStats` polling hook, and the strip consuming them with a page-scoped fallback until the first stats response.

## Key Decisions

- **"Disabled" is an overlay in the aggregate.** The query counts `disabled_at IS NOT NULL` as disabled regardless of internal status, mirroring the per-row `toWorkerResponse` behavior, rather than a naive `GROUP BY status` (which would never produce a "disabled" bucket).
- **`total` is summed in the handler**, not a separate `COUNT(*)`, so buckets always reconcile and excluded workers never leak into the total.
- **Revoked excluded everywhere (extended mid-session).** The original design excluded revoked only from stats and accepted that the list still counted them. After the user questioned this, the list endpoint and `CountWorkers` were changed to exclude revoked too, and the earlier "disabled+revoked counts as disabled" micro-decision was reversed so a disabled+revoked worker counts nowhere. Stats `total` and list `total` now agree, and the frontend's 4-value `WorkerStatus` union is guaranteed by the backend.
- **Scheduler's `ListWorkers` left untouched.** It is used by the dispatch loop, which already gates on status; filtering it would have changed scheduler behavior out of scope.

## Problems Encountered

- **A design gap surfaced during review, not design.** The "disabled+revoked counts as disabled" decision was internally consistent at brainstorm time but became inconsistent the moment we decided to exclude revoked from the list. Tracing the revoked invariant across all endpoints (not just the new one) would have caught at design time that the list endpoint already leaked revoked workers. The fix was correct but arrived as a mid-implementation pivot.
- **SQL operator precedence in the cursor queries.** Adding `status != 'revoked'` to `WHERE NOT @cursor_set OR (...)` required wrapping each existing disjunction in parentheses, because `AND` binds tighter than `OR`. Got this right by giving the implementer exact replacement blocks for all 8 variants, including the two nested-CASE last_seen queries.
- **CRLF/LF noise on Windows.** `sqlc generate` rewrote every `internal/store/*.sql.go` with line-ending-only changes (empty diffs), which polluted `git status` and produced a "12 uncommitted changes" warning at PR-create time. Had to repeatedly `git checkout -- internal/store/` to keep commits scoped to the two intended files.

## Known Limitations

- The stats query and the workers list poll on independent 3 s timers (separate react-query keys), so the strip total and the grid rows can briefly reflect different snapshots. Self-correcting within one tick.

## Open Questions

- See [`idea-2026-06-04-surface-revoked-workers-admin-audit`](../backlog/idea-2026-06-04-surface-revoked-workers-admin-audit.md) - Surface revoked workers for admin audit or re-enrollment

## Improvement Goals

- During brainstorming, when a feature introduces an "exclude X" rule, explicitly trace X across every related endpoint and the frontend type model before writing the spec, rather than scoping the rule to only the new code path.

## Files Most Touched

- `internal/store/query/workers.sql` - new `WorkerStatusCounts` aggregate; `status != 'revoked'` added to 8 paginated queries and `CountWorkers`.
- `internal/store/workers.sql.go` - sqlc-generated counterpart to the query changes.
- `internal/api/workers.go` - `workerStatsResponse` struct and `handleWorkerStats` handler.
- `internal/api/server.go` - `GET /v1/workers/stats` route registration.
- `internal/api/workers_stats_integration_test.go` - bucket/overlay/revoked-exclusion coverage including the disabled+revoked edge case.
- `internal/api/workers_list_revoked_integration_test.go` - asserts revoked workers are absent from the list rows and total.
- `web/src/workers/WorkersPage.tsx` - strip rewired to fleet-wide counts with page-scoped fallback; "loaded page" caveat dropped.
- `web/src/workers/useWorkerStats.ts` + `api.ts` - polling hook and API client for the new endpoint.
- `docs/superpowers/specs/2026-06-04-workers-stats-endpoint-design.md` - design spec, updated to record the revoked-everywhere reversal.
- `docs/superpowers/plans/2026-06-04-workers-stats-endpoint.md` - task-by-task implementation plan executed this session.

## Commit Range

d1650a7e4be21d8bfe600deb5c571e362bb1a377..5d758ae4037a79a2c9d8ee2fc71e8e23c4d46bf1
