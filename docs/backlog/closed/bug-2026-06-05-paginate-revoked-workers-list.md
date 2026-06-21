---
title: Paginate revoked workers list (UI and client)
type: bug
status: closed
created: 2026-06-05
closed: 2026-06-21
resolution: fixed
priority: low
source: noticed during the surface-revoked-workers retro (2026-06-05)
---

# Paginate revoked workers list (UI and client)

## Summary
The revoked list and `useRevokedWorkers` hook fetch only the first page (`limit=50`); the `GET /v1/workers/revoked` endpoint is fully paginated but there is no pagination UI. Fine under the current "small revoked set" assumption, but worth addressing if fleets accumulate many decommissioned workers.

## Related
- `web/src/workers/useRevokedWorkers.ts`
- `web/src/workers/RevokedWorkersTable.tsx`
- `web/src/workers/WorkersPage.tsx`
- `docs/retros/2026-06-05-surface-revoked-workers.md`

## Resolution
fixed (2026-06-21). Added cursor pagination to the revoked-workers list, mirroring JobsPage: `listRevokedWorkers`/`useRevokedWorkers` now accept a cursor and key on `['workers', 'revoked', cursor]`; WorkersPage holds a separate revoked cursor/stack/offsets state and renders an `X-Y of total` footer (plain hyphen) via the shared `computePageRange`. Both prev/next buttons gate on `isPlaceholderData` so a double-next during an in-flight fetch cannot desync the stacks (PR #65 invariant). The `computePageRange` helper was promoted from `web/src/jobs/pageRange.ts` to `web/src/lib/pageRange.ts` (now shared by jobs and workers); the old path re-exports for compatibility. Backend field names (`items`/`next_cursor`/`total`) were verified against `handleListRevokedWorkers` + `page[T]`. Code review (offset-stack correctness, in-flight guard on both buttons, no `['workers']` broad-invalidation collision, pageRange move did not regress JobsPage, no en/em dash) returned no high/medium findings. New hook + WorkersPage tests, full web suite 169 green, `tsc` clean.
