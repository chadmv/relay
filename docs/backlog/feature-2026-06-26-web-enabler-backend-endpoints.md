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
- **`GET /v1/auth/tokens`** - list the caller's active sessions (created_at, last_used_at,
  current-session flag) WITHOUT leaking the token hash. Needs a `ListTokensForUser` query. Unblocks
  the Profile Sessions tab (`DELETE /v1/auth/tokens` already exists).
- **`POST /v1/jobs/{id}/retry`** (`?task=failed|all`) - operator re-run of a terminal job's failed or
  all tasks (per-task retry already exists agent-internally). Must bump `assignment_epoch` and null
  `worker_id` per the epoch-fence invariant. Reopening terminal jobs reactivates the jobs-stats bug.

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
