---
title: Admin user list/lookup endpoint (`GET /v1/users`)
type: idea
status: closed
closed: 2026-04-27
resolution: fixed
created: 2026-04-26
source: deferred from 2026-04-26 token-lifecycle brainstorm
---

# Admin user list/lookup endpoint (`GET /v1/users`)

## Summary
There is no admin endpoint for listing or looking up users. Admin tooling that needs to resolve email→UUID, enumerate accounts, or surface user metadata has to either embed the lookup in each operation's request body or work around the gap. A `GET /v1/users` (admin-only) endpoint — optionally with `?email=` filtering — would be a clean primitive for future admin CLI commands.

## Context
Came up while brainstorming the admin password-reset flow. To keep that work scoped, the admin reset endpoint accepts `email` directly in the request body and resolves server-side; no separate user-lookup primitive was added. As more admin operations land, the pattern of email-in-body will get repetitive and a proper lookup endpoint will be the right abstraction.

## Proposal
- `GET /v1/users` (admin-only) — list all users (id, email, name, is_admin, created_at).
- Optional `?email=<exact-match>` query param for direct lookup.
- Pagination can be deferred until the user count makes it necessary.
- CLI: `relay admin users list` and `relay admin users get <email>`.

## Resolution
Implemented as `GET /v1/users` (admin-only) with optional `?email=` exact-match filter, plus `relay admin users list` / `relay admin users get <email>` CLI subcommands. Spec: [`docs/superpowers/specs/2026-04-27-admin-user-list-endpoint-design.md`](../../superpowers/specs/2026-04-27-admin-user-list-endpoint-design.md).
