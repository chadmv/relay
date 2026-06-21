---
date: 2026-06-21
topic: reenrollment-integration-test
branch: claude/sad-feistel-4bc73c
pr: "2026-06-21 / reenrollment-integration-test"
merge: "2026-06-21 / reenrollment-integration-test"
---

# Session Retro: 2026-06-21 - Through-the-stack re-enrollment integration test

**TL;DR:** Closed `idea-2026-06-05-reenrollment-integration-test`. Added one integration test that drives
a revoked worker back through the worker `Connect` enrollment-token revive path and asserts end-to-end
that `revoked_at` clears. The invariant was previously covered only at the store layer
(`TestSetWorkerAgentToken_ClearsRevokedAt`) and via the API revoked-list guard, not through the gRPC
register path. Autopilot batch item 5 (integration).

## What Was Built

- `internal/worker/handler_reenrollment_revive_test.go` (new, `//go:build integration`,
  `package worker_test`) - `TestConnect_EnrollmentTokenRevivesRevokedWorker`:
  1. Enrolls a fresh worker via the enrollment-token `Connect` path; captures the worker id.
  2. Revokes it via `ClearWorkerAgentToken` (the exact query the API revoke action in
     `agent_enrollments.go` calls), then asserts the precondition (`status='revoked'`, `revoked_at`
     non-null) so the test proves a real state transition.
  3. Re-enrolls the same hostname via a second enrollment token through `Connect` (the revive).
  4. Asserts `revoked_at` is NULL and status is no longer `revoked`, and that the same worker row is
     reused (`resp1.WorkerId == resp2.WorkerId`) with a fresh agent token issued.

## Key Decisions

- **Lives in `internal/worker` (`package worker_test`).** It needs to drive `worker.Handler.Connect`
  directly via the existing in-memory `mockConnectStream` and read worker state from the store; the
  `handler_auth_test.go` harness (`newWorkerTestFixture`, `seedEnrollment`, `mockConnectStream`) already
  provides all of that, so no API server or new gRPC server was needed and there is no import cycle. The
  revoked-state assertions read through `q.GetWorker` - the same store queries that back the API endpoint.
- **Second enrollment token created directly via the store.** `seedEnrollment` derives its raw token
  from `t.Name()`, so a second call would collide on the unique token hash; the revive token is created
  with a distinct raw value via `CreateAgentEnrollment`.

## Verification

- Local integration run (testcontainers Postgres) PASSES: `TestConnect_EnrollmentTokenRevivesRevokedWorker (2.40s)`.
- Proven non-vacuous: skipping the revive step makes both post-revive assertions FAIL
  (`revoked_at must be NULL ...`, `status must not remain 'revoked'`).
- `make vet-integration` (the CI gate) and `go build ./...` clean. CI runs `vet-integration` +
  race unit tests; it builds integration-tagged code but does not run the container test, so the local
  Docker run is the execution evidence.
