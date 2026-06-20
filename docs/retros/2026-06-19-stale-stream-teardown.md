---
date: 2026-06-19
topic: stale-stream-teardown
branch: claude/festive-leavitt-238ce5
range: a26ec0a..74afa91
---

# Session Retro: 2026-06-19 - Stale Stream Teardown

**TL;DR:** Closed `bug-2026-06-10-stale-stream-teardown-clobbers-registration` by
replacing `Registry.Unregister` with an identity-checked `UnregisterIf` and
gating `Connect`'s teardown on registry ownership; shipped via the full
agent-team flow (brainstorm spec -> planner -> one backend engineer -> relay-verify),
and filed a follow-up backlog item for a residual finishRegister-gap race the
verification surfaced.

## What Was Built

A stale, half-open gRPC stream's teardown can no longer clobber a freshly
reconnected worker. The fix enforces the CLAUDE.md "Identity-checked teardown"
invariant.

- **`Registry.UnregisterIf(workerID, sender) bool`** replaces `Unregister`:
  deletes the slot only when the registered sender is pointer-identical to the
  caller's. The sole production caller was the code being changed, so the old
  method was removed rather than left as dead code.
- **`Handler.teardownConnection(workerID, sender)`** extracts `Connect`'s four
  unconditional defers into one method that *always* closes its own send
  goroutine but only runs `markWorkerOffline` + grace/requeue when `UnregisterIf`
  returns true (this connection still owns the slot).
- **Tests:** fast DB-free registry unit tests (`UnregisterIf` + replace-then-stale)
  and an integration regression test that drives `teardownConnection` with a
  stale sender after a fresh one replaced it, asserting the live worker stays
  online and its task keeps its `assignment_epoch`. A temporary-revert guard
  confirmed the test fails when the gate is removed.
- The proposal's keepalive bullet was already implemented (main.go:177-182), so
  it was excluded from scope.

## Key Decisions

- **Verified the backlog proposal against current code before scoping.** The
  proposal's third bullet (configure gRPC keepalive) was already done; brainstorm
  narrowed scope to just the teardown + one structural extraction
  (`teardownConnection`) that earned its keep by making the gate unit-testable.
- **One implementer for the whole plan, not subagent-per-task.** Tightly-coupled
  single-package work where later tasks depend on earlier symbols; matched the
  prior coupled-SQL session's approach, with one consolidated relay-verify pass.
- **Backlogged the residual race rather than scope-creeping.** relay-verify found
  that `finishRegister` writes online + cancels grace *before* `registry.Register`,
  so a stale teardown landing in that gap can still clobber. Confirmed it is
  pre-existing and narrower than the fixed bug; the complete fix is a worker
  connection-epoch fence (its own migration/spec). Shipped the strict improvement
  and filed the follow-up.
- **Declined the low-severity review findings with reasons.** Pointer-equality in
  `UnregisterIf` is latent-only (registry stores only `*workerSender`, and
  constraining the type would break `fakeSender` unit tests); the "only tested
  under integration" finding was already moot (registry-level unit tests are
  untagged).

## Known Limitations

- See [`bug-2026-06-19-finishregister-gap-connection-epoch-race`](../backlog/bug-2026-06-19-finishregister-gap-connection-epoch-race.md) - the ownership gate's check and action are not atomic w.r.t. a concurrent `finishRegister`; a worker connection-epoch fence is the proper fix.

## Improvement Goals

- **Combined single review for trivial/no-logic tasks** (carried 3+ retros,
  already promoted to [[feedback-combined-review-trivial-tasks]]). Applied: the
  doc/backlog tasks rode in the single implementer dispatch under one
  relay-verify pass.
- **Expect sqlc/`make generate` line-ending churn** (promoted to CLAUDE.md last
  session). N/A here - no `.sql`/`.proto` changes.
- **Scope a retro to the branch merge-base when the chain forks** (new last
  session). Applied: scoped to `a26ec0a..HEAD` (this branch's fork point from the
  PR #30 merge) instead of chaining off the prior retro's feature-branch SHA.
- **Match commit here-string syntax to the tool's shell** (applied last session).
  Applied again: bash heredocs throughout.
- **Treat a backlog proposal as a starting point, not a contract - verify it
  against current code before scoping** (promoted to [[feedback-backlog-proposal-not-contract]]).
  Recurred from last session (there: one of five queries was dead/duplicate;
  here: the keepalive bullet was already done).

## Files Most Touched

- `internal/worker/handler.go` - extracted `teardownConnection`; replaced the
  `Connect` defer block with the ownership-gated teardown.
- `internal/worker/registry.go` - `Unregister` -> identity-checked `UnregisterIf`.
- `internal/worker/registry_test.go` - replaced the unregister test; added the
  replace-then-stale unit test.
- `internal/worker/handler_teardown_test.go` - new integration regression for the gate.
- `internal/worker/export_test.go` - test shims (`RegisteredSenderForTest`,
  `TeardownConnectionForTest`, `SendToWorkerForTest`, `UUIDStringForTest`).
- `docs/superpowers/specs/2026-06-19-stale-stream-teardown-design.md`,
  `docs/superpowers/plans/2026-06-19-stale-stream-teardown.md` - spec + plan.
- `docs/backlog/closed/...stale-stream-teardown-clobbers-registration.md` - closed.
- `docs/backlog/bug-2026-06-19-finishregister-gap-connection-epoch-race.md` - filed.
