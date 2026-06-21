---
date: 2026-06-20
topic: run-now-owner-or-admin
branch: claude/vigilant-dhawan-6b43bc
pr: 2026-06-20 / run-now-not-admin-gated
---

# Session Retro: 2026-06-20 - run-now is owner-or-admin

**TL;DR:** Closed `bug-2026-06-18-run-now-not-admin-gated`, a docs/contract
mismatch (not a security hole). `POST /v1/scheduled-jobs/{id}/run-now` has always
authorized via `ownedScheduledJob` (owner-or-admin, 404 for non-owners), but the
README and one backlog item described it as admin-only. The user chose the
owner-triggered contract, so the fix was docs-only: reconcile four README spots
and the MCP role-filtering item to the code, and add a test that pins the
contract. No production code changed.

## What Was Built

- **Test** (`internal/api/scheduled_jobs_test.go`, `TestRunScheduledJobNow_Authz`)
  - pins the chosen contract end to end: non-admin owner 201, non-admin non-owner
  404 (enumeration-safe via `ownedScheduledJob`), admin non-owner 201. The
  pre-existing `TestRunScheduledJobNow_CreatesJob` already exercised the non-admin
  owner path; the new test makes the full owner-or-admin rule explicit.
- **README** - four "admin only"/"admin-only" run-now claims corrected to "owner
  or admin": the schedules narrative, `relay schedules run-now`, the MCP tool
  table, and the REST route table. The MCP write-tools table now correctly groups
  `relay_run_schedule_now` under "any logged-in user".
- **MCP role-filtering backlog item**
  (`bug-2026-05-09-mcp-admin-tools-role-filtering`) - dropped
  `relay_run_schedule_now` from its admin-only set; only `relay_list_reservations`
  remains in scope there.

## Key Decisions

- **Owner-triggered, not admin-only.** The user resolved the proposal's open
  decision in favor of owners being able to fire their own schedules - consistent
  with owners already being able to GET/PATCH/DELETE the schedule through
  `ownedScheduledJob`. This made the handler correct as-is and turned the bug into
  a pure docs reconciliation.
- **No handler change.** `handleRunScheduledJobNow` already gated on
  `ownedScheduledJob`. The handler comment ("run-now submits the job as the
  schedule owner, not the calling admin") had hinted at an admin-triggered mental
  model, but the actual authorization was already owner-or-admin, and the CLI, web
  (`web/src/schedules/api.ts` already said "owner or an admin"), and MCP
  (`run_now.go` has no inline admin gate) all matched. Only the prose was stale.
- **Test asserts the deny, not just the allow.** The existing test only proved an
  owner could run-now. The new test adds the non-owner 404 and the admin-any 201
  so the contract is pinned on both sides, mirroring the job-cancel-authz test
  trio from the prior session.

## Files Most Touched

- `internal/api/scheduled_jobs_test.go` - `TestRunScheduledJobNow_Authz`.
- `README.md` - four run-now authorization descriptions.
- `docs/backlog/bug-2026-05-09-mcp-admin-tools-role-filtering.md` - scope reduced
  to `relay_list_reservations`.
- `docs/backlog/closed/bug-2026-06-18-run-now-not-admin-gated.md` - closed with
  resolution.
- `ROADMAP.md` - removed the closed item from Now and the api-auth list; added a
  "What moved" line.
