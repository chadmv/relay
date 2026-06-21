---
title: Through-the-stack re-enrollment integration test
type: idea
status: closed
created: 2026-06-05
closed: 2026-06-21
resolution: fixed
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

## Resolution
Fixed 2026-06-21 (autopilot batch, item reenrollment-integration-test). Added
`TestConnect_EnrollmentTokenRevivesRevokedWorker` in `internal/worker/handler_reenrollment_revive_test.go`
(`//go:build integration`, `package worker_test`). It drives `worker.Handler.Connect` via the existing
`mockConnectStream` harness: enroll a worker, revoke it with `ClearWorkerAgentToken` (the exact query the
API revoke action calls) while asserting the `status='revoked'` + non-null `revoked_at` precondition,
then re-enroll the same hostname through a second enrollment token and assert `revoked_at` is cleared,
status is no longer `revoked`, the same worker row is reused, and a fresh agent token is issued. Closes
the gap between the store-layer `TestSetWorkerAgentToken_ClearsRevokedAt` and the gRPC register path.
Local testcontainers run PASSES (2.40s); proven non-vacuous (skipping the revive fails both post-revive
assertions); `make vet-integration` and `go build ./...` clean.
