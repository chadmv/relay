---
title: handleAdminPasswordReset and handleChangePassword are not transactional
type: bug
status: open
created: 2026-04-26
source: 2026-04-26 token-lifecycle-auth retro — Known Limitations
---

# handleAdminPasswordReset and handleChangePassword are not transactional

## Summary
If `SetPasswordHash` succeeds but `DeleteTokensForUser`/`DeleteOtherTokensForUser` fails on a DB blip, the password is changed with old sessions still alive until the next call. Wrapping both in a `pool.Begin`/`tx.Commit` would close the window.

## Context
Came up during the 2026-04-26 token-lifecycle implementation. The graceful-degradation choice (log + return 204 for self-service, log + return 500 for admin reset) was intentional to avoid a retry trap, but it leaves a partial-state window. The consequence is small for self-service — the user can recover via `relay logout --all`. More significant for admin reset, where the admin's security guarantee is "all sessions killed."

## Proposal
Wrap the `SetPasswordHash` + `DeleteTokens*` calls in a single transaction using `q.WithTx(tx)`. Both queries operate on different tables (`users` and `api_tokens`) but share the same pool, so a transaction is straightforward.

## Acceptance / Done When
- `handleChangePassword` runs `SetPasswordHash` + `DeleteOtherTokensForUser` in a single transaction
- `handleAdminPasswordReset` runs `SetPasswordHash` + `DeleteTokensForUser` in a single transaction
- If either step fails, the transaction rolls back and the handler returns 500 with no partial state
- Integration tests verify the atomic behavior

## Related
- `internal/api/auth.go` — both handlers
- `internal/store/` — `q.WithTx(tx)` pattern already used elsewhere
