---
date: 2026-06-20
topic: finishregister-gap-connection-epoch
branch: claude/strange-nobel-08bd7a
pr: 38
merge: 8001278
---

# Session Retro: 2026-06-20 - finishRegister Gap Connection-Epoch Fence

**TL;DR:** Closed `bug-2026-06-19-finishregister-gap-connection-epoch-race` by adding a DB-enforced `connection_epoch` column on `workers` (migration 000016), mirroring the task `assignment_epoch` invariant. The residual stale-teardown race - where a stale connection's teardown could pass the in-memory ownership gate during the `finishRegister` gap and then land its `markWorkerOffline` / `grace.Start` writes after the fresh connection went online - is now no-oped by a DB conditional UPDATE, not by Go ordering. Two process lessons stood out: an integration-tagged compile break stayed invisible to the unit `make test` until the integration build, and the first regression test passed for the wrong reason (the in-memory gate short-circuited before the DB fence was exercised).

## What Was Built

A stale, half-open gRPC stream's teardown can no longer flip a freshly reconnected worker offline or arm a grace timer that requeues tasks the live agent is actively running (duplicate execution). The fix enforces the CLAUDE.md "Identity-checked teardown" and "Epoch fence" invariants at the worker-connection level, where previously only in-memory pointer-identity (`UnregisterIf`) guarded the slot.

- **`connection_epoch` column on `workers`** (migration 000016) - the worker-connection analogue of `tasks.assignment_epoch`.
- **`RegisterWorkerConnection`** bumps the epoch and sets `status=online` atomically in one statement, `RETURNING` the new epoch. The per-connection `workerSender` records that epoch immutably for its lifetime.
- **`MarkWorkerOfflineIfEpoch` / `RequeueWorkerTasksIfEpoch`** fence the stale teardown's writes via conditional `UPDATE ... WHERE connection_epoch = $N`. Once a fresher connection has bumped the epoch, the stale writes match zero rows and no-op. Atomicity comes from the DB conditional-UPDATE rowcount, not from Go statement ordering.
- **Grace requeue fenced at FIRE time** - the grace timer carries the epoch it was armed under and the requeue no-ops if a newer connection has since registered.

Spec at `docs/superpowers/specs/2026-06-20-finishregister-gap-connection-epoch-design.md`, plan at `docs/superpowers/plans/2026-06-20-finishregister-gap-connection-epoch.md`.

## Key Decisions

- **DB-enforced fence over Go reordering.** The predecessor fix's gate (`UnregisterIf`) and its action (`markWorkerOffline` / grace) are not atomic with respect to a concurrent `finishRegister`, so simply moving `registry.Register` earlier could not close the window. The only durable fix is a conditional UPDATE whose rowcount decides whether the stale write lands - the same pattern the task `assignment_epoch` already uses. This keeps the invariant DB-enforced and consistent across server processes.
- **Stamped the epoch immutably on the per-connection sender.** Each connection owns exactly one epoch for its lifetime; teardown and grace fire against that captured value, never a re-read, so a stale connection can never accidentally observe (and act on) the fresh connection's epoch.

## Problems Encountered

- **An integration-tagged compile break was invisible to the unit `make test`.** Phase 4 verify caught a HIGH-severity break: `cmd/relay-server/startup_reconcile_test.go` still used the old grace `onExpire` signature `func(string)` after the callback gained an epoch parameter. Because that file is `//go:build integration`, the unit `make test` never compiled it, so the break did not surface until the integration build. When changing a callback signature, grep ALL callsites including integration-tagged test files, and run `go vet -tags integration ./...` (or compile the integration build) before declaring done - unit `make test` does not cover integration-tagged files.
- **The first regression test passed for the wrong reason.** The initial handler test went green not because the DB fence worked but because the in-memory ownership gate short-circuited before the DB fence was ever reached - so it certified nothing about the new code. The integration tester added two tests that genuinely fail against pre-fix code: a gap-path SQL fence test and a grace fire-time fence test. Reinforces the carried lesson that a "regression test" must fail against the unfixed code before it counts as proof.

## Improvement Goals

- **Run the integration build (or `go vet -tags integration ./...`) as part of "done" whenever a shared signature changes.** Unit `make test` silently skips `//go:build integration` files, so a callback-signature change can leave an integration-tagged callsite broken and green. Grep every callsite including tagged tests, and compile the integration build before declaring done. (Adjacent to the carried "verify platform-gated tests on a runnable platform" memory: tagged code that the default flow does not compile is a blind spot.)
- **A regression test must provably fail against the unfixed code, and for the right reason.** This recurs from the 2026-06-20 job-status-recompute retro and the 2026-06-19 pipe-drain retro, sharpened here: it is not enough for the test to go red against pre-fix code - confirm it exercises the new guard, not an unrelated earlier short-circuit (here, the in-memory gate). A test that the bug's own upstream check pre-empts certifies nothing about the fix.

## Files Most Touched

- `internal/store/migrations/000016_*.{up,down}.sql` - `connection_epoch` column on `workers`.
- `internal/store/query/workers.sql` - `RegisterWorkerConnection` (bump + online, RETURNING epoch), `MarkWorkerOfflineIfEpoch`, `RequeueWorkerTasksIfEpoch`.
- `internal/worker/handler.go` - threaded the epoch through `finishRegister`, teardown, and grace; epoch stamped on the per-connection sender.
- `cmd/relay-server/startup_reconcile_test.go` - updated to the new grace `onExpire` signature (the integration-tagged compile break).
- `docs/superpowers/specs/2026-06-20-finishregister-gap-connection-epoch-design.md`, `docs/superpowers/plans/2026-06-20-finishregister-gap-connection-epoch.md` - spec + plan.
- `docs/backlog/bug-2026-06-19-finishregister-gap-connection-epoch-race.md` - closed.
