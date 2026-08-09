---
title: No endpoint can promote or demote an existing user (is_admin is create-only)
type: feature
status: open
created: 2026-08-09
priority: medium
source: admin-console shell + Users tab slice (2026-08-09) - spec verification against internal/api/users.go
---

# No endpoint can promote or demote an existing user (is_admin is create-only)

## Summary
`is_admin` can only ever be set at `POST /v1/users`. `updateUserRequest` carries a single `Name`
field and no other handler mutates `is_admin`, so there is no way to promote a user to admin or
demote an admin once the account exists. The only workaround is to archive the account and create
a replacement under a different email, which loses the user's identity and their scheduled jobs.

## Context
Surfaced while specifying the admin console's Users tab (2026-08-09). The omnibus item
[[feature-2026-06-26-admin-console-pages]] described the rename endpoint as "rename/role", and the
Holo design's role pill implies a role-change control. Verification against the code showed the
capability does not exist, so the shipped Users tab deliberately exposes no role-change control
rather than a button that cannot work. This item is that omission's backend half.

## Proposal
Extend `PATCH /v1/users/{id}` to accept an optional `is_admin` boolean alongside `name`, keeping
both fields independently optional so a rename does not silently reset the role.

Guards, mirroring the ones `handleArchiveUser` already enforces:
- Refuse to demote the **last active admin** (400), the same check archive uses - otherwise the
  deployment can be locked out of every admin-gated route with a single request.
- Decide and document the self-demotion case. Archive refuses `cannot archive yourself`; the
  cheapest consistent answer is to refuse self-demotion too, since an admin demoting themselves
  is indistinguishable from a mistake and is unrecoverable without another admin.
- Admin-only, which the route already is.

Consider whether a role change should revoke the target's existing API tokens. Archive does
(`DeleteTokensForUser`), and a demotion that leaves live tokens behind means the demoted user
keeps admin reach until each token's own expiry - the tokens themselves carry no role claim
today, so verify how `AdminOnly` resolves the role per request before deciding this is a no-op.

## Acceptance / Done When
- An admin can promote a non-admin and demote an admin via `PATCH /v1/users/{id}`.
- A rename that omits `is_admin` leaves the role untouched, and vice versa.
- Demoting the last active admin is rejected with a clear 400, with a test proving the
  lockout is actually prevented (not merely that a 400 is returned for some other reason).
- The token-revocation question above is answered explicitly in the implementation or a comment.
- The admin Users tab gains the role-change control it currently omits.

## Related
- Blocks the role-change control omitted from [[feature-2026-06-26-admin-console-pages]]
- Source: `internal/api/users.go` (`updateUserRequest`, `handleUpdateUser`, `handleArchiveUser`
  for the last-active-admin guard to mirror), `internal/store/query/users.sql`
- Adjacent: [[feature-2026-06-26-audit-log-admin-console-actions]] - a privilege change is
  exactly the kind of action that audit trail should record.

## Notes
This is a genuine authorization-model gap, not UI polish: a deployment whose sole admin leaves
the organization currently has no in-product way to hand over administrative access.
