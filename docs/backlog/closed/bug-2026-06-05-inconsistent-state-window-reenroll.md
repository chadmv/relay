---
title: Brief inconsistent-state window on worker re-enroll
type: bug
status: closed
created: 2026-06-05
closed: 2026-06-21
resolution: fixed
priority: low
source: noticed during the surface-revoked-workers retro (2026-06-05)
---

# Brief inconsistent-state window on worker re-enroll

## Summary
On re-enroll, `SetWorkerAgentToken` clears `revoked_at` inside the enroll transaction, but `status` flips to `online` afterward in `finishRegister` (a separate post-commit query). In that window a worker is `status='revoked'` with `revoked_at=NULL`, so it would momentarily appear in the revoked list with an "unknown" timestamp.

## Notes
Transient and cosmetic, mirroring the existing post-transaction status-update pattern. Would only be worth fixing by folding the status flip into the enroll transaction.

## Related
- `internal/worker/handler.go` (`enrollAndRegister`, `finishRegister`)
- `docs/retros/2026-06-05-surface-revoked-workers.md`

## Resolution
Fixed 2026-06-21 (inconsistent-state-window-reenroll). The `SetWorkerAgentToken` store query
(`internal/store/query/workers.sql`) now clears the revoked status atomically with `revoked_at`
via `status = CASE WHEN status = 'revoked' THEN 'offline' ELSE status END`, so a re-enrolling
worker never sits in the `status='revoked'` + `revoked_at=NULL` window. `'offline'` is the
natural not-yet-connected resting state and is constraint-legal (migration 000019
`workers_status_check` vocabulary); `RegisterWorkerConnection` flips it to `'online'` a moment
later post-commit. The `CASE`'s `ELSE status` branch leaves every non-revoked caller
(new-worker enroll, autoEnroll of a live worker) byte-identical - a query-text-only change, no
new bind parameter, no handler change. Covered by two integration tests: a RED-proven
`TestSetWorkerAgentToken_RevivesRevokedStatus` (revoked->offline) and a regression guard
`TestSetWorkerAgentToken_LeavesNonRevokedStatusUnchanged` (an `online` worker stays online).
Code review returned no high/medium findings; the one low test-coverage note (the no-op branch)
was closed with the second test.
