---
title: Through-the-stack re-enrollment integration test
type: idea
status: open
created: 2026-06-05
source: noticed during the surface-revoked-workers retro (2026-06-05)
---

# Through-the-stack re-enrollment integration test

## Summary
Add an API-level integration test that drives the worker `Connect` enrollment-token revive path and then asserts the worker has left `GET /v1/workers/revoked` and carries a cleared `revoked_at`. The invariant is currently covered at the store layer (`TestSetWorkerAgentToken_ClearsRevokedAt`) plus the `TestListWorkers_ExcludesRevoked` regression guard, but not end-to-end through the gRPC register path.

## Proposal
Drive a revoked worker back through the enrollment-token `Connect` path and assert it leaves the revoked list with `revoked_at` cleared. Would lock the cross-layer revive invariant against future regressions.

## Related
- `internal/worker/handler.go`
- `internal/api/workers.go`
- `docs/retros/2026-06-05-surface-revoked-workers.md`
