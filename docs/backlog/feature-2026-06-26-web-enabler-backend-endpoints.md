---
title: "Web-enabler backend endpoints: invites list, sessions list, job retry"
type: feature
status: open
created: 2026-06-26
priority: medium
source: ROADMAP deep-refresh gaps pass (2026-06-26)
---

# Web-enabler backend endpoints: invites list, sessions list, job retry

## Summary
Three small backend endpoints are each the lone backend dependency of a high-value web item but have
no standalone backend tracking. Grouped as one combined item so the backend half can be scheduled
ahead of the UIs.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps pass; consolidated into a single combined item (per
the user's choice) rather than three. Each endpoint is also noted inline in its consuming web item.

## Proposal
- **`GET /v1/invites`** (admin) - list invites with active / expiring / expired / redeemed state.
  Needs a `ListInvites` store query; `invites.sql` today has only Create / GetByTokenHash / MarkUsed.
  Unblocks the Admin Invites tab.
- **`GET /v1/auth/tokens`** - list the caller's active sessions WITHOUT leaking the token hash.
  Unblocks the Profile Sessions tab (`DELETE /v1/auth/tokens` already exists).
  **Corrected 2026-08-13 (2026-08-12-profile-pages):** this bullet previously said the work is
  "a `ListTokensForUser` query" and listed `last_used_at` among the columns. That understates it.
  `api_tokens` has exactly five columns - `id`, `user_id`, `token_hash`, `created_at`, `expires_at`
  (`internal/store/migrations/000001_initial.up.sql:13-19`) - so there is **no** `last_used_at`, no
  user-agent and no IP anywhere on the row. A useful session list is therefore a **migration plus a
  write path plus a query**, not a query: something has to stamp last-use on every authenticated
  request, which is a hot-path write on `BearerAuth` and needs its own throttling decision. Scope
  the honest minimum first - `created_at`, `expires_at`, and a current-session flag derived by
  comparing `tokenhash.Hash` of the caller's own bearer token - and treat last-active as a separate,
  larger item. The shipped Sessions tab states this omission on the page, so a future implementer
  reading only that tab does not have to rediscover it.
- **`POST /v1/jobs/{id}/retry`** (`?task=failed|all`) - operator re-run of a terminal job's failed or
  all tasks (per-task retry already exists agent-internally). Must bump `assignment_epoch` and null
  `worker_id` per the epoch-fence invariant. Reopening terminal jobs reactivates the jobs-stats bug.

  **It must NOT call `IncrementTaskRetryCount`** (constraint added 2026-08-12, when
  `bug-2026-06-26-retry-resurrects-cancelled-task` was fixed). That statement now fences on
  `assignment_epoch`, `worker_id` and `status IN ('pending','dispatched','running')`, which are the
  exact inverse of this endpoint's preconditions: it reopens tasks that ARE terminal and has no
  worker identity to supply, so the status and worker predicates would reject every call. That is
  the correct outcome, not an obstacle - the two operations were only ever conflatable because
  neither had a stated precondition. This endpoint needs **its own statement**: an explicit
  `status IN ('failed','timed_out')` allow-list (widened for `?task=all`), setting `status='pending'`,
  nulling `worker_id`, clearing `started_at`/`finished_at`, and bumping `assignment_epoch` - the
  operator analogue of `RequeueTaskByID`, not of `IncrementTaskRetryCount`. It must also decide
  explicitly what happens to `retry_count` (leaving it exhausted gives the task zero agent-side
  retries on the new generation), and what happens to a `cancelled` job's status, since
  `RecomputeJobStatus` is `cancelled`-blind. See the query comment on `IncrementTaskRetryCount` in
  `internal/store/query/tasks.sql` and
  `docs/superpowers/specs/2026-08-12-retry-resurrect-status-guard.md` section 11.

## Acceptance / Done When
- All three endpoints exist with auth gating, tests, and response shapes that never leak token hashes.
- The retry path respects the epoch fence and is scheduled together with the jobs-stats-24h fix.
- The three consuming web items can drop their "backend-blocked" caveat.

## Related
- Unblocks [[feature-2026-06-26-admin-console-pages]] (invites), [[feature-2026-06-26-profile-identity-password-sessions]] (sessions), [[feature-2026-06-26-job-actions-submit-cancel-retry]] (retry)
- Retry ties to [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]]
- Source: `internal/api/server.go` (115-119 jobs, 99-100 auth tokens, 139 invites), `internal/store/query/invites.sql`, `internal/api/auth.go:341-357`

## Notes
Three independent endpoints under one item for scheduling convenience; split later if one grows.
