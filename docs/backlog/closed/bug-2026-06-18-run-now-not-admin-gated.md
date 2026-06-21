---
title: run-now on scheduled jobs is owner-or-admin, but README and the MCP tool treat it as admin-only
type: bug
status: closed
created: 2026-06-18
closed: 2026-06-20
priority: medium
source: 2026-06-18 /roadmap deep Gaps review agent, confirmed by direct code read
---

## Resolution
Resolved 2026-06-20 by adopting the owner-triggered contract (run-now is
**owner-or-admin**) and reconciling the docs to the code. The user chose this
branch of the proposal: an owner being able to fire their own schedule is
consistent with owners already being able to GET/PATCH/DELETE it via
`ownedScheduledJob`.

No handler change was needed - `handleRunScheduledJobNow`
(`internal/api/scheduled_jobs.go:632`) already authorizes via `ownedScheduledJob`
(owner-or-admin, 404 for non-owners). The CLI, web (`web/src/schedules/api.ts:45`
already said "owner or an admin"), and MCP `relay_run_schedule_now`
(`internal/mcp/run_now.go` - no inline admin gate, just calls the REST endpoint)
were likewise already owner-or-admin. The misclassification lived only in docs:

- **README** (4 spots) corrected from "admin only"/"admin-only" to "owner or
  admin": the schedules narrative (~:801), `relay schedules run-now` (~:886), the
  MCP tool table (~:1034), and the REST route table (~:1263).
- **MCP role-filtering backlog item**
  ([[bug-2026-05-09-mcp-admin-tools-role-filtering]]) updated to drop
  `relay_run_schedule_now` from its admin-only set; only `relay_list_reservations`
  remains in scope there.
- **Test** `TestRunScheduledJobNow_Authz`
  (`internal/api/scheduled_jobs_test.go`) pins the contract: non-admin owner 201,
  non-admin non-owner 404, admin non-owner 201.

# run-now on scheduled jobs is owner-or-admin, but README and the MCP tool treat it as admin-only

## Summary
`POST /v1/scheduled-jobs/{id}/run-now` is registered with `auth(...)` only and
`handleRunScheduledJobNow` authorizes via `ownedScheduledJob` (owner-or-admin), with no
`IsAdmin` check. So a non-admin who owns a schedule can trigger an explicit run, even
though the README documents run-now as admin-only and the MCP `relay_run_schedule_now`
tool is registered as admin-only. The implementation, the REST docs, and the MCP tool
contract disagree about who may run-now; they should be reconciled.

## Repro / Symptoms
- As a non-admin user who owns a scheduled job, `POST /v1/scheduled-jobs/{id}/run-now`
  returns 201 instead of 403.
- A non-admin, non-owner is already correctly rejected (404) by `ownedScheduledJob`
  (`internal/api/scheduled_jobs.go:163`), so the blast radius is bounded to one's own
  schedules - this is a contract/authorization inconsistency, not a cross-tenant escalation.

## Proposal
Pick the intended contract and make code, REST docs, and MCP agree:
- If run-now is admin-only (as README and the MCP tool currently imply): register the
  route as `auth(admin(...))` and/or add an `IsAdmin` check in the handler, mirroring the
  `AdminOnly` pattern used elsewhere.
- If owner-triggered run-now is intended (consistent with owners already being able to
  GET/PATCH/DELETE their own schedules through `ownedScheduledJob`): keep the handler as
  is and correct the README plus the MCP role-filtering assumption.
The handler comment at `scheduled_jobs.go:657-659` ("run-now submits the job as the
schedule owner, not the calling admin") suggests admin-triggered was the original mental
model, which favors the admin-only reading.

## Acceptance / Done When
- The owner-vs-admin rule for run-now is enforced identically on the REST route and the
  MCP tool, and the README matches it.
- A handler test pins the chosen behavior for the non-admin owner case.

## Related
- `internal/api/server.go:156` (route registered `auth(...)`)
- `internal/api/scheduled_jobs.go:632-675` (`handleRunScheduledJobNow`), `:148-168` (`ownedScheduledJob`)
- `internal/mcp/run_now.go` (admin-only tool that relies on the server to forbid non-admins)
- [[bug-2026-05-09-mcp-admin-tools-role-filtering]] - MCP side; its "server returns forbidden" fallback assumes this check exists
- [[bug-2026-06-10-job-cancel-missing-authz]] - related authorization-gap class on a different endpoint

## Notes
Surfaced by the 2026-06-18 `/roadmap deep` Gaps review agent. The agent rated it HIGH;
on verifying `ownedScheduledJob` enforces ownership for non-admins, the practical severity
is medium (bounded to own resources, primarily a docs/contract mismatch).
