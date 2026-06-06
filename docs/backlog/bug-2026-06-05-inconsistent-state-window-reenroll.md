---
title: Brief inconsistent-state window on worker re-enroll
type: bug
status: open
created: 2026-06-05
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
