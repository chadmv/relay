---
title: User profile update endpoint (name, email)
type: idea
status: open
created: 2026-04-27
source: deferred from 2026-04-27 admin user list endpoint brainstorm
---

# User profile update endpoint (name, email)

## Summary
The `users.name` column is set once at `POST /v1/auth/register` and is never updated afterward — there is no self-service or admin path to change a user's display name (or email). As more admin tooling lands that surfaces user metadata (e.g. `GET /v1/users`), the inability to correct a typo'd name or rotate an email address will become a visible gap.

## Context
Surfaced while designing the admin `GET /v1/users` endpoint (see [`2026-04-27-admin-user-list-endpoint-design.md`](../superpowers/specs/2026-04-27-admin-user-list-endpoint-design.md)). The list endpoint exposes `name`, but there is no corresponding write path. Treated as orthogonal scope at the time.

## Proposal
- `PATCH /v1/users/me` — self-service update of `name` (and possibly `email`, with the usual revoke-all-tokens semantics if email changes).
- `PATCH /v1/users/{id}` — admin-only override for the same fields.
- New sqlc query `UpdateUserProfile` (or `SetUserName` / `SetUserEmail` if kept narrower).
- CLI: `relay profile set-name "<name>"` and possibly `relay admin users set-name <email> "<name>"`.

Open question: does changing `email` invalidate existing tokens? (Probably yes, mirroring the password-change behavior.) Brainstorm before implementing.
