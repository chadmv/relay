---
title: Relay cannot delete a worker at any layer, and the auto-enroll guards made that a terminal state
type: bug
status: open
created: 2026-08-25
priority: high
source: 2026-08-25 auto-enroll-guards slice - the remediation prescribed `relay workers delete`, which does not exist
---

# Relay cannot delete a worker at any layer, and the auto-enroll guards made that a terminal state

## Summary

There is no way to delete a `workers` row. No CLI subcommand, no HTTP route, no query. Before the
2026-08-25 auto-enroll guards that was a gap; now it is a **terminal state** for one real operator
case.

Auto-enroll refuses any hostname that already has a row, whatever its status. Revoking does not
delete the row, so the hostname stays claimed forever. That leaves a machine re-provisioned in place,
under auto-enroll, whose operator will not or cannot have an enrollment token issued, with **no
remedy at all** on the token-less path.

## Repro / Symptoms

Verified at all three layers on 2026-08-25:

- `internal/cli/workers.go:33` - usage is `<list|get|disable|enable|revoke|workspaces|evict-workspace>`;
  the switch has no `delete` arm, so `relay workers delete` returns `unknown workers subcommand: delete`.
- `grep -rn "DELETE FROM workers" internal/store/query/` returns nothing.
- `internal/api/server.go` - the only DELETE on the resource is `/v1/workers/{id}/token`, which is
  revoke. There is no `handleDeleteWorker`.

The slice's own remediation shipped `relay workers delete` into README twice and into the agent's
terminal exit message, and pinned it with a test, before review caught it. Those sites are corrected;
this item is the real gap they were reaching for.

## Context

The escape hatches that DO exist, so this item is not overstated:

- Revoke, then re-enroll with an admin-issued enrollment token. This works and is now what the docs
  say - `ClearWorkerAgentToken` nulls the hash, so `enrollAndRegister`'s guard admits the row and
  `SetWorkerAgentToken` revives it with history intact.
- Rename the host. README:369 already documents that a renamed host rejoins as a new worker.

Neither helps the case above, and the second is not a real answer for a fleet with naming conventions.

## Proposal

**The FK work is the content of this item, not the CLI arm.** A naive `DELETE FROM workers` fails or
does the wrong thing on four relations, each needing its own decision:

- `agent_enrollments.consumed_by` (`000005_agent_auth.up.sql:9`) has **no `ON DELETE` action at all**,
  so the delete fails outright with an FK violation for every worker that ever consumed an enrollment
  token - i.e. every token-enrolled worker. This is the blocker; decide between `SET NULL` (keeps the
  audit trail, loses the link) and refusing to delete such workers.
- `tasks.worker_id` is `ON DELETE SET NULL` (`000001_initial.up.sql:62`), so a running task is
  **orphaned in place** - status `running`, `worker_id` NULL - not cleaned up. Decide whether delete
  refuses while tasks are live, or requeues them.
- `reservations.worker_ids` is a bare `UUID[]` with no FK (`000001_initial.up.sql:89`), so stale ids
  linger silently and the dispatcher keeps matching against them.
- `worker_workspaces.worker_id` is `ON DELETE CASCADE`, which is fine.

Then: an admin-only route, a CLI arm, and README's recovery prose pointing at it.

## Acceptance / Done When

- An admin can delete a worker row, and the hostname is immediately re-usable by token-less
  auto-enroll, proven by a test that is RED today.
- Each of the four relations above has a stated, tested decision. The `agent_enrollments` FK
  specifically must not make the delete fail for a token-enrolled worker.
- README's auto-enrollment recovery section names the real command, and the agent's exit message
  does too. The ghost-command guard in `internal/agent/messages_test.go` must go green *because the
  command exists*, not by removing the check.
- The operator loop is terminating for the re-provisioned-in-place case.

## Related

- `internal/cli/workers.go`, `internal/api/server.go`, `internal/store/query/workers.sql`
- `internal/store/migrations/000001_initial.up.sql`, `000005_agent_auth.up.sql`
- [[bug-2026-08-12-auto-enroll-hostname-takeover]] (closed) - the guard that made this terminal
- [[idea-2026-08-25-ttl-reaper-for-never-reconnected-workers]] - the other half of reclaiming rows
- `docs/retros/2026-08-25-auto-enroll-guards.md`
