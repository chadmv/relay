---
title: Paginate revoked workers list (UI and client)
type: bug
status: open
created: 2026-06-05
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
