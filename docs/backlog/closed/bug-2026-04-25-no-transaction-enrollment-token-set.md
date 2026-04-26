---
title: No transaction wrapping enrollment + token set
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-22 major-concurrency-fixes retro — Known Limitations
---

# No transaction wrapping enrollment + token set

## Summary
**No transaction wrapping enrollment + token set**: `UpsertWorkerByHostname`, `ConsumeAgentEnrollment`, and `SetWorkerAgentToken` are three separate DB calls. A crash between consume and set-token would leave the enrollment consumed but no token written. The agent would be stuck until an admin issues a new enrollment. A future improvement is to wrap these in a single transaction.

## Resolution
Wrapped UpsertWorkerByHostname, ConsumeAgentEnrollment, and SetWorkerAgentToken in a single pgx.BeginTxFunc transaction in enrollAndRegister (internal/worker/handler.go). A crash or failure on any step now rolls back all three, leaving the enrollment token reusable. Integration tests in handler_atomic_test.go verify atomicity via a forced UNIQUE-constraint collision on agent_token_hash.
