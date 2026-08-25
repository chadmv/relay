---
title: The enrollment paths are fakeable now but still have no default-lane test
type: idea
status: open
created: 2026-08-25
priority: medium
source: 2026-08-25 handler-pool-seam slice - the seam covers all three BeginTxFunc sites, but only applyInventory's was spent
---

# The enrollment paths are fakeable now but still have no default-lane test

## Summary

Narrowing `Handler.pool` to `txBeginner` made **all three** of its transaction sites drivable without
Postgres, not just the one the seam was filed for. `applyInventory`'s site was spent immediately;
`enrollAndRegister`'s and `autoEnrollAndRegister`'s were not. Both hold branch logic whose only
witnesses are `//go:build integration`, which CI does not run.

## Context

The 2026-08-25 slice's spec recorded this as finding F2 and scoped it out deliberately: one interface
covers `handler.go`'s three `pgx.BeginTxFunc(ctx, h.pool, ...)` call sites, because they differ only
in the closure they pass. The slice built the whole default-lane fixture family - `fakePool`,
`fakeTx`, `emptyRows`, `scriptedStream`'s recorder, `newSuccessFixture` - and pointed it at the
reconnect path only.

So the remaining cost is a fixture *variation*, not a fixture. That is the cheapest this will ever
be, and it gets more expensive the further the fixture drifts from the enrollment paths' needs.

## Proposal

Point `newSuccessFixture` (or a sibling) at the two enrollment callers and cover the branches that
have no default-lane witness today:

- `errEnrollmentNotConsumable` - the enrollment token is expired, already consumed, or missing.
- `errWorkerRevoked` - a revoked worker attempting auto-enroll.
- The auto-enroll audit log line, whose own comment argues at length about its forgeability and which
  is the only record anywhere that a token-less enrollment happened.

## Acceptance / Done When

- A default-lane test drives `enrollAndRegister` to a successful return, and one drives
  `autoEnrollAndRegister`.
- The three branches above are asserted somewhere CI executes.
- `TestScriptedStream_DoesNotRetainARawAgentToken` stops being speculative: these are the paths that
  mint a real `rawAgentToken`, so the token scrub becomes load-bearing rather than precautionary.
  Confirm the scrub actually fires here (`agentTokensSent` is the counter for it).

## Related

- `internal/worker/handler.go` - `enrollAndRegister`, `autoEnrollAndRegister`, `txBeginner`
- `internal/worker/handler_register_success_test.go` - the fixture family to extend
- `docs/superpowers/specs/2026-08-25-handler-pool-seam.md` - finding F2 and section 12.1
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the lane problem this reduces further
- [[idea-2026-08-24-handler-pool-has-no-seam]] - closed; the seam this spends
