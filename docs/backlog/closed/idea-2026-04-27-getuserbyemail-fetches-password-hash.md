---
title: GetUserByEmail fetches password_hash unnecessarily on email filter path
type: idea
status: closed
closed: 2026-04-29
created: 2026-04-27
source: noticed during 2026-04-27 admin user list endpoint retro
---

# GetUserByEmail fetches password_hash unnecessarily on email filter path

## Summary
On the `GET /v1/users?email=` path, `handleListUsers` calls `s.q.GetUserByEmail` which issues `SELECT id, name, email, is_admin, created_at, password_hash FROM users WHERE email = $1`. The hash is discarded by the `userResponse` mapping, but the column is still transferred from Postgres. A dedicated `GetUserByEmailPublic` query selecting only `id, email, name, is_admin, created_at` would tighten the security boundary and match the `ListUsers` query's approach. Low priority at current scale.

## Proposal
Add a new sqlc query `GetUserByEmailPublic :one` selecting only the five public columns. Use it in `handleListUsers` for the email-filter path. Remove the `GetUserByEmail` call from that handler entirely.

## Related
- `internal/api/users.go` — `handleListUsers` email-filter branch
- `internal/store/query/users.sql` — where the new query would live
- `idea-2026-04-26-admin-user-list-endpoint` (closed) — parent feature
