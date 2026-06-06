# Session Retro: 2026-06-05 - Surface Revoked Workers for Admin Audit

## What Was Built

A read-only, admin-only surface that lists revoked (decommissioned) workers and when they were revoked, across all three client surfaces. Closes the gap opened by the workers-stats work (PR #12), which excluded revoked workers from every operational view and left them visible nowhere.

- **Schema:** migration `000014` adds `revoked_at TIMESTAMPTZ NULL`. `ClearWorkerAgentToken` (the revoke path) stamps `NOW()`; `SetWorkerAgentToken` (the (re)enroll path) clears it - so a re-enrolled worker carries no stale stamp.
- **REST:** `GET /v1/workers/revoked`, admin-only, DESC-only (`revoked_at DESC NULLS LAST, id DESC`), reusing the standard cursor-pagination `page` envelope. `revoked_at` added to the worker JSON response.
- **CLI:** `relay workers list --revoked` (a flag on the existing `list` subcommand).
- **Web:** an "Active" / "Decommissioned" section toggle on the Workers page, with a read-only `RevokedWorkersTable` fed by an `enabled`-gated `useRevokedWorkers` hook.
- **Docs:** README endpoint + CLI rows; backlog item moved to `closed/`.

Built through the full superpowers flow: brainstorming -> spec -> plan -> subagent-driven development (fresh implementer + two-stage spec-then-quality review per task) -> final holistic review.

## Key Decisions

- **Audit-only scope.** No mutating action (re-enable / un-revoke / delete). Re-enrollment stays out-of-band as it was. Smallest safe slice; an action surface can be a later feature.
- **Dedicated endpoint over a `?status=revoked` filter.** The operational list hardcodes `status != 'revoked'` across 8 sort variants; inverting that contract via a flag would have been semantically muddy and high-churn. A dedicated handler keeps the operational dispatch untouched and matches the "one unit, one purpose" grain.
- **`SetWorkerAgentToken` is the revive point.** Revocation nulls the token, so a revoked worker can only return via the enrollment-token path, which calls `SetWorkerAgentToken`. Clearing `revoked_at` there (rather than in `finishRegister`'s shared status update) is the surgical, correct place.
- **DESC-only endpoint.** Supporting ascending would have required a second paginated query - the 8-arm complexity we deliberately avoid for a small audit list. The handler rejects any non-default sort with a 400 so the contract is honest.
- **CLI `--revoked` flag, not a `revoked` subcommand.** A `relay workers revoked` subcommand sits one keystroke from the existing `relay workers revoke` and invites typo-confusion.

## Problems Encountered

- **Latent ascending-sort bug caught in code-quality review.** The first API implementation listed `revoked_at` as a sort key, so `?sort=revoked_at` (ascending) was accepted but the fixed DESC query silently returned descending rows with a mismatched cursor. Fixed with a guard rejecting any sort but `-revoked_at`. A per-task quality reviewer found this; the spec-compliance pass had not, because the code matched the plan - the plan itself carried the latent inconsistency.
- **Test-fixture hostname collision.** `newTestWorker` derives the worker hostname from `t.Name()` and upserts by hostname, so two calls within one test resolved to a single row. The store-layer implementer caught it during its own run and wrapped the two calls in `t.Run` subtests (distinct `t.Name()`), confined to the test file.
- **Union widening broke an exhaustive switch.** Adding `'revoked'` to the web `WorkerStatus` union broke `liveness.ts`'s no-default switch (TS2366) - a file the plan's file list did not anticipate. The web implementer added the required `revoked` case (mirroring `disabled`) and flagged it; the spec reviewer confirmed it necessary and minimal.
- **Windows/sqlc CRLF churn, again.** `sqlc generate` rewrote line endings on ~10 unrelated `*.sql.go` files. Handled as in prior sessions: stage only the intended files, `git checkout --` the rest.

## Known Limitations

- See [`bug-2026-06-05-paginate-revoked-workers-list`](../backlog/bug-2026-06-05-paginate-revoked-workers-list.md) - Paginate revoked workers list (UI and client)
- See [`bug-2026-06-05-inconsistent-state-window-reenroll`](../backlog/bug-2026-06-05-inconsistent-state-window-reenroll.md) - Brief inconsistent-state window on worker re-enroll

## Open Questions

- See [`idea-2026-06-05-reenrollment-integration-test`](../backlog/idea-2026-06-05-reenrollment-integration-test.md) - Through-the-stack re-enrollment integration test

## Improvement Goals

- The two-stage per-task review earned its keep: the code-quality stage caught the ascending-sort bug that the spec stage (correctly) waved through because the code matched the plan. When a plan encodes a latent design inconsistency, only the quality lens catches it - worth continuing to run both.
- The plan's per-file task lists twice missed a consequential file (`liveness.ts` exhaustiveness; the sqlc CRLF set). Widening a shared type or running a generator has blast radius beyond the named files; the plan should call that out so implementers expect it.

## Files Most Touched

- `internal/store/query/workers.sql` - two query edits (stamp/clear) plus `CountRevokedWorkers` and `ListRevokedWorkersPage`.
- `internal/store/workers.sql.go` / `models.go` - sqlc-generated counterparts (`Worker.RevokedAt`, new query funcs).
- `internal/store/workers_revoked_test.go` - store-layer tests for stamp, clear, and revoked-only listing.
- `internal/api/workers.go` - `revoked_at` response field, `RevokedWorkersSortSpec`, `workersRowKeyByRevoked`, `handleListRevokedWorkers`, the DESC-only guard.
- `internal/api/server.go` - the admin-only route registration.
- `internal/api/workers_revoked_list_integration_test.go` - returns-only-revoked and admin-only coverage.
- `internal/cli/workers.go` - `--revoked` flag and the `REVOKED AT` table.
- `web/src/workers/WorkersPage.tsx` - the Active/Decommissioned section toggle.
- `web/src/workers/{RevokedWorkersTable.tsx,useRevokedWorkers.ts,api.ts,liveness.ts}` - the decommissioned view, hook, client, and the exhaustiveness case.
- `README.md` + `docs/superpowers/{specs,plans}/2026-06-05-*` - docs, design spec, implementation plan.

## Commit Range

c24f2b2..d980b42
