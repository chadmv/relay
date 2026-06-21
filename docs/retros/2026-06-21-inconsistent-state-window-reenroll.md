---
date: 2026-06-21
topic: inconsistent-state-window-reenroll
branch: claude/gifted-meninsky-5fc18a
pr: "2026-06-21 / inconsistent-state-window-reenroll"
merge: "2026-06-21 / inconsistent-state-window-reenroll"
---

# Session Retro: 2026-06-21 - Re-enroll inconsistent-state window

**TL;DR:** Closed `bug-2026-06-05-inconsistent-state-window-reenroll`. On the enroll-token
revive path, `SetWorkerAgentToken` cleared `revoked_at` but left `status='revoked'`; the status
only flipped to `online` post-commit in `RegisterWorkerConnection`. Between those, a worker was
`status='revoked'` with `revoked_at=NULL` - a revoked row with no revocation timestamp. Folded
the status reset into the same query. Autopilot iteration 2 of a `/autopilot 4` run.

## What Was Built

- `internal/store/query/workers.sql` - `SetWorkerAgentToken` now clears the revoked status
  atomically with `revoked_at`:
  `status = CASE WHEN status = 'revoked' THEN 'offline' ELSE status END`. Query-text-only; no
  new bind parameter, so the regenerated `workers.sql.go` and both handler call sites are
  unchanged.
- `internal/store/workers_revoked_test.go` - two integration tests: a RED-proven
  `TestSetWorkerAgentToken_RevivesRevokedStatus` (revoked -> offline, revoked_at null) and a
  regression guard `TestSetWorkerAgentToken_LeavesNonRevokedStatusUnchanged` (an `online` worker
  stays online - pins the `ELSE status` no-op branch).

## Key Decisions

- **Minimal query-text fix over a handler refactor.** The backlog item suggested "folding the
  status flip into the enroll transaction," which read as moving `RegisterWorkerConnection`
  into the enroll tx - a real refactor of the shared `finishRegister`. The simpler, lower-risk
  fix is to make the revoked-status clear ride along with the `revoked_at` clear that already
  happens in `SetWorkerAgentToken`. Same atomicity guarantee, one line, no caller change.
- **`CASE ... ELSE status` to stay surgical.** Setting `status='offline'` unconditionally would
  briefly flip a live worker offline on the rare autoEnroll-of-online path. The `CASE` touches
  only the revoked-revive case, leaving every other caller byte-identical - the property that
  the second (regression-guard) test now pins.
- **`'offline'` as the intermediate.** It is the natural not-yet-connected resting state, is in
  the `workers_status_check` vocabulary (migration 000019), and is overwritten by
  `RegisterWorkerConnection`'s `'online'` a moment later. If the connection registration fails,
  `offline` (token valid, revoked_at null) is a consistent resting state - strictly better than
  the prior `revoked`+null.

## Testing the transient

- The inconsistent state lives only between tx commit and the post-commit query, so it is not
  observable live. The fix and its property live at the store-query level, so the test asserts
  the deterministic post-condition of `SetWorkerAgentToken` directly (revoke -> reenroll ->
  assert not-revoked + null revoked_at). That is where RED is provable and stable.

## Backlog Triage

- No new items. Code review returned no high/medium findings; its single low note (the no-op
  branch was untested) was closed inline with the regression-guard test.

## Process Note

- Proportionate verification: a one-line constraint-validated SQL `CASE` with an
  already-RED-proven integration test got a single focused code-review pass rather than the full
  parallel relay-verify fan-out (the project's "combined review for trivial tasks" practice).
  The reviewer still caught the genuine gap - that the behavioral risk of the change (clobbering
  a live worker's status) had no assertion - which became the second test.
