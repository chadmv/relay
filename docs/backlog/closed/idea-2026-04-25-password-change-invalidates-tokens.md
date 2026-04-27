---
title: Should password change invalidate existing tokens?
type: idea
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-18 password-auth retro — Open Questions
---

# Should password change invalidate existing tokens?

## Summary
Should `PUT /v1/users/me/password` invalidate existing tokens after a password change?

## Resolution
Yes. `handleChangePassword` now calls `DeleteOtherTokensForUser` after a successful `SetPasswordHash`, revoking all sessions except the one that made the change (so the caller stays authenticated). If revocation fails the password change has already committed — the error is logged server-side and the handler returns 204 to avoid a retry trap (the user can recover with `relay logout --all`). Implemented in commit c34c33e.
