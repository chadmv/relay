---
title: "`relay passwd` has no admin password-reset flow"
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-18 password-auth retro — Known Limitations
---

# `relay passwd` has no admin password-reset flow

## Summary
The CLI `relay passwd` requires the current password; there is no admin password-reset flow.

## Resolution
Implemented `POST /v1/users/password-reset` (admin-only endpoint) and `relay admin passwd <email>` CLI command. The admin provides a new password interactively (prompted twice); the server bcrypt-hashes it, updates the target user, and revokes all of that user's existing tokens. Implemented in commits d15d232 and 394ada3 (API) and 805fa06 (CLI), wired in 115c22f.
