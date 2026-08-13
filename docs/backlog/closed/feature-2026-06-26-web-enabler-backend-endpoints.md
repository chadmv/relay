---
title: "Web-enabler backend endpoints: invites list, sessions list, job retry"
type: feature
status: closed
created: 2026-06-26
closed: 2026-08-13
resolution: fixed
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

**Split 2026-08-13.** The 2026-08-13-web-enabler-list-endpoints slice shipped the first two bullets
below and scoped out the third, per this item's own Notes ("split later if one grows"). The retry
endpoint now has its own item, [[feature-2026-08-13-job-retry-endpoint]], which carries the entire
constraint block from the third bullet verbatim plus one addition. This item is **not** complete
until that split is accounted for by whoever closes it.

## Proposal
- **`GET /v1/invites`** (admin) - list invites with active / expiring / expired / redeemed state.
  Needs a `ListInvites` store query; `invites.sql` today has only Create / GetByTokenHash / MarkUsed.
  Unblocks the Admin Invites tab.
  **Shipped 2026-08-13** as an unfiltered, paginated list; the four states are derived client-side
  from `expires_at` and `used_at`, per the house rule that the server ships facts.
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
  **Shipped 2026-08-13** with two deviations from the text above, both improvements: the
  current-session flag is `row.ID == authUser.TokenID` (the id `BearerAuth` already resolved) rather
  than a re-hash, so the query never selects `token_hash` at all; and the list filters on
  `(expires_at IS NULL OR expires_at > NOW())`, because the column is nullable and NULL means
  "never expires", which this item did not record.
- **`POST /v1/jobs/{id}/retry`** (`?task=failed|all`) - operator re-run of a terminal job's failed or
  all tasks (per-task retry already exists agent-internally). Must bump `assignment_epoch` and null
  `worker_id` per the epoch-fence invariant. Reopening terminal jobs reactivates the jobs-stats bug.
  **NOT shipped. Split out 2026-08-13 into [[feature-2026-08-13-job-retry-endpoint]]**, which
  carries everything below plus a fifth constraint (it must not reopen a task whose dependents
  already ran). The text is retained here so this item's record stays accurate.

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
  **Two of three met 2026-08-13**; the third moved to [[feature-2026-08-13-job-retry-endpoint]].
- The retry path respects the epoch fence and is scheduled together with the jobs-stats-24h fix.
  **Carried to the split item.**
- The three consuming web items can drop their "backend-blocked" caveat.
  **Two can** ([[feature-2026-06-26-admin-console-pages]] list half,
  [[feature-2026-06-26-profile-identity-password-sessions]] list half);
  [[feature-2026-07-01-job-retry-action]] stays blocked on the split item.

## Related
- Unblocks [[feature-2026-06-26-admin-console-pages]] (invites), [[feature-2026-06-26-profile-identity-password-sessions]] (sessions), [[feature-2026-06-26-job-actions-submit-cancel-retry]] (retry)
- Retry ties to [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]]
- Retry endpoint split out 2026-08-13 into [[feature-2026-08-13-job-retry-endpoint]]
- Shipped work: `docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md`,
  `docs/superpowers/plans/2026-08-13-web-enabler-list-endpoints.md`,
  `docs/retros/2026-08-13-web-enabler-list-endpoints.md`
- Source: `internal/api/server.go` (115-119 jobs, 99-100 auth tokens, 139 invites), `internal/store/query/invites.sql`, `internal/api/auth.go:341-357`

## Notes
Three independent endpoints under one item for scheduling convenience; split later if one grows.
**That happened on 2026-08-13**: retry grew, and it is now
[[feature-2026-08-13-job-retry-endpoint]]. Whoever closes this item must state in the Resolution
that two of three endpoints shipped and that the third moved intact, or the split loses its record.

## Resolution
Closed 2026-08-13 (`2026-08-13-web-enabler-list-endpoints`) as **two of three endpoints shipped,
one split out intact**. This is not a partial close: the third endpoint moved to its own item with
its entire constraint block, which is what the Notes above sanctioned.

**Shipped:** `GET /v1/invites` (admin-only, paginated, unfiltered; the four states are derived
client-side from `expires_at` and `used_at`) and `GET /v1/auth/tokens` (self-service, scoped to
`authUser.ID` from context). Neither response can carry a token hash: the queries do not select the
column, so the generated row types have no field for it and returning one is a compile error rather
than a review miss.

**Two things this item had wrong, both improvements in the shipped work.** The current-session flag
is `row.ID == authUser.TokenID` - the id `BearerAuth` already resolved - not the re-hash this item
proposed, which is what removes the hash from the query entirely. And the list filters on
`(expires_at IS NULL OR expires_at > NOW())`, because `api_tokens.expires_at` is nullable and NULL
means *never expires*: the bare `expires_at > NOW()` this item implied would have hidden exactly the
most powerful credentials in the system from the one screen a user goes to in order to find them.

**Not shipped, moved intact:** `POST /v1/jobs/{id}/retry` is now
[[feature-2026-08-13-job-retry-endpoint]], carrying the full constraint block plus a fifth
constraint found while splitting (it must not reopen a task whose dependents already ran).
[[feature-2026-07-01-job-retry-action]] remains blocked on that item; the invites and sessions web
items can drop their backend-blocked caveat.

**One security fix landed that this item did not anticipate**, and its subject is the slice that has
not been written yet: `DeleteToken` was `DELETE FROM api_tokens WHERE id = $1`, unscoped by user.
Harmless at its single call site, but this item's own sessions list hands every user their session
UUIDs, so the obvious per-session-revoke implementation would have been an IDOR. Now scoped, with a
test that goes RED against the unscoped statement.

Also filed: [[bug-2026-08-13-cursor-value-kind-not-validated]],
[[bug-2026-08-13-token-expiry-two-clocks]], [[idea-2026-08-13-no-store-on-json-responses]],
[[idea-2026-08-13-reap-expired-invites-and-tokens]].
