---
title: "Should there be a `DELETE /v1/auth/token` (logout) endpoint?"
type: idea
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-18 password-auth retro — Open Questions
---

# Should there be a `DELETE /v1/auth/token` (logout) endpoint?

## Summary
Should there be a `DELETE /v1/auth/token` (logout) endpoint?

## Resolution
Yes. Implemented `DELETE /v1/auth/token` (revoke current session) and `DELETE /v1/auth/tokens` (revoke all sessions for the authenticated user), both returning 204. CLI: `relay logout` and `relay logout --all`. On success the CLI clears the saved token from config so the user is not left with a dead credential. Implemented in commit 403308a (CLI) wired in 115c22f; API routes landed in earlier tasks.
