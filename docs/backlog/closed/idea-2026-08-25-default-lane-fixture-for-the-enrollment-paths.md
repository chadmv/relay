---
title: The enrollment paths are fakeable now but still have no default-lane test
type: idea
status: closed
created: 2026-08-25
closed: 2026-08-25
resolution: fixed
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

## Resolution

Fixed. `internal/worker/handler_enroll_guards_test.go` drives both `enrollAndRegister` and
`autoEnrollAndRegister` to success and to every refusal branch in the default lane, with no Postgres
and no build tag.

Four blockers this item did not state had to be closed first, all found by the spec:
`strandDB.QueryRow` could not express `pgx.ErrNoRows`; `fakeTx` had no `QueryRow` at all;
`fakeTx.Exec` returned zero rows so `errEnrollmentNotConsumable` fired by default; and
`strandWorkerRow` made every enrollment token look consumed and expired.

One criterion is met differently than written: `errWorkerRevoked` no longer exists, so its named
branch cannot be asserted - the behaviour is, by the create-only refusal that replaced it.

The token scrub PR #149 shipped is now load-bearing rather than speculative. These are the paths that
mint a real `rawAgentToken`, and `tokensSent() == 1` is asserted positively so the test cannot pass
against a build that never minted one.

Review found the fixture initially could not distinguish a POOL statement from a TRANSACTION
statement, so three mutations hoisting each guard out of its transaction survived. Closed with an
owner tag supplied by whichever fake receives the call.

See `docs/retros/2026-08-25-auto-enroll-guards.md`.
